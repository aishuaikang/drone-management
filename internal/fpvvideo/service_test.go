package fpvvideo

import (
	"strings"
	"testing"
)

func TestMediaMTXBinaryName(t *testing.T) {
	tests := []struct {
		name   string
		goos   string
		goarch string
		want   string
		ok     bool
	}{
		{
			name:   "linux arm64",
			goos:   "linux",
			goarch: "arm64",
			want:   "mediamtx_v1.19.0_linux_arm64",
			ok:     true,
		},
		{
			name:   "windows amd64",
			goos:   "windows",
			goarch: "amd64",
			want:   "mediamtx_v1.19.0_windows_amd64.exe",
			ok:     true,
		},
		{
			name:   "darwin arm64",
			goos:   "darwin",
			goarch: "arm64",
			want:   "mediamtx_v1.19.0_darwin_arm64",
			ok:     true,
		},
		{
			name:   "unsupported",
			goos:   "linux",
			goarch: "amd64",
			ok:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := mediaMTXBinaryName(tt.goos, tt.goarch)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("mediaMTXBinaryName() = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestMediaMTXConfigUsesWHEPAndRTSPSource(t *testing.T) {
	service := New(Options{
		RTSPURL:          "rtsp://192.168.100.106:554/live/1_1",
		WebRTCListenHost: "127.0.0.1",
		WebRTCListenPort: 18889,
		WebRTCUDPPort:    18189,
		PathName:         "fpv",
	})

	config := service.mediaMTXConfig()
	wants := []string{
		"rtsp: false",
		"hls: false",
		"webrtc: true",
		"webrtcAddress: 127.0.0.1:18889",
		"webrtcLocalUDPAddress: 127.0.0.1:18189",
		"sourceOnDemand: true",
		"rtspTransport: tcp",
		"paths:",
		"  fpv:",
		`source: "rtsp://192.168.100.106:554/live/1_1"`,
	}
	for _, want := range wants {
		if !strings.Contains(config, want) {
			t.Fatalf("config missing %q:\n%s", want, config)
		}
	}
}

func TestMediaMTXConfigEnablesRecordingWhenPathConfigured(t *testing.T) {
	service := New(Options{
		RTSPURL:          "rtsp://192.168.100.106:554/live/1_1",
		WebRTCListenHost: "127.0.0.1",
		WebRTCListenPort: 18889,
		WebRTCUDPPort:    18189,
		PathName:         "fpv",
		RecordPath:       "/tmp/fpv-video/session-1_%path_%s",
	})

	config := service.mediaMTXConfig()
	wants := []string{
		"record: yes",
		`recordPath: "/tmp/fpv-video/session-1_%path_%s"`,
		"recordFormat: fmp4",
		"recordPartDuration: 1s",
		"recordSegmentDuration: 24h0m0s",
		"recordDeleteAfter: 0s",
	}
	for _, want := range wants {
		if !strings.Contains(config, want) {
			t.Fatalf("config missing %q:\n%s", want, config)
		}
	}
}

func TestMediaMTXConfigDoesNotRecordWithoutPath(t *testing.T) {
	service := New(Options{
		RTSPURL: "rtsp://192.168.100.106:554/live/1_1",
	})

	config := service.mediaMTXConfig()
	if strings.Contains(config, "record: yes") || strings.Contains(config, "recordPath:") {
		t.Fatalf("config should not enable recording:\n%s", config)
	}
}

func TestCleanPathNameRejectsNestedPath(t *testing.T) {
	if got := cleanPathName("nested/fpv"); got != "" {
		t.Fatalf("cleanPathName() = %q, want empty", got)
	}
	if got := cleanPathName("/fpv/"); got != "fpv" {
		t.Fatalf("cleanPathName() = %q, want fpv", got)
	}
}

func TestPlaybackURLUsesBackendWHEPProxy(t *testing.T) {
	service := New(Options{
		RTSPURL:          "rtsp://192.168.100.106:554/live/1_1",
		WebRTCListenHost: "127.0.0.1",
		WebRTCListenPort: 18889,
		PathName:         "fpv",
	})

	if got := service.PlaybackURL(); got != "/api/v1/screen/fpv-video/whep" {
		t.Fatalf("PlaybackURL() = %q", got)
	}
}

func TestPlaybackURLUsesBackendWHEPProxyForExternalWHEP(t *testing.T) {
	service := New(Options{
		WHEPURL: "http://127.0.0.1:18889/fpv/whep",
	})

	if got := service.PlaybackURL(); got != "/api/v1/screen/fpv-video/whep" {
		t.Fatalf("PlaybackURL() = %q", got)
	}
}

func TestLimitedBufferRetainsMostRecentBytes(t *testing.T) {
	buf := newLimitedBuffer(6)

	n, err := buf.Write([]byte("abcdef"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 6 {
		t.Fatalf("Write() n = %d, want 6", n)
	}

	n, err = buf.Write([]byte("ghij"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 4 {
		t.Fatalf("Write() n = %d, want 4", n)
	}
	if got := buf.String(); got != "efghij" {
		t.Fatalf("String() = %q, want %q", got, "efghij")
	}
}

func TestLimitedBufferNormalizesNonPositiveLimit(t *testing.T) {
	buf := newLimitedBuffer(0)

	if _, err := buf.Write([]byte("abc")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if got := buf.String(); got != "c" {
		t.Fatalf("String() = %q, want %q", got, "c")
	}
}
