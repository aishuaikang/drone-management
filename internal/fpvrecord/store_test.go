package fpvrecord

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dr600ab-net/internal/model"
)

func TestStoreInsertListGetAndDelete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "records.db"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	records := []model.FPVVideoRecord{
		{
			ID:              "old",
			TargetID:        "fpv-old",
			Frequency:       1360,
			RSSI:            -60,
			SignalType:      "FPV",
			DeviceSN:        "A-1",
			StartedAt:       now,
			EndedAt:         now.Add(2 * time.Second),
			DurationSeconds: 2,
			Status:          model.FPVVideoRecordStatusReady,
			FileName:        "old.mp4",
			FileSizeBytes:   12,
		},
		{
			ID:              "new",
			TargetID:        "fpv-new",
			Frequency:       1400,
			RSSI:            -50,
			SignalType:      "O4",
			DeviceSN:        "B-2",
			StartedAt:       now.Add(time.Hour),
			EndedAt:         now.Add(time.Hour + time.Second),
			DurationSeconds: 1,
			Status:          model.FPVVideoRecordStatusFailed,
			Error:           "missing",
			LastRecord: model.ScreenFPVLastRecord{
				Format:     "ascii",
				ReceivedAt: now.Add(time.Hour),
				Frequency:  1400,
				RSSI:       -50,
				SignalType: "O4",
				Valid:      true,
			},
		},
	}
	for _, record := range records {
		if err := store.Insert(context.Background(), record); err != nil {
			t.Fatalf("Insert(%s) error = %v", record.ID, err)
		}
	}

	items, err := store.List(context.Background(), QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 2 || items[0].ID != "new" || items[1].ID != "old" {
		t.Fatalf("records order = %#v", items)
	}
	if items[0].LastRecord.SignalType != "O4" {
		t.Fatalf("last record was not decoded: %#v", items[0].LastRecord)
	}

	filtered, err := store.List(context.Background(), QueryOptions{
		SignalType: "fp",
		DeviceSN:   "a-",
		DateFrom:   now.Add(-time.Hour),
		DateTo:     now,
	})
	if err != nil {
		t.Fatalf("filtered List() error = %v", err)
	}
	if len(filtered) != 1 || filtered[0].ID != "old" {
		t.Fatalf("filtered records = %#v", filtered)
	}

	got, ok, err := store.Get(context.Background(), "old")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok || got.FileName != "old.mp4" {
		t.Fatalf("Get() = %#v, %v", got, ok)
	}

	result, err := store.Delete(context.Background(), []string{"old", "old"}, dir)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if result.Deleted != 1 || len(result.FilePaths) != 1 || result.FilePaths[0] != filepath.Join(dir, "old.mp4") {
		t.Fatalf("Delete() = %#v", result)
	}
}

func TestSafeRecordPathRejectsNestedNames(t *testing.T) {
	dir := t.TempDir()
	if _, ok := SafeRecordPath(dir, "../x.mp4"); ok {
		t.Fatal("nested path should be rejected")
	}
	if _, ok := SafeRecordPath(dir, filepath.Join("nested", "x.mp4")); ok {
		t.Fatal("path with separator should be rejected")
	}
	path, ok := SafeRecordPath(dir, "x.mp4")
	if !ok || filepath.Dir(path) != dir {
		t.Fatalf("SafeRecordPath() = %q, %v", path, ok)
	}
	if err := os.WriteFile(path, []byte("video"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
