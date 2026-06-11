package interference

import (
	"errors"
	"testing"
	"time"

	"dr600ab-net/internal/model"
	"dr600ab-net/internal/store"
)

type fakePin struct {
	value     int
	direction string
	setupErr  error
	highErr   error
	lowErr    error
	cleaned   bool
}

func (p *fakePin) Setup() error {
	if p.setupErr != nil {
		return p.setupErr
	}
	p.direction = "out"
	return nil
}

func (p *fakePin) SetHigh() error {
	if p.highErr != nil {
		return p.highErr
	}
	p.value = 1
	return nil
}

func (p *fakePin) SetLow() error {
	if p.lowErr != nil {
		return p.lowErr
	}
	p.value = 0
	return nil
}

func (p *fakePin) GetValue() (int, error) {
	return p.value, nil
}

func (p *fakePin) GetDirection() (string, error) {
	if p.direction == "" {
		return "in", nil
	}
	return p.direction, nil
}

func (p *fakePin) Cleanup() {
	p.cleaned = true
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
	pins := map[int][]*fakePin{}
	service := NewService(store.New(10, 10), ChannelsFromNumbers([]int{2, 3, 1}), func(number int) GPIOPin {
		pin := &fakePin{}
		pins[number] = append(pins[number], pin)
		return pin
	})
	reports := &memoryReportStore{}
	service.SetReportStore(reports)
	service.SetUserSettingsStore(memorySettingsStore{
		ok: true,
		settings: model.UserSettings{
			ScreenStrikeChannelLabels: []string{"A", "B", "C"},
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
	if lastFakePin(pins[2]).value != 1 || lastFakePin(pins[1]).value != 1 {
		t.Fatalf("pins after start = %#v", pins)
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
	if !hasCleanedLowPin(pins[2]) || !hasCleanedLowPin(pins[1]) {
		t.Fatalf("pins after stop = %#v", pins)
	}
	if len(reports.updated) != 1 || reports.updated[0].Status != model.InterferenceReportStatusCompleted {
		t.Fatalf("updated reports = %#v", reports.updated)
	}
}

func TestScreenStrikeValidation(t *testing.T) {
	service := NewService(store.New(10, 10), ChannelsFromNumbers([]int{2, 3, 1}), func(number int) GPIOPin {
		return &fakePin{}
	})

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
		ChannelIDs:      []string{"io4"},
		DurationSeconds: 10,
	})
	if ErrorCode(err) != "strike_invalid_channels" {
		t.Fatalf("invalid channel error = %v, code = %q", err, ErrorCode(err))
	}
}

func TestSetStateRejectsTimedAndReservedChannels(t *testing.T) {
	service := NewService(store.New(10, 10), ChannelsFromNumbers([]int{2, 3, 1, 4}), func(number int) GPIOPin {
		return &fakePin{}
	})

	_, err := service.SetState("io1", true)
	if ErrorCode(err) != "strike_channel_requires_timed_operation" {
		t.Fatalf("timed channel error = %v, code = %q", err, ErrorCode(err))
	}

	_, err = service.SetState("io4", true)
	if ErrorCode(err) != "channel_reserved" {
		t.Fatalf("reserved channel error = %v, code = %q", err, ErrorCode(err))
	}
}

func TestScreenStrikeCreatesFailedReportOnPinError(t *testing.T) {
	cause := errors.New("gpio failed")
	service := NewService(store.New(10, 10), ChannelsFromNumbers([]int{2, 3, 1}), func(number int) GPIOPin {
		return &fakePin{highErr: cause}
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

func TestScreenStrikeTimeoutStopsChannels(t *testing.T) {
	pins := map[int][]*fakePin{}
	service := NewService(store.New(10, 10), ChannelsFromNumbers([]int{2, 3, 1}), func(number int) GPIOPin {
		pin := &fakePin{}
		pins[number] = append(pins[number], pin)
		return pin
	})
	reports := &memoryReportStore{}
	service.SetReportStore(reports)
	state, err := service.applyScreenStrike(true, []string{"io1"}, 20*time.Millisecond, 10)
	if err != nil {
		t.Fatalf("applyScreenStrike() error = %v", err)
	}
	if !state.Active || lastFakePin(pins[2]).value != 1 {
		t.Fatalf("started state = %#v pin = %#v", state, pins[2])
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !service.ScreenStrikeState().Active {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if service.ScreenStrikeState().Active {
		t.Fatal("strike should stop after timeout")
	}
	if !hasCleanedLowPin(pins[2]) {
		t.Fatalf("pin after timeout = %#v", pins[2])
	}
	if len(reports.updated) != 1 || reports.updated[0].Status != model.InterferenceReportStatusCompleted {
		t.Fatalf("updated reports = %#v", reports.updated)
	}
}

func lastFakePin(pins []*fakePin) *fakePin {
	if len(pins) == 0 {
		return &fakePin{}
	}
	return pins[len(pins)-1]
}

func hasCleanedLowPin(pins []*fakePin) bool {
	for _, pin := range pins {
		if pin.cleaned && pin.value == 0 {
			return true
		}
	}
	return false
}
