$ErrorActionPreference = "Stop"

$RootDir = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$FrontendDir = Join-Path $RootDir "frontend"
$WebAssetsDir = Join-Path $RootDir "internal\webassets"
$OutputDir = if ($env:OUTPUT_DIR) { $env:OUTPUT_DIR } else { Join-Path $RootDir "dist" }
$OutputName = if ($env:OUTPUT_NAME) { $env:OUTPUT_NAME } else { "drone-management" }
$Version = if ($env:VERSION) { $env:VERSION } else { Get-Date -Format "yyyyMMddHHmmss" }
$CgoEnabled = if ($env:CGO_ENABLED) { $env:CGO_ENABLED } else { "0" }
$DefaultTargets = @(
  "linux/arm64",
  "windows/amd64",
  "darwin/arm64"
)

if (-not [System.IO.Path]::IsPathRooted($OutputDir)) {
  $OutputDir = Join-Path $RootDir $OutputDir
}

function Write-Stderr {
  param([string]$Message)
  [Console]::Error.WriteLine($Message)
}

function Show-Usage {
  Write-Stderr @"
Usage:
  powershell -ExecutionPolicy Bypass -File scripts/build-release.ps1 [GOOS/GOARCH ...]

Environment:
  TARGETS      Space-separated targets. Overrides positional targets.
               Default: $($DefaultTargets -join " ")
  OUTPUT_NAME  Binary/package base name. Default: drone-management
  OUTPUT_DIR   Release output directory. Default: ./dist
  VERSION      Package version suffix. Default: current timestamp
  CGO_ENABLED  Go CGO setting for cross compilation. Default: 0

Examples:
  powershell -ExecutionPolicy Bypass -File scripts/build-release.ps1
  powershell -ExecutionPolicy Bypass -File scripts/build-release.ps1 linux/arm64 windows/amd64
  `$env:VERSION="2.2.6"; `$env:TARGETS="linux/arm64"; powershell -ExecutionPolicy Bypass -File scripts/build-release.ps1
"@
}

function Require-Command {
  param([string]$CommandName)

  if (-not (Get-Command $CommandName -ErrorAction SilentlyContinue)) {
    Write-Stderr "Missing required command: $CommandName"
    exit 1
  }
}

function Assert-LastExitCode {
  param([string]$CommandName)

  if ($LASTEXITCODE -ne 0) {
    throw "$CommandName failed with exit code $LASTEXITCODE"
  }
}

function Restore-EnvVar {
  param(
    [string]$Name,
    [AllowNull()][string]$Value
  )

  if ($null -eq $Value) {
    Remove-Item -Path "Env:\$Name" -ErrorAction SilentlyContinue
  } else {
    Set-Item -Path "Env:\$Name" -Value $Value
  }
}

function Invoke-NpmBuild {
  $npmCmd = Get-Command npm.cmd -ErrorAction SilentlyContinue

  Push-Location $FrontendDir
  try {
    if ($npmCmd) {
      & $npmCmd.Source run build
    } else {
      & npm run build
    }
    Assert-LastExitCode "npm run build"
  } finally {
    Pop-Location
  }
}

function Invoke-GoBuild {
  param(
    [string]$Goos,
    [string]$Goarch,
    [string]$OutputPath
  )

  $oldCgo = [Environment]::GetEnvironmentVariable("CGO_ENABLED", "Process")
  $oldGoos = [Environment]::GetEnvironmentVariable("GOOS", "Process")
  $oldGoarch = [Environment]::GetEnvironmentVariable("GOARCH", "Process")

  try {
    $env:CGO_ENABLED = $CgoEnabled
    $env:GOOS = $Goos
    $env:GOARCH = $Goarch

    Push-Location $RootDir
    try {
      & go build -trimpath -ldflags="-s -w" -o $OutputPath ./cmd/api
      Assert-LastExitCode "go build"
    } finally {
      Pop-Location
    }
  } finally {
    Restore-EnvVar "CGO_ENABLED" $oldCgo
    Restore-EnvVar "GOOS" $oldGoos
    Restore-EnvVar "GOARCH" $oldGoarch
  }
}

function Copy-MediaMTX {
  param(
    [string]$Goos,
    [string]$Goarch,
    [string]$PackageDir
  )

  $binaryName = "mediamtx_v1.19.0_${Goos}_${Goarch}"
  if ($Goos -eq "windows") {
    $binaryName = "$binaryName.exe"
  }

  $source = Join-Path $RootDir "MediaMTX\$binaryName"
  if (-not (Test-Path -LiteralPath $source)) {
    Write-Stderr "Missing MediaMTX binary for ${Goos}/${Goarch}: $source"
    exit 1
  }

  $mediaMtxDir = Join-Path $PackageDir "MediaMTX"
  New-Item -ItemType Directory -Force -Path $mediaMtxDir | Out-Null
  Copy-Item -LiteralPath $source -Destination $mediaMtxDir -Force
}

function New-ReleaseArchive {
  param(
    [string]$PackageDir,
    [string]$ArchivePath,
    [string]$Format
  )

  if (Test-Path -LiteralPath $ArchivePath) {
    Remove-Item -LiteralPath $ArchivePath -Force
  }

  $parentDir = Split-Path -Parent $PackageDir
  $packageName = Split-Path -Leaf $PackageDir

  switch ($Format) {
    "zip" {
      Compress-Archive -Path $PackageDir -DestinationPath $ArchivePath -CompressionLevel Optimal
    }
    "tar.gz" {
      & tar --exclude ".DS_Store" --exclude "__MACOSX" --exclude "._*" -C $parentDir -czf $ArchivePath $packageName
      Assert-LastExitCode "tar"
    }
    default {
      Write-Stderr "Unsupported package format: $Format"
      exit 2
    }
  }
}

$TargetArgs = @($args)
if ($TargetArgs.Count -gt 0 -and ($TargetArgs[0] -in @("-h", "--help"))) {
  Show-Usage
  exit 0
}

if ($env:TARGETS) {
  $TargetList = @($env:TARGETS -split "\s+" | Where-Object { $_ })
} elseif ($TargetArgs.Count -gt 0) {
  $TargetList = $TargetArgs
} else {
  $TargetList = $DefaultTargets
}

foreach ($Target in $TargetList) {
  if ($Target -notmatch "^[^/]+/[^/]+$") {
    Write-Stderr "Invalid target: $Target. Expected GOOS/GOARCH."
    Show-Usage
    exit 2
  }
}

Require-Command "go"
Require-Command "npm"
Require-Command "tar"
Require-Command "Compress-Archive"

New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null

Write-Host "==> Building frontend"
Invoke-NpmBuild

Write-Host "==> Syncing embedded frontend assets"
$webAssetsDist = Join-Path $WebAssetsDir "dist"
if (Test-Path -LiteralPath $webAssetsDist) {
  Remove-Item -LiteralPath $webAssetsDist -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $WebAssetsDir | Out-Null
Copy-Item -LiteralPath (Join-Path $FrontendDir "dist") -Destination $WebAssetsDir -Recurse -Force

Write-Host "==> Building release packages"
foreach ($Target in $TargetList) {
  $targetParts = $Target -split "/", 2
  $goos = $targetParts[0]
  $goarch = $targetParts[1]
  $targetName = "${OutputName}_${Version}_${goos}_${goarch}"
  $packageDir = Join-Path $OutputDir $targetName
  $binaryName = $OutputName
  $archiveFormat = "tar.gz"

  if ($goos -eq "windows") {
    $binaryName = "$OutputName.exe"
    $archiveFormat = "zip"
  }

  if (Test-Path -LiteralPath $packageDir) {
    Remove-Item -LiteralPath $packageDir -Recurse -Force
  }
  New-Item -ItemType Directory -Force -Path $packageDir | Out-Null

  Write-Host "  -> $Target"
  Invoke-GoBuild -Goos $goos -Goarch $goarch -OutputPath (Join-Path $packageDir $binaryName)
  Copy-MediaMTX -Goos $goos -Goarch $goarch -PackageDir $packageDir

  $archivePath = Join-Path $OutputDir "$targetName.$archiveFormat"
  New-ReleaseArchive -PackageDir $packageDir -ArchivePath $archivePath -Format $archiveFormat
  Remove-Item -LiteralPath $packageDir -Recurse -Force
  Write-Host "     built $archivePath"
}

Write-Host "==> Done. Packages are in $OutputDir"
