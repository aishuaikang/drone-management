package store

import (
	"sync"
	"testing"
	"time"

	"dr600ab-net/internal/model"
)

func TestPositionsSortByFirstSeen(t *testing.T) {
	state := New(10, 10)
	base := time.Now().UTC()

	_, _ = state.AddPosition(model.ScreenPositionTarget{
		Serial:    "old-target",
		Model:     "DJI Old",
		Source:    "dji_O:2/3",
		FirstSeen: base,
		LastSeen:  base,
	})
	_, _ = state.AddPosition(model.ScreenPositionTarget{
		Serial:    "new-target",
		Model:     "DJI New",
		Source:    "dji_O:4",
		FirstSeen: base.Add(time.Second),
		LastSeen:  base.Add(time.Second),
	})
	_, _ = state.AddPosition(model.ScreenPositionTarget{
		Serial:    "old-target",
		Model:     "DJI Old",
		Source:    "dji_O:2/3",
		FirstSeen: base,
		LastSeen:  base.Add(2 * time.Second),
	})

	items := state.Positions(10)
	if len(items) != 2 {
		t.Fatalf("positions count = %d, want 2", len(items))
	}
	if items[0].Serial != "new-target" || items[1].Serial != "old-target" {
		t.Fatalf("order = %s, %s; want new-target, old-target", items[0].Serial, items[1].Serial)
	}
}

func TestSetPositionTTLPrunesAndArchivesExpiredTargets(t *testing.T) {
	state := New(10, 10)
	archiver := &memoryPositionArchiver{}
	state.SetPositionArchiver(archiver)
	state.SetPositionTTL(30 * time.Second)
	base := time.Now().Add(-10 * time.Second)

	_, _ = state.AddPosition(model.ScreenPositionTarget{
		Serial:    "expired",
		Model:     "DJI Mini",
		Source:    "RID",
		FirstSeen: base,
		LastSeen:  base,
	})
	if items := state.Positions(10); len(items) != 1 {
		t.Fatalf("positions before ttl update = %d, want 1", len(items))
	}

	state.SetPositionTTL(5 * time.Second)
	if got := state.PositionTTL(); got != 5*time.Second {
		t.Fatalf("position ttl = %s, want 5s", got)
	}
	if items := state.Positions(10); len(items) != 0 {
		t.Fatalf("positions after ttl update = %#v, want empty", items)
	}
	if len(archiver.items) != 1 || archiver.items[0].Serial != "expired" {
		t.Fatalf("archived positions = %#v", archiver.items)
	}
}

func TestManualDeviceLocationFallback(t *testing.T) {
	state := New(10, 10)
	location := state.SetManualDeviceLocation(model.GeoPoint{Latitude: 39.9, Longitude: 116.3})
	if location.Source != "manual" || !location.Valid || location.Point == nil {
		t.Fatalf("manual location = %#v", location)
	}
	if location.Point.Latitude != 39.9 || location.Point.Longitude != 116.3 {
		t.Fatalf("manual point = %#v", location.Point)
	}

	pilot := &model.ScreenPositionPoint{Latitude: 39.91, Longitude: 116.31}
	_, _ = state.AddPosition(model.ScreenPositionTarget{
		Serial:    "SN",
		Model:     "DJI",
		Source:    "test",
		Pilot:     pilot,
		FirstSeen: time.Now(),
		LastSeen:  time.Now(),
	})
	items := state.Positions(10)
	if len(items) != 1 || items[0].PilotDistanceM == nil {
		t.Fatalf("manual location was not used for relations: %#v", items)
	}
}

func TestLiveDeviceLocationOverridesManualFallback(t *testing.T) {
	state := New(10, 10)
	state.SetManualDeviceLocation(model.GeoPoint{Latitude: 39.9, Longitude: 116.3})
	state.UpdateDeviceLocation(model.ScreenDeviceLocationResponse{
		Source: "ddsT1",
		Point:  &model.GeoPoint{Latitude: 31.23, Longitude: 121.47},
		Valid:  true,
		Locked: true,
	})

	location := state.DeviceLocation()
	if location.Source != "ddsT1" || !location.Valid || location.Point == nil {
		t.Fatalf("live location = %#v", location)
	}
	if location.Point.Latitude != 31.23 || location.Point.Longitude != 121.47 {
		t.Fatalf("live point = %#v", location.Point)
	}
}

func TestLoadManualDeviceLocation(t *testing.T) {
	state := New(10, 10)
	updatedAt := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	state.LoadManualDeviceLocation(model.GeoPoint{Latitude: 39.9, Longitude: 116.3}, &updatedAt)

	location := state.DeviceLocation()
	if location.Source != "manual" || !location.Valid || location.Point == nil {
		t.Fatalf("loaded manual location = %#v", location)
	}
	if location.UpdatedAt == nil || !location.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("updated at = %v, want %v", location.UpdatedAt, updatedAt)
	}
}

func TestPositionFallbackMergesIntoDecodedTarget(t *testing.T) {
	state := New(10, 10)
	base := recentStoreTestTime()
	correlationID := "dji_O:4:01fa261e"

	_, _ = state.AddPosition(model.ScreenPositionTarget{
		CorrelationID: correlationID,
		Serial:        "01fa261e",
		Model:         "DJI-Drone",
		Source:        "dji_O:4",
		Frequency:     5816.5,
		RSSI:          -81,
		FirstSeen:     base,
		LastSeen:      base,
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "dji_O:4",
			ReceivedAt: base,
			Serial:     "01fa261e",
			Model:      "DJI-Drone",
			Frequency:  5816.5,
			RSSI:       -81,
		},
	})

	height := 12.5
	speed := 8.4
	_, _ = state.AddPosition(model.ScreenPositionTarget{
		CorrelationID: correlationID,
		Serial:        "real-sn",
		Model:         "DJI Mini 4 Pro",
		Source:        "dji_O:4",
		Frequency:     5816.5,
		RSSI:          -72,
		Drone:         &model.ScreenPositionPoint{Latitude: 31.23, Longitude: 121.47},
		Pilot:         &model.ScreenPositionPoint{Latitude: 31.24, Longitude: 121.48},
		Height:        &height,
		Speed:         &speed,
		Cracked:       true,
		FirstSeen:     base.Add(time.Second),
		LastSeen:      base.Add(time.Second),
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "dji_O:4",
			ReceivedAt: base.Add(time.Second),
			Serial:     "real-sn",
			Model:      "DJI Mini 4 Pro",
			Frequency:  5816.5,
			RSSI:       -72,
			Cracked:    true,
			Raw:        "decoded",
		},
	})

	items := state.Positions(10)
	if len(items) != 1 {
		t.Fatalf("positions count = %d, want 1", len(items))
	}
	item := items[0]
	if item.Serial != "real-sn" || item.Model != "Mini 4 Pro" || !item.Cracked {
		t.Fatalf("decoded identity = %#v", item)
	}
	if !item.FirstSeen.Equal(base) {
		t.Fatalf("first seen = %v, want %v", item.FirstSeen, base)
	}
	if item.Drone == nil || item.Drone.Latitude != 31.23 || item.Speed == nil || *item.Speed != speed {
		t.Fatalf("decoded fields = %#v", item)
	}
}

func TestPositionDecodedTargetDoesNotMergeLaterFallbackFrame(t *testing.T) {
	state := New(10, 10)
	base := recentStoreTestTime()
	correlationID := "dji_O:4:01fa261e"
	height := 12.5

	_, _ = state.AddPosition(model.ScreenPositionTarget{
		CorrelationID: correlationID,
		Serial:        "real-sn",
		Model:         "DJI Mini 4 Pro",
		Source:        "dji_O:4",
		Frequency:     5816.5,
		RSSI:          -72,
		Drone:         &model.ScreenPositionPoint{Latitude: 31.23, Longitude: 121.47},
		Height:        &height,
		Cracked:       true,
		FirstSeen:     base,
		LastSeen:      base,
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "dji_O:4",
			ReceivedAt: base,
			Serial:     "real-sn",
			Model:      "DJI Mini 4 Pro",
			Frequency:  5816.5,
			RSSI:       -72,
			Cracked:    true,
			Raw:        "decoded",
		},
	})
	_, _ = state.AddPosition(model.ScreenPositionTarget{
		CorrelationID: correlationID,
		Serial:        "01fa261e",
		Model:         "DJI-Drone",
		Source:        "dji_O:4",
		Frequency:     5796.5,
		RSSI:          -91,
		FirstSeen:     base.Add(2 * time.Second),
		LastSeen:      base.Add(2 * time.Second),
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "dji_O:4",
			ReceivedAt: base.Add(2 * time.Second),
			Serial:     "01fa261e",
			Model:      "DJI-Drone",
			Frequency:  5796.5,
			RSSI:       -91,
			Raw:        "fallback",
		},
	})

	items := state.Positions(10)
	if len(items) != 2 {
		t.Fatalf("positions count = %d, want decoded target plus fallback", len(items))
	}
	var decoded, fallback *model.ScreenPositionTarget
	for index := range items {
		if items[index].Cracked {
			decoded = &items[index]
			continue
		}
		if items[index].Model == "DJI-Drone" {
			fallback = &items[index]
		}
	}
	if decoded == nil {
		t.Fatalf("decoded target not found: %#v", items)
	}
	if fallback == nil {
		t.Fatalf("fallback target not found: %#v", items)
	}
	if decoded.Serial != "real-sn" || decoded.Model != "Mini 4 Pro" || !decoded.Cracked {
		t.Fatalf("decoded identity was overwritten = %#v", decoded)
	}
	if decoded.Drone == nil || decoded.Drone.Latitude != 31.23 || decoded.Height == nil || *decoded.Height != height {
		t.Fatalf("decoded fields were overwritten = %#v", decoded)
	}
	if fallback.Serial != "01fa261e" || fallback.Cracked {
		t.Fatalf("fallback target = %#v", fallback)
	}
	if _, ok := state.RemoveUncrackedDIDScreenPositionByCorrelationID(correlationID); !ok {
		t.Fatalf("expected fallback to be removed")
	}
	items = state.Positions(10)
	if len(items) != 1 || items[0].Serial != "real-sn" || !items[0].Cracked {
		t.Fatalf("positions after fallback removal = %#v", items)
	}
}

func TestHasCrackedScreenPositionByCorrelationID(t *testing.T) {
	state := New(10, 10)
	base := recentStoreTestTime()
	correlationID := "dji_O:4:01fa261e"

	_, _ = state.AddPosition(model.ScreenPositionTarget{
		CorrelationID: correlationID,
		Serial:        "01fa261e",
		Model:         "DJI-Drone",
		Source:        "dji_O:4",
		FirstSeen:     base,
		LastSeen:      base,
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "dji_O:4",
			ReceivedAt: base,
			Serial:     "01fa261e",
			Model:      "DJI-Drone",
		},
	})
	if state.HasCrackedScreenPositionByCorrelationID(correlationID) {
		t.Fatalf("fallback should not count as cracked")
	}
	_, _ = state.RemoveUncrackedDIDScreenPositionByCorrelationID(correlationID)
	_, _ = state.AddPosition(model.ScreenPositionTarget{
		CorrelationID: correlationID,
		Serial:        "real-sn",
		Model:         "DJI Mini 4 Pro",
		Source:        "dji_O:4",
		Cracked:       true,
		FirstSeen:     base.Add(time.Second),
		LastSeen:      base.Add(time.Second),
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "dji_O:4",
			ReceivedAt: base.Add(time.Second),
			Serial:     "real-sn",
			Model:      "DJI Mini 4 Pro",
			Cracked:    true,
		},
	})
	if !state.HasCrackedScreenPositionByCorrelationID(correlationID) {
		t.Fatalf("decoded target should count as cracked")
	}
}

func TestPositionDelayedDecodedFrameDoesNotMoveLastSeenBackward(t *testing.T) {
	state := New(10, 10)
	base := recentStoreTestTime()
	correlationID := "dji_O:4:01fa261e"

	_, _ = state.AddPosition(model.ScreenPositionTarget{
		CorrelationID: correlationID,
		Serial:        "01fa261e",
		Model:         "DJI-Drone",
		Source:        "dji_O:4",
		Frequency:     5816.5,
		RSSI:          -81,
		FirstSeen:     base,
		LastSeen:      base,
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "dji_O:4",
			ReceivedAt: base,
			Serial:     "01fa261e",
			Model:      "DJI-Drone",
			Frequency:  5816.5,
			RSSI:       -81,
			Raw:        "fallback-old",
		},
	})
	_, _ = state.AddPosition(model.ScreenPositionTarget{
		CorrelationID: correlationID,
		Serial:        "01fa261e",
		Model:         "DJI-Drone",
		Source:        "dji_O:4",
		Frequency:     5796.5,
		RSSI:          -91,
		FirstSeen:     base.Add(2 * time.Second),
		LastSeen:      base.Add(2 * time.Second),
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "dji_O:4",
			ReceivedAt: base.Add(2 * time.Second),
			Serial:     "01fa261e",
			Model:      "DJI-Drone",
			Frequency:  5796.5,
			RSSI:       -91,
			Raw:        "fallback-new",
		},
	})

	height := 12.5
	_, _ = state.AddPosition(model.ScreenPositionTarget{
		CorrelationID: correlationID,
		Serial:        "real-sn",
		Model:         "DJI Mini 4 Pro",
		Source:        "dji_O:4",
		Frequency:     5816.5,
		RSSI:          -72,
		Drone:         &model.ScreenPositionPoint{Latitude: 31.23, Longitude: 121.47},
		Height:        &height,
		Cracked:       true,
		FirstSeen:     base,
		LastSeen:      base,
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "dji_O:4",
			ReceivedAt: base,
			Serial:     "real-sn",
			Model:      "DJI Mini 4 Pro",
			Frequency:  5816.5,
			RSSI:       -72,
			Cracked:    true,
			Raw:        "decoded-old",
		},
	})

	items := state.Positions(10)
	if len(items) != 1 {
		t.Fatalf("positions count = %d, want 1", len(items))
	}
	item := items[0]
	if item.Serial != "real-sn" || item.Model != "Mini 4 Pro" || !item.Cracked {
		t.Fatalf("decoded identity = %#v", item)
	}
	if item.Drone == nil || item.Drone.Latitude != 31.23 || item.Height == nil || *item.Height != height {
		t.Fatalf("decoded fields = %#v", item)
	}
	if !item.LastSeen.Equal(base.Add(2 * time.Second)) {
		t.Fatalf("last seen moved backward = %v, want %v", item.LastSeen, base.Add(2*time.Second))
	}
	if item.Frequency != 5796.5 || item.RSSI != -91 {
		t.Fatalf("latest radio was overwritten by old decoded frame = %#v", item)
	}
	if item.LastRecord.Raw != "fallback-new" {
		t.Fatalf("last record raw = %q, want fallback-new", item.LastRecord.Raw)
	}
}

func TestPositionMergesRIDPrefixedSerialWithDJIOSerial(t *testing.T) {
	state := New(10, 10)
	base := recentStoreTestTime()
	height := 12.0
	pilot := &model.ScreenPositionPoint{Latitude: 31.24, Longitude: 121.48}

	_, _ = state.AddPosition(model.ScreenPositionTarget{
		Serial:    "F6Z9C2412003L1W8",
		Model:     "Mini 4 Pro",
		Source:    "dji_O:4",
		Frequency: 5797,
		RSSI:      -84,
		Pilot:     pilot,
		FirstSeen: base,
		LastSeen:  base,
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "dji_O:4",
			ReceivedAt: base,
			Serial:     "F6Z9C2412003L1W8",
			Model:      "Mini 4 Pro",
			Frequency:  5797,
			RSSI:       -84,
		},
	})
	_, _ = state.AddPosition(model.ScreenPositionTarget{
		Serial:    "1581F6Z9C2412003L1W8",
		Model:     "DJI Mini4 pro",
		Source:    "RID",
		Frequency: 2437,
		RSSI:      -42,
		Height:    &height,
		FirstSeen: base.Add(time.Second),
		LastSeen:  base.Add(time.Second),
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "RID",
			ReceivedAt: base.Add(time.Second),
			Serial:     "1581F6Z9C2412003L1W8",
			Model:      "DJI Mini4 pro",
			Frequency:  2437,
			RSSI:       -42,
		},
	})

	items := state.Positions(10)
	if len(items) != 1 {
		t.Fatalf("positions count = %d, want 1", len(items))
	}
	item := items[0]
	if item.Serial != "F6Z9C2412003L1W8" || item.Model != "Mini 4 Pro" {
		t.Fatalf("identity = %#v", item)
	}
	if item.Pilot == nil || item.Pilot.Latitude != pilot.Latitude || item.Pilot.Longitude != pilot.Longitude {
		t.Fatalf("pilot was not preserved from dji_O: %#v", item.Pilot)
	}
	if item.Height == nil || *item.Height != height {
		t.Fatalf("height was not merged from RID: %#v", item.Height)
	}
	if len(item.Sources) != 2 || item.Sources[0] != "dji_O:4" || item.Sources[1] != "RID" {
		t.Fatalf("sources = %#v, want dji_O:4 and RID", item.Sources)
	}
	if item.Frequency != 2437 || item.RSSI != -42 {
		t.Fatalf("latest radio = %#v, want RID radio", item)
	}
}

func TestPositionMergeIgnoresInvalidIncomingFields(t *testing.T) {
	state := New(10, 10)
	base := recentStoreTestTime()
	height := 42.0
	speed := 8.5
	drone := &model.ScreenPositionPoint{Latitude: 31.23, Longitude: 121.47}
	pilot := &model.ScreenPositionPoint{Latitude: 31.24, Longitude: 121.48}

	_, _ = state.AddPosition(model.ScreenPositionTarget{
		Serial:          "SN123",
		Model:           "Mini 4 Pro",
		Source:          "dji_O:2/3",
		Frequency:       5776.5,
		RSSI:            -81,
		Drone:           drone,
		Pilot:           pilot,
		Height:          &height,
		Speed:           &speed,
		TrajectorySpeed: &speed,
		FirstSeen:       base,
		LastSeen:        base,
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "dji_O:2/3",
			ReceivedAt: base,
			Serial:     "SN123",
			Model:      "Mini 4 Pro",
			Frequency:  5776.5,
			RSSI:       -81,
			Raw:        "valid",
		},
	})
	zeroHeight := 0.0
	zeroSpeed := 0.0
	_, _ = state.AddPosition(model.ScreenPositionTarget{
		Serial:          "SN123",
		Model:           "Mini 4 Pro",
		Source:          "dji_O:2/3",
		Frequency:       0,
		RSSI:            0,
		Drone:           &model.ScreenPositionPoint{Latitude: 0, Longitude: 0},
		Pilot:           &model.ScreenPositionPoint{Latitude: 91, Longitude: 121.48},
		Home:            &model.ScreenPositionPoint{Latitude: 0, Longitude: 0},
		Height:          &zeroHeight,
		Speed:           &zeroSpeed,
		TrajectorySpeed: &zeroSpeed,
		FirstSeen:       base.Add(time.Second),
		LastSeen:        base.Add(time.Second),
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "dji_O:2/3",
			ReceivedAt: base.Add(time.Second),
			Serial:     "SN123",
			Model:      "Mini 4 Pro",
			Raw:        "invalid",
		},
	})

	items := state.Positions(10)
	if len(items) != 1 {
		t.Fatalf("positions count = %d, want 1", len(items))
	}
	item := items[0]
	if item.Drone == nil || item.Drone.Latitude != drone.Latitude || item.Drone.Longitude != drone.Longitude {
		t.Fatalf("drone was overwritten by invalid coordinate: %#v", item.Drone)
	}
	if item.Pilot == nil || item.Pilot.Latitude != pilot.Latitude || item.Pilot.Longitude != pilot.Longitude {
		t.Fatalf("pilot was overwritten by invalid coordinate: %#v", item.Pilot)
	}
	if item.Home != nil {
		t.Fatalf("invalid home coordinate was merged: %#v", item.Home)
	}
	if item.Height == nil || *item.Height != height {
		t.Fatalf("height was overwritten by invalid zero telemetry: %#v", item.Height)
	}
	if item.Speed == nil || *item.Speed != speed {
		t.Fatalf("speed was overwritten by invalid zero telemetry: %#v", item.Speed)
	}
	if item.Frequency != 5776.5 || item.RSSI != -81 {
		t.Fatalf("radio was overwritten by invalid zero values: %#v", item)
	}
	if len(item.DroneTrajectory) != 1 || item.DroneTrajectory[0].Latitude != drone.Latitude {
		t.Fatalf("invalid drone trajectory was merged: %#v", item.DroneTrajectory)
	}
	if len(item.PilotTrajectory) != 1 || item.PilotTrajectory[0].Latitude != pilot.Latitude {
		t.Fatalf("invalid pilot trajectory was merged: %#v", item.PilotTrajectory)
	}
}

func TestPositionAddFiltersInvalidCoordinates(t *testing.T) {
	state := New(10, 10)
	base := recentStoreTestTime()
	speed := 0.0
	height := 0.0

	_, _ = state.AddPosition(model.ScreenPositionTarget{
		Serial:          "zero-point",
		Model:           "Mini 4 Pro",
		Source:          "RID",
		Frequency:       2437,
		RSSI:            -42,
		Drone:           &model.ScreenPositionPoint{Latitude: 0, Longitude: 0},
		Pilot:           &model.ScreenPositionPoint{Latitude: 91, Longitude: 121.48},
		Home:            &model.ScreenPositionPoint{Latitude: 31.2, Longitude: 181},
		Speed:           &speed,
		Height:          &height,
		TrajectorySpeed: &speed,
		FirstSeen:       base,
		LastSeen:        base,
	})

	items := state.Positions(10)
	if len(items) != 1 {
		t.Fatalf("positions count = %d, want 1", len(items))
	}
	item := items[0]
	if item.Drone != nil || item.Pilot != nil || item.Home != nil {
		t.Fatalf("invalid coordinates were kept: drone=%#v pilot=%#v home=%#v", item.Drone, item.Pilot, item.Home)
	}
	if item.Height != nil || item.Speed != nil {
		t.Fatalf("zero telemetry without a valid position point was kept: height=%#v speed=%#v", item.Height, item.Speed)
	}
	if len(item.DroneTrajectory) != 0 || len(item.PilotTrajectory) != 0 {
		t.Fatalf("invalid coordinates were added to trajectories: drone=%#v pilot=%#v", item.DroneTrajectory, item.PilotTrajectory)
	}
}

func TestPositionArchiveKeepsFullTrajectoryWhileLiveViewIsCapped(t *testing.T) {
	state := New(10, 10)
	archiver := &memoryPositionArchiver{}
	state.SetPositionArchiver(archiver)
	base := recentStoreTestTime()

	for index := range 90 {
		point := &model.ScreenPositionPoint{
			Latitude:  31.20 + float64(index)*0.0001,
			Longitude: 121.40 + float64(index)*0.0001,
		}
		_, _ = state.AddPosition(model.ScreenPositionTarget{
			Serial:    "long-track",
			Model:     "Mini 4 Pro",
			Source:    "RID",
			Drone:     point,
			FirstSeen: base,
			LastSeen:  base.Add(time.Duration(index) * 10 * time.Millisecond),
		})
	}

	items := state.Positions(10)
	if len(items) != 1 {
		t.Fatalf("positions count = %d, want 1", len(items))
	}
	if len(items[0].DroneTrajectory) != maxDisplayTrajectoryPoints {
		t.Fatalf("live drone trajectory points = %d, want %d", len(items[0].DroneTrajectory), maxDisplayTrajectoryPoints)
	}
	if len(items[0].FullDroneTrajectory) != 90 {
		t.Fatalf("full drone trajectory points = %d, want 90", len(items[0].FullDroneTrajectory))
	}

	state.SetPositionTTL(500 * time.Millisecond)
	archived := archiver.Items()
	if len(archived) != 1 {
		t.Fatalf("archived positions = %d, want 1", len(archived))
	}
	if len(archived[0].DroneTrajectory) != maxDisplayTrajectoryPoints {
		t.Fatalf("archived display trajectory points = %d, want %d", len(archived[0].DroneTrajectory), maxDisplayTrajectoryPoints)
	}
	if len(archived[0].FullDroneTrajectory) != 90 {
		t.Fatalf("archived full trajectory points = %d, want 90", len(archived[0].FullDroneTrajectory))
	}
}

func TestPositionNormalizesModelAliases(t *testing.T) {
	state := New(10, 10)
	base := recentStoreTestTime()

	_, _ = state.AddPosition(model.ScreenPositionTarget{
		Serial:    "1581F6Z9C2412003L1W8",
		Model:     "DJI Mini4 pro",
		Source:    "RID",
		FirstSeen: base,
		LastSeen:  base,
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "RID",
			ReceivedAt: base,
			Serial:     "1581F6Z9C2412003L1W8",
			Model:      "DJI Mini4 pro",
		},
	})

	items := state.Positions(10)
	if len(items) != 1 {
		t.Fatalf("positions count = %d, want 1", len(items))
	}
	if items[0].Serial != "F6Z9C2412003L1W8" || items[0].Model != "Mini 4 Pro" {
		t.Fatalf("normalized target = %#v", items[0])
	}
	if items[0].LastRecord.Model != "Mini 4 Pro" {
		t.Fatalf("last record model = %q, want Mini 4 Pro", items[0].LastRecord.Model)
	}
}

func TestPositionDecodedDIDCollapsesPlaceholderAndRID(t *testing.T) {
	state := New(10, 10)
	base := recentStoreTestTime()
	correlationID := "dji_O:4:c41992e1"
	height := 12.0
	pilot := &model.ScreenPositionPoint{Latitude: 31.24, Longitude: 121.48}

	_, _ = state.AddPosition(model.ScreenPositionTarget{
		CorrelationID: correlationID,
		Serial:        "c41992e1",
		Model:         "DJI-Drone",
		Source:        "dji_O:4",
		Frequency:     2430,
		RSSI:          -85,
		FirstSeen:     base,
		LastSeen:      base,
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "dji_O:4",
			ReceivedAt: base,
			Serial:     "c41992e1",
			Model:      "DJI-Drone",
			Frequency:  2430,
			RSSI:       -85,
		},
	})
	_, _ = state.AddPosition(model.ScreenPositionTarget{
		Serial:    "1581F6Z9C2412003L1W8",
		Model:     "DJI Mini4 pro",
		Source:    "RID",
		Frequency: 2437,
		RSSI:      -46,
		Height:    &height,
		Cracked:   true,
		FirstSeen: base.Add(time.Second),
		LastSeen:  base.Add(time.Second),
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "RID",
			ReceivedAt: base.Add(time.Second),
			Serial:     "1581F6Z9C2412003L1W8",
			Model:      "DJI Mini4 pro",
			Frequency:  2437,
			RSSI:       -46,
			Cracked:    true,
		},
	})

	items := state.Positions(10)
	if len(items) != 2 {
		t.Fatalf("positions before decode = %d, want unresolved placeholder and RID", len(items))
	}

	_, _ = state.AddPosition(model.ScreenPositionTarget{
		CorrelationID: correlationID,
		Serial:        "F6Z9C2412003L1W8",
		Model:         "Mini 4 Pro",
		Source:        "dji_O:4",
		Frequency:     5816,
		RSSI:          -72,
		Drone:         &model.ScreenPositionPoint{Latitude: 31.23, Longitude: 121.47},
		Pilot:         pilot,
		Cracked:       true,
		FirstSeen:     base.Add(2 * time.Second),
		LastSeen:      base.Add(2 * time.Second),
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "dji_O:4",
			ReceivedAt: base.Add(2 * time.Second),
			Serial:     "F6Z9C2412003L1W8",
			Model:      "Mini 4 Pro",
			Frequency:  5816,
			RSSI:       -72,
			Cracked:    true,
			Raw:        "decoded",
		},
	})

	items = state.Positions(10)
	if len(items) != 1 {
		t.Fatalf("positions count = %d, want 1", len(items))
	}
	item := items[0]
	if item.Serial != "F6Z9C2412003L1W8" || item.Model != "Mini 4 Pro" || !item.Cracked {
		t.Fatalf("identity = %#v", item)
	}
	if item.CorrelationID != correlationID {
		t.Fatalf("correlation id = %q, want %q", item.CorrelationID, correlationID)
	}
	if !item.FirstSeen.Equal(base) {
		t.Fatalf("first seen = %v, want placeholder time %v", item.FirstSeen, base)
	}
	if !item.LastSeen.Equal(base.Add(2 * time.Second)) {
		t.Fatalf("last seen = %v, want decoded time", item.LastSeen)
	}
	if item.Pilot == nil || item.Pilot.Latitude != pilot.Latitude || item.Pilot.Longitude != pilot.Longitude {
		t.Fatalf("pilot from decoded target was not preserved: %#v", item.Pilot)
	}
	if item.Height == nil || *item.Height != height {
		t.Fatalf("height from RID was not preserved: %#v", item.Height)
	}
	if item.LastRecord.Raw != "decoded" {
		t.Fatalf("last record raw = %q, want decoded", item.LastRecord.Raw)
	}
	if !hasSource(item.Sources, "RID") || !hasSource(item.Sources, "dji_O:4") {
		t.Fatalf("sources = %#v, want RID and dji_O:4", item.Sources)
	}
}

func TestFPVExpiresAfterTenSeconds(t *testing.T) {
	state := New(10, 10)
	now := time.Now().UTC()
	oldSeen := now.Add(-fpvTTL - time.Second)
	freshSeen := now.Add(-fpvTTL / 2)

	_, _ = state.AddFPV(model.ScreenFPVTarget{
		Frequency:  1360,
		RSSI:       34,
		SignalType: "FPV",
		Valid:      true,
		FirstSeen:  oldSeen,
		LastSeen:   oldSeen,
	})
	_, _ = state.AddFPV(model.ScreenFPVTarget{
		Frequency:  1380,
		RSSI:       42,
		SignalType: "FPV",
		Valid:      true,
		FirstSeen:  freshSeen,
		LastSeen:   freshSeen,
	})

	items := state.FPV(10)
	if len(items) != 1 {
		t.Fatalf("fpv count = %d, want 1", len(items))
	}
	if items[0].Frequency != 1380 {
		t.Fatalf("fpv frequency = %v, want fresh target only", items[0].Frequency)
	}
}

func TestExpiredPositionIsArchived(t *testing.T) {
	state := New(10, 10)
	archiver := &memoryPositionArchiver{}
	state.SetPositionArchiver(archiver)
	now := time.Now().UTC()
	oldSeen := now.Add(-defaultPositionTTL - time.Second)

	_, _ = state.AddPosition(model.ScreenPositionTarget{
		Serial:    "expired-sn",
		Model:     "Mini 4 Pro",
		Source:    "RID",
		FirstSeen: oldSeen,
		LastSeen:  oldSeen,
	})
	_ = state.Positions(10)

	items := archiver.Items()
	if len(items) != 1 {
		t.Fatalf("archived positions = %d, want 1", len(items))
	}
	if items[0].Serial != "expired-sn" {
		t.Fatalf("archived target = %#v", items[0])
	}
}

func TestActivePositionAndFPVAreNotArchived(t *testing.T) {
	state := New(10, 10)
	archiver := &memoryPositionArchiver{}
	state.SetPositionArchiver(archiver)
	now := time.Now().UTC()

	_, _ = state.AddPosition(model.ScreenPositionTarget{
		Serial:    "active-sn",
		Model:     "Mini 4 Pro",
		Source:    "RID",
		FirstSeen: now,
		LastSeen:  now,
	})
	_, _ = state.AddFPV(model.ScreenFPVTarget{
		Frequency:  1360,
		RSSI:       34,
		SignalType: "FPV",
		Valid:      true,
		FirstSeen:  now.Add(-fpvTTL - time.Second),
		LastSeen:   now.Add(-fpvTTL - time.Second),
	})
	_ = state.Positions(10)
	_ = state.FPV(10)

	if items := archiver.Items(); len(items) != 0 {
		t.Fatalf("archived positions = %#v, want none", items)
	}
}

type memoryPositionArchiver struct {
	mu    sync.Mutex
	items []model.ScreenPositionTarget
}

func (a *memoryPositionArchiver) ArchivePosition(target model.ScreenPositionTarget) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.items = append(a.items, target)
	return nil
}

func (a *memoryPositionArchiver) Items() []model.ScreenPositionTarget {
	a.mu.Lock()
	defer a.mu.Unlock()
	items := make([]model.ScreenPositionTarget, len(a.items))
	copy(items, a.items)
	return items
}

func recentStoreTestTime() time.Time {
	return time.Now().Add(-2 * time.Second).UTC()
}

func hasSource(sources []string, want string) bool {
	for _, source := range sources {
		if source == want {
			return true
		}
	}
	return false
}
