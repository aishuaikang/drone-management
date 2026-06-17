package config

import (
	"testing"
	"time"
)

func TestLoadFPVVideoConfig(t *testing.T) {
	t.Setenv("API_FPV_VIDEO_RTSP_URL", "rtsp://192.168.100.200:554/live/1_1")
	t.Setenv("API_FPV_VIDEO_MEDIAMTX_PATH", "/opt/mediamtx")
	t.Setenv("API_FPV_VIDEO_MEDIAMTX_WORK_DIR", "/tmp/drone-management-fpv-video")
	t.Setenv("API_FPV_VIDEO_MEDIAMTX_BIN", "/opt/mediamtx/mediamtx")
	t.Setenv("API_FPV_VIDEO_WEBRTC_HOST", "127.0.0.2")
	t.Setenv("API_FPV_VIDEO_WEBRTC_PORT", "28889")
	t.Setenv("API_FPV_VIDEO_WEBRTC_UDP_PORT", "28189")
	t.Setenv("API_FPV_VIDEO_WHEP_URL", "http://127.0.0.1:8889/fpv/whep")
	t.Setenv("API_FPV_VIDEO_RECORD_DIR", "/var/lib/drone-management/fpv-videos")
	t.Setenv("API_FPV_VIDEO_RECORD_DB_PATH", "/var/lib/drone-management/fpv-videos.db")
	t.Setenv("API_FPV_COMMAND_TIMEOUT_MS", "4500")
	t.Setenv("API_POSITION_TARGET_TTL_SECONDS", "12")
	t.Setenv("API_INTRUSION_DB_PATH", "/var/lib/drone-management/intrusions.db")
	t.Setenv("API_USER_SETTINGS_PATH", "/var/lib/drone-management/user-settings.json")

	cfg := Load()
	if cfg.FPVVideo.RTSPURL != "rtsp://192.168.100.200:554/live/1_1" {
		t.Fatalf("rtsp url = %q", cfg.FPVVideo.RTSPURL)
	}
	if cfg.FPVVideo.MediaMTXPath != "/opt/mediamtx" {
		t.Fatalf("mediamtx path = %q", cfg.FPVVideo.MediaMTXPath)
	}
	if cfg.FPVVideo.MediaMTXWorkDir != "/tmp/drone-management-fpv-video" {
		t.Fatalf("mediamtx work dir = %q", cfg.FPVVideo.MediaMTXWorkDir)
	}
	if cfg.FPVVideo.MediaMTXBin != "/opt/mediamtx/mediamtx" {
		t.Fatalf("mediamtx bin = %q", cfg.FPVVideo.MediaMTXBin)
	}
	if cfg.FPVVideo.WebRTCListenHost != "127.0.0.2" ||
		cfg.FPVVideo.WebRTCListenPort != 28889 ||
		cfg.FPVVideo.WebRTCUDPPort != 28189 {
		t.Fatalf("webrtc listen config = %#v", cfg.FPVVideo)
	}
	if cfg.FPVVideo.WHEPURL != "http://127.0.0.1:8889/fpv/whep" {
		t.Fatalf("whep url = %q", cfg.FPVVideo.WHEPURL)
	}
	if cfg.FPVVideo.RecordDir != "/var/lib/drone-management/fpv-videos" {
		t.Fatalf("record dir = %q", cfg.FPVVideo.RecordDir)
	}
	if cfg.FPVVideoRecordDBPath != "/var/lib/drone-management/fpv-videos.db" {
		t.Fatalf("record db path = %q", cfg.FPVVideoRecordDBPath)
	}
	if cfg.FPVCommandTimeout.String() != "4.5s" {
		t.Fatalf("fpv command timeout = %s", cfg.FPVCommandTimeout)
	}
	if cfg.PositionTargetTTL.String() != "12s" {
		t.Fatalf("position target ttl = %s", cfg.PositionTargetTTL)
	}
	if cfg.IntrusionDBPath != "/var/lib/drone-management/intrusions.db" {
		t.Fatalf("intrusion db path = %q", cfg.IntrusionDBPath)
	}
	if cfg.UserSettingsPath != "/var/lib/drone-management/user-settings.json" {
		t.Fatalf("user settings path = %q", cfg.UserSettingsPath)
	}
}

func TestLoadDeviceAndOfflineMapConfig(t *testing.T) {
	t.Setenv("API_DEVICE_SN", "SL67CB3FC848FA0E795P")
	t.Setenv("API_LICENSE_PATH", "/var/lib/drone-management/license.lic")
	t.Setenv("API_OFFLINE_MAP_PATH", "/var/lib/drone-management/static/map")
	t.Setenv("API_OFFLINE_MAP_UPLOAD_MAX_MB", "32")

	cfg := Load()
	if cfg.DeviceSN != "SL67CB3FC848FA0E795P" {
		t.Fatalf("device sn = %q", cfg.DeviceSN)
	}
	if cfg.LicensePath != "/var/lib/drone-management/license.lic" {
		t.Fatalf("license path = %q", cfg.LicensePath)
	}
	if cfg.OfflineMapPath != "/var/lib/drone-management/static/map" {
		t.Fatalf("offline map path = %q", cfg.OfflineMapPath)
	}
	if cfg.OfflineMapUploadMaxBytes != 32*1024*1024 {
		t.Fatalf("upload max bytes = %d", cfg.OfflineMapUploadMaxBytes)
	}
}

func TestLoadInterferenceRelayConfig(t *testing.T) {
	t.Setenv("API_INTERFERENCE_RELAY_HOST", "192.168.1.210")
	t.Setenv("API_INTERFERENCE_RELAY_PORT", "2030")
	t.Setenv("API_INTERFERENCE_RELAY_ADDRESS", "2")
	t.Setenv("API_INTERFERENCE_RELAY_TIMEOUT_MS", "1500")

	cfg := Load()
	if cfg.InterferenceRelay.Host != "192.168.1.210" {
		t.Fatalf("relay host = %q", cfg.InterferenceRelay.Host)
	}
	if cfg.InterferenceRelay.Port != 2030 {
		t.Fatalf("relay port = %d", cfg.InterferenceRelay.Port)
	}
	if cfg.InterferenceRelay.Address != 2 {
		t.Fatalf("relay address = %d", cfg.InterferenceRelay.Address)
	}
	if cfg.InterferenceRelay.Timeout != 1500*time.Millisecond {
		t.Fatalf("relay timeout = %s", cfg.InterferenceRelay.Timeout)
	}
}
