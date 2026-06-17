// Package license verifies and activates local license files.
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"drone-management/internal/model"
)

var (
	ErrInvalidLicense   = errors.New("invalid license")
	ErrLicenseExpired   = errors.New("license has expired")
	ErrSNMismatch       = errors.New("license SN does not match current device")
	ErrLicenseNotFound  = errors.New("license file not found")
	ErrInvalidSignature = errors.New("invalid license signature")
	ErrDeviceSNMissing  = errors.New("device SN is missing")
)

const defaultPublicKeyBase64 = "+/+tirfJ7mRgU4uJGhtiDyUS2EP+j4diyuCYhPrFu/s="

var defaultPublicKey = mustDecodePublicKey(defaultPublicKeyBase64)

// DeviceSNProvider returns the current standardized device SN.
type DeviceSNProvider func() (string, error)

// Service manages the runtime license state.
type Service struct {
	mu        sync.RWMutex
	filePath  string
	publicKey ed25519.PublicKey
	deviceSN  DeviceSNProvider
	valid     bool
}

// Info stores decoded license file content.
type Info struct {
	DeviceSN    string    `json:"device_sn"`
	IssuedAt    time.Time `json:"issued_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	IsPermanent bool      `json:"is_permanent"`
	Customer    string    `json:"customer"`
	Signature   string    `json:"signature"`
}

// NewService creates a license service for the given file.
func NewService(filePath string, provider DeviceSNProvider) *Service {
	service, err := NewServiceWithPublicKey(filePath, provider, defaultPublicKey)
	if err != nil {
		panic(err)
	}
	return service
}

// NewServiceWithPublicKey creates a license service with an explicit verifier key.
func NewServiceWithPublicKey(filePath string, provider DeviceSNProvider, publicKey ed25519.PublicKey) (*Service, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid license public key size: %d", len(publicKey))
	}
	return &Service{
		filePath:  filePath,
		publicKey: append(ed25519.PublicKey(nil), publicKey...),
		deviceSN:  provider,
	}, nil
}

// Refresh verifies the configured license and stores the runtime validity.
func (s *Service) Refresh() model.LicenseInfo {
	status, err := s.Status()
	s.setValid(err == nil && status.Valid)
	return status
}

// IsValid returns the last stored runtime license state.
func (s *Service) IsValid() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.valid
}

// Status verifies and returns the current license status.
func (s *Service) Status() (model.LicenseInfo, error) {
	if s == nil {
		return licenseStatus(nil, ErrLicenseNotFound, ""), ErrLicenseNotFound
	}
	deviceSN, err := s.currentDeviceSN()
	if err != nil {
		return licenseStatus(nil, err, ""), err
	}
	info, err := s.load(s.filePath)
	if err != nil {
		return licenseStatus(nil, err, deviceSN), err
	}
	err = s.verifyInfo(info, deviceSN, time.Now())
	return licenseStatus(info, err, deviceSN), err
}

// Activate validates uploaded license content and atomically replaces the active file.
func (s *Service) Activate(src io.Reader) (model.LicenseInfo, error) {
	if s == nil {
		return licenseStatus(nil, ErrLicenseNotFound, ""), ErrLicenseNotFound
	}
	if src == nil {
		return licenseStatus(nil, ErrInvalidLicense, ""), ErrInvalidLicense
	}
	deviceSN, err := s.currentDeviceSN()
	if err != nil {
		return licenseStatus(nil, err, ""), err
	}
	data, err := io.ReadAll(src)
	if err != nil {
		return licenseStatus(nil, err, deviceSN), err
	}
	info, err := s.decode(data)
	if err != nil {
		return licenseStatus(nil, ErrInvalidLicense, deviceSN), err
	}
	if err := s.verifyInfo(info, deviceSN, time.Now()); err != nil {
		return licenseStatus(info, err, deviceSN), err
	}
	if err := s.replaceFile(data); err != nil {
		return licenseStatus(info, err, deviceSN), err
	}
	s.setValid(true)
	return licenseStatus(info, nil, deviceSN), nil
}

func (s *Service) currentDeviceSN() (string, error) {
	if s.deviceSN == nil {
		return "", ErrDeviceSNMissing
	}
	deviceSN, err := s.deviceSN()
	if err != nil {
		return "", err
	}
	deviceSN = stringsTrim(deviceSN)
	if deviceSN == "" {
		return "", ErrDeviceSNMissing
	}
	return deviceSN, nil
}

func (s *Service) setValid(valid bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.valid = valid
	s.mu.Unlock()
}

func (s *Service) replaceFile(data []byte) error {
	path := stringsTrim(s.filePath)
	if path == "" {
		return ErrLicenseNotFound
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".license-*.uploading")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		_ = temp.Close()
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	removeTemp = false
	return syncDir(dir)
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *Service) load(path string) (*Info, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrLicenseNotFound
		}
		return nil, err
	}
	return s.decode(data)
}

func (s *Service) decode(data []byte) (*Info, error) {
	decoded, err := base64.StdEncoding.DecodeString(stringsTrim(string(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidLicense, err)
	}
	return parseInfo(decoded)
}

func parseInfo(data []byte) (*Info, error) {
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidLicense, err)
	}
	return &info, nil
}

func (s *Service) encode(info *Info) ([]byte, error) {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return nil, err
	}
	return []byte(base64.StdEncoding.EncodeToString(data)), nil
}

func (s *Service) verifyInfo(info *Info, deviceSN string, now time.Time) error {
	if info == nil || stringsTrim(info.DeviceSN) == "" {
		return ErrInvalidLicense
	}
	signature, err := base64.StdEncoding.DecodeString(stringsTrim(info.Signature))
	if err != nil || !ed25519.Verify(s.publicKey, signaturePayload(info), signature) {
		return ErrInvalidSignature
	}
	if stringsTrim(info.DeviceSN) != deviceSN {
		return ErrSNMismatch
	}
	if !info.IsPermanent && now.After(info.ExpiresAt) {
		return ErrLicenseExpired
	}
	return nil
}

func signaturePayload(info *Info) []byte {
	if info == nil {
		return nil
	}
	return []byte(fmt.Sprintf("%s|%d|%d|%t|%s",
		stringsTrim(info.DeviceSN),
		info.IssuedAt.Unix(),
		info.ExpiresAt.Unix(),
		info.IsPermanent,
		info.Customer,
	))
}

func mustDecodePublicKey(encoded string) ed25519.PublicKey {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		panic(fmt.Sprintf("decode license public key: %v", err))
	}
	if len(key) != ed25519.PublicKeySize {
		panic(fmt.Sprintf("invalid license public key size: %d", len(key)))
	}
	return ed25519.PublicKey(key)
}

func signInfo(info *Info, privateKey ed25519.PrivateKey) (string, error) {
	if info == nil {
		return "", ErrInvalidLicense
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return "", ErrInvalidSignature
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, signaturePayload(info))), nil
}

func licenseStatus(info *Info, err error, currentDeviceSN string) model.LicenseInfo {
	status := model.LicenseInfo{
		DeviceSN: stringsTrim(currentDeviceSN),
		Valid:    err == nil,
	}
	if info != nil {
		issuedAt := info.IssuedAt
		expiresAt := info.ExpiresAt
		if status.DeviceSN == "" {
			status.DeviceSN = info.DeviceSN
		}
		status.Customer = info.Customer
		status.IssuedAt = &issuedAt
		status.ExpiresAt = &expiresAt
		status.IsPermanent = info.IsPermanent
		status.RemainingDays = remainingDays(info, time.Now())
	}
	if err != nil {
		status.Code = ErrorCode(err)
		status.Message = errorMessage(err)
	}
	return status
}

func remainingDays(info *Info, now time.Time) int {
	if info == nil || info.IsPermanent {
		return -1
	}
	return int(info.ExpiresAt.Sub(now).Hours() / 24)
}

// ErrorCode returns a stable API error code for license errors.
func ErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrDeviceSNMissing):
		return "device_sn_missing"
	case errors.Is(err, ErrLicenseNotFound):
		return "license_not_found"
	case errors.Is(err, ErrLicenseExpired):
		return "license_expired"
	case errors.Is(err, ErrSNMismatch):
		return "license_sn_mismatch"
	case errors.Is(err, ErrInvalidSignature):
		return "license_invalid_signature"
	case errors.Is(err, ErrInvalidLicense):
		return "license_invalid"
	default:
		return "license_verification_failed"
	}
}

func errorMessage(err error) string {
	switch {
	case errors.Is(err, ErrDeviceSNMissing):
		return "device SN is missing"
	case errors.Is(err, ErrLicenseNotFound):
		return "license file not found"
	case errors.Is(err, ErrLicenseExpired):
		return "license has expired"
	case errors.Is(err, ErrSNMismatch):
		return "license SN does not match current device"
	case errors.Is(err, ErrInvalidSignature):
		return "invalid license signature"
	case errors.Is(err, ErrInvalidLicense):
		return "invalid license"
	default:
		if err == nil {
			return ""
		}
		return err.Error()
	}
}

func stringsTrim(value string) string {
	return strings.TrimSpace(value)
}
