package settings

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"drone-management/internal/model"
)

func TestManualDeviceLocationPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manual-device-location.json")
	updatedAt := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	point := model.GeoPoint{Latitude: 39.909181, Longitude: 116.397472}

	if err := SaveManualDeviceLocation(path, point, updatedAt); err != nil {
		t.Fatalf("save manual location: %v", err)
	}
	got, gotUpdatedAt, ok, err := LoadManualDeviceLocation(path)
	if err != nil {
		t.Fatalf("load manual location: %v", err)
	}
	if !ok || got != point || gotUpdatedAt == nil || !gotUpdatedAt.Equal(updatedAt) {
		t.Fatalf("loaded location = %+v, %v, %v; want %+v, %v, true", got, gotUpdatedAt, ok, point, updatedAt)
	}

	if err := ClearManualDeviceLocation(path); err != nil {
		t.Fatalf("clear manual location: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat after clear = %v, want not exist", err)
	}
}

func TestLoadManualDeviceLocationMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	_, _, ok, err := LoadManualDeviceLocation(path)
	if err != nil {
		t.Fatalf("load missing manual location: %v", err)
	}
	if ok {
		t.Fatal("load missing manual location ok = true, want false")
	}
}
