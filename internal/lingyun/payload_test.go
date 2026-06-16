package lingyun

import (
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
