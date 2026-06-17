package lingyun

import (
	"slices"
	"testing"

	"drone-management/internal/model"
)

func TestDefinitionByTopicTrimsDeviceID(t *testing.T) {
	settings := model.LingyunSettingsWithDefaults(model.LingyunSettings{
		Enabled:      true,
		Broker:       "127.0.0.1:1883",
		ProviderCode: " DPTEST ",
		Devices: []model.LingyunDeviceSettings{
			{Type: model.LingyunDeviceDCD, Enabled: true, DeviceID: " DCD01 ", DeviceName: "DCD"},
		},
	})

	def, device, ok := definitionByTopic(" DPTEST ", "bridge/DPTEST/device_control/dcd/DCD01", settings)
	if !ok {
		t.Fatal("expected control topic to match trimmed device ID")
	}
	if def.Type != model.LingyunDeviceDCD || device.DeviceID != " DCD01 " {
		t.Fatalf("matched definition = %#v, device = %#v", def, device)
	}
}

func TestBuildInterferenceDevicePayload(t *testing.T) {
	settings := model.LingyunSettingsWithDefaults(model.LingyunSettings{
		ProviderCode: "DPTEST",
		Devices: []model.LingyunDeviceSettings{
			{
				Type:                model.LingyunDeviceInterference,
				Enabled:             true,
				DeviceID:            "IFR01",
				DeviceName:          "IFR",
				CountermeasureRange: 1500,
				Bands:               []string{"2.4G", "5.8G"},
				InterferenceTypes:   []int{0, 1, 2},
			},
		},
	})
	def, ok := definitionByType(model.LingyunDeviceInterference)
	if !ok {
		t.Fatal("missing interference definition")
	}
	device, ok := lingyunDevice(settings, model.LingyunDeviceInterference)
	if !ok {
		t.Fatal("missing interference device")
	}

	payload := buildDevicePayload(settings, def, device, 0)
	if payload.DeviceType != 6 || !slices.Equal(payload.SupFun, []int{60001, 60002, 60003}) {
		t.Fatalf("interference payload type/supFun = %d/%#v", payload.DeviceType, payload.SupFun)
	}
	extension, ok := payload.Extension.(interferenceDeviceExtension)
	if !ok {
		t.Fatalf("extension = %#v, want interferenceDeviceExtension", payload.Extension)
	}
	if extension.CountermeasureRange != 1500 ||
		!slices.Equal(extension.Bands, []string{"2.4G", "5.8G"}) ||
		!slices.Equal(extension.InterferenceTypes, []int{0, 1, 2}) ||
		extension.VerticalCoverageStartAngle != -90 ||
		extension.VerticalCoverageEndAngle != 90 {
		t.Fatalf("interference extension = %#v", extension)
	}
}
