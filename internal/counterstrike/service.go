package counterstrike

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"drone-management/internal/model"
	"drone-management/internal/protocol"
	"drone-management/internal/store"
)

const (
	defaultStrikeDurationSeconds = 60
	minStrikeDurationSeconds     = 10
	maxStrikeDurationSeconds     = 180
	maxPublishLogs               = 8
	maxIdempotencyEntries        = 256
	connectRetryDelay            = 10 * time.Second
)

var _ protocol.Connector = (*Service)(nil)

type interferenceController interface {
	ListChannels() []model.InterferenceChannel
	ListChannelsCached() []model.InterferenceChannel
	ScreenStrikeActive() bool
	SetScreenStrike(model.ScreenStrikeRequest) (model.ScreenStrikeState, error)
}

// Option configures the strike protocol service.
type Option func(*Service)

// WithTransport replaces MQTT transport, primarily for tests.
func WithTransport(value transport) Option {
	return func(service *Service) {
		if value != nil {
			service.transport = value
		}
	}
}

// WithNow replaces the time source, primarily for tests.
func WithNow(now func() time.Time) Option {
	return func(service *Service) {
		if now != nil {
			service.now = now
		}
	}
}

// WithInterferenceController connects protocol controls to local interference hardware.
func WithInterferenceController(controller interferenceController) Option {
	return func(service *Service) { service.interference = controller }
}

// Service implements the generic counter-device strike V1.0 MQTT protocol.
type Service struct {
	store        *store.Store
	transport    transport
	interference interferenceController
	now          func() time.Time
	wake         chan struct{}

	mu                   sync.RWMutex
	settings             model.CounterStrikeSettings
	settingsKey          string
	status               model.CounterStrikeStatus
	subscribed           bool
	nextConnectAttemptAt time.Time

	controlMu       sync.Mutex
	responses       map[string]controlResponse
	responseTaskIDs []string
}

// NewService creates the generic strike protocol integration.
func NewService(state *store.Store, settings model.UserSettings, opts ...Option) *Service {
	service := &Service{
		store:     state,
		transport: newPahoTransport(),
		now:       time.Now,
		wake:      make(chan struct{}, 1),
		responses: map[string]controlResponse{},
	}
	for _, opt := range opts {
		opt(service)
	}
	service.ApplySettings(settings)
	return service
}

// Name returns the protocol connector name.
func (s *Service) Name() string { return protocolName }

// ApplySettings updates the runtime configuration.
func (s *Service) ApplySettings(settings model.UserSettings) {
	next := model.CounterStrikeSettingsWithGeneratedClientID(model.UserSettingsWithDefaults(settings).CounterStrike)
	now := s.now()
	key := settingsFingerprint(next)

	s.mu.Lock()
	changed := s.settingsKey != key
	s.settings = next
	s.settingsKey = key
	s.status.Enabled = next.Enabled
	s.status.Configured = configured(next)
	s.status.ClientID = strings.TrimSpace(next.ClientID)
	s.status.Broker = strings.TrimSpace(next.Broker)
	s.status.DeviceTypeAbbr = strings.TrimSpace(next.Device.DeviceTypeAbbr)
	s.status.DeviceID = strings.TrimSpace(next.Device.DeviceID)
	s.status.UpdatedAt = cloneTime(now)
	if changed {
		s.subscribed = false
		s.nextConnectAttemptAt = time.Time{}
		s.status.Connected = false
		s.status.Connecting = next.Enabled && configured(next)
		s.status.LastError = ""
		s.status.LastRegisterAt = nil
		s.status.LastStatusAt = nil
		s.status.PublishLogs = nil
	}
	s.mu.Unlock()

	if changed {
		s.transport.Disconnect()
	}
	s.signal()
}

// Status returns a runtime snapshot.
func (s *Service) Status() model.CounterStrikeStatus {
	s.mu.RLock()
	status := s.status
	status.Connected = s.transport.Connected()
	status.PublishLogs = cloneLogs(status.PublishLogs)
	s.mu.RUnlock()
	if s.interference != nil && s.interference.ScreenStrikeActive() {
		status.WorkState = 1
	} else {
		status.WorkState = 0
	}
	return status
}

// PublishDebug publishes a manually supplied JSON payload through the active MQTT connection.
// It is intentionally exposed for the protocol management diagnostics panel.
func (s *Service) PublishDebug(ctx context.Context, topic string, payload []byte, encrypt bool) error {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return fmt.Errorf("debug topic is required")
	}
	settings := s.settingsSnapshot()
	if !configured(settings) {
		return fmt.Errorf("counter strike protocol is not configured")
	}
	if !s.transport.Connected() {
		return fmt.Errorf("MQTT is not connected")
	}
	data := append([]byte(nil), payload...)
	if encrypt {
		var err error
		data, err = encryptSM4CBC(data, settings.SM4Key, settings.SM4IV)
		if err != nil {
			return err
		}
	}
	publishCtx, cancel := context.WithTimeout(ctx, defaultMQTTTimeout)
	err := s.transport.Publish(publishCtx, topic, data)
	cancel()
	s.recordPublish("debug", topic, string(payload), err)
	return err
}

// Run processes protocol timers until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer s.transport.Disconnect()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		case <-s.wake:
			s.tick(ctx)
		}
	}
}

func (s *Service) tick(ctx context.Context) {
	settings := s.settingsSnapshot()
	if !settings.Enabled {
		s.transport.Disconnect()
		s.setConnectionState(false, false, "")
		return
	}
	if !configured(settings) {
		s.transport.Disconnect()
		s.setConnectionState(false, false, "MQTT, device, and SM4 configuration is incomplete")
		return
	}
	if !s.transport.Connected() {
		if s.connectRetryPending(s.now()) {
			return
		}
		s.mu.Lock()
		s.subscribed = false
		s.mu.Unlock()
		s.setConnectionState(false, true, "")
		connectCtx, cancel := context.WithTimeout(ctx, defaultMQTTTimeout)
		err := s.transport.Connect(connectCtx, transportConfig{
			Broker: settings.Broker, ClientID: settings.ClientID,
			Username: settings.Username, Password: settings.Password,
		})
		cancel()
		if err != nil {
			s.setNextConnectAttempt(s.now().Add(connectRetryDelay))
			s.setConnectionState(false, false, err.Error())
			return
		}
	}
	if err := s.ensureSubscriptions(ctx, settings); err != nil {
		s.transport.Disconnect()
		s.setNextConnectAttempt(s.now().Add(connectRetryDelay))
		s.setConnectionState(false, false, err.Error())
		return
	}
	s.clearNextConnectAttempt()
	s.setConnectionState(true, false, "")

	settings = s.settingsWithRuntimeDevice(settings)
	now := s.now()
	status := s.Status()
	if due(status.LastRegisterAt, time.Duration(settings.RegisterIntervalSeconds)*time.Second, now) {
		payload := buildRegistrationPayload(settings, status.WorkState)
		s.publishEncrypted(ctx, "device", registrationTopic(settings), payload, settings, func(at time.Time) {
			s.mu.Lock()
			s.status.LastRegisterAt = cloneTime(at)
			s.mu.Unlock()
		})
	}
	interval := time.Duration(settings.StatusIntervalSeconds) * time.Second
	if status.WorkState == 1 {
		interval = time.Second
	}
	if due(status.LastStatusAt, interval, now) {
		payload := buildStatusPayload(settings.Device, status.WorkState)
		s.publishEncrypted(ctx, "device_state", statusTopic(settings), payload, settings, func(at time.Time) {
			s.mu.Lock()
			s.status.LastStatusAt = cloneTime(at)
			s.mu.Unlock()
		})
	}
}

func (s *Service) ensureSubscriptions(ctx context.Context, settings model.CounterStrikeSettings) error {
	s.mu.RLock()
	subscribed := s.subscribed
	s.mu.RUnlock()
	if subscribed {
		return nil
	}
	handler := func(topic string, payload []byte) { s.handleControl(topic, payload) }
	for _, topic := range []string{controlTopic(settings), legacyControlPath} {
		subscribeCtx, cancel := context.WithTimeout(ctx, defaultMQTTTimeout)
		err := s.transport.Subscribe(subscribeCtx, topic, handler)
		cancel()
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", topic, err)
		}
	}
	s.mu.Lock()
	s.subscribed = true
	s.mu.Unlock()
	return nil
}

func (s *Service) handleControl(topic string, payload []byte) {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()

	settings := s.settingsSnapshot()
	var req controlEnvelope
	if err := json.Unmarshal(payload, &req); err != nil {
		if topic == legacyControlPath {
			return
		}
		s.publishControlResponse(settings, buildControlResponse(req, 1, "invalid JSON payload", nil, s.now()))
		return
	}
	if topic == legacyControlPath && (strings.TrimSpace(req.DeviceID) != strings.TrimSpace(settings.Device.DeviceID) ||
		strings.TrimSpace(req.DeviceTypeAbbr) != strings.TrimSpace(settings.Device.DeviceTypeAbbr)) {
		return
	}
	if cached, ok := s.responses[strings.TrimSpace(req.TaskID)]; ok && strings.TrimSpace(req.TaskID) != "" {
		s.publishControlResponse(settings, cached)
		return
	}

	code, message, applied := s.executeControl(settings, req)
	response := buildControlResponse(req, code, message, applied, s.now())
	s.rememberResponse(response)
	s.publishControlResponse(settings, response)
	s.mu.Lock()
	s.status.LastControlAt = cloneTime(s.now())
	s.status.LastControlResult = message
	if code == 0 {
		s.status.LastControlResult = strings.TrimSuffix(response.Cmd, ".resp")
	}
	s.status.LastStatusAt = nil
	s.mu.Unlock()
	s.signal()
}

func (s *Service) executeControl(settings model.CounterStrikeSettings, req controlEnvelope) (int, string, map[string]any) {
	if strings.TrimSpace(req.TaskID) == "" {
		return 1, "taskId is required", nil
	}
	if strings.TrimSpace(req.DeviceID) != strings.TrimSpace(settings.Device.DeviceID) {
		return 1, "deviceId does not match this device", nil
	}
	if strings.TrimSpace(req.DeviceTypeAbbr) != strings.TrimSpace(settings.Device.DeviceTypeAbbr) {
		return 1, "deviceTypeAbbr does not match this device", nil
	}
	if s.interference == nil {
		return 1, "interference service is unavailable", nil
	}
	switch strings.TrimSpace(req.Cmd) {
	case "strike.close":
		if _, err := s.interference.SetScreenStrike(model.ScreenStrikeRequest{Enabled: false}); err != nil {
			return 1, err.Error(), nil
		}
		return 0, "success", map[string]any{"enabled": false}
	case "strike.open":
		if req.Params.Power != nil {
			return 1, "power control is unsupported by local interference hardware", nil
		}
		channelIDs, err := s.interferenceChannelIDs(req.Params.FreqList)
		if err != nil {
			return 1, err.Error(), nil
		}
		duration := normalizeDuration(req.Duration)
		if _, err := s.interference.SetScreenStrike(model.ScreenStrikeRequest{
			Enabled: true, ChannelIDs: channelIDs, DurationSeconds: duration,
		}); err != nil {
			return 1, err.Error(), nil
		}
		return 0, "success", map[string]any{
			"freqList": s.protocolBandsForChannelIDs(channelIDs),
			"duration": duration,
		}
	default:
		return 1, "unsupported cmd", nil
	}
}

func (s *Service) interferenceChannelIDs(bands []string) ([]string, error) {
	channels := s.interference.ListChannels()
	if len(bands) == 0 {
		ids := make([]string, 0, len(channels))
		for _, channel := range channels {
			if !channel.Reserved && strings.TrimSpace(channel.ID) != "" {
				ids = append(ids, channel.ID)
			}
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("no interference channels are configured")
		}
		return ids, nil
	}

	selected := make([]string, 0, len(bands))
	seen := map[string]struct{}{}
	missing := make([]string, 0)
	for _, band := range bands {
		normalized := normalizeBand(band)
		matched := false
		for _, channel := range channels {
			if channel.Reserved || strings.TrimSpace(channel.ID) == "" {
				continue
			}
			if channelMatchesBand(channel, normalized) {
				if _, ok := seen[channel.ID]; !ok {
					selected = append(selected, channel.ID)
					seen[channel.ID] = struct{}{}
				}
				matched = true
			}
		}
		if !matched {
			missing = append(missing, strings.TrimSpace(band))
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("unmatched interference bands: %s", strings.Join(missing, ","))
	}
	return selected, nil
}

func (s *Service) protocolBandsForChannelIDs(ids []string) []string {
	if len(ids) == 0 || s.interference == nil {
		return nil
	}
	byID := make(map[string]model.InterferenceChannel)
	for _, channel := range s.interference.ListChannelsCached() {
		byID[channel.ID] = channel
	}
	result := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		channel, ok := byID[id]
		if !ok {
			continue
		}
		values := channel.Bands
		if len(values) == 0 {
			values = []string{channel.Label}
		}
		for _, value := range values {
			label := protocolBandLabel(value)
			if label == "" {
				continue
			}
			if _, ok := seen[label]; ok {
				continue
			}
			seen[label] = struct{}{}
			result = append(result, label)
		}
	}
	return result
}

func channelMatchesBand(channel model.InterferenceChannel, normalized string) bool {
	for _, band := range channel.Bands {
		if normalizeBand(band) == normalized {
			return true
		}
	}
	return normalizeBand(channel.Label) == normalized
}

func normalizeBand(value string) string {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
	switch {
	case strings.HasSuffix(value, "ghz"):
		return canonicalBand(strings.TrimSuffix(value, "ghz"))
	case strings.HasSuffix(value, "g"):
		return canonicalBand(strings.TrimSuffix(value, "g"))
	case strings.HasSuffix(value, "mhz"):
		return normalizeMHz(strings.TrimSuffix(value, "mhz"))
	case strings.HasSuffix(value, "m"):
		return normalizeMHz(strings.TrimSuffix(value, "m"))
	default:
		return canonicalBand(value)
	}
}

func normalizeMHz(value string) string {
	frequency, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return value
	}
	if frequency >= 1000 {
		frequency /= 1000
	}
	return canonicalBand(strings.TrimRight(strings.TrimRight(strconv.FormatFloat(frequency, 'f', 3, 64), "0"), "."))
}

func canonicalBand(value string) string {
	switch value {
	case "0.9", "0.915", "900", "915":
		return "915"
	case "1.2", "1200":
		return "1.2"
	case "1.4", "1400":
		return "1.4"
	case "1.5", "1500":
		return "1.5"
	case "2.4", "2400":
		return "2.4"
	case "5.2", "5200":
		return "5.2"
	case "5.8", "5800":
		return "5.8"
	default:
		return value
	}
}

func protocolBandLabel(value string) string {
	switch normalizeBand(value) {
	case "433":
		return "433M"
	case "915":
		return "915M"
	case "1.2":
		return "1.2G"
	case "1.4":
		return "1.4G"
	case "1.5":
		return "1.5G"
	case "2.4":
		return "2.4G"
	case "5.2":
		return "5.2G"
	case "5.8":
		return "5.8G"
	default:
		return strings.TrimSpace(value)
	}
}

func normalizeDuration(seconds int) int {
	if seconds <= 0 {
		return defaultStrikeDurationSeconds
	}
	if seconds < minStrikeDurationSeconds {
		return minStrikeDurationSeconds
	}
	if seconds > maxStrikeDurationSeconds {
		return maxStrikeDurationSeconds
	}
	return seconds
}

func (s *Service) publishEncrypted(
	ctx context.Context,
	kind string,
	topic string,
	payload any,
	settings model.CounterStrikeSettings,
	onSuccess func(time.Time),
) {
	plain, err := json.Marshal(payload)
	if err != nil {
		s.recordPublish(kind, topic, "", err)
		return
	}
	encrypted, err := encryptSM4CBC(plain, settings.SM4Key, settings.SM4IV)
	if err == nil {
		publishCtx, cancel := context.WithTimeout(ctx, defaultMQTTTimeout)
		err = s.transport.Publish(publishCtx, topic, encrypted)
		cancel()
	}
	s.recordPublish(kind, topic, string(plain), err)
	if err == nil && onSuccess != nil {
		onSuccess(s.now())
	}
}

func (s *Service) publishControlResponse(settings model.CounterStrikeSettings, response controlResponse) {
	data, err := json.Marshal(response)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), defaultMQTTTimeout)
		err = s.transport.Publish(ctx, controlResponsePath, data)
		cancel()
	}
	s.recordPublish("strike_response", controlResponsePath, string(data), err)
	if err != nil {
		s.setError(err.Error())
	}
}

func (s *Service) rememberResponse(response controlResponse) {
	if response.TaskID == "" {
		return
	}
	if _, exists := s.responses[response.TaskID]; exists {
		return
	}
	s.responses[response.TaskID] = response
	s.responseTaskIDs = append(s.responseTaskIDs, response.TaskID)
	if len(s.responseTaskIDs) > maxIdempotencyEntries {
		oldest := s.responseTaskIDs[0]
		s.responseTaskIDs = s.responseTaskIDs[1:]
		delete(s.responses, oldest)
	}
}

func (s *Service) settingsWithRuntimeDevice(settings model.CounterStrikeSettings) model.CounterStrikeSettings {
	if s.store != nil {
		location := s.store.DeviceLocation()
		if location.Valid && location.Point != nil {
			settings = model.CounterStrikeSettingsWithDeviceLocation(settings, location.Point)
		}
	}
	if s.interference != nil {
		bands := bandsFromChannels(s.interference.ListChannelsCached())
		if len(bands) > 0 {
			settings.Device.Bands = bands
		}
	}
	return settings
}

func bandsFromChannels(channels []model.InterferenceChannel) []string {
	bands := make([]string, 0)
	seen := map[string]struct{}{}
	for _, channel := range channels {
		if channel.Reserved {
			continue
		}
		values := channel.Bands
		if len(values) == 0 {
			values = []string{channel.Label}
		}
		for _, value := range values {
			value = protocolBandLabel(value)
			if value == "" {
				continue
			}
			key := strings.ToLower(value)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			bands = append(bands, value)
		}
	}
	return bands
}

func configured(settings model.CounterStrikeSettings) bool {
	if !settings.Enabled || !settings.Device.Enabled ||
		strings.TrimSpace(settings.Broker) == "" ||
		strings.TrimSpace(settings.ProviderCode) == "" ||
		strings.TrimSpace(settings.BridgeCode) == "" ||
		strings.TrimSpace(settings.Device.DeviceID) == "" {
		return false
	}
	deviceName := strings.TrimSpace(settings.Device.DeviceName)
	if deviceName == "" || strings.EqualFold(deviceName, strings.TrimSpace(settings.Device.DeviceID)) {
		return false
	}
	if strings.TrimSpace(settings.Device.DeviceSpec.DevModel) == "" ||
		strings.TrimSpace(settings.Device.DeviceSpec.InstLoc) == "" {
		return false
	}
	if _, err := decodeSM4Material(settings.SM4Key); err != nil {
		return false
	}
	if _, err := decodeSM4Material(settings.SM4IV); err != nil {
		return false
	}
	return true
}

func due(last *time.Time, interval time.Duration, now time.Time) bool {
	return last == nil || last.IsZero() || now.Sub(*last) >= interval
}

func settingsFingerprint(settings model.CounterStrikeSettings) string {
	data, _ := json.Marshal(settings)
	return string(data)
}

func (s *Service) settingsSnapshot() model.CounterStrikeSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

func (s *Service) recordPublish(kind, topic, payload string, err error) {
	entry := model.LingyunPublishLog{Kind: kind, Topic: topic, Payload: payload, Success: err == nil, At: s.now()}
	if err != nil {
		entry.Error = err.Error()
	}
	s.mu.Lock()
	s.status.PublishLogs = append([]model.LingyunPublishLog{entry}, s.status.PublishLogs...)
	if len(s.status.PublishLogs) > maxPublishLogs {
		s.status.PublishLogs = s.status.PublishLogs[:maxPublishLogs]
	}
	if err != nil {
		s.status.LastError = err.Error()
	}
	s.status.UpdatedAt = cloneTime(entry.At)
	s.mu.Unlock()
}

func (s *Service) setConnectionState(connected, connecting bool, errorMessage string) {
	s.mu.Lock()
	s.status.Connected = connected
	s.status.Connecting = connecting
	s.status.LastError = errorMessage
	s.status.UpdatedAt = cloneTime(s.now())
	s.mu.Unlock()
}

func (s *Service) setError(message string) {
	s.mu.Lock()
	s.status.LastError = message
	s.status.UpdatedAt = cloneTime(s.now())
	s.mu.Unlock()
}

func (s *Service) connectRetryPending(now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.nextConnectAttemptAt.IsZero() && now.Before(s.nextConnectAttemptAt)
}

func (s *Service) setNextConnectAttempt(at time.Time) {
	s.mu.Lock()
	s.nextConnectAttemptAt = at
	s.mu.Unlock()
}

func (s *Service) clearNextConnectAttempt() {
	s.mu.Lock()
	s.nextConnectAttemptAt = time.Time{}
	s.mu.Unlock()
}

func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func cloneTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	cloned := value
	return &cloned
}

func cloneLogs(logs []model.LingyunPublishLog) []model.LingyunPublishLog {
	return append([]model.LingyunPublishLog(nil), logs...)
}
