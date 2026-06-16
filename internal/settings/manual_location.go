// Package settings persists operator-managed runtime settings.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"drone-management/internal/model"
)

type manualDeviceLocationFile struct {
	Point     model.GeoPoint `json:"point"`
	UpdatedAt time.Time      `json:"updatedAt"`
}

// LoadManualDeviceLocation reads the persisted manual receiver/device location.
func LoadManualDeviceLocation(path string) (model.GeoPoint, *time.Time, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return model.GeoPoint{}, nil, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return model.GeoPoint{}, nil, false, nil
		}
		return model.GeoPoint{}, nil, false, err
	}
	var payload manualDeviceLocationFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return model.GeoPoint{}, nil, false, err
	}
	if !validGeoPoint(&payload.Point) {
		return model.GeoPoint{}, nil, false, fmt.Errorf("invalid manual device location: %+v", payload.Point)
	}
	updatedAt := payload.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	return payload.Point, &updatedAt, true, nil
}

// SaveManualDeviceLocation writes the manual receiver/device location.
func SaveManualDeviceLocation(path string, point model.GeoPoint, updatedAt time.Time) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if !validGeoPoint(&point) {
		return fmt.Errorf("invalid manual device location: %+v", point)
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manualDeviceLocationFile{
		Point:     point,
		UpdatedAt: updatedAt,
	}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

// ClearManualDeviceLocation removes the persisted manual receiver/device location.
func ClearManualDeviceLocation(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
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
