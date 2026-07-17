package httpapi

import (
	"errors"
	"net/http"

	"drone-management/internal/license"
	"drone-management/internal/model"
)

const maxLicenseUploadBytes int64 = 1 << 20

func (s *Server) handleLicenseStatus(w http.ResponseWriter, _ *http.Request) {
	if s.license == nil {
		respondErrorCode(w, http.StatusServiceUnavailable, "license_unavailable", "license service is unavailable", nil)
		return
	}
	status, _ := s.license.Status()
	respondJSON(w, http.StatusOK, status)
}

func (s *Server) handleUploadLicense(w http.ResponseWriter, r *http.Request) {
	if s.license == nil {
		respondErrorCode(w, http.StatusServiceUnavailable, "license_unavailable", "license service is unavailable", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLicenseUploadBytes)
	if err := r.ParseMultipartForm(maxLicenseUploadBytes); err != nil {
		respondErrorCode(w, http.StatusBadRequest, "invalid_request", "invalid license upload", nil)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		respondErrorCode(w, http.StatusBadRequest, "invalid_request", "license file is required", nil)
		return
	}
	defer file.Close()

	status, err := s.license.Activate(file)
	if err != nil {
		s.respondLicenseUploadError(w, err, status)
		return
	}
	s.mapTileLicenseStatus.invalidate()
	respondJSON(w, http.StatusOK, model.LicenseUploadResponse{
		License: status,
		Message: "license uploaded",
	})
}

func (s *Server) respondLicenseUploadError(w http.ResponseWriter, err error, status model.LicenseInfo) {
	code := status.Code
	if code == "" {
		code = license.ErrorCode(err)
	}
	message := status.Message
	if message == "" {
		message = err.Error()
	}
	httpStatus := http.StatusBadRequest
	if errors.Is(err, license.ErrDeviceSNMissing) {
		httpStatus = http.StatusServiceUnavailable
	}
	respondErrorCode(w, httpStatus, code, message, status)
}
