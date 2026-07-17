package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	networkmanager "drone-management/internal/network"
)

type networkConfigUpdateRequest struct {
	Content string `json:"content"`
}

type networkDNSUpdateRequest struct {
	Servers []string `json:"servers"`
}

func (s *Server) handleNetworkConfig(w http.ResponseWriter, _ *http.Request) {
	if !s.requireNetworkService(w) {
		return
	}
	config, err := s.network.GetConfig()
	if err != nil {
		respondNetworkError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, config)
}

func (s *Server) handleUpdateNetworkConfig(w http.ResponseWriter, r *http.Request) {
	if !s.requireNetworkService(w) {
		return
	}
	var req networkConfigUpdateRequest
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErrorCode(w, http.StatusBadRequest, "invalid_request", "invalid network config request", nil)
		return
	}
	config, err := s.network.UpdateConfig(r.Context(), req.Content)
	if err != nil {
		respondNetworkError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, config)
}

func (s *Server) handleApplyNetworkConfig(w http.ResponseWriter, r *http.Request) {
	if !s.requireNetworkService(w) {
		return
	}
	if err := s.network.Apply(r.Context()); err != nil {
		respondNetworkError(w, err)
		return
	}
	respondJSON(w, http.StatusAccepted, map[string]string{"message": "network configuration apply scheduled"})
}

func (s *Server) handleRestartNetwork(w http.ResponseWriter, r *http.Request) {
	if !s.requireNetworkService(w) {
		return
	}
	if err := s.network.Restart(r.Context()); err != nil {
		respondNetworkError(w, err)
		return
	}
	respondJSON(w, http.StatusAccepted, map[string]string{"message": "network restart scheduled"})
}

func (s *Server) handleNetworkInterfaces(w http.ResponseWriter, r *http.Request) {
	if !s.requireNetworkService(w) {
		return
	}
	items, err := s.network.Interfaces(r.Context())
	if err != nil {
		respondNetworkError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (s *Server) handleNetworkRoutes(w http.ResponseWriter, r *http.Request) {
	if !s.requireNetworkService(w) {
		return
	}
	items, err := s.network.Routes(r.Context())
	if err != nil {
		respondNetworkError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (s *Server) handleNetworkConnectivity(w http.ResponseWriter, r *http.Request) {
	if !s.requireNetworkService(w) {
		return
	}
	result, err := s.network.TestConnectivity(r.Context())
	if err != nil {
		respondNetworkError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (s *Server) handleNetworkDNS(w http.ResponseWriter, r *http.Request) {
	if !s.requireNetworkService(w) {
		return
	}
	result, err := s.network.DiagnoseDNS(r.Context())
	if err != nil {
		respondNetworkError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (s *Server) handleFixNetworkDNS(w http.ResponseWriter, r *http.Request) {
	if !s.requireNetworkService(w) {
		return
	}
	var req networkDNSUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErrorCode(w, http.StatusBadRequest, "invalid_request", "invalid DNS request", nil)
		return
	}
	if err := s.network.FixDNS(r.Context(), req.Servers); err != nil {
		respondNetworkError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "DNS configuration updated"})
}

func (s *Server) handleNetworkDiagnostics(w http.ResponseWriter, r *http.Request) {
	if !s.requireNetworkService(w) {
		return
	}
	result, err := s.network.Diagnose(r.Context())
	if err != nil {
		respondNetworkError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (s *Server) handleNetworkBackups(w http.ResponseWriter, _ *http.Request) {
	if !s.requireNetworkService(w) {
		return
	}
	items, err := s.network.Backups()
	if err != nil {
		respondNetworkError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (s *Server) handleNetworkBackupContent(w http.ResponseWriter, r *http.Request) {
	if !s.requireNetworkService(w) {
		return
	}
	content, err := s.network.BackupContent(strings.TrimSpace(r.PathValue("name")))
	if err != nil {
		respondNetworkError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"content": content})
}

func (s *Server) handleRestoreNetworkBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireNetworkService(w) {
		return
	}
	if err := s.network.RestoreBackup(r.Context(), strings.TrimSpace(r.PathValue("name"))); err != nil {
		respondNetworkError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "network backup restored"})
}

func (s *Server) handleDeleteNetworkBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireNetworkService(w) {
		return
	}
	if err := s.network.DeleteBackup(strings.TrimSpace(r.PathValue("name"))); err != nil {
		respondNetworkError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "network backup deleted"})
}

func (s *Server) requireNetworkService(w http.ResponseWriter) bool {
	if s.network != nil {
		return true
	}
	respondErrorCode(w, http.StatusServiceUnavailable, "network_unavailable", "network management service is unavailable", nil)
	return false
}

func respondNetworkError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "network_operation_failed"
	switch {
	case errors.Is(err, networkmanager.ErrUnsupported):
		status = http.StatusNotImplemented
		code = "network_unsupported"
	case errors.Is(err, networkmanager.ErrInvalidInput):
		status = http.StatusBadRequest
		code = "invalid_network_config"
	case errors.Is(err, networkmanager.ErrNotFound):
		status = http.StatusNotFound
		code = "network_backup_not_found"
	}
	respondErrorCode(w, status, code, err.Error(), nil)
}
