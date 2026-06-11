package offlinemap

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServiceInstallNormalizesLayout(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "map.zip")
	writeTestZip(t, source, map[string]string{
		"root/dt/12/345/678.jpeg": "tile",
	})

	service := NewService(root)
	status, err := service.Install(source, UploadOptions{
		SourceFile: "map.zip",
		Now:        time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if !status.Available || status.TileCount != 1 || status.SourceFile != "map.zip" {
		t.Fatalf("status = %#v", status)
	}
	data, err := os.ReadFile(filepath.Join(root, "dt", "12", "345", "678.jpg"))
	if err != nil {
		t.Fatalf("ReadFile(tile) error = %v", err)
	}
	if string(data) != "tile" {
		t.Fatalf("tile = %q, want tile", data)
	}
}

func TestServiceInstallRejectsTraversal(t *testing.T) {
	source := filepath.Join(t.TempDir(), "bad.zip")
	writeTestZip(t, source, map[string]string{
		"../dt/1/2/3.jpg": "bad",
	})

	_, err := NewService(t.TempDir()).Install(source, UploadOptions{})
	if err == nil || !strings.Contains(err.Error(), "非法路径") {
		t.Fatalf("Install() error = %v, want traversal error", err)
	}
}

func TestServiceInstallRejectsNonJPGTile(t *testing.T) {
	source := filepath.Join(t.TempDir(), "bad.zip")
	writeTestZip(t, source, map[string]string{
		"dt/1/2/3.png": "bad",
	})

	_, err := NewService(t.TempDir()).Install(source, UploadOptions{})
	if err == nil || !strings.Contains(err.Error(), "JPG") {
		t.Fatalf("Install() error = %v, want jpg-only error", err)
	}
}

func writeTestZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(out)
	for name, content := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
