package model

import "testing"

func TestUserSettingsWithDefaultsAddsPositionExpireSeconds(t *testing.T) {
	settings := UserSettingsWithDefaults(UserSettings{})
	if settings.PositionExpireSeconds == nil || *settings.PositionExpireSeconds != DefaultPositionExpireSeconds {
		t.Fatalf("position expire seconds = %#v, want %d", settings.PositionExpireSeconds, DefaultPositionExpireSeconds)
	}
	if settings.WarningZoneEnabled == nil || *settings.WarningZoneEnabled {
		t.Fatalf("warning zone enabled = %#v, want false", settings.WarningZoneEnabled)
	}
	if settings.WarningZoneRadiusMeters == nil || *settings.WarningZoneRadiusMeters != DefaultWarningZoneRadiusMeters {
		t.Fatalf("warning zone radius = %#v, want %.0f", settings.WarningZoneRadiusMeters, DefaultWarningZoneRadiusMeters)
	}
}

func TestUserSettingsPositionExpireSeconds(t *testing.T) {
	custom := 12
	if got := UserSettingsPositionExpireSeconds(UserSettings{PositionExpireSeconds: &custom}); got != custom {
		t.Fatalf("custom position expire seconds = %d, want %d", got, custom)
	}
	if got := UserSettingsPositionExpireSeconds(UserSettings{}); got != DefaultPositionExpireSeconds {
		t.Fatalf("default position expire seconds = %d, want %d", got, DefaultPositionExpireSeconds)
	}
}
