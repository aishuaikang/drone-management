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
	if object.Extension.Direction != nil ||
		object.Extension.PilotAlt != nil ||
		object.Extension.Angle != nil ||
		object.Extension.VSpeed != nil ||
		object.Extension.BaroAlt != nil ||
		object.Extension.UAVType != nil ||
		object.Extension.Status != nil {
		t.Fatalf("RID optional-only fields must not be fabricated: %#v", object.Extension)
	}
}

func TestProjectDCDUsesCommonDroneFields(t *testing.T) {
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	device := model.LingyunDeviceSettingsWithDefaults(model.LingyunDeviceSettings{
		Type:      model.LingyunDeviceDCD,
		BandWidth: "10MHz",
	})
	target := model.ScreenPositionTarget{
		ID:        "dcd-1",
		Serial:    "DCD-SN",
		Model:     "DJI Air 3",
		Source:    "dji_O4",
		Frequency: 5796.5,
		Drone:     &model.ScreenPositionPoint{Latitude: 31.821698, Longitude: 117.2391},
		Pilot:     &model.ScreenPositionPoint{Latitude: 31.821778, Longitude: 117.239375},
		LastSeen:  now,
	}

	object, ok := projectPosition(target, device, now)
	if !ok {
		t.Fatal("projectPosition() ok = false")
	}
	if object.ObjectID != "DCD-SN" ||
		object.Longitude == nil ||
		*object.Longitude != 117.2391 ||
		object.Latitude == nil ||
		*object.Latitude != 31.821698 {
		t.Fatalf("DCD object = %#v", object)
	}
	if object.Extension.ObjectType != uavObjectType ||
		object.Extension.Channel != "5.796GHz" ||
		object.Extension.BandWidth != "10MHz" ||
		object.Extension.UAVModel != "DJI Air 3" ||
		object.Extension.UAVSN != "DCD-SN" ||
		object.Extension.PilotLon == nil ||
		*object.Extension.PilotLon != 117.239375 ||
		object.Extension.PilotLat == nil ||
		*object.Extension.PilotLat != 31.821778 {
		t.Fatalf("DCD extension = %#v", object.Extension)
	}
}

func TestProjectPositionRoutesRIDAndDJIOSourcesToRIDAndDCD(t *testing.T) {
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	ridDevice := model.LingyunDeviceSettingsWithDefaults(model.LingyunDeviceSettings{Type: model.LingyunDeviceRemoteID})
	dcdDevice := model.LingyunDeviceSettingsWithDefaults(model.LingyunDeviceSettings{Type: model.LingyunDeviceDCD})

	for _, tt := range []struct {
		name   string
		source string
		device model.LingyunDeviceSettings
	}{
		{name: "rid accepts rid", source: "RID", device: ridDevice},
		{name: "rid accepts dji", source: "dji_O:4", device: ridDevice},
		{name: "dcd accepts rid", source: "RID", device: dcdDevice},
		{name: "dcd accepts dji", source: "dji_O:2/3", device: dcdDevice},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := projectPosition(model.ScreenPositionTarget{
				Source: tt.source,
				Serial: "SN",
				Model:  "DJI",
				Drone:  &model.ScreenPositionPoint{Latitude: 22.1, Longitude: 113.9},
			}, tt.device, now)
			if !ok {
				t.Fatalf("%s rejected source %q", tt.device.Type, tt.source)
			}
		})
	}

	if _, ok := projectPosition(model.ScreenPositionTarget{Source: "dji_O:4", Serial: "SN"}, dcdDevice, now); ok {
		t.Fatal("DCD device accepted missing drone point")
	}
	if _, ok := projectPosition(model.ScreenPositionTarget{
		Source: "dji_O:4",
		Serial: "PLACEHOLDER-SN",
		Model:  "DJI-Drone",
		Drone:  &model.ScreenPositionPoint{Latitude: 22.1, Longitude: 113.9},
	}, ridDevice, now); ok {
		t.Fatal("RID device accepted DJI-Drone placeholder")
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
