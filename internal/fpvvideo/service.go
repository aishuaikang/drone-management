package fpvvideo

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultMediaMTXPath          = "./MediaMTX"
	defaultMediaMTXWorkDir       = "./tmp/fpv-video"
	defaultMediaMTXPathName      = "fpv"
	defaultWebRTCListenHost      = "127.0.0.1"
	defaultWebRTCListenPort      = 18889
	defaultWebRTCUDPPort         = 18189
	defaultWebRTCReadyWait       = 15 * time.Second
	defaultWebRTCReadyPeriod     = 200 * time.Millisecond
	defaultMediaMTXStopWait      = 3 * time.Second
	defaultWHEPProxyPath         = "/api/v1/screen/fpv-video/whep"
	defaultWHEPTrackWait         = 10 * time.Second
	defaultWHEPHandshakeWait     = 10 * time.Second
	defaultWHEPSTUNGatherWait    = 5 * time.Second
	defaultRecordPartDuration    = time.Second
	defaultRecordSegmentDuration = 24 * time.Hour
)

var (
	// ErrNotConfigured means no RTSP source or WHEP endpoint is configured.
	ErrNotConfigured = errors.New("fpv video stream is not configured")
	// ErrUnsupportedPlatform means no bundled MediaMTX binary matches this OS and architecture.
	ErrUnsupportedPlatform = errors.New("bundled mediamtx is not available for this platform")
)

// Options configures RTSP to WebRTC playback for browser clients.
type Options struct {
	RTSPURL          string
	MediaMTXPath     string
	MediaMTXWorkDir  string
	MediaMTXBin      string
	WebRTCListenHost string
	WebRTCListenPort int
	WebRTCUDPPort    int
	PathName         string
	WHEPURL          string
	RecordPath       string
}

// Service owns a MediaMTX process that converts a configured RTSP stream to WHEP/WebRTC.
type Service struct {
	options Options

	mu        sync.Mutex
	cmd       *exec.Cmd
	done      chan struct{}
	lastError string
	config    string
	whepURL   string
}

// New creates a video stream service.
func New(options Options) *Service {
	options = normalizeOptions(options)
	return &Service{
		options: options,
		whepURL: externalWHEPURL(options),
	}
}

// Enabled reports whether browser playback is configured.
func (s *Service) Enabled() bool {
	return strings.TrimSpace(s.options.WHEPURL) != "" || strings.TrimSpace(s.options.RTSPURL) != ""
}

// PlaybackURL returns the browser-facing WHEP endpoint.
func (s *Service) PlaybackURL() string {
	if !s.Enabled() {
		return ""
	}
	return defaultWHEPProxyPath
}

// SetRecordPath sets the output file path used by the next MediaMTX start.
func (s *Service) SetRecordPath(recordPath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.options.RecordPath = strings.TrimSpace(recordPath)
}

// WHEPURL returns the upstream WHEP URL used for readiness checks.
func (s *Service) WHEPURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.whepURL
}

// Close stops a running MediaMTX process.
func (s *Service) Close() error {
	return s.stop(false)
}

// Shutdown gracefully stops a running MediaMTX process.
func (s *Service) Shutdown() error {
	return s.stop(true)
}

func (s *Service) stop(graceful bool) error {
	s.mu.Lock()
	cmd := s.cmd
	done := s.done
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if graceful {
		if err := signalMediaMTXStop(cmd); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("stop mediamtx: %w", err)
		}
	} else if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop mediamtx: %w", err)
	}
	if done == nil {
		s.clearProcess(cmd)
		return nil
	}
	select {
	case <-done:
		return nil
	case <-time.After(defaultMediaMTXStopWait):
		if graceful {
			if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return fmt.Errorf("kill mediamtx after graceful stop timeout: %w", err)
			}
			select {
			case <-done:
				return nil
			case <-time.After(defaultMediaMTXStopWait):
			}
		}
		return errors.New("timed out waiting for mediamtx to stop")
	}
}

func (s *Service) clearProcess(cmd *exec.Cmd) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != cmd {
		return
	}
	s.cmd = nil
	s.done = nil
}

// Restart starts a fresh WebRTC bridge and waits until its WHEP endpoint is reachable.
func (s *Service) Restart(ctx context.Context) error {
	if !s.Enabled() {
		return ErrNotConfigured
	}
	if err := s.Close(); err != nil {
		return err
	}
	if strings.TrimSpace(s.options.WHEPURL) != "" && strings.TrimSpace(s.options.RecordPath) == "" {
		return s.waitForWHEPEndpoint(ctx)
	}
	return s.ensureStarted(ctx)
}

func (s *Service) ensureStarted(ctx context.Context) error {
	s.mu.Lock()
	if s.cmd == nil {
		if err := s.startLocked(); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	s.mu.Unlock()
	return s.waitForWHEPEndpoint(ctx)
}

func (s *Service) startLocked() error {
	bin, err := s.resolveMediaMTXBinary()
	if err != nil {
		return err
	}
	if err := os.RemoveAll(s.options.MediaMTXWorkDir); err != nil {
		return fmt.Errorf("prepare fpv video work dir: %w", err)
	}
	if err := os.MkdirAll(s.options.MediaMTXWorkDir, 0o755); err != nil {
		return fmt.Errorf("prepare fpv video work dir: %w", err)
	}
	if s.options.RecordPath != "" {
		if err := os.MkdirAll(filepath.Dir(s.options.RecordPath), 0o755); err != nil {
			return fmt.Errorf("prepare fpv video record dir: %w", err)
		}
	}
	configPath := filepath.Join(s.options.MediaMTXWorkDir, "mediamtx.yml")
	if err := os.WriteFile(configPath, []byte(s.mediaMTXConfig()), 0o644); err != nil {
		return fmt.Errorf("write mediamtx config: %w", err)
	}

	stderr := newLimitedBuffer(16 * 1024)
	cmd := exec.Command(bin, configPath)
	cmd.Stdout = nil
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start mediamtx: %w", err)
	}

	s.cmd = cmd
	done := make(chan struct{})
	s.done = done
	s.lastError = ""
	s.config = configPath
	s.whepURL = localWHEPURL(s.options)
	go s.wait(cmd, stderr, done)
	return nil
}

func (s *Service) resolveMediaMTXBinary() (string, error) {
	if s.options.MediaMTXBin != "" {
		return s.options.MediaMTXBin, nil
	}

	filename, ok := mediaMTXBinaryName(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return "", fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, runtime.GOOS, runtime.GOARCH)
	}
	for _, root := range mediaMTXSearchRoots(s.options.MediaMTXPath) {
		candidate := filepath.Join(root, filename)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrUnsupportedPlatform, filename)
}

func mediaMTXSearchRoots(configured string) []string {
	roots := []string{}
	if configured != "" {
		roots = append(roots, configured)
	}
	if exe, err := os.Executable(); err == nil {
		roots = append(roots, filepath.Join(filepath.Dir(exe), "MediaMTX"))
	}
	roots = append(roots, defaultMediaMTXPath)

	seen := map[string]struct{}{}
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		clean := filepath.Clean(root)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

func mediaMTXBinaryName(goos, goarch string) (string, bool) {
	name := fmt.Sprintf("mediamtx_v1.19.0_%s_%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	switch goos + "/" + goarch {
	case "linux/arm64",
		"windows/amd64",
		"darwin/arm64":
		return name, true
	default:
		return "", false
	}
}

func (s *Service) wait(cmd *exec.Cmd, stderr fmt.Stringer, done chan<- struct{}) {
	defer close(done)
	err := cmd.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != cmd {
		return
	}
	s.cmd = nil
	s.done = nil
	if err != nil {
		if output := strings.TrimSpace(stderr.String()); output != "" {
			s.lastError = fmt.Sprintf("%v: %s", err, output)
		} else {
			s.lastError = err.Error()
		}
		return
	}
	s.lastError = "mediamtx exited"
}

func signalMediaMTXStop(cmd *exec.Cmd) error {
	if runtime.GOOS == "windows" {
		return cmd.Process.Kill()
	}
	return cmd.Process.Signal(os.Interrupt)
}

func (s *Service) waitForWHEPEndpoint(ctx context.Context) error {
	deadline := time.NewTimer(defaultWebRTCReadyWait)
	defer deadline.Stop()
	ticker := time.NewTicker(defaultWebRTCReadyPeriod)
	defer ticker.Stop()

	for {
		s.mu.Lock()
		lastError := s.lastError
		cmd := s.cmd
		whepURL := s.whepURL
		external := strings.TrimSpace(s.options.WHEPURL) != ""
		s.mu.Unlock()

		if cmd == nil && lastError != "" && !external {
			return fmt.Errorf("fpv video stream stopped: %s", lastError)
		}
		if whepURL != "" && whepEndpointReachable(ctx, whepURL) {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if lastError != "" {
				return fmt.Errorf("fpv video stream unavailable: %s", lastError)
			}
			return errors.Join(errors.New("fpv video stream is not ready"), s.Close())
		case <-ticker.C:
		}
	}
}

func whepEndpointReachable(ctx context.Context, whepURL string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodOptions, whepURL, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusInternalServerError
}

func (s *Service) mediaMTXConfig() string {
	var builder strings.Builder
	builder.WriteString("logLevel: info\n")
	builder.WriteString("rtsp: false\n")
	builder.WriteString("rtmp: false\n")
	builder.WriteString("hls: false\n")
	builder.WriteString("srt: false\n")
	builder.WriteString("playback: false\n")
	builder.WriteString("webrtc: true\n")
	builder.WriteString("webrtcAddress: ")
	builder.WriteString(net.JoinHostPort(s.options.WebRTCListenHost, strconv.Itoa(s.options.WebRTCListenPort)))
	builder.WriteString("\n")
	builder.WriteString("webrtcLocalUDPAddress: ")
	builder.WriteString(net.JoinHostPort(s.options.WebRTCListenHost, strconv.Itoa(s.options.WebRTCUDPPort)))
	builder.WriteString("\n")
	builder.WriteString("webrtcAllowOrigins: ['*']\n")
	builder.WriteString("pathDefaults:\n")
	builder.WriteString("  sourceOnDemand: true\n")
	builder.WriteString("  sourceOnDemandStartTimeout: 10s\n")
	builder.WriteString("  sourceOnDemandCloseAfter: 5s\n")
	builder.WriteString("  rtspTransport: tcp\n")
	builder.WriteString("  whepHandshakeTimeout: ")
	builder.WriteString(defaultWHEPHandshakeWait.String())
	builder.WriteString("\n")
	builder.WriteString("  whepSTUNGatherTimeout: ")
	builder.WriteString(defaultWHEPSTUNGatherWait.String())
	builder.WriteString("\n")
	builder.WriteString("  whepTrackGatherTimeout: ")
	builder.WriteString(defaultWHEPTrackWait.String())
	builder.WriteString("\n")
	if s.options.RecordPath != "" {
		builder.WriteString("  record: yes\n")
		builder.WriteString("  recordPath: ")
		builder.WriteString(quoteYAMLString(s.options.RecordPath))
		builder.WriteString("\n")
		builder.WriteString("  recordFormat: fmp4\n")
		builder.WriteString("  recordPartDuration: ")
		builder.WriteString(defaultRecordPartDuration.String())
		builder.WriteString("\n")
		builder.WriteString("  recordSegmentDuration: ")
		builder.WriteString(defaultRecordSegmentDuration.String())
		builder.WriteString("\n")
		builder.WriteString("  recordDeleteAfter: 0s\n")
	}
	builder.WriteString("paths:\n")
	builder.WriteString("  ")
	builder.WriteString(s.options.PathName)
	builder.WriteString(":\n")
	builder.WriteString("    source: ")
	builder.WriteString(quoteYAMLString(s.options.RTSPURL))
	builder.WriteString("\n")
	return builder.String()
}

func normalizeOptions(options Options) Options {
	options.RTSPURL = strings.TrimSpace(options.RTSPURL)
	options.MediaMTXPath = strings.TrimSpace(options.MediaMTXPath)
	if options.MediaMTXPath == "" {
		options.MediaMTXPath = defaultMediaMTXPath
	}
	options.MediaMTXWorkDir = strings.TrimSpace(options.MediaMTXWorkDir)
	if options.MediaMTXWorkDir == "" {
		options.MediaMTXWorkDir = defaultMediaMTXWorkDir
	}
	options.MediaMTXBin = strings.TrimSpace(options.MediaMTXBin)
	options.WebRTCListenHost = strings.TrimSpace(options.WebRTCListenHost)
	if options.WebRTCListenHost == "" {
		options.WebRTCListenHost = defaultWebRTCListenHost
	}
	if options.WebRTCListenPort <= 0 {
		options.WebRTCListenPort = defaultWebRTCListenPort
	}
	if options.WebRTCUDPPort <= 0 {
		options.WebRTCUDPPort = defaultWebRTCUDPPort
	}
	options.PathName = cleanPathName(options.PathName)
	if options.PathName == "" {
		options.PathName = defaultMediaMTXPathName
	}
	options.WHEPURL = strings.TrimSpace(options.WHEPURL)
	options.RecordPath = strings.TrimSpace(options.RecordPath)
	return options
}

func externalWHEPURL(options Options) string {
	if options.WHEPURL != "" {
		return options.WHEPURL
	}
	return ""
}

func localWHEPURL(options Options) string {
	base := url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(options.WebRTCListenHost, strconv.Itoa(options.WebRTCListenPort)),
		Path:   "/" + options.PathName + "/whep",
	}
	return base.String()
}

func cleanPathName(value string) string {
	value = strings.Trim(strings.TrimSpace(value), "/")
	if value == "" || strings.Contains(value, "/") {
		return ""
	}
	return value
}

func quoteYAMLString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

type limitedBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{limit: max(1, limit)}
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	written := len(data)
	b.data = append(b.data, data...)
	if overflow := len(b.data) - b.limit; overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:b.limit]
	}
	return written, nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
