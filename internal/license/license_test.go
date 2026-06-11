package license

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServiceActivateValidLicense(t *testing.T) {
	deviceSN := "SL67CB3FC848FA0E795P"
	path := filepath.Join(t.TempDir(), "license.lic")
	service := NewService(path, func() (string, error) { return deviceSN, nil })

	raw, err := service.Generate(deviceSN, time.Hour, "test", time.Now())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	status, err := service.Activate(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if !status.Valid || status.DeviceSN != deviceSN || !service.IsValid() {
		t.Fatalf("status = %#v, runtime valid = %v", status, service.IsValid())
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Equal(saved, raw) {
		t.Fatalf("saved license does not match uploaded content")
	}
}

func TestServiceRejectsSNMismatchWithoutReplacingExistingLicense(t *testing.T) {
	deviceSN := "SL67CB3FC848FA0E795P"
	path := filepath.Join(t.TempDir(), "license.lic")
	service := NewService(path, func() (string, error) { return deviceSN, nil })

	valid, err := service.Generate(deviceSN, time.Hour, "test", time.Now())
	if err != nil {
		t.Fatalf("Generate(valid) error = %v", err)
	}
	if _, err := service.Activate(bytes.NewReader(valid)); err != nil {
		t.Fatalf("Activate(valid) error = %v", err)
	}
	invalid, err := service.Generate("SLOTHERDEVICE000000000P", time.Hour, "test", time.Now())
	if err != nil {
		t.Fatalf("Generate(invalid) error = %v", err)
	}
	status, err := service.Activate(bytes.NewReader(invalid))
	if !errors.Is(err, ErrSNMismatch) {
		t.Fatalf("Activate(invalid) error = %v, want ErrSNMismatch", err)
	}
	if status.Valid || status.Code != "license_sn_mismatch" {
		t.Fatalf("status = %#v, want sn mismatch", status)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Equal(saved, valid) {
		t.Fatalf("existing valid license was replaced")
	}
	if !service.IsValid() {
		t.Fatalf("runtime state should keep previous valid license")
	}
}

func TestServiceStatusIncludesDeviceSNWhenLicenseMissing(t *testing.T) {
	deviceSN := "SL67CB3FC848FA0E795P"
	service := NewService(filepath.Join(t.TempDir(), "missing.lic"), func() (string, error) {
		return deviceSN, nil
	})

	status, err := service.Status()
	if !errors.Is(err, ErrLicenseNotFound) {
		t.Fatalf("Status() error = %v, want ErrLicenseNotFound", err)
	}
	if status.Valid || status.DeviceSN != deviceSN || status.Code != "license_not_found" {
		t.Fatalf("status = %#v, want missing license status with device SN", status)
	}
}
