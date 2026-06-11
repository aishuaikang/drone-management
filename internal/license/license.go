// Package license verifies and activates local license files.
package license

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
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

	"dr600ab-net/internal/model"
)

var (
	ErrInvalidLicense   = errors.New("invalid license")
	ErrLicenseExpired   = errors.New("license has expired")
	ErrSNMismatch       = errors.New("license SN does not match current device")
	ErrLicenseNotFound  = errors.New("license file not found")
	ErrInvalidSignature = errors.New("invalid license signature")
	ErrDeviceSNMissing  = errors.New("device SN is missing")
)

const defaultSecretKey = "zkzp_uav_defender_license_secret_key_2024"

// DeviceSNProvider returns the current standardized device SN.
type DeviceSNProvider func() (string, error)

// Service manages the runtime license state.
type Service struct {
	mu       sync.RWMutex
	filePath string
	secret   string
	deviceSN DeviceSNProvider
	valid    bool
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
	return &Service{
		filePath: filePath,
		secret:   defaultSecretKey,
		deviceSN: provider,
	}
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
		return licenseStatus(nil, ErrLicenseNotFound), ErrLicenseNotFound
	}
	deviceSN, err := s.currentDeviceSN()
	if err != nil {
		return licenseStatus(nil, err), err
	}
	info, err := s.load(s.filePath)
	if err != nil {
		status := licenseStatus(nil, err)
		status.DeviceSN = deviceSN
		return status, err
	}
	err = s.verifyInfo(info, deviceSN, time.Now())
	return licenseStatus(info, err), err
}

// Activate validates uploaded license content and atomically replaces the active file.
func (s *Service) Activate(src io.Reader) (model.LicenseInfo, error) {
	if s == nil {
		return licenseStatus(nil, ErrLicenseNotFound), ErrLicenseNotFound
	}
	if src == nil {
		return licenseStatus(nil, ErrInvalidLicense), ErrInvalidLicense
	}
	deviceSN, err := s.currentDeviceSN()
	if err != nil {
		return licenseStatus(nil, err), err
	}

	data, err := io.ReadAll(src)
	if err != nil {
		return licenseStatus(nil, err), err
	}
	info, err := s.decode(data)
	if err != nil {
		return licenseStatus(nil, ErrInvalidLicense), err
	}
	if err := s.verifyInfo(info, deviceSN, time.Now()); err != nil {
		return licenseStatus(info, err), err
	}
	if err := s.replaceFile(data); err != nil {
		return licenseStatus(info, err), err
	}
	s.setValid(true)
	return licenseStatus(info, nil), nil
}

// Generate returns encoded license bytes for tests and local tooling.
func (s *Service) Generate(deviceSN string, duration time.Duration, customer string, now time.Time) ([]byte, error) {
	deviceSN = strings.TrimSpace(deviceSN)
	if deviceSN == "" {
		return nil, ErrDeviceSNMissing
	}
	if now.IsZero() {
		now = time.Now()
	}
	info := &Info{
		DeviceSN:    deviceSN,
		IssuedAt:    now,
		Customer:    customer,
		IsPermanent: duration <= 0,
	}
	if info.IsPermanent {
		info.ExpiresAt = now.AddDate(100, 0, 0)
	} else {
		info.ExpiresAt = now.Add(duration)
	}
	signature, err := s.signature(info)
	if err != nil {
		return nil, err
	}
	info.Signature = signature
	return s.encode(info)
}

func (s *Service) currentDeviceSN() (string, error) {
	if s.deviceSN == nil {
		return "", ErrDeviceSNMissing
	}
	deviceSN, err := s.deviceSN()
	if err != nil {
		return "", err
	}
	deviceSN = strings.TrimSpace(deviceSN)
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
	path := strings.TrimSpace(s.filePath)
	if path == "" {
		return ErrLicenseNotFound
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	if err := temp.Chmod(0o644); err != nil {
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
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidLicense, err)
	}
	decrypted, err := s.decrypt(decoded)
	if err == nil {
		return parseInfo(decrypted)
	}
	if info, parseErr := parseInfo(decoded); parseErr == nil {
		return info, nil
	}
	return nil, fmt.Errorf("%w: %v", ErrInvalidLicense, err)
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
	encrypted, err := s.encrypt(data)
	if err != nil {
		return nil, err
	}
	return []byte(base64.StdEncoding.EncodeToString(encrypted)), nil
}

func (s *Service) verifyInfo(info *Info, deviceSN string, now time.Time) error {
	if info == nil || strings.TrimSpace(info.DeviceSN) == "" {
		return ErrInvalidLicense
	}
	expectedSignature, err := s.signature(info)
	if err != nil {
		return err
	}
	if info.Signature != expectedSignature {
		return ErrInvalidSignature
	}
	if info.DeviceSN != deviceSN {
		return ErrSNMismatch
	}
	if !info.IsPermanent && now.After(info.ExpiresAt) {
		return ErrLicenseExpired
	}
	return nil
}

func (s *Service) signature(info *Info) (string, error) {
	if info == nil {
		return "", ErrInvalidLicense
	}
	data := fmt.Sprintf(
		"%s|%d|%d|%t",
		info.DeviceSN,
		info.IssuedAt.Unix(),
		info.ExpiresAt.Unix(),
		info.IsPermanent,
	)
	h := hmac.New(sha256.New, []byte(s.secret))
	if _, err := h.Write([]byte(data)); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

func (s *Service) encrypt(data []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.key())
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	padding := blockSize - len(data)%blockSize
	data = append(data, bytes.Repeat([]byte{byte(padding)}, padding)...)
	iv := make([]byte, blockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}
	out := make([]byte, blockSize+len(data))
	copy(out[:blockSize], iv)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out[blockSize:], data)
	return out, nil
}

func (s *Service) decrypt(data []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.key())
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	if len(data) < blockSize {
		return nil, errors.New("ciphertext too short")
	}
	iv := data[:blockSize]
	ciphertext := append([]byte(nil), data[blockSize:]...)
	if len(ciphertext)%blockSize != 0 {
		return nil, errors.New("ciphertext is not a multiple of the block size")
	}
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(ciphertext, ciphertext)
	if len(ciphertext) == 0 {
		return nil, errors.New("empty ciphertext")
	}
	padding := int(ciphertext[len(ciphertext)-1])
	if padding == 0 || padding > blockSize || padding > len(ciphertext) {
		return nil, fmt.Errorf("invalid padding size: %d", padding)
	}
	for i := len(ciphertext) - padding; i < len(ciphertext); i++ {
		if int(ciphertext[i]) != padding {
			return nil, ErrInvalidLicense
		}
	}
	return ciphertext[:len(ciphertext)-padding], nil
}

func (s *Service) key() []byte {
	hash := sha256.Sum256([]byte(s.secret))
	return hash[:]
}

func licenseStatus(info *Info, err error) model.LicenseInfo {
	status := model.LicenseInfo{
		Valid: err == nil,
	}
	if info != nil {
		issuedAt := info.IssuedAt
		expiresAt := info.ExpiresAt
		status.DeviceSN = info.DeviceSN
		status.Customer = info.Customer
		status.IssuedAt = &issuedAt
		status.ExpiresAt = &expiresAt
		status.IsPermanent = info.IsPermanent
		status.RemainingDays = remainingDays(info, time.Now())
	}
	if err != nil {
		status.Code = ErrorCode(err)
		status.Message = ErrorMessage(err)
	}
	return status
}

func remainingDays(info *Info, now time.Time) int {
	if info == nil || info.IsPermanent {
		return -1
	}
	return int(info.ExpiresAt.Sub(now).Hours() / 24)
}

// ErrorCode returns the stable API code for a license error.
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

// ErrorMessage returns a user-facing English fallback message for a license error.
func ErrorMessage(err error) string {
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
