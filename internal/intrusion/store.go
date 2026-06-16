// Package intrusion persists disappeared positioning targets.
package intrusion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"drone-management/internal/model"

	_ "modernc.org/sqlite"
)

const defaultQueryLimit = 200

// Store persists intrusion records in a local SQLite database.
type Store struct {
	db *sql.DB

	mu                     sync.RWMutex
	deviceLocationProvider func() model.ScreenDeviceLocationResponse
}

// QueryOptions controls intrusion record listing.
type QueryOptions struct {
	Limit      int
	Offset     int
	TargetType model.IntrusionTargetType
	Model      string
	Serial     string
	DateFrom   time.Time
	DateTo     time.Time
}

// NewStore opens and initializes an intrusion SQLite database.
func NewStore(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return &Store{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create intrusion data directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open intrusion database: %w", err)
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

// SetDeviceLocationProvider sets the source used to stamp archived records.
func (s *Store) SetDeviceLocationProvider(provider func() model.ScreenDeviceLocationResponse) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deviceLocationProvider = provider
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
CREATE TABLE IF NOT EXISTS intrusion_records (
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
);
CREATE INDEX IF NOT EXISTS idx_intrusion_records_last_seen ON intrusion_records(last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_intrusion_records_archived_at ON intrusion_records(archived_at);
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("initialize intrusion database: %w", err)
	}
	return nil
}

// ArchivePosition persists an expired positioning target.
func (s *Store) ArchivePosition(target model.ScreenPositionTarget) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.ArchivePositionContext(ctx, target)
}

// ArchivePositionContext persists an expired positioning target.
func (s *Store) ArchivePositionContext(ctx context.Context, target model.ScreenPositionTarget) error {
	if s == nil || s.db == nil || strings.TrimSpace(target.ID) == "" {
		return nil
	}
	if isUncrackedDJIDrone(target) {
		return nil
	}
	record := s.recordFromPosition(target)
	if record.FirstSeen.IsZero() || record.LastSeen.IsZero() {
		return nil
	}
	return s.insert(ctx, record)
}

func (s *Store) recordFromPosition(target model.ScreenPositionTarget) model.IntrusionRecord {
	deviceLocation := s.currentDeviceLocation()
	record := model.IntrusionRecord{
		ID:                intrusionRecordID(model.IntrusionTargetTypePosition, target.ID, target.FirstSeen),
		TargetID:          strings.TrimSpace(target.ID),
		TargetType:        model.IntrusionTargetTypePosition,
		Model:             strings.TrimSpace(target.Model),
		Serial:            strings.TrimSpace(target.Serial),
		Device:            strings.TrimSpace(target.Device),
		Frequency:         target.Frequency,
		RSSI:              target.RSSI,
		FirstSeen:         target.FirstSeen,
		LastSeen:          target.LastSeen,
		DurationSeconds:   durationSeconds(target.FirstSeen, target.LastSeen),
		HitCount:          target.HitCount,
		Source:            strings.TrimSpace(target.Source),
		Sources:           normalizeSources(target.Sources, target.Source),
		Cracked:           target.Cracked,
		DeviceLocation:    deviceLocation,
		Drone:             clonePoint(target.Drone),
		Pilot:             clonePoint(target.Pilot),
		Home:              clonePoint(target.Home),
		DroneTrajectory:   archivedTrajectory(target.FullDroneTrajectory, target.DroneTrajectory),
		PilotTrajectory:   archivedTrajectory(target.FullPilotTrajectory, target.PilotTrajectory),
		Height:            cloneFloat(target.Height),
		Altitude:          cloneFloat(target.Altitude),
		Speed:             cloneFloat(target.Speed),
		LastRecord:        target.LastRecord,
		ArchivedAt:        time.Now(),
		PilotDistanceM:    cloneFloat(target.PilotDistanceM),
		DroneDistanceM:    cloneFloat(target.DroneDistanceM),
		DroneDirectionDeg: cloneFloat(target.DroneDirectionDeg),
	}
	applyDeviceRelations(&record)
	return record
}

func (s *Store) currentDeviceLocation() *model.ScreenDeviceLocationResponse {
	s.mu.RLock()
	provider := s.deviceLocationProvider
	s.mu.RUnlock()
	if provider == nil {
		return nil
	}
	location := provider()
	if !validGeoPoint(location.Point) || !location.Valid {
		return nil
	}
	return cloneDeviceLocation(location)
}

func applyDeviceRelations(record *model.IntrusionRecord) {
	if record == nil || record.DeviceLocation == nil || !validGeoPoint(record.DeviceLocation.Point) {
		return
	}
	device := model.ScreenPositionPoint{
		Latitude:  record.DeviceLocation.Point.Latitude,
		Longitude: record.DeviceLocation.Point.Longitude,
	}
	if record.Pilot != nil {
		distance := distanceMeters(device, *record.Pilot)
		direction := bearingDegrees(device, *record.Pilot)
		deviceDirection := bearingDegrees(*record.Pilot, device)
		record.PilotDistanceM = &distance
		if record.DroneDirectionDeg == nil {
			record.DroneDirectionDeg = &direction
		}
		record.DeviceDirectionDeg = &deviceDirection
	}
	if record.Drone != nil {
		distance := distanceMeters(device, *record.Drone)
		direction := bearingDegrees(device, *record.Drone)
		record.DroneDistanceM = &distance
		record.DroneDirectionDeg = &direction
	}
}

func (s *Store) insert(ctx context.Context, record model.IntrusionRecord) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO intrusion_records (
			id, target_id, target_type, model, serial, device, frequency, rssi,
			first_seen, last_seen, duration_seconds, hit_count, source, sources_json, cracked,
			device_location_json, drone_json, pilot_json, home_json, drone_trajectory_json, pilot_trajectory_json,
			pilot_distance_m, drone_distance_m, drone_direction_deg, device_direction_deg,
			height, altitude, speed, last_record_json, archived_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID,
		record.TargetID,
		string(record.TargetType),
		record.Model,
		record.Serial,
		record.Device,
		record.Frequency,
		record.RSSI,
		formatTime(record.FirstSeen),
		formatTime(record.LastSeen),
		record.DurationSeconds,
		record.HitCount,
		record.Source,
		jsonString(record.Sources),
		boolInt(record.Cracked),
		jsonString(record.DeviceLocation),
		jsonString(record.Drone),
		jsonString(record.Pilot),
		jsonString(record.Home),
		jsonString(record.DroneTrajectory),
		jsonString(record.PilotTrajectory),
		nullableFloat(record.PilotDistanceM),
		nullableFloat(record.DroneDistanceM),
		nullableFloat(record.DroneDirectionDeg),
		nullableFloat(record.DeviceDirectionDeg),
		nullableFloat(record.Height),
		nullableFloat(record.Altitude),
		nullableFloat(record.Speed),
		jsonString(record.LastRecord),
		formatTime(record.ArchivedAt),
	)
	if err != nil {
		return fmt.Errorf("insert intrusion record: %w", err)
	}
	return nil
}

// List returns intrusion records ordered by latest disappearance first.
func (s *Store) List(ctx context.Context, options QueryOptions) ([]model.IntrusionRecord, error) {
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

	query := `SELECT id, target_id, target_type, model, serial, device, frequency, rssi,
		first_seen, last_seen, duration_seconds, hit_count, source, sources_json, cracked,
		device_location_json, drone_json, pilot_json, home_json, drone_trajectory_json, pilot_trajectory_json,
		pilot_distance_m, drone_distance_m, drone_direction_deg, device_direction_deg,
		height, altitude, speed, last_record_json, archived_at
		FROM intrusion_records`
	args := []any{}
	conditions := []string{`NOT (lower(model) = ? AND cracked = 0)`}
	args = append(args, strings.ToLower("DJI-Drone"))
	if options.TargetType != "" {
		conditions = append(conditions, `target_type = ?`)
		args = append(args, string(options.TargetType))
	}
	if modelQuery := strings.TrimSpace(options.Model); modelQuery != "" {
		conditions = append(conditions, `lower(model) LIKE ? ESCAPE '\'`)
		args = append(args, likePattern(modelQuery))
	}
	if serialQuery := strings.TrimSpace(options.Serial); serialQuery != "" {
		conditions = append(conditions, `lower(serial) LIKE ? ESCAPE '\'`)
		args = append(args, likePattern(serialQuery))
	}
	if !options.DateFrom.IsZero() {
		conditions = append(conditions, `last_seen >= ?`)
		args = append(args, formatTime(options.DateFrom))
	}
	if !options.DateTo.IsZero() {
		conditions = append(conditions, `first_seen < ?`)
		args = append(args, formatTime(options.DateTo.AddDate(0, 0, 1)))
	}
	query += ` WHERE ` + strings.Join(conditions, ` AND `)
	query += ` ORDER BY last_seen DESC, archived_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query intrusion records: %w", err)
	}
	defer rows.Close()

	records := make([]model.IntrusionRecord, 0, limit)
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read intrusion records: %w", err)
	}
	return records, nil
}

// Delete removes intrusion records by ID and returns the number of deleted rows.
func (s *Store) Delete(ctx context.Context, ids []string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	ids = normalizeIDs(ids)
	if len(ids) == 0 {
		return 0, errors.New("empty intrusion record ids")
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for index, id := range ids {
		placeholders[index] = "?"
		args[index] = id
	}
	result, err := s.db.ExecContext(
		ctx,
		`DELETE FROM intrusion_records WHERE id IN (`+strings.Join(placeholders, ",")+`)`,
		args...,
	)
	if err != nil {
		return 0, fmt.Errorf("delete intrusion records: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read deleted intrusion count: %w", err)
	}
	return deleted, nil
}

// PruneRetention removes records older than the configured retention days. 0 means keep forever.
func (s *Store) PruneRetention(ctx context.Context, days int, now time.Time) (int64, error) {
	if s == nil || s.db == nil || days <= 0 {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.AddDate(0, 0, -days)
	result, err := s.db.ExecContext(ctx, `DELETE FROM intrusion_records WHERE archived_at < ?`, formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("prune intrusion records: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read pruned intrusion count: %w", err)
	}
	return deleted, nil
}

func scanRecord(rows *sql.Rows) (model.IntrusionRecord, error) {
	var record model.IntrusionRecord
	var targetType string
	var cracked int
	var firstSeen, lastSeen, archivedAt string
	var sourcesJSON sql.NullString
	var deviceLocationJSON sql.NullString
	var droneJSON, pilotJSON, homeJSON sql.NullString
	var droneTrajectoryJSON, pilotTrajectoryJSON sql.NullString
	var pilotDistanceM, droneDistanceM, droneDirectionDeg, deviceDirectionDeg sql.NullFloat64
	var height, altitude, speed sql.NullFloat64
	var lastRecordJSON sql.NullString

	err := rows.Scan(
		&record.ID,
		&record.TargetID,
		&targetType,
		&record.Model,
		&record.Serial,
		&record.Device,
		&record.Frequency,
		&record.RSSI,
		&firstSeen,
		&lastSeen,
		&record.DurationSeconds,
		&record.HitCount,
		&record.Source,
		&sourcesJSON,
		&cracked,
		&deviceLocationJSON,
		&droneJSON,
		&pilotJSON,
		&homeJSON,
		&droneTrajectoryJSON,
		&pilotTrajectoryJSON,
		&pilotDistanceM,
		&droneDistanceM,
		&droneDirectionDeg,
		&deviceDirectionDeg,
		&height,
		&altitude,
		&speed,
		&lastRecordJSON,
		&archivedAt,
	)
	if err != nil {
		return model.IntrusionRecord{}, fmt.Errorf("scan intrusion record: %w", err)
	}

	record.TargetType = model.IntrusionTargetType(targetType)
	record.Cracked = cracked != 0
	record.FirstSeen = parseStoredTime(firstSeen)
	record.LastSeen = parseStoredTime(lastSeen)
	record.ArchivedAt = parseStoredTime(archivedAt)
	record.Sources = normalizeSources(decodeJSONSlice[string](sourcesJSON), record.Source)
	record.DeviceLocation = decodeJSONPtr[model.ScreenDeviceLocationResponse](deviceLocationJSON)
	record.Drone = decodeJSONPtr[model.ScreenPositionPoint](droneJSON)
	record.Pilot = decodeJSONPtr[model.ScreenPositionPoint](pilotJSON)
	record.Home = decodeJSONPtr[model.ScreenPositionPoint](homeJSON)
	record.DroneTrajectory = decodeJSONSlice[model.ScreenPositionTrackPoint](droneTrajectoryJSON)
	record.PilotTrajectory = decodeJSONSlice[model.ScreenPositionTrackPoint](pilotTrajectoryJSON)
	record.PilotDistanceM = floatPtr(pilotDistanceM)
	record.DroneDistanceM = floatPtr(droneDistanceM)
	record.DroneDirectionDeg = floatPtr(droneDirectionDeg)
	record.DeviceDirectionDeg = floatPtr(deviceDirectionDeg)
	record.Height = floatPtr(height)
	record.Altitude = floatPtr(altitude)
	record.Speed = floatPtr(speed)
	record.LastRecord = decodeJSONValue[model.ScreenPositionLastRecord](lastRecordJSON)
	return record, nil
}

// ParseTargetType validates a public target type query value.
func ParseTargetType(value string) (model.IntrusionTargetType, error) {
	targetType := model.IntrusionTargetType(strings.TrimSpace(value))
	switch targetType {
	case "", model.IntrusionTargetTypePosition:
		return targetType, nil
	default:
		return "", errors.New("invalid intrusion target type")
	}
}

func normalizeIDs(ids []string) []string {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || slices.Contains(result, id) {
			continue
		}
		result = append(result, id)
	}
	return result
}

func likePattern(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return "%" + value + "%"
}

func intrusionRecordID(targetType model.IntrusionTargetType, targetID string, firstSeen time.Time) string {
	return fmt.Sprintf("intrusion-%s-%s-%d", targetType, strings.TrimSpace(targetID), firstSeen.UnixNano())
}

func isUncrackedDJIDrone(target model.ScreenPositionTarget) bool {
	return strings.EqualFold(strings.TrimSpace(target.Model), "DJI-Drone") && !target.Cracked
}

func durationSeconds(firstSeen, lastSeen time.Time) int64 {
	if firstSeen.IsZero() || lastSeen.IsZero() || lastSeen.Before(firstSeen) {
		return 0
	}
	return int64(lastSeen.Sub(firstSeen).Seconds())
}

func normalizeSources(values []string, fallback string) []string {
	result := make([]string, 0, len(values)+1)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || slices.Contains(result, value) {
			continue
		}
		result = append(result, value)
	}
	fallback = strings.TrimSpace(fallback)
	if fallback != "" && !slices.Contains(result, fallback) {
		result = append(result, fallback)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func clonePoint(point *model.ScreenPositionPoint) *model.ScreenPositionPoint {
	if point == nil {
		return nil
	}
	cloned := *point
	return &cloned
}

func cloneDeviceLocation(location model.ScreenDeviceLocationResponse) *model.ScreenDeviceLocationResponse {
	cloned := location
	if location.Point != nil {
		point := *location.Point
		cloned.Point = &point
	}
	if location.UpdatedAt != nil {
		updatedAt := *location.UpdatedAt
		cloned.UpdatedAt = &updatedAt
	}
	if location.RFTempC != nil {
		value := *location.RFTempC
		cloned.RFTempC = &value
	}
	if location.MainTempC != nil {
		value := *location.MainTempC
		cloned.MainTempC = &value
	}
	return &cloned
}

func cloneTrajectory(points []model.ScreenPositionTrackPoint) []model.ScreenPositionTrackPoint {
	if len(points) == 0 {
		return nil
	}
	cloned := make([]model.ScreenPositionTrackPoint, len(points))
	copy(cloned, points)
	return cloned
}

func archivedTrajectory(full, display []model.ScreenPositionTrackPoint) []model.ScreenPositionTrackPoint {
	if len(full) > 0 {
		return cloneTrajectory(full)
	}
	return cloneTrajectory(display)
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func jsonString(value any) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	data, err := json.Marshal(value)
	if err != nil || string(data) == "null" || string(data) == "[]" {
		return sql.NullString{}
	}
	return sql.NullString{String: string(data), Valid: true}
}

func nullableFloat(value *float64) sql.NullFloat64 {
	if value == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *value, Valid: true}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
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
		return nil
	}
	var value []T
	if err := json.Unmarshal([]byte(raw.String), &value); err != nil {
		return nil
	}
	return value
}

func decodeJSONValue[T any](raw sql.NullString) T {
	var value T
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return value
	}
	_ = json.Unmarshal([]byte(raw.String), &value)
	return value
}

func floatPtr(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

func validGeoPoint(point *model.GeoPoint) bool {
	return point != nil &&
		!math.IsNaN(point.Latitude) &&
		!math.IsInf(point.Latitude, 0) &&
		!math.IsNaN(point.Longitude) &&
		!math.IsInf(point.Longitude, 0) &&
		point.Latitude >= -90 &&
		point.Latitude <= 90 &&
		point.Longitude >= -180 &&
		point.Longitude <= 180 &&
		!(point.Latitude == 0 && point.Longitude == 0)
}

func distanceMeters(a, b model.ScreenPositionPoint) float64 {
	const earthRadiusM = 6371008.8
	lat1 := a.Latitude * math.Pi / 180
	lat2 := b.Latitude * math.Pi / 180
	dLat := (b.Latitude - a.Latitude) * math.Pi / 180
	dLon := (b.Longitude - a.Longitude) * math.Pi / 180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return earthRadiusM * 2 * math.Atan2(math.Sqrt(h), math.Sqrt(1-h))
}

func bearingDegrees(a, b model.ScreenPositionPoint) float64 {
	lat1 := a.Latitude * math.Pi / 180
	lat2 := b.Latitude * math.Pi / 180
	dLon := (b.Longitude - a.Longitude) * math.Pi / 180
	y := math.Sin(dLon) * math.Cos(lat2)
	x := math.Cos(lat1)*math.Sin(lat2) -
		math.Sin(lat1)*math.Cos(lat2)*math.Cos(dLon)
	value := math.Atan2(y, x) * 180 / math.Pi
	if value < 0 {
		value += 360
	}
	return value
}
