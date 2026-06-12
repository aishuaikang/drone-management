package interferencereport

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"dr600ab-net/internal/model"
)

func TestStoreCreateListGetDeleteAndCloseRunning(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "reports.db"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	running, err := store.CreateRunning(model.InterferenceReport{
		InterferenceReportSummary: model.InterferenceReportSummary{
			ID:                       "running",
			StartedAt:                now,
			RequestedDurationSeconds: 10,
			ChannelIDs:               []string{"io1"},
			ChannelLabels:            []string{"433M"},
			ChannelOutputs:           []int{2},
		},
		Request: model.ScreenStrikeRequest{
			Enabled:         true,
			ChannelIDs:      []string{"io1"},
			DurationSeconds: 10,
		},
		StartState: &model.ScreenStrikeState{Active: true, ChannelIDs: []string{"io1"}},
	})
	if err != nil {
		t.Fatalf("CreateRunning() error = %v", err)
	}
	endedAt := now.Add(time.Minute + 5*time.Second)
	failed, err := store.Create(model.InterferenceReport{
		InterferenceReportSummary: model.InterferenceReportSummary{
			ID:                       "failed",
			Status:                   model.InterferenceReportStatusFailed,
			StartedAt:                now.Add(time.Minute),
			EndedAt:                  &endedAt,
			RequestedDurationSeconds: 10,
			ChannelIDs:               []string{"io2"},
			ChannelLabels:            []string{"1.2G"},
			ChannelOutputs:           []int{3},
			LastError:                "relay failed",
		},
		Request: model.ScreenStrikeRequest{
			Enabled:         true,
			ChannelIDs:      []string{"io2"},
			DurationSeconds: 10,
		},
	})
	if err != nil {
		t.Fatalf("Create(failed) error = %v", err)
	}

	items, err := store.List(context.Background(), QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 2 || items[0].ID != failed.ID || items[1].ID != running.ID {
		t.Fatalf("items = %#v", items)
	}
	if items[0].DurationSeconds != 5 || items[0].ChannelLabels[0] != "1.2G" {
		t.Fatalf("failed summary = %#v", items[0])
	}

	filtered, err := store.List(context.Background(), QueryOptions{Status: model.InterferenceReportStatusFailed})
	if err != nil {
		t.Fatalf("filtered List() error = %v", err)
	}
	if len(filtered) != 1 || filtered[0].ID != failed.ID {
		t.Fatalf("filtered = %#v", filtered)
	}

	got, ok, err := store.Get(context.Background(), running.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok || got.Request.DurationSeconds != 10 || got.StartState == nil || !got.StartState.Active {
		t.Fatalf("Get() = %#v, ok=%v", got, ok)
	}

	if _, err := store.DeleteFailed(context.Background(), running.ID); !errors.Is(err, ErrNotFailed) {
		t.Fatalf("DeleteFailed(running) error = %v", err)
	}
	deleted, err := store.DeleteFailed(context.Background(), failed.ID)
	if err != nil {
		t.Fatalf("DeleteFailed(failed) error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d", deleted)
	}
	if _, ok, err := store.Get(context.Background(), failed.ID); err != nil || ok {
		t.Fatalf("failed report should be deleted: ok=%v err=%v", ok, err)
	}

	closed, err := store.CloseRunning("service_restarted", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("CloseRunning() error = %v", err)
	}
	if closed != 1 {
		t.Fatalf("closed = %d, want 1", closed)
	}
	got, ok, err = store.Get(context.Background(), running.ID)
	if err != nil {
		t.Fatalf("Get(closed) error = %v", err)
	}
	if !ok || got.Status != model.InterferenceReportStatusAbnormal || got.AbnormalReason != "service_restarted" || got.EndedAt == nil {
		t.Fatalf("closed report = %#v", got)
	}
}

func TestParseStatus(t *testing.T) {
	status, err := ParseStatus("failed")
	if err != nil || status != model.InterferenceReportStatusFailed {
		t.Fatalf("ParseStatus(failed) = %q, %v", status, err)
	}
	status, err = ParseStatus("")
	if err != nil || status != "" {
		t.Fatalf("ParseStatus(empty) = %q, %v", status, err)
	}
	if _, err := ParseStatus("bad"); err == nil {
		t.Fatal("ParseStatus(bad) should fail")
	}
}
