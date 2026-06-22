#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FRONTEND_DIR="$ROOT_DIR/frontend"
WEBASSETS_DIR="$ROOT_DIR/internal/webassets"
OUTPUT_DIR="${OUTPUT_DIR:-$ROOT_DIR/dist}"
OUTPUT_NAME="${OUTPUT_NAME:-drone-management}"
VERSION="${VERSION:-$(date +%Y%m%d%H%M%S)}"
CGO_ENABLED="${CGO_ENABLED:-0}"
DEFAULT_TARGETS=(
  "linux/arm64"
  "windows/amd64"
  "darwin/arm64"
)

if [[ "$OUTPUT_DIR" != /* ]]; then
  OUTPUT_DIR="$ROOT_DIR/$OUTPUT_DIR"
fi

usage() {
  cat >&2 <<EOF
Usage:
  scripts/build-release.sh [GOOS/GOARCH ...]

Environment:
  TARGETS      Space-separated targets. Overrides positional targets.
               Default: ${DEFAULT_TARGETS[*]}
  OUTPUT_NAME  Binary/package base name. Default: drone-management
  OUTPUT_DIR   Release output directory. Default: ./dist
  VERSION      Package version suffix. Default: current timestamp
  CGO_ENABLED  Go CGO setting for cross compilation. Default: 0

Examples:
  scripts/build-release.sh
  scripts/build-release.sh linux/arm64 windows/amd64
  VERSION=2.2.6 TARGETS="linux/arm64" scripts/build-release.sh
EOF
}

target_list=()
if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi
if [[ -n "${TARGETS:-}" ]]; then
  read -r -a target_list <<< "$TARGETS"
elif [[ "$#" -gt 0 ]]; then
  target_list=("$@")
else
  target_list=("${DEFAULT_TARGETS[@]}")
fi

for target in "${target_list[@]}"; do
  if [[ "$target" != */* ]]; then
    echo "Invalid target: $target. Expected GOOS/GOARCH." >&2
    usage
    exit 2
  fi
done

require_command() {
  local command_name="$1"
  if ! command -v "$command_name" >/dev/null 2>&1; then
    echo "Missing required command: $command_name" >&2
    exit 1
  fi
}

copy_mediamtx() {
  local goos="$1"
  local goarch="$2"
  local package_dir="$3"
  local binary_name="mediamtx_v1.19.0_${goos}_${goarch}"

  if [[ "$goos" == "windows" ]]; then
    binary_name="${binary_name}.exe"
  fi

  local source="$ROOT_DIR/MediaMTX/$binary_name"
  if [[ ! -e "$source" ]]; then
    echo "Missing MediaMTX binary for $goos/$goarch: $source" >&2
    exit 1
  fi

  mkdir -p "$package_dir/MediaMTX"
  cp "$source" "$package_dir/MediaMTX/"
  if [[ "$goos" != "windows" ]]; then
    chmod +x "$package_dir/MediaMTX/$binary_name"
  fi
}

package_target() {
  local package_dir="$1"
  local archive_path="$2"
  local format="$3"

  rm -f "$archive_path"
  case "$format" in
    zip)
      (
        cd "$(dirname "$package_dir")"
        COPYFILE_DISABLE=1 zip -X -qr "$archive_path" "$(basename "$package_dir")" \
          -x "*/.DS_Store" "*/__MACOSX/*" "*/._*"
      )
      ;;
    tar.gz)
      COPYFILE_DISABLE=1 tar \
        --exclude ".DS_Store" \
        --exclude "__MACOSX" \
        --exclude "._*" \
        -C "$(dirname "$package_dir")" \
        -czf "$archive_path" \
        "$(basename "$package_dir")"
      ;;
    *)
      echo "Unsupported package format: $format" >&2
      exit 2
      ;;
  esac
}

require_command go
require_command npm
require_command tar

mkdir -p "$OUTPUT_DIR"

echo "==> Building frontend"
(
  cd "$FRONTEND_DIR"
  npm run build
)

echo "==> Syncing embedded frontend assets"
rm -rf "$WEBASSETS_DIR/dist"
mkdir -p "$WEBASSETS_DIR"
cp -R "$FRONTEND_DIR/dist" "$WEBASSETS_DIR/dist"

echo "==> Building release packages"
for target in "${target_list[@]}"; do
  goos="${target%%/*}"
  goarch="${target##*/}"
  target_name="${OUTPUT_NAME}_${VERSION}_${goos}_${goarch}"
  package_dir="$OUTPUT_DIR/$target_name"
  binary_name="$OUTPUT_NAME"
  archive_format="tar.gz"

  if [[ "$goos" == "windows" ]]; then
    binary_name="$OUTPUT_NAME.exe"
    archive_format="zip"
    require_command zip
  fi

  rm -rf "$package_dir"
  mkdir -p "$package_dir"

  echo "  -> $target"
  (
    cd "$ROOT_DIR"
    env CGO_ENABLED="$CGO_ENABLED" GOOS="$goos" GOARCH="$goarch" \
      go build -trimpath -ldflags="-s -w" -o "$package_dir/$binary_name" ./cmd/api
  )

  copy_mediamtx "$goos" "$goarch" "$package_dir"

  if [[ "$goos" != "windows" ]]; then
    chmod +x "$package_dir/$binary_name"
  fi

  archive_path="$OUTPUT_DIR/$target_name.$archive_format"
  package_target "$package_dir" "$archive_path" "$archive_format"
  rm -rf "$package_dir"
  echo "     built $archive_path"
done

echo "==> Done. Packages are in $OUTPUT_DIR"
