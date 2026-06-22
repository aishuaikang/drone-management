package license

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testDeviceSN = "drone-management-001A2B3C4D5E"

func TestStatusValidLicense(t *testing.T) {
	now := time.Now().Add(-time.Hour)
	path := filepath.Join(t.TempDir(), "license.lic")
	svc := newTestService(t, path, testDeviceSN)
	raw := generateTestLicense(t, svc, testDeviceSN, 24*time.Hour, "customer", now)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	status, err := svc.Status()
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !status.Valid || status.DeviceSN != testDeviceSN || status.Customer != "customer" {
		t.Fatalf("status = %#v, want valid customer license", status)
	}
	if refreshed := svc.Refresh(); !refreshed.Valid || !svc.IsValid() {
		t.Fatalf("Refresh() = %#v, IsValid = %v", refreshed, svc.IsValid())
	}
}

func TestStatusMissingLicenseReturnsDeviceSN(t *testing.T) {
	svc := newTestService(t, filepath.Join(t.TempDir(), "missing.lic"), testDeviceSN)

	status, err := svc.Status()
	if !errors.Is(err, ErrLicenseNotFound) {
		t.Fatalf("Status() error = %v, want %v", err, ErrLicenseNotFound)
	}
	if status.Valid || status.DeviceSN != testDeviceSN || status.Code != "license_not_found" {
		t.Fatalf("status = %#v, want missing license with current SN", status)
	}
}

func TestStatusRejectsInvalidLicense(t *testing.T) {
	path := filepath.Join(t.TempDir(), "license.lic")
	if err := os.WriteFile(path, []byte("not-base64"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := newTestService(t, path, testDeviceSN)

	status, err := svc.Status()
	if err == nil {
		t.Fatalf("Status() error = nil, want invalid license")
	}
	if status.Valid || status.Code != "license_invalid" || status.DeviceSN != testDeviceSN {
		t.Fatalf("status = %#v, want invalid status with current SN", status)
	}
}

func TestStatusRejectsInvalidSignature(t *testing.T) {
	path := filepath.Join(t.TempDir(), "license.lic")
	svc := newTestService(t, path, testDeviceSN)
	raw := encodedTestLicense(t, svc, Info{
		DeviceSN:    testDeviceSN,
		IssuedAt:    time.Now().Add(-time.Hour),
		ExpiresAt:   time.Now().Add(time.Hour),
		Customer:    "customer",
		IsPermanent: false,
		Signature:   "tampered",
	})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	status, err := svc.Status()
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("Status() error = %v, want %v", err, ErrInvalidSignature)
	}
	if status.Valid || status.Code != "license_invalid_signature" || status.DeviceSN != testDeviceSN {
		t.Fatalf("status = %#v, want invalid signature status", status)
	}
}

func TestStatusRejectsSNMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "license.lic")
	svc := newTestService(t, path, testDeviceSN)
	raw := generateTestLicense(t, svc, "drone-management-OTHER", 24*time.Hour, "customer", time.Now())
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	status, err := svc.Status()
	if !errors.Is(err, ErrSNMismatch) {
		t.Fatalf("Status() error = %v, want %v", err, ErrSNMismatch)
	}
	if status.Valid || status.Code != "license_sn_mismatch" || status.DeviceSN != testDeviceSN {
		t.Fatalf("status = %#v, want SN mismatch status with current SN", status)
	}
}

func TestStatusRejectsExpiredLicense(t *testing.T) {
	path := filepath.Join(t.TempDir(), "license.lic")
	svc := newTestService(t, path, testDeviceSN)
	info := Info{
		DeviceSN:    testDeviceSN,
		IssuedAt:    time.Now().Add(-48 * time.Hour),
		ExpiresAt:   time.Now().Add(-24 * time.Hour),
		Customer:    "customer",
		IsPermanent: false,
	}
	signature, err := svc.signature(&info)
	if err != nil {
		t.Fatalf("signature() error = %v", err)
	}
	info.Signature = signature
	if err := os.WriteFile(path, encodedTestLicense(t, svc, info), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	status, err := svc.Status()
	if !errors.Is(err, ErrLicenseExpired) {
		t.Fatalf("Status() error = %v, want %v", err, ErrLicenseExpired)
	}
	if status.Valid || status.Code != "license_expired" {
		t.Fatalf("status = %#v, want expired status", status)
	}
}

func TestGeneratePermanentLicense(t *testing.T) {
	path := filepath.Join(t.TempDir(), "license.lic")
	svc := newTestService(t, path, testDeviceSN)
	raw := generateTestLicense(t, svc, testDeviceSN, 0, "customer", time.Now())
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	status, err := svc.Status()
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !status.Valid || !status.IsPermanent || status.RemainingDays != -1 {
		t.Fatalf("status = %#v, want permanent valid status", status)
	}
}

func TestStatusAcceptsPlainBase64License(t *testing.T) {
	path := filepath.Join(t.TempDir(), "license.lic")
	svc := newTestService(t, path, testDeviceSN)
	info := Info{
		DeviceSN:    testDeviceSN,
		IssuedAt:    time.Now().Add(-time.Hour),
		ExpiresAt:   time.Now().Add(time.Hour),
		Customer:    "customer",
		IsPermanent: false,
	}
	signature, err := svc.signature(&info)
	if err != nil {
		t.Fatalf("signature() error = %v", err)
	}
	info.Signature = signature
	raw, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(base64Encode(raw)), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	status, err := svc.Status()
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !status.Valid || status.DeviceSN != testDeviceSN {
		t.Fatalf("status = %#v, want valid plain base64 license", status)
	}
}

func TestStatusRejectsMissingDeviceSN(t *testing.T) {
	svc := newTestService(t, filepath.Join(t.TempDir(), "license.lic"), "")

	status, err := svc.Status()
	if !errors.Is(err, ErrDeviceSNMissing) {
		t.Fatalf("Status() error = %v, want %v", err, ErrDeviceSNMissing)
	}
	if status.Valid || status.DeviceSN != "" || status.Code != "device_sn_missing" {
		t.Fatalf("status = %#v, want missing device SN", status)
	}
}

func TestActivateWritesLicenseFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "license.lic")
	svc := newTestService(t, path, testDeviceSN)
	raw := generateTestLicense(t, svc, testDeviceSN, 24*time.Hour, "customer", time.Now())

	status, err := svc.Activate(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if !status.Valid || !svc.IsValid() {
		t.Fatalf("Activate() = %#v, IsValid = %v", status, svc.IsValid())
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(stored) != string(raw) {
		t.Fatalf("stored license differs from upload")
	}
}

func TestActivateRejectsNilReader(t *testing.T) {
	svc := newTestService(t, filepath.Join(t.TempDir(), "license.lic"), testDeviceSN)

	status, err := svc.Activate(nil)
	if !errors.Is(err, ErrInvalidLicense) {
		t.Fatalf("Activate() error = %v, want %v", err, ErrInvalidLicense)
	}
	if status.Valid || status.Code != "license_invalid" {
		t.Fatalf("status = %#v, want invalid license", status)
	}
}

func newTestService(t *testing.T, path string, deviceSN string) *Service {
	t.Helper()
	service, err := NewServiceWithSecret(path, func() (string, error) { return deviceSN, nil }, defaultSecretKey)
	if err != nil {
		t.Fatalf("NewServiceWithSecret() error = %v", err)
	}
	return service
}

func generateTestLicense(t *testing.T, svc *Service, deviceSN string, duration time.Duration, customer string, now time.Time) []byte {
	t.Helper()
	raw, err := svc.Generate(deviceSN, duration, customer, now)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	return raw
}

func encodedTestLicense(t *testing.T, svc *Service, info Info) []byte {
	t.Helper()
	raw, err := svc.encode(&info)
	if err != nil {
		t.Fatalf("encode() error = %v", err)
	}
	return raw
}

func base64Encode(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}
