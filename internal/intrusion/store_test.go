package intrusion

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"dr600ab-net/internal/model"
)

func TestStoreArchivesListsAndIgnoresDuplicatePosition(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC)
	height := 120.5
	speed := 8.2
	store.SetDeviceLocationProvider(func() model.ScreenDeviceLocationResponse {
		updatedAt := base
		return model.ScreenDeviceLocationResponse{
			Source:    "manual",
			Point:     &model.GeoPoint{Latitude: 31.20, Longitude: 121.40},
			UpdatedAt: &updatedAt,
			Valid:     true,
		}
	})

	target := model.ScreenPositionTarget{
		ID:        "target-1",
		Serial:    "SN-001",
		Model:     "Mini 4 Pro",
		Source:    "RID",
		Sources:   []string{"RID", "dji_O:4"},
		Frequency: 2437,
		RSSI:      -42,
		Drone:     &model.ScreenPositionPoint{Latitude: 31.21, Longitude: 121.41},
		Pilot:     &model.ScreenPositionPoint{Latitude: 31.22, Longitude: 121.42},
		Home:      &model.ScreenPositionPoint{Latitude: 31.23, Longitude: 121.43},
		DroneTrajectory: []model.ScreenPositionTrackPoint{
			{Latitude: 31.21, Longitude: 121.41, Time: base},
		},
		PilotTrajectory: []model.ScreenPositionTrackPoint{
			{Latitude: 31.22, Longitude: 121.42, Time: base},
		},
		Height:    &height,
		Speed:     &speed,
		Cracked:   true,
		FirstSeen: base,
		LastSeen:  base.Add(20 * time.Second),
		HitCount:  3,
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "RID",
			ReceivedAt: base.Add(20 * time.Second),
			Serial:     "SN-001",
			Model:      "Mini 4 Pro",
			Raw:        "raw-record",
		},
	}

	if err := store.ArchivePositionContext(ctx, target); err != nil {
		t.Fatalf("ArchivePositionContext() error = %v", err)
	}
	if err := store.ArchivePositionContext(ctx, target); err != nil {
		t.Fatalf("duplicate ArchivePositionContext() error = %v", err)
	}

	items, err := store.List(ctx, QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("records = %d, want 1", len(items))
	}
	got := items[0]
	if got.TargetType != model.IntrusionTargetTypePosition || got.Serial != "SN-001" || got.DurationSeconds != 20 || got.HitCount != 3 {
		t.Fatalf("record summary = %#v", got)
	}
	if got.DeviceLocation == nil || got.DeviceLocation.Point == nil || got.PilotDistanceM == nil || got.DroneDistanceM == nil {
		t.Fatalf("record location relations missing = %#v", got)
	}
	if got.Drone == nil || got.Pilot == nil || got.Home == nil || len(got.DroneTrajectory) != 1 || len(got.PilotTrajectory) != 1 {
		t.Fatalf("record coordinates missing = %#v", got)
	}
	if got.Height == nil || *got.Height != height || got.Speed == nil || *got.Speed != speed {
		t.Fatalf("record motion values = %#v", got)
	}
	if got.LastRecord.Raw != "raw-record" {
		t.Fatalf("last record raw = %q", got.LastRecord.Raw)
	}
}

func TestStoreArchivesFullTrajectoryWhenDisplayTrajectoryIsCapped(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC)
	full := make([]model.ScreenPositionTrackPoint, 90)
	for index := range full {
		full[index] = model.ScreenPositionTrackPoint{
			Latitude:  31.20 + float64(index)*0.0001,
			Longitude: 121.40 + float64(index)*0.0001,
			Time:      base.Add(time.Duration(index) * time.Second),
		}
	}

	err := store.ArchivePositionContext(ctx, model.ScreenPositionTarget{
		ID:                  "long-track",
		Serial:              "SN-LONG",
		Model:               "Mini 4 Pro",
		Source:              "RID",
		Drone:               &model.ScreenPositionPoint{Latitude: 31.22, Longitude: 121.42},
		DroneTrajectory:     full[10:],
		FullDroneTrajectory: full,
		FirstSeen:           base,
		LastSeen:            base.Add(90 * time.Second),
	})
	if err != nil {
		t.Fatalf("ArchivePositionContext() error = %v", err)
	}

	items, err := store.List(ctx, QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("records = %d, want 1", len(items))
	}
	if len(items[0].DroneTrajectory) != len(full) {
		t.Fatalf("stored trajectory points = %d, want %d", len(items[0].DroneTrajectory), len(full))
	}
	if items[0].DroneTrajectory[0].Latitude != full[0].Latitude {
		t.Fatalf("stored trajectory starts at %#v, want %#v", items[0].DroneTrajectory[0], full[0])
	}
}

func TestStoreSkipsUncrackedDJIDronePosition(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC)

	if err := store.ArchivePositionContext(ctx, model.ScreenPositionTarget{
		ID:        "encrypted-dji-drone",
		Serial:    "temporary-id",
		Model:     "DJI-Drone",
		Source:    "dji_O:4",
		FirstSeen: now,
		LastSeen:  now.Add(10 * time.Second),
		HitCount:  2,
		Cracked:   false,
	}); err != nil {
		t.Fatalf("ArchivePositionContext() error = %v", err)
	}
	if err := store.ArchivePositionContext(ctx, model.ScreenPositionTarget{
		ID:        "encrypted-dji-drone",
		Serial:    "real-sn",
		Model:     "Mini 4 Pro",
		Source:    "dji_O:4",
		FirstSeen: now,
		LastSeen:  now.Add(20 * time.Second),
		HitCount:  3,
		Cracked:   true,
	}); err != nil {
		t.Fatalf("ArchivePositionContext() decoded target error = %v", err)
	}

	items, err := store.List(ctx, QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0].Serial != "real-sn" {
		t.Fatalf("records = %#v, want decoded target only", items)
	}
}

func TestStoreListHidesExistingUncrackedDJIDroneRecords(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC)

	if err := store.insert(ctx, model.IntrusionRecord{
		ID:         "old-uncracked",
		TargetID:   "old-uncracked-target",
		TargetType: model.IntrusionTargetTypePosition,
		Model:      "DJI-Drone",
		Serial:     "447e5681",
		FirstSeen:  now,
		LastSeen:   now,
		ArchivedAt: now,
		Cracked:    false,
	}); err != nil {
		t.Fatalf("insert uncracked record: %v", err)
	}
	if err := store.insert(ctx, model.IntrusionRecord{
		ID:         "decoded",
		TargetID:   "decoded-target",
		TargetType: model.IntrusionTargetTypePosition,
		Model:      "Mini 4 Pro",
		Serial:     "real-sn",
		FirstSeen:  now,
		LastSeen:   now.Add(time.Second),
		ArchivedAt: now,
		Cracked:    true,
	}); err != nil {
		t.Fatalf("insert decoded record: %v", err)
	}

	items, err := store.List(ctx, QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0].ID != "decoded" {
		t.Fatalf("records = %#v, want decoded record only", items)
	}
}

func TestStoreListDeleteAndPruneRetention(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	old := model.IntrusionRecord{
		ID:         "old",
		TargetID:   "old-target",
		TargetType: model.IntrusionTargetTypePosition,
		FirstSeen:  now.Add(-2 * time.Hour),
		LastSeen:   now.Add(-2 * time.Hour),
		ArchivedAt: now.AddDate(0, 0, -100),
	}
	recent := model.IntrusionRecord{
		ID:         "recent",
		TargetID:   "recent-target",
		TargetType: model.IntrusionTargetTypePosition,
		FirstSeen:  now,
		LastSeen:   now,
		ArchivedAt: now,
	}
	if err := store.insert(ctx, old); err != nil {
		t.Fatalf("insert old: %v", err)
	}
	if err := store.insert(ctx, recent); err != nil {
		t.Fatalf("insert recent: %v", err)
	}

	items, err := store.List(ctx, QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 2 || items[0].ID != "recent" || items[1].ID != "old" {
		t.Fatalf("list order = %#v", items)
	}

	deleted, err := store.Delete(ctx, []string{"missing", "recent", "recent"})
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}

	pruned, err := store.PruneRetention(ctx, 90, now)
	if err != nil {
		t.Fatalf("PruneRetention() error = %v", err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1", pruned)
	}
	items, err = store.List(ctx, QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List() after prune error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items after delete/prune = %#v", items)
	}
}

func TestStoreListFiltersRecords(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	records := []model.IntrusionRecord{
		{ID: "match", TargetID: "match-target", TargetType: model.IntrusionTargetTypePosition, Model: "Mini 4 Pro", Serial: "SN-2", FirstSeen: now, LastSeen: now, ArchivedAt: now},
		{ID: "other-model", TargetID: "other-model-target", TargetType: model.IntrusionTargetTypePosition, Model: "Mavic 3", Serial: "SN-2", FirstSeen: now, LastSeen: now, ArchivedAt: now},
		{ID: "other-date", TargetID: "other-date-target", TargetType: model.IntrusionTargetTypePosition, Model: "Mini 4 Pro", Serial: "SN-2", FirstSeen: now.AddDate(0, 0, -2), LastSeen: now.AddDate(0, 0, -2), ArchivedAt: now},
		{ID: "other-serial", TargetID: "other-serial-target", TargetType: model.IntrusionTargetTypePosition, Model: "Mini 4 Pro", Serial: "SN-1", FirstSeen: now, LastSeen: now, ArchivedAt: now},
	}
	for _, record := range records {
		if err := store.insert(ctx, record); err != nil {
			t.Fatalf("insert %s: %v", record.ID, err)
		}
	}

	items, err := store.List(ctx, QueryOptions{
		Limit:    10,
		Model:    "mini",
		Serial:   "sn-2",
		DateFrom: now,
		DateTo:   now,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0].ID != "match" {
		t.Fatalf("filtered records = %#v", items)
	}
}

func TestStorePruneRetentionZeroKeepsRecords(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	record := model.IntrusionRecord{
		ID:         "old",
		TargetID:   "old-target",
		TargetType: model.IntrusionTargetTypePosition,
		FirstSeen:  now.Add(-time.Hour),
		LastSeen:   now.Add(-time.Hour),
		ArchivedAt: now.AddDate(0, 0, -100),
	}
	if err := store.insert(ctx, record); err != nil {
		t.Fatalf("insert record: %v", err)
	}
	pruned, err := store.PruneRetention(ctx, 0, now)
	if err != nil {
		t.Fatalf("PruneRetention(0) error = %v", err)
	}
	if pruned != 0 {
		t.Fatalf("pruned = %d, want 0", pruned)
	}
	items, err := store.List(ctx, QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
}

func TestParseTargetType(t *testing.T) {
	if targetType, err := ParseTargetType("position"); err != nil || targetType != model.IntrusionTargetTypePosition {
		t.Fatalf("ParseTargetType(position) = %q, %v", targetType, err)
	}
	if _, err := ParseTargetType("fpv"); err == nil {
		t.Fatal("ParseTargetType(fpv) error = nil, want error")
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "intrusions.db"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	return store
}
