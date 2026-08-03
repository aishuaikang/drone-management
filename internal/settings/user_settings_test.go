package settings

import (
	"path/filepath"
	"testing"

	"drone-management/internal/model"
)

func TestUserSettingsPersistence(t *testing.T) {
	store := NewUserStore(filepath.Join(t.TempDir(), "user-settings.json"))
	retentionDays := 0
	saved, err := store.SaveEditableUser(model.UserSettings{
		IntrusionRetentionDays: &retentionDays,
		FPVVideoWebRTCHost:     "192.168.31.254",
		Whitelist: []model.WhitelistItem{
			{Serial: "DJI-001", Model: "Mini 4 Pro", Source: "manual"},
		},
	})
	if err != nil {
		t.Fatalf("SaveEditableUser() error = %v", err)
	}
	if saved.IntrusionRetentionDays == nil || *saved.IntrusionRetentionDays != 0 {
		t.Fatalf("saved retention = %#v, want 0", saved.IntrusionRetentionDays)
	}

	loaded, ok, err := store.LoadUser()
	if err != nil {
		t.Fatalf("LoadUser() error = %v", err)
	}
	if !ok {
		t.Fatal("LoadUser() ok = false, want true")
	}
	if loaded.IntrusionRetentionDays == nil || *loaded.IntrusionRetentionDays != 0 {
		t.Fatalf("loaded retention = %#v, want 0", loaded.IntrusionRetentionDays)
	}
	if len(loaded.Whitelist) != 1 || loaded.Whitelist[0].Serial != "DJI-001" {
		t.Fatalf("loaded whitelist = %#v", loaded.Whitelist)
	}
	if loaded.FPVVideoWebRTCHost != "192.168.31.254" {
		t.Fatalf("loaded FPV WebRTC host = %q", loaded.FPVVideoWebRTCHost)
	}
}

func TestLoadUserSettingsMissingFile(t *testing.T) {
	store := NewUserStore(filepath.Join(t.TempDir(), "missing.json"))
	_, ok, err := store.LoadUser()
	if err != nil {
		t.Fatalf("LoadUser() error = %v", err)
	}
	if ok {
		t.Fatal("LoadUser() ok = true, want false")
	}
}
