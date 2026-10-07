package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLocalAboutInfoSaveAndClear(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	info, err := app.ReadAboutInfo(dir, true)
	if err != nil || info != (AboutInfo{}) {
		t.Fatalf("missing about info = %#v, error = %v", info, err)
	}
	for _, input := range []AboutInfo{{UserCompany: "  使用厂家  ", UserName: "  使用人员  "}, {UserCompany: "另一厂家"}, {}} {
		want, err := normalizeAboutInfo(input)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := app.SaveAboutInfo(AboutInfoRequest{InstallDir: dir, Local: true, Info: input})
		if err != nil || saved != want {
			t.Fatalf("save = %#v, error = %v, want %#v", saved, err, want)
		}
		loaded, err := app.ReadAboutInfo(dir, true)
		if err != nil || loaded != want {
			t.Fatalf("load = %#v, error = %v, want %#v", loaded, err, want)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "data", "about.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "manufacturer") {
		t.Fatalf("manufacturer must not be configurable: %s", data)
	}
}

func TestSaveAboutInfoRejectsInvalidNamesWithoutReplacingFile(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	original := AboutInfo{UserCompany: "原厂家", UserName: "原人员"}
	if _, err := app.SaveAboutInfo(AboutInfoRequest{InstallDir: dir, Local: true, Info: original}); err != nil {
		t.Fatal(err)
	}
	for _, input := range []AboutInfo{{UserCompany: strings.Repeat("厂", 129)}, {UserName: strings.Repeat("人", 65)}} {
		if _, err := app.SaveAboutInfo(AboutInfoRequest{InstallDir: dir, Local: true, Info: input}); err == nil {
			t.Fatal("long names must be rejected")
		}
	}
	loaded, err := app.ReadAboutInfo(dir, true)
	if err != nil || loaded != original {
		t.Fatalf("invalid save changed file: %#v, error = %v", loaded, err)
	}
}

func TestReadAboutInfoRejectsCorruptFile(t *testing.T) {
	if _, err := decodeAboutInfo(strings.NewReader(`{`)); err == nil {
		t.Fatal("corrupt JSON must not become empty software information")
	}
}

func TestRemoteAboutInfoRepeatedSaveWithoutSFTP(t *testing.T) {
	shell := aboutInfoTestShell(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "程序 '目录", "data", "about.json")
	for _, info := range []AboutInfo{
		{UserCompany: "厂商 ' $(touch payload-executed) `touch payload-executed`", UserName: "使用人员\n第二行"},
		{UserCompany: "另一厂家"},
		{},
	} {
		data, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, '\n')
		command := buildRemoteAboutInfoWriteCommand(aboutInfoTestPath(path), data)
		if output, err := runAboutInfoTestCommand(t, shell, dir, command); err != nil {
			t.Fatalf("save failed: %v\n%s", err, output)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(data) {
			t.Fatalf("saved information = %s, error = %v, want %s", got, err, data)
		}
		assertNoAboutInfoTemporaryFiles(t, path)
	}
	if _, err := os.Stat(filepath.Join(dir, "payload-executed")); !os.IsNotExist(err) {
		t.Fatalf("software information was interpreted as a shell command: %v", err)
	}
}

func TestRemoteAboutInfoSudoFallback(t *testing.T) {
	shell := aboutInfoTestShell(t)
	for _, operation := range []string{"mkdir", "mktemp", "mv"} {
		for _, sudoAllowed := range []bool{true, false} {
			name := operation + "/sudo-denied"
			if sudoAllowed {
				name = operation + "/sudo-allowed"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "data", "about.json")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				original := []byte(`{"userCompany":"原厂家","userName":"原人员"}`)
				if err := os.WriteFile(path, original, 0o644); err != nil {
					t.Fatal(err)
				}
				bin := filepath.Join(dir, "bin")
				if err := os.Mkdir(bin, 0o755); err != nil {
					t.Fatal(err)
				}
				program, err := runAboutInfoTestCommand(t, shell, dir, "command -v "+operation)
				if err != nil {
					t.Fatalf("find %s: %v\n%s", operation, err, program)
				}
				// Emulate an SSH account that cannot write the root-owned installation directory.
				writeAboutInfoTestProgram(t, bin, "id", "printf '1000\\n'\n")
				writeAboutInfoTestProgram(t, bin, operation, `if [ "${ABOUT_INFO_TEST_ELEVATED:-}" != 1 ]; then
  echo 'Permission denied' >&2
  exit 1
fi
exec `+shellQuote(strings.TrimSpace(string(program)))+` "$@"
`)
				marker := filepath.Join(dir, "sudo-invoked")
				sudoScript := `[ "$1" = '-n' ] || exit 2
shift
printf 'called' > ` + shellQuote(aboutInfoTestPath(marker)) + "\n"
				if sudoAllowed {
					sudoScript += "export ABOUT_INFO_TEST_ELEVATED=1\nexec \"$@\"\n"
				} else {
					sudoScript += "echo 'sudo: a password is required' >&2\nexit 1\n"
				}
				writeAboutInfoTestProgram(t, bin, "sudo", sudoScript)
				data := []byte(`{"userCompany":"新厂家","userName":"新人员"}`)
				command := "PATH=" + shellQuote(aboutInfoTestPath(bin)) + ":$PATH\n" +
					buildRemoteAboutInfoWriteCommand(aboutInfoTestPath(path), data)
				output, err := runAboutInfoTestCommand(t, shell, dir, command)
				want := data
				if sudoAllowed {
					if err != nil {
						t.Fatalf("sudo save failed: %v\n%s", err, output)
					}
				} else {
					if err == nil || !strings.Contains(string(output), "a password is required") {
						t.Fatalf("denied sudo must report an error: %v\n%s", err, output)
					}
					want = original
				}
				got, err := os.ReadFile(path)
				if err != nil || string(got) != string(want) {
					t.Fatalf("saved information = %s, error = %v, want %s", got, err, want)
				}
				if _, err := os.Stat(marker); err != nil {
					t.Fatalf("sudo fallback was not invoked: %v", err)
				}
				assertNoAboutInfoTemporaryFiles(t, path)
			})
		}
	}
}

func TestRemoteAboutInfoDoesNotReplaceDirectory(t *testing.T) {
	shell := aboutInfoTestShell(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "data", "about.json")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	command := buildRemoteAboutInfoWriteCommand(aboutInfoTestPath(path), []byte(`{}`))
	if output, err := runAboutInfoTestCommand(t, shell, dir, command); err == nil {
		t.Fatalf("saving to a directory must fail: %s", output)
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed save wrote inside the target directory: %v, %v", entries, err)
	}
	assertNoAboutInfoTemporaryFiles(t, path)
}

func aboutInfoTestShell(t *testing.T) string {
	t.Helper()
	if shell, err := exec.LookPath("sh"); err == nil {
		return shell
	}
	if runtime.GOOS == "windows" {
		if git, err := exec.LookPath("git"); err == nil {
			shell := filepath.Join(filepath.Dir(filepath.Dir(git)), "bin", "sh.exe")
			if _, err := os.Stat(shell); err == nil {
				return shell
			}
		}
	}
	t.Skip("remote write tests require a POSIX shell (or Git for Windows)")
	return ""
}

func aboutInfoTestPath(path string) string {
	path = filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		return "/" + strings.ToLower(path[:1]) + path[2:]
	}
	return path
}

func runAboutInfoTestCommand(t *testing.T, shell, dir, command string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-c", command)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

func writeAboutInfoTestProgram(t *testing.T, bin, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertNoAboutInfoTemporaryFiles(t *testing.T, path string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".about-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary software information files remain: %v, error = %v", files, err)
	}
}
