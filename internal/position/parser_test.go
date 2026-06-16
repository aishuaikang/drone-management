package position

import (
	"context"
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

func TestParseDJIOSerialOnlyFrameDoesNotEmitPosition(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	line := "dji_O,2/3,5776.5,-79,dji,F4XFC237300753P5,0.000000,0.000000,0.000000,0.000000,0.000000,0.000000,0.00|0.00,0.00|0.00|0.00,0;0x6d"
	parsed, ok := ParseLine(line, now)
	if !ok {
		t.Fatal("ParseLine() returned ok=false")
	}
	if parsed.Position != nil {
		t.Fatalf("position = %#v, want nil for serial-only frame", parsed.Position)
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

func TestParseRIDKeepsZeroCoordinatesForListDisplay(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	line := "RID,serial,,0.000000,0.000000,0.000000,0.000000,0.00,137.00,2437.0,0.0,-82,29658"
	parsed, ok := ParseLine(line, now)
	if !ok || parsed.Position == nil {
		t.Fatalf("parsed = %#v, ok = %v", parsed, ok)
	}
	if parsed.Position.Drone == nil || parsed.Position.Home == nil {
		t.Fatalf("expected zero coordinates to be kept: %#v", parsed.Position)
	}
	if parsed.Position.Drone.Latitude != 0 || parsed.Position.Drone.Longitude != 0 {
		t.Fatalf("drone = %#v, want zero point", parsed.Position.Drone)
	}
	if parsed.Position.Home.Latitude != 0 || parsed.Position.Home.Longitude != 0 {
		t.Fatalf("home = %#v, want zero point", parsed.Position.Home)
	}
}

func TestParseDJIOKeepsZeroCoordinatesForListDisplay(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	line := "dji_O,2/3,5776.5,-79,dji,F4XFC237300753P5,0.000000,0.000000,0.000000,0.000000,0.000000,0.000000,0.00|0.00,0.00|0.00|0.00,1744703230504;0x6d"
	parsed, ok := ParseLine(line, now)
	if !ok || parsed.Position == nil {
		t.Fatalf("parsed = %#v, ok = %v", parsed, ok)
	}
	if parsed.Position.Drone == nil || parsed.Position.Pilot == nil || parsed.Position.Home == nil {
		t.Fatalf("expected zero coordinates to be kept: %#v", parsed.Position)
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

func TestServiceSkipsDJIOSerialOnlyFrameMerge(t *testing.T) {
	state := store.New(10, 10)
	service := NewService(state, Options{Host: "127.0.0.1", Port: 10007})
	positionLine := "dji_O,2/3,5776.5,-81,DJI Mini 3 pro,F4XFC237300753P5,121.664104,31.172048,121.699656,31.158675,121.699673,31.158600,14.90|110.60,965.00|-432.00|0.00,1744703230504;0x6d"
	serialOnlyLine := "dji_O,2/3,5776.5,-79,dji,F4XFC237300753P5,0.000000,0.000000,0.000000,0.000000,0.000000,0.000000,0.00|0.00,0.00|0.00|0.00,0;0x6d"

	service.IngestLine(positionLine)
	items := state.Positions(10)
	if len(items) != 1 {
		t.Fatalf("positions count = %d, want 1", len(items))
	}
	lastSeen := items[0].LastSeen
	hitCount := items[0].HitCount
	lastRaw := items[0].LastRecord.Raw

	service.IngestLine(serialOnlyLine)
	items = state.Positions(10)
	if len(items) != 1 {
		t.Fatalf("positions count = %d, want 1", len(items))
	}
	if items[0].HitCount != hitCount {
		t.Fatalf("hit count = %d, want %d", items[0].HitCount, hitCount)
	}
	if !items[0].LastSeen.Equal(lastSeen) {
		t.Fatalf("last seen = %v, want %v", items[0].LastSeen, lastSeen)
	}
	if items[0].LastRecord.Raw != lastRaw {
		t.Fatalf("last raw = %q, want original position frame", items[0].LastRecord.Raw)
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

func float64PtrForTest(value float64) *float64 {
	return &value
}
