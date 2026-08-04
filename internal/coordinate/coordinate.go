// Package coordinate provides shared geographic coordinate validation.
package coordinate

import "math"

const originThreshold = 0.1

// IsValid reports whether longitude and latitude contain usable WGS84 coordinates.
func IsValid(longitude, latitude float64) bool {
	if math.IsNaN(longitude) || math.IsInf(longitude, 0) ||
		math.IsNaN(latitude) || math.IsInf(latitude, 0) {
		return false
	}
	if longitude < -180 || longitude > 180 || latitude < -90 || latitude > 90 {
		return false
	}

	nearOrigin := math.Abs(longitude) < originThreshold && math.Abs(latitude) < originThreshold
	return !nearOrigin && longitude != 0 && latitude != 0
}

// IsValidCoord reports whether non-nil longitude and latitude pointers contain usable WGS84 coordinates.
func IsValidCoord(longitude, latitude *float64) bool {
	return longitude != nil && latitude != nil && IsValid(*longitude, *latitude)
}
