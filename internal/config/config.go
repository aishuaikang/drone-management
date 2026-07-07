// Package config loads runtime configuration from environment variables.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config describes runtime configuration for the API and TCP receivers.
type Config struct {
	Addr                     string
	TCPBindHost              string
	PositionTCPPort          int
	FPVTCPPort               int
	TCPBindRetry             time.Duration
	TCPReadIdleTimeout       time.Duration
	FPVCommandTimeout        time.Duration
	PositionTargetTTL        time.Duration
	MaxPositionTargets       int
	MaxFPVTargets            int
	EventBufferSize          int
	DefaultLocale            string
	DeviceTargetAddress      string
	DeviceSN                 string
	ManualLocationPath       string
	IntrusionDBPath          string
	InterferenceReportDBPath string
	FPVVideoRecordDBPath     string
	UserSettingsPath         string
	LicensePath              string
	OfflineMapPath           string
	OfflineMapUploadMaxBytes int64
	InterferenceRelay        InterferenceRelayConfig
	FPVVideo                 FPVVideoConfig
	O3Decrypt                O3DecryptConfig
}

// InterferenceRelayConfig configures the network relay used for interference output.
type InterferenceRelayConfig struct {
	Host    string
	Port    int
	Address int
	Timeout time.Duration
}

// FPVVideoConfig configures browser playback for the FPV RTSP stream.
type FPVVideoConfig struct {
	RTSPURL          string
	MediaMTXPath     string
	MediaMTXWorkDir  string
	MediaMTXBin      string
	WebRTCListenHost string
	WebRTCListenPort int
	WebRTCUDPPort    int
	WHEPURL          string
	RecordDir        string
}

// O3DecryptConfig configures O3+/O4 encrypted DID MQTT decryption.
type O3DecryptConfig struct {
	Enabled        bool
	Broker         string
	Port           int
	Username       string
	Password       string
	Timeout        time.Duration
	ConnectTimeout time.Duration
}

// Load returns runtime configuration with production defaults.
func Load() Config {
	return Config{
		Addr:                     envString("API_ADDR", ":18080"),
		TCPBindHost:              envString("API_TCP_BIND_HOST", "0.0.0.0"),
		PositionTCPPort:          envInt("API_POSITION_TCP_PORT", 10007),
		FPVTCPPort:               envInt("API_FPV_TCP_PORT", 10005),
		TCPBindRetry:             time.Duration(envInt("API_TCP_BIND_RETRY_MS", 1000)) * time.Millisecond,
		TCPReadIdleTimeout:       time.Duration(envInt("API_TCP_READ_IDLE_TIMEOUT_MS", 0)) * time.Millisecond,
		FPVCommandTimeout:        time.Duration(envInt("API_FPV_COMMAND_TIMEOUT_MS", 3000)) * time.Millisecond,
		PositionTargetTTL:        time.Duration(envInt("API_POSITION_TARGET_TTL_SECONDS", 20)) * time.Second,
		MaxPositionTargets:       envInt("API_MAX_POSITION_TARGETS", 500),
		MaxFPVTargets:            envInt("API_MAX_FPV_TARGETS", 500),
		EventBufferSize:          envInt("API_EVENT_BUFFER_SIZE", 64),
		DefaultLocale:            envString("API_DEFAULT_LOCALE", "zh-CN"),
		DeviceTargetAddress:      envString("API_DEVICE_TARGET_ADDRESS", "192.168.100.101"),
		DeviceSN:                 envString("API_DEVICE_SN", ""),
		ManualLocationPath:       envString("API_MANUAL_DEVICE_LOCATION_PATH", "./data/manual-device-location.json"),
		IntrusionDBPath:          envString("API_INTRUSION_DB_PATH", "./data/intrusions.db"),
		InterferenceReportDBPath: envString("API_INTERFERENCE_REPORT_DB_PATH", "./data/interference-reports.db"),
		FPVVideoRecordDBPath:     envString("API_FPV_VIDEO_RECORD_DB_PATH", "./data/fpv-videos.db"),
		UserSettingsPath:         envString("API_USER_SETTINGS_PATH", "./data/user-settings.json"),
		LicensePath:              envString("API_LICENSE_PATH", "./license.lic"),
		OfflineMapPath:           envString("API_OFFLINE_MAP_PATH", "./static/map"),
		OfflineMapUploadMaxBytes: int64(envInt("API_OFFLINE_MAP_UPLOAD_MAX_MB", 2048)) * 1024 * 1024,
		InterferenceRelay: InterferenceRelayConfig{
			Host:    envString("API_INTERFERENCE_RELAY_HOST", "192.168.100.107"),
			Port:    envInt("API_INTERFERENCE_RELAY_PORT", 1030),
			Address: envInt("API_INTERFERENCE_RELAY_ADDRESS", 1),
			Timeout: time.Duration(envInt("API_INTERFERENCE_RELAY_TIMEOUT_MS", 1000)) * time.Millisecond,
		},
		FPVVideo: FPVVideoConfig{
			RTSPURL:          envString("API_FPV_VIDEO_RTSP_URL", "rtsp://192.168.100.106:554/live/1_1"),
			MediaMTXPath:     envString("API_FPV_VIDEO_MEDIAMTX_PATH", "./MediaMTX"),
			MediaMTXWorkDir:  envString("API_FPV_VIDEO_MEDIAMTX_WORK_DIR", "./tmp/fpv-video"),
			MediaMTXBin:      envString("API_FPV_VIDEO_MEDIAMTX_BIN", ""),
			WebRTCListenHost: envString("API_FPV_VIDEO_WEBRTC_HOST", "127.0.0.1"),
			WebRTCListenPort: envInt("API_FPV_VIDEO_WEBRTC_PORT", 18889),
			WebRTCUDPPort:    envInt("API_FPV_VIDEO_WEBRTC_UDP_PORT", 18189),
			WHEPURL:          envString("API_FPV_VIDEO_WHEP_URL", ""),
			RecordDir:        envString("API_FPV_VIDEO_RECORD_DIR", "./data/fpv-videos"),
		},
		O3Decrypt: O3DecryptConfig{
			Enabled:        envBool("API_O3_DECRYPT_ENABLED", true),
			Broker:         envString("API_O3_DECRYPT_BROKER", "101.36.159.2"),
			Port:           envInt("API_O3_DECRYPT_PORT", 1883),
			Username:       envString("API_O3_DECRYPT_USERNAME", "zkzp"),
			Password:       envString("API_O3_DECRYPT_PASSWORD", "Zkzp123456.."),
			Timeout:        time.Duration(envInt("API_O3_DECRYPT_TIMEOUT_MS", 10000)) * time.Millisecond,
			ConnectTimeout: time.Duration(envInt("API_O3_DECRYPT_CONNECT_TIMEOUT_MS", 10000)) * time.Millisecond,
		},
	}
}

func envString(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	switch raw {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}
