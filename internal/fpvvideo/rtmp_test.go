package fpvvideo

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bluenviron/gortmplib"
	rtmpcodecs "github.com/bluenviron/gortmplib/pkg/codecs"
	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
)

type rtmpTestRTSPHandler struct {
	stream *gortsplib.ServerStream
}

func (h *rtmpTestRTSPHandler) OnDescribe(
	_ *gortsplib.ServerHandlerOnDescribeCtx,
) (*base.Response, *gortsplib.ServerStream, error) {
	return &base.Response{StatusCode: base.StatusOK}, h.stream, nil
}

func (h *rtmpTestRTSPHandler) OnSetup(
	_ *gortsplib.ServerHandlerOnSetupCtx,
) (*base.Response, *gortsplib.ServerStream, error) {
	return &base.Response{StatusCode: base.StatusOK}, h.stream, nil
}

func (h *rtmpTestRTSPHandler) OnPlay(
	_ *gortsplib.ServerHandlerOnPlayCtx,
) (*base.Response, error) {
	return &base.Response{StatusCode: base.StatusOK}, nil
}

func TestValidateRTMPSettings(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		url     string
		wantErr bool
	}{
		{name: "disabled empty", url: ""},
		{name: "rtmp", enabled: true, url: "rtmp://example.com/live/stream"},
		{name: "rtmps", enabled: true, url: "rtmps://user:secret@example.com/app/key?token=x"},
		{name: "enabled empty", enabled: true, wantErr: true},
		{name: "wrong scheme", url: "https://example.com/live/stream", wantErr: true},
		{name: "missing host", url: "rtmp:///live/stream", wantErr: true},
		{name: "missing path", url: "rtmp://example.com", wantErr: true},
		{name: "zero port", url: "rtmp://example.com:0/live/stream", wantErr: true},
		{name: "port above range", url: "rtmp://example.com:65536/live/stream", wantErr: true},
		{name: "malformed escape", url: "rtmp://user:secret@example.com/live/%zz?token=private", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRTMPSettings(tt.enabled, tt.url)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateRTMPSettings() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateRTMPURLDoesNotReturnDestinationSecrets(t *testing.T) {
	rawURL := "rtmp://user:secret@example.com/live/%zz?token=private"
	err := ValidateRTMPURL(rawURL)
	if err == nil {
		t.Fatal("ValidateRTMPURL() accepted malformed URL")
	}
	for _, secret := range []string{"user", "secret", "example.com", "private"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("validation error %q contains %q", err, secret)
		}
	}
}

func TestNormalizeRTMPClientURLAddsProtocolDefaultPort(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "rtmp", raw: "rtmp://example.com/live/key", want: "example.com:1935"},
		{name: "rtmps", raw: "rtmps://example.com/live/key", want: "example.com:1936"},
		{name: "explicit", raw: "rtmp://example.com:11935/live/key", want: "example.com:11935"},
		{name: "ipv6", raw: "rtmp://[2001:db8::1]/live/key", want: "[2001:db8::1]:1935"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, err := normalizeRTMPClientURL(tt.raw)
			if err != nil {
				t.Fatalf("normalizeRTMPClientURL() error = %v", err)
			}
			if target.Host != tt.want {
				t.Fatalf("host = %q, want %q", target.Host, tt.want)
			}
		})
	}
}

func TestRedactRTMPErrorRemovesDestinationsAndSecrets(t *testing.T) {
	target := "rtmps://operator:secret@example.com:1936/live/key?token=private"
	got := redactRTMPError("dial example.com:1936 while opening "+target+" for operator:secret /live/key token=private", target)
	for _, secret := range []string{"operator", "secret", "private", "example.com", "/live/key"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted error %q contains %q", got, secret)
		}
	}
}

func TestTimestampToDuration(t *testing.T) {
	if got := timestampToDuration(135000, 90000); got != 1500*time.Millisecond {
		t.Fatalf("timestampToDuration() = %s, want 1.5s", got)
	}
}

func TestPublisherDialContextHonorsCallAndSessionCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	tests := []struct {
		name          string
		cancelSession bool
	}{
		{name: "call context"},
		{name: "session context", cancelSession: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionCtx, cancelSession := context.WithCancel(context.Background())
			defer cancelSession()
			callCtx, cancelCall := context.WithCancel(context.Background())
			defer cancelCall()
			if tt.cancelSession {
				cancelSession()
			} else {
				cancelCall()
			}
			dial := publisherDialContext(sessionCtx, make(chan struct{}))
			conn, err := dial(callCtx, "tcp", listener.Addr().String())
			if conn != nil {
				conn.Close()
			}
			if err == nil {
				t.Fatal("dial unexpectedly succeeded after cancellation")
			}
		})
	}
}

func TestRTMPPublisherRetriesAndStops(t *testing.T) {
	var starts atomic.Int32
	publisher := newRTMPPublisher(rtmpPublisherOptions{
		Enabled:     true,
		URL:         "rtmp://example.com/live/key",
		RetryDelays: []time.Duration{5 * time.Millisecond},
		Attempt: func(
			_ context.Context,
			_, _ string,
			onConnected func(),
			onPushing func(),
		) error {
			starts.Add(1)
			onConnected()
			onPushing()
			return errors.New("connection closed")
		},
	})

	publisher.Start("rtsp://127.0.0.1:18554/fpv")
	waitForRTMPTest(t, time.Second, func() bool {
		return starts.Load() >= 2 && publisher.Status().State == RTMPStateRetrying
	})
	if err := publisher.Stop(false); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	status := publisher.Status()
	if status.State != RTMPStateIdle || status.Active {
		t.Fatalf("status after stop = %#v", status)
	}
}

func TestRTMPPublisherReportsPushingAndCleansUp(t *testing.T) {
	started := make(chan struct{})
	publisher := newRTMPPublisher(rtmpPublisherOptions{
		Enabled: true,
		URL:     "rtmp://example.com/live/key",
		Attempt: func(
			ctx context.Context,
			_, _ string,
			onConnected func(),
			onPushing func(),
		) error {
			onConnected()
			onPushing()
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	})

	publisher.Start("rtsp://127.0.0.1:18554/fpv")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("publisher did not start")
	}
	status := publisher.Status()
	if status.State != RTMPStatePushing || !status.Active || status.LastError != "" {
		t.Fatalf("pushing status = %#v", status)
	}
	if err := publisher.Stop(false); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	status = publisher.Status()
	if status.State != RTMPStateIdle || status.Active {
		t.Fatalf("status after stop = %#v", status)
	}
}

func TestRTMPPublisherUnsupportedCodecIsTerminal(t *testing.T) {
	publisher := newRTMPPublisher(rtmpPublisherOptions{
		Enabled: true,
		URL:     "rtmp://example.com/live/key",
		Attempt: func(context.Context, string, string, func(), func()) error {
			return errUnsupportedRTMPVideoCodec
		},
	})
	publisher.Start("rtsp://127.0.0.1:18554/fpv")
	waitForRTMPTest(t, time.Second, func() bool { return publisher.Status().State == RTMPStateFailed })
	if publisher.Status().Active {
		t.Fatalf("failed publisher must not be active")
	}
}

func TestRTMPPublisherBackoffSchedule(t *testing.T) {
	publisher := newRTMPPublisher(rtmpPublisherOptions{})
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}
	for index, delay := range want {
		if got := publisher.retryDelay(index); got != delay {
			t.Fatalf("retryDelay(%d) = %s, want %s", index, got, delay)
		}
	}
}

func TestRTMPPublisherStopTimeoutPreservesRunningHandle(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	publisher := newRTMPPublisher(rtmpPublisherOptions{
		Enabled:  true,
		URL:      "rtmp://example.com/live/key",
		StopWait: 5 * time.Millisecond,
		Attempt: func(context.Context, string, string, func(), func()) error {
			close(started)
			<-release
			return errors.New("released")
		},
	})
	publisher.Start("rtsp://127.0.0.1:18554/fpv")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("publisher attempt did not start")
	}
	if err := publisher.Stop(false); err == nil || !strings.Contains(err.Error(), "cleanup") {
		t.Fatalf("Stop() error = %v, want cleanup timeout", err)
	}
	publisher.mu.Lock()
	if publisher.done == nil {
		publisher.mu.Unlock()
		t.Fatal("publisher cleared its running handle before cleanup")
	}
	publisher.mu.Unlock()
	close(release)
	waitForRTMPTest(t, time.Second, func() bool {
		publisher.mu.Lock()
		defer publisher.mu.Unlock()
		return publisher.done == nil
	})
}

func TestPublishRTSPToRTMPForwardsH264(t *testing.T) {
	rtspListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen RTSP: %v", err)
	}
	rtspHandler := &rtmpTestRTSPHandler{}
	rtspServer := &gortsplib.Server{
		Handler:     rtspHandler,
		RTSPAddress: rtspListener.Addr().String(),
		Listen: func(_, _ string) (net.Listener, error) {
			return rtspListener, nil
		},
	}
	if err := rtspServer.Start(); err != nil {
		t.Fatalf("start RTSP server: %v", err)
	}
	defer rtspServer.Close()

	videoFormat := &format.H264{
		PayloadTyp: 96,
		SPS: []byte{
			0x67, 0x42, 0xc0, 0x28, 0xd9, 0x00, 0x78, 0x02,
			0x27, 0xe5, 0x84, 0x00, 0x00, 0x03, 0x00, 0x04,
			0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60, 0xc9, 0x20,
		},
		PPS:               []byte{0x08, 0x06, 0x07, 0x08},
		PacketizationMode: 1,
	}
	media := &description.Media{
		Type:    description.MediaTypeVideo,
		Formats: []format.Format{videoFormat},
	}
	rtspHandler.stream = &gortsplib.ServerStream{
		Server: rtspServer,
		Desc:   &description.Session{Medias: []*description.Media{media}},
	}
	if err := rtspHandler.stream.Initialize(); err != nil {
		t.Fatalf("initialize RTSP stream: %v", err)
	}
	defer rtspHandler.stream.Close()

	rtmpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen RTMP: %v", err)
	}
	defer rtmpListener.Close()
	received := make(chan struct{})
	serverError := make(chan error, 1)
	var receivedOnce sync.Once
	var receivedFrame atomic.Bool
	go func() {
		conn, err := rtmpListener.Accept()
		if err != nil {
			serverError <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		serverConn := &gortmplib.ServerConn{RW: conn}
		if err := serverConn.Initialize(); err != nil {
			serverError <- err
			return
		}
		if err := serverConn.Accept(); err != nil {
			serverError <- err
			return
		}
		reader := &gortmplib.Reader{Conn: serverConn}
		if err := reader.Initialize(); err != nil {
			serverError <- err
			return
		}
		for _, track := range reader.Tracks() {
			if _, ok := track.Codec.(*rtmpcodecs.H264); ok {
				reader.OnDataH264(track, func(_ time.Duration, _ time.Duration, _ [][]byte) {
					receivedFrame.Store(true)
					receivedOnce.Do(func() { close(received) })
				})
			}
		}
		for {
			if err := reader.Read(); err != nil {
				if !receivedFrame.Load() {
					serverError <- err
				}
				return
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connected := make(chan struct{})
	pushing := make(chan struct{})
	var connectedOnce sync.Once
	var pushingOnce sync.Once
	publishResult := make(chan error, 1)
	go func() {
		publishResult <- publishRTSPToRTMP(
			ctx,
			"rtsp://"+rtspListener.Addr().String()+"/fpv",
			"rtmp://"+rtmpListener.Addr().String()+"/live/key",
			func() { connectedOnce.Do(func() { close(connected) }) },
			func() { pushingOnce.Do(func() { close(pushing) }) },
		)
	}()

	select {
	case <-connected:
	case err := <-serverError:
		t.Fatalf("RTMP server failed before connection: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("publisher did not connect to RTMP server")
	}

	encoder := &rtph264.Encoder{PayloadType: 96, PacketizationMode: 1}
	if err := encoder.Init(); err != nil {
		t.Fatalf("initialize H264 RTP encoder: %v", err)
	}
	accessUnit := [][]byte{
		videoFormat.SPS,
		videoFormat.PPS,
		{0x65, 0x88, 0x84, 0x00, 0x33, 0xff},
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var timestamp uint32 = 90000
sendLoop:
	for {
		select {
		case <-received:
			break sendLoop
		case err := <-serverError:
			t.Fatalf("RTMP server failed while receiving video: %v", err)
		case err := <-publishResult:
			t.Fatalf("publisher stopped before forwarding video: %v", err)
		case <-deadline.C:
			t.Fatal("timed out waiting for forwarded H264 video")
		case <-ticker.C:
			packets, err := encoder.Encode(accessUnit)
			if err != nil {
				t.Fatalf("encode H264 RTP: %v", err)
			}
			for _, packet := range packets {
				packet.Timestamp = timestamp
				if err := rtspHandler.stream.WritePacketRTP(media, packet); err != nil {
					t.Fatalf("write RTSP packet: %v", err)
				}
			}
			timestamp += 3000
		}
	}
	select {
	case <-pushing:
	case <-time.After(time.Second):
		t.Fatal("publisher did not report pushing state")
	}
	cancel()
	select {
	case err := <-publishResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("publish result = %v, want context cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("publisher did not stop after cancellation")
	}
}

func waitForRTMPTest(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for RTMP publisher state")
}
