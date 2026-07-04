package lingyun

import (
	"slices"
	"strings"
	"testing"

	"drone-management/internal/model"
)

func TestCommonV13DeviceDefinitions(t *testing.T) {
	want := map[string]struct {
		abbr       string
		deviceType int
		commands   []int
	}{
		model.LingyunDeviceAOA:          {abbr: "aoa", deviceType: 9, commands: []int{90000}},
		model.LingyunDeviceDCD:          {abbr: "dcd", deviceType: 11, commands: []int{110000}},
		model.LingyunDeviceRemoteID:     {abbr: "rid", deviceType: 102, commands: []int{1020000}},
		model.LingyunDeviceInterference: {abbr: "ifr", deviceType: 6, commands: []int{60001, 60002, 60003}},
	}
	if len(deviceDefinitions) != len(want) {
		t.Fatalf("deviceDefinitions length = %d, want %d: %#v", len(deviceDefinitions), len(want), deviceDefinitions)
	}
	for _, def := range deviceDefinitions {
		expected, ok := want[def.Type]
		if !ok {
			t.Fatalf("unexpected device definition in common protocol: %#v", def)
		}
		if def.Abbr != expected.abbr || def.DeviceType != expected.deviceType || !slices.Equal(def.operationCmds(), expected.commands) {
			t.Fatalf("definition for %s = %#v, commands %v, want abbr=%s deviceType=%d commands=%v",
				def.Type, def, def.operationCmds(), expected.abbr, expected.deviceType, expected.commands)
		}
	}
	if _, ok := definitionByType("oe"); ok {
		t.Fatal("common protocol must not define optical/electro-optical oe device")
	}
	unsupported := []int{30000, 30001, 30002, 30003, 60004, 60005, 60100, 60101}
	for _, def := range deviceDefinitions {
		for _, cmd := range unsupported {
			if def.supportsOperationCmd(cmd) {
				t.Fatalf("%s unexpectedly supports non-intersection command %d", def.Type, cmd)
			}
		}
	}
}

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

func TestCommonV13TopicsUseBridgeNamespace(t *testing.T) {
	settings := model.LingyunSettingsWithDefaults(model.LingyunSettings{
		Enabled:      true,
		Broker:       "127.0.0.1:1883",
		ProviderCode: " DPTEST ",
		Devices: []model.LingyunDeviceSettings{
			{Type: model.LingyunDeviceAOA, Enabled: true, DeviceID: " AOA01 ", DeviceName: "AOA"},
		},
	})
	def, ok := definitionByType(model.LingyunDeviceAOA)
	if !ok {
		t.Fatal("missing AOA definition")
	}
	device, ok := lingyunDevice(settings, model.LingyunDeviceAOA)
	if !ok {
		t.Fatal("missing AOA device")
	}
	want := map[string]string{
		"device":              "bridge/DPTEST/device/aoa/AOA01",
		"device_state":        "bridge/DPTEST/device_state/aoa/AOA01",
		"device_data":         "bridge/DPTEST/device_data/aoa/AOA01",
		"device_control":      "bridge/DPTEST/device_control/aoa/AOA01",
		"device_control_resp": "bridge/DPTEST/device_control_resp/aoa/AOA01",
	}
	for name, expected := range want {
		topic := deviceTopic(settings, def, device, name)
		if topic != expected {
			t.Fatalf("%s topic = %q, want %q", name, topic, expected)
		}
		if strings.HasPrefix(topic, "platform/") || strings.Contains(topic, "/oe/") {
			t.Fatalf("common topic leaked non-intersection namespace/device: %s", topic)
		}
	}
	if _, _, ok := definitionByTopic("DPTEST", "platform/DPTEST/control/aoa/AOA01", settings); ok {
		t.Fatal("platform control topic must not match common protocol")
	}
	if _, _, ok := definitionByTopic("DPTEST", "bridge/DPTEST/device_control/oe/OE01", settings); ok {
		t.Fatal("oe control topic must not match common protocol")
	}
}

func TestBuildCommonV13RegistrationPayloads(t *testing.T) {
	settings := model.LingyunSettingsWithDefaults(model.LingyunSettings{
		ProviderCode: "DPTEST",
		Devices: []model.LingyunDeviceSettings{
			{Type: model.LingyunDeviceAOA, Enabled: true, DeviceID: "AOA01", DeviceName: "AOA", DetectionFrequency: []string{"2.4GHz", "5.8GHz"}},
			{Type: model.LingyunDeviceDCD, Enabled: true, DeviceID: "DCD01", DeviceName: "DCD"},
			{Type: model.LingyunDeviceRemoteID, Enabled: true, DeviceID: "RID01", DeviceName: "RID"},
			{Type: model.LingyunDeviceInterference, Enabled: true, DeviceID: "IFR01", DeviceName: "IFR", Bands: []string{"2.4G", "5.8G"}, InterferenceTypes: []int{0, 1, 2}},
		},
	})
	want := map[string]struct {
		deviceType int
		commands   []int
	}{
		model.LingyunDeviceAOA:          {deviceType: 9, commands: []int{90000}},
		model.LingyunDeviceDCD:          {deviceType: 11, commands: []int{110000}},
		model.LingyunDeviceRemoteID:     {deviceType: 102, commands: []int{1020000}},
		model.LingyunDeviceInterference: {deviceType: 6, commands: []int{60001, 60002, 60003}},
	}
	for _, device := range settings.Devices {
		def, ok := definitionByType(device.Type)
		if !ok {
			t.Fatalf("missing definition for %s", device.Type)
		}
		payload := buildDevicePayload(settings, def, device, 1)
		expected := want[device.Type]
		if payload.ProviderCode != "DPTEST" ||
			payload.DeviceID != device.DeviceID ||
			payload.DeviceName != def.Abbr+"-"+device.DeviceID ||
			payload.DeviceSpec.DevModel != def.Abbr+"-"+device.DeviceID+"型号" ||
			payload.DeviceSpec.DevMfr != def.Abbr+"-"+device.DeviceID+"厂商" ||
			payload.DeviceType != expected.deviceType ||
			payload.ProtocolVersion != model.DefaultLingyunProtocolVersion ||
			!slices.Equal(payload.SupFun, expected.commands) {
			t.Fatalf("registration payload for %s = %#v, want generated name/spec, type=%d commands=%v",
				device.Type, payload, expected.deviceType, expected.commands)
		}
		if device.Type == model.LingyunDeviceInterference {
			extension, ok := payload.Extension.(interferenceDeviceExtension)
			if !ok {
				t.Fatalf("IFR extension = %#v, want interferenceDeviceExtension", payload.Extension)
			}
			if !slices.Equal(extension.Bands, []string{"2.4G", "5.8G"}) ||
				!slices.Equal(extension.InterferenceTypes, []int{0, 1, 2}) ||
				extension.AntennaType != 1 ||
				extension.ActiveAntennaType != 1 {
				t.Fatalf("IFR extension = %#v", extension)
			}
			continue
		}
		extension, ok := payload.Extension.(detectionDeviceExtension)
		if !ok {
			t.Fatalf("%s extension = %#v, want detectionDeviceExtension", device.Type, payload.Extension)
		}
		if extension.DetectionRange <= 0 ||
			extension.BandWidth != model.DefaultLingyunBandWidth ||
			extension.HorizontalCoverageStartAngle != 0 ||
			extension.HorizontalCoverageEndAngle != 360 {
			t.Fatalf("%s detection extension = %#v", device.Type, extension)
		}
	}
}

func TestBuildRegistrationPayloadOverridesReportedIdentityFields(t *testing.T) {
	settings := model.LingyunSettingsWithDefaults(model.LingyunSettings{
		ProviderCode: "DPTEST",
		Devices: []model.LingyunDeviceSettings{
			{
				Type:       model.LingyunDeviceDCD,
				Enabled:    true,
				DeviceID:   " DCD01 ",
				DeviceName: "legacy name",
				DeviceSpec: model.LingyunDeviceSpec{
					DevModel:   "legacy model",
					DevMfr:     "legacy manufacturer",
					DevSN:      "legacy-sn",
					DevHWVer:   "hw-1",
					DevSoftVer: "soft-1",
					InstLoc:    "roof",
				},
			},
		},
	})
	def, ok := definitionByType(model.LingyunDeviceDCD)
	if !ok {
		t.Fatal("missing DCD definition")
	}
	device, ok := lingyunDevice(settings, model.LingyunDeviceDCD)
	if !ok {
		t.Fatal("missing DCD device")
	}

	payload := buildDevicePayload(settings, def, device, 1)
	if payload.DeviceName != "dcd-DCD01" ||
		payload.DeviceSpec.DevModel != "dcd-DCD01型号" ||
		payload.DeviceSpec.DevMfr != "dcd-DCD01厂商" {
		t.Fatalf("reported identity = %q/%q/%q",
			payload.DeviceName,
			payload.DeviceSpec.DevModel,
			payload.DeviceSpec.DevMfr)
	}
	if payload.DeviceSpec.DevSN != "legacy-sn" ||
		payload.DeviceSpec.DevHWVer != "hw-1" ||
		payload.DeviceSpec.DevSoftVer != "soft-1" ||
		payload.DeviceSpec.InstLoc != "roof" {
		t.Fatalf("non-identity spec fields were not preserved: %#v", payload.DeviceSpec)
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
