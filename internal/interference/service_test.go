package interference

import (
	"errors"
	"sync"
	"testing"
	"time"

	"dr600ab-net/internal/model"
	"dr600ab-net/internal/store"
)

type fakeOutput struct {
	mu             sync.Mutex
	value          int
	remaining      time.Duration
	setupErr       error
	highErr        error
	lowErr         error
	stateErr       error
	cleaned        bool
	timedDurations []time.Duration
}

func (o *fakeOutput) Setup() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.setupErr != nil {
		return o.setupErr
	}
	return nil
}

func (o *fakeOutput) SetHigh() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.highErr != nil {
		return o.highErr
	}
	o.value = 1
	o.remaining = 0
	return nil
}

func (o *fakeOutput) SetHighFor(duration time.Duration) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.highErr != nil {
		return o.highErr
	}
	o.value = 1
	o.remaining = duration
	o.timedDurations = append(o.timedDurations, duration)
	return nil
}

func (o *fakeOutput) SetLow() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.lowErr != nil {
		return o.lowErr
	}
	o.value = 0
	o.remaining = 0
	return nil
}

func (o *fakeOutput) GetValue() (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.value, nil
}

func (o *fakeOutput) GetState() (OutputState, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stateErr != nil {
		return OutputState{}, o.stateErr
	}
	return OutputState{
		Value:     o.value,
		Remaining: o.remaining,
	}, nil
}

func (o *fakeOutput) Cleanup() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cleaned = true
}

func (o *fakeOutput) snapshot() fakeOutputSnapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	return fakeOutputSnapshot{
		value:          o.value,
		remaining:      o.remaining,
		cleaned:        o.cleaned,
		timedDurations: append([]time.Duration{}, o.timedDurations...),
	}
}

func (o *fakeOutput) setState(value int, remaining time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.value = value
	o.remaining = remaining
}

func (o *fakeOutput) setStateErr(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stateErr = err
}

type fakeOutputSnapshot struct {
	value          int
	remaining      time.Duration
	cleaned        bool
	timedDurations []time.Duration
}

type memoryReportStore struct {
	mu      sync.Mutex
	created []model.InterferenceReport
	updated []model.InterferenceReport
}

func (s *memoryReportStore) Create(report model.InterferenceReport) (model.InterferenceReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if report.ID == "" {
		report.ID = "report-created"
	}
	s.created = append(s.created, report)
	return report, nil
}

func (s *memoryReportStore) CreateRunning(report model.InterferenceReport) (model.InterferenceReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	report.Status = model.InterferenceReportStatusRunning
	if report.ID == "" {
		report.ID = "report-running"
	}
	s.created = append(s.created, report)
	return report, nil
}

func (s *memoryReportStore) Update(report model.InterferenceReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updated = append(s.updated, report)
	return nil
}

func (s *memoryReportStore) createdReports() []model.InterferenceReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]model.InterferenceReport{}, s.created...)
}

func (s *memoryReportStore) updatedReports() []model.InterferenceReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]model.InterferenceReport{}, s.updated...)
}

type memorySettingsStore struct {
	settings model.UserSettings
	ok       bool
}

func (s memorySettingsStore) LoadUser() (model.UserSettings, bool, error) {
	return s.settings, s.ok, nil
}

func (s memorySettingsStore) SaveEditableUser(settings model.UserSettings) (model.UserSettings, error) {
	return model.UserSettingsWithDefaults(settings), nil
}

func TestScreenStrikeStartStopAndReport(t *testing.T) {
	outputs, factory := newFakeOutputFactory()
	service := NewService(store.New(10, 10), DefaultChannels(), factory)
	reports := &memoryReportStore{}
	service.SetReportStore(reports)
	service.SetUserSettingsStore(memorySettingsStore{
		ok: true,
		settings: model.UserSettings{
			ScreenStrikeChannelLabels: []string{"A", "B", "C", "D", "E", "F", "G", "H"},
		},
	})

	state, err := service.SetScreenStrike(model.ScreenStrikeRequest{
		Enabled:         true,
		ChannelIDs:      []string{"io1", "io3"},
		DurationSeconds: 10,
	})
	if err != nil {
		t.Fatalf("SetScreenStrike(start) error = %v", err)
	}
	if !state.Active || len(state.ChannelIDs) != 2 || state.RemainingSeconds <= 0 {
		t.Fatalf("active state = %#v", state)
	}
	output1 := outputs[1].snapshot()
	output3 := outputs[3].snapshot()
	if output1.value != 1 || output3.value != 1 {
		t.Fatalf("outputs after start = %#v %#v", output1, output3)
	}
	if len(output1.timedDurations) != 1 || output1.timedDurations[0] != 10*time.Second {
		t.Fatalf("timed durations for output 1 = %#v", output1.timedDurations)
	}
	if len(output3.timedDurations) != 1 || output3.timedDurations[0] != 10*time.Second {
		t.Fatalf("timed durations for output 3 = %#v", output3.timedDurations)
	}
	createdReports := reports.createdReports()
	if len(createdReports) != 1 || createdReports[0].Status != model.InterferenceReportStatusRunning {
		t.Fatalf("created report = %#v", createdReports)
	}
	if got := createdReports[0].ChannelLabels; len(got) != 2 || got[0] != "A" || got[1] != "C" {
		t.Fatalf("channel labels = %#v", got)
	}

	state, err = service.SetScreenStrike(model.ScreenStrikeRequest{Enabled: false})
	if err != nil {
		t.Fatalf("SetScreenStrike(stop) error = %v", err)
	}
	if state.Active {
		t.Fatalf("state should be inactive after stop: %#v", state)
	}
	output1 = outputs[1].snapshot()
	output3 = outputs[3].snapshot()
	if !output1.cleaned || output1.value != 0 || !output3.cleaned || output3.value != 0 {
		t.Fatalf("outputs after stop = %#v %#v", output1, output3)
	}
	updatedReports := reports.updatedReports()
	if len(updatedReports) != 1 || updatedReports[0].Status != model.InterferenceReportStatusCompleted {
		t.Fatalf("updated reports = %#v", updatedReports)
	}
}

func TestScreenStrikeValidation(t *testing.T) {
	_, factory := newFakeOutputFactory()
	service := NewService(store.New(10, 10), DefaultChannels(), factory)

	_, err := service.SetScreenStrike(model.ScreenStrikeRequest{
		Enabled:         true,
		ChannelIDs:      []string{"io1"},
		DurationSeconds: 9,
	})
	if ErrorCode(err) != "strike_invalid_duration" {
		t.Fatalf("invalid duration error = %v, code = %q", err, ErrorCode(err))
	}

	state, err := service.SetScreenStrike(model.ScreenStrikeRequest{
		Enabled:         true,
		ChannelIDs:      []string{"io1"},
		DurationSeconds: 180,
	})
	if err != nil {
		t.Fatalf("max duration SetScreenStrike() error = %v", err)
	}
	if !state.Active || state.DurationSeconds != 180 {
		t.Fatalf("max duration state = %#v", state)
	}
	_, err = service.SetScreenStrike(model.ScreenStrikeRequest{Enabled: false})
	if err != nil {
		t.Fatalf("stop after max duration error = %v", err)
	}

	_, err = service.SetScreenStrike(model.ScreenStrikeRequest{
		Enabled:         true,
		ChannelIDs:      []string{"io1"},
		DurationSeconds: 181,
	})
	if ErrorCode(err) != "strike_invalid_duration" {
		t.Fatalf("over max duration error = %v, code = %q", err, ErrorCode(err))
	}

	_, err = service.SetScreenStrike(model.ScreenStrikeRequest{
		Enabled:         true,
		ChannelIDs:      []string{"io9"},
		DurationSeconds: 10,
	})
	if ErrorCode(err) != "strike_invalid_channels" {
		t.Fatalf("invalid channel error = %v, code = %q", err, ErrorCode(err))
	}
}

func TestSetStateRejectsTimedChannels(t *testing.T) {
	_, factory := newFakeOutputFactory()
	service := NewService(store.New(10, 10), DefaultChannels(), factory)

	_, err := service.SetState("io1", true)
	if ErrorCode(err) != "strike_channel_requires_timed_operation" {
		t.Fatalf("timed channel error = %v, code = %q", err, ErrorCode(err))
	}

	_, err = service.SetState("io8", true)
	if ErrorCode(err) != "strike_channel_requires_timed_operation" {
		t.Fatalf("eighth timed channel error = %v, code = %q", err, ErrorCode(err))
	}
}

func TestScreenStrikeCreatesFailedReportOnPinError(t *testing.T) {
	cause := errors.New("relay failed")
	service := NewService(store.New(10, 10), DefaultChannels(), func(_ int) Output {
		return &fakeOutput{highErr: cause}
	})
	reports := &memoryReportStore{}
	service.SetReportStore(reports)

	_, err := service.SetScreenStrike(model.ScreenStrikeRequest{
		Enabled:         true,
		ChannelIDs:      []string{"io1"},
		DurationSeconds: 10,
	})
	if !errors.Is(err, cause) {
		t.Fatalf("SetScreenStrike() error = %v, want %v", err, cause)
	}
	createdReports := reports.createdReports()
	if len(createdReports) != 1 || createdReports[0].Status != model.InterferenceReportStatusFailed {
		t.Fatalf("created reports = %#v", createdReports)
	}
	if createdReports[0].LastError != cause.Error() {
		t.Fatalf("last error = %q", createdReports[0].LastError)
	}
}

func TestScreenStrikeUsesRelayStateInsteadOfLocalTimeout(t *testing.T) {
	outputs, factory := newFakeOutputFactory()
	service := NewService(store.New(10, 10), DefaultChannels(), factory)
	reports := &memoryReportStore{}
	service.SetReportStore(reports)

	state, err := service.applyScreenStrike(true, []string{"io1"}, 20*time.Millisecond, 10)
	if err != nil {
		t.Fatalf("applyScreenStrike() error = %v", err)
	}
	if !state.Active || outputs[1].snapshot().value != 1 {
		t.Fatalf("started state = %#v output = %#v", state, outputs[1].snapshot())
	}

	time.Sleep(50 * time.Millisecond)
	state = service.ScreenStrikeState()
	if !state.Active || outputs[1].snapshot().value != 1 {
		t.Fatalf("service should keep reporting relay state until relay opens: state=%#v output=%#v", state, outputs[1].snapshot())
	}
	if updatedReports := reports.updatedReports(); len(updatedReports) != 0 {
		t.Fatalf("reports should not be completed by a local timer: %#v", updatedReports)
	}

	outputs[1].setState(0, 0)
	state = service.ScreenStrikeState()
	if state.Active {
		t.Fatalf("state should follow relay auto-off: %#v", state)
	}
	if updatedReports := reports.updatedReports(); len(updatedReports) != 1 || updatedReports[0].Status != model.InterferenceReportStatusCompleted {
		t.Fatalf("updated reports = %#v", updatedReports)
	}
}

func TestScreenStrikeReadFailureDoesNotUseStaleActiveState(t *testing.T) {
	outputs, factory := newFakeOutputFactory()
	service := NewService(store.New(10, 10), DefaultChannels(), factory)
	reports := &memoryReportStore{}
	service.SetReportStore(reports)

	state, err := service.SetScreenStrike(model.ScreenStrikeRequest{
		Enabled:         true,
		ChannelIDs:      []string{"io1"},
		DurationSeconds: 10,
	})
	if err != nil {
		t.Fatalf("SetScreenStrike(start) error = %v", err)
	}
	if !state.Active {
		t.Fatalf("started state should be active: %#v", state)
	}

	cause := errors.New("relay read failed")
	outputs[1].setStateErr(cause)
	state = service.ScreenStrikeState()
	if state.Active || len(state.ChannelIDs) != 0 {
		t.Fatalf("read failure should not reuse stale active state: %#v", state)
	}
	if len(state.Channels) == 0 || state.Channels[0].Status != "error" || state.Channels[0].LastError != cause.Error() {
		t.Fatalf("channel should expose relay read error: %#v", state.Channels)
	}
	if updatedReports := reports.updatedReports(); len(updatedReports) != 0 {
		t.Fatalf("report should not complete while relay state is unknown: %#v", updatedReports)
	}

	outputs[1].setStateErr(nil)
	outputs[1].setState(0, 0)
	state = service.ScreenStrikeState()
	if state.Active {
		t.Fatalf("state should become inactive after confirmed relay read: %#v", state)
	}
	if updatedReports := reports.updatedReports(); len(updatedReports) != 1 || updatedReports[0].Status != model.InterferenceReportStatusCompleted {
		t.Fatalf("report should complete after confirmed inactive state: %#v", updatedReports)
	}
}

func TestUnattendedWaitsForTarget(t *testing.T) {
	outputs, factory := newFakeOutputFactory()
	service := NewService(store.New(10, 10), DefaultChannels(), factory)
	service.SetUnattendedTimings(10*time.Millisecond, 20*time.Millisecond, 5*time.Millisecond)
	defer service.Shutdown()

	state, err := service.SetUnattended(model.ScreenStrikeUnattendedConfig{
		Enabled:         true,
		ChannelIDs:      []string{"io1"},
		DurationSeconds: 10,
	})
	if err != nil {
		t.Fatalf("SetUnattended() error = %v", err)
	}
	if !state.Unattended.Enabled || state.Unattended.Phase != unattendedPhaseWatching {
		t.Fatalf("unattended state = %#v", state.Unattended)
	}
	time.Sleep(30 * time.Millisecond)
	if outputs[1] != nil && outputs[1].snapshot().value != 0 {
		t.Fatalf("output should not start without targets: %#v", outputs[1].snapshot())
	}
}

func TestUnattendedStartsWhenTargetPresent(t *testing.T) {
	stateStore := store.New(10, 10)
	_, _ = stateStore.AddFPV(model.ScreenFPVTarget{
		Frequency:  5800,
		SignalType: "fpv",
		LastSeen:   time.Now(),
	})
	outputs, factory := newFakeOutputFactory()
	service := NewService(stateStore, DefaultChannels(), factory)
	service.SetUnattendedTimings(10*time.Millisecond, 20*time.Millisecond, 5*time.Millisecond)
	reports := &memoryReportStore{}
	service.SetReportStore(reports)
	defer service.Shutdown()

	_, err := service.SetUnattended(model.ScreenStrikeUnattendedConfig{
		Enabled:         true,
		ChannelIDs:      []string{"io1"},
		DurationSeconds: 10,
	})
	if err != nil {
		t.Fatalf("SetUnattended() error = %v", err)
	}
	waitUntil(t, 100*time.Millisecond, func() bool {
		return outputs[1] != nil && outputs[1].snapshot().value == 1
	})
	createdReports := reports.createdReports()
	if len(createdReports) != 1 || createdReports[0].OperationType != model.InterferenceOperationUnattended {
		t.Fatalf("created reports = %#v", createdReports)
	}
}

func TestUnattendedIgnoresWhitelistedPositionTargets(t *testing.T) {
	stateStore := store.New(10, 10)
	_, _ = stateStore.AddPosition(model.ScreenPositionTarget{
		Serial:   "SN-WHITE",
		Model:    "Mini 4 Pro",
		Source:   "ddsT1",
		LastSeen: time.Now(),
	})
	outputs, factory := newFakeOutputFactory()
	service := NewService(stateStore, DefaultChannels(), factory)
	service.SetUnattendedTimings(10*time.Millisecond, 20*time.Millisecond, 5*time.Millisecond)
	service.SetUserSettingsStore(memorySettingsStore{
		ok: true,
		settings: model.UserSettings{
			Whitelist: []model.WhitelistItem{
				{Serial: "sn-white"},
			},
		},
	})
	defer service.Shutdown()

	_, err := service.SetUnattended(model.ScreenStrikeUnattendedConfig{
		Enabled:         true,
		ChannelIDs:      []string{"io1"},
		DurationSeconds: 10,
	})
	if err != nil {
		t.Fatalf("SetUnattended() error = %v", err)
	}
	waitUntil(t, 100*time.Millisecond, func() bool {
		return service.ScreenStrikeState().Unattended.LastCheckedAt != nil
	})
	if outputs[1] != nil && outputs[1].snapshot().value != 0 {
		t.Fatalf("output should not start for whitelisted position targets: %#v", outputs[1].snapshot())
	}
	if state := service.ScreenStrikeState(); state.Unattended.TargetPresent {
		t.Fatalf("whitelisted position targets should not mark target present: %#v", state.Unattended)
	}
}

func TestUnattendedStartsForUnwhitelistedPositionTarget(t *testing.T) {
	stateStore := store.New(10, 10)
	now := time.Now()
	_, _ = stateStore.AddPosition(model.ScreenPositionTarget{
		Serial:   "SN-WHITE",
		Model:    "Mini 4 Pro",
		Source:   "ddsT1",
		LastSeen: now,
	})
	_, _ = stateStore.AddPosition(model.ScreenPositionTarget{
		Serial:   "SN-ALERT",
		Model:    "Air 3",
		Source:   "ddsT1",
		LastSeen: now,
	})
	outputs, factory := newFakeOutputFactory()
	service := NewService(stateStore, DefaultChannels(), factory)
	service.SetUnattendedTimings(10*time.Millisecond, 20*time.Millisecond, 5*time.Millisecond)
	service.SetUserSettingsStore(memorySettingsStore{
		ok: true,
		settings: model.UserSettings{
			Whitelist: []model.WhitelistItem{
				{Serial: "SN-WHITE"},
			},
		},
	})
	defer service.Shutdown()

	_, err := service.SetUnattended(model.ScreenStrikeUnattendedConfig{
		Enabled:         true,
		ChannelIDs:      []string{"io1"},
		DurationSeconds: 10,
	})
	if err != nil {
		t.Fatalf("SetUnattended() error = %v", err)
	}
	waitUntil(t, 100*time.Millisecond, func() bool {
		return outputs[1] != nil && outputs[1].snapshot().value == 1
	})
}

func TestIsWhitelistedPositionTargetMatchesSerialOnly(t *testing.T) {
	target := model.ScreenPositionTarget{
		Serial: " SN-WHITE ",
		Model:  "Air 3",
		Source: "ddsT1",
	}
	whitelist := []model.WhitelistItem{
		{Serial: "sn-white", Model: "Different model", Source: "manual"},
	}

	if !isWhitelistedPositionTarget(target, whitelist) {
		t.Fatalf("target should match whitelist by serial")
	}
}

func TestUnattendedDisablesManualStartAndStopsActiveStrike(t *testing.T) {
	stateStore := store.New(10, 10)
	_, _ = stateStore.AddFPV(model.ScreenFPVTarget{
		Frequency:  5800,
		SignalType: "fpv",
		LastSeen:   time.Now(),
	})
	outputs, factory := newFakeOutputFactory()
	service := NewService(stateStore, DefaultChannels(), factory)
	service.SetUnattendedTimings(10*time.Millisecond, 20*time.Millisecond, 5*time.Millisecond)
	defer service.Shutdown()

	_, err := service.SetUnattended(model.ScreenStrikeUnattendedConfig{
		Enabled:         true,
		ChannelIDs:      []string{"io1"},
		DurationSeconds: 10,
	})
	if err != nil {
		t.Fatalf("SetUnattended() error = %v", err)
	}
	_, err = service.SetScreenStrike(model.ScreenStrikeRequest{
		Enabled:         true,
		ChannelIDs:      []string{"io2"},
		DurationSeconds: 10,
	})
	if ErrorCode(err) != "strike_unattended_active" {
		t.Fatalf("manual start error = %v, code = %q", err, ErrorCode(err))
	}

	waitUntil(t, 100*time.Millisecond, func() bool {
		return outputs[1] != nil && outputs[1].snapshot().value == 1
	})
	state, err := service.SetUnattended(model.ScreenStrikeUnattendedConfig{Enabled: false})
	if err != nil {
		t.Fatalf("disable unattended error = %v", err)
	}
	if state.Unattended.Enabled || state.Active || outputs[1].snapshot().value != 0 {
		t.Fatalf("state after disable = %#v output = %#v", state, outputs[1].snapshot())
	}
}

func waitUntil(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func newFakeOutputFactory() (map[int]*fakeOutput, OutputFactory) {
	outputs := map[int]*fakeOutput{}
	for number := 1; number <= 8; number++ {
		outputs[number] = &fakeOutput{}
	}
	return outputs, func(number int) Output {
		return outputs[number]
	}
}
