package model

import (
	"encoding/json"
	"math"
	"net"
	"slices"
	"strings"
	"testing"
)

func TestUserSettingsWithDefaultsAddsPositionExpireSeconds(t *testing.T) {
	settings := UserSettingsWithDefaults(UserSettings{})
	if settings.PositionExpireSeconds == nil || *settings.PositionExpireSeconds != DefaultPositionExpireSeconds {
		t.Fatalf("position expire seconds = %#v, want %d", settings.PositionExpireSeconds, DefaultPositionExpireSeconds)
	}
	if settings.WarningZoneEnabled == nil || *settings.WarningZoneEnabled {
		t.Fatalf("warning zone enabled = %#v, want false", settings.WarningZoneEnabled)
	}
	if settings.WarningZoneRadiusMeters == nil || *settings.WarningZoneRadiusMeters != DefaultWarningZoneRadiusMeters {
		t.Fatalf("warning zone radius = %#v, want %.0f", settings.WarningZoneRadiusMeters, DefaultWarningZoneRadiusMeters)
	}
}

func TestUserSettingsPositionExpireSeconds(t *testing.T) {
	custom := 12
	if got := UserSettingsPositionExpireSeconds(UserSettings{PositionExpireSeconds: &custom}); got != custom {
		t.Fatalf("custom position expire seconds = %d, want %d", got, custom)
	}
	if got := UserSettingsPositionExpireSeconds(UserSettings{}); got != DefaultPositionExpireSeconds {
		t.Fatalf("default position expire seconds = %d, want %d", got, DefaultPositionExpireSeconds)
	}
}

func TestLingyunSettingsWithDefaultsAddsLogicalDevices(t *testing.T) {
	settings := LingyunSettingsWithDefaults(LingyunSettings{})
	if settings.ProtocolVersion != DefaultLingyunProtocolVersion ||
		settings.PublishMinIntervalSeconds != DefaultLingyunPublishMinIntervalSec ||
		settings.RegisterIntervalSeconds != DefaultLingyunRegisterIntervalSec ||
		settings.StatusIntervalSeconds != DefaultLingyunStatusIntervalSec {
		t.Fatalf("settings defaults = %#v", settings)
	}
	if len(settings.Devices) != 4 {
		t.Fatalf("devices = %#v, want 4", settings.Devices)
	}
	want := []string{LingyunDeviceAOA, LingyunDeviceDCD, LingyunDeviceRemoteID, LingyunDeviceInterference}
	for index, deviceType := range want {
		if settings.Devices[index].Type != deviceType || !settings.Devices[index].Enabled {
			t.Fatalf("device[%d] = %#v, want enabled %s", index, settings.Devices[index], deviceType)
		}
		if settings.Devices[index].BandWidth != DefaultLingyunBandWidth {
			t.Fatalf("device[%d] bandwidth = %q", index, settings.Devices[index].BandWidth)
		}
	}
	if !slices.Equal(settings.Devices[0].DetectionFrequency, []string{"400MHz-8GHz"}) {
		t.Fatalf("AOA detectionFrequency = %#v", settings.Devices[0].DetectionFrequency)
	}
	if !slices.Equal(settings.Devices[1].DetectionFrequency, []string{"2.4GHz", "5.8GHz"}) {
		t.Fatalf("DCD detectionFrequency = %#v", settings.Devices[1].DetectionFrequency)
	}
	if !slices.Equal(settings.Devices[2].DetectionFrequency, []string{"2.4GHz", "5.8GHz"}) {
		t.Fatalf("RID detectionFrequency = %#v", settings.Devices[2].DetectionFrequency)
	}
	if settings.Devices[0].DetectionRange != 5000 {
		t.Fatalf("AOA detectionRange = %.0f, want 5000", settings.Devices[0].DetectionRange)
	}
	if settings.Devices[1].DetectionRange != 5000 {
		t.Fatalf("DCD detectionRange = %.0f, want 5000", settings.Devices[1].DetectionRange)
	}
	if settings.Devices[2].DetectionRange != 3000 {
		t.Fatalf("RID detectionRange = %.0f, want 3000", settings.Devices[2].DetectionRange)
	}
	if settings.Devices[3].CountermeasureRange != 3000 ||
		!slices.Equal(settings.Devices[3].Bands, []string{"433M", "915M", "1.2G", "1.4G", "1.5G", "2.4G", "5.2G", "5.8G"}) ||
		!slices.Equal(settings.Devices[3].InterferenceTypes, []int{0, 1, 2}) ||
		settings.Devices[3].AntennaType != 1 ||
		settings.Devices[3].ActiveAntennaType != 1 {
		t.Fatalf("IFR defaults = %#v", settings.Devices[3])
	}
}

func TestLingyunSettingsWithDefaultsAppliesReportedIdentityFields(t *testing.T) {
	settings := LingyunSettingsWithDefaults(LingyunSettings{
		Devices: []LingyunDeviceSettings{
			{
				Type:       LingyunDeviceAOA,
				DeviceID:   " AOA01 ",
				DeviceName: "custom name",
				DeviceSpec: LingyunDeviceSpec{
					DevModel: "custom model",
					DevMfr:   "custom manufacturer",
				},
			},
			{
				Type:     LingyunDeviceInterference,
				DeviceID: "IFR01",
			},
		},
	})

	if settings.Devices[0].DeviceName != "aoa-AOA01" ||
		settings.Devices[0].DeviceSpec.DevModel != "aoa-AOA01型号" ||
		settings.Devices[0].DeviceSpec.DevMfr != "aoa-AOA01厂商" {
		t.Fatalf("AOA reported identity = %q/%q/%q",
			settings.Devices[0].DeviceName,
			settings.Devices[0].DeviceSpec.DevModel,
			settings.Devices[0].DeviceSpec.DevMfr)
	}
	if settings.Devices[3].DeviceName != "ifr-IFR01" ||
		settings.Devices[3].DeviceSpec.DevModel != "ifr-IFR01型号" ||
		settings.Devices[3].DeviceSpec.DevMfr != "ifr-IFR01厂商" {
		t.Fatalf("IFR reported identity = %q/%q/%q",
			settings.Devices[3].DeviceName,
			settings.Devices[3].DeviceSpec.DevModel,
			settings.Devices[3].DeviceSpec.DevMfr)
	}
}

func TestLingyunSettingsWithDefaultsKeepsInterferenceOmnidirectional(t *testing.T) {
	settings := LingyunSettingsWithDefaults(LingyunSettings{
		Devices: []LingyunDeviceSettings{
			{
				Type:              LingyunDeviceInterference,
				AntennaType:       0,
				ActiveAntennaType: 0,
			},
		},
	})
	if settings.Devices[3].AntennaType != 1 || settings.Devices[3].ActiveAntennaType != 1 {
		t.Fatalf("IFR antenna defaults = %d/%d, want omnidirectional 1/1", settings.Devices[3].AntennaType, settings.Devices[3].ActiveAntennaType)
	}
}

func TestLingyunSettingsWithDefaultsMigratesLegacyDetectionFrequency(t *testing.T) {
	settings := LingyunSettingsWithDefaults(LingyunSettings{
		Devices: []LingyunDeviceSettings{
			{Type: LingyunDeviceAOA, DetectionFrequency: []string{"2.4GHz", "5.8GHz"}},
			{Type: LingyunDeviceRemoteID, DetectionFrequency: []string{"2.4GHz"}},
		},
	})
	if !slices.Equal(settings.Devices[0].DetectionFrequency, []string{"400MHz-8GHz"}) {
		t.Fatalf("AOA detectionFrequency = %#v", settings.Devices[0].DetectionFrequency)
	}
	if !slices.Equal(settings.Devices[2].DetectionFrequency, []string{"2.4GHz", "5.8GHz"}) {
		t.Fatalf("RID detectionFrequency = %#v", settings.Devices[2].DetectionFrequency)
	}
}

func TestLingyunSettingsWithDefaultsMigratesLegacyDetectionRange(t *testing.T) {
	settings := LingyunSettingsWithDefaults(LingyunSettings{
		Devices: []LingyunDeviceSettings{
			{Type: LingyunDeviceAOA, DetectionRange: 1000},
			{Type: LingyunDeviceDCD, DetectionRange: 1000},
			{Type: LingyunDeviceRemoteID, DetectionRange: 1000},
			{Type: LingyunDeviceInterference, CountermeasureRange: 1000},
		},
	})
	if settings.Devices[0].DetectionRange != 5000 {
		t.Fatalf("AOA detectionRange = %.0f, want 5000", settings.Devices[0].DetectionRange)
	}
	if settings.Devices[1].DetectionRange != 5000 {
		t.Fatalf("DCD detectionRange = %.0f, want 5000", settings.Devices[1].DetectionRange)
	}
	if settings.Devices[2].DetectionRange != 3000 {
		t.Fatalf("RID detectionRange = %.0f, want 3000", settings.Devices[2].DetectionRange)
	}
	if settings.Devices[3].CountermeasureRange != 3000 {
		t.Fatalf("IFR countermeasureRange = %.0f, want 3000", settings.Devices[3].CountermeasureRange)
	}
}

func TestLingyunSettingsWithGeneratedClientID(t *testing.T) {
	settings := LingyunSettingsWithGeneratedClientID(LingyunSettings{})
	if !strings.HasPrefix(settings.ClientID, DefaultLingyunClientIDPrefix) {
		t.Fatalf("client id = %q, want prefix %q", settings.ClientID, DefaultLingyunClientIDPrefix)
	}

	custom := LingyunSettingsWithGeneratedClientID(LingyunSettings{ClientID: " custom-client "})
	if custom.ClientID != "custom-client" {
		t.Fatalf("client id = %q, want custom-client", custom.ClientID)
	}
}

func TestLingyunSettingsWithDefaultsLocksProtocolVersion(t *testing.T) {
	settings := LingyunSettingsWithDefaults(LingyunSettings{ProtocolVersion: "V9.9"})
	if settings.ProtocolVersion != DefaultLingyunProtocolVersion {
		t.Fatalf("protocol version = %q, want %q", settings.ProtocolVersion, DefaultLingyunProtocolVersion)
	}
}

func TestLingyunSettingsWithDefaultsKeepsValidInstallMode(t *testing.T) {
	settings := LingyunSettingsWithDefaults(LingyunSettings{
		Devices: []LingyunDeviceSettings{
			{Type: LingyunDeviceAOA, InstallMode: 1},
			{Type: LingyunDeviceDCD, InstallMode: -1},
			{Type: LingyunDeviceRemoteID, InstallMode: 2},
		},
	})
	if settings.Devices[0].InstallMode != 1 {
		t.Fatalf("AOA installMode = %d, want 1", settings.Devices[0].InstallMode)
	}
	if settings.Devices[1].InstallMode != 0 {
		t.Fatalf("DCD installMode = %d, want 0", settings.Devices[1].InstallMode)
	}
	if settings.Devices[2].InstallMode != 0 {
		t.Fatalf("RID installMode = %d, want 0", settings.Devices[2].InstallMode)
	}
}

func TestLingyunDeviceSNFromHardwareAddr(t *testing.T) {
	got := lingyunDeviceSNFromHardwareAddr(net.HardwareAddr{0x00, 0x1a, 0x2b, 0x3c, 0x4d, 0x5e})
	want := DefaultLingyunDeviceSNPrefix + "001A2B3C4D5E"
	if got != want {
		t.Fatalf("SN = %q, want %q", got, want)
	}
	if got := lingyunDeviceSNFromHardwareAddr(net.HardwareAddr{0, 0, 0, 0, 0, 0}); got != "" {
		t.Fatalf("zero MAC SN = %q, want empty", got)
	}
}

func TestStableHardwareAddrFromInterfacesPrefersDeterministicGlobalAddr(t *testing.T) {
	got := stableHardwareAddrFromInterfaces([]net.Interface{
		{
			Name:         "local-lower",
			Flags:        net.FlagUp,
			HardwareAddr: net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
		},
		{
			Name:         "global-higher",
			Flags:        0,
			HardwareAddr: net.HardwareAddr{0x10, 0x00, 0x00, 0x00, 0x00, 0x02},
		},
		{
			Name:         "loopback",
			Flags:        net.FlagLoopback,
			HardwareAddr: net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		},
		{
			Name:         "global-lower",
			Flags:        net.FlagUp,
			HardwareAddr: net.HardwareAddr{0x10, 0x00, 0x00, 0x00, 0x00, 0x01},
		},
	})
	want := net.HardwareAddr{0x10, 0x00, 0x00, 0x00, 0x00, 0x01}
	if !slices.Equal(got, want) {
		t.Fatalf("stable hardware addr = %s, want %s", got, want)
	}
}

func TestLingyunSettingsWithDeviceIdentityFillsEmptyDeviceIDAndKeepsCustom(t *testing.T) {
	const identity = "drone-management-001A2B3C4D5E"
	settings := LingyunSettingsWithDeviceIdentity(LingyunSettings{
		Devices: []LingyunDeviceSettings{
			{
				Type:     LingyunDeviceAOA,
				DeviceID: "custom-aoa",
				DeviceSpec: LingyunDeviceSpec{
					DevSN: "custom-sn",
				},
			},
			{
				Type: LingyunDeviceDCD,
			},
		},
	}, identity)

	for _, device := range settings.Devices {
		if device.Type == LingyunDeviceAOA {
			if device.DeviceID != "custom-aoa" || device.DeviceSpec.DevSN != "custom-sn" ||
				device.DeviceName != "aoa-custom-aoa" ||
				device.DeviceSpec.DevModel != "aoa-custom-aoa型号" ||
				device.DeviceSpec.DevMfr != "aoa-custom-aoa厂商" {
				t.Fatalf("AOA custom identity = %#v", device)
			}
			continue
		}
		if device.DeviceID != identity || device.DeviceSpec.DevSN != identity ||
			device.DeviceName == "" || device.DeviceSpec.DevModel == "" || device.DeviceSpec.DevMfr == "" {
			t.Fatalf("device %s default identity = %#v, want identity %q", device.Type, device, identity)
		}
	}
}

func TestLingyunSettingsWithDeviceLocationOverridesLogicalDevices(t *testing.T) {
	settings := LingyunSettingsWithDefaults(LingyunSettings{
		Devices: []LingyunDeviceSettings{
			{
				Type:            LingyunDeviceAOA,
				DeviceLongitude: 1,
				DeviceLatitude:  2,
			},
			{
				Type:            LingyunDeviceDCD,
				DeviceLongitude: 3,
				DeviceLatitude:  4,
			},
		},
	})

	settings = LingyunSettingsWithDeviceLocation(settings, &GeoPoint{
		Latitude:  39.1234,
		Longitude: 116.5678,
	})

	for _, device := range settings.Devices {
		if device.DeviceLongitude != 116.5678 || device.DeviceLatitude != 39.1234 {
			t.Fatalf("device %s location = %.4f/%.4f, want 116.5678/39.1234", device.Type, device.DeviceLongitude, device.DeviceLatitude)
		}
	}
}

func TestLingyunSettingsWithDeviceLocationSkipsInvalidPoint(t *testing.T) {
	settings := LingyunSettingsWithDefaults(LingyunSettings{
		Devices: []LingyunDeviceSettings{
			{
				Type:            LingyunDeviceAOA,
				DeviceLongitude: 116.1,
				DeviceLatitude:  39.9,
			},
		},
	})

	got := LingyunSettingsWithDeviceLocation(settings, &GeoPoint{
		Latitude:  math.NaN(),
		Longitude: 116.5678,
	})

	if got.Devices[0].DeviceLongitude != 116.1 || got.Devices[0].DeviceLatitude != 39.9 {
		t.Fatalf("invalid point changed location to %.4f/%.4f", got.Devices[0].DeviceLongitude, got.Devices[0].DeviceLatitude)
	}
}

func TestLingyunSettingsMarshalIncludesEmptyAndZeroFields(t *testing.T) {
	settings := LingyunSettingsWithDefaults(LingyunSettings{
		ClientID: "client-1",
		Devices: []LingyunDeviceSettings{
			{
				Type:     LingyunDeviceAOA,
				DeviceID: "device-1",
				DeviceSpec: LingyunDeviceSpec{
					DevSN: "device-1",
				},
			},
		},
	})
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal lingyun settings: %v", err)
	}
	body := string(data)
	for _, field := range []string{
		`"broker":""`,
		`"username":""`,
		`"password":""`,
		`"providerCode":""`,
		`"deviceLongitude":0`,
		`"deviceLatitude":0`,
		`"deviceAltitude":0`,
		`"installMode":0`,
		`"horizontalCoverageStartAngle":0`,
		`"devMfr":""`,
		`"instLoc":""`,
	} {
		if !strings.Contains(body, field) {
			t.Fatalf("marshaled settings missing %s: %s", field, body)
		}
	}
}
