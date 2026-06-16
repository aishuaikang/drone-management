package lingyun

import (
	"testing"
	"time"

	"drone-management/internal/model"
)

func TestProjectPositionRoutesAndFillsRequiredTelemetry(t *testing.T) {
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	device := model.LingyunDeviceSettingsWithDefaults(model.LingyunDeviceSettings{
		Type:      model.LingyunDeviceRemoteID,
		BandWidth: "20MHz",
	})
	target := model.ScreenPositionTarget{
		ID:        "position-1",
		Serial:    "RID-SN",
		Model:     "DJI Mini 4 Pro",
		Source:    "RID",
		Frequency: 2437,
		Drone:     &model.ScreenPositionPoint{Latitude: 22.6799, Longitude: 114.2036},
		Pilot:     &model.ScreenPositionPoint{Latitude: 22.6802, Longitude: 114.2037},
		LastSeen:  now,
	}

	object, ok := projectPosition(target, device, now)
	if !ok {
		t.Fatal("projectPosition() ok = false")
	}
	if object.ObjectID != "RID-SN" || object.Longitude == nil || *object.Longitude != 114.2036 {
		t.Fatalf("object identity/point = %#v", object)
	}
	if object.Altitude == nil || *object.Altitude != 0 ||
		object.Height == nil || *object.Height != 0 ||
		object.Speed == nil || *object.Speed != 0 {
		t.Fatalf("required telemetry = altitude:%v height:%v speed:%v", object.Altitude, object.Height, object.Speed)
	}
	if object.Extension.Channel != "2.437GHz" ||
		object.Extension.UAVSN != "RID-SN" ||
		object.Extension.PilotLon == nil ||
		*object.Extension.PilotLon != 114.2037 {
		t.Fatalf("extension = %#v", object.Extension)
	}
}

func TestProjectPositionSkipsWrongSourceAndMissingDrone(t *testing.T) {
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	ridDevice := model.LingyunDeviceSettingsWithDefaults(model.LingyunDeviceSettings{Type: model.LingyunDeviceRemoteID})
	dcdDevice := model.LingyunDeviceSettingsWithDefaults(model.LingyunDeviceSettings{Type: model.LingyunDeviceDCD})

	if _, ok := projectPosition(model.ScreenPositionTarget{Source: "dji_O:4"}, ridDevice, now); ok {
		t.Fatal("RID device accepted dji_O source")
	}
	if _, ok := projectPosition(model.ScreenPositionTarget{Source: "dji_O:4", Serial: "SN"}, dcdDevice, now); ok {
		t.Fatal("DCD device accepted missing drone point")
	}
}

func TestProjectAOAUsesPlaceholders(t *testing.T) {
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	device := model.LingyunDeviceSettingsWithDefaults(model.LingyunDeviceSettings{Type: model.LingyunDeviceAOA})
	target := model.ScreenFPVTarget{
		Frequency:  5750,
		RSSI:       88,
		SignalType: "FPV",
		Valid:      true,
		LastSeen:   now,
	}

	object, ok := projectAOA(target, device, now)
	if !ok {
		t.Fatal("projectAOA() ok = false")
	}
	if object.ObjectID != "AOA-5750" || object.Extension.UAVSN != "AOA-5750" {
		t.Fatalf("placeholder id = %#v", object)
	}
	if object.Longitude != nil || object.Latitude != nil || object.Extension.Direction == nil || *object.Extension.Direction != 0 {
		t.Fatalf("AOA optional fields = %#v", object)
	}
	if object.Extension.Channel != "5.75GHz" || object.Extension.BandWidth != model.DefaultLingyunBandWidth {
		t.Fatalf("radio extension = %#v", object.Extension)
	}
}
