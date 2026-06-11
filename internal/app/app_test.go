package app

import (
	"testing"

	"dr600ab-net/internal/config"
	"dr600ab-net/internal/model"
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
