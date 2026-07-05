package lingyun

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"drone-management/internal/model"
	"drone-management/internal/protocol"
	"drone-management/internal/store"
)

const (
	eventPositionUpdated = "screen.position.updated"
	eventFPVUpdated      = "screen.fpv.updated"
	eventStrikeUpdated   = "screen.strike.updated"
)

const (
	defaultInterferenceDurationSeconds = 60
	minInterferenceDurationSeconds     = 10
	maxInterferenceDurationSeconds     = 180
	maxDevicePublishLogs               = 8
	defaultMQTTRetryDelay              = 10 * time.Second
)

var _ protocol.Connector = (*Service)(nil)

// Option configures a Lingyun protocol service.
type Option func(*Service)

type interferenceController interface {
	ListChannels() []model.InterferenceChannel
	ScreenStrikeState() model.ScreenStrikeState
	SetScreenStrike(model.ScreenStrikeRequest) (model.ScreenStrikeState, error)
}

type interferenceCachedChannelProvider interface {
	ListChannelsCached() []model.InterferenceChannel
}

// WithTransport replaces MQTT transport, primarily for tests.
func WithTransport(transport transport) Option {
	return func(s *Service) {
		if transport != nil {
			s.transport = transport
		}
	}
}

// WithNow replaces the time source, primarily for tests.
func WithNow(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

// WithInterferenceController connects Lingyun interference controls to local hardware control.
func WithInterferenceController(controller interferenceController) Option {
	return func(s *Service) {
		s.interference = controller
	}
}

type deviceRuntimeState struct {
	ReportingEnabled   bool
	LastRegisterAt     time.Time
	LastStatusAt       time.Time
	LastDataAt         time.Time
	LastControlAt      time.Time
	LastControlResult  string
	LastError          string
	MsgCnt             int64
	PublishInFlight    bool
	PublishInFlightKey string
	PublishLogs        []model.LingyunPublishLog
}

type devicePublishJob struct {
	settingsKey string
	def         deviceDefinition
	device      model.LingyunDeviceSettings
	register    bool
	status      bool
	data        bool
}

// Service publishes local targets to China Mobile Lingyun and handles controls.
type Service struct {
	store        *store.Store
	transport    transport
	now          func() time.Time
	interference interferenceController

	mu                   sync.RWMutex
	settings             model.LingyunSettings
	states               map[string]*deviceRuntimeState
	pending              map[string]map[string]senseDataObject
	status               model.LingyunStatus
	settingsKey          string
	clientID             string
	subscribed           bool
	seeded               bool
	nextConnectAttemptAt time.Time
	wake                 chan struct{}
}

// NewService creates a Lingyun protocol integration.
func NewService(state *store.Store, settings model.UserSettings, opts ...Option) *Service {
	s := &Service{
		store:     state,
		transport: newPahoTransport(),
		now:       time.Now,
		states:    map[string]*deviceRuntimeState{},
		pending:   map[string]map[string]senseDataObject{},
		clientID:  model.NewLingyunClientID(),
		wake:      make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.ApplySettings(settings)
	return s
}

// Name returns the protocol connector name.
func (s *Service) Name() string {
	return protocolName
}

// ApplySettings updates Lingyun runtime configuration.
func (s *Service) ApplySettings(settings model.UserSettings) {
	next := model.UserSettingsWithDefaults(settings).Lingyun
	now := s.now()

	s.mu.Lock()
	if strings.TrimSpace(s.clientID) == "" {
		s.clientID = model.NewLingyunClientID()
	}
	next.ClientID = s.clientID
	key := settingsFingerprint(next)
	changed := s.settingsKey != key
	s.settings = next
	s.settingsKey = key
	for _, device := range next.Devices {
		if _, ok := s.states[device.Type]; !ok {
			s.states[device.Type] = &deviceRuntimeState{ReportingEnabled: true}
		}
		if _, ok := s.pending[device.Type]; !ok {
			s.pending[device.Type] = map[string]senseDataObject{}
		}
	}
	s.status.Enabled = next.Enabled
	s.status.Configured = lingyunConfigured(next)
	s.status.ClientID = strings.TrimSpace(next.ClientID)
	s.status.Broker = strings.TrimSpace(next.Broker)
	s.status.UpdatedAt = cloneTime(now)
	if changed {
		s.subscribed = false
		s.seeded = false
		s.nextConnectAttemptAt = time.Time{}
		s.pending = map[string]map[string]senseDataObject{}
		for _, device := range next.Devices {
			s.pending[device.Type] = map[string]senseDataObject{}
		}
		for _, state := range s.states {
			state.LastRegisterAt = time.Time{}
			state.LastStatusAt = time.Time{}
			state.LastDataAt = time.Time{}
			state.LastError = ""
			state.PublishLogs = nil
		}
		s.status.Connected = false
		s.status.Connecting = next.Enabled && lingyunConfigured(next)
	}
	s.mu.Unlock()

	if changed {
		s.transport.Disconnect()
	}
	s.signal()
}

// Status returns a runtime snapshot.
func (s *Service) Status() model.LingyunStatus {
	interferenceActive := s.interferenceActive()
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := s.status
	status.Connected = s.transport.Connected()
	status.Devices = s.deviceStatusesLocked(interferenceActive)
	status.UpdatedAt = cloneTimeValue(status.UpdatedAt)
	return status
}

// Run processes store events and protocol timers until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	events, unsubscribe := s.store.Subscribe(64)
	defer unsubscribe()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.transport.Disconnect()
			return
		case evt := <-events:
			s.handleEvent(evt)
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
		s.clearAllPending()
		s.clearNextConnectAttempt()
		s.markDisconnected()
		s.setConnected(false, "")
		return
	}
	if !lingyunConfigured(settings) {
		s.clearNextConnectAttempt()
		s.setConnected(false, "Lingyun MQTT/provider/device config is incomplete")
		return
	}
	if !s.transport.Connected() && s.connectRetryPending(s.now()) {
		return
	}
	s.setConnecting(true, "")
	if err := s.connect(ctx, settings); err != nil {
		s.setNextConnectAttempt(s.now().Add(defaultMQTTRetryDelay))
		s.setConnected(false, err.Error())
		return
	}
	s.clearNextConnectAttempt()

	if !s.isSeeded() {
		s.seedCurrentTargets(settings)
		s.setSeeded()
	}

	now := s.now()
	settingsKey := s.settingsKeySnapshot()
	jobs := make([]devicePublishJob, 0, len(settings.Devices))
	for _, device := range settings.Devices {
		def, ok := definitionByType(device.Type)
		if !ok || !device.Enabled || strings.TrimSpace(device.DeviceID) == "" {
			continue
		}
		job := devicePublishJob{
			settingsKey: settingsKey,
			def:         def,
			device:      device,
			register:    s.deviceDue(device.Type, "register", now, time.Duration(settings.RegisterIntervalSeconds)*time.Second),
		}
		statusInterval := s.statusInterval(settings, device)
		job.status = s.deviceDue(device.Type, "status", now, statusInterval)
		job.data = device.Type != model.LingyunDeviceInterference &&
			s.deviceDue(device.Type, "data", now, time.Duration(settings.PublishMinIntervalSeconds)*time.Second)
		if job.register || job.status || job.data {
			jobs = append(jobs, job)
		}
	}
	s.publishDeviceJobs(ctx, settings, jobs)
}

func (s *Service) publishDeviceJobs(ctx context.Context, settings model.LingyunSettings, jobs []devicePublishJob) {
	for _, job := range jobs {
		job := job
		if !s.tryStartDevicePublishJob(job.device.Type, job.settingsKey) {
			continue
		}
		go func() {
			defer s.finishDevicePublishJob(job.device.Type, job.settingsKey)
			s.publishDeviceJob(ctx, settings, job)
		}()
	}
}

func (s *Service) publishDeviceJob(ctx context.Context, settings model.LingyunSettings, job devicePublishJob) {
	if job.register {
		if !s.settingsKeyMatches(job.settingsKey) {
			return
		}
		if err := s.publishRegistration(ctx, settings, job.settingsKey, job.def, job.device); err != nil {
			if s.settingsKeyMatches(job.settingsKey) {
				s.setDeviceError(job.device.Type, err.Error())
			}
		}
	}
	if job.status {
		if !s.settingsKeyMatches(job.settingsKey) {
			return
		}
		if err := s.publishStatus(ctx, settings, job.settingsKey, job.def, job.device); err != nil {
			if s.settingsKeyMatches(job.settingsKey) {
				s.setDeviceError(job.device.Type, err.Error())
			}
		}
	}
	if job.data {
		if !s.settingsKeyMatches(job.settingsKey) {
			return
		}
		if err := s.flushDevice(ctx, settings, job.settingsKey, job.def, job.device); err != nil {
			if s.settingsKeyMatches(job.settingsKey) {
				s.setDeviceError(job.device.Type, err.Error())
			}
		}
	}
}

func (s *Service) connect(ctx context.Context, settings model.LingyunSettings) error {
	if s.transport.Connected() && s.isSubscribed() {
		s.setConnected(true, "")
		return nil
	}
	if err := s.transport.Connect(ctx, transportConfig{
		Broker:   settings.Broker,
		ClientID: settings.ClientID,
		Username: settings.Username,
		Password: settings.Password,
	}); err != nil {
		return err
	}
	if err := s.subscribeControls(ctx, settings); err != nil {
		return err
	}
	s.mu.Lock()
	s.subscribed = true
	s.status.Connected = true
	s.status.Connecting = false
	s.status.LastError = ""
	s.status.UpdatedAt = cloneTime(s.now())
	s.mu.Unlock()
	return nil
}

func (s *Service) subscribeControls(ctx context.Context, settings model.LingyunSettings) error {
	topics := make([]string, 0, len(settings.Devices))
	for _, device := range settings.Devices {
		def, ok := definitionByType(device.Type)
		if !ok || !device.Enabled || strings.TrimSpace(device.DeviceID) == "" {
			continue
		}
		topics = append(topics, controlTopic(settings, def, device))
	}
	errCh := make(chan error, len(topics))
	for _, topic := range topics {
		topic := topic
		go func() {
			errCh <- s.transport.Subscribe(ctx, topic, s.handleControlMessage)
		}()
	}
	var firstErr error
	for range topics {
		if err := <-errCh; err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (s *Service) publishRegistration(ctx context.Context, settings model.LingyunSettings, settingsKey string, def deviceDefinition, device model.LingyunDeviceSettings) error {
	device = s.deviceWithCurrentLocation(device)
	device = s.deviceWithCurrentInterferenceBands(device)
	payload := buildDevicePayload(settings, def, device, s.currentWorkState(device))
	topic := deviceTopic(settings, def, device, "device")
	payloadText, err := s.publishJSON(ctx, topic, payload)
	if s.settingsKeyMatches(settingsKey) {
		s.recordDevicePublish(device.Type, "device", topic, payloadText, err)
	}
	if err != nil {
		return err
	}
	if s.settingsKeyMatches(settingsKey) {
		s.markDevicePublished(device.Type, "register")
	}
	return nil
}

func (s *Service) publishStatus(ctx context.Context, settings model.LingyunSettings, settingsKey string, def deviceDefinition, device model.LingyunDeviceSettings) error {
	device = s.deviceWithCurrentLocation(device)
	payload := buildStatusPayload(device, s.currentWorkState(device))
	topic := deviceTopic(settings, def, device, "device_state")
	payloadText, err := s.publishJSON(ctx, topic, payload)
	if s.settingsKeyMatches(settingsKey) {
		s.recordDevicePublish(device.Type, "device_state", topic, payloadText, err)
	}
	if err != nil {
		return err
	}
	if s.settingsKeyMatches(settingsKey) {
		s.markDevicePublished(device.Type, "status")
	}
	return nil
}

func (s *Service) flushDevice(ctx context.Context, settings model.LingyunSettings, settingsKey string, def deviceDefinition, device model.LingyunDeviceSettings) error {
	if device.Type == model.LingyunDeviceInterference {
		return nil
	}
	objects := s.popPending(device.Type)
	if len(objects) == 0 {
		return nil
	}
	if !s.reportingEnabled(device.Type) {
		return nil
	}
	objects = completeDataObjectsForPublish(device.Type, objects)
	msgCnt := s.nextMsgCnt(device.Type)
	payload := dataPayload{
		DeviceID:        strings.TrimSpace(device.DeviceID),
		MsgCnt:          msgCnt,
		PointTime:       s.now().UnixMilli(),
		ProtocolVersion: strings.TrimSpace(settings.ProtocolVersion),
		Objects:         objects,
	}
	topic := deviceTopic(settings, def, device, "device_data")
	payloadText, err := s.publishJSON(ctx, topic, payload)
	if s.settingsKeyMatches(settingsKey) {
		s.recordDevicePublish(device.Type, "device_data", topic, payloadText, err)
	}
	if err != nil {
		s.restorePending(device.Type, objects)
		return err
	}
	if s.settingsKeyMatches(settingsKey) {
		s.markDevicePublished(device.Type, "data")
	}
	return nil
}

func completeDataObjectsForPublish(deviceType string, objects []senseDataObject) []senseDataObject {
	if deviceType == model.LingyunDeviceAOA {
		return objects
	}
	for index := range objects {
		if objects[index].Speed == nil {
			speed := 0.0
			objects[index].Speed = &speed
		}
	}
	return objects
}

func (s *Service) publishJSON(ctx context.Context, topic string, payload any) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal Lingyun payload: %w", err)
	}
	publishCtx, cancel := context.WithTimeout(ctx, defaultMQTTTimeout)
	defer cancel()
	if err := s.transport.Publish(publishCtx, topic, data); err != nil {
		return string(data), err
	}
	return string(data), nil
}

func (s *Service) deviceWithCurrentLocation(device model.LingyunDeviceSettings) model.LingyunDeviceSettings {
	if s == nil || s.store == nil {
		return device
	}
	location := s.store.DeviceLocation()
	if !location.Valid || location.Point == nil {
		return device
	}
	return model.LingyunSettingsWithDeviceLocation(
		model.LingyunSettings{Devices: []model.LingyunDeviceSettings{device}},
		location.Point,
	).Devices[0]
}

func (s *Service) deviceWithCurrentInterferenceBands(device model.LingyunDeviceSettings) model.LingyunDeviceSettings {
	if device.Type != model.LingyunDeviceInterference || s.interference == nil {
		return device
	}
	var channels []model.InterferenceChannel
	if provider, ok := s.interference.(interferenceCachedChannelProvider); ok {
		channels = provider.ListChannelsCached()
	} else {
		channels = s.interference.ListChannels()
	}
	if bands := lingyunInterferenceBandsFromChannels(channels); len(bands) > 0 {
		device.Bands = bands
	}
	return device
}

func lingyunInterferenceBandsFromChannels(channels []model.InterferenceChannel) []string {
	bands := make([]string, 0, len(channels))
	seen := map[string]struct{}{}
	for _, channel := range channels {
		if channel.Reserved {
			continue
		}
		for _, band := range channel.Bands {
			formatted := formatLingyunInterferenceBand(band)
			if formatted == "" {
				continue
			}
			if _, ok := seen[formatted]; ok {
				continue
			}
			bands = append(bands, formatted)
			seen[formatted] = struct{}{}
		}
	}
	return bands
}

func formatLingyunInterferenceBand(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	frequency, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return value
	}
	unit := "G"
	if frequency >= 100 {
		unit = "M"
	}
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(frequency, 'f', 3, 64), "0"), ".") + unit
}

func (s *Service) handleEvent(evt model.Event) {
	settings := s.settingsSnapshot()
	if !settings.Enabled {
		return
	}
	switch evt.Type {
	case eventPositionUpdated:
		target, ok := evt.Payload.(model.ScreenPositionTarget)
		if !ok {
			return
		}
		for _, device := range settings.Devices {
			if !device.Enabled || !s.reportingEnabled(device.Type) {
				continue
			}
			object, ok := projectPosition(target, device, s.now())
			if ok {
				s.addPending(device.Type, object)
			}
		}
	case eventFPVUpdated:
		target, ok := evt.Payload.(model.ScreenFPVTarget)
		if !ok {
			return
		}
		device, ok := lingyunDevice(settings, model.LingyunDeviceAOA)
		if !ok || !device.Enabled || !s.reportingEnabled(device.Type) {
			return
		}
		object, ok := projectAOA(target, device, s.now())
		if ok {
			s.addPending(device.Type, object)
		}
	case eventStrikeUpdated:
		s.forceDeviceStatusDue(model.LingyunDeviceInterference)
		s.signal()
	}
}

func (s *Service) seedCurrentTargets(settings model.LingyunSettings) {
	for _, target := range s.store.Positions(0) {
		for _, device := range settings.Devices {
			if !device.Enabled || !s.reportingEnabled(device.Type) {
				continue
			}
			object, ok := projectPosition(target, device, s.now())
			if ok {
				s.addPending(device.Type, object)
			}
		}
	}
	device, ok := lingyunDevice(settings, model.LingyunDeviceAOA)
	if !ok || !device.Enabled || !s.reportingEnabled(device.Type) {
		return
	}
	for _, target := range s.store.FPV(0) {
		object, ok := projectAOA(target, device, s.now())
		if ok {
			s.addPending(device.Type, object)
		}
	}
}

func (s *Service) handleControlMessage(topic string, payload []byte) {
	settings := s.settingsSnapshot()
	def, device, ok := definitionByTopic(settings.ProviderCode, topic, settings)
	if !ok {
		return
	}

	var req controlEnvelope
	if err := json.Unmarshal(payload, &req); err != nil {
		req.Head.DeviceID = device.DeviceID
		req.Head.Time = s.now().UnixMilli()
		req.Data.OperationCmd = def.OperationCmd
		s.publishControlResponse(settings, def, device, req, 1, "控制消息格式错误")
		s.setDeviceControl(device.Type, false, "invalid control")
		return
	}
	if strings.TrimSpace(req.Head.DeviceID) == "" {
		req.Head.DeviceID = device.DeviceID
	}

	if device.Type == model.LingyunDeviceInterference {
		code, message := s.handleInterferenceControl(def, device, req)
		s.publishControlResponse(settings, def, device, req, code, message)
		return
	}

	code := 0
	message := ""
	switch {
	case !def.supportsOperationCmd(req.Data.OperationCmd):
		code = 1
		message = "不支持的控制指令"
	case req.Data.OperationType != 0 && req.Data.OperationType != 1:
		code = 1
		message = "不支持的操作类型"
	default:
		reporting := req.Data.OperationType == 1
		s.setReporting(device.Type, reporting)
		s.setDeviceControl(device.Type, true, controlResult(reporting))
		if !reporting {
			s.clearPending(device.Type)
		} else {
			s.clearSeeded()
			s.signal()
		}
	}
	if code != 0 {
		s.setDeviceControl(device.Type, false, message)
	}
	s.publishControlResponse(settings, def, device, req, code, message)
}

func (s *Service) handleInterferenceControl(def deviceDefinition, device model.LingyunDeviceSettings, req controlEnvelope) (int, string) {
	if !def.supportsOperationCmd(req.Data.OperationCmd) {
		s.setDeviceControl(device.Type, false, "不支持的控制指令")
		return 1, "不支持的控制指令"
	}
	if req.Data.OperationType != 0 && req.Data.OperationType != 1 {
		s.setDeviceControl(device.Type, false, "不支持的操作类型")
		return 1, "不支持的操作类型"
	}
	if s.interference == nil {
		s.setDeviceControl(device.Type, false, "干扰服务不可用")
		return 1, "干扰服务不可用"
	}
	if req.Data.OperationType == 0 {
		if _, err := s.interference.SetScreenStrike(model.ScreenStrikeRequest{Enabled: false}); err != nil {
			message := err.Error()
			s.setDeviceControl(device.Type, false, message)
			return 1, message
		}
		s.setDeviceControl(device.Type, true, "stopped")
		s.forceDeviceStatusDue(device.Type)
		s.signal()
		return 0, ""
	}

	params, err := decodeInterferenceControlParams(req.Data.OperationParams)
	if err != nil {
		s.setDeviceControl(device.Type, false, "控制参数格式错误")
		return 1, "控制参数格式错误"
	}
	channelIDs, err := s.interferenceChannelIDs(params.Bands)
	if err != nil {
		message := err.Error()
		s.setDeviceControl(device.Type, false, message)
		return 1, message
	}
	durationSeconds := normalizeInterferenceDuration(params.Duration)
	if _, err := s.interference.SetScreenStrike(model.ScreenStrikeRequest{
		Enabled:         true,
		ChannelIDs:      channelIDs,
		DurationSeconds: durationSeconds,
	}); err != nil {
		message := err.Error()
		s.setDeviceControl(device.Type, false, message)
		return 1, message
	}
	s.setDeviceControl(device.Type, true, "started")
	s.forceDeviceStatusDue(device.Type)
	s.signal()
	return 0, ""
}

type interferenceControlParams struct {
	Bands    []string
	Duration int
}

func decodeInterferenceControlParams(raw json.RawMessage) (interferenceControlParams, error) {
	if len(raw) == 0 || strings.EqualFold(strings.TrimSpace(string(raw)), "null") {
		return interferenceControlParams{}, nil
	}
	var params struct {
		Bands    []string `json:"bands"`
		Duration int      `json:"duration"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return interferenceControlParams{}, err
	}
	return interferenceControlParams{
		Bands:    params.Bands,
		Duration: params.Duration,
	}, nil
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
			return nil, fmt.Errorf("未配置可用干扰通道")
		}
		return ids, nil
	}

	selected := make([]string, 0, len(bands))
	seen := map[string]struct{}{}
	missing := make([]string, 0)
	for _, band := range bands {
		normalizedBand := normalizeInterferenceBand(band)
		if normalizedBand == "" {
			continue
		}
		matched := false
		for _, channel := range channels {
			if channel.Reserved || strings.TrimSpace(channel.ID) == "" {
				continue
			}
			if interferenceChannelMatchesBand(channel, normalizedBand) {
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
		return nil, fmt.Errorf("未匹配干扰频段: %s", strings.Join(missing, ","))
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("未配置可用干扰通道")
	}
	return selected, nil
}

func interferenceChannelMatchesBand(channel model.InterferenceChannel, normalizedBand string) bool {
	for _, band := range channel.Bands {
		if normalizeInterferenceBand(band) == normalizedBand {
			return true
		}
	}
	return normalizeInterferenceBand(channel.Label) == normalizedBand
}

func normalizeInterferenceBand(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "")
	switch {
	case strings.HasSuffix(value, "ghz"):
		return strings.TrimSuffix(value, "ghz")
	case strings.HasSuffix(value, "g"):
		return strings.TrimSuffix(value, "g")
	case strings.HasSuffix(value, "mhz"):
		return normalizeMHzBand(strings.TrimSuffix(value, "mhz"))
	case strings.HasSuffix(value, "m"):
		return normalizeMHzBand(strings.TrimSuffix(value, "m"))
	default:
		return value
	}
}

func normalizeMHzBand(value string) string {
	frequency, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return value
	}
	if frequency >= 1000 {
		frequency = frequency / 1000
	}
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(frequency, 'f', 3, 64), "0"), ".")
}

func normalizeInterferenceDuration(seconds int) int {
	if seconds <= 0 {
		return defaultInterferenceDurationSeconds
	}
	if seconds < minInterferenceDurationSeconds {
		return minInterferenceDurationSeconds
	}
	if seconds > maxInterferenceDurationSeconds {
		return maxInterferenceDurationSeconds
	}
	return seconds
}

func (s *Service) publishControlResponse(settings model.LingyunSettings, def deviceDefinition, device model.LingyunDeviceSettings, req controlEnvelope, code int, message string) {
	resp := buildControlResponse(req, code, message, s.now())
	topic := controlResponseTopic(settings, def, device)
	payloadText, err := s.publishJSON(context.Background(), topic, resp)
	s.recordDevicePublish(device.Type, "device_control_resp", topic, payloadText, err)
	if err != nil {
		s.setDeviceError(device.Type, err.Error())
	}
}

func (s *Service) settingsSnapshot() model.LingyunSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

func (s *Service) settingsKeySnapshot() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settingsKey
}

func (s *Service) settingsKeyMatches(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settingsKey == key
}

func (s *Service) addPending(deviceType string, object senseDataObject) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pending[deviceType]; !ok {
		s.pending[deviceType] = map[string]senseDataObject{}
	}
	s.pending[deviceType][object.ObjectID] = object
}

func (s *Service) popPending(deviceType string) []senseDataObject {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.pending[deviceType]
	if len(items) == 0 {
		return nil
	}
	objects := make([]senseDataObject, 0, len(items))
	for _, object := range items {
		objects = append(objects, object)
	}
	s.pending[deviceType] = map[string]senseDataObject{}
	return objects
}

func (s *Service) restorePending(deviceType string, objects []senseDataObject) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pending[deviceType]; !ok {
		s.pending[deviceType] = map[string]senseDataObject{}
	}
	for _, object := range objects {
		s.pending[deviceType][object.ObjectID] = object
	}
}

func (s *Service) clearPending(deviceType string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[deviceType] = map[string]senseDataObject{}
}

func (s *Service) clearAllPending() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for deviceType := range s.pending {
		s.pending[deviceType] = map[string]senseDataObject{}
	}
}

func (s *Service) reportingEnabled(deviceType string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := s.states[deviceType]
	return state == nil || state.ReportingEnabled
}

func (s *Service) setReporting(deviceType string, reporting bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.deviceStateLocked(deviceType)
	state.ReportingEnabled = reporting
}

func (s *Service) forceDeviceStatusDue(deviceType string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.deviceStateLocked(deviceType)
	state.LastStatusAt = time.Time{}
}

func (s *Service) nextMsgCnt(deviceType string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.deviceStateLocked(deviceType)
	state.MsgCnt++
	if state.MsgCnt > 2147483647 {
		state.MsgCnt = 0
	}
	return state.MsgCnt
}

func (s *Service) deviceDue(deviceType string, kind string, now time.Time, interval time.Duration) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := s.states[deviceType]
	if state == nil {
		return true
	}
	var last time.Time
	switch kind {
	case "register":
		last = state.LastRegisterAt
	case "status":
		last = state.LastStatusAt
	case "data":
		last = state.LastDataAt
	}
	return last.IsZero() || interval <= 0 || now.Sub(last) >= interval
}

func (s *Service) markDevicePublished(deviceType string, kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.deviceStateLocked(deviceType)
	now := s.now()
	switch kind {
	case "register":
		state.LastRegisterAt = now
	case "status":
		state.LastStatusAt = now
	case "data":
		state.LastDataAt = now
	}
	state.LastError = ""
	s.status.UpdatedAt = cloneTime(now)
}

func (s *Service) recordDevicePublish(deviceType string, kind string, topic string, payload string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.deviceStateLocked(deviceType)
	now := s.now()
	entry := model.LingyunPublishLog{
		Kind:    kind,
		Topic:   topic,
		Payload: payload,
		Success: err == nil,
		At:      now,
	}
	if err != nil {
		entry.Error = err.Error()
	}
	state.PublishLogs = append([]model.LingyunPublishLog{entry}, state.PublishLogs...)
	if len(state.PublishLogs) > maxDevicePublishLogs {
		state.PublishLogs = state.PublishLogs[:maxDevicePublishLogs]
	}
	s.status.UpdatedAt = cloneTime(now)
}

func (s *Service) setDeviceError(deviceType string, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.deviceStateLocked(deviceType)
	state.LastError = message
	s.status.LastError = message
	s.status.UpdatedAt = cloneTime(s.now())
	if message != "" {
		slog.Warn("Lingyun protocol error", "deviceType", deviceType, "error", message)
	}
}

func (s *Service) setDeviceControl(deviceType string, ok bool, result string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.deviceStateLocked(deviceType)
	state.LastControlAt = s.now()
	state.LastControlResult = result
	if ok {
		state.LastError = ""
	} else {
		state.LastError = result
	}
	s.status.UpdatedAt = cloneTime(state.LastControlAt)
}

func (s *Service) setConnected(connected bool, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Enabled = s.settings.Enabled
	s.status.Configured = lingyunConfigured(s.settings)
	s.status.ClientID = strings.TrimSpace(s.settings.ClientID)
	s.status.Broker = strings.TrimSpace(s.settings.Broker)
	s.status.Connected = connected
	s.status.Connecting = false
	s.status.LastError = message
	s.status.UpdatedAt = cloneTime(s.now())
}

func (s *Service) setConnecting(connecting bool, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Enabled = s.settings.Enabled
	s.status.Configured = lingyunConfigured(s.settings)
	s.status.ClientID = strings.TrimSpace(s.settings.ClientID)
	s.status.Broker = strings.TrimSpace(s.settings.Broker)
	s.status.Connecting = connecting
	if connecting {
		s.status.Connected = false
	}
	s.status.LastError = message
	s.status.UpdatedAt = cloneTime(s.now())
}

func (s *Service) connectRetryPending(now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.nextConnectAttemptAt.IsZero() && now.Before(s.nextConnectAttemptAt)
}

func (s *Service) setNextConnectAttempt(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextConnectAttemptAt = at
}

func (s *Service) clearNextConnectAttempt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextConnectAttemptAt = time.Time{}
}

func (s *Service) markDisconnected() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscribed = false
	s.seeded = false
	s.status.Connected = false
	s.status.Connecting = false
	s.status.UpdatedAt = cloneTime(s.now())
}

func (s *Service) deviceStateLocked(deviceType string) *deviceRuntimeState {
	state := s.states[deviceType]
	if state == nil {
		state = &deviceRuntimeState{ReportingEnabled: true}
		s.states[deviceType] = state
	}
	return state
}

func (s *Service) tryStartDevicePublishJob(deviceType string, settingsKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.deviceStateLocked(deviceType)
	if state.PublishInFlight && state.PublishInFlightKey == settingsKey {
		return false
	}
	state.PublishInFlight = true
	state.PublishInFlightKey = settingsKey
	return true
}

func (s *Service) finishDevicePublishJob(deviceType string, settingsKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.deviceStateLocked(deviceType)
	if !state.PublishInFlight || state.PublishInFlightKey != settingsKey {
		return
	}
	state.PublishInFlight = false
	state.PublishInFlightKey = ""
}

func (s *Service) isSubscribed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.subscribed
}

func (s *Service) isSeeded() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.seeded
}

func (s *Service) setSeeded() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seeded = true
}

func (s *Service) clearSeeded() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seeded = false
}

func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) deviceStatusesLocked(interferenceActive bool) []model.LingyunDeviceStatus {
	items := make([]model.LingyunDeviceStatus, 0, len(s.settings.Devices))
	for _, device := range s.settings.Devices {
		def, ok := definitionByType(device.Type)
		if !ok {
			continue
		}
		state := s.states[device.Type]
		reporting := true
		if state != nil {
			reporting = state.ReportingEnabled
		}
		status := model.LingyunDeviceStatus{
			Type:             device.Type,
			Abbr:             def.Abbr,
			DeviceID:         strings.TrimSpace(device.DeviceID),
			Enabled:          device.Enabled,
			ReportingEnabled: reporting,
			WorkState:        statusWorkState(device, reporting, interferenceActive),
		}
		if state != nil {
			status.LastRegisterAt = cloneTimeValue(&state.LastRegisterAt)
			status.LastStatusAt = cloneTimeValue(&state.LastStatusAt)
			status.LastDataAt = cloneTimeValue(&state.LastDataAt)
			status.LastControlAt = cloneTimeValue(&state.LastControlAt)
			status.LastControlResult = state.LastControlResult
			status.LastError = state.LastError
			status.PublishLogs = clonePublishLogs(state.PublishLogs)
		}
		items = append(items, status)
	}
	return items
}

func (s *Service) statusInterval(settings model.LingyunSettings, device model.LingyunDeviceSettings) time.Duration {
	if device.Type == model.LingyunDeviceInterference && s.interferenceActive() {
		return time.Second
	}
	return time.Duration(settings.StatusIntervalSeconds) * time.Second
}

func (s *Service) currentWorkState(device model.LingyunDeviceSettings) int {
	reporting := s.reportingEnabled(device.Type)
	return statusWorkState(device, reporting, s.interferenceActive())
}

func statusWorkState(device model.LingyunDeviceSettings, reporting bool, interferenceActive bool) int {
	if device.Type == model.LingyunDeviceInterference {
		if device.Enabled && interferenceActive {
			return 1
		}
		return 0
	}
	return workState(device.Enabled, reporting)
}

func (s *Service) interferenceActive() bool {
	if s.interference == nil {
		return false
	}
	return s.interference.ScreenStrikeState().Active
}

func lingyunConfigured(settings model.LingyunSettings) bool {
	if !settings.Enabled {
		return false
	}
	if strings.TrimSpace(settings.Broker) == "" || strings.TrimSpace(settings.ProviderCode) == "" {
		return false
	}
	for _, device := range settings.Devices {
		if device.Enabled && strings.TrimSpace(device.DeviceID) != "" {
			return true
		}
	}
	return false
}

func settingsFingerprint(settings model.LingyunSettings) string {
	data, err := json.Marshal(settings)
	if err != nil {
		return ""
	}
	return string(data)
}

func lingyunDevice(settings model.LingyunSettings, deviceType string) (model.LingyunDeviceSettings, bool) {
	for _, device := range settings.Devices {
		if device.Type == deviceType {
			return device, true
		}
	}
	return model.LingyunDeviceSettings{}, false
}

func controlResult(reporting bool) string {
	if reporting {
		return "started"
	}
	return "stopped"
}

func cloneTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	cloned := value
	return &cloned
}

func cloneTimeValue(value *time.Time) *time.Time {
	if value == nil || value.IsZero() {
		return nil
	}
	cloned := *value
	return &cloned
}

func clonePublishLogs(logs []model.LingyunPublishLog) []model.LingyunPublishLog {
	if len(logs) == 0 {
		return nil
	}
	cloned := make([]model.LingyunPublishLog, len(logs))
	copy(cloned, logs)
	return cloned
}
