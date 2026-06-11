// Package fpvrecord persists FPV video recording records.
package fpvrecord

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dr600ab-net/internal/model"

	_ "modernc.org/sqlite"
)

const defaultQueryLimit = 200

// Store persists FPV video records in a local SQLite database.
type Store struct {
	db *sql.DB
}

// QueryOptions controls FPV video record listing.
type QueryOptions struct {
	Limit      int
	Offset     int
	SignalType string
	DeviceSN   string
	DateFrom   time.Time
	DateTo     time.Time
}

// DeleteResult describes deleted rows and file paths that should be removed.
type DeleteResult struct {
	Deleted   int64
	FilePaths []string
}

// NewStore opens and initializes an FPV video record SQLite database.
func NewStore(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return &Store{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create fpv video record data directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open fpv video record database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Store{db: db}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.init(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) init(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	const schema = `
CREATE TABLE IF NOT EXISTS fpv_video_records (
	id TEXT PRIMARY KEY,
	target_id TEXT NOT NULL DEFAULT '',
	frequency REAL NOT NULL DEFAULT 0,
	rssi REAL NOT NULL DEFAULT 0,
	signal_type TEXT NOT NULL DEFAULT '',
	device_sn TEXT NOT NULL DEFAULT '',
	started_at TEXT NOT NULL,
	ended_at TEXT NOT NULL,
	duration_seconds INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT '',
	file_name TEXT NOT NULL DEFAULT '',
	file_size_bytes INTEGER NOT NULL DEFAULT 0,
	error TEXT NOT NULL DEFAULT '',
	last_record_json TEXT,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_fpv_video_records_started_at ON fpv_video_records(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_fpv_video_records_device_sn ON fpv_video_records(device_sn);
CREATE INDEX IF NOT EXISTS idx_fpv_video_records_signal_type ON fpv_video_records(signal_type);
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("initialize fpv video record database: %w", err)
	}
	return nil
}

// Insert persists a single FPV video record.
func (s *Store) Insert(ctx context.Context, record model.FPVVideoRecord) error {
	if s == nil || s.db == nil || strings.TrimSpace(record.ID) == "" {
		return nil
	}
	if record.DurationSeconds < 0 {
		record.DurationSeconds = 0
	}
	if record.Status == "" {
		record.Status = model.FPVVideoRecordStatusFailed
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT OR REPLACE INTO fpv_video_records (
			id, target_id, frequency, rssi, signal_type, device_sn,
			started_at, ended_at, duration_seconds, status, file_name, file_size_bytes,
			error, last_record_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID,
		strings.TrimSpace(record.TargetID),
		record.Frequency,
		record.RSSI,
		strings.TrimSpace(record.SignalType),
		strings.TrimSpace(record.DeviceSN),
		formatTime(record.StartedAt),
		formatTime(record.EndedAt),
		record.DurationSeconds,
		string(record.Status),
		strings.TrimSpace(record.FileName),
		record.FileSizeBytes,
		strings.TrimSpace(record.Error),
		jsonString(record.LastRecord),
		formatTime(time.Now()),
	)
	if err != nil {
		return fmt.Errorf("insert fpv video record: %w", err)
	}
	return nil
}

// List returns FPV video records ordered by newest start time first.
func (s *Store) List(ctx context.Context, options QueryOptions) ([]model.FPVVideoRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	limit := options.Limit
	if limit <= 0 {
		limit = defaultQueryLimit
	}
	offset := options.Offset
	if offset < 0 {
		offset = 0
	}

	query := `SELECT id, target_id, frequency, rssi, signal_type, device_sn,
		started_at, ended_at, duration_seconds, status, file_name, file_size_bytes,
		error, last_record_json
		FROM fpv_video_records`
	args := []any{}
	conditions := []string{}
	if signalType := strings.TrimSpace(options.SignalType); signalType != "" {
		conditions = append(conditions, `lower(signal_type) LIKE ? ESCAPE '\'`)
		args = append(args, likePattern(signalType))
	}
	if deviceSN := strings.TrimSpace(options.DeviceSN); deviceSN != "" {
		conditions = append(conditions, `lower(device_sn) LIKE ? ESCAPE '\'`)
		args = append(args, likePattern(deviceSN))
	}
	if !options.DateFrom.IsZero() {
		conditions = append(conditions, `started_at >= ?`)
		args = append(args, formatTime(options.DateFrom))
	}
	if !options.DateTo.IsZero() {
		conditions = append(conditions, `started_at < ?`)
		args = append(args, formatTime(options.DateTo.AddDate(0, 0, 1)))
	}
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	query += ` ORDER BY started_at DESC, ended_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query fpv video records: %w", err)
	}
	defer rows.Close()

	records := make([]model.FPVVideoRecord, 0, limit)
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read fpv video records: %w", err)
	}
	return records, nil
}

// Get returns a single FPV video record by ID.
func (s *Store) Get(ctx context.Context, id string) (model.FPVVideoRecord, bool, error) {
	if s == nil || s.db == nil {
		return model.FPVVideoRecord{}, false, nil
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return model.FPVVideoRecord{}, false, nil
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, target_id, frequency, rssi, signal_type, device_sn,
			started_at, ended_at, duration_seconds, status, file_name, file_size_bytes,
			error, last_record_json
			FROM fpv_video_records WHERE id = ?`,
		id,
	)
	if err != nil {
		return model.FPVVideoRecord{}, false, fmt.Errorf("query fpv video record: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return model.FPVVideoRecord{}, false, nil
	}
	record, err := scanRecord(rows)
	if err != nil {
		return model.FPVVideoRecord{}, false, err
	}
	if err := rows.Err(); err != nil {
		return model.FPVVideoRecord{}, false, fmt.Errorf("read fpv video record: %w", err)
	}
	return record, true, nil
}

// Delete removes FPV video records and returns local file paths that should be removed.
func (s *Store) Delete(ctx context.Context, ids []string, recordDir string) (DeleteResult, error) {
	if s == nil || s.db == nil {
		return DeleteResult{}, nil
	}
	ids = normalizeIDs(ids)
	if len(ids) == 0 {
		return DeleteResult{}, errors.New("empty fpv video record ids")
	}

	placeholders := placeholders(len(ids))
	args := make([]any, len(ids))
	for index, id := range ids {
		args[index] = id
	}
	query := `SELECT file_name FROM fpv_video_records WHERE id IN (` + placeholders + `)`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return DeleteResult{}, fmt.Errorf("query fpv video record files: %w", err)
	}
	filePaths := []string{}
	for rows.Next() {
		var fileName string
		if err := rows.Scan(&fileName); err != nil {
			_ = rows.Close()
			return DeleteResult{}, fmt.Errorf("scan fpv video record file: %w", err)
		}
		if path, ok := safeRecordPath(recordDir, fileName); ok {
			filePaths = append(filePaths, path)
		}
	}
	if err := rows.Close(); err != nil {
		return DeleteResult{}, fmt.Errorf("close fpv video record file rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return DeleteResult{}, fmt.Errorf("read fpv video record files: %w", err)
	}

	result, err := s.db.ExecContext(
		ctx,
		`DELETE FROM fpv_video_records WHERE id IN (`+placeholders+`)`,
		args...,
	)
	if err != nil {
		return DeleteResult{}, fmt.Errorf("delete fpv video records: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return DeleteResult{}, fmt.Errorf("read deleted fpv video record count: %w", err)
	}
	return DeleteResult{
		Deleted:   deleted,
		FilePaths: filePaths,
	}, nil
}

// SafeRecordPath resolves a file name under the configured record directory.
func SafeRecordPath(recordDir string, fileName string) (string, bool) {
	return safeRecordPath(recordDir, fileName)
}

func scanRecord(rows *sql.Rows) (model.FPVVideoRecord, error) {
	var record model.FPVVideoRecord
	var startedAt, endedAt string
	var status string
	var lastRecordJSON sql.NullString

	err := rows.Scan(
		&record.ID,
		&record.TargetID,
		&record.Frequency,
		&record.RSSI,
		&record.SignalType,
		&record.DeviceSN,
		&startedAt,
		&endedAt,
		&record.DurationSeconds,
		&status,
		&record.FileName,
		&record.FileSizeBytes,
		&record.Error,
		&lastRecordJSON,
	)
	if err != nil {
		return model.FPVVideoRecord{}, fmt.Errorf("scan fpv video record: %w", err)
	}
	record.StartedAt = parseStoredTime(startedAt)
	record.EndedAt = parseStoredTime(endedAt)
	record.Status = model.FPVVideoRecordStatus(status)
	record.LastRecord = decodeJSONValue[model.ScreenFPVLastRecord](lastRecordJSON)
	return record, nil
}

func safeRecordPath(recordDir string, fileName string) (string, bool) {
	recordDir = strings.TrimSpace(recordDir)
	fileName = strings.TrimSpace(fileName)
	if recordDir == "" || fileName == "" || filepath.IsAbs(fileName) || strings.Contains(fileName, string(filepath.Separator)) {
		return "", false
	}
	cleanDir := filepath.Clean(recordDir)
	path := filepath.Join(cleanDir, fileName)
	if filepath.Dir(path) != cleanDir {
		return "", false
	}
	return path, true
}

func normalizeIDs(ids []string) []string {
	normalized := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	return normalized
}

func placeholders(count int) string {
	items := make([]string, count)
	for index := range items {
		items[index] = "?"
	}
	return strings.Join(items, ",")
}

func likePattern(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return "%" + value + "%"
}

func jsonString(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func decodeJSONValue[T any](value sql.NullString) T {
	var out T
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return out
	}
	_ = json.Unmarshal([]byte(value.String), &out)
	return out
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseStoredTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
