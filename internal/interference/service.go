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

// Output controls one physical interference output.
type Output interface {
	Setup() error
	SetHigh() error
	SetLow() error
	GetValue() (int, error)
	Cleanup()
}

// OutputState is the physical state reported by one relay output.
type OutputState struct {
	Value     int
	Remaining time.Duration
}

// TimedOutput can ask the relay to close and then auto-open after a delay.
type TimedOutput interface {
	SetHighFor(duration time.Duration) error
}

// StateOutput can report both output state and relay-side remaining delay.
type StateOutput interface {
	GetState() (OutputState, error)
}

// OutputFactory creates physical outputs by relay output number.
type OutputFactory func(number int) Output

// ConnectionStatusProvider returns the physical output controller connection status.
type ConnectionStatusProvider func() model.TCPClientStatus

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

// Service manages interference channel state and screen strike lifecycle.
type Service struct {
	mu sync.RWMutex

	channels       map[string]*channelState
	order          []string
	outputFactory  OutputFactory
	statusProvider ConnectionStatusProvider
	store          *store.Store
	reports        ReportStore
	settings       UserSettingsStore

	activeReport   *model.InterferenceReport
	activeReportID string
}

// SetConnectionStatusProvider sets the physical output connection status source.
func (s *Service) SetConnectionStatusProvider(provider ConnectionStatusProvider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusProvider = provider
}

// ConnectionStatus returns the current physical output connection status.
func (s *Service) ConnectionStatus() model.TCPClientStatus {
	s.mu.RLock()
	provider := s.statusProvider
	s.mu.RUnlock()
	if provider == nil {
		return model.TCPClientStatus{}
	}
	return provider()
}

type channelState struct {
	def          ChannelDefinition
	output       Output
	initialized  bool
	enabled      bool
	actualLevel  string
	desiredLevel string
	status       string
	lastError    string
}

type screenStrikeSnapshot struct {
	state         model.ScreenStrikeState
	fullyObserved bool
}

// NewService creates an interference output control service.
func NewService(
	store *store.Store,
	definitions []ChannelDefinition,
	outputFactory OutputFactory,
) *Service {
	if outputFactory == nil {
		outputFactory = NewRelayOutputFactory(RelayOptions{})
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
		channels:      channels,
		order:         order,
		outputFactory: outputFactory,
		store:         store,
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
func (s *Service) ListChannels() []model.InterferenceChannel {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]model.InterferenceChannel, 0, len(s.order))
	for _, id := range s.order {
		result = append(result, s.dtoWithActual(s.channels[id]))
	}
	return result
}

// SetState sets one channel high or low.
func (s *Service) SetState(id string, enabled bool) (model.InterferenceChannel, error) {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.channels[id]
	if !ok {
		return model.InterferenceChannel{}, s.codedError("channel_not_found", "interference channel was not found")
	}
	if enabled && s.isScreenStrikeChannelIDLocked(id) {
		return s.dtoWithActual(state), s.codedError("strike_channel_requires_timed_operation", "screen strike channels must be started with timed strike control")
	}
	channel, err := s.setStateLocked(id, enabled)
	if err == nil && s.isScreenStrikeChannelIDLocked(id) {
		now := time.Now()
		strikeSnapshot := s.screenStrikeSnapshotLocked(now)
		s.finishCompletedReportIfInactiveLocked(strikeSnapshot, now)
		s.publishScreenStrikeLocked(strikeSnapshot.state)
	}
	return channel, err
}

// ScreenStrikeState returns current screen strike state.
func (s *Service) ScreenStrikeState() model.ScreenStrikeState {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	snapshot := s.screenStrikeSnapshotLocked(now)
	if s.finishCompletedReportIfInactiveLocked(snapshot, now) {
		s.publishScreenStrikeLocked(snapshot.state)
	}
	return snapshot.state
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
		endedAt := time.Now()
		initialSnapshot := s.screenStrikeSnapshotLocked(endedAt)
		activeChannelIDs := initialSnapshot.state.ChannelIDs
		if len(activeChannelIDs) == 0 && !initialSnapshot.fullyObserved && s.activeReport != nil {
			activeChannelIDs = append([]string{}, s.activeReport.ChannelIDs...)
		}
		err := s.stopScreenStrikeChannelIDsLocked(activeChannelIDs)
		snapshot := s.screenStrikeSnapshotLocked(endedAt)
		if snapshot.fullyObserved || err != nil {
			s.finishActiveReportLocked(model.InterferenceReportStatusCompleted, "", err, endedAt, snapshot.state)
		}
		s.publishScreenStrikeLocked(snapshot.state)
		return snapshot.state, err
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
	current := s.screenStrikeSnapshotLocked(startedAt)
	if current.state.Active {
		return current.state, s.codedError("strike_already_active", "interference is already active")
	}
	if !current.fullyObserved && s.activeReport != nil {
		return current.state, s.codedError("strike_state_unknown", "interference state is unknown")
	}
	s.finishCompletedReportIfInactiveLocked(current, startedAt)

	for _, id := range selected {
		if _, err := s.setTimedStateLocked(id, duration); err != nil {
			_ = s.stopScreenStrikeChannelIDsLocked(selected)
			state := s.screenStrikeStateLocked(time.Now())
			s.createFailedReportLocked(req, selected, startedAt, err, state)
			s.publishScreenStrikeLocked(state)
			return state, err
		}
	}

	now := time.Now()
	state := s.screenStrikeStateLocked(now)
	if state.Active {
		state.DurationSeconds = durationSeconds
		startedAtValue := startedAt
		state.StartedAt = &startedAtValue
	}
	s.createRunningReportLocked(req, selected, startedAt, state)
	s.publishScreenStrikeLocked(state)
	return state, nil
}

func (s *Service) setStateLocked(id string, enabled bool) (model.InterferenceChannel, error) {
	state, ok := s.channels[id]
	if !ok {
		return model.InterferenceChannel{}, s.codedError("channel_not_found", "interference channel was not found")
	}
	if state.def.Reserved {
		return s.dtoWithActual(state), s.codedError("channel_reserved", "interference channel is reserved")
	}

	if enabled {
		if state.output == nil {
			state.output = s.outputFactory(state.def.Output)
		}
		if !state.initialized {
			if err := state.output.Setup(); err != nil {
				return s.markError(state, err)
			}
			state.initialized = true
		}
		if err := state.output.SetHigh(); err != nil {
			return s.markError(state, err)
		}
		state.enabled = true
		state.actualLevel = "high"
		state.desiredLevel = "high"
		state.status = "active"
		state.lastError = ""
	} else {
		if state.output == nil {
			state.output = s.outputFactory(state.def.Output)
		}
		if state.output != nil {
			if err := state.output.SetLow(); err != nil {
				return s.markError(state, err)
			}
			state.output.Cleanup()
			state.output = nil
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
		s.store.Publish(model.Event{Type: "interference.channel.updated", Time: time.Now(), Payload: channel})
	}
	return channel, nil
}

func (s *Service) setTimedStateLocked(id string, duration time.Duration) (model.InterferenceChannel, error) {
	state, ok := s.channels[id]
	if !ok {
		return model.InterferenceChannel{}, s.codedError("channel_not_found", "interference channel was not found")
	}
	if state.def.Reserved {
		return s.dtoWithActual(state), s.codedError("channel_reserved", "interference channel is reserved")
	}
	if state.output == nil {
		state.output = s.outputFactory(state.def.Output)
	}
	if !state.initialized {
		if err := state.output.Setup(); err != nil {
			return s.markError(state, err)
		}
		state.initialized = true
	}
	timed, ok := state.output.(TimedOutput)
	if !ok {
		err := s.codedError("strike_timed_output_required", "screen strike output must support relay-side timed control")
		return s.markError(state, err)
	}
	if err := timed.SetHighFor(duration); err != nil {
		return s.markError(state, err)
	}
	state.enabled = true
	state.actualLevel = "high"
	state.desiredLevel = "high"
	state.status = "active"
	state.lastError = ""

	channel := s.dtoWithActual(state)
	if s.store != nil {
		s.store.Publish(model.Event{Type: "interference.channel.updated", Time: time.Now(), Payload: channel})
	}
	return channel, nil
}

// Shutdown releases local output handles and closes active report as abnormal.
func (s *Service) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, state := range s.channels {
		if state.output == nil {
			continue
		}
		state.output = nil
		state.initialized = false
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
		return nil, s.codedError("strike_invalid_channels", "interference channels can only use configured relay outputs")
	}
	return selected, nil
}

func (s *Service) screenStrikeChannelIDsLocked() []string {
	ids := make([]string, 0, len(s.order))
	for _, id := range s.order {
		state := s.channels[id]
		if state == nil || state.def.Reserved {
			continue
		}
		ids = append(ids, id)
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

func (s *Service) stopScreenStrikeChannelIDsLocked(ids []string) error {
	var firstErr error
	for _, id := range ids {
		if _, err := s.setStateLocked(id, false); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Service) screenStrikeStateLocked(now time.Time) model.ScreenStrikeState {
	return s.screenStrikeSnapshotLocked(now).state
}

func (s *Service) screenStrikeSnapshotLocked(now time.Time) screenStrikeSnapshot {
	if now.IsZero() {
		now = time.Now()
	}

	channels := make([]model.InterferenceChannel, 0, len(s.order))
	activeChannelIDs := make([]string, 0, len(s.order))
	var maxRemaining time.Duration
	fullyObserved := true
	for _, id := range s.screenStrikeChannelIDsLocked() {
		channel, outputState, ok := s.dtoWithActualState(s.channels[id])
		if !ok {
			fullyObserved = false
		}
		channels = append(channels, channel)
		if channel.Enabled {
			activeChannelIDs = append(activeChannelIDs, channel.ID)
			if ok && outputState.Remaining > maxRemaining {
				maxRemaining = outputState.Remaining
			}
		}
	}

	active := len(activeChannelIDs) > 0
	state := model.ScreenStrikeState{
		Active:           active,
		ChannelIDs:       append([]string{}, activeChannelIDs...),
		DurationSeconds:  0,
		RemainingSeconds: ceilSeconds(maxRemaining),
		Channels:         channels,
	}
	if active && s.activeReport != nil {
		state.DurationSeconds = s.activeReport.RequestedDurationSeconds
		startedAt := s.activeReport.StartedAt
		state.StartedAt = &startedAt
	}
	return screenStrikeSnapshot{
		state:         state,
		fullyObserved: fullyObserved,
	}
}

func (s *Service) finishCompletedReportIfInactiveLocked(snapshot screenStrikeSnapshot, endedAt time.Time) bool {
	if s.activeReport == nil || s.activeReportID == "" || snapshot.state.Active || !snapshot.fullyObserved {
		return false
	}
	s.finishActiveReportLocked(model.InterferenceReportStatusCompleted, "", nil, endedAt, snapshot.state)
	return true
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
	labels, outputs := s.reportChannelMetadataLocked(selected)
	report := model.InterferenceReport{
		InterferenceReportSummary: model.InterferenceReportSummary{
			Status:                   model.InterferenceReportStatusRunning,
			StartedAt:                startedAt,
			RequestedDurationSeconds: req.DurationSeconds,
			ChannelIDs:               append([]string{}, selected...),
			ChannelLabels:            labels,
			ChannelOutputs:           outputs,
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
	labels, outputs := s.reportChannelMetadataLocked(selected)
	report := model.InterferenceReport{
		InterferenceReportSummary: model.InterferenceReportSummary{
			Status:                   model.InterferenceReportStatusFailed,
			StartedAt:                startedAt,
			EndedAt:                  &endedAt,
			RequestedDurationSeconds: req.DurationSeconds,
			ChannelIDs:               append([]string{}, selected...),
			ChannelLabels:            labels,
			ChannelOutputs:           outputs,
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
	outputs := make([]int, 0, len(ids))
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
		outputs = append(outputs, state.def.Output)
	}
	return labels, outputs
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
	indexes := make(map[string]int, len(s.order))
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
	cloned.Channels = cloneInterferenceChannels(state.Channels)
	if state.StartedAt != nil {
		startedAt := *state.StartedAt
		cloned.StartedAt = &startedAt
	}
	return &cloned
}

func cloneInterferenceChannels(channels []model.InterferenceChannel) []model.InterferenceChannel {
	if len(channels) == 0 {
		return []model.InterferenceChannel{}
	}
	cloned := make([]model.InterferenceChannel, len(channels))
	for index, channel := range channels {
		cloned[index] = channel
		cloned[index].Bands = append([]string{}, channel.Bands...)
	}
	return cloned
}

func cloneInterferenceReport(report model.InterferenceReport) model.InterferenceReport {
	report.ChannelIDs = append([]string{}, report.ChannelIDs...)
	report.ChannelLabels = append([]string{}, report.ChannelLabels...)
	report.ChannelOutputs = append([]int{}, report.ChannelOutputs...)
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

func (s *Service) markError(state *channelState, err error) (model.InterferenceChannel, error) {
	state.status = "error"
	state.lastError = err.Error()
	channel := state.dto()
	if s.store != nil {
		s.store.Publish(model.Event{Type: "interference.channel.updated", Time: time.Now(), Payload: channel})
	}
	return channel, err
}

func (s *channelState) dto() model.InterferenceChannel {
	return model.InterferenceChannel{
		ID:           s.def.ID,
		Label:        s.def.Label,
		Output:       s.def.Output,
		Bands:        append([]string{}, s.def.Bands...),
		Reserved:     s.def.Reserved,
		Enabled:      s.enabled,
		ActualLevel:  s.actualLevel,
		DesiredLevel: s.desiredLevel,
		Status:       s.status,
		LastError:    s.lastError,
	}
}

func (s *Service) dtoWithActual(state *channelState) model.InterferenceChannel {
	channel, _, _ := s.dtoWithActualState(state)
	return channel
}

func (s *Service) dtoWithActualState(state *channelState) (model.InterferenceChannel, OutputState, bool) {
	if state == nil {
		return model.InterferenceChannel{}, OutputState{}, false
	}
	channel := state.dto()
	outputState, err := s.readOutputStateLocked(state)
	if err != nil {
		channel.Enabled = false
		channel.ActualLevel = "unknown"
		channel.Status = "error"
		channel.LastError = err.Error()
		state.enabled = false
		state.actualLevel = channel.ActualLevel
		state.status = channel.Status
		state.lastError = channel.LastError
		return channel, OutputState{}, false
	}
	channel = applyActualLevel(channel, outputState.Value)
	state.enabled = channel.Enabled
	state.actualLevel = channel.ActualLevel
	state.status = channel.Status
	state.lastError = ""
	return channel, outputState, true
}

func (s *Service) readOutputStateLocked(state *channelState) (OutputState, error) {
	if state == nil {
		return OutputState{}, fmt.Errorf("interference channel state is nil")
	}
	output := state.output
	if output == nil {
		output = s.outputFactory(state.def.Output)
	}
	if output == nil {
		return OutputState{}, fmt.Errorf("interference output is not configured")
	}
	if stateOutput, ok := output.(StateOutput); ok {
		return stateOutput.GetState()
	}
	value, err := output.GetValue()
	if err != nil {
		return OutputState{}, err
	}
	return OutputState{Value: value}, nil
}

func applyActualLevel(channel model.InterferenceChannel, value int) model.InterferenceChannel {
	switch value {
	case 0:
		channel.Enabled = false
		channel.ActualLevel = "low"
		channel.Status = "idle"
	case 1:
		channel.ActualLevel = "high"
		channel.Enabled = true
		channel.Status = "active"
	default:
		channel.ActualLevel = strconv.Itoa(value)
		channel.Enabled = value != 0
		if channel.Enabled {
			channel.Status = "active"
		} else {
			channel.Status = "idle"
		}
	}
	return channel
}
