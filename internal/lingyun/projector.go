package lingyun

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"drone-management/internal/model"
)

const uavObjectType = 30

func projectPosition(
	target model.ScreenPositionTarget,
	device model.LingyunDeviceSettings,
	now time.Time,
) (senseDataObject, bool) {
	if !isLingyunPositionSource(target.Source, device.Type) {
		return senseDataObject{}, false
	}
	if target.Drone == nil || !validCoordinate(target.Drone.Latitude, target.Drone.Longitude) {
		return senseDataObject{}, false
	}
	objectID := strings.TrimSpace(target.Serial)
	if objectID == "" {
		objectID = strings.TrimSpace(target.ID)
	}
	if objectID == "" {
		return senseDataObject{}, false
	}

	seenAt := target.LastSeen
	if seenAt.IsZero() {
		seenAt = now
	}
	longitude := target.Drone.Longitude
	latitude := target.Drone.Latitude
	altitude := floatValueOrZero(target.Altitude)
	height := floatValueOrZero(target.Height)
	speed := floatValueOrZero(target.Speed)
	extension := dataExtension{
		ObjectType: uavObjectType,
		Channel:    channelFromFrequency(target.Frequency),
		BandWidth:  firstNonEmpty(device.BandWidth, model.DefaultLingyunBandWidth),
		UAVModel:   strings.TrimSpace(target.Model),
		UAVSN:      objectID,
	}
	if target.Pilot != nil && validCoordinate(target.Pilot.Latitude, target.Pilot.Longitude) {
		pilotLon := target.Pilot.Longitude
		pilotLat := target.Pilot.Latitude
		extension.PilotLon = &pilotLon
		extension.PilotLat = &pilotLat
	}
	return senseDataObject{
		ObjectID:  objectID,
		Longitude: &longitude,
		Latitude:  &latitude,
		Altitude:  &altitude,
		Height:    &height,
		Speed:     &speed,
		Time:      seenAt.UnixMilli(),
		Extension: extension,
	}, true
}

func projectAOA(
	target model.ScreenFPVTarget,
	device model.LingyunDeviceSettings,
	now time.Time,
) (senseDataObject, bool) {
	if !target.Valid || target.Frequency <= 0 {
		return senseDataObject{}, false
	}
	objectID := strings.TrimSpace(target.DeviceSN)
	if objectID == "" {
		objectID = fmt.Sprintf("AOA-%.0f", target.Frequency)
	}
	seenAt := target.LastSeen
	if seenAt.IsZero() {
		seenAt = now
	}
	direction := 0.0
	return senseDataObject{
		ObjectID: objectID,
		Time:     seenAt.UnixMilli(),
		Extension: dataExtension{
			ObjectType: uavObjectType,
			Channel:    channelFromFrequency(target.Frequency),
			BandWidth:  firstNonEmpty(device.BandWidth, model.DefaultLingyunBandWidth),
			UAVModel:   firstNonEmpty(target.SignalType, "FPV"),
			UAVSN:      objectID,
			Direction:  &direction,
		},
	}, true
}

func isLingyunPositionSource(source string, deviceType string) bool {
	source = strings.TrimSpace(source)
	switch deviceType {
	case model.LingyunDeviceRemoteID, model.LingyunDeviceDCD:
		return strings.EqualFold(source, "RID") || strings.HasPrefix(strings.ToLower(source), "dji_o")
	default:
		return false
	}
}

func channelFromFrequency(frequency float64) string {
	if frequency <= 0 || math.IsNaN(frequency) || math.IsInf(frequency, 0) {
		return ""
	}
	if frequency >= 1000 {
		return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(frequency/1000, 'f', 3, 64), "0"), ".") + "GHz"
	}
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(frequency, 'f', 3, 64), "0"), ".") + "MHz"
}

func validCoordinate(lat, lng float64) bool {
	return !math.IsNaN(lat) &&
		!math.IsInf(lat, 0) &&
		!math.IsNaN(lng) &&
		!math.IsInf(lng, 0) &&
		lat >= -90 &&
		lat <= 90 &&
		lng >= -180 &&
		lng <= 180 &&
		!(lat == 0 && lng == 0)
}

func floatValueOrZero(value *float64) float64 {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return 0
	}
	return *value
}

func firstNonEmpty(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return strings.TrimSpace(primary)
	}
	return strings.TrimSpace(fallback)
}
