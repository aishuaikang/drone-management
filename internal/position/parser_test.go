package position

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"drone-management/internal/diddecrypt"
	"drone-management/internal/model"
	"drone-management/internal/store"
)

func TestParseDeviceStatus(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	parsed, ok := ParseLine("device_status,33.33,46.14,121.470000,31.230000,1", now)
	if !ok || parsed.Location == nil {
		t.Fatalf("parsed = %#v, ok = %v", parsed, ok)
	}
	location := parsed.Location
	if !location.Valid || !location.Locked || location.Point == nil {
		t.Fatalf("location = %#v", location)
	}
	if location.Point.Latitude != 31.23 || location.Point.Longitude != 121.47 {
		t.Fatalf("point = %#v", location.Point)
	}
}

func TestParseRID(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	line := "RID,1581F4XFC237300753P5,DJI Mini4 pro,37.743615,-122.373298,37.743652,-122.373314,0.00,137.00,2437.0,0.0,-82,29658"
	parsed, ok := ParseLine(line, now)
	if !ok || parsed.Position == nil {
		t.Fatalf("parsed = %#v, ok = %v", parsed, ok)
	}
	target := parsed.Position
	if target.Serial != "1581F4XFC237300753P5" || target.Model != "DJI Mini4 pro" {
		t.Fatalf("target identity = %#v", target)
	}
	if target.LastRecord.Model != "DJI Mini4 pro" {
		t.Fatalf("last record model = %q, want DJI Mini4 pro", target.LastRecord.Model)
	}
	if target.Drone == nil || target.Home == nil || target.Drone.Latitude != 37.743652 {
		t.Fatalf("target coords = %#v", target)
	}
	if target.Drone.Longitude != -122.373314 || target.Home.Longitude != -122.373298 {
		t.Fatalf("target longitudes = %#v", target)
	}
	if target.Frequency != 2437 || target.RSSI != -82 {
		t.Fatalf("target radio = %#v", target)
	}
	if target.Height == nil || *target.Height != 0 {
		t.Fatalf("height = %#v, want 0", target.Height)
	}
	if target.Speed == nil || *target.Speed != 0 {
		t.Fatalf("speed = %#v, want 0", target.Speed)
	}
}

func TestParseRIDGB46750Accepts18FieldsAndPreservesUnknownAircraftPosition(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	line := "RID_GB46750,1581FA6QC259B00C67T3,,1,31.158553,121.699907,,,,-63.50,0.0,0.0,,1,2437.0,-91,,3481440;"
	parsed, ok := ParseLine(line, now)
	if !ok || parsed.ParseError != "" || parsed.Position == nil {
		t.Fatalf("parsed = %#v, ok = %v", parsed, ok)
	}
	target := parsed.Position
	if target.Serial != "1581FA6QC259B00C67T3" || target.Model != "RID_GB46750" {
		t.Fatalf("identity = %#v", target)
	}
	if target.Drone != nil || target.Pilot != nil {
		t.Fatalf("unknown aircraft position became coordinates: drone=%#v pilot=%#v", target.Drone, target.Pilot)
	}
	if target.Home == nil || target.Home.Latitude != 31.158553 || target.Home.Longitude != 121.699907 {
		t.Fatalf("home = %#v", target.Home)
	}
	if target.Height != nil || target.Altitude == nil || *target.Altitude != -63.5 {
		t.Fatalf("height/altitude = %#v/%#v", target.Height, target.Altitude)
	}
	if target.Speed == nil || *target.Speed != 0 || target.Frequency != 2437 || target.RSSI != -91 {
		t.Fatalf("speed/radio = %#v", target)
	}
	if !target.LastSeen.Equal(now) {
		t.Fatalf("LastSeen = %v, want server receive time %v", target.LastSeen, now)
	}
	var data map[string]string
	if err := json.Unmarshal(target.LastRecord.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["uavLat"] != "" || data["gbTimestampMs"] != "" || data["recvTimeMs"] != "3481440" {
		t.Fatalf("last record data = %#v", data)
	}
}

func TestParseRIDGB46750AcceptsOptionalProductModel(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	line := "RID_GB46750,1581FA6QC259B00C67T3,,1,31.158521,121.699861,,,,-40.00,0.0,0.0,,1,2437.0,-34,,905570,DJI Neo 2;"
	parsed, ok := ParseLine(line, now)
	if !ok || parsed.ParseError != "" || parsed.Position == nil {
		t.Fatalf("parsed = %#v, ok = %v", parsed, ok)
	}
	if parsed.Position.Model != "DJI Neo 2" || parsed.Position.LastRecord.Model != "DJI Neo 2" {
		t.Fatalf("model = %#v", parsed.Position)
	}
}

func TestParseRIDGB46750MapsFullRecord(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	line := "RID_GB46750,product,tail,2,31.1,121.2,31.3,121.4,12.5,43.5,8.5,-1.5,270,3,5800,-72,1000,2000,Model X;"
	parsed, ok := ParseLine(line, now)
	if !ok || parsed.ParseError != "" || parsed.Position == nil {
		t.Fatalf("parsed = %#v, ok = %v", parsed, ok)
	}
	target := parsed.Position
	if target.Drone == nil || target.Drone.Latitude != 31.3 || target.Drone.Longitude != 121.4 {
		t.Fatalf("drone = %#v", target.Drone)
	}
	if target.Home == nil || target.Home.Latitude != 31.1 || target.Home.Longitude != 121.2 {
		t.Fatalf("home = %#v", target.Home)
	}
	if target.Height == nil || *target.Height != 12.5 || target.Altitude == nil || *target.Altitude != 43.5 || target.Speed == nil || *target.Speed != 8.5 {
		t.Fatalf("telemetry = %#v", target)
	}
	var data map[string]string
	if err := json.Unmarshal(target.LastRecord.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["registrationIdTail"] != "tail" || data["verticalSpeed"] != "-1.5" || data["trackAngle"] != "270" || data["status"] != "3" {
		t.Fatalf("last record data = %#v", data)
	}
}

func TestParseRIDGB46750ReportsInvalidFields(t *testing.T) {
	parsed, ok := ParseLine("RID_GB46750,product,tail,2,31.1,121.2,31.3,bad,12.5,43.5,8.5,-1.5,270,3,5800,-72,1000,2000;", time.Now())
	if !ok || parsed.ParseError == "" || parsed.Position != nil {
		t.Fatalf("parsed = %#v, ok = %v", parsed, ok)
	}
}

func TestParseDeviceInfo(t *testing.T) {
	parsed, ok := ParseLine("device_info,DDM-P1,2026-07-27 15:30:00;", time.Now())
	if !ok || parsed.ParseError != "" || parsed.DeviceInfo == nil {
		t.Fatalf("parsed = %#v, ok = %v", parsed, ok)
	}
	if parsed.DeviceInfo.DeviceName != "DDM-P1" || parsed.DeviceInfo.FirmwareTime != "2026-07-27 15:30:00" {
		t.Fatalf("device info = %#v", parsed.DeviceInfo)
	}
}

func TestParseDJIOEncryptedRawEmitsDIDPacket(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	line := "dji_O,4,5816.5,-81,dji,,0.000000,0.000000,0.000000,0.000000,0.000000,0.000000,0.00|0.00,0.00|0.00|0.00,0;" +
		testDIDAirData("80", "01fa261e")

	parsed, ok := ParseLine(line, now)
	if !ok {
		t.Fatal("ParseLine() returned ok=false")
	}
	if parsed.Position == nil {
		t.Fatal("position = nil, want fallback target for encrypted-only raw frame")
	}
	if parsed.Position.Model != diddecrypt.FallbackModel || parsed.Position.Serial != "01fa261e" || parsed.Position.Cracked {
		t.Fatalf("fallback target = %#v", parsed.Position)
	}
	if parsed.Position.Frequency != 5816.5 || parsed.Position.RSSI != -81 {
		t.Fatalf("fallback radio = %#v", parsed.Position)
	}
	if parsed.Position.Drone != nil || parsed.Position.Pilot != nil || parsed.Position.Home != nil {
		t.Fatalf("fallback coordinates = %#v/%#v/%#v, want nil", parsed.Position.Drone, parsed.Position.Pilot, parsed.Position.Home)
	}
	if parsed.EncryptedDID == nil {
		t.Fatalf("encrypted DID packet was not extracted")
	}
	if parsed.EncryptedDID.EncryptedID != "01fa261e" {
		t.Fatalf("encrypted id = %q, want 01fa261e", parsed.EncryptedDID.EncryptedID)
	}
	if parsed.EncryptedDID.Device != "01fa261e" {
		t.Fatalf("device fallback = %q, want encrypted id", parsed.EncryptedDID.Device)
	}
	if parsed.EncryptedDID.Freq != 5816.5 || parsed.EncryptedDID.RSSI != -81 {
		t.Fatalf("radio = %#v", parsed.EncryptedDID)
	}
}

func TestParseDJIOEncryptedRawPadsSingleNibbleAirDataBytes(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	line := "dji_O,4,5816.5,-81,dji,,0.000000,0.000000,0.000000,0.000000,0.000000,0.000000,0.00|0.00,0.00|0.00|0.00,0;" +
		testUnpaddedDIDAirData("80", "01fa261e")

	parsed, ok := ParseLine(line, now)
	if !ok || parsed.EncryptedDID == nil {
		t.Fatalf("parsed = %#v, ok = %v", parsed, ok)
	}
	if parsed.EncryptedDID.EncryptedID != "01fa261e" {
		t.Fatalf("encrypted id = %q, want 01fa261e", parsed.EncryptedDID.EncryptedID)
	}
	if len(parsed.EncryptedDID.Bytes) != 352 {
		t.Fatalf("packet hex len = %d, want 352", len(parsed.EncryptedDID.Bytes))
	}
}

func TestParseDJIOEncryptedRawPreserves180Bytes(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	airData := testDIDAirData("80", "01fa261e") + ",0xaa,0xbb,0xcc,0xdd"
	line := "dji_O,4,5816.5,-81,dji,,0.000000,0.000000,0.000000,0.000000,0.000000,0.000000,0.00|0.00,0.00|0.00|0.00,0;" + airData
	parsed, ok := ParseLine(line, now)
	if !ok || parsed.ParseError != "" || parsed.EncryptedDID == nil || parsed.Position == nil {
		t.Fatalf("parsed = %#v, ok = %v", parsed, ok)
	}
	if len(parsed.EncryptedDID.Bytes) != 360 {
		t.Fatalf("decrypt payload hex len = %d, want 360", len(parsed.EncryptedDID.Bytes))
	}
	if !strings.HasSuffix(parsed.EncryptedDID.Bytes, "aabbccdd") {
		t.Fatalf("decrypt payload tail = %q", parsed.EncryptedDID.Bytes[len(parsed.EncryptedDID.Bytes)-8:])
	}
	if parsed.EncryptedDID.EncryptedID != "01fa261e" {
		t.Fatalf("encrypted id = %q", parsed.EncryptedDID.EncryptedID)
	}
	var data map[string]string
	if err := json.Unmarshal(parsed.Position.LastRecord.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["airData"] != airData {
		t.Fatalf("full air data was not preserved: got len %d, want len %d", len(data["airData"]), len(airData))
	}
}

func TestParseDJIOEncryptedKeyRawEmitsDIDPacket(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	line := "dji_O,4,5796.5,-91,dji(afe5b174),,0.000000,0.000000,0.000000,0.000000,0.000000,0.000000,0.00|0.00,0.00|0.00|0.00,1970-01-01 08:00:00,;" +
		testDIDAirDataWithMagic("a3", "43525950", "afe5b174")

	parsed, ok := ParseLine(line, now)
	if !ok || parsed.EncryptedDID == nil {
		t.Fatalf("parsed = %#v, ok = %v", parsed, ok)
	}
	if parsed.EncryptedDID.EncryptedID != "afe5b174" {
		t.Fatalf("encrypted id = %q, want afe5b174", parsed.EncryptedDID.EncryptedID)
	}
}

func TestParseDJIO(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name         string
		line         string
		wantSerial   string
		wantModel    string
		wantAltitude float64
		wantHeight   float64
		wantSpeedMin float64
	}{
		{
			name:         "unencrypted position frame",
			line:         "dji_O,2/3,5776.5,-81,DJI Mini 3 pro,F4XFC237300753P5,121.664104,31.172048,121.699656,31.158675,121.699673,31.158600,14.90|110.60,965.00|-432.00|0.00,1744703230504;0x6d",
			wantSerial:   "F4XFC237300753P5",
			wantModel:    "DJI Mini 3 pro",
			wantAltitude: 149,
			wantHeight:   110.6,
			wantSpeedMin: 10,
		},
		{
			name:         "encrypted decoded position frame",
			line:         "dji_O,4,5816.5,-82,Mini 4 Pro(93),F6Z9C251E003BRXP,121.677225,31.164733,121.699702,31.158677,121.699794,31.158625,160|119.800000,2.090000|-5.920000|0.030000,2025-10-22 16:41:47,1180663215273172992;0x80",
			wantSerial:   "F6Z9C251E003BRXP",
			wantModel:    "Mini 4 Pro(93)",
			wantAltitude: 160,
			wantHeight:   119.8,
			wantSpeedMin: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, ok := ParseLine(tt.line, now)
			if !ok || parsed.Position == nil {
				t.Fatalf("parsed = %#v, ok = %v", parsed, ok)
			}
			target := parsed.Position
			if target.Serial != tt.wantSerial || target.Model != tt.wantModel {
				t.Fatalf("target = %#v", target)
			}
			if tt.wantAltitude != 0 && (target.Altitude == nil || *target.Altitude != tt.wantAltitude) {
				t.Fatalf("altitude = %#v, want %v", target.Altitude, tt.wantAltitude)
			}
			if tt.wantHeight != 0 && (target.Height == nil || *target.Height != tt.wantHeight) {
				t.Fatalf("height = %#v, want %v", target.Height, tt.wantHeight)
			}
			if tt.wantSpeedMin != 0 && (target.Speed == nil || *target.Speed < tt.wantSpeedMin) {
				t.Fatalf("speed = %#v, want >= %v", target.Speed, tt.wantSpeedMin)
			}
		})
	}
}

func TestParseDJIOSerialOnlyFrameEmitsTargetWithoutTelemetry(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	line := "dji_O,2/3,5776.5,-79,dji,F4XFC237300753P5,0.000000,0.000000,0.000000,0.000000,0.000000,0.000000,0.00|0.00,0.00|0.00|0.00,2026-07-27 15:30:00;0x6d"
	parsed, ok := ParseLine(line, now)
	if !ok || parsed.ParseError != "" || parsed.Position == nil {
		t.Fatalf("parsed = %#v, ok = %v", parsed, ok)
	}
	target := parsed.Position
	if target.Serial != "F4XFC237300753P5" || target.Model != "dji" || target.Frequency != 5776.5 || target.RSSI != -79 {
		t.Fatalf("identity/radio = %#v", target)
	}
	if target.Drone != nil || target.Pilot != nil || target.Home != nil || target.Height != nil || target.Altitude != nil || target.Speed != nil {
		t.Fatalf("serial-only telemetry = %#v", target)
	}
	if parsed.EncryptedDID != nil {
		t.Fatalf("encrypted DID = %#v, want nil", parsed.EncryptedDID)
	}
}

func TestParseDJIOUsesDocumentLongitudeLatitudeOrder(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	line := "dji_O,2/3,5776.5,-81,DJI Test,SN123,80.123456,31.172048,80.223456,31.158675,80.323456,31.158600,0.00|0.00,0.00|0.00|0.00,1744703230504;0x6d"
	parsed, ok := ParseLine(line, now)
	if !ok || parsed.Position == nil {
		t.Fatalf("parsed = %#v, ok = %v", parsed, ok)
	}
	target := parsed.Position
	if target.Drone == nil || target.Drone.Latitude != 31.172048 || target.Drone.Longitude != 80.123456 {
		t.Fatalf("drone point = %#v", target.Drone)
	}
	if target.Pilot == nil || target.Pilot.Latitude != 31.158675 || target.Pilot.Longitude != 80.223456 {
		t.Fatalf("pilot point = %#v", target.Pilot)
	}
	if target.Home == nil || target.Home.Latitude != 31.1586 || target.Home.Longitude != 80.323456 {
		t.Fatalf("home point = %#v", target.Home)
	}
}

func TestParseDJIOKeepsZeroTelemetryValues(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	line := "dji_O,2/3,5776.5,-79,dji,F4XFC237300753P5,121.664104,31.172048,121.699656,31.158675,121.699673,31.158600,0.00|0.00,0.00|0.00|0.00,1744703230504;0x6d"
	parsed, ok := ParseLine(line, now)
	if !ok || parsed.Position == nil {
		t.Fatalf("parsed = %#v, ok = %v", parsed, ok)
	}
	target := parsed.Position
	if target.Altitude == nil || *target.Altitude != 0 {
		t.Fatalf("altitude = %#v, want 0", target.Altitude)
	}
	if target.Height == nil || *target.Height != 0 {
		t.Fatalf("height = %#v, want 0", target.Height)
	}
	if target.Speed == nil || *target.Speed != 0 {
		t.Fatalf("speed = %#v, want 0", target.Speed)
	}
}

func TestParseRIDRejectsZeroCoordinatesForListDisplay(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	line := "RID,serial,,0.000000,0.000000,0.000000,0.000000,0.00,137.00,2437.0,0.0,-82,29658"
	parsed, ok := ParseLine(line, now)
	if !ok || parsed.Position == nil {
		t.Fatalf("parsed = %#v, ok = %v", parsed, ok)
	}
	if parsed.Position.Drone != nil || parsed.Position.Home != nil {
		t.Fatalf("zero coordinates were kept: %#v", parsed.Position)
	}
}

func TestParseDJIOZeroTelemetryIsTreatedAsSerialOnly(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	line := "dji_O,2/3,5776.5,-79,dji,F4XFC237300753P5,0.000000,0.000000,0.000000,0.000000,0.000000,0.000000,0.00|0.00,0.00|0.00|0.00,1744703230504;0x6d"
	parsed, ok := ParseLine(line, now)
	if !ok || parsed.Position == nil {
		t.Fatalf("parsed = %#v, ok = %v", parsed, ok)
	}
	if parsed.Position.Drone != nil || parsed.Position.Pilot != nil || parsed.Position.Home != nil {
		t.Fatalf("zero telemetry created coordinates: %#v", parsed.Position)
	}
	if parsed.Position.Height != nil || parsed.Position.Altitude != nil || parsed.Position.Speed != nil {
		t.Fatalf("zero telemetry created scalar values: %#v", parsed.Position)
	}
}

func TestServiceReceivesTCP(t *testing.T) {
	port := freeTCPPort(t)
	state := store.New(10, 10)
	service := NewService(state, Options{Host: "127.0.0.1", Port: port})
	ctx, cancel := contextWithTimeout(t, time.Second)
	defer cancel()
	go service.Run(ctx)
	waitFor(t, time.Second, func() bool { return service.Status().Listening })

	conn, err := net.Dial("tcp", service.Address())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	line := "RID,1581F4XFC237300753P5,,37.743615,-122.373298,37.743652,-122.373314,0.00,137.00,2437.0,0.0,-82,29658\n"
	_, _ = conn.Write([]byte(line[:30]))
	_, _ = conn.Write([]byte(line[30:]))
	_ = conn.Close()

	waitFor(t, time.Second, func() bool { return len(state.Positions(10)) == 1 })
}

func TestServiceReceivesUDPDatagrams(t *testing.T) {
	tcpPort := freeTCPPort(t)
	udpPort := freeUDPPort(t)
	state := store.New(10, 10)
	service := NewService(state, Options{
		Host:       "127.0.0.1",
		Port:       tcpPort,
		UDPEnabled: true,
		UDPPort:    udpPort,
	})
	ctx, cancel := contextWithTimeout(t, 2*time.Second)
	defer cancel()
	go service.Run(ctx)
	waitFor(t, time.Second, func() bool {
		status := service.Status()
		return status.Listening && status.UDPListening
	})

	conn, err := net.Dial("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(udpPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	first := "RID_GB46750,udp-1,,1,31.1,121.1,31.2,121.2,10,20,3,1,90,1,2437,-60,100,101;"
	if _, err := conn.Write([]byte(first)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return len(state.Positions(10)) == 1 })

	multiple := "RID_GB46750,udp-2,,1,31.1,121.1,31.2,121.2,10,20,3,1,90,1,2437,-61,100,102;\r\n" +
		"RID_GB46750,udp-3,,1,31.1,121.1,31.2,121.2,10,20,3,1,90,1,2437,-62,100,103;"
	if _, err := conn.Write([]byte(multiple)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return len(state.Positions(10)) == 3 })
	if status := service.Status(); status.UDPSourceAddress == "" || status.UDPPort != udpPort || !status.UDPEnabled || !status.UDPSourceActive || status.UDPLastMessageAt == nil {
		t.Fatalf("UDP status = %#v", status)
	}
}

func TestServiceUDPSourceActivityExpires(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	service := NewService(store.New(10, 10), Options{
		Host:                  "127.0.0.1",
		Port:                  10007,
		UDPEnabled:            true,
		UDPPort:               10007,
		UDPSourceActiveWindow: 5 * time.Second,
		Now:                   func() time.Time { return now },
	})
	service.recordUDPDatagram(&net.UDPAddr{IP: net.ParseIP("192.168.100.106"), Port: 52000})
	status := service.Status()
	if !status.UDPSourceActive || status.UDPLastMessageAt == nil || !status.UDPLastMessageAt.Equal(now) {
		t.Fatalf("active UDP status = %#v", status)
	}
	now = now.Add(6 * time.Second)
	status = service.Status()
	if status.UDPSourceActive || status.UDPLastMessageAt == nil {
		t.Fatalf("expired UDP status = %#v", status)
	}
}

func TestServiceUDPListenFailureDoesNotStopTCP(t *testing.T) {
	port := freeTCPPort(t)
	service := NewService(store.New(10, 10), Options{
		Host:              "127.0.0.1",
		Port:              port,
		UDPEnabled:        true,
		UDPPort:           freeUDPPort(t),
		BindRetryInterval: 10 * time.Millisecond,
		OpenPacketListener: func(string, string) (net.PacketConn, error) {
			return nil, errors.New("forced UDP bind failure")
		},
	})
	ctx, cancel := contextWithTimeout(t, time.Second)
	defer cancel()
	go service.Run(ctx)
	waitFor(t, time.Second, func() bool {
		status := service.Status()
		return status.Listening && strings.Contains(status.UDPListenError, "forced UDP bind failure")
	})

	conn, err := net.DialTimeout("tcp", service.Address(), time.Second)
	if err != nil {
		t.Fatalf("TCP listener stopped after UDP failure: %v", err)
	}
	_ = conn.Close()
}

func TestServiceUDPSkipsOversizedLine(t *testing.T) {
	state := store.New(10, 10)
	service := NewService(state, Options{Host: "127.0.0.1", Port: 10007, MaxLineBytes: 32})
	service.ingestUDPDatagram(strings.Repeat("x", 33))
	if items := state.Positions(10); len(items) != 0 {
		t.Fatalf("positions = %#v, want empty", items)
	}
}

func TestServiceSetPortRestartsListener(t *testing.T) {
	initialPort := freeTCPPort(t)
	nextPort := freeTCPPort(t)
	state := store.New(10, 10)
	service := NewService(state, Options{Host: "127.0.0.1", Port: initialPort})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go service.Run(ctx)
	waitFor(t, time.Second, func() bool { return service.Status().Listening })

	service.SetPort(nextPort)
	waitFor(t, time.Second, func() bool { return service.Status().Port == nextPort && service.Status().Listening })

	if conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(initialPort)), 50*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatalf("old port %d is still accepting connections", initialPort)
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(nextPort)), time.Second)
	if err != nil {
		t.Fatalf("new port dial error = %v", err)
	}
	_ = conn.Close()
}

func TestServiceTracksMultipleSourceConnections(t *testing.T) {
	state := store.New(10, 10)
	service := NewService(state, Options{Host: "127.0.0.1", Port: 10007})

	service.setSourceConnection(true, "192.168.100.10:10000", nil)
	service.setSourceConnection(true, "127.0.0.1:20000", nil)
	service.setSourceConnection(false, "127.0.0.1:20000", nil)

	status := service.Status()
	if !status.SourceConnected {
		t.Fatalf("SourceConnected = false, want true")
	}
	if status.ClientAddress != "192.168.100.10:10000" {
		t.Fatalf("ClientAddress = %q, want device address", status.ClientAddress)
	}
}

func TestServicePreservesDeviceLocationWhenGPSUnlocks(t *testing.T) {
	state := store.New(10, 10)
	service := NewService(state, Options{Host: "127.0.0.1", Port: 10007})

	service.IngestLine("device_status,33.33,46.14,121.470000,31.230000,1")
	location := state.DeviceLocation()
	if !location.Valid || !location.Locked || location.Point == nil {
		t.Fatalf("locked location = %#v", location)
	}

	service.IngestLine("device_status,33.00,46.00,0.000000,0.000000,0")
	location = state.DeviceLocation()
	if !location.Valid || location.Point == nil {
		t.Fatalf("unlocked location should keep last valid point: %#v", location)
	}
	if location.Locked {
		t.Fatalf("locked = true, want false")
	}
	if location.Point.Latitude != 31.23 || location.Point.Longitude != 121.47 {
		t.Fatalf("point = %#v", location.Point)
	}
}

func TestServiceMergesDJIOSerialOnlyFrameWithoutOverwritingTelemetry(t *testing.T) {
	state := store.New(10, 10)
	service := NewService(state, Options{Host: "127.0.0.1", Port: 10007})
	positionLine := "dji_O,2/3,5776.5,-81,DJI Mini 3 pro,F4XFC237300753P5,121.664104,31.172048,121.699656,31.158675,121.699673,31.158600,14.90|110.60,965.00|-432.00|0.00,1744703230504;0x6d"
	serialOnlyLine := "dji_O,2/3,5776.5,-79,dji,F4XFC237300753P5,0.000000,0.000000,0.000000,0.000000,0.000000,0.000000,0.00|0.00,0.00|0.00|0.00,2026-07-27 15:30:00;0x6d"

	service.IngestLine(positionLine)
	items := state.Positions(10)
	if len(items) != 1 {
		t.Fatalf("positions count = %d, want 1", len(items))
	}
	lastSeen := items[0].LastSeen
	hitCount := items[0].HitCount
	drone := *items[0].Drone
	altitude := *items[0].Altitude
	height := *items[0].Height
	speed := *items[0].Speed

	service.IngestLine(serialOnlyLine)
	items = state.Positions(10)
	if len(items) != 1 {
		t.Fatalf("positions count = %d, want 1", len(items))
	}
	if items[0].HitCount != hitCount+1 {
		t.Fatalf("hit count = %d, want %d", items[0].HitCount, hitCount+1)
	}
	if items[0].LastSeen.Before(lastSeen) {
		t.Fatalf("last seen = %v, want >= %v", items[0].LastSeen, lastSeen)
	}
	if items[0].LastRecord.Raw != serialOnlyLine {
		t.Fatalf("last raw = %q, want serial-only frame", items[0].LastRecord.Raw)
	}
	if items[0].Drone == nil || *items[0].Drone != drone || items[0].Altitude == nil || *items[0].Altitude != altitude || items[0].Height == nil || *items[0].Height != height || items[0].Speed == nil || *items[0].Speed != speed {
		t.Fatalf("existing telemetry was overwritten: %#v", items[0])
	}
}

func TestServiceExposesDeviceInfoInStatus(t *testing.T) {
	service := NewService(store.New(10, 10), Options{Host: "127.0.0.1", Port: 10007})
	service.IngestLine("device_info,DDM-P1,2026-07-27 15:30:00;")
	status := service.Status()
	if status.DeviceName != "DDM-P1" || status.FirmwareTime != "2026-07-27 15:30:00" || status.UpdatedAt == nil {
		t.Fatalf("status = %#v", status)
	}
}

func TestServiceDecryptsDJIOEncryptedRaw(t *testing.T) {
	state := store.New(10, 10)
	decoder := &fakeDIDDecoder{packets: make(chan diddecrypt.Packet, 1)}
	service := NewService(state, Options{
		Host:       "127.0.0.1",
		Port:       10007,
		DIDDecoder: decoder,
	})
	line := "dji_O,4,5816.5,-81,dji,,0.000000,0.000000,0.000000,0.000000,0.000000,0.000000,0.00|0.00,0.00|0.00|0.00,0;" +
		testDIDAirData("80", "01fa261e")

	service.IngestLine(line)

	waitFor(t, time.Second, func() bool {
		items := state.Positions(10)
		return len(items) == 1 && items[0].Cracked && items[0].Serial == "real-sn"
	})
	items := state.Positions(10)
	if items[0].Serial != "real-sn" || items[0].Source != "dji_O:4" || !items[0].Cracked {
		t.Fatalf("position = %#v", items[0])
	}
	if items[0].CorrelationID != "dji_O:4:01fa261e" {
		t.Fatalf("correlation id = %q", items[0].CorrelationID)
	}
	select {
	case packet := <-decoder.packets:
		if packet.EncryptedID != "01fa261e" {
			t.Fatalf("decoder packet = %#v", packet)
		}
	default:
		t.Fatal("fake decoder did not receive packet")
	}
}

func TestServiceSkipsDJIOFallbackAfterCorrelationCracked(t *testing.T) {
	state := store.New(10, 10)
	decoder := &oneShotDIDDecoder{packets: make(chan diddecrypt.Packet, 2)}
	service := NewService(state, Options{
		Host:       "127.0.0.1",
		Port:       10007,
		DIDDecoder: decoder,
	})
	line := "dji_O,4,5816.5,-81,dji,,0.000000,0.000000,0.000000,0.000000,0.000000,0.000000,0.00|0.00,0.00|0.00|0.00,0;" +
		testDIDAirData("80", "01fa261e")

	service.IngestLine(line)
	waitFor(t, time.Second, func() bool {
		items := state.Positions(10)
		return len(items) == 1 && items[0].Cracked && items[0].Serial == "real-sn"
	})

	service.IngestLine(line)
	waitFor(t, time.Second, func() bool {
		return decoder.Calls() >= 2
	})

	items := state.Positions(10)
	if len(items) != 1 {
		t.Fatalf("positions count = %d, want only decoded target", len(items))
	}
	if items[0].Serial != "real-sn" || items[0].Model == diddecrypt.FallbackModel || !items[0].Cracked {
		t.Fatalf("position after repeated encrypted frame = %#v", items[0])
	}
}

type fakeDIDDecoder struct {
	packets chan diddecrypt.Packet
}

func (f *fakeDIDDecoder) DecodeDID(
	_ context.Context,
	packet diddecrypt.Packet,
	raw string,
	receivedAt time.Time,
) (model.ScreenPositionTarget, bool) {
	f.packets <- packet
	return model.ScreenPositionTarget{
		Serial:           "real-sn",
		Model:            "DJI O4",
		Source:           "dji_O:4",
		Frequency:        packet.Freq,
		RSSI:             packet.RSSI,
		Device:           packet.Device,
		Drone:            &model.ScreenPositionPoint{Latitude: 31.2, Longitude: 121.4},
		TrajectoryHeight: float64PtrForTest(35),
		Cracked:          true,
		FirstSeen:        receivedAt,
		LastSeen:         receivedAt,
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "dji_O:4",
			ReceivedAt: receivedAt,
			Serial:     "real-sn",
			Model:      "DJI O4",
			Frequency:  packet.Freq,
			RSSI:       packet.RSSI,
			Raw:        raw,
			Cracked:    true,
		},
	}, true
}

type oneShotDIDDecoder struct {
	mu      sync.Mutex
	calls   int
	packets chan diddecrypt.Packet
}

func (f *oneShotDIDDecoder) DecodeDID(
	_ context.Context,
	packet diddecrypt.Packet,
	raw string,
	receivedAt time.Time,
) (model.ScreenPositionTarget, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.packets <- packet
	if f.calls > 1 {
		return model.ScreenPositionTarget{}, false
	}
	return model.ScreenPositionTarget{
		Serial:           "real-sn",
		Model:            "DJI O4",
		Source:           "dji_O:4",
		Frequency:        packet.Freq,
		RSSI:             packet.RSSI,
		Device:           packet.Device,
		Drone:            &model.ScreenPositionPoint{Latitude: 31.2, Longitude: 121.4},
		TrajectoryHeight: float64PtrForTest(35),
		Cracked:          true,
		FirstSeen:        receivedAt,
		LastSeen:         receivedAt,
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "dji_O:4",
			ReceivedAt: receivedAt,
			Serial:     "real-sn",
			Model:      "DJI O4",
			Frequency:  packet.Freq,
			RSSI:       packet.RSSI,
			Raw:        raw,
			Cracked:    true,
		},
	}, true
}

func (f *oneShotDIDDecoder) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func testDIDAirData(packetType string, encryptedID string) string {
	return testDIDAirDataWithMagic(packetType, "494e4650", encryptedID)
}

func testUnpaddedDIDAirData(packetType string, encryptedID string) string {
	hexStr := strings.ToLower(packetType + "10" + "494e4650" + encryptedID + strings.Repeat("00", 166))
	parts := make([]string, 0, len(hexStr)/2)
	for index := 0; index < len(hexStr); index += 2 {
		value := strings.TrimLeft(hexStr[index:index+2], "0")
		if value == "" {
			value = "0"
		}
		parts = append(parts, "0x"+value)
	}
	return strings.Join(parts, ",")
}

func testDIDAirDataWithMagic(packetType string, magic string, encryptedID string) string {
	secondByte := "10"
	if strings.EqualFold(magic, "43525950") {
		secondByte = "13"
	}
	hexStr := strings.ToLower(packetType + secondByte + magic + encryptedID + strings.Repeat("00", 166))
	parts := make([]string, 0, len(hexStr)/2)
	for index := 0; index < len(hexStr); index += 2 {
		parts = append(parts, "0x"+hexStr[index:index+2])
	}
	return strings.Join(parts, ",")
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.LocalAddr().(*net.UDPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func float64PtrForTest(value float64) *float64 {
	return &value
}
