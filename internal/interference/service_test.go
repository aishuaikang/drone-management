package interference

import (
	"errors"
	"testing"
	"time"

	"dr600ab-net/internal/model"
	"dr600ab-net/internal/store"
)

type fakeOutput struct {
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
	if o.setupErr != nil {
		return o.setupErr
	}
	return nil
}

func (o *fakeOutput) SetHigh() error {
	if o.highErr != nil {
		return o.highErr
	}
	o.value = 1
	o.remaining = 0
	return nil
}

func (o *fakeOutput) SetHighFor(duration time.Duration) error {
	if o.highErr != nil {
		return o.highErr
	}
	o.value = 1
	o.remaining = duration
	o.timedDurations = append(o.timedDurations, duration)
	return nil
}

func (o *fakeOutput) SetLow() error {
	if o.lowErr != nil {
		return o.lowErr
	}
	o.value = 0
	o.remaining = 0
	return nil
}

func (o *fakeOutput) GetValue() (int, error) {
	return o.value, nil
}

func (o *fakeOutput) GetState() (OutputState, error) {
	if o.stateErr != nil {
		return OutputState{}, o.stateErr
	}
	return OutputState{
		Value:     o.value,
		Remaining: o.remaining,
	}, nil
}

func (o *fakeOutput) Cleanup() {
	o.cleaned = true
}

type memoryReportStore struct {
	created []model.InterferenceReport
	updated []model.InterferenceReport
}

func (s *memoryReportStore) Create(report model.InterferenceReport) (model.InterferenceReport, error) {
	if report.ID == "" {
		report.ID = "report-created"
	}
	s.created = append(s.created, report)
	return report, nil
}

func (s *memoryReportStore) CreateRunning(report model.InterferenceReport) (model.InterferenceReport, error) {
	report.Status = model.InterferenceReportStatusRunning
	if report.ID == "" {
		report.ID = "report-running"
	}
	s.created = append(s.created, report)
	return report, nil
}

func (s *memoryReportStore) Update(report model.InterferenceReport) error {
	s.updated = append(s.updated, report)
	return nil
}

type memorySettingsStore struct {
	settings model.UserSettings
	ok       bool
}

func (s memorySettingsStore) LoadUser() (model.UserSettings, bool, error) {
	return s.settings, s.ok, nil
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
	if outputs[1].value != 1 || outputs[3].value != 1 {
		t.Fatalf("outputs after start = %#v", outputs)
	}
	if len(outputs[1].timedDurations) != 1 || outputs[1].timedDurations[0] != 10*time.Second {
		t.Fatalf("timed durations for output 1 = %#v", outputs[1].timedDurations)
	}
	if len(outputs[3].timedDurations) != 1 || outputs[3].timedDurations[0] != 10*time.Second {
		t.Fatalf("timed durations for output 3 = %#v", outputs[3].timedDurations)
	}
	if len(reports.created) != 1 || reports.created[0].Status != model.InterferenceReportStatusRunning {
		t.Fatalf("created report = %#v", reports.created)
	}
	if got := reports.created[0].ChannelLabels; len(got) != 2 || got[0] != "A" || got[1] != "C" {
		t.Fatalf("channel labels = %#v", got)
	}

	state, err = service.SetScreenStrike(model.ScreenStrikeRequest{Enabled: false})
	if err != nil {
		t.Fatalf("SetScreenStrike(stop) error = %v", err)
	}
	if state.Active {
		t.Fatalf("state should be inactive after stop: %#v", state)
	}
	if !outputs[1].cleaned || outputs[1].value != 0 || !outputs[3].cleaned || outputs[3].value != 0 {
		t.Fatalf("outputs after stop = %#v", outputs)
	}
	if len(reports.updated) != 1 || reports.updated[0].Status != model.InterferenceReportStatusCompleted {
		t.Fatalf("updated reports = %#v", reports.updated)
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
	if len(reports.created) != 1 || reports.created[0].Status != model.InterferenceReportStatusFailed {
		t.Fatalf("created reports = %#v", reports.created)
	}
	if reports.created[0].LastError != cause.Error() {
		t.Fatalf("last error = %q", reports.created[0].LastError)
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
	if !state.Active || outputs[1].value != 1 {
		t.Fatalf("started state = %#v output = %#v", state, outputs[1])
	}

	time.Sleep(50 * time.Millisecond)
	state = service.ScreenStrikeState()
	if !state.Active || outputs[1].value != 1 {
		t.Fatalf("service should keep reporting relay state until relay opens: state=%#v output=%#v", state, outputs[1])
	}
	if len(reports.updated) != 0 {
		t.Fatalf("reports should not be completed by a local timer: %#v", reports.updated)
	}

	outputs[1].value = 0
	outputs[1].remaining = 0
	state = service.ScreenStrikeState()
	if state.Active {
		t.Fatalf("state should follow relay auto-off: %#v", state)
	}
	if len(reports.updated) != 1 || reports.updated[0].Status != model.InterferenceReportStatusCompleted {
		t.Fatalf("updated reports = %#v", reports.updated)
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
	outputs[1].stateErr = cause
	state = service.ScreenStrikeState()
	if state.Active || len(state.ChannelIDs) != 0 {
		t.Fatalf("read failure should not reuse stale active state: %#v", state)
	}
	if len(state.Channels) == 0 || state.Channels[0].Status != "error" || state.Channels[0].LastError != cause.Error() {
		t.Fatalf("channel should expose relay read error: %#v", state.Channels)
	}
	if len(reports.updated) != 0 {
		t.Fatalf("report should not complete while relay state is unknown: %#v", reports.updated)
	}

	outputs[1].stateErr = nil
	outputs[1].value = 0
	outputs[1].remaining = 0
	state = service.ScreenStrikeState()
	if state.Active {
		t.Fatalf("state should become inactive after confirmed relay read: %#v", state)
	}
	if len(reports.updated) != 1 || reports.updated[0].Status != model.InterferenceReportStatusCompleted {
		t.Fatalf("report should complete after confirmed inactive state: %#v", reports.updated)
	}
}

func newFakeOutputFactory() (map[int]*fakeOutput, OutputFactory) {
	outputs := map[int]*fakeOutput{}
	return outputs, func(number int) Output {
		output := outputs[number]
		if output == nil {
			output = &fakeOutput{}
			outputs[number] = output
		}
		return output
	}
}
