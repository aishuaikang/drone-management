package interference

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"dr600ab-net/internal/model"
	"dr600ab-net/internal/store"
)

const (
	screenStrikeEventType          = "screen.strike.updated"
	screenStrikeMinDurationSeconds = 10
	screenStrikeMaxDurationSeconds = 180
)

type codedError struct {
	code    string
	message string
}

func (e *codedError) Error() string {
	return e.message
}

// ErrorCode returns a stable error code for Service errors.
func ErrorCode(err error) string {
	var coded *codedError
	if errors.As(err, &coded) {
		return coded.code
	}
	return ""
}

// PinFactory creates GPIO pins from external IO numbers.
type PinFactory func(number int) GPIOPin

// ReportStore persists screen interference operation reports.
type ReportStore interface {
	Create(contextReport model.InterferenceReport) (model.InterferenceReport, error)
	CreateRunning(contextReport model.InterferenceReport) (model.InterferenceReport, error)
	Update(contextReport model.InterferenceReport) error
}

// UserSettingsStore reads user settings used in report labels.
type UserSettingsStore interface {
	LoadUser() (model.UserSettings, bool, error)
}

// Service manages GPIO channel state and screen strike lifecycle.
type Service struct {
	mu sync.RWMutex

	channels   map[string]*channelState
	order      []string
	pinFactory PinFactory
	store      *store.Store
	reports    ReportStore
	settings   UserSettingsStore

	strikeTimer           *time.Timer
	strikeSeq             uint64
	strikeActive          bool
	strikeChannelIDs      []string
	strikeDurationSeconds int
	strikeStartedAt       time.Time
	strikeEndsAt          time.Time
	activeReport          *model.InterferenceReport
	activeReportID        string
}

type channelState struct {
	def          ChannelDefinition
	pin          GPIOPin
	initialized  bool
	enabled      bool
	actualLevel  string
	desiredLevel string
	status       string
	lastError    string
}

// NewService creates a GPIO control service.
func NewService(
	store *store.Store,
	definitions []ChannelDefinition,
	pinFactory PinFactory,
) *Service {
	if pinFactory == nil {
		pinFactory = func(number int) GPIOPin {
			return NewPin(number)
		}
	}
	if definitions == nil {
		definitions = DefaultChannels()
	}

	channels := make(map[string]*channelState, len(definitions))
	order := make([]string, 0, len(definitions))
	for _, def := range definitions {
		channels[def.ID] = &channelState{
			def:          def,
			actualLevel:  "unknown",
			desiredLevel: "low",
			status:       "idle",
		}
		order = append(order, def.ID)
	}
	return &Service{
		channels:   channels,
		order:      order,
		pinFactory: pinFactory,
		store:      store,
	}
}

// SetReportStore sets the report store.
func (s *Service) SetReportStore(reports ReportStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports = reports
}

// SetUserSettingsStore sets the user settings store.
func (s *Service) SetUserSettingsStore(settings UserSettingsStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = settings
}

// ListChannels returns channel state in stable display order.
func (s *Service) ListChannels() []model.GpioChannel {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]model.GpioChannel, 0, len(s.order))
	for _, id := range s.order {
		result = append(result, s.dtoWithActual(s.channels[id]))
	}
	return result
}

// SetState sets one channel high or low.
func (s *Service) SetState(id string, enabled bool) (model.GpioChannel, error) {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.channels[id]
	if !ok {
		return model.GpioChannel{}, s.codedError("channel_not_found", "interference channel was not found")
	}
	if enabled && s.isScreenStrikeChannelIDLocked(id) {
		return s.dtoWithActual(state), s.codedError("strike_channel_requires_timed_operation", "screen strike channels must be started with timed strike control")
	}
	channel, err := s.setStateLocked(id, enabled)
	if err == nil && s.isScreenStrikeChannelIDLocked(id) {
		if !s.screenStrikeHasHighChannelLocked() {
			s.strikeSeq++
			if s.strikeTimer != nil {
				s.strikeTimer.Stop()
				s.strikeTimer = nil
			}
			s.clearScreenStrikeLocked()
			s.finishActiveReportLocked(
				model.InterferenceReportStatusCompleted,
				"",
				nil,
				time.Now(),
				s.screenStrikeStateLocked(time.Now()),
			)
		}
		s.publishScreenStrikeLocked(s.screenStrikeStateLocked(time.Now()))
	}
	return channel, err
}

// ScreenStrikeState returns current screen strike state.
func (s *Service) ScreenStrikeState() model.ScreenStrikeState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.screenStrikeStateLocked(time.Now())
}

// SetScreenStrike starts or stops screen interference operation.
func (s *Service) SetScreenStrike(req model.ScreenStrikeRequest) (model.ScreenStrikeState, error) {
	durationSeconds := req.DurationSeconds
	duration := time.Duration(durationSeconds) * time.Second
	return s.applyScreenStrike(req.Enabled, req.ChannelIDs, duration, durationSeconds)
}

func (s *Service) applyScreenStrike(
	enabled bool,
	channelIDs []string,
	duration time.Duration,
	durationSeconds int,
) (model.ScreenStrikeState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !enabled {
		s.strikeSeq++
		if s.strikeTimer != nil {
			s.strikeTimer.Stop()
			s.strikeTimer = nil
		}
		err := s.stopScreenStrikeChannelsLocked()
		s.clearScreenStrikeLocked()
		endedAt := time.Now()
		state := s.screenStrikeStateLocked(endedAt)
		s.finishActiveReportLocked(model.InterferenceReportStatusCompleted, "", err, endedAt, state)
		s.publishScreenStrikeLocked(state)
		return state, err
	}

	startedAt := time.Now()
	req := model.ScreenStrikeRequest{
		Enabled:         enabled,
		ChannelIDs:      append([]string{}, channelIDs...),
		DurationSeconds: durationSeconds,
	}
	selected, err := s.validateScreenStrikeChannelsLocked(channelIDs)
	if err != nil {
		return s.screenStrikeStateLocked(time.Now()), err
	}
	if duration <= 0 || durationSeconds < screenStrikeMinDurationSeconds || durationSeconds > screenStrikeMaxDurationSeconds {
		return s.screenStrikeStateLocked(time.Now()), s.codedError("strike_invalid_duration", "interference duration must be between 10 and 180 seconds")
	}
	if s.screenStrikeHasHighChannelLocked() {
		return s.screenStrikeStateLocked(time.Now()), s.codedError("strike_already_active", "interference is already active")
	}

	s.strikeSeq++
	if s.strikeTimer != nil {
		s.strikeTimer.Stop()
		s.strikeTimer = nil
	}

	for _, id := range selected {
		if _, err := s.setStateLocked(id, true); err != nil {
			_ = s.stopScreenStrikeChannelIDsLocked(selected)
			s.clearScreenStrikeLocked()
			state := s.screenStrikeStateLocked(time.Now())
			s.createFailedReportLocked(req, selected, startedAt, err, state)
			s.publishScreenStrikeLocked(state)
			return state, err
		}
	}

	now := time.Now()
	endsAt := now.Add(duration)
	s.strikeActive = true
	s.strikeChannelIDs = append([]string{}, selected...)
	s.strikeDurationSeconds = durationSeconds
	s.strikeStartedAt = startedAt
	s.strikeEndsAt = endsAt

	seq := s.strikeSeq
	s.strikeTimer = time.AfterFunc(duration, func() {
		s.stopScreenStrikeOnTimeout(seq)
	})

	state := s.screenStrikeStateLocked(now)
	s.createRunningReportLocked(req, selected, startedAt, state)
	s.publishScreenStrikeLocked(state)
	return state, nil
}

func (s *Service) stopScreenStrikeOnTimeout(seq uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if seq != s.strikeSeq || !s.strikeActive {
		return
	}
	s.strikeSeq++
	s.strikeTimer = nil
	_ = s.stopScreenStrikeChannelsLocked()
	s.clearScreenStrikeLocked()
	endedAt := time.Now()
	state := s.screenStrikeStateLocked(endedAt)
	s.finishActiveReportLocked(model.InterferenceReportStatusCompleted, "", nil, endedAt, state)
	s.publishScreenStrikeLocked(state)
}

func (s *Service) setStateLocked(id string, enabled bool) (model.GpioChannel, error) {
	state, ok := s.channels[id]
	if !ok {
		return model.GpioChannel{}, s.codedError("channel_not_found", "interference channel was not found")
	}
	if state.def.Reserved {
		return s.dtoWithActual(state), s.codedError("channel_reserved", "interference channel is reserved")
	}

	if enabled {
		if state.pin == nil {
			state.pin = s.pinFactory(state.def.Pin)
		}
		if !state.initialized {
			if err := state.pin.Setup(); err != nil {
				return s.markError(state, err)
			}
			state.initialized = true
		}
		if err := state.pin.SetHigh(); err != nil {
			return s.markError(state, err)
		}
		state.enabled = true
		state.actualLevel = "high"
		state.desiredLevel = "high"
		state.status = "active"
		state.lastError = ""
	} else {
		if state.pin == nil {
			pin := s.pinFactory(state.def.Pin)
			if pinOutputHigh(pin) {
				state.pin = pin
			}
		}
		if state.pin != nil {
			if err := state.pin.SetLow(); err != nil {
				return s.markError(state, err)
			}
			state.pin.Cleanup()
			state.pin = nil
			state.initialized = false
		}
		state.enabled = false
		state.actualLevel = "low"
		state.desiredLevel = "low"
		state.status = "idle"
		state.lastError = ""
	}

	channel := s.dtoWithActual(state)
	if s.store != nil {
		s.store.Publish(model.Event{Type: "gpio.channel.updated", Time: time.Now(), Payload: channel})
	}
	return channel, nil
}

// Shutdown lowers all initialized IOs and closes active report as abnormal.
func (s *Service) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.strikeSeq++
	if s.strikeTimer != nil {
		s.strikeTimer.Stop()
		s.strikeTimer = nil
	}
	s.clearScreenStrikeLocked()

	for _, state := range s.channels {
		if state.pin == nil {
			continue
		}
		_ = state.pin.SetLow()
		state.pin.Cleanup()
		state.pin = nil
		state.initialized = false
		state.enabled = false
		state.actualLevel = "low"
		state.desiredLevel = "low"
		state.status = "idle"
	}
	endedAt := time.Now()
	s.finishActiveReportLocked(
		model.InterferenceReportStatusAbnormal,
		"service_shutdown",
		nil,
		endedAt,
		s.screenStrikeStateLocked(endedAt),
	)
}

func (s *Service) validateScreenStrikeChannelsLocked(ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, s.codedError("strike_channels_required", "select at least one interference channel")
	}

	requested := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			requested[id] = struct{}{}
		}
	}
	if len(requested) == 0 {
		return nil, s.codedError("strike_channels_required", "select at least one interference channel")
	}

	selected := make([]string, 0, len(requested))
	for _, id := range s.screenStrikeChannelIDsLocked() {
		if _, ok := requested[id]; ok {
			selected = append(selected, id)
			delete(requested, id)
		}
	}
	if len(requested) > 0 {
		return nil, s.codedError("strike_invalid_channels", "interference channels can only use the first three external IO channels")
	}
	return selected, nil
}

func (s *Service) screenStrikeChannelIDsLocked() []string {
	ids := make([]string, 0, 3)
	for _, id := range s.order {
		state := s.channels[id]
		if state == nil || state.def.Reserved {
			continue
		}
		ids = append(ids, id)
		if len(ids) == 3 {
			break
		}
	}
	return ids
}

func (s *Service) isScreenStrikeChannelIDLocked(id string) bool {
	for _, strikeID := range s.screenStrikeChannelIDsLocked() {
		if strikeID == id {
			return true
		}
	}
	return false
}

func (s *Service) screenStrikeHasHighChannelLocked() bool {
	for _, id := range s.screenStrikeChannelIDsLocked() {
		if s.dtoWithActual(s.channels[id]).Enabled {
			return true
		}
	}
	return false
}

func (s *Service) stopScreenStrikeChannelsLocked() error {
	return s.stopScreenStrikeChannelIDsLocked(s.controlledScreenStrikeChannelIDsLocked())
}

func (s *Service) stopScreenStrikeChannelIDsLocked(ids []string) error {
	var firstErr error
	for _, id := range ids {
		if _, err := s.setStateLocked(id, false); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Service) controlledScreenStrikeChannelIDsLocked() []string {
	if len(s.strikeChannelIDs) > 0 {
		return append([]string{}, s.strikeChannelIDs...)
	}

	ids := make([]string, 0, 3)
	for _, id := range s.screenStrikeChannelIDsLocked() {
		state := s.channels[id]
		if state == nil {
			continue
		}
		if state.initialized || state.pin != nil || state.enabled {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *Service) clearScreenStrikeLocked() {
	s.strikeActive = false
	s.strikeChannelIDs = nil
	s.strikeDurationSeconds = 0
	s.strikeStartedAt = time.Time{}
	s.strikeEndsAt = time.Time{}
}

func (s *Service) screenStrikeStateLocked(now time.Time) model.ScreenStrikeState {
	if now.IsZero() {
		now = time.Now()
	}

	channels := make([]model.GpioChannel, 0, 3)
	activeChannelIDs := make([]string, 0, 3)
	for _, id := range s.screenStrikeChannelIDsLocked() {
		channel := s.dtoWithActual(s.channels[id])
		channels = append(channels, channel)
		if channel.Enabled {
			activeChannelIDs = append(activeChannelIDs, channel.ID)
		}
	}

	if s.strikeActive {
		activeChannelIDs = append([]string{}, s.strikeChannelIDs...)
	}
	active := len(activeChannelIDs) > 0
	state := model.ScreenStrikeState{
		Active:           active,
		ChannelIDs:       append([]string{}, activeChannelIDs...),
		DurationSeconds:  s.strikeDurationSeconds,
		RemainingSeconds: 0,
		Channels:         channels,
	}
	if active && s.strikeActive {
		startedAt := s.strikeStartedAt
		endsAt := s.strikeEndsAt
		state.StartedAt = &startedAt
		state.EndsAt = &endsAt
		state.RemainingSeconds = ceilSeconds(endsAt.Sub(now))
	}
	return state
}

func (s *Service) publishScreenStrikeLocked(state model.ScreenStrikeState) {
	if s.store == nil {
		return
	}
	s.store.Publish(model.Event{Type: screenStrikeEventType, Time: time.Now(), Payload: state})
}

func (s *Service) createRunningReportLocked(
	req model.ScreenStrikeRequest,
	selected []string,
	startedAt time.Time,
	state model.ScreenStrikeState,
) {
	if s.reports == nil {
		return
	}
	labels, pins := s.reportChannelMetadataLocked(selected)
	report := model.InterferenceReport{
		InterferenceReportSummary: model.InterferenceReportSummary{
			Status:                   model.InterferenceReportStatusRunning,
			StartedAt:                startedAt,
			RequestedDurationSeconds: req.DurationSeconds,
			ChannelIDs:               append([]string{}, selected...),
			ChannelLabels:            labels,
			ChannelPins:              pins,
			Summary:                  interferenceReportSummary(labels, req.DurationSeconds),
		},
		Request:    cloneStrikeRequest(req),
		StartState: cloneStrikeState(&state),
	}
	created, err := s.reports.CreateRunning(report)
	if err != nil {
		return
	}
	s.activeReportID = created.ID
	cloned := cloneInterferenceReport(created)
	s.activeReport = &cloned
}

func (s *Service) createFailedReportLocked(
	req model.ScreenStrikeRequest,
	selected []string,
	startedAt time.Time,
	cause error,
	endState model.ScreenStrikeState,
) {
	if s.reports == nil || cause == nil {
		return
	}
	endedAt := time.Now()
	labels, pins := s.reportChannelMetadataLocked(selected)
	report := model.InterferenceReport{
		InterferenceReportSummary: model.InterferenceReportSummary{
			Status:                   model.InterferenceReportStatusFailed,
			StartedAt:                startedAt,
			EndedAt:                  &endedAt,
			RequestedDurationSeconds: req.DurationSeconds,
			ChannelIDs:               append([]string{}, selected...),
			ChannelLabels:            labels,
			ChannelPins:              pins,
			Summary:                  interferenceReportSummary(labels, req.DurationSeconds),
			LastError:                cause.Error(),
		},
		Request:  cloneStrikeRequest(req),
		EndState: cloneStrikeState(&endState),
	}
	_, _ = s.reports.Create(report)
}

func (s *Service) finishActiveReportLocked(
	status model.InterferenceReportStatus,
	reason string,
	cause error,
	endedAt time.Time,
	endState model.ScreenStrikeState,
) {
	if s.reports == nil || s.activeReport == nil || s.activeReportID == "" {
		return
	}
	report := cloneInterferenceReport(*s.activeReport)
	if report.ID == "" {
		return
	}
	report.Status = status
	report.EndedAt = &endedAt
	report.EndState = cloneStrikeState(&endState)
	if cause != nil {
		report.LastError = cause.Error()
	}
	if reason := strings.TrimSpace(reason); reason != "" {
		report.AbnormalReason = reason
		if report.LastError == "" {
			report.LastError = report.AbnormalReason
		}
	}
	_ = s.reports.Update(report)
	s.activeReportID = ""
	s.activeReport = nil
}

func (s *Service) reportChannelMetadataLocked(ids []string) ([]string, []int) {
	labels := make([]string, 0, len(ids))
	pins := make([]int, 0, len(ids))
	customLabels := s.screenStrikeCustomLabelsLocked()
	strikeIndexes := s.screenStrikeChannelIndexesLocked()
	for _, id := range ids {
		state := s.channels[id]
		if state == nil {
			continue
		}
		strikeIndex, ok := strikeIndexes[id]
		if !ok {
			strikeIndex = -1
		}
		labels = append(labels, reportChannelLabel(state.def, strikeIndex, customLabels))
		pins = append(pins, state.def.Pin)
	}
	return labels, pins
}

func (s *Service) screenStrikeCustomLabelsLocked() []string {
	if s.settings == nil {
		return nil
	}
	settings, ok, err := s.settings.LoadUser()
	if err != nil || !ok {
		return nil
	}
	return append([]string{}, settings.ScreenStrikeChannelLabels...)
}

func (s *Service) screenStrikeChannelIndexesLocked() map[string]int {
	indexes := make(map[string]int, 3)
	for index, id := range s.screenStrikeChannelIDsLocked() {
		indexes[id] = index
	}
	return indexes
}

func reportChannelLabel(def ChannelDefinition, strikeIndex int, customLabels []string) string {
	if strikeIndex >= 0 && strikeIndex < len(customLabels) {
		if label := strings.TrimSpace(customLabels[strikeIndex]); label != "" {
			return label
		}
	}
	if label := formatStrikeBands(def.Bands); label != "" {
		return label
	}
	if def.Label != "" {
		return def.Label
	}
	return def.ID
}

func formatStrikeBands(bands []string) string {
	parts := make([]string, 0, len(bands))
	for _, band := range bands {
		if label := formatStrikeBand(band); label != "" {
			parts = append(parts, label)
		}
	}
	return strings.Join(parts, "/")
}

func formatStrikeBand(value string) string {
	band := strings.TrimSpace(value)
	if band == "" {
		return ""
	}
	numeric, err := strconv.ParseFloat(band, 64)
	if err == nil {
		if numeric >= 100 {
			return band + "M"
		}
		return band + "G"
	}
	return band
}

func interferenceReportSummary(labels []string, durationSeconds int) string {
	parts := make([]string, 0, len(labels))
	for _, label := range labels {
		if value := strings.TrimSpace(label); value != "" {
			parts = append(parts, value)
		}
	}
	channelText := strings.Join(parts, ", ")
	if channelText == "" {
		channelText = "unknown"
	}
	if durationSeconds > 0 {
		return fmt.Sprintf("%s / %ds", channelText, durationSeconds)
	}
	return channelText
}

func cloneStrikeRequest(req model.ScreenStrikeRequest) model.ScreenStrikeRequest {
	req.ChannelIDs = append([]string{}, req.ChannelIDs...)
	return req
}

func cloneStrikeState(state *model.ScreenStrikeState) *model.ScreenStrikeState {
	if state == nil {
		return nil
	}
	cloned := *state
	cloned.ChannelIDs = append([]string{}, state.ChannelIDs...)
	cloned.Channels = cloneGPIOChannels(state.Channels)
	if state.StartedAt != nil {
		startedAt := *state.StartedAt
		cloned.StartedAt = &startedAt
	}
	if state.EndsAt != nil {
		endsAt := *state.EndsAt
		cloned.EndsAt = &endsAt
	}
	return &cloned
}

func cloneGPIOChannels(channels []model.GpioChannel) []model.GpioChannel {
	if len(channels) == 0 {
		return []model.GpioChannel{}
	}
	cloned := make([]model.GpioChannel, len(channels))
	for index, channel := range channels {
		cloned[index] = channel
		cloned[index].Bands = append([]string{}, channel.Bands...)
	}
	return cloned
}

func cloneInterferenceReport(report model.InterferenceReport) model.InterferenceReport {
	report.ChannelIDs = append([]string{}, report.ChannelIDs...)
	report.ChannelLabels = append([]string{}, report.ChannelLabels...)
	report.ChannelPins = append([]int{}, report.ChannelPins...)
	report.Request = cloneStrikeRequest(report.Request)
	report.StartState = cloneStrikeState(report.StartState)
	report.EndState = cloneStrikeState(report.EndState)
	if report.EndedAt != nil {
		endedAt := *report.EndedAt
		report.EndedAt = &endedAt
	}
	return report
}

func ceilSeconds(duration time.Duration) int {
	if duration <= 0 {
		return 0
	}
	return int((duration + time.Second - 1) / time.Second)
}

func (s *Service) codedError(code string, fallback string) error {
	return &codedError{code: code, message: fallback}
}

func (s *Service) markError(state *channelState, err error) (model.GpioChannel, error) {
	state.status = "error"
	state.lastError = err.Error()
	channel := state.dto()
	if s.store != nil {
		s.store.Publish(model.Event{Type: "gpio.channel.updated", Time: time.Now(), Payload: channel})
	}
	return channel, err
}

func (s *channelState) dto() model.GpioChannel {
	return model.GpioChannel{
		ID:           s.def.ID,
		Label:        s.def.Label,
		Pin:          s.def.Pin,
		Bands:        append([]string{}, s.def.Bands...),
		Reserved:     s.def.Reserved,
		Enabled:      s.enabled,
		ActualLevel:  s.actualLevel,
		DesiredLevel: s.desiredLevel,
		Status:       s.status,
		LastError:    s.lastError,
	}
}

func (s *Service) dtoWithActual(state *channelState) model.GpioChannel {
	if state == nil {
		return model.GpioChannel{}
	}
	channel := state.dto()

	pin := state.pin
	if pin == nil {
		pin = s.pinFactory(state.def.Pin)
	}
	value, err := pin.GetValue()
	if err != nil {
		return channel
	}

	output := true
	if direction, ok := pinDirection(pin); ok {
		output = direction == "out"
	}
	return applyActualLevel(channel, value, output)
}

func pinOutputHigh(pin GPIOPin) bool {
	value, err := pin.GetValue()
	if err != nil || value == 0 {
		return false
	}
	if direction, ok := pinDirection(pin); ok {
		return direction == "out"
	}
	return true
}

func pinDirection(pin GPIOPin) (string, bool) {
	reader, ok := pin.(gpioDirectionReader)
	if !ok {
		return "", false
	}
	direction, err := reader.GetDirection()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(direction), true
}

func applyActualLevel(channel model.GpioChannel, value int, output bool) model.GpioChannel {
	switch value {
	case 0:
		channel.Enabled = false
		channel.ActualLevel = "low"
		channel.Status = "idle"
	case 1:
		channel.ActualLevel = "high"
		channel.Enabled = output
		if output {
			channel.Status = "active"
		} else {
			channel.Status = "idle"
		}
	default:
		channel.ActualLevel = strconv.Itoa(value)
		channel.Enabled = output && value != 0
		if channel.Enabled {
			channel.Status = "active"
		} else {
			channel.Status = "idle"
		}
	}
	return channel
}
