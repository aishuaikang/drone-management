package app

import (
	"testing"

	"drone-management/internal/config"
	"drone-management/internal/model"
)

func TestConfigWithUserTCPPorts(t *testing.T) {
	positionPort := 11007
	fpvPort := 11005
	cfg := config.Config{
		PositionTCPPort: 10007,
		FPVTCPPort:      10005,
	}

	got := configWithUserTCPPorts(cfg, model.UserSettings{
		PositionTCPPort: &positionPort,
		FPVTCPPort:      &fpvPort,
	})
	if got.PositionTCPPort != positionPort || got.FPVTCPPort != fpvPort {
		t.Fatalf("ports = %d/%d, want %d/%d", got.PositionTCPPort, got.FPVTCPPort, positionPort, fpvPort)
	}
}

func TestConfigWithUserTCPPortsIgnoresInvalidPair(t *testing.T) {
	positionPort := 11005
	fpvPort := 11005
	cfg := config.Config{
		PositionTCPPort: 10007,
		FPVTCPPort:      10005,
	}

	got := configWithUserTCPPorts(cfg, model.UserSettings{
		PositionTCPPort: &positionPort,
		FPVTCPPort:      &fpvPort,
	})
	if got.PositionTCPPort != cfg.PositionTCPPort || got.FPVTCPPort != cfg.FPVTCPPort {
		t.Fatalf("ports = %d/%d, want defaults %d/%d", got.PositionTCPPort, got.FPVTCPPort, cfg.PositionTCPPort, cfg.FPVTCPPort)
	}
}

func TestConfigWithUserFPVVideoSettings(t *testing.T) {
	cfg := config.Config{}
	got := configWithUserFPVVideoSettings(cfg, model.UserSettings{
		FPVVideoRTMPEnabled: true,
		FPVVideoRTMPURL:     "rtmp://example.com/live/key",
	})
	if !got.FPVVideo.RTMPEnabled || got.FPVVideo.RTMPURL != "rtmp://example.com/live/key" {
		t.Fatalf("RTMP config = enabled:%v url:%q", got.FPVVideo.RTMPEnabled, got.FPVVideo.RTMPURL)
	}
}

func TestConfigWithUserFPVVideoSettingsRejectsInvalidRTMP(t *testing.T) {
	cfg := config.Config{}
	got := configWithUserFPVVideoSettings(cfg, model.UserSettings{
		FPVVideoRTMPEnabled: true,
		FPVVideoRTMPURL:     "https://example.com/live/key",
	})
	if got.FPVVideo.RTMPEnabled || got.FPVVideo.RTMPURL != "" {
		t.Fatalf("invalid RTMP config = enabled:%v url:%q", got.FPVVideo.RTMPEnabled, got.FPVVideo.RTMPURL)
	}
}
