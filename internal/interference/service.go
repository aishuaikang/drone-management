package interference

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"drone-management/internal/model"
	"drone-management/internal/store"
)

const (
	screenStrikeEventType          = "screen.strike.updated"
	screenStrikeMinDurationSeconds = 10
	screenStrikeMaxDurationSeconds = 180
	unattendedPhaseDisabled        = "disabled"
	unattendedPhaseWatching        = "watching"
	unattendedPhaseStriking        = "striking"
	unattendedPhaseResting         = "resting"
	unattendedTargetCheckInterval  = 5 * time.Second
	unattendedRestInterval         = time.Minute
	unattendedStatePollInterval    = time.Second
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
	SaveEditableUser(model.UserSettings) (model.UserSettings, error)
}

type unattendedTimings struct {
	CheckInterval time.Duration
	RestInterval  time.Duration
	PollInterval  time.Duration
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

	unattended        model.ScreenStrikeUnattendedState
	unattendedTimings unattendedTimings
	unattendedCancel  context.CancelFunc
	unattendedDone    chan struct{}
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
		unattended: model.ScreenStrikeUnattendedState{
			Phase: unattendedPhaseDisabled,
		},
		unattendedTimings: unattendedTimings{
			CheckInterval: unattendedTargetCheckInterval,
			RestInterval:  unattendedRestInterval,
			PollInterval:  unattendedStatePollInterval,
		},
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

func (s *Service) SetUnattendedTimings(checkInterval, restInterval, pollInterval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if checkInterval > 0 {
		s.unattendedTimings.CheckInterval = checkInterval
	}
	if restInterval > 0 {
		s.unattendedTimings.RestInterval = restInterval
	}
	if pollInterval > 0 {
		s.unattendedTimings.PollInterval = pollInterval
	}
}

// RestoreUnattended starts unattended strike from persisted settings without writing settings again.
func (s *Service) RestoreUnattended(config model.ScreenStrikeUnattendedConfig) (model.ScreenStrikeState, error) {
	return s.setUnattended(config, false)
}

// SetUnattended enables or disables the unattended strike loop.
func (s *Service) SetUnattended(config model.ScreenStrikeUnattendedConfig) (model.ScreenStrikeState, error) {
	return s.setUnattended(config, true)
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
	return s.applyScreenStrikeWithType(req.Enabled, req.ChannelIDs, duration, durationSeconds, model.InterferenceOperationManual)
}

func (s *Service) setUnattended(config model.ScreenStrikeUnattendedConfig, persist bool) (model.ScreenStrikeState, error) {
	if !config.Enabled {
		return s.disableUnattended(persist)
	}

	duration := time.Duration(config.DurationSeconds) * time.Second
	s.mu.Lock()
	selected, err := s.validateScreenStrikeChannelsLocked(config.ChannelIDs)
	if err != nil {
		state := s.screenStrikeStateLocked(time.Now())
		s.mu.Unlock()
		return state, err
	}
	if duration <= 0 || config.DurationSeconds < screenStrikeMinDurationSeconds || config.DurationSeconds > screenStrikeMaxDurationSeconds {
		state := s.screenStrikeStateLocked(time.Now())
		s.mu.Unlock()
		return state, s.codedError("strike_invalid_duration", "interference duration must be between 10 and 180 seconds")
	}
	current := s.screenStrikeSnapshotLocked(time.Now())
	if current.state.Active {
		state := current.state
		s.mu.Unlock()
		return state, s.codedError("strike_already_active", "interference is already active")
	}
	s.stopUnattendedLocked()
	now := time.Now()
	s.unattended = model.ScreenStrikeUnattendedState{
		Enabled:         true,
		ChannelIDs:      append([]string{}, selected...),
		DurationSeconds: config.DurationSeconds,
		Phase:           unattendedPhaseWatching,
		NextCheckAt:     cloneTime(&now),
	}
	s.startUnattendedLocked()
	state := s.screenStrikeStateLocked(now)
	s.publishScreenStrikeLocked(state)
	s.mu.Unlock()

	if persist {
		if err := s.persistUnattendedConfig(model.ScreenStrikeUnattendedConfig{
			Enabled:         true,
			ChannelIDs:      selected,
			DurationSeconds: config.DurationSeconds,
		}); err != nil {
			_, _ = s.disableUnattended(false)
			return state, err
		}
	}
	return s.ScreenStrikeState(), nil
}

func (s *Service) disableUnattended(persist bool) (model.ScreenStrikeState, error) {
	s.mu.Lock()
	wasEnabled := s.unattended.Enabled
	s.stopUnattendedLocked()
	s.unattended = model.ScreenStrikeUnattendedState{Phase: unattendedPhaseDisabled}
	state := s.screenStrikeStateLocked(time.Now())
	s.publishScreenStrikeLocked(state)
	s.mu.Unlock()

	var err error
	if wasEnabled && state.Active {
		_, err = s.applyScreenStrikeWithType(false, nil, 0, 0, model.InterferenceOperationUnattended)
	}
	if persist {
		if persistErr := s.persistUnattendedConfig(model.ScreenStrikeUnattendedConfig{}); persistErr != nil && err == nil {
			err = persistErr
		}
	}
	if err != nil {
		return s.ScreenStrikeState(), err
	}
	return s.ScreenStrikeState(), nil
}

func (s *Service) startUnattendedLocked() {
	if !s.unattended.Enabled {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	config := model.ScreenStrikeUnattendedConfig{
		Enabled:         true,
		ChannelIDs:      append([]string{}, s.unattended.ChannelIDs...),
		DurationSeconds: s.unattended.DurationSeconds,
	}
	timings := s.unattendedTimings
	s.unattendedCancel = cancel
	s.unattendedDone = done
	go s.runUnattended(ctx, done, config, timings)
}

func (s *Service) stopUnattendedLocked() {
	cancel := s.unattendedCancel
	done := s.unattendedDone
	s.unattendedCancel = nil
	s.unattendedDone = nil
	if cancel == nil {
		return
	}
	cancel()
	s.mu.Unlock()
	<-done
	s.mu.Lock()
}

func (s *Service) runUnattended(
	ctx context.Context,
	done chan<- struct{},
	config model.ScreenStrikeUnattendedConfig,
	timings unattendedTimings,
) {
	defer close(done)
	if timings.CheckInterval <= 0 {
		timings.CheckInterval = unattendedTargetCheckInterval
	}
	if timings.RestInterval <= 0 {
		timings.RestInterval = unattendedRestInterval
	}
	if timings.PollInterval <= 0 {
		timings.PollInterval = unattendedStatePollInterval
	}

	for {
		if !sleepUntilDone(ctx, s.unattendedNextCheck()) {
			return
		}
		if ctx.Err() != nil {
			return
		}

		checkedAt := time.Now()
		targetPresent := s.hasLiveTarget()
		if !targetPresent {
			next := checkedAt.Add(timings.CheckInterval)
			s.updateUnattendedStatus(unattendedPhaseWatching, targetPresent, checkedAt, &next, "")
			continue
		}

		s.updateUnattendedStatus(unattendedPhaseStriking, true, checkedAt, nil, "")
		_, err := s.applyScreenStrikeWithType(
			true,
			config.ChannelIDs,
			time.Duration(config.DurationSeconds)*time.Second,
			config.DurationSeconds,
			model.InterferenceOperationUnattended,
		)
		if err != nil {
			next := time.Now().Add(timings.CheckInterval)
			s.updateUnattendedStatus(unattendedPhaseWatching, true, checkedAt, &next, err.Error())
			continue
		}

		for {
			if !sleepOrDone(ctx, timings.PollInterval) {
				return
			}
			state := s.ScreenStrikeState()
			if !state.Active {
				break
			}
		}

		next := time.Now().Add(timings.RestInterval)
		s.updateUnattendedStatus(unattendedPhaseResting, true, checkedAt, &next, "")
	}
}

func (s *Service) unattendedNextCheck() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unattended.NextCheckAt == nil {
		now := time.Now()
		s.unattended.NextCheckAt = &now
		return now
	}
	return *s.unattended.NextCheckAt
}

func (s *Service) hasLiveTarget() bool {
	if s.store == nil {
		return false
	}
	if len(s.store.FPV(1)) > 0 {
		return true
	}
	settings := s.userSettings()
	for _, target := range s.store.Positions(0) {
		if !isWhitelistedPositionTarget(target, settings.Whitelist) {
			return true
		}
	}
	return false
}

func (s *Service) userSettings() model.UserSettings {
	if s.settings == nil {
		return model.UserSettingsWithDefaults(model.UserSettings{})
	}
	settings, ok, err := s.settings.LoadUser()
	if err != nil || !ok {
		return model.UserSettingsWithDefaults(model.UserSettings{})
	}
	return model.UserSettingsWithDefaults(settings)
}

func isWhitelistedPositionTarget(target model.ScreenPositionTarget, whitelist []model.WhitelistItem) bool {
	serial := normalizeTargetIdentity(target.Serial)
	if serial == "" {
		return false
	}
	for _, item := range whitelist {
		if normalizeTargetIdentity(item.Serial) == serial {
			return true
		}
	}
	return false
}

func normalizeTargetIdentity(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func (s *Service) updateUnattendedStatus(
	phase string,
	targetPresent bool,
	lastCheckedAt time.Time,
	nextCheckAt *time.Time,
	lastError string,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.unattended.Enabled {
		return
	}
	s.unattended.Phase = phase
	s.unattended.TargetPresent = targetPresent
	s.unattended.LastCheckedAt = cloneTime(&lastCheckedAt)
	s.unattended.NextCheckAt = cloneTime(nextCheckAt)
	s.unattended.LastError = lastError
	s.publishScreenStrikeLocked(s.screenStrikeStateLocked(time.Now()))
}

func (s *Service) persistUnattendedConfig(config model.ScreenStrikeUnattendedConfig) error {
	if s.settings == nil {
		return nil
	}
	settings, ok, err := s.settings.LoadUser()
	if err != nil {
		return fmt.Errorf("load user settings: %w", err)
	}
	if !ok {
		settings = model.UserSettings{}
	}
	settings = model.UserSettingsWithDefaults(settings)
	if config.Enabled {
		settings.ScreenStrikeUnattended = &model.ScreenStrikeUnattendedConfig{
			Enabled:         true,
			ChannelIDs:      append([]string{}, config.ChannelIDs...),
			DurationSeconds: config.DurationSeconds,
		}
	} else {
		settings.ScreenStrikeUnattended = &model.ScreenStrikeUnattendedConfig{}
	}
	if _, err := s.settings.SaveEditableUser(settings); err != nil {
		return fmt.Errorf("save user settings: %w", err)
	}
	return nil
}

func (s *Service) applyScreenStrike(
	enabled bool,
	channelIDs []string,
	duration time.Duration,
	durationSeconds int,
) (model.ScreenStrikeState, error) {
	return s.applyScreenStrikeWithType(enabled, channelIDs, duration, durationSeconds, model.InterferenceOperationManual)
}

func (s *Service) applyScreenStrikeWithType(
	enabled bool,
	channelIDs []string,
	duration time.Duration,
	durationSeconds int,
	operationType model.InterferenceOperationType,
) (model.ScreenStrikeState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if enabled && operationType == model.InterferenceOperationManual && s.unattended.Enabled {
		return s.screenStrikeStateLocked(time.Now()), s.codedError("strike_unattended_active", "unattended strike is active")
	}

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
			s.createFailedReportLocked(req, selected, startedAt, err, state, operationType)
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
	s.createRunningReportLocked(req, selected, startedAt, state, operationType)
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

	s.stopUnattendedLocked()
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
		Unattended:       cloneUnattendedState(s.unattended),
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
	operationType model.InterferenceOperationType,
) {
	if s.reports == nil {
		return
	}
	labels, outputs := s.reportChannelMetadataLocked(selected)
	report := model.InterferenceReport{
		InterferenceReportSummary: model.InterferenceReportSummary{
			Status:                   model.InterferenceReportStatusRunning,
			OperationType:            normalizeOperationType(operationType),
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
	operationType model.InterferenceOperationType,
) {
	if s.reports == nil || cause == nil {
		return
	}
	endedAt := time.Now()
	labels, outputs := s.reportChannelMetadataLocked(selected)
	report := model.InterferenceReport{
		InterferenceReportSummary: model.InterferenceReportSummary{
			Status:                   model.InterferenceReportStatusFailed,
			OperationType:            normalizeOperationType(operationType),
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

func normalizeOperationType(operationType model.InterferenceOperationType) model.InterferenceOperationType {
	if operationType == model.InterferenceOperationUnattended {
		return model.InterferenceOperationUnattended
	}
	return model.InterferenceOperationManual
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

func cloneUnattendedState(state model.ScreenStrikeUnattendedState) model.ScreenStrikeUnattendedState {
	state.ChannelIDs = append([]string{}, state.ChannelIDs...)
	state.LastCheckedAt = cloneTime(state.LastCheckedAt)
	state.NextCheckAt = cloneTime(state.NextCheckAt)
	if state.Phase == "" {
		state.Phase = unattendedPhaseDisabled
	}
	return state
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil || value.IsZero() {
		return nil
	}
	cloned := *value
	return &cloned
}

func sleepOrDone(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func sleepUntilDone(ctx context.Context, at time.Time) bool {
	return sleepOrDone(ctx, time.Until(at))
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
