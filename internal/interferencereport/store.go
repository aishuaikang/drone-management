// Package interferencereport persists interference operation reports.
package interferencereport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"dr600ab-net/internal/model"

	_ "modernc.org/sqlite"
)

const defaultQueryLimit = 200

var (
	defaultInterferenceBandLabelsByID, defaultInterferenceBandLabelsByGPIO = defaultInterferenceBandMaps()
)

// QueryOptions controls interference report listing.
type QueryOptions struct {
	Limit  int
	Offset int
	Status model.InterferenceReportStatus
}

// Store persists interference reports in a local SQLite database.
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

// NewStore opens and initializes an interference report SQLite database.
func NewStore(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return &Store{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create interference report data directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open interference report database: %w", err)
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
CREATE TABLE IF NOT EXISTS interference_reports (
	id TEXT PRIMARY KEY,
	status TEXT NOT NULL,
	started_at TEXT NOT NULL,
	ended_at TEXT,
	duration_seconds INTEGER NOT NULL DEFAULT 0,
	requested_duration_seconds INTEGER NOT NULL DEFAULT 0,
	channel_ids_json TEXT,
	channel_labels_json TEXT,
	channel_pins_json TEXT,
	summary TEXT NOT NULL DEFAULT '',
	last_error TEXT NOT NULL DEFAULT '',
	abnormal_reason TEXT NOT NULL DEFAULT '',
	request_json TEXT,
	start_state_json TEXT,
	end_state_json TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_interference_reports_status_started_at ON interference_reports(status, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_interference_reports_started_at ON interference_reports(started_at DESC);
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("initialize interference report database: %w", err)
	}
	return nil
}

// Create inserts a new interference report.
func (s *Store) Create(report model.InterferenceReport) (model.InterferenceReport, error) {
	if s == nil || s.db == nil {
		return report, nil
	}
	now := time.Now().UTC()
	if strings.TrimSpace(report.ID) == "" {
		report.ID = newReportID()
	}
	if report.Status == "" {
		report.Status = model.InterferenceReportStatusRunning
	}
	if report.StartedAt.IsZero() {
		report.StartedAt = now
	}
	if report.CreatedAt.IsZero() {
		report.CreatedAt = now
	}
	report.UpdatedAt = now
	report.DurationSeconds = reportDuration(report.StartedAt, report.EndedAt)
	report = normalizeReportSummary(report)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.insert(report); err != nil {
		return model.InterferenceReport{}, err
	}
	return report, nil
}

// CreateRunning inserts a newly started interference report.
func (s *Store) CreateRunning(report model.InterferenceReport) (model.InterferenceReport, error) {
	report.Status = model.InterferenceReportStatusRunning
	return s.Create(report)
}

// Update replaces an existing report with the supplied state.
func (s *Store) Update(report model.InterferenceReport) error {
	if s == nil || s.db == nil || strings.TrimSpace(report.ID) == "" {
		return nil
	}
	report.UpdatedAt = time.Now().UTC()
	report.DurationSeconds = reportDuration(report.StartedAt, report.EndedAt)
	report = normalizeReportSummary(report)

	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec(
		`UPDATE interference_reports SET
			status = ?, started_at = ?, ended_at = ?, duration_seconds = ?, requested_duration_seconds = ?,
			channel_ids_json = ?, channel_labels_json = ?, channel_pins_json = ?, summary = ?, last_error = ?,
			abnormal_reason = ?, request_json = ?, start_state_json = ?, end_state_json = ?, updated_at = ?
		WHERE id = ?`,
		string(report.Status),
		formatTime(report.StartedAt),
		nullableTime(report.EndedAt),
		report.DurationSeconds,
		report.RequestedDurationSeconds,
		jsonString(report.ChannelIDs),
		jsonString(report.ChannelLabels),
		jsonString(report.ChannelPins),
		report.Summary,
		report.LastError,
		report.AbnormalReason,
		jsonString(report.Request),
		jsonString(report.StartState),
		jsonString(report.EndState),
		formatTime(report.UpdatedAt),
		report.ID,
	)
	if err != nil {
		return fmt.Errorf("update interference report: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read interference report update count: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) insert(report model.InterferenceReport) error {
	_, err := s.db.Exec(
		`INSERT INTO interference_reports (
			id, status, started_at, ended_at, duration_seconds, requested_duration_seconds,
			channel_ids_json, channel_labels_json, channel_pins_json, summary, last_error,
			abnormal_reason, request_json, start_state_json, end_state_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		report.ID,
		string(report.Status),
		formatTime(report.StartedAt),
		nullableTime(report.EndedAt),
		report.DurationSeconds,
		report.RequestedDurationSeconds,
		jsonString(report.ChannelIDs),
		jsonString(report.ChannelLabels),
		jsonString(report.ChannelPins),
		report.Summary,
		report.LastError,
		report.AbnormalReason,
		jsonString(report.Request),
		jsonString(report.StartState),
		jsonString(report.EndState),
		formatTime(report.CreatedAt),
		formatTime(report.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert interference report: %w", err)
	}
	return nil
}

// List returns report summaries ordered by newest start time first.
func (s *Store) List(ctx context.Context, options QueryOptions) ([]model.InterferenceReportSummary, error) {
	if s == nil || s.db == nil {
		return []model.InterferenceReportSummary{}, nil
	}
	limit := options.Limit
	if limit <= 0 {
		limit = defaultQueryLimit
	}
	offset := options.Offset
	if offset < 0 {
		offset = 0
	}
	args := []any{}
	query := `SELECT
		id, status, started_at, ended_at, duration_seconds, requested_duration_seconds,
		channel_ids_json, channel_labels_json, channel_pins_json, summary, last_error,
		abnormal_reason, created_at, updated_at
		FROM interference_reports`
	if options.Status != "" {
		query += ` WHERE status = ?`
		args = append(args, string(options.Status))
	}
	query += ` ORDER BY started_at DESC, created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query interference reports: %w", err)
	}
	defer rows.Close()

	items := make([]model.InterferenceReportSummary, 0, limit)
	for rows.Next() {
		item, err := scanSummary(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read interference reports: %w", err)
	}
	return items, nil
}

// Get returns a full interference report by ID.
func (s *Store) Get(ctx context.Context, id string) (model.InterferenceReport, bool, error) {
	if s == nil || s.db == nil {
		return model.InterferenceReport{}, false, nil
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return model.InterferenceReport{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT
		id, status, started_at, ended_at, duration_seconds, requested_duration_seconds,
		channel_ids_json, channel_labels_json, channel_pins_json, summary, last_error,
		abnormal_reason, request_json, start_state_json, end_state_json, created_at, updated_at
		FROM interference_reports WHERE id = ?`, id)
	report, err := scanReport(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return model.InterferenceReport{}, false, nil
		}
		return model.InterferenceReport{}, false, err
	}
	return report, true, nil
}

// DeleteFailed deletes a failed interference report.
func (s *Store) DeleteFailed(ctx context.Context, id string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return 0, ErrNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var status string
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM interference_reports WHERE id = ?`, id).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	if model.InterferenceReportStatus(status) != model.InterferenceReportStatusFailed {
		return 0, ErrNotFailed
	}

	result, err := s.db.ExecContext(ctx, `DELETE FROM interference_reports WHERE id = ? AND status = ?`, id, string(model.InterferenceReportStatusFailed))
	if err != nil {
		return 0, fmt.Errorf("delete interference report: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read interference report delete count: %w", err)
	}
	return deleted, nil
}

// CloseRunning marks all still-running reports as abnormal.
func (s *Store) CloseRunning(reason string, now time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "abnormal"
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec(
		`UPDATE interference_reports SET
			status = ?, ended_at = COALESCE(ended_at, ?),
			duration_seconds = CAST((julianday(COALESCE(ended_at, ?)) - julianday(started_at)) * 86400 AS INTEGER),
			abnormal_reason = CASE WHEN abnormal_reason = '' THEN ? ELSE abnormal_reason END,
			last_error = CASE WHEN last_error = '' THEN ? ELSE last_error END,
			updated_at = ?
		WHERE status = ?`,
		string(model.InterferenceReportStatusAbnormal),
		formatTime(now),
		formatTime(now),
		reason,
		reason,
		formatTime(now),
		string(model.InterferenceReportStatusRunning),
	)
	if err != nil {
		return 0, fmt.Errorf("close running interference reports: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read close running interference report count: %w", err)
	}
	return affected, nil
}

// ErrNotFound is returned when a report does not exist.
var ErrNotFound = errors.New("interference report not found")

// ErrNotFailed is returned when deleting a report that is not failed.
var ErrNotFailed = errors.New("interference report is not failed")

func scanSummary(rows *sql.Rows) (model.InterferenceReportSummary, error) {
	var summary model.InterferenceReportSummary
	var status, startedAt, createdAt, updatedAt string
	var endedAt sql.NullString
	var channelIDsJSON, channelLabelsJSON, channelPinsJSON sql.NullString
	err := rows.Scan(
		&summary.ID,
		&status,
		&startedAt,
		&endedAt,
		&summary.DurationSeconds,
		&summary.RequestedDurationSeconds,
		&channelIDsJSON,
		&channelLabelsJSON,
		&channelPinsJSON,
		&summary.Summary,
		&summary.LastError,
		&summary.AbnormalReason,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return model.InterferenceReportSummary{}, fmt.Errorf("scan interference report summary: %w", err)
	}
	fillSummary(&summary, status, startedAt, endedAt, createdAt, updatedAt, channelIDsJSON, channelLabelsJSON, channelPinsJSON)
	return summary, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanReport(scanner rowScanner) (model.InterferenceReport, error) {
	var report model.InterferenceReport
	var status, startedAt, createdAt, updatedAt string
	var endedAt sql.NullString
	var channelIDsJSON, channelLabelsJSON, channelPinsJSON sql.NullString
	var requestJSON, startStateJSON, endStateJSON sql.NullString
	err := scanner.Scan(
		&report.ID,
		&status,
		&startedAt,
		&endedAt,
		&report.DurationSeconds,
		&report.RequestedDurationSeconds,
		&channelIDsJSON,
		&channelLabelsJSON,
		&channelPinsJSON,
		&report.Summary,
		&report.LastError,
		&report.AbnormalReason,
		&requestJSON,
		&startStateJSON,
		&endStateJSON,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.InterferenceReport{}, ErrNotFound
		}
		return model.InterferenceReport{}, fmt.Errorf("scan interference report: %w", err)
	}
	fillSummary(&report.InterferenceReportSummary, status, startedAt, endedAt, createdAt, updatedAt, channelIDsJSON, channelLabelsJSON, channelPinsJSON)
	report.Request = decodeJSONValue[model.ScreenStrikeRequest](requestJSON)
	report.StartState = decodeJSONPtr[model.ScreenStrikeState](startStateJSON)
	report.EndState = decodeJSONPtr[model.ScreenStrikeState](endStateJSON)
	report = normalizeReportSummary(report)
	return report, nil
}

func fillSummary(
	summary *model.InterferenceReportSummary,
	status string,
	startedAt string,
	endedAt sql.NullString,
	createdAt string,
	updatedAt string,
	channelIDsJSON sql.NullString,
	channelLabelsJSON sql.NullString,
	channelPinsJSON sql.NullString,
) {
	summary.Status = model.InterferenceReportStatus(status)
	summary.StartedAt = parseStoredTime(startedAt)
	summary.EndedAt = parseStoredTimePtr(endedAt)
	summary.CreatedAt = parseStoredTime(createdAt)
	summary.UpdatedAt = parseStoredTime(updatedAt)
	summary.ChannelIDs = decodeJSONSlice[string](channelIDsJSON)
	summary.ChannelLabels = decodeJSONSlice[string](channelLabelsJSON)
	summary.ChannelPins = decodeJSONSlice[int](channelPinsJSON)
	summary.ChannelLabels = normalizeInterferenceBandLabels(summary.ChannelIDs, summary.ChannelLabels)
}

func normalizeReportSummary(report model.InterferenceReport) model.InterferenceReport {
	if report.RequestedDurationSeconds == 0 {
		report.RequestedDurationSeconds = report.Request.DurationSeconds
	}
	if len(report.ChannelIDs) == 0 {
		report.ChannelIDs = append([]string{}, report.Request.ChannelIDs...)
	}
	if report.StartState != nil {
		if report.RequestedDurationSeconds == 0 {
			report.RequestedDurationSeconds = report.StartState.DurationSeconds
		}
		if len(report.ChannelIDs) == 0 {
			report.ChannelIDs = append([]string{}, report.StartState.ChannelIDs...)
		}
		if len(report.ChannelLabels) == 0 || len(report.ChannelPins) == 0 {
			labels, pins := strikeChannelMetadata(report.StartState.Channels, report.ChannelIDs)
			if len(report.ChannelLabels) == 0 {
				report.ChannelLabels = labels
			}
			if len(report.ChannelPins) == 0 {
				report.ChannelPins = pins
			}
		}
	}
	report.ChannelLabels = normalizeInterferenceBandLabels(report.ChannelIDs, report.ChannelLabels)
	return report
}

func strikeChannelMetadata(channels []model.GpioChannel, ids []string) ([]string, []int) {
	if len(ids) == 0 {
		return []string{}, []int{}
	}
	byID := make(map[string]model.GpioChannel, len(channels))
	for _, channel := range channels {
		byID[channel.ID] = channel
	}
	labels := make([]string, 0, len(ids))
	pins := make([]int, 0, len(ids))
	for _, id := range ids {
		channel, ok := byID[id]
		if !ok {
			continue
		}
		label := formatStrikeBands(channel.Bands)
		if label == "" {
			label = defaultInterferenceBandLabel(id, channel.Label)
		}
		labels = append(labels, label)
		pins = append(pins, channel.Pin)
	}
	return labels, pins
}

func normalizeInterferenceBandLabels(ids []string, labels []string) []string {
	if len(ids) == 0 && len(labels) == 0 {
		return []string{}
	}

	count := len(labels)
	if len(ids) > count {
		count = len(ids)
	}
	normalized := make([]string, 0, count)
	for index := range count {
		id := ""
		if index < len(ids) {
			id = ids[index]
		}
		label := ""
		if index < len(labels) {
			label = strings.TrimSpace(labels[index])
		}
		if mapped := defaultInterferenceBandLabel(id, label); mapped != "" {
			normalized = append(normalized, mapped)
			continue
		}
		if label != "" {
			normalized = append(normalized, label)
			continue
		}
		if id != "" {
			normalized = append(normalized, id)
		}
	}
	if len(normalized) == 0 {
		return []string{}
	}
	return normalized
}

func defaultInterferenceBandLabel(id string, label string) string {
	label = strings.TrimSpace(label)
	if label != "" {
		return defaultInterferenceBandLabelsByGPIO[label]
	}
	return defaultInterferenceBandLabelsByID[strings.TrimSpace(id)]
}

func defaultInterferenceBandMaps() (map[string]string, map[string]string) {
	byID := map[string]string{
		"io1": "433M/800M/900M/1.4G",
		"io2": "1.2G/1.5G",
		"io3": "2.4G/5.2G/5.8G",
	}
	byGPIO := map[string]string{
		"IO2":    byID["io1"],
		"IO3":    byID["io2"],
		"IO1":    byID["io3"],
		"IOC4":   byID["io1"],
		"IOC2":   byID["io2"],
		"IOC3":   byID["io3"],
		"GPIO20": byID["io1"],
		"GPIO18": byID["io2"],
		"GPIO19": byID["io3"],
	}
	return byID, byGPIO
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

func newReportID() string {
	var token [8]byte
	if _, err := rand.Read(token[:]); err == nil {
		return "interference-" + strings.ToUpper(hex.EncodeToString(token[:]))
	}
	return fmt.Sprintf("interference-%d", time.Now().UnixNano())
}

func reportDuration(startedAt time.Time, endedAt *time.Time) int64 {
	if startedAt.IsZero() || endedAt == nil || endedAt.Before(startedAt) {
		return 0
	}
	return int64(endedAt.Sub(startedAt).Seconds())
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func nullableTime(value *time.Time) sql.NullString {
	if value == nil || value.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(*value), Valid: true}
}

func parseStoredTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func parseStoredTimePtr(value sql.NullString) *time.Time {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	parsed := parseStoredTime(value.String)
	if parsed.IsZero() {
		return nil
	}
	return &parsed
}

func jsonString(value any) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	data, err := json.Marshal(value)
	if err != nil || string(data) == "null" {
		return sql.NullString{}
	}
	return sql.NullString{String: string(data), Valid: true}
}

func decodeJSONValue[T any](raw sql.NullString) T {
	var value T
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return value
	}
	_ = json.Unmarshal([]byte(raw.String), &value)
	return value
}

func decodeJSONPtr[T any](raw sql.NullString) *T {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	var value T
	if err := json.Unmarshal([]byte(raw.String), &value); err != nil {
		return nil
	}
	return &value
}

func decodeJSONSlice[T any](raw sql.NullString) []T {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return []T{}
	}
	var value []T
	if err := json.Unmarshal([]byte(raw.String), &value); err != nil {
		return []T{}
	}
	if value == nil {
		return []T{}
	}
	return value
}

// ParseStatus validates a public interference report status query value.
func ParseStatus(value string) (model.InterferenceReportStatus, error) {
	switch status := model.InterferenceReportStatus(strings.TrimSpace(value)); status {
	case "", model.InterferenceReportStatusRunning, model.InterferenceReportStatusCompleted,
		model.InterferenceReportStatusFailed, model.InterferenceReportStatusAbnormal:
		return status, nil
	default:
		return "", errors.New("invalid interference report status")
	}
}
