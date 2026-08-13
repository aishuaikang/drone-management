package fpvvideo

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bluenviron/gortmplib"
	rtmpcodecs "github.com/bluenviron/gortmplib/pkg/codecs"
	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph265"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
	"github.com/pion/rtp"
)

const (
	defaultInternalRTSPPort  = 18554
	defaultPublisherStopWait = 3 * time.Second
	publisherReadTimeout     = 10 * time.Second
	publisherWriteTimeout    = 10 * time.Second
)

// RTMPState describes the external publisher lifecycle.
type RTMPState string

const (
	RTMPStateDisabled RTMPState = "disabled"
	RTMPStateIdle     RTMPState = "idle"
	RTMPStateStarting RTMPState = "starting"
	RTMPStatePushing  RTMPState = "pushing"
	RTMPStateRetrying RTMPState = "retrying"
	RTMPStateFailed   RTMPState = "failed"
)

// RTMPStatus is a concurrency-safe snapshot of the external publisher.
type RTMPStatus struct {
	Enabled   bool
	Active    bool
	State     RTMPState
	LastError string
	UpdatedAt *time.Time
}

type rtmpAttemptFunc func(
	ctx context.Context,
	inputURL string,
	targetURL string,
	onConnected func(),
	onPushing func(),
) error

type rtmpPublisherOptions struct {
	Enabled     bool
	URL         string
	RetryDelays []time.Duration
	StopWait    time.Duration
	Attempt     rtmpAttemptFunc
}

type rtmpPublisher struct {
	mu      sync.Mutex
	options rtmpPublisherOptions
	status  RTMPStatus

	cancel context.CancelFunc
	done   chan struct{}

	now func() time.Time
}

var errUnsupportedRTMPVideoCodec = errors.New("RTMP publisher supports H264 or H265 video only")

func newRTMPPublisher(options rtmpPublisherOptions) *rtmpPublisher {
	options.URL = strings.TrimSpace(options.URL)
	if len(options.RetryDelays) == 0 {
		options.RetryDelays = []time.Duration{
			time.Second,
			2 * time.Second,
			4 * time.Second,
			8 * time.Second,
			10 * time.Second,
		}
	}
	if options.StopWait <= 0 {
		options.StopWait = defaultPublisherStopWait
	}
	if options.Attempt == nil {
		options.Attempt = publishRTSPToRTMP
	}
	p := &rtmpPublisher{
		options: options,
		now:     time.Now,
	}
	p.setStatusLocked(initialRTMPState(options.Enabled), false, "")
	return p
}

// ValidateRTMPSettings validates the persisted publisher settings.
func ValidateRTMPSettings(enabled bool, rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if enabled && rawURL == "" {
		return errors.New("RTMP URL is required when publishing is enabled")
	}
	if rawURL == "" {
		return nil
	}
	return ValidateRTMPURL(rawURL)
}

// ValidateRTMPURL accepts RTMP and RTMPS destinations with a host and stream path.
func ValidateRTMPURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return errors.New("invalid RTMP URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "rtmp" && scheme != "rtmps" {
		return errors.New("RTMP URL must use rtmp or rtmps")
	}
	if parsed.Hostname() == "" || strings.Trim(parsed.EscapedPath(), "/") == "" {
		return errors.New("RTMP URL must include a host and stream path")
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return errors.New("RTMP URL port must be between 1 and 65535")
		}
	}
	return nil
}

func (p *rtmpPublisher) Configure(enabled bool, rawURL string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.options.Enabled = enabled
	p.options.URL = strings.TrimSpace(rawURL)
	p.setStatusLocked(initialRTMPState(enabled), false, "")
}

func (p *rtmpPublisher) Settings() (bool, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.options.Enabled, p.options.URL
}

func (p *rtmpPublisher) Status() RTMPStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	status := p.status
	if status.UpdatedAt != nil {
		updatedAt := *status.UpdatedAt
		status.UpdatedAt = &updatedAt
	}
	return status
}

func (p *rtmpPublisher) Start(inputURL string) {
	p.mu.Lock()
	if !p.options.Enabled || p.options.URL == "" {
		p.setStatusLocked(initialRTMPState(p.options.Enabled), false, "")
		p.mu.Unlock()
		return
	}
	if p.done != nil {
		p.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	p.cancel = cancel
	p.done = done
	p.setStatusLocked(RTMPStateStarting, false, "")
	p.mu.Unlock()

	go p.run(ctx, done, strings.TrimSpace(inputURL))
}

func (p *rtmpPublisher) Stop(_ bool) error {
	p.mu.Lock()
	cancel := p.cancel
	done := p.done
	stopWait := p.options.StopWait
	p.mu.Unlock()

	if cancel == nil && done == nil {
		p.markStopped()
		return nil
	}
	if cancel != nil {
		cancel()
	}
	if done != nil && !waitForPublisherDone(done, stopWait) {
		return errors.New("timed out waiting for RTMP publisher cleanup")
	}
	p.markStopped()
	return nil
}

func waitForPublisherDone(done <-chan struct{}, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func (p *rtmpPublisher) run(ctx context.Context, done chan<- struct{}, inputURL string) {
	defer close(done)
	defer func() {
		p.mu.Lock()
		p.cancel = nil
		p.done = nil
		if ctx.Err() != nil {
			p.setStatusLocked(initialRTMPState(p.options.Enabled), false, p.status.LastError)
		}
		p.mu.Unlock()
	}()

	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}
		if attempt > 0 {
			delay := p.retryDelay(attempt - 1)
			p.setStatus(RTMPStateRetrying, false, p.Status().LastError)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}

		p.setStatus(RTMPStateStarting, false, p.Status().LastError)
		var pushed atomic.Bool
		runErr := p.attempt()(
			ctx,
			inputURL,
			p.targetURL(),
			func() { p.setStatus(RTMPStateStarting, true, p.Status().LastError) },
			func() {
				if pushed.CompareAndSwap(false, true) {
					p.setStatus(RTMPStatePushing, true, "")
				}
			},
		)
		if ctx.Err() != nil {
			return
		}
		if runErr == nil {
			runErr = errors.New("RTMP publisher stopped")
		}
		lastError := redactRTMPError(runErr.Error(), p.targetURL())
		if errors.Is(runErr, errUnsupportedRTMPVideoCodec) {
			p.setStatus(RTMPStateFailed, false, lastError)
			return
		}
		p.setStatus(RTMPStateRetrying, false, lastError)
		if pushed.Load() {
			attempt = 1
		} else {
			attempt++
		}
	}
}

func (p *rtmpPublisher) attempt() rtmpAttemptFunc {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.options.Attempt
}

func publishRTSPToRTMP(
	ctx context.Context,
	inputURL string,
	targetURL string,
	onConnected func(),
	onPushing func(),
) error {
	input, err := base.ParseURL(inputURL)
	if err != nil {
		return fmt.Errorf("parse internal RTSP URL: %w", err)
	}
	target, err := normalizeRTMPClientURL(targetURL)
	if err != nil {
		return fmt.Errorf("parse RTMP target: %w", err)
	}

	attemptDone := make(chan struct{})
	defer close(attemptDone)
	dialContext := publisherDialContext(ctx, attemptDone)
	protocol := gortsplib.ProtocolTCP
	rtspClient := &gortsplib.Client{
		Scheme:       input.Scheme,
		Host:         input.Host,
		Protocol:     &protocol,
		ReadTimeout:  publisherReadTimeout,
		WriteTimeout: publisherWriteTimeout,
		DialContext:  dialContext,
	}
	if err := rtspClient.Start(); err != nil {
		return fmt.Errorf("start internal RTSP client: %w", err)
	}
	defer rtspClient.Close()

	desc, _, err := rtspClient.Describe(input)
	if err != nil {
		return fmt.Errorf("describe internal RTSP stream: %w", err)
	}

	var h264Format *format.H264
	media := desc.FindFormat(&h264Format)
	var h265Format *format.H265
	if media == nil {
		media = desc.FindFormat(&h265Format)
	}
	if media == nil {
		return errUnsupportedRTMPVideoCodec
	}
	if _, err := rtspClient.Setup(desc.BaseURL, media, 0, 0); err != nil {
		return fmt.Errorf("setup internal RTSP video track: %w", err)
	}

	rtmpClient := &gortmplib.Client{
		URL:         target,
		Publish:     true,
		DialContext: dialContext,
	}
	if err := rtmpClient.Initialize(ctx); err != nil {
		return fmt.Errorf("connect RTMP target: %w", err)
	}
	defer rtmpClient.Close()
	onConnected()

	writeError := make(chan error, 1)
	reportWriteError := func(err error) {
		select {
		case writeError <- err:
		default:
		}
	}

	if h264Format != nil {
		if err := configureH264Forwarder(rtspClient, media, h264Format, rtmpClient, reportWriteError, onPushing); err != nil {
			return err
		}
	} else if err := configureH265Forwarder(rtspClient, media, h265Format, rtmpClient, reportWriteError, onPushing); err != nil {
		return err
	}

	if _, err := rtspClient.Play(nil); err != nil {
		return fmt.Errorf("play internal RTSP stream: %w", err)
	}

	rtspWait := make(chan error, 1)
	go func() {
		rtspWait <- rtspClient.Wait()
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-writeError:
		return fmt.Errorf("write RTMP video: %w", err)
	case err := <-rtspWait:
		return fmt.Errorf("read internal RTSP stream: %w", err)
	}
}

func configureH264Forwarder(
	rtspClient *gortsplib.Client,
	media *description.Media,
	videoFormat *format.H264,
	rtmpClient *gortmplib.Client,
	reportError func(error),
	onPushing func(),
) error {
	decoder, err := videoFormat.CreateDecoder()
	if err != nil {
		return fmt.Errorf("create H264 RTP decoder: %w", err)
	}
	sps, pps := videoFormat.SafeParams()
	track := &gortmplib.Track{Codec: &rtmpcodecs.H264{SPS: sps, PPS: pps}}
	writer := &gortmplib.Writer{Conn: rtmpClient, Tracks: []*gortmplib.Track{track}}
	if err := writer.Initialize(); err != nil {
		return fmt.Errorf("initialize H264 RTMP writer: %w", err)
	}

	var dtsExtractor *h264.DTSExtractor
	rtspClient.OnPacketRTP(media, videoFormat, func(packet *rtp.Packet) {
		accessUnit, err := decoder.Decode(packet)
		if err != nil {
			if !errors.Is(err, rtph264.ErrMorePacketsNeeded) &&
				!errors.Is(err, rtph264.ErrNonStartingPacketAndNoPrevious) {
				reportError(fmt.Errorf("decode H264 RTP packet: %w", err))
			}
			return
		}
		pts, ok := rtspClient.PacketPTS(media, packet)
		if !ok {
			return
		}

		idrPresent, videoPresent := h264VideoTypes(accessUnit)
		if dtsExtractor == nil {
			if !idrPresent {
				return
			}
			dtsExtractor = &h264.DTSExtractor{}
			dtsExtractor.Initialize()
		} else if !videoPresent {
			return
		}

		extractorUnit := accessUnit
		if !containsH264SPS(accessUnit) && len(sps) > 0 {
			extractorUnit = append([][]byte{sps}, accessUnit...)
		}
		dts, err := dtsExtractor.Extract(extractorUnit, pts)
		if err != nil {
			reportError(fmt.Errorf("extract H264 DTS: %w", err))
			return
		}
		if err := rtmpClient.NetConn().SetWriteDeadline(time.Now().Add(publisherWriteTimeout)); err != nil {
			reportError(fmt.Errorf("set RTMP write deadline: %w", err))
			return
		}
		if err := writer.WriteH264(
			track,
			timestampToDuration(pts, videoFormat.ClockRate()),
			timestampToDuration(dts, videoFormat.ClockRate()),
			accessUnit,
		); err != nil {
			reportError(err)
			return
		}
		onPushing()
	})
	return nil
}

func configureH265Forwarder(
	rtspClient *gortsplib.Client,
	media *description.Media,
	videoFormat *format.H265,
	rtmpClient *gortmplib.Client,
	reportError func(error),
	onPushing func(),
) error {
	decoder, err := videoFormat.CreateDecoder()
	if err != nil {
		return fmt.Errorf("create H265 RTP decoder: %w", err)
	}
	vps, sps, pps := videoFormat.SafeParams()
	track := &gortmplib.Track{Codec: &rtmpcodecs.H265{VPS: vps, SPS: sps, PPS: pps}}
	writer := &gortmplib.Writer{Conn: rtmpClient, Tracks: []*gortmplib.Track{track}}
	if err := writer.Initialize(); err != nil {
		return fmt.Errorf("initialize H265 RTMP writer: %w", err)
	}

	var dtsExtractor *h265.DTSExtractor
	rtspClient.OnPacketRTP(media, videoFormat, func(packet *rtp.Packet) {
		accessUnit, err := decoder.Decode(packet)
		if err != nil {
			if !errors.Is(err, rtph265.ErrMorePacketsNeeded) &&
				!errors.Is(err, rtph265.ErrNonStartingPacketAndNoPrevious) {
				reportError(fmt.Errorf("decode H265 RTP packet: %w", err))
			}
			return
		}
		pts, ok := rtspClient.PacketPTS(media, packet)
		if !ok {
			return
		}

		if dtsExtractor == nil {
			if !h265RandomAccess(accessUnit) {
				return
			}
			dtsExtractor = &h265.DTSExtractor{}
			dtsExtractor.Initialize()
		}
		extractorUnit := accessUnit
		if !containsH265SPS(accessUnit) && len(sps) > 0 {
			extractorUnit = prependNALUs(accessUnit, vps, sps, pps)
		}
		dts, err := dtsExtractor.Extract(extractorUnit, pts)
		if err != nil {
			reportError(fmt.Errorf("extract H265 DTS: %w", err))
			return
		}
		if err := rtmpClient.NetConn().SetWriteDeadline(time.Now().Add(publisherWriteTimeout)); err != nil {
			reportError(fmt.Errorf("set RTMP write deadline: %w", err))
			return
		}
		if err := writer.WriteH265(
			track,
			timestampToDuration(pts, videoFormat.ClockRate()),
			timestampToDuration(dts, videoFormat.ClockRate()),
			accessUnit,
		); err != nil {
			reportError(err)
			return
		}
		onPushing()
	})
	return nil
}

func publisherDialContext(ctx context.Context, attemptDone <-chan struct{}) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{}
	return func(callCtx context.Context, network, address string) (net.Conn, error) {
		dialCtx, cancel := context.WithCancel(callCtx)
		stopParentCancel := func() bool { return false }
		if ctx.Err() != nil {
			cancel()
		} else {
			stopParentCancel = context.AfterFunc(ctx, cancel)
		}
		conn, err := dialer.DialContext(dialCtx, network, address)
		stopParentCancel()
		cancel()
		if err != nil {
			return nil, err
		}
		go func() {
			select {
			case <-ctx.Done():
				_ = conn.Close()
			case <-attemptDone:
			}
		}()
		return conn, nil
	}
}

func normalizeRTMPClientURL(rawURL string) (*url.URL, error) {
	target, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, errors.New("invalid RTMP URL")
	}
	if err := ValidateRTMPURL(target.String()); err != nil {
		return nil, err
	}
	if target.Port() == "" {
		port := "1935"
		if strings.EqualFold(target.Scheme, "rtmps") {
			port = "1936"
		}
		target.Host = net.JoinHostPort(target.Hostname(), port)
	}
	return target, nil
}

func h264VideoTypes(accessUnit [][]byte) (bool, bool) {
	idrPresent := false
	videoPresent := false
	for _, nalu := range accessUnit {
		if len(nalu) == 0 {
			continue
		}
		switch h264.NALUType(nalu[0] & 0x1F) {
		case h264.NALUTypeIDR:
			idrPresent = true
			videoPresent = true
		case h264.NALUTypeNonIDR:
			videoPresent = true
		}
	}
	return idrPresent, videoPresent
}

func containsH264SPS(accessUnit [][]byte) bool {
	for _, nalu := range accessUnit {
		if len(nalu) > 0 && h264.NALUType(nalu[0]&0x1F) == h264.NALUTypeSPS {
			return true
		}
	}
	return false
}

func containsH265SPS(accessUnit [][]byte) bool {
	for _, nalu := range accessUnit {
		if len(nalu) > 0 && h265.NALUType((nalu[0]>>1)&0b111111) == h265.NALUType_SPS_NUT {
			return true
		}
	}
	return false
}

func h265RandomAccess(accessUnit [][]byte) bool {
	for _, nalu := range accessUnit {
		if len(nalu) > 0 && h265.IsRandomAccess([][]byte{nalu}) {
			return true
		}
	}
	return false
}

func prependNALUs(accessUnit [][]byte, parameters ...[]byte) [][]byte {
	result := make([][]byte, 0, len(parameters)+len(accessUnit))
	for _, parameter := range parameters {
		if len(parameter) > 0 {
			result = append(result, parameter)
		}
	}
	return append(result, accessUnit...)
}

func timestampToDuration(value int64, clockRate int) time.Duration {
	seconds := value / int64(clockRate)
	remainder := value % int64(clockRate)
	return time.Duration(seconds)*time.Second +
		time.Duration(remainder)*time.Second/time.Duration(clockRate)
}

func (p *rtmpPublisher) retryDelay(index int) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index >= len(p.options.RetryDelays) {
		return p.options.RetryDelays[len(p.options.RetryDelays)-1]
	}
	return p.options.RetryDelays[index]
}

func (p *rtmpPublisher) targetURL() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.options.URL
}

func (p *rtmpPublisher) setStatus(state RTMPState, active bool, lastError string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.setStatusLocked(state, active, lastError)
}

func (p *rtmpPublisher) setStatusLocked(state RTMPState, active bool, lastError string) {
	now := p.now()
	p.status = RTMPStatus{
		Enabled:   p.options.Enabled,
		Active:    active,
		State:     state,
		LastError: strings.TrimSpace(lastError),
		UpdatedAt: &now,
	}
}

func (p *rtmpPublisher) markStopped() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancel = nil
	p.done = nil
	p.setStatusLocked(initialRTMPState(p.options.Enabled), false, p.status.LastError)
}

func initialRTMPState(enabled bool) RTMPState {
	if enabled {
		return RTMPStateIdle
	}
	return RTMPStateDisabled
}

func redactRTMPError(message, target string) string {
	message = strings.TrimSpace(message)
	if target == "" {
		return message
	}
	message = strings.ReplaceAll(message, target, "<rtmp-target>")
	parsed, err := url.Parse(target)
	if err != nil {
		return message
	}
	for _, sensitive := range []string{
		parsed.Host,
		parsed.User.String(),
		parsed.RawQuery,
		parsed.EscapedPath(),
	} {
		if sensitive != "" {
			message = strings.ReplaceAll(message, sensitive, "<redacted>")
		}
	}
	return message
}
