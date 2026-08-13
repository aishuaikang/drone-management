package fpvvideo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSetWebRTCListenHostUpdatesConfigBetweenSessions(t *testing.T) {
	service := New(Options{
		RTSPURL:          "rtsp://192.168.100.106:554/live/1_1",
		WebRTCListenHost: "192.168.77.101",
		WebRTCListenPort: 18889,
		WebRTCUDPPort:    18189,
		PathName:         "fpv",
	})
	service.whepURL = localWHEPURL(service.options)

	if err := service.SetWebRTCListenHost("192.168.31.254"); err != nil {
		t.Fatalf("SetWebRTCListenHost() error = %v", err)
	}
	if got := service.WebRTCListenHost(); got != "192.168.31.254" {
		t.Fatalf("WebRTCListenHost() = %q", got)
	}
	if got := service.WHEPURL(); got != "http://192.168.31.254:18889/fpv/whep" {
		t.Fatalf("WHEPURL() = %q", got)
	}
	config := service.mediaMTXConfig()
	if !strings.Contains(config, "webrtcAddress: 192.168.31.254:18889") ||
		!strings.Contains(config, "webrtcLocalUDPAddress: 192.168.31.254:18189") {
		t.Fatalf("config does not use updated host:\n%s", config)
	}
}

func TestSetWebRTCListenHostRejectsInvalidOrRunning(t *testing.T) {
	service := New(Options{WebRTCListenHost: "127.0.0.1"})
	if err := service.SetWebRTCListenHost("::1"); err == nil {
		t.Fatal("SetWebRTCListenHost() accepted IPv6 address")
	}
	service.cmd = &exec.Cmd{}
	if err := service.SetWebRTCListenHost("192.168.31.254"); !errors.Is(err, ErrRunning) {
		t.Fatalf("SetWebRTCListenHost() error = %v, want ErrRunning", err)
	}
}

func TestRestartPreservesExternalWHEPWhenRTMPEnabled(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	const rtspURL = "rtsp://192.168.100.106:554/live/1_1"
	whepURL := upstream.URL + "/fpv/whep"
	inputURLs := make(chan string, 1)
	service := New(Options{
		RTSPURL:     rtspURL,
		WHEPURL:     whepURL,
		RTMPEnabled: true,
		RTMPURL:     "rtmp://example.com/live/key",
		MediaMTXBin: "/missing/mediamtx",
	})
	service.publisher.mu.Lock()
	service.publisher.options.Attempt = func(
		ctx context.Context,
		inputURL string,
		_ string,
		onConnected func(),
		_ func(),
	) error {
		inputURLs <- inputURL
		onConnected()
		<-ctx.Done()
		return ctx.Err()
	}
	service.publisher.mu.Unlock()
	t.Cleanup(func() { _ = service.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := service.Restart(ctx); err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	if got := service.WHEPURL(); got != whepURL {
		t.Fatalf("WHEPURL() = %q, want external URL %q", got, whepURL)
	}
	select {
	case got := <-inputURLs:
		if got != rtspURL {
			t.Fatalf("publisher input URL = %q, want %q", got, rtspURL)
		}
	case <-time.After(time.Second):
		t.Fatal("RTMP publisher did not start with the upstream RTSP URL")
	}
	service.mu.Lock()
	cmd := service.cmd
	service.mu.Unlock()
	if cmd != nil {
		t.Fatal("Restart() started MediaMTX for external WHEP playback")
	}
}

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

func TestMediaMTXConfigEnablesLoopbackRTSPOnlyForRTMP(t *testing.T) {
	service := New(Options{
		RTSPURL:          "rtsp://192.168.100.106:554/live/1_1",
		RTMPEnabled:      true,
		RTMPURL:          "rtmp://example.com/live/key",
		InternalRTSPPort: 28554,
	})

	config := service.mediaMTXConfig()
	for _, want := range []string{"rtsp: true", "rtspTransports: [tcp]", "rtspAddress: 127.0.0.1:28554"} {
		if !strings.Contains(config, want) {
			t.Fatalf("config missing %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, "rtspAddress: 0.0.0.0") {
		t.Fatalf("config exposes internal RTSP proxy:\n%s", config)
	}
}

func TestSetRTMPSettingsRejectsChangesWhileMediaMTXRunning(t *testing.T) {
	service := New(Options{})
	service.cmd = &exec.Cmd{}
	if err := service.SetRTMPSettings(true, "rtmp://example.com/live/key"); !errors.Is(err, ErrRunning) {
		t.Fatalf("SetRTMPSettings() error = %v, want ErrRunning", err)
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
