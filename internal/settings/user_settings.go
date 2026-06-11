package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"dr600ab-net/internal/model"
)

// UserStore persists public operator settings.
type UserStore struct {
	mu   sync.Mutex
	path string
}

// NewUserStore creates a user settings store backed by path.
func NewUserStore(path string) *UserStore {
	return &UserStore{path: strings.TrimSpace(path)}
}

// LoadUser reads persisted public user settings.
func (s *UserStore) LoadUser() (model.UserSettings, bool, error) {
	if s == nil || s.path == "" {
		return model.UserSettings{}, false, nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return model.UserSettings{}, false, nil
		}
		return model.UserSettings{}, false, err
	}
	var settings model.UserSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return model.UserSettings{}, false, err
	}
	return settings, true, nil
}

// SaveEditableUser writes public user settings atomically.
func (s *UserStore) SaveEditableUser(settings model.UserSettings) (model.UserSettings, error) {
	settings = model.UserSettingsWithDefaults(settings)
	if s == nil || s.path == "" {
		return settings, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return model.UserSettings{}, err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return model.UserSettings{}, err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(s.path), "."+filepath.Base(s.path)+".*.tmp")
	if err != nil {
		return model.UserSettings{}, err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return model.UserSettings{}, err
	}
	if err := tmp.Close(); err != nil {
		return model.UserSettings{}, err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return model.UserSettings{}, err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return model.UserSettings{}, err
	}
	cleanup = false
	return settings, nil
}
