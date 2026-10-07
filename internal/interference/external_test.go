package interference

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"drone-management/internal/interferencereport"
	"drone-management/internal/model"
	"drone-management/internal/store"
)

func externalTestService(t *testing.T) (*Service, *interferencereport.Store, map[int]*fakeOutput) {
	t.Helper()
	reports, err := interferencereport.NewStore(filepath.Join(t.TempDir(), "reports.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reports.Close() })
	outputs := make(map[int]*fakeOutput)
	for number := 1; number <= relayChannelCount; number++ {
		outputs[number] = &fakeOutput{}
	}
	service := NewService(store.New(10, 10), DefaultChannels(), func(number int) Output { return outputs[number] })
	service.SetReportStore(reports)
	return service, reports, outputs
}

func externalTestReports(t *testing.T, reports *interferencereport.Store) []model.InterferenceReportSummary {
	t.Helper()
	items, err := reports.List(context.Background(), interferencereport.QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestExternalControlRecordsEachChannelAndIgnoresDIAndDuplicates(t *testing.T) {
	service, reports, outputs := externalTestService(t)
	start := time.Now().Add(-time.Minute)
	update := RelayStateUpdate{Time: start, Inputs: true, Values: [8]int{1}}
	service.ObserveRelayState(update)
	if got := externalTestReports(t, reports); len(got) != 0 || service.ScreenStrikeActive() {
		t.Fatalf("DI-only report/state = %#v / %v", got, service.ScreenStrikeActive())
	}
	update.Inputs = false
	service.ObserveRelayState(update)
	service.ObserveRelayState(update)
	if got := externalTestReports(t, reports); len(got) != 1 || got[0].OperationType != model.InterferenceOperationExternal {
		t.Fatalf("duplicate output report = %#v", got)
	}
	update.Time = start.Add(2 * time.Second)
	update.Values[1] = 1
	service.ObserveRelayState(update)
	update.Time = start.Add(5 * time.Second)
	update.Values[0] = 0
	service.ObserveRelayState(update)
	items := externalTestReports(t, reports)
	if len(items) != 2 || items[0].Status != model.InterferenceReportStatusRunning ||
		items[1].Status != model.InterferenceReportStatusCompleted || items[1].DurationSeconds != 5 {
		t.Fatalf("independent reports = %#v", items)
	}
	state := service.CachedScreenStrikeState()
	if !state.Active || !reflect.DeepEqual(state.ChannelIDs, []string{"io2"}) ||
		state.StartedAt == nil || !state.StartedAt.Equal(start.Add(2*time.Second)) || state.DurationSeconds != 0 {
		t.Fatalf("external screen state = %#v", state)
	}
	update.Time = start.Add(8 * time.Second)
	update.Values[1] = 0
	service.ObserveRelayState(update)
	items = externalTestReports(t, reports)
	if items[0].DurationSeconds != 6 || items[0].Status != model.InterferenceReportStatusCompleted || service.ScreenStrikeActive() {
		t.Fatalf("finished reports/state = %#v / %v", items, service.ScreenStrikeActive())
	}
	for _, item := range items {
		if item.RequestedDurationSeconds != 0 || len(item.ChannelOutputs) != 1 {
			t.Fatalf("external metadata = %#v", item)
		}
		detail, found, err := reports.Get(context.Background(), item.ID)
		if err != nil || !found || detail.Request.Enabled || detail.EndState == nil || detail.StartState == nil {
			t.Fatalf("external evidence = %#v, found=%v error=%v", detail, found, err)
		}
	}
	for number, output := range outputs {
		if snapshot := output.snapshot(); snapshot.value != 0 || snapshot.readCount != 0 || len(snapshot.timedDurations) != 0 {
			t.Fatalf("observation performed I/O on output %d: %#v", number, snapshot)
		}
	}
}

func TestExternalControlSurvivesDisconnectAndClosesAbnormallyOnShutdown(t *testing.T) {
	service, reports, outputs := externalTestService(t)
	start := time.Now().Add(-time.Minute)
	service.ObserveRelayState(RelayStateUpdate{Time: start, Values: [8]int{1}})
	service.ObserveRelayState(RelayStateUpdate{Time: start.Add(time.Second), Error: errors.New("disconnected")})
	items := externalTestReports(t, reports)
	if len(items) != 1 || items[0].Status != model.InterferenceReportStatusRunning || items[0].EndedAt != nil {
		t.Fatalf("disconnect fabricated a stop: %#v", items)
	}
	if state := service.CachedScreenStrikeState(); state.Channels[0].ActualLevel != "unknown" {
		t.Fatalf("disconnected output state = %#v", state)
	}
	service.ObserveRelayState(RelayStateUpdate{Time: start.Add(2 * time.Second), Values: [8]int{1}})
	if items = externalTestReports(t, reports); len(items) != 1 {
		t.Fatalf("reconnect duplicated report: %#v", items)
	}
	service.ObserveRelayState(RelayStateUpdate{Time: start.Add(5 * time.Second)})
	if items = externalTestReports(t, reports); items[0].Status != model.InterferenceReportStatusCompleted || items[0].DurationSeconds != 5 {
		t.Fatalf("reconnected stop = %#v", items)
	}
	service.ObserveRelayState(RelayStateUpdate{Time: start.Add(6 * time.Second), Values: [8]int{1}})
	outputs[1].setState(1, 0)
	service.Shutdown()
	items = externalTestReports(t, reports)
	if len(items) != 2 || items[0].Status != model.InterferenceReportStatusAbnormal || items[0].AbnormalReason != "service_shutdown" {
		t.Fatalf("shutdown report = %#v", items)
	}
}

func TestSoftwareReportIsNotDuplicatedOrHeldOpenByExternalChannel(t *testing.T) {
	service, reports, _ := externalTestService(t)
	_, err := service.SetScreenStrike(model.ScreenStrikeRequest{Enabled: true, ChannelIDs: []string{"io1"}, DurationSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	service.ObserveRelayState(RelayStateUpdate{Time: start, Values: [8]int{1}})
	service.ObserveRelayState(RelayStateUpdate{Time: start.Add(time.Second), Values: [8]int{1, 1}})
	items := externalTestReports(t, reports)
	if len(items) != 2 || items[0].OperationType != model.InterferenceOperationExternal || items[0].RequestedDurationSeconds != 0 ||
		items[1].OperationType != model.InterferenceOperationManual {
		t.Fatalf("software/field reports = %#v", items)
	}
	service.ObserveRelayState(RelayStateUpdate{Time: start.Add(2 * time.Second), Values: [8]int{0, 1}})
	items = externalTestReports(t, reports)
	if items[1].Status != model.InterferenceReportStatusCompleted || items[0].Status != model.InterferenceReportStatusRunning ||
		!service.ScreenStrikeActive() {
		t.Fatalf("software completion while external output active = %#v", items)
	}
	service.ObserveRelayState(RelayStateUpdate{Time: start.Add(3 * time.Second)})
	if items = externalTestReports(t, reports); len(items) != 2 || items[0].Status != model.InterferenceReportStatusCompleted {
		t.Fatalf("completed reports = %#v", items)
	}
}

func TestStaleRelayObservationDoesNotUndoSoftwareCommand(t *testing.T) {
	service, reports, _ := externalTestService(t)
	old := time.Now().Add(-time.Second)
	_, err := service.SetScreenStrike(model.ScreenStrikeRequest{Enabled: true, ChannelIDs: []string{"io1"}, DurationSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	service.ObserveRelayState(RelayStateUpdate{Time: old})
	items := externalTestReports(t, reports)
	if len(items) != 1 || items[0].Status != model.InterferenceReportStatusRunning || !service.ScreenStrikeActive() {
		t.Fatalf("stale snapshot changed commanded state: %#v", items)
	}
}

func TestExternalReportEndsWhenSoftwareConfirmsStop(t *testing.T) {
	service, reports, _ := externalTestService(t)
	service.ObserveRelayState(RelayStateUpdate{Time: time.Now().Add(-10 * time.Second), Values: [8]int{1}})
	if _, err := service.SetState("io1", false); err != nil {
		t.Fatal(err)
	}
	items := externalTestReports(t, reports)
	if len(items) != 1 || items[0].Status != model.InterferenceReportStatusCompleted || items[0].EndedAt == nil {
		t.Fatalf("software-confirmed external stop = %#v", items)
	}
	service.ObserveRelayState(RelayStateUpdate{Time: time.Now().Add(time.Second)})
	if items = externalTestReports(t, reports); len(items) != 1 || service.ScreenStrikeActive() {
		t.Fatalf("stop feedback changed completed operation: %#v", items)
	}
}

func TestQueuedRelayObservationDoesNotUndoNewerASCIIRead(t *testing.T) {
	for _, test := range []struct {
		name        string
		queuedValue int
		queuedError error
		actualValue int
		readError   error
	}{
		{name: "older high", queuedValue: 1, actualValue: 0},
		{name: "older low", queuedValue: 0, actualValue: 1},
		{name: "older error", queuedError: errors.New("disconnected"), actualValue: 1},
		{name: "older high after read failure", queuedValue: 1, actualValue: 1, readError: errors.New("read failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, reports, outputs := externalTestService(t)
			if _, err := service.SetScreenStrike(model.ScreenStrikeRequest{
				Enabled: true, ChannelIDs: []string{"io1"}, DurationSeconds: 10,
			}); err != nil {
				t.Fatal(err)
			}
			queued := RelayStateUpdate{
				Time: time.Now(), Values: [8]int{test.queuedValue}, Error: test.queuedError,
			}
			outputs[1].setState(test.actualValue, 0)
			outputs[1].setStateErr(test.readError)
			refreshed := service.ScreenStrikeState()
			service.ObserveRelayState(queued)
			state := service.CachedScreenStrikeState()
			if state.Active != refreshed.Active || state.Channels[0].ActualLevel != refreshed.Channels[0].ActualLevel ||
				state.Channels[0].LastError != refreshed.Channels[0].LastError {
				t.Fatalf("queued observation replaced newer ASCII state: refreshed active=%v channel=%#v; cached active=%v channel=%#v",
					refreshed.Active, refreshed.Channels[0], state.Active, state.Channels[0])
			}
			status := model.InterferenceReportStatusRunning
			if test.actualValue == 0 {
				status = model.InterferenceReportStatusCompleted
			}
			items := externalTestReports(t, reports)
			if len(items) != 1 || items[0].OperationType != model.InterferenceOperationManual || items[0].Status != status {
				t.Fatalf("queued observation changed manual report lifecycle: %#v", items)
			}
		})
	}
}

func TestRelayObservationDoesNotUndoNewerRelayObservation(t *testing.T) {
	service, reports, _ := externalTestService(t)
	startedAt := time.Now().Add(-10 * time.Second)
	service.ObserveRelayState(RelayStateUpdate{Time: startedAt, Values: [8]int{1}})
	service.ObserveRelayState(RelayStateUpdate{Time: startedAt.Add(5 * time.Second)})
	service.ObserveRelayState(RelayStateUpdate{Time: startedAt.Add(2 * time.Second), Values: [8]int{1}})
	items := externalTestReports(t, reports)
	if len(items) != 1 || items[0].Status != model.InterferenceReportStatusCompleted ||
		items[0].DurationSeconds != 5 || service.ScreenStrikeActive() {
		t.Fatalf("older relay observation changed completed external operation: %#v", items)
	}
}

func TestExternalReportEndsWhenASCIIReadConfirmsStop(t *testing.T) {
	for _, test := range []struct {
		name    string
		refresh func(*Service)
	}{
		{name: "screen strike", refresh: func(s *Service) { s.ScreenStrikeState() }},
		{name: "channel list", refresh: func(s *Service) { s.ListChannels() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, reports, outputs := externalTestService(t)
			outputs[1].setState(1, 0)
			service.ObserveRelayState(RelayStateUpdate{Time: time.Now().Add(-10 * time.Second), Values: [8]int{1}})
			outputs[1].setState(0, 0)
			test.refresh(service)
			items := externalTestReports(t, reports)
			if len(items) != 1 || items[0].Status != model.InterferenceReportStatusCompleted || items[0].EndedAt == nil ||
				service.ScreenStrikeActive() {
				t.Fatalf("ASCII-confirmed stop did not finish external operation: %#v", items)
			}
			externalID, endedAt := items[0].ID, *items[0].EndedAt
			detail, found, err := reports.Get(context.Background(), externalID)
			if err != nil || !found || detail.EndState == nil || detail.EndState.Channels[0].ActualLevel != "low" {
				t.Fatalf("ASCII-confirmed stop evidence = %#v, found=%v error=%v", detail, found, err)
			}
			if _, err := service.SetScreenStrike(model.ScreenStrikeRequest{
				Enabled: true, ChannelIDs: []string{"io1"}, DurationSeconds: 10,
			}); err != nil {
				t.Fatal(err)
			}
			service.ObserveRelayState(RelayStateUpdate{Time: time.Now(), Values: [8]int{1}})
			outputs[1].setState(0, 0)
			service.ObserveRelayState(RelayStateUpdate{Time: time.Now()})
			items = externalTestReports(t, reports)
			if len(items) != 2 || service.ScreenStrikeActive() {
				t.Fatalf("manual operation after external stop = %#v", items)
			}
			for _, item := range items {
				if item.Status != model.InterferenceReportStatusCompleted || item.EndedAt == nil ||
					(item.ID == externalID && !item.EndedAt.Equal(endedAt)) {
					t.Fatalf("manual operation changed completed external report: %#v", items)
				}
			}
		})
	}
}

func TestExternalReportsPreserveTransitionsWithSameReceiveTime(t *testing.T) {
	service, reports, _ := externalTestService(t)
	receivedAt := time.Now()
	for _, value := range []int{1, 0, 1, 0} {
		service.ObserveRelayState(RelayStateUpdate{Time: receivedAt, Values: [8]int{value}})
	}
	items := externalTestReports(t, reports)
	if len(items) != 2 || service.ScreenStrikeActive() {
		t.Fatalf("coalesced relay transitions = %#v", items)
	}
	for _, item := range items {
		if item.OperationType != model.InterferenceOperationExternal || item.Status != model.InterferenceReportStatusCompleted ||
			item.EndedAt == nil || !item.EndedAt.Equal(receivedAt) || item.DurationSeconds != 0 {
			t.Fatalf("coalesced relay report = %#v", item)
		}
	}
}
