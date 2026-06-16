package fpv

import (
	"net"
	"strconv"
	"testing"
	"time"

	"drone-management/internal/store"
)

func TestParseASCII(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		raw        string
		wantFormat string
		wantFreq   float64
		wantRSSI   float64
		wantValid  bool
		wantErr    bool
	}{
		{
			name:       "format 0 code",
			raw:        withASCIIChecksum("F2472R056T03"),
			wantFormat: "ascii-code",
			wantFreq:   2472,
			wantRSSI:   56,
			wantValid:  true,
		},
		{
			name:       "format 1 code",
			raw:        withASCIIChecksum("F2472R056T20"),
			wantFormat: "ascii-code",
			wantFreq:   2472,
			wantRSSI:   56,
			wantValid:  true,
		},
		{
			name:       "format 2 name",
			raw:        withASCIIChecksum("F5750R098T=FPV#"),
			wantFormat: "ascii-name-checksum",
			wantFreq:   5750,
			wantRSSI:   98,
			wantValid:  true,
		},
		{
			name:       "format 3 readable",
			raw:        "$ ALM F=5750MHz R=098 T=FPV \x00",
			wantFormat: "ascii-name",
			wantFreq:   5750,
			wantRSSI:   98,
			wantValid:  true,
		},
		{
			name:    "bad checksum",
			raw:     "F2472R056T03C000",
			wantErr: true,
		},
		{
			name:       "unknown type remains invalid",
			raw:        withASCIIChecksum("F2472R056T00"),
			wantFormat: "ascii-code",
			wantFreq:   2472,
			wantRSSI:   56,
			wantValid:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseASCII(tt.raw, now)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseASCII() error = %v", err)
			}
			if got.Format != tt.wantFormat {
				t.Fatalf("format = %q, want %q", got.Format, tt.wantFormat)
			}
			if got.Frequency != tt.wantFreq || got.RSSI != tt.wantRSSI || got.Valid != tt.wantValid {
				t.Fatalf("target = %#v", got)
			}
		})
	}
}

func TestParseHEXFormats(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

	format4, err := ParseFormat4([]byte{0xfe, 0x16, 0x9e, 0x00, 0x00, 0x32, 0x00, 0x00}, now)
	if err != nil {
		t.Fatalf("ParseFormat4() error = %v", err)
	}
	if format4.Frequency != 5790 || format4.RSSI != 50 || !format4.Valid {
		t.Fatalf("format4 = %#v", format4)
	}

	format5Frame := []byte{0x1f, 0x02, 0x04, 0x01, 0x40, 0x06, 0x11, 0x03, 0x16, 0x7b, 0x00, 0x00, 0x32, 0x01, 0x22, 0x03}
	format5, err := ParseFormat5(format5Frame, now)
	if err != nil {
		t.Fatalf("ParseFormat5() error = %v", err)
	}
	if format5.Frequency != 5755 || format5.RSSI != 50 || !format5.Valid || format5.DeviceSN != "040140" {
		t.Fatalf("format5 = %#v", format5)
	}

	badChecksum := append([]byte(nil), format5Frame...)
	badChecksum[14] = 0x23
	if _, err := ParseFormat5(badChecksum, now); err == nil {
		t.Fatal("expected checksum error")
	}
}

func TestService_ingestBufferHandlesSplitFrames(t *testing.T) {
	state := store.New(10, 10)
	service := NewService(state, Options{Host: "127.0.0.1", Port: 10005})
	line := withASCIIChecksum("F5750R098T=FPV#") + "\r\n"

	remainder := service.ingestBuffer([]byte(line[:8]))
	if len(remainder) == 0 {
		t.Fatal("expected partial line remainder")
	}
	remainder = service.ingestBuffer(append(remainder, []byte(line[8:])...))
	if len(remainder) != 0 {
		t.Fatalf("remainder length = %d, want 0", len(remainder))
	}
	items := state.FPV(10)
	if len(items) != 1 || items[0].Frequency != 5750 {
		t.Fatalf("items = %#v", items)
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
	_, _ = conn.Write([]byte(withASCIIChecksum("F5750R098T=FPV#") + "\r\n"))
	_ = conn.Close()

	waitFor(t, time.Second, func() bool { return len(state.FPV(10)) == 1 })
}

func TestServiceSetPortRestartsListener(t *testing.T) {
	initialPort := freeTCPPort(t)
	nextPort := freeTCPPort(t)
	state := store.New(10, 10)
	service := NewService(state, Options{Host: "127.0.0.1", Port: initialPort})
	ctx, cancel := contextWithTimeout(t, time.Second)
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

func withASCIIChecksum(prefix string) string {
	var checksum int
	for _, value := range []byte(prefix) {
		checksum += int(value)
	}
	return prefix + "C" + pad3(checksum%256)
}

func pad3(value int) string {
	if value < 10 {
		return "00" + strconv.Itoa(value)
	}
	if value < 100 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}
