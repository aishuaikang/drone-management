// Package httpapi exposes the read-only screen API and frontend assets.
package httpapi

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"drone-management/internal/config"
	"drone-management/internal/fpv"
	"drone-management/internal/fpvrecord"
	"drone-management/internal/fpvvideo"
	"drone-management/internal/interference"
	"drone-management/internal/interferencereport"
	"drone-management/internal/intrusion"
	"drone-management/internal/license"
	"drone-management/internal/model"
	"drone-management/internal/offlinemap"
	"drone-management/internal/position"
	"drone-management/internal/settings"
	"drone-management/internal/store"
	"drone-management/internal/webassets"
)

const (
	sseHeartbeatInterval     = 15 * time.Second
	fpvVideoStopTimeout      = 10 * time.Second
	fpvVideoSessionReadyType = "screen.fpv_video.ready"
	fpvVideoSessionErrorType = "screen.fpv_video.error"
	fpvVideoSessionBusyCode  = "busy"
)

var errFPVVideoSessionNotActive = errors.New("fpv video session is not active")

// UserSettingsStore persists public user settings.
type UserSettingsStore interface {
	LoadUser() (model.UserSettings, bool, error)
	SaveEditableUser(model.UserSettings) (model.UserSettings, error)
}

type userSettingsUpdateRequest struct {
	IntrusionRetentionDays    *int                   `json:"intrusionRetentionDays,omitempty"`
	ScreenTitle               *string                `json:"screenTitle,omitempty"`
	PositionExpireSeconds     *int                   `json:"positionExpireSeconds,omitempty"`
	ScreenStrikeChannelLabels *[]string              `json:"screenStrikeChannelLabels,omitempty"`
	Lingyun                   *model.LingyunSettings `json:"lingyun,omitempty"`
	WarningZoneEnabled        *bool                  `json:"warningZoneEnabled,omitempty"`
	WarningZoneRadiusMeters   *float64               `json:"warningZoneRadiusMeters,omitempty"`
	WarningZones              *[]model.WarningZone   `json:"warningZones,omitempty"`
	Whitelist                 *[]model.WhitelistItem `json:"whitelist,omitempty"`
}

// LingyunService controls the optional Lingyun protocol connector.
type LingyunService interface {
	ApplySettings(model.UserSettings)
	Status() model.LingyunStatus
}

// IntrusionStore persists disappeared positioning targets.
type IntrusionStore interface {
	List(context.Context, intrusion.QueryOptions) ([]model.IntrusionRecord, error)
	Delete(context.Context, []string) (int64, error)
	PruneRetention(context.Context, int, time.Time) (int64, error)
}

// FPVVideoRecordStore persists FPV video recording records.
type FPVVideoRecordStore interface {
	Insert(context.Context, model.FPVVideoRecord) error
	List(context.Context, fpvrecord.QueryOptions) ([]model.FPVVideoRecord, error)
	Get(context.Context, string) (model.FPVVideoRecord, bool, error)
	Delete(context.Context, []string, string) (fpvrecord.DeleteResult, error)
}

// InterferenceReportStore persists interference operation reports.
type InterferenceReportStore interface {
	List(context.Context, interferencereport.QueryOptions) ([]model.InterferenceReportSummary, error)
	Get(context.Context, string) (model.InterferenceReport, bool, error)
	DeleteFailed(context.Context, string) (int64, error)
}

// Server exposes HTTP APIs and static frontend files.
type Server struct {
	cfg                 config.Config
	store               *store.Store
	position            *position.Service
	fpv                 *fpv.Service
	interference        *interference.Service
	fpvVideo            *fpvvideo.Service
	lingyun             LingyunService
	server              *http.Server
	userSettings        UserSettingsStore
	intrusions          IntrusionStore
	fpvRecords          FPVVideoRecordStore
	interferenceReports InterferenceReportStore
	offlineMap          *offlinemap.Service
	license             *license.Service

	fpvVideoStopMu               sync.Mutex
	fpvVideoMu                   sync.Mutex
	nextFPVVideoSessionID        uint64
	activeFPVVideoSession        uint64
	activeFPVVideoFreq           int
	activeFPVVideoSince          time.Time
	activeFPVVideoToken          string
	activeFPVVideoTarget         model.ScreenFPVTarget
	activeFPVVideoRecordID       string
	activeFPVVideoRecordBasePath string
	activeFPVVideoRecordFile     string
	activeFPVVideoRecordGlob     string
	intrusionPruneMu             sync.Mutex
	lastIntrusionPruneRun        time.Time
}

type fpvVideoSessionSnapshot struct {
	ID             uint64
	Token          string
	Frequency      int
	StartedAt      time.Time
	Target         model.ScreenFPVTarget
	RecordID       string
	RecordBasePath string
	RecordFile     string
	RecordFileGlob string
}

type fpvVideoRecordingSpec struct {
	ID       string
	BasePath string
	FileName string
	FileGlob string
}

// Option configures optional server dependencies.
type Option func(*Server)

// WithUserSettingsStore injects the public user settings store.
func WithUserSettingsStore(store UserSettingsStore) Option {
	return func(s *Server) {
		s.userSettings = store
	}
}

// WithLingyunService injects the optional Lingyun protocol service.
func WithLingyunService(service LingyunService) Option {
	return func(s *Server) {
		s.lingyun = service
	}
}

// WithIntrusionStore injects the intrusion record store.
func WithIntrusionStore(store IntrusionStore) Option {
	return func(s *Server) {
		s.intrusions = store
	}
}

// WithFPVVideoRecordStore injects the FPV video record store.
func WithFPVVideoRecordStore(store FPVVideoRecordStore) Option {
	return func(s *Server) {
		s.fpvRecords = store
	}
}

// WithInterferenceReportStore injects the interference report store.
func WithInterferenceReportStore(store InterferenceReportStore) Option {
	return func(s *Server) {
		s.interferenceReports = store
	}
}

// WithInterferenceService injects the interference service.
func WithInterferenceService(service *interference.Service) Option {
	return func(s *Server) {
		s.interference = service
	}
}

// WithOfflineMapService injects the offline map service.
func WithOfflineMapService(service *offlinemap.Service) Option {
	return func(s *Server) {
		s.offlineMap = service
	}
}

// WithLicenseService injects the local license service.
func WithLicenseService(service *license.Service) Option {
	return func(s *Server) {
		s.license = service
	}
}

// New creates a configured HTTP server.
func New(
	cfg config.Config,
	store *store.Store,
	positionSvc *position.Service,
	fpvSvc *fpv.Service,
	options ...Option,
) *Server {
	s := &Server{
		cfg:      cfg,
		store:    store,
		position: positionSvc,
		fpv:      fpvSvc,
		fpvVideo: fpvvideo.New(fpvvideo.Options{
			RTSPURL:          cfg.FPVVideo.RTSPURL,
			MediaMTXPath:     cfg.FPVVideo.MediaMTXPath,
			MediaMTXWorkDir:  cfg.FPVVideo.MediaMTXWorkDir,
			MediaMTXBin:      cfg.FPVVideo.MediaMTXBin,
			WebRTCListenHost: cfg.FPVVideo.WebRTCListenHost,
			WebRTCListenPort: cfg.FPVVideo.WebRTCListenPort,
			WebRTCUDPPort:    cfg.FPVVideo.WebRTCUDPPort,
			WHEPURL:          cfg.FPVVideo.WHEPURL,
			RecordPath:       "",
		}),
	}
	for _, option := range options {
		option(s)
	}
	mux := http.NewServeMux()
	s.routes(mux)
	s.server = &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// ListenAndServe starts serving HTTP requests.
func (s *Server) ListenAndServe() error {
	err := s.server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	var shutdownErr error
	if err := s.stopActiveFPVVideoSession(ctx); err != nil {
		shutdownErr = fmt.Errorf("stop active FPV video session: %w", err)
	}
	if err := s.closeFPVVideoConverter(); err != nil {
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("close FPV video converter: %w", err))
	}

	err := s.server.Shutdown(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		if closeErr := s.server.Close(); closeErr != nil {
			err = fmt.Errorf("HTTP graceful shutdown timed out: %w; close: %v", err, closeErr)
		} else {
			err = nil
		}
	}
	return errors.Join(shutdownErr, err)
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/v1/meta/locales", s.handleLocales)
	mux.HandleFunc("GET /api/v1/license/status", s.handleLicenseStatus)
	mux.HandleFunc("POST /api/v1/license/upload", s.handleUploadLicense)
	mux.HandleFunc("GET /api/v1/offline-map/status", s.requireLicense(s.handleOfflineMapStatus))
	mux.HandleFunc("POST /api/v1/offline-map/upload", s.requireLicense(s.handleUploadOfflineMap))
	mux.HandleFunc("GET /api/v1/screen/status", s.requireLicense(s.handleScreenStatus))
	mux.HandleFunc("GET /api/v1/screen/positions", s.requireLicense(s.handleScreenPositions))
	mux.HandleFunc("GET /api/v1/screen/fpv", s.requireLicense(s.handleScreenFPV))
	mux.HandleFunc("GET /api/v1/screen/strike", s.requireLicense(s.handleScreenStrike))
	mux.HandleFunc("POST /api/v1/screen/strike", s.requireLicense(s.handleSetScreenStrike))
	mux.HandleFunc("POST /api/v1/screen/strike/unattended", s.requireLicense(s.handleSetScreenStrikeUnattended))
	mux.HandleFunc("PUT /api/v1/screen/tcp-ports", s.requireLicense(s.handleUpdateScreenTCPPorts))
	mux.HandleFunc("GET /api/v1/interference/channels", s.requireLicense(s.handleInterferenceChannels))
	mux.HandleFunc("POST /api/v1/interference/channels/{id}/state", s.requireLicense(s.handleSetInterferenceChannelState))
	mux.HandleFunc("GET /api/v1/screen/device-location", s.requireLicense(s.handleScreenDeviceLocation))
	mux.HandleFunc("PUT /api/v1/screen/device-location/manual", s.requireLicense(s.handleSetManualDeviceLocation))
	mux.HandleFunc("DELETE /api/v1/screen/device-location/manual", s.requireLicense(s.handleClearManualDeviceLocation))
	mux.HandleFunc("GET /api/v1/user/settings", s.requireLicense(s.handleUserSettings))
	mux.HandleFunc("PUT /api/v1/user/settings", s.requireLicense(s.handleUpdateUserSettings))
	mux.HandleFunc("GET /api/v1/intrusions", s.requireLicense(s.handleIntrusions))
	mux.HandleFunc("DELETE /api/v1/intrusions", s.requireLicense(s.handleDeleteIntrusions))
	mux.HandleFunc("GET /api/v1/fpv-video-records", s.requireLicense(s.handleFPVVideoRecords))
	mux.HandleFunc("POST /api/v1/fpv-video-records/export", s.requireLicense(s.handleExportFPVVideoRecords))
	mux.HandleFunc("GET /api/v1/fpv-video-records/{id}/file", s.requireLicense(s.handleFPVVideoRecordFile))
	mux.HandleFunc("DELETE /api/v1/fpv-video-records", s.requireLicense(s.handleDeleteFPVVideoRecords))
	mux.HandleFunc("GET /api/v1/interference-reports", s.requireLicense(s.handleInterferenceReports))
	mux.HandleFunc("GET /api/v1/interference-reports/{id}", s.requireLicense(s.handleInterferenceReport))
	mux.HandleFunc("DELETE /api/v1/interference-reports/{id}", s.requireLicense(s.handleDeleteFailedInterferenceReport))
	mux.HandleFunc("/api/v1/screen/fpv-video/whep", s.requireLicense(s.handleScreenFPVVideoWHEP))
	mux.HandleFunc("/api/v1/screen/fpv-video/whep/{resource...}", s.requireLicense(s.handleScreenFPVVideoWHEP))
	mux.HandleFunc("GET /api/v1/screen/fpv-video/session", s.requireLicense(s.handleScreenFPVVideoSession))
	mux.HandleFunc("POST /api/v1/screen/fpv-video/session/close", s.requireLicense(s.handleScreenFPVVideoSessionClose))
	mux.HandleFunc("GET /api/v1/screen/stream", s.requireLicense(s.handleScreenStream))
	mux.HandleFunc("GET /map/", s.requireLicense(s.handleOfflineMapTile))
	mux.HandleFunc("/", s.handleFrontend)
}

func (s *Server) requireLicense(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.license == nil {
			respondErrorCode(w, http.StatusServiceUnavailable, "license_unavailable", "license service is unavailable", nil)
			return
		}
		status, err := s.license.Status()
		if err != nil || !status.Valid {
			respondErrorCode(w, http.StatusForbidden, "license_required", "license required", status)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"time": time.Now(),
	})
}

func (s *Server) handleLocales(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, model.LocaleMeta{
		Default:    s.cfg.DefaultLocale,
		Supported:  []string{"zh-CN", "en-US"},
		Namespaces: []string{"common", "screen"},
	})
}

func (s *Server) handleOfflineMapStatus(w http.ResponseWriter, _ *http.Request) {
	if s.offlineMap == nil {
		respondErrorCode(w, http.StatusServiceUnavailable, "offline_map_unavailable", "offline map service is unavailable", nil)
		return
	}
	respondJSON(w, http.StatusOK, s.offlineMap.Status())
}

func (s *Server) handleUploadOfflineMap(w http.ResponseWriter, r *http.Request) {
	logs := newOfflineMapUploadLogs()
	logs.add("request", "收到离线地图上传请求", "running", "")
	if s.offlineMap == nil {
		logs.add("request", "离线地图服务不可用", "error", "")
		respondErrorCode(w, http.StatusServiceUnavailable, "offline_map_unavailable", "offline map service is unavailable", logs.entries())
		return
	}
	maxBytes := s.cfg.OfflineMapUploadMaxBytes
	if maxBytes <= 0 {
		maxBytes = 2048 * 1024 * 1024
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	logs.add("parse", "解析上传表单", "running", fmt.Sprintf("最大 %.0f MB", float64(maxBytes)/(1024*1024)))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		logs.add("parse", "上传表单解析失败", "error", err.Error())
		respondErrorCode(w, http.StatusBadRequest, "invalid_offline_map_upload", "invalid offline map upload", logs.entries())
		return
	}
	logs.add("parse", "上传表单解析完成", "success", "")
	file, header, err := r.FormFile("file")
	if err != nil {
		logs.add("receive", "读取上传文件失败", "error", err.Error())
		respondErrorCode(w, http.StatusBadRequest, "invalid_request", "invalid request", logs.entries())
		return
	}
	defer file.Close()
	logs.add("receive", "已读取上传文件", "success", header.Filename)
	if !strings.EqualFold(filepath.Ext(header.Filename), ".zip") {
		logs.add("validate", "文件类型校验失败", "error", header.Filename)
		respondErrorCode(w, http.StatusBadRequest, "invalid_offline_map_package", "离线地图只支持 .zip 文件", logs.entries())
		return
	}

	logs.add("store", "保存上传文件到临时目录", "running", header.Filename)
	tempPath, cleanup, err := saveMultipartUpload(file, header.Filename)
	if err != nil {
		logs.add("store", "保存上传文件失败", "error", err.Error())
		respondErrorCode(w, http.StatusInternalServerError, "offline_map_upload_failed", "save offline map upload failed", logs.entries())
		return
	}
	defer cleanup()
	logs.add("store", "上传文件已保存", "success", tempPath)

	status, err := s.offlineMap.Install(tempPath, offlinemap.UploadOptions{
		SourceFile: header.Filename,
		KeepBackup: isTruthy(r.FormValue("keepBackup")),
		Logger:     logs.add,
	})
	if err != nil {
		respondErrorCode(w, http.StatusBadRequest, "invalid_offline_map_package", err.Error(), logs.entries())
		return
	}
	logs.add("done", "离线地图上传完成", "success", header.Filename)
	respondJSON(w, http.StatusOK, model.OfflineMapUploadResponse{
		Map:     status,
		Message: "离线地图上传完成",
		Logs:    logs.entries(),
	})
}

func (s *Server) handleScreenStatus(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, s.screenRuntimeStatus())
}

func (s *Server) handleUpdateScreenTCPPorts(w http.ResponseWriter, r *http.Request) {
	var req model.ScreenTCPPortRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !validTCPPorts(req.PositionTCPPort, req.FPVTCPPort) {
		respondError(w, http.StatusBadRequest, "invalid tcp ports")
		return
	}

	settings := model.UserSettings{}
	if s.userSettings != nil {
		loaded, ok, err := s.userSettings.LoadUser()
		if err != nil {
			respondError(w, http.StatusInternalServerError, "load user settings failed")
			return
		}
		if ok {
			settings = loaded
		}
	}
	settings = model.UserSettingsWithDefaults(settings)
	settings.PositionTCPPort = &req.PositionTCPPort
	settings.FPVTCPPort = &req.FPVTCPPort
	settings = model.UserSettingsWithDefaults(settings)
	if s.userSettings != nil {
		if _, err := s.userSettings.SaveEditableUser(settings); err != nil {
			respondError(w, http.StatusInternalServerError, "save user settings failed")
			return
		}
	}

	s.applyTCPPorts(req.PositionTCPPort, req.FPVTCPPort)
	respondJSON(w, http.StatusOK, s.screenRuntimeStatus())
}

func (s *Server) handleScreenPositions(w http.ResponseWriter, r *http.Request) {
	items := s.store.Positions(parseLimit(r, 100))
	respondJSON(w, http.StatusOK, model.ListResponse[model.ScreenPositionTarget]{
		Items: items,
		Count: len(items),
	})
}

func (s *Server) handleScreenFPV(w http.ResponseWriter, r *http.Request) {
	items := s.store.FPV(parseLimit(r, 100))
	respondJSON(w, http.StatusOK, model.ListResponse[model.ScreenFPVTarget]{
		Items: items,
		Count: len(items),
	})
}

func (s *Server) handleScreenStrike(w http.ResponseWriter, _ *http.Request) {
	if s.interference == nil {
		respondJSON(w, http.StatusOK, model.ScreenStrikeState{Channels: []model.InterferenceChannel{}})
		return
	}
	respondJSON(w, http.StatusOK, s.interference.ScreenStrikeState())
}

func (s *Server) handleSetScreenStrike(w http.ResponseWriter, r *http.Request) {
	var req model.ScreenStrikeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if s.interference == nil {
		respondError(w, http.StatusServiceUnavailable, "interference service is unavailable")
		return
	}
	state, err := s.interference.SetScreenStrike(req)
	if err != nil {
		if code := interference.ErrorCode(err); code != "" {
			respondErrorCode(w, http.StatusBadRequest, code, err.Error(), nil)
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	message := "干扰已停止"
	if req.Enabled {
		message = "干扰已开启"
	}
	respondJSON(w, http.StatusOK, model.ScreenStrikeResponse{
		State:   state,
		Message: message,
	})
}

func (s *Server) handleSetScreenStrikeUnattended(w http.ResponseWriter, r *http.Request) {
	var req model.ScreenStrikeUnattendedConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if s.interference == nil {
		respondError(w, http.StatusServiceUnavailable, "interference service is unavailable")
		return
	}
	state, err := s.interference.SetUnattended(req)
	if err != nil {
		if code := interference.ErrorCode(err); code != "" {
			respondErrorCode(w, http.StatusBadRequest, code, err.Error(), nil)
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	message := "无人值守已关闭"
	if req.Enabled {
		message = "无人值守已开启"
	}
	respondJSON(w, http.StatusOK, model.ScreenStrikeResponse{
		State:   state,
		Message: message,
	})
}

func (s *Server) handleInterferenceChannels(w http.ResponseWriter, _ *http.Request) {
	if s.interference == nil {
		respondJSON(w, http.StatusOK, model.ListResponse[model.InterferenceChannel]{
			Items: []model.InterferenceChannel{},
			Count: 0,
		})
		return
	}
	channels := s.interference.ListChannels()
	respondJSON(w, http.StatusOK, model.ListResponse[model.InterferenceChannel]{
		Items: channels,
		Count: len(channels),
	})
}

func (s *Server) handleSetInterferenceChannelState(w http.ResponseWriter, r *http.Request) {
	if s.interference == nil {
		respondError(w, http.StatusServiceUnavailable, "interference service is unavailable")
		return
	}
	var req model.InterferenceChannelStateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request")
		return
	}
	channel, err := s.interference.SetState(strings.TrimSpace(r.PathValue("id")), req.Enabled)
	if err != nil {
		if code := interference.ErrorCode(err); code != "" {
			respondErrorCode(w, http.StatusBadRequest, code, err.Error(), nil)
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	message := "通道已关闭"
	if req.Enabled {
		message = "通道已开启"
	}
	respondJSON(w, http.StatusOK, model.InterferenceChannelStateResponse{
		Channel: channel,
		Message: message,
	})
}

func (s *Server) handleScreenDeviceLocation(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, s.store.DeviceLocation())
}

func (s *Server) handleSetManualDeviceLocation(w http.ResponseWriter, r *http.Request) {
	var req model.ScreenManualDeviceLocationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !validGeoPoint(&req.Point) {
		respondError(w, http.StatusBadRequest, "invalid location")
		return
	}
	now := time.Now()
	if err := settings.SaveManualDeviceLocation(s.cfg.ManualLocationPath, req.Point, now); err != nil {
		respondError(w, http.StatusInternalServerError, "save manual location failed")
		return
	}
	respondJSON(w, http.StatusOK, s.store.SetManualDeviceLocationAt(req.Point, now))
}

func (s *Server) handleClearManualDeviceLocation(w http.ResponseWriter, _ *http.Request) {
	if err := settings.ClearManualDeviceLocation(s.cfg.ManualLocationPath); err != nil {
		respondError(w, http.StatusInternalServerError, "clear manual location failed")
		return
	}
	respondJSON(w, http.StatusOK, s.store.ClearManualDeviceLocation())
}

func (s *Server) handleUserSettings(w http.ResponseWriter, _ *http.Request) {
	if s.userSettings == nil {
		respondJSON(w, http.StatusOK, s.userSettingsWithRuntimeDefaults(model.UserSettings{}))
		return
	}
	settings, ok, err := s.userSettings.LoadUser()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "load user settings failed")
		return
	}
	if !ok {
		settings = model.UserSettings{}
	}
	respondJSON(w, http.StatusOK, s.userSettingsWithRuntimeDefaults(settings))
}

func (s *Server) handleUpdateUserSettings(w http.ResponseWriter, r *http.Request) {
	var req userSettingsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !validIntrusionRetentionDays(req.IntrusionRetentionDays) {
		respondError(w, http.StatusBadRequest, "invalid intrusion retention days")
		return
	}
	if !validPositionExpireSeconds(req.PositionExpireSeconds) {
		respondError(w, http.StatusBadRequest, "invalid position expire seconds")
		return
	}
	if !validWarningZoneRadius(req.WarningZoneRadiusMeters) {
		respondError(w, http.StatusBadRequest, "invalid warning zone radius")
		return
	}
	if req.WarningZones != nil && !validWarningZones(*req.WarningZones) {
		respondError(w, http.StatusBadRequest, "invalid warning zones")
		return
	}

	settings := model.UserSettings{}
	if s.userSettings != nil {
		loaded, ok, err := s.userSettings.LoadUser()
		if err != nil {
			respondError(w, http.StatusInternalServerError, "load user settings failed")
			return
		}
		if ok {
			settings = loaded
		}
	}
	settings = model.UserSettingsWithDefaults(settings)
	if req.IntrusionRetentionDays != nil {
		settings.IntrusionRetentionDays = req.IntrusionRetentionDays
	}
	if req.ScreenTitle != nil {
		settings.ScreenTitle = truncateRunes(strings.TrimSpace(*req.ScreenTitle), 32)
	}
	if req.PositionExpireSeconds != nil {
		settings.PositionExpireSeconds = req.PositionExpireSeconds
	}
	if req.ScreenStrikeChannelLabels != nil {
		settings.ScreenStrikeChannelLabels = normalizeScreenStrikeChannelLabels(*req.ScreenStrikeChannelLabels)
	}
	if req.Lingyun != nil {
		settings.Lingyun = model.LingyunSettingsWithGeneratedClientID(*req.Lingyun)
	}
	if req.WarningZoneEnabled != nil {
		settings.WarningZoneEnabled = req.WarningZoneEnabled
	}
	if req.WarningZoneRadiusMeters != nil {
		radius := math.Round(*req.WarningZoneRadiusMeters)
		settings.WarningZoneRadiusMeters = &radius
	}
	if req.WarningZones != nil {
		zones := normalizeWarningZones(*req.WarningZones, time.Now())
		enabled := len(zones) > 0
		settings.WarningZoneEnabled = &enabled
		if enabled {
			radius := zones[0].RadiusMeters
			settings.WarningZoneRadiusMeters = &radius
		}
		settings.WarningZones = nil
	}
	if req.Whitelist != nil {
		settings.Whitelist = normalizeUserWhitelist(*req.Whitelist, time.Now())
	}
	settings = model.UserSettingsWithDefaults(settings)
	if s.userSettings == nil {
		s.applyUserSettings(settings)
		respondJSON(w, http.StatusOK, settings)
		return
	}
	saved, err := s.userSettings.SaveEditableUser(settings)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "save user settings failed")
		return
	}
	saved = model.UserSettingsWithDefaults(saved)
	saved = s.userSettingsWithRuntimeDefaults(saved)
	if err := s.pruneIntrusionsByUserSettings(r.Context(), saved); err != nil {
		respondError(w, http.StatusInternalServerError, "prune intrusion records failed")
		return
	}
	s.applyUserSettings(saved)
	respondJSON(w, http.StatusOK, saved)
}

func (s *Server) handleIntrusions(w http.ResponseWriter, r *http.Request) {
	targetType, err := intrusion.ParseTargetType(r.URL.Query().Get("type"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid intrusion target type")
		return
	}
	if s.intrusions == nil {
		respondJSON(w, http.StatusOK, model.ListResponse[model.IntrusionRecord]{
			Items: []model.IntrusionRecord{},
			Count: 0,
		})
		return
	}
	_ = s.store.Positions(0)
	if err := s.maybePruneIntrusionsByCurrentUserSettings(r.Context()); err != nil {
		respondError(w, http.StatusInternalServerError, "prune intrusion records failed")
		return
	}
	limit := parseLimit(r, 50)
	offset := parseOffset(r)
	dateFrom, err := parseDateQuery(r.URL.Query().Get("dateFrom"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid intrusion dateFrom")
		return
	}
	dateTo, err := parseDateQuery(r.URL.Query().Get("dateTo"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid intrusion dateTo")
		return
	}
	items, err := s.intrusions.List(r.Context(), intrusion.QueryOptions{
		Limit:      limit + 1,
		Offset:     offset,
		TargetType: targetType,
		Model:      r.URL.Query().Get("model"),
		Serial:     r.URL.Query().Get("serial"),
		DateFrom:   dateFrom,
		DateTo:     dateTo,
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, "list intrusion records failed")
		return
	}
	respondJSON(w, http.StatusOK, pagedListResponse(items, limit, offset))
}

func (s *Server) handleDeleteIntrusions(w http.ResponseWriter, r *http.Request) {
	var req model.IntrusionDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request")
		return
	}
	ids := normalizedIntrusionRecordIDs(req.IDs)
	if len(ids) == 0 {
		respondError(w, http.StatusBadRequest, "empty intrusion record ids")
		return
	}
	if s.intrusions == nil {
		respondJSON(w, http.StatusOK, model.IntrusionDeleteResponse{})
		return
	}
	deleted, err := s.intrusions.Delete(r.Context(), ids)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "delete intrusion records failed")
		return
	}
	respondJSON(w, http.StatusOK, model.IntrusionDeleteResponse{Deleted: deleted})
}

func (s *Server) handleFPVVideoRecords(w http.ResponseWriter, r *http.Request) {
	if s.fpvRecords == nil {
		respondJSON(w, http.StatusOK, model.ListResponse[model.FPVVideoRecord]{
			Items: []model.FPVVideoRecord{},
			Count: 0,
		})
		return
	}
	limit := parseLimit(r, 50)
	offset := parseOffset(r)
	dateFrom, err := parseDateQuery(r.URL.Query().Get("dateFrom"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid fpv video record dateFrom")
		return
	}
	dateTo, err := parseDateQuery(r.URL.Query().Get("dateTo"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid fpv video record dateTo")
		return
	}
	items, err := s.fpvRecords.List(r.Context(), fpvrecord.QueryOptions{
		Limit:      limit + 1,
		Offset:     offset,
		SignalType: r.URL.Query().Get("signalType"),
		DeviceSN:   r.URL.Query().Get("deviceSn"),
		DateFrom:   dateFrom,
		DateTo:     dateTo,
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, "list fpv video records failed")
		return
	}
	response := pagedListResponse(items, limit, offset)
	for index := range response.Items {
		response.Items[index] = s.withFPVVideoRecordFileURL(response.Items[index])
	}
	respondJSON(w, http.StatusOK, response)
}

func (s *Server) handleFPVVideoRecordFile(w http.ResponseWriter, r *http.Request) {
	if s.fpvRecords == nil {
		respondError(w, http.StatusNotFound, "fpv video record not found")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	record, ok, err := s.fpvRecords.Get(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "load fpv video record failed")
		return
	}
	if !ok || record.Status != model.FPVVideoRecordStatusReady {
		respondError(w, http.StatusNotFound, "fpv video record not found")
		return
	}
	path, ok := fpvrecord.SafeRecordPath(s.cfg.FPVVideo.RecordDir, record.FileName)
	if !ok {
		respondError(w, http.StatusNotFound, "fpv video file not found")
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		respondError(w, http.StatusNotFound, "fpv video file not found")
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	http.ServeFile(w, r, path)
}

func (s *Server) handleExportFPVVideoRecords(w http.ResponseWriter, r *http.Request) {
	var req model.FPVVideoRecordDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request")
		return
	}
	ids := normalizedIntrusionRecordIDs(req.IDs)
	if len(ids) == 0 {
		respondError(w, http.StatusBadRequest, "empty fpv video record ids")
		return
	}
	if s.fpvRecords == nil {
		respondError(w, http.StatusNotFound, "fpv video records not found")
		return
	}

	files := make([]fpvVideoRecordExportFile, 0, len(ids))
	for _, id := range ids {
		record, ok, err := s.fpvRecords.Get(r.Context(), id)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "load fpv video record failed")
			return
		}
		if !ok || record.Status != model.FPVVideoRecordStatusReady {
			continue
		}
		path, ok := fpvrecord.SafeRecordPath(s.cfg.FPVVideo.RecordDir, record.FileName)
		if !ok {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		files = append(files, fpvVideoRecordExportFile{
			record: record,
			path:   path,
			size:   info.Size(),
		})
	}
	if len(files) == 0 {
		respondError(w, http.StatusNotFound, "no exportable fpv video files")
		return
	}

	fileName := fmt.Sprintf("fpv-videos_%s.zip", time.Now().UTC().Format("20060102T150405Z"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fileName))
	if err := writeFPVVideoRecordZip(w, files); err != nil {
		slog.Warn("导出 FPV 图传视频失败", "error", err)
	}
}

func (s *Server) handleDeleteFPVVideoRecords(w http.ResponseWriter, r *http.Request) {
	var req model.FPVVideoRecordDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request")
		return
	}
	ids := normalizedIntrusionRecordIDs(req.IDs)
	if len(ids) == 0 {
		respondError(w, http.StatusBadRequest, "empty fpv video record ids")
		return
	}
	if s.fpvRecords == nil {
		respondJSON(w, http.StatusOK, model.FPVVideoRecordDeleteResponse{})
		return
	}
	result, err := s.fpvRecords.Delete(r.Context(), ids, s.cfg.FPVVideo.RecordDir)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "delete fpv video records failed")
		return
	}
	for _, path := range result.FilePaths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("删除 FPV 图传视频文件失败", "path", path, "error", err)
		}
	}
	respondJSON(w, http.StatusOK, model.FPVVideoRecordDeleteResponse{Deleted: result.Deleted})
}

func (s *Server) handleInterferenceReports(w http.ResponseWriter, r *http.Request) {
	status, err := interferencereport.ParseStatus(r.URL.Query().Get("status"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid interference report status")
		return
	}
	if s.interferenceReports == nil {
		respondJSON(w, http.StatusOK, model.ListResponse[model.InterferenceReportSummary]{
			Items: []model.InterferenceReportSummary{},
			Count: 0,
		})
		return
	}
	limit := parseLimit(r, 50)
	offset := parseOffset(r)
	items, err := s.interferenceReports.List(r.Context(), interferencereport.QueryOptions{
		Limit:  limit + 1,
		Offset: offset,
		Status: status,
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, "list interference reports failed")
		return
	}
	respondJSON(w, http.StatusOK, pagedListResponse(items, limit, offset))
}

func (s *Server) handleInterferenceReport(w http.ResponseWriter, r *http.Request) {
	if s.interferenceReports == nil {
		respondError(w, http.StatusNotFound, "interference report not found")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	report, ok, err := s.interferenceReports.Get(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "load interference report failed")
		return
	}
	if !ok {
		respondError(w, http.StatusNotFound, "interference report not found")
		return
	}
	respondJSON(w, http.StatusOK, report)
}

func (s *Server) handleDeleteFailedInterferenceReport(w http.ResponseWriter, r *http.Request) {
	if s.interferenceReports == nil {
		respondJSON(w, http.StatusOK, model.InterferenceReportDeleteResponse{})
		return
	}
	deleted, err := s.interferenceReports.DeleteFailed(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		if errors.Is(err, interferencereport.ErrNotFound) {
			respondError(w, http.StatusNotFound, "interference report not found")
			return
		}
		if errors.Is(err, interferencereport.ErrNotFailed) {
			respondError(w, http.StatusConflict, "only failed interference reports can be deleted")
			return
		}
		respondError(w, http.StatusInternalServerError, "delete interference report failed")
		return
	}
	respondJSON(w, http.StatusOK, model.InterferenceReportDeleteResponse{Deleted: deleted})
}

func (s *Server) handleScreenFPVVideoWHEP(w http.ResponseWriter, r *http.Request) {
	if s.fpvVideo == nil || !s.fpvVideo.Enabled() {
		respondError(w, http.StatusNotFound, "fpv video stream is not configured")
		return
	}
	sessionToken := r.URL.Query().Get("session")
	if !s.activeFPVVideoSessionToken(sessionToken) {
		respondError(w, http.StatusForbidden, "fpv video session is not active")
		return
	}
	upstreamRaw := s.fpvVideo.WHEPURL()
	if upstreamRaw == "" {
		respondError(w, http.StatusServiceUnavailable, "fpv video stream is not ready")
		return
	}
	resourceSuffix := strings.TrimPrefix(r.URL.Path, "/api/v1/screen/fpv-video/whep")
	s.proxyFPVVideoWHEP(w, r, upstreamRaw, resourceSuffix, sessionToken)
}

func (s *Server) proxyFPVVideoWHEP(w http.ResponseWriter, r *http.Request, upstreamRaw string, pathSuffix string, sessionToken string) {
	upstream, err := url.Parse(upstreamRaw)
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{
		Scheme: upstream.Scheme,
		Host:   upstream.Host,
	})
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.URL.Scheme = upstream.Scheme
		req.URL.Host = upstream.Host
		req.URL.Path = upstream.Path + pathSuffix
		req.URL.RawQuery = upstream.RawQuery
		req.Host = upstream.Host
		req.Header.Del("Cookie")
		req.Header.Del("Authorization")
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		rewriteFPVVideoWHEPLocation(resp, upstream, sessionToken)
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		respondError(w, http.StatusBadGateway, err.Error())
	}
	proxy.ServeHTTP(w, r)
}

func rewriteFPVVideoWHEPLocation(resp *http.Response, upstream *url.URL, sessionToken string) {
	raw := strings.TrimSpace(resp.Header.Get("Location"))
	if raw == "" {
		return
	}
	location, err := url.Parse(raw)
	if err != nil {
		return
	}
	upstreamPrefix := strings.TrimSuffix(upstream.Path, "/")
	if upstreamPrefix == "" {
		upstreamPrefix = "/"
	}
	locationPath := location.Path
	if locationPath == "" {
		return
	}
	if !strings.HasPrefix(locationPath, upstreamPrefix) {
		return
	}
	suffix := strings.TrimPrefix(locationPath, upstreamPrefix)
	proxyLocation := &url.URL{Path: "/api/v1/screen/fpv-video/whep" + suffix}
	query := location.Query()
	if sessionToken != "" {
		query.Set("session", sessionToken)
	}
	proxyLocation.RawQuery = query.Encode()
	resp.Header.Set("Location", proxyLocation.String())
}

func (s *Server) parseFPVVideoSessionTarget(r *http.Request, frequency int) (model.ScreenFPVTarget, error) {
	targetID := strings.TrimSpace(r.URL.Query().Get("targetId"))
	if targetID == "" {
		now := time.Now()
		return model.ScreenFPVTarget{
			Frequency:  float64(frequency),
			SignalType: "FPV",
			Valid:      true,
			FirstSeen:  now,
			LastSeen:   now,
			LastRecord: model.ScreenFPVLastRecord{
				ReceivedAt: now,
				Frequency:  float64(frequency),
				SignalType: "FPV",
				Valid:      true,
			},
		}, nil
	}
	target, ok := s.store.FPVTarget(targetID)
	if !ok {
		return model.ScreenFPVTarget{}, errors.New("fpv video target is not active")
	}
	return target, nil
}

func (s *Server) prepareFPVVideoRecording(
	target model.ScreenFPVTarget,
	frequency int,
) (fpvVideoRecordingSpec, error) {
	if s.fpvRecords == nil {
		return fpvVideoRecordingSpec{}, nil
	}
	if strings.TrimSpace(s.cfg.FPVVideo.RTSPURL) == "" {
		return fpvVideoRecordingSpec{}, errors.New("fpv video RTSP source is required for recording")
	}
	recordDir := strings.TrimSpace(s.cfg.FPVVideo.RecordDir)
	if recordDir == "" {
		return fpvVideoRecordingSpec{}, errors.New("fpv video record directory is not configured")
	}
	if err := os.MkdirAll(recordDir, 0o755); err != nil {
		return fpvVideoRecordingSpec{}, fmt.Errorf("prepare fpv video record directory: %w", err)
	}
	recordID := fpvVideoRecordID(target, time.Now())
	fileBase := recordID + "_" + strconv.Itoa(frequency)
	return fpvVideoRecordingSpec{
		ID:       recordID,
		BasePath: filepath.Join(recordDir, fileBase+"_%path_%s"),
		FileGlob: fileBase + "_*.mp4",
	}, nil
}

func (s *Server) handleScreenFPVVideoSession(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		respondError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	writer := bufio.NewWriter(w)
	frequency, err := parseFPVVideoFrequency(r)
	if err != nil {
		_ = writeFPVVideoSessionEvent(writer, fpvVideoSessionErrorType, map[string]any{
			"message": err.Error(),
		})
		flusher.Flush()
		return
	}
	if s.fpv == nil {
		_ = writeFPVVideoSessionEvent(writer, fpvVideoSessionErrorType, map[string]any{
			"message": fpv.ErrNoCommandConnection.Error(),
		})
		flusher.Flush()
		return
	}
	if s.fpvVideo == nil || !s.fpvVideo.Enabled() {
		_ = writeFPVVideoSessionEvent(writer, fpvVideoSessionErrorType, map[string]any{
			"message": fpvvideo.ErrNotConfigured.Error(),
		})
		flusher.Flush()
		return
	}
	target, err := s.parseFPVVideoSessionTarget(r, frequency)
	if err != nil {
		_ = writeFPVVideoSessionEvent(writer, fpvVideoSessionErrorType, map[string]any{
			"frequency": frequency,
			"message":   err.Error(),
		})
		flusher.Flush()
		return
	}
	recording, err := s.prepareFPVVideoRecording(target, frequency)
	if err != nil {
		_ = writeFPVVideoSessionEvent(writer, fpvVideoSessionErrorType, map[string]any{
			"frequency": frequency,
			"message":   err.Error(),
		})
		flusher.Flush()
		return
	}

	sessionID, sessionToken, ok := s.tryBeginFPVVideoSessionWithRecording(frequency, target, recording)
	if !ok {
		_ = writeFPVVideoSessionEvent(writer, fpvVideoSessionErrorType, map[string]any{
			"code":    fpvVideoSessionBusyCode,
			"message": "FPV video is already being viewed",
		})
		flusher.Flush()
		return
	}
	shouldReset := false
	sessionReady := false
	defer func() {
		s.stopFPVVideoSessionByID(sessionID, shouldReset, !sessionReady)
	}()

	if err := s.fpv.SetVideoFrequency(r.Context(), frequency); err != nil {
		_ = writeFPVVideoSessionEvent(writer, fpvVideoSessionErrorType, map[string]any{
			"frequency": frequency,
			"message":   err.Error(),
		})
		flusher.Flush()
		return
	}
	shouldReset = true
	if recording.BasePath != "" {
		s.fpvVideo.SetRecordPath(recording.BasePath)
	} else {
		s.fpvVideo.SetRecordPath("")
	}
	if err := s.fpvVideo.Restart(r.Context()); err != nil {
		_ = writeFPVVideoSessionEvent(writer, fpvVideoSessionErrorType, map[string]any{
			"frequency": frequency,
			"message":   err.Error(),
		})
		flusher.Flush()
		return
	}
	if err := writeFPVVideoSessionEvent(writer, fpvVideoSessionReadyType, map[string]any{
		"frequency": frequency,
		"session":   sessionToken,
	}); err != nil {
		return
	}
	flusher.Flush()
	sessionReady = true

	ticker := time.NewTicker(sseHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := writeComment(writer, "fpv-video"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleScreenFPVVideoSessionClose(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("session"))
	if token == "" {
		respondError(w, http.StatusBadRequest, "fpv video session is required")
		return
	}
	if err := s.stopFPVVideoSessionByToken(token); err != nil {
		if errors.Is(err, errFPVVideoSessionNotActive) {
			respondError(w, http.StatusNotFound, errFPVVideoSessionNotActive.Error())
			return
		}
		respondError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"ok": true,
	})
}

func (s *Server) activeFPVVideoSessionID(sessionID uint64) bool {
	s.fpvVideoMu.Lock()
	defer s.fpvVideoMu.Unlock()
	return sessionID != 0 && s.activeFPVVideoSession == sessionID
}

func (s *Server) activeFPVVideoSessionToken(token string) bool {
	s.fpvVideoMu.Lock()
	defer s.fpvVideoMu.Unlock()
	return token != "" && s.activeFPVVideoSession != 0 && s.activeFPVVideoToken == token
}

func (s *Server) activeFPVVideoSessionSnapshot() (uint64, bool) {
	s.fpvVideoMu.Lock()
	defer s.fpvVideoMu.Unlock()
	if s.activeFPVVideoSession == 0 {
		return 0, false
	}
	return s.activeFPVVideoSession, true
}

func (s *Server) activeFPVVideoSessionSnapshotByID(sessionID uint64) (fpvVideoSessionSnapshot, bool) {
	s.fpvVideoMu.Lock()
	defer s.fpvVideoMu.Unlock()
	if sessionID == 0 || s.activeFPVVideoSession != sessionID {
		return fpvVideoSessionSnapshot{}, false
	}
	return s.fpvVideoSessionSnapshotLocked(), true
}

func (s *Server) activeFPVVideoSessionSnapshotByToken(token string) (fpvVideoSessionSnapshot, bool) {
	s.fpvVideoMu.Lock()
	defer s.fpvVideoMu.Unlock()
	if token == "" || s.activeFPVVideoSession == 0 || s.activeFPVVideoToken != token {
		return fpvVideoSessionSnapshot{}, false
	}
	return s.fpvVideoSessionSnapshotLocked(), true
}

func (s *Server) activeFPVVideoFullSessionSnapshot() (fpvVideoSessionSnapshot, bool) {
	s.fpvVideoMu.Lock()
	defer s.fpvVideoMu.Unlock()
	if s.activeFPVVideoSession == 0 {
		return fpvVideoSessionSnapshot{}, false
	}
	return s.fpvVideoSessionSnapshotLocked(), true
}

func (s *Server) fpvVideoSessionSnapshotLocked() fpvVideoSessionSnapshot {
	return fpvVideoSessionSnapshot{
		ID:             s.activeFPVVideoSession,
		Token:          s.activeFPVVideoToken,
		Frequency:      s.activeFPVVideoFreq,
		StartedAt:      s.activeFPVVideoSince,
		Target:         s.activeFPVVideoTarget,
		RecordID:       s.activeFPVVideoRecordID,
		RecordBasePath: s.activeFPVVideoRecordBasePath,
		RecordFile:     s.activeFPVVideoRecordFile,
		RecordFileGlob: s.activeFPVVideoRecordGlob,
	}
}

func (s *Server) stopActiveFPVVideoSession(ctx context.Context) error {
	s.fpvVideoStopMu.Lock()
	defer s.fpvVideoStopMu.Unlock()

	snapshot, active := s.activeFPVVideoFullSessionSnapshot()
	if !active {
		return nil
	}
	if err := s.stopFPVVideo(ctx); err != nil {
		slog.Warn("关闭活跃 FPV 视频会话失败", "session", snapshot.ID, "error", err)
		if closeErr := s.closeFPVVideoConverter(); closeErr != nil {
			slog.Warn("关闭 FPV 视频转换器失败", "session", snapshot.ID, "error", closeErr)
		}
		s.releaseFPVVideoSessionAfterStop(snapshot, err)
		return err
	}
	return s.releaseFPVVideoSessionAfterStop(snapshot, nil)
}

func (s *Server) stopFPVVideoSessionByID(sessionID uint64, shouldReset bool, forceReleaseOnResetFailure bool) error {
	s.fpvVideoStopMu.Lock()
	defer s.fpvVideoStopMu.Unlock()

	snapshot, ok := s.activeFPVVideoSessionSnapshotByID(sessionID)
	if !ok {
		return errFPVVideoSessionNotActive
	}
	if shouldReset {
		if err := s.stopFPVVideoWithTimeout(context.Background()); err != nil {
			slog.Warn("关闭 FPV 视频频点失败", "session", sessionID, "error", err)
			if forceReleaseOnResetFailure || snapshot.RecordID != "" {
				if closeErr := s.closeFPVVideoConverter(); closeErr != nil {
					slog.Warn("关闭 FPV 视频转换器失败", "session", sessionID, "error", closeErr)
				}
				_ = s.releaseFPVVideoSessionAfterStop(snapshot, err)
			}
			return err
		}
	}
	return s.releaseFPVVideoSessionAfterStop(snapshot, nil)
}

func (s *Server) stopFPVVideoSessionByToken(token string) error {
	s.fpvVideoStopMu.Lock()
	defer s.fpvVideoStopMu.Unlock()

	snapshot, ok := s.activeFPVVideoSessionSnapshotByToken(token)
	if !ok {
		return errFPVVideoSessionNotActive
	}
	if err := s.stopFPVVideoWithTimeout(context.Background()); err != nil {
		slog.Warn("关闭 FPV 视频频点失败", "error", err)
		if closeErr := s.closeFPVVideoConverter(); closeErr != nil {
			slog.Warn("关闭 FPV 视频转换器失败", "error", closeErr)
		}
		_ = s.releaseFPVVideoSessionAfterStop(snapshot, err)
		return err
	}
	return s.releaseFPVVideoSessionAfterStop(snapshot, nil)
}

func (s *Server) releaseFPVVideoSessionAfterStop(snapshot fpvVideoSessionSnapshot, stopErr error) error {
	if !s.finishFPVVideoSession(snapshot.ID) {
		return errFPVVideoSessionNotActive
	}
	stopError := ""
	if stopErr != nil {
		stopError = stopErr.Error()
	}
	s.persistFPVVideoRecord(context.Background(), snapshot, time.Now(), stopError)
	return nil
}

func (s *Server) stopFPVVideoWithTimeout(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, fpvVideoStopTimeout)
	defer cancel()
	return s.stopFPVVideo(ctx)
}

func (s *Server) stopFPVVideo(ctx context.Context) error {
	if s.fpv == nil {
		return fpv.ErrNoCommandConnection
	}
	if err := s.fpv.StopVideo(ctx); err != nil {
		return err
	}
	return s.shutdownFPVVideoConverter()
}

func (s *Server) closeFPVVideoConverter() error {
	if s.fpvVideo != nil {
		return s.fpvVideo.Close()
	}
	return nil
}

func (s *Server) shutdownFPVVideoConverter() error {
	if s.fpvVideo != nil {
		return s.fpvVideo.Shutdown()
	}
	return nil
}

func (s *Server) tryBeginFPVVideoSession(frequency int) (uint64, string, bool) {
	return s.tryBeginFPVVideoSessionWithRecording(frequency, model.ScreenFPVTarget{}, fpvVideoRecordingSpec{})
}

func (s *Server) tryBeginFPVVideoSessionWithRecording(
	frequency int,
	target model.ScreenFPVTarget,
	recording fpvVideoRecordingSpec,
) (uint64, string, bool) {
	token, err := newFPVVideoSessionToken()
	if err != nil {
		slog.Warn("生成 FPV 视频会话令牌失败", "error", err)
		return 0, "", false
	}
	s.fpvVideoMu.Lock()
	defer s.fpvVideoMu.Unlock()
	if s.activeFPVVideoSession != 0 {
		return 0, "", false
	}
	s.nextFPVVideoSessionID++
	s.activeFPVVideoSession = s.nextFPVVideoSessionID
	s.activeFPVVideoFreq = frequency
	s.activeFPVVideoSince = time.Now()
	s.activeFPVVideoToken = token
	s.activeFPVVideoTarget = target
	s.activeFPVVideoRecordID = recording.ID
	s.activeFPVVideoRecordBasePath = recording.BasePath
	s.activeFPVVideoRecordFile = recording.FileName
	s.activeFPVVideoRecordGlob = recording.FileGlob
	return s.nextFPVVideoSessionID, token, true
}

func (s *Server) finishFPVVideoSession(sessionID uint64) bool {
	s.fpvVideoMu.Lock()
	defer s.fpvVideoMu.Unlock()
	if s.activeFPVVideoSession != sessionID {
		return false
	}
	s.activeFPVVideoSession = 0
	s.activeFPVVideoFreq = 0
	s.activeFPVVideoSince = time.Time{}
	s.activeFPVVideoToken = ""
	s.activeFPVVideoTarget = model.ScreenFPVTarget{}
	s.activeFPVVideoRecordID = ""
	s.activeFPVVideoRecordBasePath = ""
	s.activeFPVVideoRecordFile = ""
	s.activeFPVVideoRecordGlob = ""
	return true
}

func (s *Server) finishFPVVideoSessionByToken(token string) bool {
	s.fpvVideoMu.Lock()
	defer s.fpvVideoMu.Unlock()
	if token == "" || s.activeFPVVideoSession == 0 || s.activeFPVVideoToken != token {
		return false
	}
	s.activeFPVVideoSession = 0
	s.activeFPVVideoFreq = 0
	s.activeFPVVideoSince = time.Time{}
	s.activeFPVVideoToken = ""
	s.activeFPVVideoTarget = model.ScreenFPVTarget{}
	s.activeFPVVideoRecordID = ""
	s.activeFPVVideoRecordBasePath = ""
	s.activeFPVVideoRecordFile = ""
	s.activeFPVVideoRecordGlob = ""
	return true
}

func (s *Server) persistFPVVideoRecord(
	ctx context.Context,
	session fpvVideoSessionSnapshot,
	endedAt time.Time,
	stopError string,
) {
	if s.fpvRecords == nil || session.RecordID == "" {
		return
	}
	if endedAt.IsZero() {
		endedAt = time.Now()
	}
	startedAt := session.StartedAt
	if startedAt.IsZero() {
		startedAt = endedAt
	}
	record := model.FPVVideoRecord{
		ID:              session.RecordID,
		TargetID:        session.Target.ID,
		Frequency:       session.Target.Frequency,
		RSSI:            session.Target.RSSI,
		SignalType:      session.Target.SignalType,
		DeviceSN:        session.Target.DeviceSN,
		StartedAt:       startedAt,
		EndedAt:         endedAt,
		DurationSeconds: max(0, int64(endedAt.Sub(startedAt).Seconds())),
		Status:          model.FPVVideoRecordStatusReady,
		FileName:        session.RecordFile,
		LastRecord:      session.Target.LastRecord,
	}
	if record.Frequency <= 0 {
		record.Frequency = float64(session.Frequency)
	}
	if stopError != "" {
		record.Status = model.FPVVideoRecordStatusFailed
		record.Error = stopError
	}
	path, fileName, ok := s.resolveFPVVideoRecordFile(session)
	if !ok {
		record.Status = model.FPVVideoRecordStatusFailed
		if record.Error == "" {
			if session.RecordBasePath == "" && session.RecordFile == "" && session.RecordFileGlob == "" {
				record.Error = "fpv video recording was not configured"
			} else {
				record.Error = "fpv video recording file is missing"
			}
		}
	} else {
		record.FileName = fileName
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Size() <= 0 {
			record.Status = model.FPVVideoRecordStatusFailed
			if record.Error == "" {
				record.Error = "fpv video recording file is missing"
			}
		} else {
			record.FileSizeBytes = info.Size()
		}
	}
	if err := s.fpvRecords.Insert(ctx, record); err != nil {
		slog.Warn("保存 FPV 图传录制记录失败", "record", session.RecordID, "error", err)
	}
}

func (s *Server) resolveFPVVideoRecordFile(session fpvVideoSessionSnapshot) (string, string, bool) {
	if session.RecordFile != "" {
		path, ok := fpvrecord.SafeRecordPath(s.cfg.FPVVideo.RecordDir, session.RecordFile)
		return path, session.RecordFile, ok
	}
	recordDir := strings.TrimSpace(s.cfg.FPVVideo.RecordDir)
	pattern := strings.TrimSpace(session.RecordFileGlob)
	if recordDir == "" || pattern == "" || filepath.IsAbs(pattern) || strings.Contains(pattern, string(filepath.Separator)) {
		return "", "", false
	}
	matches, err := filepath.Glob(filepath.Join(recordDir, pattern))
	if err != nil || len(matches) == 0 {
		return "", "", false
	}
	slices.SortFunc(matches, func(left, right string) int {
		leftInfo, leftErr := os.Stat(left)
		rightInfo, rightErr := os.Stat(right)
		if leftErr != nil && rightErr != nil {
			return strings.Compare(left, right)
		}
		if leftErr != nil {
			return 1
		}
		if rightErr != nil {
			return -1
		}
		return rightInfo.ModTime().Compare(leftInfo.ModTime())
	})
	for _, path := range matches {
		fileName := filepath.Base(path)
		if safePath, ok := fpvrecord.SafeRecordPath(recordDir, fileName); ok && safePath == path {
			return path, fileName, true
		}
	}
	return "", "", false
}

func (s *Server) handleScreenStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		respondError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	events, unsubscribe := s.store.Subscribe(s.cfg.EventBufferSize)
	defer unsubscribe()

	writer := bufio.NewWriter(w)
	if err := writeComment(writer, "connected"); err != nil {
		return
	}
	flusher.Flush()

	ticker := time.NewTicker(sseHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return
			}
			if !isScreenEvent(evt.Type) {
				continue
			}
			if err := writeEvent(writer, evt); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if err := writeComment(writer, "ping"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) fpvVideoStatus() model.FPVVideoStatus {
	if s.fpvVideo == nil || !s.fpvVideo.Enabled() {
		return model.FPVVideoStatus{}
	}
	active, frequency, activeSince := s.fpvVideoSessionStatus()
	var activeSincePtr *time.Time
	if active {
		activeSincePtr = &activeSince
	}
	return model.FPVVideoStatus{
		Enabled:         true,
		PlaybackURL:     s.fpvVideo.PlaybackURL(),
		PlaybackType:    "whep",
		Active:          active,
		ActiveFrequency: frequency,
		ActiveSince:     activeSincePtr,
	}
}

func (s *Server) fpvVideoSessionStatus() (bool, int, time.Time) {
	s.fpvVideoMu.Lock()
	defer s.fpvVideoMu.Unlock()
	return s.activeFPVVideoSession != 0, s.activeFPVVideoFreq, s.activeFPVVideoSince
}

func (s *Server) handleFrontend(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		respondError(w, http.StatusNotFound, "not found")
		return
	}

	dist, err := fs.Sub(webassets.Assets, "dist")
	if err != nil {
		respondError(w, http.StatusInternalServerError, "frontend assets unavailable")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if file, err := dist.Open(path); err == nil {
		_ = file.Close()
		http.FileServer(http.FS(dist)).ServeHTTP(w, r)
		return
	}
	r.URL.Path = "/index.html"
	http.FileServer(http.FS(dist)).ServeHTTP(w, r)
}

func (s *Server) handleOfflineMapTile(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.cfg.OfflineMapPath) == "" {
		http.NotFound(w, r)
		return
	}
	requestPath := strings.TrimPrefix(r.URL.Path, "/map/")
	if requestPath == "" || strings.Contains(requestPath, "\\") {
		http.NotFound(w, r)
		return
	}
	cleanPath := filepath.Clean(filepath.FromSlash(requestPath))
	if cleanPath == "." || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) || cleanPath == ".." || filepath.IsAbs(cleanPath) {
		http.NotFound(w, r)
		return
	}
	targetPath := filepath.Join(s.cfg.OfflineMapPath, cleanPath)
	info, err := os.Stat(targetPath)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, targetPath)
}

func newFPVVideoSessionToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func parseLimit(r *http.Request, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return fallback
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return fallback
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func parseOffset(r *http.Request) int {
	raw := strings.TrimSpace(r.URL.Query().Get("offset"))
	if raw == "" {
		return 0
	}
	offset, err := strconv.Atoi(raw)
	if err != nil || offset < 0 {
		return 0
	}
	return offset
}

func saveMultipartUpload(file multipart.File, filename string) (string, func(), error) {
	temp, err := os.CreateTemp("", "drone-management-upload-*.zip")
	if err != nil {
		return "", func() {}, err
	}
	tempPath := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	if _, err := io.Copy(temp, file); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if !strings.EqualFold(filepath.Ext(filename), ".zip") {
		cleanup()
		return "", func() {}, fmt.Errorf("离线地图只支持 .zip 文件")
	}
	return tempPath, cleanup, nil
}

type offlineMapUploadLogs struct {
	entriesList []model.OfflineMapUploadLog
}

func newOfflineMapUploadLogs() *offlineMapUploadLogs {
	return &offlineMapUploadLogs{
		entriesList: []model.OfflineMapUploadLog{},
	}
}

func (l *offlineMapUploadLogs) add(stage string, message string, status string, detail string) {
	if l == nil {
		return
	}
	l.entriesList = append(l.entriesList, model.OfflineMapUploadLog{
		Stage:     stage,
		Message:   message,
		Status:    status,
		Timestamp: time.Now(),
		Detail:    strings.TrimSpace(detail),
	})
}

func (l *offlineMapUploadLogs) entries() []model.OfflineMapUploadLog {
	if l == nil {
		return []model.OfflineMapUploadLog{}
	}
	return slices.Clone(l.entriesList)
}

func isTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func parseDateQuery(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.ParseInLocation(time.DateOnly, raw, time.Local)
	if err != nil {
		return time.Time{}, err
	}
	return parsed, nil
}

func pagedListResponse[T any](items []T, limit int, offset int) model.ListResponse[T] {
	if limit <= 0 {
		limit = len(items)
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	response := model.ListResponse[T]{
		Items: items,
		Count: len(items),
	}
	if hasMore {
		response.HasMore = true
		response.NextOffset = offset + len(items)
	}
	if response.Items == nil {
		response.Items = []T{}
	}
	return response
}

func parseFPVVideoFrequency(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("frequency"))
	if raw == "" {
		return 0, fpv.ErrInvalidCommandFrequency
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 0, fpv.ErrInvalidCommandFrequency
	}
	frequency := int(math.Round(value))
	if frequency <= 0 {
		return 0, fpv.ErrInvalidCommandFrequency
	}
	return frequency, nil
}

func validGeoPoint(point *model.GeoPoint) bool {
	return point != nil &&
		!math.IsNaN(point.Latitude) &&
		!math.IsNaN(point.Longitude) &&
		!math.IsInf(point.Latitude, 0) &&
		!math.IsInf(point.Longitude, 0) &&
		point.Latitude >= -90 &&
		point.Latitude <= 90 &&
		point.Longitude >= -180 &&
		point.Longitude <= 180 &&
		!(point.Latitude == 0 && point.Longitude == 0)
}

func validIntrusionRetentionDays(days *int) bool {
	return days == nil || *days >= 0
}

func validPositionExpireSeconds(seconds *int) bool {
	return seconds == nil ||
		(*seconds >= model.MinPositionExpireSeconds && *seconds <= model.MaxPositionExpireSeconds)
}

func validTCPPorts(positionPort, fpvPort int) bool {
	return positionPort >= model.MinTCPPort &&
		positionPort <= model.MaxTCPPort &&
		fpvPort >= model.MinTCPPort &&
		fpvPort <= model.MaxTCPPort &&
		positionPort != fpvPort
}

func validWarningZoneRadius(radius *float64) bool {
	return radius == nil ||
		(!math.IsNaN(*radius) &&
			!math.IsInf(*radius, 0) &&
			*radius >= model.MinWarningZoneRadiusMeters &&
			*radius <= model.MaxWarningZoneRadiusMeters)
}

func validWarningZones(zones []model.WarningZone) bool {
	const maxZones = 20
	if len(zones) > maxZones {
		return false
	}
	for _, zone := range zones {
		if !validGeoPoint(&zone.Center) ||
			math.IsNaN(zone.RadiusMeters) ||
			math.IsInf(zone.RadiusMeters, 0) ||
			zone.RadiusMeters < model.MinWarningZoneRadiusMeters ||
			zone.RadiusMeters > model.MaxWarningZoneRadiusMeters {
			return false
		}
	}
	return true
}

func normalizeWarningZones(zones []model.WarningZone, now time.Time) []model.WarningZone {
	if len(zones) == 0 {
		return []model.WarningZone{}
	}
	const maxZones = 20
	normalized := make([]model.WarningZone, 0, min(len(zones), maxZones))
	seen := make(map[string]struct{}, len(zones))
	for _, zone := range zones {
		if len(normalized) == maxZones {
			break
		}
		if !validGeoPoint(&zone.Center) ||
			math.IsNaN(zone.RadiusMeters) ||
			math.IsInf(zone.RadiusMeters, 0) ||
			zone.RadiusMeters < model.MinWarningZoneRadiusMeters ||
			zone.RadiusMeters > model.MaxWarningZoneRadiusMeters {
			continue
		}
		id := cleanRecordIDPart(zone.ID)
		if id == "" {
			id = newWarningZoneID()
		}
		if _, ok := seen[id]; ok {
			id = newWarningZoneID()
		}
		seen[id] = struct{}{}
		createdAt := zone.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		normalized = append(normalized, model.WarningZone{
			ID: id,
			Center: model.GeoPoint{
				Latitude:  zone.Center.Latitude,
				Longitude: zone.Center.Longitude,
			},
			RadiusMeters: math.Round(zone.RadiusMeters),
			CreatedAt:    createdAt,
		})
	}
	if normalized == nil {
		return []model.WarningZone{}
	}
	return normalized
}

func newWarningZoneID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "warning-zone-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return "warning-zone-" + hex.EncodeToString(raw[:])
}

func normalizeUserWhitelist(items []model.WhitelistItem, now time.Time) []model.WhitelistItem {
	if len(items) == 0 {
		return []model.WhitelistItem{}
	}
	const maxItems = 500
	normalized := make([]model.WhitelistItem, 0, min(len(items), maxItems))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if len(normalized) == maxItems {
			break
		}
		serial := truncateRunes(strings.TrimSpace(item.Serial), 128)
		if serial == "" {
			continue
		}
		modelName := truncateRunes(strings.TrimSpace(item.Model), 64)
		source := truncateRunes(strings.TrimSpace(item.Source), 32)
		if isUncrackedDJIDroneWhitelistItem(serial, modelName, source) {
			continue
		}
		key := strings.ToLower(serial)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		createdAt := item.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		normalized = append(normalized, model.WhitelistItem{
			Serial:    serial,
			Model:     modelName,
			Source:    source,
			CreatedAt: createdAt,
		})
	}
	if normalized == nil {
		return []model.WhitelistItem{}
	}
	return normalized
}

func normalizeScreenStrikeChannelLabels(labels []string) []string {
	const maxLabels = 8
	normalized := make([]string, maxLabels)
	for index := 0; index < maxLabels && index < len(labels); index++ {
		normalized[index] = truncateRunes(strings.TrimSpace(labels[index]), 32)
	}
	return normalized
}

func isUncrackedDJIDroneWhitelistItem(serial, modelName, source string) bool {
	return strings.EqualFold(strings.TrimSpace(modelName), "DJI-Drone") &&
		!strings.EqualFold(strings.TrimSpace(source), "manual") &&
		isTemporaryDIDSerial(serial)
}

func isTemporaryDIDSerial(serial string) bool {
	serial = strings.TrimSpace(serial)
	if len(serial) != 8 {
		return false
	}
	for _, ch := range serial {
		if !((ch >= '0' && ch <= '9') ||
			(ch >= 'a' && ch <= 'f') ||
			(ch >= 'A' && ch <= 'F')) {
			return false
		}
	}
	return true
}

func truncateRunes(value string, maxLength int) string {
	if maxLength <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxLength {
		return value
	}
	return string(runes[:maxLength])
}

func normalizedIntrusionRecordIDs(ids []string) []string {
	normalized := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || slices.Contains(normalized, id) {
			continue
		}
		normalized = append(normalized, id)
	}
	return normalized
}

func (s *Server) withFPVVideoRecordFileURL(record model.FPVVideoRecord) model.FPVVideoRecord {
	if record.Status == model.FPVVideoRecordStatusReady && record.FileName != "" {
		record.FileURL = "/api/v1/fpv-video-records/" + url.PathEscape(record.ID) + "/file"
	}
	return record
}

type fpvVideoRecordExportFile struct {
	record model.FPVVideoRecord
	path   string
	size   int64
}

func writeFPVVideoRecordZip(w io.Writer, files []fpvVideoRecordExportFile) error {
	archive := zip.NewWriter(w)
	defer archive.Close()

	usedNames := map[string]int{}
	for _, file := range files {
		entryName := fpvVideoRecordZipEntryName(file.record, usedNames)
		header := &zip.FileHeader{
			Name:   entryName,
			Method: zip.Store,
		}
		header.SetModTime(file.record.EndedAt)
		header.SetMode(0o644)
		if file.size > 0 {
			header.UncompressedSize64 = uint64(file.size)
		}
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return fmt.Errorf("create zip entry %q: %w", entryName, err)
		}
		if err := copyFileToWriter(writer, file.path); err != nil {
			return fmt.Errorf("write zip entry %q: %w", entryName, err)
		}
	}
	return nil
}

func fpvVideoRecordZipEntryName(record model.FPVVideoRecord, used map[string]int) string {
	fileName := cleanZipEntryPart(filepath.Base(record.FileName))
	if fileName == "" || fileName == "." {
		fileName = cleanZipEntryPart(record.ID)
	}
	if fileName == "" {
		fileName = "video.mp4"
	}
	prefix := cleanZipEntryPart(record.ID)
	if prefix != "" && !strings.HasPrefix(fileName, prefix) {
		fileName = prefix + "_" + fileName
	}
	count := used[fileName]
	used[fileName] = count + 1
	if count == 0 {
		return fileName
	}
	extension := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, extension)
	return fmt.Sprintf("%s_%d%s", base, count+1, extension)
}

func cleanZipEntryPart(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\\", "_")
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.Trim(value, ". ")
	if value == "" {
		return ""
	}
	var builder strings.Builder
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z',
			ch >= 'A' && ch <= 'Z',
			ch >= '0' && ch <= '9',
			ch == '-',
			ch == '_',
			ch == '.':
			builder.WriteRune(ch)
		default:
			builder.WriteRune('_')
		}
	}
	return builder.String()
}

func copyFileToWriter(w io.Writer, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(w, file)
	return err
}

func fpvVideoRecordID(target model.ScreenFPVTarget, now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	parts := []string{
		"fpv-video",
		strconv.FormatInt(now.UnixNano(), 10),
		cleanRecordIDPart(target.SignalType),
		cleanRecordIDPart(target.DeviceSN),
	}
	out := []string{}
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, "-")
}

func cleanRecordIDPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var builder strings.Builder
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			builder.WriteRune(ch)
			continue
		}
		if ch == '-' || ch == '_' {
			builder.WriteRune('-')
		}
	}
	cleaned := strings.Trim(builder.String(), "-")
	if len(cleaned) > 48 {
		cleaned = cleaned[:48]
	}
	return cleaned
}

const intrusionPruneInterval = time.Minute

func (s *Server) pruneIntrusionsByUserSettings(ctx context.Context, settings model.UserSettings) error {
	if s == nil || s.intrusions == nil {
		return nil
	}
	days := model.UserSettingsIntrusionRetentionDays(settings)
	_, err := s.intrusions.PruneRetention(ctx, days, time.Now())
	return err
}

func (s *Server) applyUserSettings(settings model.UserSettings) {
	if s == nil || s.store == nil {
		return
	}
	seconds := model.UserSettingsPositionExpireSeconds(settings)
	s.store.SetPositionTTL(time.Duration(seconds) * time.Second)
	if s.lingyun != nil {
		s.lingyun.ApplySettings(settings)
	}
}

func (s *Server) applyTCPPorts(positionPort, fpvPort int) {
	if s == nil || !validTCPPorts(positionPort, fpvPort) {
		return
	}
	s.cfg.PositionTCPPort = positionPort
	s.cfg.FPVTCPPort = fpvPort
	if s.position != nil {
		s.position.SetPort(positionPort)
	}
	if s.fpv != nil {
		s.fpv.SetPort(fpvPort)
	}
}

func (s *Server) screenRuntimeStatus() model.ScreenRuntimeStatus {
	status := model.ScreenRuntimeStatus{
		DeviceTargetAddress: s.cfg.DeviceTargetAddress,
		FPVVideo:            s.fpvVideoStatus(),
		ServerTime:          time.Now(),
	}
	if s.position != nil {
		status.Position = s.position.Status()
	}
	if s.fpv != nil {
		status.FPV = s.fpv.Status()
	}
	if s.interference != nil {
		status.Interference = s.interference.ConnectionStatus()
	}
	if s.lingyun != nil {
		status.Lingyun = s.lingyun.Status()
	}
	return status
}

func (s *Server) userSettingsWithRuntimeDefaults(settings model.UserSettings) model.UserSettings {
	settings = model.UserSettingsWithDefaults(settings)
	if settings.PositionTCPPort == nil && s != nil && s.position != nil {
		port := s.position.Status().Port
		settings.PositionTCPPort = &port
	}
	if settings.FPVTCPPort == nil && s != nil && s.fpv != nil {
		port := s.fpv.Status().Port
		settings.FPVTCPPort = &port
	}
	if s != nil && s.store != nil {
		location := s.store.DeviceLocation()
		if location.Valid && location.Point != nil {
			settings.Lingyun = model.LingyunSettingsWithDeviceLocation(settings.Lingyun, location.Point)
		}
	}
	return model.UserSettingsWithDefaults(settings)
}

func (s *Server) pruneIntrusionsByCurrentUserSettings(ctx context.Context) error {
	if s == nil || s.intrusions == nil {
		return nil
	}
	settings := model.UserSettings{}
	if s.userSettings != nil {
		loaded, ok, err := s.userSettings.LoadUser()
		if err != nil {
			return err
		}
		if ok {
			settings = loaded
		}
	}
	return s.pruneIntrusionsByUserSettings(ctx, model.UserSettingsWithDefaults(settings))
}

func (s *Server) maybePruneIntrusionsByCurrentUserSettings(ctx context.Context) error {
	if s == nil || s.intrusions == nil {
		return nil
	}
	now := time.Now()
	s.intrusionPruneMu.Lock()
	if !s.lastIntrusionPruneRun.IsZero() && now.Sub(s.lastIntrusionPruneRun) < intrusionPruneInterval {
		s.intrusionPruneMu.Unlock()
		return nil
	}
	s.intrusionPruneMu.Unlock()

	if err := s.pruneIntrusionsByCurrentUserSettings(ctx); err != nil {
		return err
	}

	s.intrusionPruneMu.Lock()
	s.lastIntrusionPruneRun = now
	s.intrusionPruneMu.Unlock()
	return nil
}

func respondJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Warn("写入 JSON 响应失败", "error", err)
	}
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondErrorCode(w, status, "", message, nil)
}

func respondErrorCode(w http.ResponseWriter, status int, code string, message string, details any) {
	body := map[string]any{
		"message": message,
	}
	if code != "" {
		body["code"] = code
	}
	if details != nil {
		body["details"] = details
	}
	respondJSON(w, status, body)
}

func isScreenEvent(eventType string) bool {
	switch eventType {
	case "screen.position.updated",
		"screen.position.removed",
		"screen.fpv.updated",
		"screen.device_location.updated",
		"screen.strike.updated":
		return true
	default:
		return false
	}
}

func writeEvent(w *bufio.Writer, evt model.Event) error {
	payload, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", evt.Type); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return err
	}
	return w.Flush()
}

func writeFPVVideoSessionEvent(w *bufio.Writer, eventType string, payload any) error {
	return writeEvent(w, model.Event{
		Type:    eventType,
		Time:    time.Now(),
		Payload: payload,
	})
}

func writeComment(w *bufio.Writer, value string) error {
	if _, err := fmt.Fprintf(w, ": %s\n\n", value); err != nil {
		return err
	}
	return w.Flush()
}
