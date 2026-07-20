package intrusion

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"drone-management/internal/model"
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
	if got.LastRecord == nil || got.LastRecord.Raw != "raw-record" {
		t.Fatalf("last record = %#v", got.LastRecord)
	}
	positionJSON := intrusionRecordJSONFields(t, got)
	if _, ok := positionJSON["lastRecord"]; !ok {
		t.Fatalf("position JSON = %s, missing lastRecord", mustMarshalJSON(t, got))
	}
}

func TestStoreArchivesFullTrajectoryWhenDisplayTrajectoryIsPartial(t *testing.T) {
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

func TestStoreArchivesValidFPVOnce(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC)
	updatedAt := base.Add(-time.Minute)
	store.SetDeviceLocationProvider(func() model.ScreenDeviceLocationResponse {
		return model.ScreenDeviceLocationResponse{
			Source:    "gps",
			Point:     &model.GeoPoint{Latitude: 31.20, Longitude: 121.40},
			UpdatedAt: &updatedAt,
			Valid:     true,
		}
	})

	invalid := model.ScreenFPVTarget{
		ID:        "invalid-fpv",
		FirstSeen: base,
		LastSeen:  base.Add(time.Second),
	}
	if err := store.ArchiveFPVContext(ctx, invalid); err != nil {
		t.Fatalf("ArchiveFPVContext(invalid) error = %v", err)
	}

	target := model.ScreenFPVTarget{
		ID:         "fpv-1",
		Frequency:  5725.5,
		RSSI:       -48,
		SignalType: "O3",
		Valid:      false,
		EverValid:  true,
		DeviceSN:   " device-001 ",
		Format:     "binary",
		FirstSeen:  base,
		LastSeen:   base.Add(15 * time.Second),
		HitCount:   4,
		LastRecord: model.ScreenFPVLastRecord{
			Format:     "binary",
			ReceivedAt: base.Add(15 * time.Second),
			Frequency:  5725.5,
			RSSI:       -48,
			SignalType: "O3",
			Valid:      false,
			DeviceSN:   "device-001",
			Raw:        "fpv-raw",
		},
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := store.ArchiveFPVContext(ctx, target); err != nil {
			t.Fatalf("ArchiveFPVContext() attempt %d error = %v", attempt+1, err)
		}
	}

	items, err := store.List(ctx, QueryOptions{TargetType: model.IntrusionTargetTypeFPV, Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("FPV records = %d, want 1", len(items))
	}
	got := items[0]
	if got.ID != intrusionRecordID(model.IntrusionTargetTypeFPV, target.ID, target.FirstSeen) || got.TargetID != "fpv-1" {
		t.Fatalf("FPV identity = %#v", got)
	}
	if got.TargetType != model.IntrusionTargetTypeFPV || got.SignalType != "O3" || got.DeviceSN != "device-001" || got.Format != "binary" {
		t.Fatalf("FPV summary = %#v", got)
	}
	if !got.Valid || got.DurationSeconds != 15 || got.HitCount != 4 {
		t.Fatalf("FPV state = %#v", got)
	}
	if got.DeviceLocation == nil || got.DeviceLocation.Point == nil {
		t.Fatalf("FPV device location = %#v", got.DeviceLocation)
	}
	if got.FPVLastRecord == nil || got.FPVLastRecord.Raw != "fpv-raw" {
		t.Fatalf("FPV last record = %#v", got.FPVLastRecord)
	}
	if got.LastRecord != nil {
		t.Fatalf("FPV positioning last record = %#v, want nil", got.LastRecord)
	}
	fpvJSON := intrusionRecordJSONFields(t, got)
	if _, ok := fpvJSON["lastRecord"]; ok {
		t.Fatalf("FPV JSON = %s, unexpectedly contains lastRecord", mustMarshalJSON(t, got))
	}
	if _, ok := fpvJSON["fpvLastRecord"]; !ok {
		t.Fatalf("FPV JSON = %s, missing fpvLastRecord", mustMarshalJSON(t, got))
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

func TestStoreListFiltersFPVRecords(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	records := []model.IntrusionRecord{
		{ID: "match", TargetID: "match-target", TargetType: model.IntrusionTargetTypeFPV, SignalType: "DJI O3", DeviceSN: "FPV-SN-2", FirstSeen: now, LastSeen: now, ArchivedAt: now},
		{ID: "other-signal", TargetID: "other-signal-target", TargetType: model.IntrusionTargetTypeFPV, SignalType: "DJI O2", DeviceSN: "FPV-SN-2", FirstSeen: now, LastSeen: now, ArchivedAt: now},
		{ID: "other-sn", TargetID: "other-sn-target", TargetType: model.IntrusionTargetTypeFPV, SignalType: "DJI O3", DeviceSN: "FPV-SN-1", FirstSeen: now, LastSeen: now, ArchivedAt: now},
		{ID: "position", TargetID: "position-target", TargetType: model.IntrusionTargetTypePosition, SignalType: "DJI O3", DeviceSN: "FPV-SN-2", FirstSeen: now, LastSeen: now, ArchivedAt: now},
	}
	for _, record := range records {
		if err := store.insert(ctx, record); err != nil {
			t.Fatalf("insert %s: %v", record.ID, err)
		}
	}

	items, err := store.List(ctx, QueryOptions{
		Limit:      10,
		TargetType: model.IntrusionTargetTypeFPV,
		SignalType: "o3",
		DeviceSN:   "sn-2",
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0].ID != "match" {
		t.Fatalf("filtered FPV records = %#v", items)
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
	if targetType, err := ParseTargetType("fpv"); err != nil || targetType != model.IntrusionTargetTypeFPV {
		t.Fatalf("ParseTargetType(fpv) = %q, %v", targetType, err)
	}
	if _, err := ParseTargetType("radar"); err == nil {
		t.Fatal("ParseTargetType(radar) error = nil, want error")
	}
}

func TestStoreMigratesLegacyDatabaseForFPV(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "intrusions.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	const legacySchema = `
	CREATE TABLE intrusion_records (
		id TEXT PRIMARY KEY,
		target_id TEXT NOT NULL,
		target_type TEXT NOT NULL,
		model TEXT NOT NULL DEFAULT '',
		serial TEXT NOT NULL DEFAULT '',
		device TEXT NOT NULL DEFAULT '',
		frequency REAL NOT NULL DEFAULT 0,
		rssi REAL NOT NULL DEFAULT 0,
		first_seen TEXT NOT NULL,
		last_seen TEXT NOT NULL,
		duration_seconds INTEGER NOT NULL DEFAULT 0,
		hit_count INTEGER NOT NULL DEFAULT 0,
		source TEXT NOT NULL DEFAULT '',
		sources_json TEXT,
		cracked INTEGER NOT NULL DEFAULT 0,
		device_location_json TEXT,
		drone_json TEXT,
		pilot_json TEXT,
		home_json TEXT,
		drone_trajectory_json TEXT,
		pilot_trajectory_json TEXT,
		pilot_distance_m REAL,
		drone_distance_m REAL,
		drone_direction_deg REAL,
		device_direction_deg REAL,
		height REAL,
		altitude REAL,
		speed REAL,
		last_record_json TEXT,
		archived_at TEXT NOT NULL,
		UNIQUE(target_type, target_id, first_seen)
	);`
	if _, err := db.ExecContext(ctx, legacySchema); err != nil {
		_ = db.Close()
		t.Fatalf("create legacy schema: %v", err)
	}
	legacyTime := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO intrusion_records (id, target_id, target_type, first_seen, last_seen, archived_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"legacy-position",
		"legacy-position-target",
		string(model.IntrusionTargetTypePosition),
		formatTime(legacyTime),
		formatTime(legacyTime),
		formatTime(legacyTime),
	); err != nil {
		_ = db.Close()
		t.Fatalf("insert legacy position record: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore() migration error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	rows, err := store.db.QueryContext(ctx, `PRAGMA table_info(intrusion_records)`)
	if err != nil {
		t.Fatalf("inspect migrated schema: %v", err)
	}
	columns := []string{}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			_ = rows.Close()
			t.Fatalf("scan migrated schema: %v", err)
		}
		columns = append(columns, name)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close migrated schema rows: %v", err)
	}
	for _, expected := range []string{"signal_type", "device_sn", "valid", "format", "fpv_last_record_json"} {
		if !slices.Contains(columns, expected) {
			t.Errorf("migrated columns = %v, missing %q", columns, expected)
		}
	}
	var indexName string
	if err := store.db.QueryRowContext(
		ctx,
		`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`,
		"idx_intrusion_records_target_type_last_seen",
	).Scan(&indexName); err != nil {
		t.Fatalf("load migrated target type index: %v", err)
	}

	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	if err := store.ArchiveFPVContext(ctx, model.ScreenFPVTarget{
		ID:         "migrated-fpv",
		SignalType: "O3",
		DeviceSN:   "FPV-SN",
		Format:     "binary",
		FirstSeen:  now,
		LastSeen:   now.Add(time.Second),
		EverValid:  true,
	}); err != nil {
		t.Fatalf("ArchiveFPVContext() after migration error = %v", err)
	}
	items, err := store.List(ctx, QueryOptions{TargetType: model.IntrusionTargetTypeFPV})
	if err != nil {
		t.Fatalf("List() after migration error = %v", err)
	}
	if len(items) != 1 || items[0].DeviceSN != "FPV-SN" {
		t.Fatalf("migrated FPV records = %#v", items)
	}
	positions, err := store.List(ctx, QueryOptions{TargetType: model.IntrusionTargetTypePosition})
	if err != nil {
		t.Fatalf("List(position) after migration error = %v", err)
	}
	if len(positions) != 1 || positions[0].ID != "legacy-position" {
		t.Fatalf("migrated position records = %#v", positions)
	}
	if positions[0].LastRecord != nil {
		t.Fatalf("legacy NULL last record = %#v, want nil", positions[0].LastRecord)
	}
	if _, err := store.db.ExecContext(
		ctx,
		`UPDATE intrusion_records SET last_record_json = '' WHERE id = ?`,
		"legacy-position",
	); err != nil {
		t.Fatalf("set legacy empty last record: %v", err)
	}
	positions, err = store.List(ctx, QueryOptions{TargetType: model.IntrusionTargetTypePosition})
	if err != nil {
		t.Fatalf("List(position with empty last record) error = %v", err)
	}
	if len(positions) != 1 || positions[0].LastRecord != nil {
		t.Fatalf("legacy empty last record = %#v, want nil", positions)
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

func intrusionRecordJSONFields(t *testing.T, record model.IntrusionRecord) map[string]json.RawMessage {
	t.Helper()
	data := mustMarshalJSON(t, record)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("unmarshal intrusion record JSON: %v", err)
	}
	return fields
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return data
}
