package lingyun

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
)

var _ protocol.Connector = (*Service)(nil)

// Option configures a Lingyun protocol service.
type Option func(*Service)

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

type deviceRuntimeState struct {
	ReportingEnabled  bool
	LastRegisterAt    time.Time
	LastStatusAt      time.Time
	LastDataAt        time.Time
	LastControlAt     time.Time
	LastControlResult string
	LastError         string
	MsgCnt            int64
}

// Service publishes local targets to China Mobile Lingyun and handles controls.
type Service struct {
	store     *store.Store
	transport transport
	now       func() time.Time

	mu          sync.RWMutex
	settings    model.LingyunSettings
	states      map[string]*deviceRuntimeState
	pending     map[string]map[string]senseDataObject
	status      model.LingyunStatus
	settingsKey string
	subscribed  bool
	seeded      bool
	wake        chan struct{}
}

// NewService creates a Lingyun protocol integration.
func NewService(state *store.Store, settings model.UserSettings, opts ...Option) *Service {
	s := &Service{
		store:     state,
		transport: newPahoTransport(),
		now:       time.Now,
		states:    map[string]*deviceRuntimeState{},
		pending:   map[string]map[string]senseDataObject{},
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
	if strings.TrimSpace(next.ClientID) == "" {
		if current := strings.TrimSpace(s.settings.ClientID); current != "" {
			next.ClientID = current
		} else {
			next.ClientID = model.NewLingyunClientID()
		}
	}
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
	s.status.Broker = strings.TrimSpace(next.Broker)
	s.status.UpdatedAt = cloneTime(now)
	if changed {
		s.subscribed = false
		s.seeded = false
		s.pending = map[string]map[string]senseDataObject{}
		for _, device := range next.Devices {
			s.pending[device.Type] = map[string]senseDataObject{}
		}
		s.status.Connected = false
	}
	s.mu.Unlock()

	if changed {
		s.transport.Disconnect()
	}
	s.signal()
}

// Status returns a runtime snapshot.
func (s *Service) Status() model.LingyunStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := s.status
	status.Connected = s.transport.Connected()
	status.Devices = s.deviceStatusesLocked()
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
		s.markDisconnected()
		s.setConnected(false, "")
		return
	}
	if !lingyunConfigured(settings) {
		s.setConnected(false, "Lingyun MQTT/provider/device config is incomplete")
		return
	}
	if err := s.connect(ctx, settings); err != nil {
		s.setConnected(false, err.Error())
		return
	}

	if !s.isSeeded() {
		s.seedCurrentTargets(settings)
		s.setSeeded()
	}

	now := s.now()
	for _, device := range settings.Devices {
		def, ok := definitionByType(device.Type)
		if !ok || !device.Enabled || strings.TrimSpace(device.DeviceID) == "" {
			continue
		}
		if s.deviceDue(device.Type, "register", now, time.Duration(settings.RegisterIntervalSeconds)*time.Second) {
			if err := s.publishRegistration(ctx, settings, def, device); err != nil {
				s.setDeviceError(device.Type, err.Error())
			}
		}
		if s.deviceDue(device.Type, "status", now, time.Duration(settings.StatusIntervalSeconds)*time.Second) {
			if err := s.publishStatus(ctx, settings, def, device); err != nil {
				s.setDeviceError(device.Type, err.Error())
			}
		}
		if s.deviceDue(device.Type, "data", now, time.Duration(settings.PublishMinIntervalSeconds)*time.Second) {
			if err := s.flushDevice(ctx, settings, def, device); err != nil {
				s.setDeviceError(device.Type, err.Error())
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
	for _, device := range settings.Devices {
		def, ok := definitionByType(device.Type)
		if !ok || !device.Enabled || strings.TrimSpace(device.DeviceID) == "" {
			continue
		}
		topic := controlTopic(settings, def, device)
		if err := s.transport.Subscribe(ctx, topic, s.handleControlMessage); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.subscribed = true
	s.status.Connected = true
	s.status.LastError = ""
	s.status.UpdatedAt = cloneTime(s.now())
	s.mu.Unlock()
	return nil
}

func (s *Service) publishRegistration(ctx context.Context, settings model.LingyunSettings, def deviceDefinition, device model.LingyunDeviceSettings) error {
	device = s.deviceWithCurrentLocation(device)
	reporting := s.reportingEnabled(device.Type)
	payload := buildDevicePayload(settings, def, device, reporting)
	topic := deviceTopic(settings, def, device, "device")
	if err := s.publishJSON(ctx, topic, payload); err != nil {
		return err
	}
	s.markDevicePublished(device.Type, "register")
	return nil
}

func (s *Service) publishStatus(ctx context.Context, settings model.LingyunSettings, def deviceDefinition, device model.LingyunDeviceSettings) error {
	device = s.deviceWithCurrentLocation(device)
	reporting := s.reportingEnabled(device.Type)
	payload := buildStatusPayload(device, reporting)
	topic := deviceTopic(settings, def, device, "device_state")
	if err := s.publishJSON(ctx, topic, payload); err != nil {
		return err
	}
	s.markDevicePublished(device.Type, "status")
	return nil
}

func (s *Service) flushDevice(ctx context.Context, settings model.LingyunSettings, def deviceDefinition, device model.LingyunDeviceSettings) error {
	objects := s.popPending(device.Type)
	if len(objects) == 0 {
		return nil
	}
	if !s.reportingEnabled(device.Type) {
		return nil
	}
	msgCnt := s.nextMsgCnt(device.Type)
	payload := dataPayload{
		DeviceID:        strings.TrimSpace(device.DeviceID),
		MsgCnt:          msgCnt,
		PointTime:       s.now().UnixMilli(),
		ProtocolVersion: strings.TrimSpace(settings.ProtocolVersion),
		Objects:         objects,
	}
	topic := deviceTopic(settings, def, device, "device_data")
	if err := s.publishJSON(ctx, topic, payload); err != nil {
		s.restorePending(device.Type, objects)
		return err
	}
	s.markDevicePublished(device.Type, "data")
	return nil
}

func (s *Service) publishJSON(ctx context.Context, topic string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal Lingyun payload: %w", err)
	}
	publishCtx, cancel := context.WithTimeout(ctx, defaultMQTTTimeout)
	defer cancel()
	if err := s.transport.Publish(publishCtx, topic, data); err != nil {
		return err
	}
	return nil
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

	code := 0
	message := ""
	switch {
	case req.Data.OperationCmd != def.OperationCmd:
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

func (s *Service) publishControlResponse(settings model.LingyunSettings, def deviceDefinition, device model.LingyunDeviceSettings, req controlEnvelope, code int, message string) {
	resp := buildControlResponse(req, code, message, s.now())
	topic := controlResponseTopic(settings, def, device)
	if err := s.publishJSON(context.Background(), topic, resp); err != nil {
		s.setDeviceError(device.Type, err.Error())
	}
}

func (s *Service) settingsSnapshot() model.LingyunSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
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
	s.status.Broker = strings.TrimSpace(s.settings.Broker)
	s.status.Connected = connected
	s.status.LastError = message
	s.status.UpdatedAt = cloneTime(s.now())
}

func (s *Service) markDisconnected() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscribed = false
	s.seeded = false
	s.status.Connected = false
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

func (s *Service) deviceStatusesLocked() []model.LingyunDeviceStatus {
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
			WorkState:        workState(device.Enabled, reporting),
		}
		if state != nil {
			status.LastRegisterAt = cloneTimeValue(&state.LastRegisterAt)
			status.LastStatusAt = cloneTimeValue(&state.LastStatusAt)
			status.LastDataAt = cloneTimeValue(&state.LastDataAt)
			status.LastControlAt = cloneTimeValue(&state.LastControlAt)
			status.LastControlResult = state.LastControlResult
			status.LastError = state.LastError
		}
		items = append(items, status)
	}
	return items
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
