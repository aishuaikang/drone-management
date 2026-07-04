package httpapi

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
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
	"drone-management/internal/store"
)

func TestScreenRoutes(t *testing.T) {
	state := store.New(10, 10)
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	state.AddPosition(model.ScreenPositionTarget{
		Serial:    "SN",
		Model:     "RID",
		Source:    "RID",
		FirstSeen: now,
		LastSeen:  now,
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "RID",
			ReceivedAt: now,
			Serial:     "SN",
			Model:      "RID",
		},
	})
	state.AddFPV(model.ScreenFPVTarget{
		Frequency:  5750,
		RSSI:       98,
		SignalType: "FPV",
		Valid:      true,
		Format:     "ascii-name",
		FirstSeen:  now,
		LastSeen:   now,
		LastRecord: model.ScreenFPVLastRecord{
			Format:     "ascii-name",
			ReceivedAt: now,
			Frequency:  5750,
			RSSI:       98,
			SignalType: "FPV",
			Valid:      true,
		},
	})

	s := newTestServer(t, state)
	tests := []struct {
		name string
		path string
	}{
		{name: "status", path: "/api/v1/screen/status"},
		{name: "positions", path: "/api/v1/screen/positions"},
		{name: "fpv", path: "/api/v1/screen/fpv"},
		{name: "device location", path: "/api/v1/screen/device-location"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			s.server.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestClientDisconnectErrorDetection(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "windows wsasend abort",
			err:  errors.New("write tcp 127.0.0.1:18080->127.0.0.1:58495: wsasend: An established connection was aborted by the software in your host machine."),
			want: true,
		},
		{name: "broken pipe", err: errors.New("write: broken pipe"), want: true},
		{name: "context canceled", err: context.Canceled, want: true},
		{name: "real write error", err: errors.New("json encode failed"), want: false},
		{name: "nil", err: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isClientDisconnectError(tt.err); got != tt.want {
				t.Fatalf("isClientDisconnectError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestLicenseStatusReturnsCurrentDeviceSNWhenMissing(t *testing.T) {
	deviceSN := "drone-management-001A2B3C4D5E"
	s := newTestServerWithLicense(t, filepath.Join(t.TempDir(), "license.lic"), deviceSN)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/license/status", nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body model.LicenseInfo
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if body.Valid || body.DeviceSN != deviceSN || body.Code != "license_not_found" {
		t.Fatalf("body = %#v, want missing license status with current SN", body)
	}
}

func TestUploadLicenseActivatesStatus(t *testing.T) {
	deviceSN := "drone-management-001A2B3C4D5E"
	path := filepath.Join(t.TempDir(), "license.lic")
	s := newTestServerWithLicense(t, path, deviceSN)
	raw := generateHTTPTestLicense(t, deviceSN, 24*time.Hour, "customer", time.Now())

	req := newMultipartRequest(t, "/api/v1/license/upload", "file", "license.lic", raw, nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var upload model.LicenseUploadResponse
	if err := json.NewDecoder(rec.Body).Decode(&upload); err != nil {
		t.Fatalf("Decode(upload) error = %v", err)
	}
	if !upload.License.Valid || upload.License.DeviceSN != deviceSN || upload.Message == "" {
		t.Fatalf("upload = %#v, want valid license response", upload)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/license/status", nil)
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var status model.LicenseInfo
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("Decode(status) error = %v", err)
	}
	if !status.Valid || status.DeviceSN != deviceSN || status.Customer != "customer" {
		t.Fatalf("status = %#v, want activated license", status)
	}
}

func TestUploadLicenseRejectsMissingFile(t *testing.T) {
	s := newTestServerWithLicense(t, filepath.Join(t.TempDir(), "license.lic"), "drone-management-001A2B3C4D5E")
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("ignored", "value"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/license/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if payload["code"] != "invalid_request" {
		t.Fatalf("payload = %#v, want invalid_request", payload)
	}
}

func TestUploadLicenseRejectsSNMismatch(t *testing.T) {
	deviceSN := "drone-management-001A2B3C4D5E"
	s := newTestServerWithLicense(t, filepath.Join(t.TempDir(), "license.lic"), deviceSN)
	raw := generateHTTPTestLicense(t, "drone-management-OTHER", 24*time.Hour, "customer", time.Now())

	req := newMultipartRequest(t, "/api/v1/license/upload", "file", "license.lic", raw, nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Code    string            `json:"code"`
		Details model.LicenseInfo `json:"details"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if payload.Code != "license_sn_mismatch" || payload.Details.DeviceSN != deviceSN {
		t.Fatalf("payload = %#v, want mismatch with current SN details", payload)
	}
}

func TestUploadLicenseRejectsInvalidLicense(t *testing.T) {
	s := newTestServerWithLicense(t, filepath.Join(t.TempDir(), "license.lic"), "drone-management-001A2B3C4D5E")

	req := newMultipartRequest(t, "/api/v1/license/upload", "file", "license.lic", []byte("not-base64"), nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if payload["code"] != "license_invalid" {
		t.Fatalf("payload = %#v, want license_invalid", payload)
	}
}

func TestUploadLicenseReturnsServiceUnavailableWhenDeviceSNMissing(t *testing.T) {
	s := newTestServerWithLicense(t, filepath.Join(t.TempDir(), "license.lic"), "")

	req := newMultipartRequest(t, "/api/v1/license/upload", "file", "license.lic", []byte("not-base64"), nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if payload["code"] != "device_sn_missing" {
		t.Fatalf("payload = %#v, want device_sn_missing", payload)
	}
}

func TestLicenseRequiredBlocksBusinessAPI(t *testing.T) {
	deviceSN := "drone-management-001A2B3C4D5E"
	s := newTestServerWithLicense(t, filepath.Join(t.TempDir(), "license.lic"), deviceSN)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/screen/status", nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Code    string            `json:"code"`
		Details model.LicenseInfo `json:"details"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if payload.Code != "license_required" || payload.Details.Code != "license_not_found" || payload.Details.DeviceSN != deviceSN {
		t.Fatalf("payload = %#v, want license_required with missing license details", payload)
	}
}

func TestLicenseEndpointsBypassLicenseRequired(t *testing.T) {
	deviceSN := "drone-management-001A2B3C4D5E"
	s := newTestServerWithLicense(t, filepath.Join(t.TempDir(), "license.lic"), deviceSN)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/license/status", nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestBusinessAPIAllowedAfterLicenseUpload(t *testing.T) {
	deviceSN := "drone-management-001A2B3C4D5E"
	path := filepath.Join(t.TempDir(), "license.lic")
	s := newTestServerWithLicense(t, path, deviceSN)
	raw := generateHTTPTestLicense(t, deviceSN, 24*time.Hour, "customer", time.Now())
	uploadReq := newMultipartRequest(t, "/api/v1/license/upload", "file", "license.lic", raw, nil)
	uploadRec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body = %s", uploadRec.Code, uploadRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/screen/status", nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestOfflineMapUploadAndTiles(t *testing.T) {
	dir := t.TempDir()
	s := newTestServerWithOfflineMap(t,
		store.New(10, 10),
		filepath.Join(dir, "map"),
	)

	var zipBody bytes.Buffer
	zipWriter := zip.NewWriter(&zipBody)
	file, err := zipWriter.Create("root/dt/12/345/678.jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("tile")); err != nil {
		t.Fatal(err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	req := newMultipartRequest(
		t,
		"/api/v1/offline-map/upload",
		"file",
		"map.zip",
		zipBody.Bytes(),
		map[string]string{"keepBackup": "true"},
	)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("map upload status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var uploadResponse model.OfflineMapUploadResponse
	if err := json.NewDecoder(rec.Body).Decode(&uploadResponse); err != nil {
		t.Fatalf("decode map upload response: %v", err)
	}
	if !uploadResponse.Map.Available {
		t.Fatalf("map available = false, response = %+v", uploadResponse)
	}
	if !slices.ContainsFunc(uploadResponse.Logs, func(log model.OfflineMapUploadLog) bool {
		return log.Stage == "done" && log.Status == "success"
	}) {
		t.Fatalf("upload logs missing done stage: %+v", uploadResponse.Logs)
	}

	req = httptest.NewRequest(http.MethodGet, "/map/dt/12/345/678.jpg", nil)
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "tile" {
		t.Fatalf("tile status = %d, body = %q", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/map/dt/12/345/missing.jpg", nil)
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing tile status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestScreenStatusIncludesFPVVideoPlaybackURL(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	s.cfg.FPVVideo.RTSPURL = "rtsp://192.168.100.106:554/live/1_1"
	s.fpvVideo = nil
	s.fpvVideo = newTestFPVVideo(s.cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/screen/status", nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body model.ScreenRuntimeStatus
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.FPVVideo.Enabled {
		t.Fatalf("fpv video should be enabled: %#v", body.FPVVideo)
	}
	if body.ServerTime.IsZero() {
		t.Fatalf("server time should be included: %#v", body)
	}
	if body.FPVVideo.PlaybackURL != "/api/v1/screen/fpv-video/whep" || body.FPVVideo.PlaybackType != "whep" {
		t.Fatalf("playback url = %q", body.FPVVideo.PlaybackURL)
	}
	if body.FPVVideo.Active {
		t.Fatalf("fpv video should not be active: %#v", body.FPVVideo)
	}
}

func TestScreenStatusIncludesActiveFPVVideoSession(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	s.cfg.FPVVideo.RTSPURL = "rtsp://192.168.100.106:554/live/1_1"
	s.fpvVideo = newTestFPVVideo(s.cfg)
	sessionID, _, ok := s.tryBeginFPVVideoSession(1360)
	if !ok {
		t.Fatal("session should start")
	}
	defer s.finishFPVVideoSession(sessionID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/screen/status", nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body model.ScreenRuntimeStatus
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.FPVVideo.Active || body.FPVVideo.ActiveFrequency != 1360 || body.FPVVideo.ActiveSince == nil {
		t.Fatalf("fpv video active status = %#v", body.FPVVideo)
	}
}

func TestManualDeviceLocationRoutes(t *testing.T) {
	state := store.New(10, 10)
	s := newTestServer(t, state)

	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/screen/device-location/manual",
		strings.NewReader(`{"point":{"latitude":39.9,"longitude":116.3}}`),
	)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("put status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body model.ScreenDeviceLocationResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode put response: %v", err)
	}
	if body.Source != "manual" || !body.Valid || body.Point == nil {
		t.Fatalf("put response = %#v", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/screen/device-location", nil)
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body = model.ScreenDeviceLocationResponse{}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if body.Source != "manual" || body.Point == nil || body.Point.Latitude != 39.9 {
		t.Fatalf("get response = %#v", body)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/screen/device-location/manual", nil)
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body = model.ScreenDeviceLocationResponse{}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if body.Source != "none" || body.Valid {
		t.Fatalf("delete response = %#v", body)
	}
}

func TestManualDeviceLocationRouteRejectsInvalidPoint(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/screen/device-location/manual",
		strings.NewReader(`{"point":{"latitude":91,"longitude":116.3}}`),
	)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestManualDeviceLocationRoutePersistsFile(t *testing.T) {
	state := store.New(10, 10)
	s := newTestServer(t, state)
	path := filepath.Join(t.TempDir(), "manual-device-location.json")
	s.cfg.ManualLocationPath = path

	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/screen/device-location/manual",
		strings.NewReader(`{"point":{"latitude":39.9,"longitude":116.3}}`),
	)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("put status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("manual location file missing: %v", err)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/screen/device-location/manual", nil)
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manual location file after delete = %v, want not exist", err)
	}
}

func TestUpdateScreenTCPPortsRoute(t *testing.T) {
	state := store.New(10, 10)
	s := newTestServer(t, state)
	settingsStore := &memoryUserSettingsStore{}
	s.userSettings = settingsStore

	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/screen/tcp-ports",
		strings.NewReader(`{"positionTCPPort":11007,"fpvTCPPort":11005}`),
	)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body model.ScreenRuntimeStatus
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Position.Port != 11007 || body.FPV.Port != 11005 {
		t.Fatalf("ports = %#v", body)
	}
	if settingsStore.settings.PositionTCPPort == nil || *settingsStore.settings.PositionTCPPort != 11007 ||
		settingsStore.settings.FPVTCPPort == nil || *settingsStore.settings.FPVTCPPort != 11005 {
		t.Fatalf("saved settings = %#v", settingsStore.settings)
	}
}

func TestUpdateScreenTCPPortsRouteRejectsInvalidPorts(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	tests := []struct {
		name string
		body string
	}{
		{name: "out of range", body: `{"positionTCPPort":0,"fpvTCPPort":11005}`},
		{name: "duplicate", body: `{"positionTCPPort":11005,"fpvTCPPort":11005}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/v1/screen/tcp-ports", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			s.server.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestUserSettingsRoutesNormalizeWhitelist(t *testing.T) {
	state := store.New(10, 10)
	s := newTestServer(t, state)
	settingsStore := &memoryUserSettingsStore{}
	s.userSettings = settingsStore

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/settings", nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var defaults model.UserSettings
	if err := json.NewDecoder(rec.Body).Decode(&defaults); err != nil {
		t.Fatalf("decode defaults: %v", err)
	}
	if defaults.IntrusionRetentionDays == nil || *defaults.IntrusionRetentionDays != model.DefaultIntrusionRetentionDays {
		t.Fatalf("default retention = %#v", defaults.IntrusionRetentionDays)
	}
	if defaults.PositionExpireSeconds == nil || *defaults.PositionExpireSeconds != model.DefaultPositionExpireSeconds {
		t.Fatalf("default position expire seconds = %#v", defaults.PositionExpireSeconds)
	}
	if defaults.WarningZoneEnabled == nil || *defaults.WarningZoneEnabled {
		t.Fatalf("default warning zone enabled = %#v, want false", defaults.WarningZoneEnabled)
	}
	if defaults.WarningZoneRadiusMeters == nil || *defaults.WarningZoneRadiusMeters != model.DefaultWarningZoneRadiusMeters {
		t.Fatalf("default warning zone radius = %#v", defaults.WarningZoneRadiusMeters)
	}

	body := `{
		"intrusionRetentionDays": 0,
		"screenTitle": "  机场大屏  ",
		"positionExpireSeconds": 3,
		"warningZoneEnabled": true,
		"warningZoneRadiusMeters": 1200.6,
			"whitelist": [
				{"serial":" DJI-001 ","model":" Mini 4 Pro ","source":" manual "},
				{"serial":"dji-001","model":"Duplicate"},
				{"serial":" 447e5681 ","model":"DJI-Drone","source":"dji_O:4"},
				{"serial":" RID-002 "}
			]
		}`
	req = httptest.NewRequest(http.MethodPut, "/api/v1/user/settings", strings.NewReader(body))
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("put status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var saved model.UserSettings
	if err := json.NewDecoder(rec.Body).Decode(&saved); err != nil {
		t.Fatalf("decode saved: %v", err)
	}
	if saved.IntrusionRetentionDays == nil || *saved.IntrusionRetentionDays != 0 {
		t.Fatalf("retention = %#v, want 0", saved.IntrusionRetentionDays)
	}
	if saved.ScreenTitle != "机场大屏" {
		t.Fatalf("screen title = %q, want trimmed title", saved.ScreenTitle)
	}
	if saved.PositionExpireSeconds == nil || *saved.PositionExpireSeconds != 3 {
		t.Fatalf("position expire seconds = %#v, want 3", saved.PositionExpireSeconds)
	}
	if s.store.PositionTTL() != 3*time.Second {
		t.Fatalf("store position ttl = %s, want 3s", s.store.PositionTTL())
	}
	if saved.WarningZoneEnabled == nil || !*saved.WarningZoneEnabled {
		t.Fatalf("warning zone enabled = %#v, want true", saved.WarningZoneEnabled)
	}
	if saved.WarningZoneRadiusMeters == nil || *saved.WarningZoneRadiusMeters != 1201 {
		t.Fatalf("warning zone radius = %#v, want 1201", saved.WarningZoneRadiusMeters)
	}
	if len(saved.WarningZones) != 0 {
		t.Fatalf("warning zones = %#v, want no fixed-center zones", saved.WarningZones)
	}
	if len(saved.Whitelist) != 2 {
		t.Fatalf("whitelist = %#v, want 2 items", saved.Whitelist)
	}
	if saved.Whitelist[0].Serial != "DJI-001" || saved.Whitelist[0].Model != "Mini 4 Pro" || saved.Whitelist[0].Source != "manual" || saved.Whitelist[0].CreatedAt.IsZero() {
		t.Fatalf("first whitelist item = %#v", saved.Whitelist[0])
	}
	if settingsStore.settings.Whitelist[1].Serial != "RID-002" {
		t.Fatalf("stored whitelist = %#v", settingsStore.settings.Whitelist)
	}
}

func TestUserSettingsRoutesSaveLingyunAndApplyService(t *testing.T) {
	state := store.New(10, 10)
	s := newTestServer(t, state)
	settingsStore := &memoryUserSettingsStore{}
	lingyunSvc := &memoryLingyunService{}
	s.userSettings = settingsStore
	s.lingyun = lingyunSvc

	body := `{
		"lingyun": {
			"enabled": true,
			"broker": "tcp://127.0.0.1:1883",
			"username": "user",
			"password": "plain-secret",
			"providerCode": "DPTEST",
			"devices": [
				{"type":"aoa","enabled":true,"deviceId":"AOA01","deviceName":"AOA"},
				{"type":"dcd","enabled":true,"deviceId":"DCD01","deviceName":"DCD"},
				{"type":"rid","enabled":true,"deviceId":"RID01","deviceName":"RID"}
			]
		}
	}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/user/settings", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var saved model.UserSettings
	if err := json.NewDecoder(rec.Body).Decode(&saved); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if saved.Lingyun.Password != "plain-secret" {
		t.Fatalf("password = %q, want plain-secret", saved.Lingyun.Password)
	}
	if !strings.HasPrefix(saved.Lingyun.ClientID, model.DefaultLingyunClientIDPrefix) {
		t.Fatalf("clientId = %q, want generated prefix %q", saved.Lingyun.ClientID, model.DefaultLingyunClientIDPrefix)
	}
	if settingsStore.settings.Lingyun.ProviderCode != "DPTEST" {
		t.Fatalf("saved Lingyun = %#v", settingsStore.settings.Lingyun)
	}
	if !lingyunSvc.applied.Lingyun.Enabled || lingyunSvc.applied.Lingyun.ProviderCode != "DPTEST" {
		t.Fatalf("applied settings = %#v", lingyunSvc.applied.Lingyun)
	}
}

func TestUserSettingsRouteAppliesLingyunRuntimeIdentityAndLocationWithoutOverridingCustomDeviceID(t *testing.T) {
	state := store.New(10, 10)
	state.SetManualDeviceLocationAt(model.GeoPoint{Latitude: 39.1234, Longitude: 116.5678}, time.Now())
	s := newTestServer(t, state)
	s.userSettings = &memoryUserSettingsStore{
		ok: true,
		settings: model.UserSettings{
			Lingyun: model.LingyunSettings{
				ClientID: "client-1",
				Devices: []model.LingyunDeviceSettings{
					{
						Type:            model.LingyunDeviceAOA,
						Enabled:         true,
						DeviceID:        "old-device",
						DeviceLongitude: 1,
						DeviceLatitude:  2,
						DeviceSpec: model.LingyunDeviceSpec{
							DevSN: "old-sn",
						},
					},
				},
			},
		},
	}
	s.lingyun = &memoryLingyunService{
		status: model.LingyunStatus{ClientID: "runtime-client"},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/settings", nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body model.UserSettings
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Lingyun.Devices) != 4 {
		t.Fatalf("devices = %#v, want 4", body.Lingyun.Devices)
	}
	if body.Lingyun.ClientID != "runtime-client" {
		t.Fatalf("clientId = %q, want runtime-client", body.Lingyun.ClientID)
	}
	for _, device := range body.Lingyun.Devices {
		if device.DeviceLongitude != 116.5678 || device.DeviceLatitude != 39.1234 {
			t.Fatalf("device %s location = %.4f/%.4f, want 116.5678/39.1234", device.Type, device.DeviceLongitude, device.DeviceLatitude)
		}
	}
	if identity := model.NewLingyunDeviceSN(); identity != "" {
		for _, device := range body.Lingyun.Devices {
			if device.Type == model.LingyunDeviceAOA {
				if device.DeviceID != "old-device" || device.DeviceSpec.DevSN != "old-sn" {
					t.Fatalf("AOA custom identity = %q/%q", device.DeviceID, device.DeviceSpec.DevSN)
				}
				continue
			}
			if device.DeviceID != identity || device.DeviceSpec.DevSN != identity {
				t.Fatalf("device %s identity = %q/%q, want %q", device.Type, device.DeviceID, device.DeviceSpec.DevSN, identity)
			}
		}
	}
}

func TestUserSettingsRouteRejectsInvalidPositionExpireSeconds(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	s.userSettings = &memoryUserSettingsStore{}

	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/user/settings",
		strings.NewReader(`{"positionExpireSeconds":0}`),
	)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestUserSettingsRoutePreservesUnattendedConfig(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	settingsStore := &memoryUserSettingsStore{
		ok: true,
		settings: model.UserSettings{
			ScreenStrikeUnattended: &model.ScreenStrikeUnattendedConfig{
				Enabled:         true,
				ChannelIDs:      []string{"io1"},
				DurationSeconds: 60,
			},
		},
	}
	s.userSettings = settingsStore

	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/user/settings",
		strings.NewReader(`{"screenTitle":"机场","screenStrikeUnattended":{"enabled":false}}`),
	)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := settingsStore.settings.ScreenStrikeUnattended; got == nil || !got.Enabled || got.DurationSeconds != 60 || len(got.ChannelIDs) != 1 || got.ChannelIDs[0] != "io1" {
		t.Fatalf("unattended config should be preserved, got %#v", got)
	}
}

func TestUserSettingsRouteRejectsInvalidWarningZoneRadius(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	s.userSettings = &memoryUserSettingsStore{}

	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/user/settings",
		strings.NewReader(`{"warningZoneRadiusMeters":9}`),
	)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestUserSettingsRouteMigratesLegacyWarningZones(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	s.userSettings = &memoryUserSettingsStore{}

	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/user/settings",
		strings.NewReader(`{"warningZones":[{"id":"legacy","center":{"latitude":39.9,"longitude":116.4},"radiusMeters":800.4}]}`),
	)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var saved model.UserSettings
	if err := json.NewDecoder(rec.Body).Decode(&saved); err != nil {
		t.Fatalf("decode saved: %v", err)
	}
	if saved.WarningZoneEnabled == nil || !*saved.WarningZoneEnabled {
		t.Fatalf("warning zone enabled = %#v, want true", saved.WarningZoneEnabled)
	}
	if saved.WarningZoneRadiusMeters == nil || *saved.WarningZoneRadiusMeters != 800 {
		t.Fatalf("warning zone radius = %#v, want 800", saved.WarningZoneRadiusMeters)
	}
	if len(saved.WarningZones) != 0 {
		t.Fatalf("warning zones = %#v, want no fixed-center zones", saved.WarningZones)
	}
}

func TestUserSettingsRouteMergesPartialUpdate(t *testing.T) {
	retentionDays := 7
	expireSeconds := 12
	warningZoneEnabled := true
	warningZoneRadius := 800.0
	settingsStore := &memoryUserSettingsStore{
		ok: true,
		settings: model.UserSettings{
			IntrusionRetentionDays:  &retentionDays,
			ScreenTitle:             "旧标题",
			PositionExpireSeconds:   &expireSeconds,
			WarningZoneEnabled:      &warningZoneEnabled,
			WarningZoneRadiusMeters: &warningZoneRadius,
			Whitelist: []model.WhitelistItem{
				{Serial: "SN-001", Model: "Mini 4 Pro", Source: "manual", CreatedAt: time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)},
			},
		},
	}
	s := newTestServer(t, store.New(10, 10))
	s.userSettings = settingsStore

	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/user/settings",
		strings.NewReader(`{"screenTitle":"新标题"}`),
	)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var saved model.UserSettings
	if err := json.NewDecoder(rec.Body).Decode(&saved); err != nil {
		t.Fatalf("decode saved: %v", err)
	}
	if saved.ScreenTitle != "新标题" {
		t.Fatalf("screen title = %q, want new title", saved.ScreenTitle)
	}
	if saved.IntrusionRetentionDays == nil || *saved.IntrusionRetentionDays != retentionDays {
		t.Fatalf("retention days = %#v, want %d", saved.IntrusionRetentionDays, retentionDays)
	}
	if saved.PositionExpireSeconds == nil || *saved.PositionExpireSeconds != expireSeconds {
		t.Fatalf("position expire seconds = %#v, want %d", saved.PositionExpireSeconds, expireSeconds)
	}
	if saved.WarningZoneEnabled == nil || !*saved.WarningZoneEnabled {
		t.Fatalf("warning zone enabled = %#v, want preserved true", saved.WarningZoneEnabled)
	}
	if saved.WarningZoneRadiusMeters == nil || *saved.WarningZoneRadiusMeters != warningZoneRadius {
		t.Fatalf("warning zone radius = %#v, want %.0f", saved.WarningZoneRadiusMeters, warningZoneRadius)
	}
	if len(saved.Whitelist) != 1 || saved.Whitelist[0].Serial != "SN-001" {
		t.Fatalf("whitelist = %#v, want preserved item", saved.Whitelist)
	}
}

func TestInterferenceChannelRoutes(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/interference/channels", nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var list model.ListResponse[model.InterferenceChannel]
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Items) != 8 || list.Items[0].ID != "io1" || list.Items[0].Output != 1 || list.Items[3].Reserved {
		t.Fatalf("channels = %#v", list.Items)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/interference/channels/io1/state", strings.NewReader(`{"enabled":true}`))
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("timed strike channel status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestScreenStrikeRoutes(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/screen/strike", nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var state model.ScreenStrikeState
	if err := json.NewDecoder(rec.Body).Decode(&state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.Active || len(state.Channels) != 8 {
		t.Fatalf("initial state = %#v", state)
	}

	req = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/screen/strike",
		strings.NewReader(`{"enabled":true,"channelIds":["io1","io2"],"durationSeconds":10}`),
	)
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("post status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response model.ScreenStrikeResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.State.Active || len(response.State.ChannelIDs) != 2 {
		t.Fatalf("started state = %#v", response.State)
	}

	req = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/screen/strike",
		strings.NewReader(`{"enabled":true,"channelIds":["io1"],"durationSeconds":1}`),
	)
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid duration status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestScreenStrikeUnattendedRoute(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	settingsStore := &memoryUserSettingsStore{}
	s.userSettings = settingsStore
	s.interference.SetUserSettingsStore(settingsStore)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/screen/strike/unattended",
		strings.NewReader(`{"enabled":true,"channelIds":["io1"],"durationSeconds":10}`),
	)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response model.ScreenStrikeResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.State.Unattended.Enabled || response.State.Unattended.DurationSeconds != 10 {
		t.Fatalf("unattended state = %#v", response.State.Unattended)
	}
	if settingsStore.settings.ScreenStrikeUnattended == nil || !settingsStore.settings.ScreenStrikeUnattended.Enabled {
		t.Fatalf("persisted unattended settings = %#v", settingsStore.settings.ScreenStrikeUnattended)
	}

	req = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/screen/strike",
		strings.NewReader(`{"enabled":true,"channelIds":["io1"],"durationSeconds":10}`),
	)
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("manual while unattended status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var errorBody map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&errorBody); err != nil {
		t.Fatalf("decode manual error: %v", err)
	}
	if errorBody["code"] != "strike_unattended_active" {
		t.Fatalf("manual error body = %#v", errorBody)
	}

	req = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/screen/strike/unattended",
		strings.NewReader(`{"enabled":false}`),
	)
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if settingsStore.settings.ScreenStrikeUnattended == nil || settingsStore.settings.ScreenStrikeUnattended.Enabled {
		t.Fatalf("persisted disabled settings = %#v", settingsStore.settings.ScreenStrikeUnattended)
	}
}

func TestInterferenceReportRoutes(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	reportStore := &memoryInterferenceReportStore{
		items: []model.InterferenceReport{
			{
				InterferenceReportSummary: model.InterferenceReportSummary{
					ID:                       "running",
					Status:                   model.InterferenceReportStatusRunning,
					OperationType:            model.InterferenceOperationManual,
					StartedAt:                now,
					RequestedDurationSeconds: 10,
					ChannelIDs:               []string{"io1"},
					ChannelLabels:            []string{"433M"},
					ChannelOutputs:           []int{2},
					CreatedAt:                now,
					UpdatedAt:                now,
				},
				Request: model.ScreenStrikeRequest{Enabled: true, ChannelIDs: []string{"io1"}, DurationSeconds: 10},
			},
			{
				InterferenceReportSummary: model.InterferenceReportSummary{
					ID:                       "failed",
					Status:                   model.InterferenceReportStatusFailed,
					OperationType:            model.InterferenceOperationUnattended,
					StartedAt:                now.Add(time.Minute),
					EndedAt:                  ptrTime(now.Add(time.Minute + time.Second)),
					DurationSeconds:          1,
					RequestedDurationSeconds: 10,
					ChannelIDs:               []string{"io2"},
					ChannelLabels:            []string{"1.2G"},
					ChannelOutputs:           []int{3},
					LastError:                "relay failed",
					CreatedAt:                now,
					UpdatedAt:                now,
				},
				Request: model.ScreenStrikeRequest{Enabled: true, ChannelIDs: []string{"io2"}, DurationSeconds: 10},
			},
		},
	}
	s := newTestServer(t, store.New(10, 10))
	s.interferenceReports = reportStore

	req := httptest.NewRequest(http.MethodGet, "/api/v1/interference-reports?limit=1", nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var page model.ListResponse[model.InterferenceReportSummary]
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "failed" || !page.HasMore {
		t.Fatalf("page = %#v", page)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/interference-reports/failed", nil)
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var report model.InterferenceReport
	if err := json.NewDecoder(rec.Body).Decode(&report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.ID != "failed" || report.LastError != "relay failed" {
		t.Fatalf("report = %#v", report)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/interference-reports/running", nil)
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete running status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/interference-reports/failed", nil)
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete failed status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var deleted model.InterferenceReportDeleteResponse
	if err := json.NewDecoder(rec.Body).Decode(&deleted); err != nil {
		t.Fatalf("decode delete: %v", err)
	}
	if deleted.Deleted != 1 {
		t.Fatalf("deleted = %d", deleted.Deleted)
	}
}

func TestIntrusionRoutesPageAndDeleteRecords(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	s := newTestServer(t, store.New(10, 10))
	intrusions := &memoryIntrusionStore{
		items: []model.IntrusionRecord{
			{ID: "new", TargetType: model.IntrusionTargetTypePosition, Serial: "SN-2", FirstSeen: now, LastSeen: now, ArchivedAt: now},
			{ID: "old", TargetType: model.IntrusionTargetTypePosition, Serial: "SN-1", FirstSeen: now.Add(-time.Hour), LastSeen: now.Add(-time.Hour), ArchivedAt: now.Add(-time.Hour)},
		},
	}
	s.intrusions = intrusions
	s.userSettings = &memoryUserSettingsStore{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/intrusions?limit=1&offset=0", nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var page model.ListResponse[model.IntrusionRecord]
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "new" || !page.HasMore || page.NextOffset != 1 {
		t.Fatalf("page = %#v", page)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/intrusions", strings.NewReader(`{"ids":["new","new",""]}`))
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var deleted model.IntrusionDeleteResponse
	if err := json.NewDecoder(rec.Body).Decode(&deleted); err != nil {
		t.Fatalf("decode delete: %v", err)
	}
	if deleted.Deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted.Deleted)
	}
}

func TestIntrusionRoutesFilterRecords(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	s := newTestServer(t, store.New(10, 10))
	s.intrusions = &memoryIntrusionStore{
		items: []model.IntrusionRecord{
			{ID: "match", TargetType: model.IntrusionTargetTypePosition, Model: "Mini 4 Pro", Serial: "SN-2", FirstSeen: now, LastSeen: now, ArchivedAt: now},
			{ID: "other-model", TargetType: model.IntrusionTargetTypePosition, Model: "Mavic 3", Serial: "SN-2", FirstSeen: now, LastSeen: now, ArchivedAt: now},
			{ID: "other-date", TargetType: model.IntrusionTargetTypePosition, Model: "Mini 4 Pro", Serial: "SN-2", FirstSeen: now.AddDate(0, 0, -2), LastSeen: now.AddDate(0, 0, -2), ArchivedAt: now},
			{ID: "other-serial", TargetType: model.IntrusionTargetTypePosition, Model: "Mini 4 Pro", Serial: "SN-1", FirstSeen: now, LastSeen: now, ArchivedAt: now},
		},
	}
	s.userSettings = &memoryUserSettingsStore{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/intrusions?model=mini&serial=sn-2&dateFrom=2026-06-05&dateTo=2026-06-05", nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var page model.ListResponse[model.IntrusionRecord]
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "match" {
		t.Fatalf("filtered page = %#v", page)
	}
}

func TestIntrusionRoutesRejectInvalidInput(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	s.intrusions = &memoryIntrusionStore{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/intrusions?type=fpv", nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid type status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/intrusions", strings.NewReader(`{"ids":[""," "]}`))
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty delete status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestScreenStream(t *testing.T) {
	state := store.New(10, 10)
	s := newTestServer(t, state)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(s.server.Handler)
	defer server.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/screen/stream", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("stream status = %d, body = %s", resp.StatusCode, body)
	}
	reader := bufio.NewReader(resp.Body)
	waitForStream(t, reader, ": connected")

	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	state.AddFPV(model.ScreenFPVTarget{
		Frequency:  5750,
		RSSI:       98,
		SignalType: "FPV",
		Valid:      true,
		Format:     "ascii-name",
		FirstSeen:  now,
		LastSeen:   now,
		LastRecord: model.ScreenFPVLastRecord{
			Format:     "ascii-name",
			ReceivedAt: now,
			Frequency:  5750,
			RSSI:       98,
			SignalType: "FPV",
			Valid:      true,
		},
	})

	waitForStream(t, reader, "event: screen.fpv.updated")
	cancel()
	_ = resp.Body.Close()
}

func TestFPVVideoSessionSendsTuneAndStopsOnDisconnect(t *testing.T) {
	state := store.New(10, 10)
	s := newTestServer(t, state)
	configureFakeFPVVideo(t, s)
	port := freeHTTPTestTCPPort(t)
	s.fpv = fpv.NewService(state, fpv.Options{
		Host:           "127.0.0.1",
		Port:           port,
		CommandTimeout: time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.fpv.Run(ctx)
	waitFor(t, time.Second, func() bool { return s.fpv.Status().Listening })

	deviceConn, err := net.Dial("tcp", s.fpv.Address())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer deviceConn.Close()
	waitFor(t, time.Second, func() bool { return s.fpv.Status().SourceConnected })
	commands := respondOKToFPVCommands(t, deviceConn)

	httpServer := httptest.NewServer(s.server.Handler)
	defer httpServer.Close()

	respCh := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := http.Get(httpServer.URL + "/api/v1/screen/fpv-video/session?frequency=1360")
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	if got := readCommand(t, commands); got != "AT+F=1360\r\n" {
		t.Fatalf("start command = %q, want %q", got, "AT+F=1360\r\n")
	}

	var resp *http.Response
	select {
	case resp = <-respCh:
	case err := <-errCh:
		t.Fatalf("GET session error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("GET session did not return headers")
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("status = %d", resp.StatusCode)
	}
	readHTTPStreamUntil(t, resp.Body, "event: "+fpvVideoSessionReadyType)

	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}
	if got := readCommand(t, commands); got != "AT+F=0\r\n" {
		t.Fatalf("stop command = %q, want %q", got, "AT+F=0\r\n")
	}
}

func TestFPVVideoSessionOnlyActiveSessionCanFinish(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))

	first, _, ok := s.tryBeginFPVVideoSession(1360)
	if !ok {
		t.Fatal("first session should start")
	}
	if _, _, ok := s.tryBeginFPVVideoSession(1400); ok {
		t.Fatal("second session should not start while first is active")
	}
	active, frequency, activeSince := s.fpvVideoSessionStatus()
	if !active || frequency != 1360 || activeSince.IsZero() {
		t.Fatalf("active session status = active:%v frequency:%d since:%v", active, frequency, activeSince)
	}
	if !s.finishFPVVideoSession(first) {
		t.Fatal("first session should finish")
	}
	active, frequency, _ = s.fpvVideoSessionStatus()
	if active || frequency != 0 {
		t.Fatalf("session should be inactive after finish: active:%v frequency:%d", active, frequency)
	}
	second, _, ok := s.tryBeginFPVVideoSession(1400)
	if !ok {
		t.Fatal("second session should start after first finishes")
	}
	if !s.finishFPVVideoSession(second) {
		t.Fatal("second session should finish")
	}
}

func TestFPVVideoSessionRejectsConcurrentViewer(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	configureFakeFPVVideo(t, s)
	first, _, ok := s.tryBeginFPVVideoSession(1360)
	if !ok {
		t.Fatal("first session should start")
	}
	defer s.finishFPVVideoSession(first)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/screen/fpv-video/session?frequency=1400", nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: "+fpvVideoSessionErrorType) || !strings.Contains(body, `"code":"`+fpvVideoSessionBusyCode+`"`) {
		t.Fatalf("busy event not returned: %s", body)
	}
	active, frequency, _ := s.fpvVideoSessionStatus()
	if !active || frequency != 1360 {
		t.Fatalf("first session should remain active: active:%v frequency:%d", active, frequency)
	}
}

func TestFPVVideoWHEPRequiresActiveSessionToken(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	upstream, calls := newWHEPTestServer(t)
	s.cfg.FPVVideo = config.FPVVideoConfig{
		WHEPURL: upstream.URL + "/fpv/whep",
	}
	s.fpvVideo = newTestFPVVideo(s.cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/screen/fpv-video/whep", strings.NewReader("offer"))
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status without token = %d, body = %s", rec.Code, rec.Body.String())
	}
	if calls.Len() != 0 {
		t.Fatalf("upstream should not be called without token: %#v", calls)
	}

	sessionID, token, ok := s.tryBeginFPVVideoSession(1360)
	if !ok {
		t.Fatal("session should start")
	}
	defer s.finishFPVVideoSession(sessionID)
	if err := s.fpvVideo.Restart(context.Background()); err != nil {
		t.Fatalf("restart fpv video: %v", err)
	}

	req = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/screen/fpv-video/whep?session="+token,
		strings.NewReader("offer"),
	)
	req.Header.Set("Content-Type", "application/sdp")
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status with token = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "answer" {
		t.Fatalf("body = %q, want answer", rec.Body.String())
	}
	wantLocation := "/api/v1/screen/fpv-video/whep/session?session=" + token
	if rec.Header().Get("Location") != wantLocation {
		t.Fatalf("location = %q, want %q", rec.Header().Get("Location"), wantLocation)
	}

	req = httptest.NewRequest(http.MethodDelete, wantLocation, nil)
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}

	gotCalls := calls.All()
	if len(gotCalls) != 3 {
		t.Fatalf("upstream calls = %#v, want OPTIONS readiness, POST proxy, DELETE proxy", gotCalls)
	}
	if gotCalls[1] != "POST /fpv/whep offer" {
		t.Fatalf("proxied call = %q", gotCalls[1])
	}
	if gotCalls[2] != "DELETE /fpv/whep/session " {
		t.Fatalf("proxied delete = %q", gotCalls[2])
	}
}

func TestFPVVideoPlayerAssetsAreNotExposed(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	for _, path := range []string{
		"/api/v1/screen/fpv-video/player",
		"/api/v1/screen/fpv-video/reader.js",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, body = %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestFPVVideoSessionCloseReleasesActiveViewer(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	configureFakeFPVVideo(t, s)
	deviceConn, commands, cancelFPV := connectFPVCommandDevice(t, s, time.Second, true)
	defer cancelFPV()
	defer deviceConn.Close()

	sessionID, token, ok := s.tryBeginFPVVideoSession(1360)
	if !ok {
		t.Fatal("session should start")
	}
	defer s.finishFPVVideoSession(sessionID)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/screen/fpv-video/session/close?session="+token, nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := readCommand(t, commands); got != "AT+F=0\r\n" {
		t.Fatalf("stop command = %q, want %q", got, "AT+F=0\r\n")
	}
	active, frequency, _ := s.fpvVideoSessionStatus()
	if active || frequency != 0 {
		t.Fatalf("session should be released after close: active:%v frequency:%d", active, frequency)
	}
}

func TestFPVVideoSessionCloseReleasesAndRecordsFailedWhenStopFails(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	configureFakeFPVVideo(t, s)
	recordDir := t.TempDir()
	s.cfg.FPVVideo.RecordDir = recordDir
	recordStore := &memoryFPVVideoRecordStore{}
	s.fpvRecords = recordStore
	deviceConn, commands, cancelFPV := connectFPVCommandDevice(t, s, 25*time.Millisecond, false)
	defer cancelFPV()
	defer deviceConn.Close()

	sessionID, token, ok := s.tryBeginFPVVideoSessionWithRecording(1360, model.ScreenFPVTarget{
		ID:         "fpv-1",
		Frequency:  1360,
		RSSI:       -42,
		SignalType: "FPV",
	}, fpvVideoRecordingSpec{
		ID:       "record-stop-failed",
		BasePath: filepath.Join(recordDir, "record-stop-failed_%path_%s"),
		FileGlob: "record-stop-failed_*.mp4",
	})
	if !ok {
		t.Fatal("session should start")
	}
	_ = sessionID

	req := httptest.NewRequest(http.MethodPost, "/api/v1/screen/fpv-video/session/close?session="+token, nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := readCommand(t, commands); got != "AT+F=0\r\n" {
		t.Fatalf("stop command = %q, want %q", got, "AT+F=0\r\n")
	}
	active, frequency, _ := s.fpvVideoSessionStatus()
	if active || frequency != 0 {
		t.Fatalf("session should be released after stop failure: active:%v frequency:%d", active, frequency)
	}
	if len(recordStore.items) != 1 {
		t.Fatalf("failed record should be saved: %#v", recordStore.items)
	}
	if recordStore.items[0].Status != model.FPVVideoRecordStatusFailed || recordStore.items[0].Error == "" {
		t.Fatalf("failed record metadata = %#v", recordStore.items[0])
	}
}

func TestStopActiveFPVVideoSessionReleasesAndRecordsFailedWhenStopFails(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	configureFakeFPVVideo(t, s)
	recordDir := t.TempDir()
	s.cfg.FPVVideo.RecordDir = recordDir
	recordStore := &memoryFPVVideoRecordStore{}
	s.fpvRecords = recordStore
	deviceConn, commands, cancelFPV := connectFPVCommandDevice(t, s, 25*time.Millisecond, false)
	defer cancelFPV()
	defer deviceConn.Close()

	sessionID, _, ok := s.tryBeginFPVVideoSessionWithRecording(1360, model.ScreenFPVTarget{
		ID:         "fpv-1",
		Frequency:  1360,
		RSSI:       -42,
		SignalType: "FPV",
	}, fpvVideoRecordingSpec{
		ID:       "record-active-stop-failed",
		BasePath: filepath.Join(recordDir, "record-active-stop-failed_%path_%s"),
		FileGlob: "record-active-stop-failed_*.mp4",
	})
	if !ok || sessionID == 0 {
		t.Fatal("session should start")
	}

	if err := s.stopActiveFPVVideoSession(context.Background()); err == nil {
		t.Fatal("stopActiveFPVVideoSession() should return stop error")
	}
	if got := readCommand(t, commands); got != "AT+F=0\r\n" {
		t.Fatalf("stop command = %q, want %q", got, "AT+F=0\r\n")
	}
	active, frequency, _ := s.fpvVideoSessionStatus()
	if active || frequency != 0 {
		t.Fatalf("session should be released after active stop failure: active:%v frequency:%d", active, frequency)
	}
	if len(recordStore.items) != 1 {
		t.Fatalf("failed record should be saved: %#v", recordStore.items)
	}
	if recordStore.items[0].Status != model.FPVVideoRecordStatusFailed || recordStore.items[0].Error == "" {
		t.Fatalf("failed record metadata = %#v", recordStore.items[0])
	}
}

func TestFPVVideoSessionStartupFailureReleasesActiveWhenStopFails(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	configureFailingFPVVideo(t, s)
	port := freeHTTPTestTCPPort(t)
	s.fpv = fpv.NewService(s.store, fpv.Options{
		Host:           "127.0.0.1",
		Port:           port,
		CommandTimeout: 25 * time.Millisecond,
	})
	ctx, cancelFPV := context.WithCancel(context.Background())
	defer cancelFPV()
	go s.fpv.Run(ctx)
	waitFor(t, time.Second, func() bool { return s.fpv.Status().Listening })

	deviceConn, err := net.Dial("tcp", s.fpv.Address())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer deviceConn.Close()
	waitFor(t, time.Second, func() bool { return s.fpv.Status().SourceConnected })
	commands := respondOKToFirstFPVCommand(t, deviceConn)

	requestCtx, cancelRequest := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelRequest()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/screen/fpv-video/session?frequency=1360",
		nil,
	).WithContext(requestCtx)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: "+fpvVideoSessionErrorType) {
		t.Fatalf("error event not returned: %s", body)
	}
	if got := readCommand(t, commands); got != "AT+F=1360\r\n" {
		t.Fatalf("start command = %q, want %q", got, "AT+F=1360\r\n")
	}
	if got := readCommand(t, commands); got != "AT+F=0\r\n" {
		t.Fatalf("stop command = %q, want %q", got, "AT+F=0\r\n")
	}
	active, frequency, _ := s.fpvVideoSessionStatus()
	if active || frequency != 0 {
		t.Fatalf("session should be released after startup failure: active:%v frequency:%d", active, frequency)
	}
}

func TestFPVVideoRecordRoutesListFileAndDelete(t *testing.T) {
	state := store.New(10, 10)
	s := newTestServer(t, state)
	recordDir := t.TempDir()
	s.cfg.FPVVideo.RecordDir = recordDir
	videoPath := filepath.Join(recordDir, "record.mp4")
	if err := os.WriteFile(videoPath, []byte("0123456789"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	recordStore := &memoryFPVVideoRecordStore{
		items: []model.FPVVideoRecord{
			{
				ID:              "record-1",
				TargetID:        "fpv-1",
				Frequency:       1360,
				RSSI:            -40,
				SignalType:      "FPV",
				DeviceSN:        "SN-1",
				StartedAt:       now,
				EndedAt:         now.Add(2 * time.Second),
				DurationSeconds: 2,
				Status:          model.FPVVideoRecordStatusReady,
				FileName:        "record.mp4",
				FileSizeBytes:   10,
			},
		},
	}
	s.fpvRecords = recordStore

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fpv-video-records?signalType=fp&deviceSn=sn", nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var list model.ListResponse[model.FPVVideoRecord]
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].FileURL != "/api/v1/fpv-video-records/record-1/file" {
		t.Fatalf("list = %#v", list)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/fpv-video-records/record-1/file", nil)
	req.Header.Set("Range", "bytes=2-5")
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("file status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "2345" {
		t.Fatalf("range body = %q", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/fpv-video-records", strings.NewReader(`{"ids":["record-1"]}`))
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(videoPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("video file should be removed, stat err = %v", err)
	}
	if len(recordStore.items) != 0 {
		t.Fatalf("record should be deleted: %#v", recordStore.items)
	}
}

func TestExportFPVVideoRecordsRoute(t *testing.T) {
	state := store.New(10, 10)
	s := newTestServer(t, state)
	recordDir := t.TempDir()
	s.cfg.FPVVideo.RecordDir = recordDir
	if err := os.WriteFile(filepath.Join(recordDir, "record-a.mp4"), []byte("video-a"), 0o644); err != nil {
		t.Fatalf("WriteFile(record-a) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(recordDir, "record-b.mp4"), []byte("video-b"), 0o644); err != nil {
		t.Fatalf("WriteFile(record-b) error = %v", err)
	}
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	s.fpvRecords = &memoryFPVVideoRecordStore{
		items: []model.FPVVideoRecord{
			{
				ID:        "record-a",
				StartedAt: now,
				EndedAt:   now.Add(time.Second),
				Status:    model.FPVVideoRecordStatusReady,
				FileName:  "record-a.mp4",
			},
			{
				ID:        "record-b",
				StartedAt: now.Add(time.Second),
				EndedAt:   now.Add(2 * time.Second),
				Status:    model.FPVVideoRecordStatusReady,
				FileName:  "record-b.mp4",
			},
			{
				ID:        "record-failed",
				StartedAt: now,
				EndedAt:   now,
				Status:    model.FPVVideoRecordStatusFailed,
				FileName:  "failed.mp4",
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/fpv-video-records/export", strings.NewReader(`{"ids":["record-a","record-failed","record-b"]}`))
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("export status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); contentType != "application/zip" {
		t.Fatalf("content type = %q, want application/zip", contentType)
	}
	reader, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("open zip response: %v", err)
	}
	files := map[string]string{}
	for _, file := range reader.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open zip entry %q: %v", file.Name, err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read zip entry %q: %v", file.Name, err)
		}
		files[file.Name] = string(body)
	}
	if len(files) != 2 {
		t.Fatalf("zip files = %#v, want 2 ready files", files)
	}
	if files["record-a.mp4"] != "video-a" || files["record-b.mp4"] != "video-b" {
		t.Fatalf("zip files = %#v", files)
	}
}

func TestPersistFPVVideoRecordMarksReadyAndFailed(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	recordDir := t.TempDir()
	s.cfg.FPVVideo.RecordDir = recordDir
	recordStore := &memoryFPVVideoRecordStore{}
	s.fpvRecords = recordStore
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	if err := os.WriteFile(filepath.Join(recordDir, "ready.mp4"), []byte("video"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	s.persistFPVVideoRecord(context.Background(), fpvVideoSessionSnapshot{
		ID:        1,
		Frequency: 1360,
		StartedAt: now,
		Target: model.ScreenFPVTarget{
			ID:         "fpv-1",
			Frequency:  1360,
			RSSI:       -42,
			SignalType: "FPV",
			DeviceSN:   "SN-1",
		},
		RecordID:   "ready",
		RecordFile: "ready.mp4",
	}, now.Add(3*time.Second), "")
	if len(recordStore.items) != 1 || recordStore.items[0].Status != model.FPVVideoRecordStatusReady {
		t.Fatalf("ready record = %#v", recordStore.items)
	}
	if recordStore.items[0].FileSizeBytes != 5 || recordStore.items[0].DurationSeconds != 3 {
		t.Fatalf("ready file metadata = %#v", recordStore.items[0])
	}

	templateFile := "templated_fpv_1700000000.mp4"
	if err := os.WriteFile(filepath.Join(recordDir, templateFile), []byte("video-template"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	s.persistFPVVideoRecord(context.Background(), fpvVideoSessionSnapshot{
		ID:             2,
		Frequency:      1360,
		StartedAt:      now,
		RecordID:       "templated",
		RecordBasePath: filepath.Join(recordDir, "templated_%path_%s"),
		RecordFileGlob: "templated_*.mp4",
	}, now.Add(2*time.Second), "")
	if len(recordStore.items) != 2 || recordStore.items[1].Status != model.FPVVideoRecordStatusReady {
		t.Fatalf("templated record = %#v", recordStore.items)
	}
	if recordStore.items[1].FileName != templateFile || recordStore.items[1].FileSizeBytes != int64(len("video-template")) {
		t.Fatalf("templated file metadata = %#v", recordStore.items[1])
	}

	s.persistFPVVideoRecord(context.Background(), fpvVideoSessionSnapshot{
		ID:         3,
		Frequency:  1400,
		StartedAt:  now,
		RecordID:   "failed",
		RecordFile: "missing.mp4",
	}, now.Add(time.Second), "")
	if len(recordStore.items) != 3 || recordStore.items[2].Status != model.FPVVideoRecordStatusFailed {
		t.Fatalf("failed record = %#v", recordStore.items)
	}
}

func newTestServer(t *testing.T, state *store.Store) *Server {
	t.Helper()
	deviceSN := "drone-management-001A2B3C4D5E"
	cfg := config.Config{
		Addr:                ":0",
		TCPBindHost:         "127.0.0.1",
		PositionTCPPort:     10007,
		FPVTCPPort:          10005,
		TCPBindRetry:        time.Second,
		FPVCommandTimeout:   time.Second,
		MaxPositionTargets:  10,
		MaxFPVTargets:       10,
		EventBufferSize:     16,
		DefaultLocale:       "zh-CN",
		DeviceTargetAddress: "192.168.100.101",
		LicensePath:         filepath.Join(t.TempDir(), "license.lic"),
	}
	positionSvc := position.NewService(state, position.Options{Host: "127.0.0.1", Port: 10007})
	fpvSvc := fpv.NewService(state, fpv.Options{Host: "127.0.0.1", Port: 10005, CommandTimeout: time.Second})
	outputs := map[int]*httpTestOutput{}
	interferenceSvc := interference.NewService(state, interference.DefaultChannels(), func(number int) interference.Output {
		output := outputs[number]
		if output == nil {
			output = &httpTestOutput{}
			outputs[number] = output
		}
		return output
	})
	licenseSvc := newValidTestLicenseService(t, cfg.LicensePath, deviceSN)
	return New(cfg, state, positionSvc, fpvSvc, WithInterferenceService(interferenceSvc), WithLicenseService(licenseSvc))
}

func newTestServerWithLicense(t *testing.T, path string, deviceSN string) *Server {
	t.Helper()
	s := newTestServer(t, store.New(10, 10))
	s.license = license.NewService(path, func() (string, error) { return deviceSN, nil })
	return s
}

func newTestServerWithOfflineMap(t *testing.T, state *store.Store, mapPath string) *Server {
	t.Helper()
	deviceSN := "drone-management-001A2B3C4D5E"
	cfg := config.Config{
		Addr:                     ":0",
		TCPBindHost:              "127.0.0.1",
		PositionTCPPort:          10007,
		FPVTCPPort:               10005,
		TCPBindRetry:             time.Second,
		FPVCommandTimeout:        time.Second,
		MaxPositionTargets:       10,
		MaxFPVTargets:            10,
		EventBufferSize:          16,
		DefaultLocale:            "zh-CN",
		DeviceTargetAddress:      "192.168.100.101",
		OfflineMapPath:           mapPath,
		OfflineMapUploadMaxBytes: 64 << 20,
		LicensePath:              filepath.Join(t.TempDir(), "license.lic"),
	}
	positionSvc := position.NewService(state, position.Options{Host: "127.0.0.1", Port: 10007})
	fpvSvc := fpv.NewService(state, fpv.Options{Host: "127.0.0.1", Port: 10005, CommandTimeout: time.Second})
	licenseSvc := newValidTestLicenseService(t, cfg.LicensePath, deviceSN)
	options := []Option{
		WithOfflineMapService(offlinemap.NewService(mapPath)),
		WithLicenseService(licenseSvc),
	}
	return New(cfg, state, positionSvc, fpvSvc, options...)
}

func newValidTestLicenseService(t *testing.T, path string, deviceSN string) *license.Service {
	t.Helper()
	service := license.NewService(path, func() (string, error) { return deviceSN, nil })
	raw := generateHTTPTestLicense(t, deviceSN, 24*time.Hour, "test", time.Now())
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("WriteFile(license) error = %v", err)
	}
	return service
}

func generateHTTPTestLicense(t *testing.T, deviceSN string, duration time.Duration, customer string, now time.Time) []byte {
	t.Helper()
	raw, err := license.NewService("", nil).Generate(deviceSN, duration, customer, now)
	if err != nil {
		t.Fatalf("Generate(license) error = %v", err)
	}
	return raw
}

func newMultipartRequest(
	t *testing.T,
	path string,
	fieldName string,
	fileName string,
	content []byte,
	fields map[string]string,
) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile(fieldName, fileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

type httpTestOutput struct {
	value     int
	remaining time.Duration
}

func (o *httpTestOutput) Setup() error {
	return nil
}

func (o *httpTestOutput) SetHigh() error {
	o.value = 1
	o.remaining = 0
	return nil
}

func (o *httpTestOutput) SetHighFor(duration time.Duration) error {
	o.value = 1
	o.remaining = duration
	return nil
}

func (o *httpTestOutput) SetLow() error {
	o.value = 0
	o.remaining = 0
	return nil
}

func (o *httpTestOutput) GetValue() (int, error) {
	return o.value, nil
}

func (o *httpTestOutput) GetState() (interference.OutputState, error) {
	return interference.OutputState{
		Value:     o.value,
		Remaining: o.remaining,
	}, nil
}

func (o *httpTestOutput) Cleanup() {
}

type memoryUserSettingsStore struct {
	settings model.UserSettings
	ok       bool
}

func (s *memoryUserSettingsStore) LoadUser() (model.UserSettings, bool, error) {
	return s.settings, s.ok, nil
}

func (s *memoryUserSettingsStore) SaveEditableUser(settings model.UserSettings) (model.UserSettings, error) {
	s.settings = settings
	s.ok = true
	return settings, nil
}

type memoryLingyunService struct {
	applied model.UserSettings
	status  model.LingyunStatus
}

func (s *memoryLingyunService) ApplySettings(settings model.UserSettings) {
	s.applied = settings
}

func (s *memoryLingyunService) Status() model.LingyunStatus {
	return s.status
}

type memoryIntrusionStore struct {
	items []model.IntrusionRecord
}

func (s *memoryIntrusionStore) List(_ context.Context, options intrusion.QueryOptions) ([]model.IntrusionRecord, error) {
	filtered := make([]model.IntrusionRecord, 0, len(s.items))
	for _, item := range s.items {
		if options.TargetType != "" && item.TargetType != options.TargetType {
			continue
		}
		if !containsFold(item.Model, options.Model) {
			continue
		}
		if !containsFold(item.Serial, options.Serial) {
			continue
		}
		if !options.DateFrom.IsZero() && item.LastSeen.Before(options.DateFrom) {
			continue
		}
		if !options.DateTo.IsZero() && !item.FirstSeen.Before(options.DateTo.AddDate(0, 0, 1)) {
			continue
		}
		filtered = append(filtered, item)
	}
	slices.SortFunc(filtered, func(a, b model.IntrusionRecord) int {
		if cmp := b.LastSeen.Compare(a.LastSeen); cmp != 0 {
			return cmp
		}
		return b.ArchivedAt.Compare(a.ArchivedAt)
	})
	offset := options.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= len(filtered) {
		return []model.IntrusionRecord{}, nil
	}
	limit := options.Limit
	if limit <= 0 || offset+limit > len(filtered) {
		limit = len(filtered) - offset
	}
	return append([]model.IntrusionRecord(nil), filtered[offset:offset+limit]...), nil
}

func containsFold(value string, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(value), strings.ToLower(needle))
}

func (s *memoryIntrusionStore) Delete(_ context.Context, ids []string) (int64, error) {
	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}
	next := s.items[:0]
	var deleted int64
	for _, item := range s.items {
		if _, ok := idSet[item.ID]; ok {
			deleted++
			continue
		}
		next = append(next, item)
	}
	clear(s.items[len(next):])
	s.items = next
	return deleted, nil
}

func (s *memoryIntrusionStore) PruneRetention(_ context.Context, days int, now time.Time) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	cutoff := now.AddDate(0, 0, -days)
	next := s.items[:0]
	var deleted int64
	for _, item := range s.items {
		if item.ArchivedAt.Before(cutoff) {
			deleted++
			continue
		}
		next = append(next, item)
	}
	clear(s.items[len(next):])
	s.items = next
	return deleted, nil
}

type memoryFPVVideoRecordStore struct {
	items []model.FPVVideoRecord
}

func (s *memoryFPVVideoRecordStore) Insert(_ context.Context, record model.FPVVideoRecord) error {
	for index, item := range s.items {
		if item.ID == record.ID {
			s.items[index] = record
			return nil
		}
	}
	s.items = append(s.items, record)
	return nil
}

func (s *memoryFPVVideoRecordStore) List(_ context.Context, options fpvrecord.QueryOptions) ([]model.FPVVideoRecord, error) {
	filtered := make([]model.FPVVideoRecord, 0, len(s.items))
	for _, item := range s.items {
		if !containsFold(item.SignalType, options.SignalType) {
			continue
		}
		if !containsFold(item.DeviceSN, options.DeviceSN) {
			continue
		}
		if !options.DateFrom.IsZero() && item.StartedAt.Before(options.DateFrom) {
			continue
		}
		if !options.DateTo.IsZero() && !item.StartedAt.Before(options.DateTo.AddDate(0, 0, 1)) {
			continue
		}
		filtered = append(filtered, item)
	}
	slices.SortFunc(filtered, func(a, b model.FPVVideoRecord) int {
		if cmp := b.StartedAt.Compare(a.StartedAt); cmp != 0 {
			return cmp
		}
		return b.EndedAt.Compare(a.EndedAt)
	})
	offset := options.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= len(filtered) {
		return []model.FPVVideoRecord{}, nil
	}
	limit := options.Limit
	if limit <= 0 || offset+limit > len(filtered) {
		limit = len(filtered) - offset
	}
	return append([]model.FPVVideoRecord(nil), filtered[offset:offset+limit]...), nil
}

func (s *memoryFPVVideoRecordStore) Get(_ context.Context, id string) (model.FPVVideoRecord, bool, error) {
	for _, item := range s.items {
		if item.ID == id {
			return item, true, nil
		}
	}
	return model.FPVVideoRecord{}, false, nil
}

func (s *memoryFPVVideoRecordStore) Delete(_ context.Context, ids []string, recordDir string) (fpvrecord.DeleteResult, error) {
	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}
	next := s.items[:0]
	filePaths := []string{}
	var deleted int64
	for _, item := range s.items {
		if _, ok := idSet[item.ID]; ok {
			deleted++
			if path, ok := fpvrecord.SafeRecordPath(recordDir, item.FileName); ok {
				filePaths = append(filePaths, path)
			}
			continue
		}
		next = append(next, item)
	}
	clear(s.items[len(next):])
	s.items = next
	return fpvrecord.DeleteResult{Deleted: deleted, FilePaths: filePaths}, nil
}

type memoryInterferenceReportStore struct {
	items []model.InterferenceReport
}

func (s *memoryInterferenceReportStore) List(_ context.Context, options interferencereport.QueryOptions) ([]model.InterferenceReportSummary, error) {
	filtered := make([]model.InterferenceReportSummary, 0, len(s.items))
	for _, item := range s.items {
		if options.Status != "" && item.Status != options.Status {
			continue
		}
		filtered = append(filtered, item.InterferenceReportSummary)
	}
	slices.SortFunc(filtered, func(a, b model.InterferenceReportSummary) int {
		if cmp := b.StartedAt.Compare(a.StartedAt); cmp != 0 {
			return cmp
		}
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	offset := options.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= len(filtered) {
		return []model.InterferenceReportSummary{}, nil
	}
	limit := options.Limit
	if limit <= 0 || offset+limit > len(filtered) {
		limit = len(filtered) - offset
	}
	return append([]model.InterferenceReportSummary(nil), filtered[offset:offset+limit]...), nil
}

func (s *memoryInterferenceReportStore) Get(_ context.Context, id string) (model.InterferenceReport, bool, error) {
	for _, item := range s.items {
		if item.ID == id {
			return item, true, nil
		}
	}
	return model.InterferenceReport{}, false, nil
}

func (s *memoryInterferenceReportStore) DeleteFailed(_ context.Context, id string) (int64, error) {
	for index, item := range s.items {
		if item.ID != id {
			continue
		}
		if item.Status != model.InterferenceReportStatusFailed {
			return 0, interferencereport.ErrNotFailed
		}
		s.items = append(s.items[:index], s.items[index+1:]...)
		return 1, nil
	}
	return 0, interferencereport.ErrNotFound
}

func ptrTime(value time.Time) *time.Time {
	return &value
}

func connectFPVCommandDevice(t *testing.T, s *Server, commandTimeout time.Duration, respondOK bool) (net.Conn, <-chan string, context.CancelFunc) {
	t.Helper()
	port := freeHTTPTestTCPPort(t)
	s.fpv = fpv.NewService(s.store, fpv.Options{
		Host:           "127.0.0.1",
		Port:           port,
		CommandTimeout: commandTimeout,
	})
	ctx, cancel := context.WithCancel(context.Background())
	go s.fpv.Run(ctx)
	waitFor(t, time.Second, func() bool { return s.fpv.Status().Listening })

	deviceConn, err := net.Dial("tcp", s.fpv.Address())
	if err != nil {
		cancel()
		t.Fatalf("Dial() error = %v", err)
	}
	waitFor(t, time.Second, func() bool { return s.fpv.Status().SourceConnected })
	if respondOK {
		return deviceConn, respondOKToFPVCommands(t, deviceConn), cancel
	}
	return deviceConn, recordFPVCommands(t, deviceConn), cancel
}

func newTestFPVVideo(cfg config.Config) *fpvvideo.Service {
	return fpvvideo.New(fpvvideo.Options{
		RTSPURL:          cfg.FPVVideo.RTSPURL,
		MediaMTXPath:     cfg.FPVVideo.MediaMTXPath,
		MediaMTXWorkDir:  cfg.FPVVideo.MediaMTXWorkDir,
		MediaMTXBin:      cfg.FPVVideo.MediaMTXBin,
		WebRTCListenHost: cfg.FPVVideo.WebRTCListenHost,
		WebRTCListenPort: cfg.FPVVideo.WebRTCListenPort,
		WebRTCUDPPort:    cfg.FPVVideo.WebRTCUDPPort,
		WHEPURL:          cfg.FPVVideo.WHEPURL,
	})
}

func configureFakeFPVVideo(t *testing.T, s *Server) {
	t.Helper()
	upstream, _ := newWHEPTestServer(t)
	s.cfg.FPVVideo = config.FPVVideoConfig{
		WHEPURL: upstream.URL + "/fpv/whep",
	}
	s.fpvVideo = newTestFPVVideo(s.cfg)
}

func configureFailingFPVVideo(t *testing.T, s *Server) {
	t.Helper()
	port := freeHTTPTestTCPPort(t)
	s.cfg.FPVVideo = config.FPVVideoConfig{
		WHEPURL: "http://127.0.0.1:" + strconv.Itoa(port) + "/fpv/whep",
	}
	s.fpvVideo = newTestFPVVideo(s.cfg)
}

type whepTestCalls struct {
	mu    sync.Mutex
	items []string
}

func (c *whepTestCalls) Add(value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = append(c.items, value)
}

func (c *whepTestCalls) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

func (c *whepTestCalls) All() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.items)
}

func newWHEPTestServer(t *testing.T) (*httptest.Server, *whepTestCalls) {
	t.Helper()
	calls := &whepTestCalls{items: []string{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read WHEP body: %v", err)
		}
		calls.Add(r.Method + " " + r.URL.Path + " " + string(data))
		switch r.Method {
		case http.MethodOptions:
			w.Header().Set("Accept-Post", "application/sdp")
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html>player</html>"))
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/sdp")
			w.Header().Set("Location", "/fpv/whep/session")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("answer"))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(server.Close)
	return server, calls
}

func freeHTTPTestTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()
	_, portRaw, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		t.Fatalf("Atoi() error = %v", err)
	}
	return port
}

func respondOKToFPVCommands(t *testing.T, conn net.Conn) <-chan string {
	t.Helper()
	commands := make(chan string, 4)
	go func() {
		reader := bufio.NewReader(conn)
		for {
			command, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			commands <- command
			if _, err := conn.Write([]byte("OK\r\n")); err != nil {
				return
			}
		}
	}()
	return commands
}

func recordFPVCommands(t *testing.T, conn net.Conn) <-chan string {
	t.Helper()
	commands := make(chan string, 4)
	go func() {
		reader := bufio.NewReader(conn)
		for {
			command, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			commands <- command
		}
	}()
	return commands
}

func respondOKToFirstFPVCommand(t *testing.T, conn net.Conn) <-chan string {
	t.Helper()
	commands := make(chan string, 4)
	go func() {
		reader := bufio.NewReader(conn)
		first := true
		for {
			command, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			commands <- command
			if first {
				first = false
				if _, err := conn.Write([]byte("OK\r\n")); err != nil {
					return
				}
			}
		}
	}()
	return commands
}

func readCommand(t *testing.T, commands <-chan string) string {
	t.Helper()
	select {
	case command := <-commands:
		return command
	case <-time.After(time.Second):
		t.Fatal("command was not received")
	}
	return ""
}

func readHTTPStreamUntil(t *testing.T, body io.Reader, pattern string) {
	t.Helper()
	reader := bufio.NewReader(body)
	deadline := time.Now().Add(time.Second)
	var seen strings.Builder
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read stream: %v; seen = %s", err, seen.String())
		}
		seen.WriteString(line)
		if strings.Contains(seen.String(), pattern) {
			return
		}
	}
	t.Fatalf("stream did not contain %q; seen = %s", pattern, seen.String())
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not satisfied before timeout")
}

func waitForStream(t *testing.T, reader *bufio.Reader, pattern string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	var seen strings.Builder
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if line != "" {
			seen.WriteString(line)
		}
		if strings.Contains(seen.String(), pattern) {
			return
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			t.Fatalf("read stream: %v; body = %s", err, seen.String())
		}
	}
	t.Fatalf("stream did not contain %q; body = %s", pattern, seen.String())
}

func TestListResponsesAreJSON(t *testing.T) {
	state := store.New(10, 10)
	s := newTestServer(t, state)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/screen/fpv", nil)
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, req)

	var payload model.ListResponse[model.ScreenFPVTarget]
	if err := json.NewDecoder(bufio.NewReader(rec.Body)).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if payload.Items == nil {
		t.Fatal("items should encode as []")
	}
}
