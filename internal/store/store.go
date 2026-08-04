// Package store keeps bounded in-memory runtime state and SSE subscribers.
package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"drone-management/internal/coordinate"
	"drone-management/internal/model"
)

const (
	defaultPositionTTL        = 20 * time.Second
	fpvTTL                    = 10 * time.Second
	fpvArchiveQueueLimit      = 4096
	fpvArchiveBatchSize       = 64
	fpvArchiveRetryBase       = time.Second
	fpvArchiveRetryMax        = time.Minute
	fpvArchiveDropLogInterval = time.Minute
	screenPositionUpdatedType = "screen.position.updated"
	screenPositionRemovedType = "screen.position.removed"
	screenFPVUpdatedType      = "screen.fpv.updated"
	screenFPVRemovedType      = "screen.fpv.removed"
)

// Store owns runtime records and event subscribers.
type Store struct {
	mu             sync.RWMutex
	fpvEventMu     sync.Mutex
	fpvArchiveGate chan struct{}

	maxPositions int
	maxFPV       int
	positionTTL  time.Duration

	positions []model.ScreenPositionTarget
	fpv       []model.ScreenFPVTarget
	location  model.ScreenDeviceLocationResponse
	manual    *model.GeoPoint
	manualAt  *time.Time

	positionArchiver        PositionArchiver
	expiredPositions        []model.ScreenPositionTarget
	fpvArchiver             FPVArchiver
	pendingFPV              []model.ScreenFPVTarget
	pendingFPVHead          int
	fpvArchiveFailures      int
	fpvArchiveRetryAt       time.Time
	fpvArchiveDropped       uint64
	fpvArchiveDropsSinceLog uint64
	fpvArchiveLastDropLog   time.Time
	positionSeq             uint64
	fpvSeq                  uint64
	subscribers             map[chan model.Event]struct{}
}

// PositionArchiver persists positioning targets that disappeared from the live list.
type PositionArchiver interface {
	ArchivePosition(model.ScreenPositionTarget) error
}

// FPVArchiver persists FPV targets that disappeared from the live list.
type FPVArchiver interface {
	ArchiveFPVContext(context.Context, model.ScreenFPVTarget) error
}

type fpvArchiveKey struct {
	id        string
	firstSeen int64
}

// New creates a bounded runtime store.
func New(maxPositions, maxFPV int) *Store {
	return &Store{
		maxPositions:   max(1, maxPositions),
		maxFPV:         max(1, maxFPV),
		positionTTL:    defaultPositionTTL,
		fpvArchiveGate: make(chan struct{}, 1),
		subscribers:    map[chan model.Event]struct{}{},
		location: model.ScreenDeviceLocationResponse{
			Source: "none",
			Valid:  false,
			Locked: false,
		},
	}
}

// SetPositionTTL updates the live positioning target TTL and prunes expired targets immediately.
func (s *Store) SetPositionTTL(ttl time.Duration) {
	if ttl <= 0 {
		ttl = defaultPositionTTL
	}
	s.mu.Lock()
	s.positionTTL = ttl
	s.prunePositionsLocked(time.Now())
	archiver := s.positionArchiver
	s.mu.Unlock()

	s.archiveExpiredPositions(archiver)
}

// PositionTTL returns the current live positioning target TTL.
func (s *Store) PositionTTL() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.positionTTL
}

// SetPositionArchiver sets the archiver used when positioning targets expire.
func (s *Store) SetPositionArchiver(archiver PositionArchiver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.positionArchiver = archiver
}

// SetFPVArchiver sets the archiver used when FPV targets retire.
func (s *Store) SetFPVArchiver(archiver FPVArchiver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fpvArchiver = archiver
	s.fpvArchiveFailures = 0
	s.fpvArchiveRetryAt = time.Time{}
}

// UpdateDeviceLocation stores the latest receiver/device position.
func (s *Store) UpdateDeviceLocation(location model.ScreenDeviceLocationResponse) {
	now := time.Now()
	if location.UpdatedAt == nil {
		location.UpdatedAt = &now
	}
	if location.Source == "" {
		location.Source = "ddsT1"
	}

	s.mu.Lock()
	if !location.Valid || location.Point == nil {
		if s.location.Valid && s.location.Point != nil {
			location.Point = cloneGeoPoint(s.location.Point)
			location.Valid = true
		} else {
			location.Point = nil
			location.Valid = false
		}
	}
	s.location = location
	effective := s.effectiveDeviceLocationLocked()
	s.mu.Unlock()

	s.Publish(model.Event{
		Type:    "screen.device_location.updated",
		Time:    now,
		Payload: effective,
	})
}

// SetManualDeviceLocation stores a fallback receiver/device position.
func (s *Store) SetManualDeviceLocation(point model.GeoPoint) model.ScreenDeviceLocationResponse {
	return s.SetManualDeviceLocationAt(point, time.Now())
}

// SetManualDeviceLocationAt stores a fallback receiver/device position with a known timestamp.
func (s *Store) SetManualDeviceLocationAt(point model.GeoPoint, updatedAt time.Time) model.ScreenDeviceLocationResponse {
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	s.mu.Lock()
	s.manual = cloneGeoPoint(&point)
	s.manualAt = cloneTime(&updatedAt)
	effective := s.effectiveDeviceLocationLocked()
	s.mu.Unlock()

	s.Publish(model.Event{
		Type:    "screen.device_location.updated",
		Time:    updatedAt,
		Payload: effective,
	})
	return effective
}

// LoadManualDeviceLocation restores a persisted fallback receiver/device position without publishing.
func (s *Store) LoadManualDeviceLocation(point model.GeoPoint, updatedAt *time.Time) {
	if updatedAt == nil || updatedAt.IsZero() {
		now := time.Now()
		updatedAt = &now
	}
	s.mu.Lock()
	s.manual = cloneGeoPoint(&point)
	s.manualAt = cloneTime(updatedAt)
	s.mu.Unlock()
}

// ClearManualDeviceLocation removes the fallback receiver/device position.
func (s *Store) ClearManualDeviceLocation() model.ScreenDeviceLocationResponse {
	now := time.Now()
	s.mu.Lock()
	s.manual = nil
	s.manualAt = nil
	effective := s.effectiveDeviceLocationLocked()
	s.mu.Unlock()

	s.Publish(model.Event{
		Type:    "screen.device_location.updated",
		Time:    now,
		Payload: effective,
	})
	return effective
}

// DeviceLocation returns the latest receiver/device position.
func (s *Store) DeviceLocation() model.ScreenDeviceLocationResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.effectiveDeviceLocationLocked()
}

func (s *Store) effectiveDeviceLocationLocked() model.ScreenDeviceLocationResponse {
	if s.location.Valid && s.location.Point != nil {
		return cloneDeviceLocation(s.location)
	}
	if s.manual != nil {
		location := cloneDeviceLocation(s.location)
		location.Source = "manual"
		location.Point = cloneGeoPoint(s.manual)
		location.UpdatedAt = cloneTime(s.manualAt)
		location.Valid = true
		location.Locked = false
		return location
	}
	return cloneDeviceLocation(s.location)
}

// AddPosition merges a positioning target and publishes an update.
func (s *Store) AddPosition(target model.ScreenPositionTarget) (model.ScreenPositionTarget, bool) {
	target.ReportedSerial = preferredReportedSerial(target.ReportedSerial, target.LastRecord.Serial, target.Serial)
	target.Serial = normalizePositionSerial(target.Source, target.Serial)
	target.Model = normalizePositionModel(target.Model)
	target.Source = strings.TrimSpace(target.Source)
	if target.Serial == "" || target.Model == "" {
		return model.ScreenPositionTarget{}, false
	}
	if target.LastRecord.Model != "" {
		target.LastRecord.Model = target.Model
	}
	if target.LastSeen.IsZero() {
		target.LastSeen = time.Now()
	}
	if target.FirstSeen.IsZero() {
		target.FirstSeen = target.LastSeen
	}
	target = sanitizePositionTarget(target)
	target.Sources = appendSource(target.Sources, target.Source)
	target.FullDroneTrajectory = appendTrajectory(
		target.FullDroneTrajectory,
		target.Drone,
		target.LastSeen,
		target.TrajectorySpeed,
		target.TrajectoryHeight,
	)
	target.FullPilotTrajectory = appendTrajectory(
		target.FullPilotTrajectory,
		target.Pilot,
		target.LastSeen,
		target.TrajectorySpeed,
		target.TrajectoryHeight,
	)

	s.mu.Lock()

	s.prunePositionsLocked(target.LastSeen)
	target = s.absorbDecodedDIDPlaceholderLocked(target)
	index := s.findPositionLocked(target)
	if index == -1 {
		if target.ID == "" {
			s.positionSeq++
			target.ID = fmt.Sprintf("position-%d-%d", target.LastSeen.UnixNano(), s.positionSeq)
		}
		if target.HitCount <= 0 {
			target.HitCount = 1
		}
		s.positions = append(s.positions, target)
		trimNewestPositions(&s.positions, s.maxPositions)
		merged := publicPosition(target, s.effectiveDeviceLocationLocked())
		archiver := s.positionArchiver
		s.mu.Unlock()
		s.archiveExpiredPositions(archiver)
		go s.Publish(model.Event{Type: screenPositionUpdatedType, Time: merged.LastSeen, Payload: merged})
		return merged, true
	}

	merged := mergePosition(s.positions[index], target)
	s.positions[index] = merged
	result := publicPosition(merged, s.effectiveDeviceLocationLocked())
	archiver := s.positionArchiver
	s.mu.Unlock()
	s.archiveExpiredPositions(archiver)
	go s.Publish(model.Event{Type: screenPositionUpdatedType, Time: result.LastSeen, Payload: result})
	return result, true
}

// RemoveUncrackedDIDScreenPositionByCorrelationID removes the temporary DID placeholder for a decoded target.
func (s *Store) RemoveUncrackedDIDScreenPositionByCorrelationID(correlationID string) (model.ScreenPositionTarget, bool) {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return model.ScreenPositionTarget{}, false
	}

	s.mu.Lock()
	removed, ok := s.removeUncrackedDIDScreenPositionByCorrelationIDLocked(correlationID)
	location := s.effectiveDeviceLocationLocked()
	s.mu.Unlock()
	if !ok {
		return model.ScreenPositionTarget{}, false
	}

	removed = publicPosition(removed, location)
	s.Publish(model.Event{Type: screenPositionRemovedType, Time: removed.LastSeen, Payload: removed})
	return removed, true
}

// HasCrackedScreenPositionByCorrelationID reports whether a live decoded DID target already exists.
func (s *Store) HasCrackedScreenPositionByCorrelationID(correlationID string) bool {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return false
	}

	s.mu.Lock()
	s.prunePositionsLocked(time.Now())
	found := false
	for _, target := range s.positions {
		if strings.TrimSpace(target.CorrelationID) == correlationID && target.Cracked {
			found = true
			break
		}
	}
	archiver := s.positionArchiver
	s.mu.Unlock()

	s.archiveExpiredPositions(archiver)
	return found
}

// Positions returns positioning targets ordered by first-seen time.
func (s *Store) Positions(limit int) []model.ScreenPositionTarget {
	s.mu.Lock()
	s.prunePositionsLocked(time.Now())
	items := latestByPositionFirstSeen(s.positions, limit)
	location := s.effectiveDeviceLocationLocked()
	archiver := s.positionArchiver
	s.mu.Unlock()

	s.archiveExpiredPositions(archiver)
	for index := range items {
		items[index] = publicPosition(items[index], location)
	}
	return items
}

// AddFPV merges an FPV signal and publishes an update.
func (s *Store) AddFPV(target model.ScreenFPVTarget) (model.ScreenFPVTarget, bool) {
	target.SignalType = strings.TrimSpace(target.SignalType)
	target.DeviceSN = strings.TrimSpace(target.DeviceSN)
	if target.Frequency <= 0 || target.SignalType == "" {
		return model.ScreenFPVTarget{}, false
	}
	if target.LastSeen.IsZero() {
		target.LastSeen = time.Now()
	}
	if target.FirstSeen.IsZero() {
		target.FirstSeen = target.LastSeen
	}
	target.EverValid = target.EverValid || target.Valid

	s.fpvEventMu.Lock()
	s.mu.Lock()
	retired := s.pruneFPVLocked(time.Now())
	index := s.findFPVLocked(target)
	if index == -1 {
		s.fpvSeq++
		target.ID = fmt.Sprintf("fpv-%d-%d", target.LastSeen.UnixNano(), s.fpvSeq)
		target.HitCount = 1
		s.fpv = append(s.fpv, target)
		retired = append(retired, s.retireFPVLocked(trimNewestFPV(&s.fpv, s.maxFPV))...)
		result := cloneFPV(target)
		live := s.hasFPVLocked(result.ID)
		s.mu.Unlock()
		s.publishRetiredFPV(retired)
		if live {
			s.Publish(model.Event{Type: screenFPVUpdatedType, Time: result.LastSeen, Payload: result})
		}
		s.fpvEventMu.Unlock()
		s.reportFPVArchiveDrops(false)
		return result, true
	}

	merged := s.fpv[index]
	if target.FirstSeen.Before(merged.FirstSeen) {
		merged.FirstSeen = target.FirstSeen
	}
	if merged.DeviceSN == "" && target.DeviceSN != "" {
		merged.DeviceSN = target.DeviceSN
	}
	merged.EverValid = merged.EverValid || target.EverValid || target.Valid
	merged.HitCount++
	if !target.LastSeen.Before(merged.LastSeen) {
		merged.Frequency = target.Frequency
		merged.RSSI = target.RSSI
		merged.SignalType = target.SignalType
		merged.Valid = target.Valid
		merged.Format = target.Format
		merged.LastSeen = target.LastSeen
		merged.LastRecord = target.LastRecord
	}
	s.fpv[index] = merged
	result := cloneFPV(merged)
	s.mu.Unlock()
	s.publishRetiredFPV(retired)
	s.Publish(model.Event{Type: screenFPVUpdatedType, Time: result.LastSeen, Payload: result})
	s.fpvEventMu.Unlock()
	s.reportFPVArchiveDrops(false)
	return result, true
}

// FPV returns latest FPV targets.
func (s *Store) FPV(limit int) []model.ScreenFPVTarget {
	s.fpvEventMu.Lock()
	s.mu.Lock()
	retired := s.pruneFPVLocked(time.Now())
	items := latestByFPVLastSeen(s.fpv, limit)
	s.mu.Unlock()
	s.publishRetiredFPV(retired)
	s.fpvEventMu.Unlock()
	s.reportFPVArchiveDrops(false)
	return items
}

// FPVTarget returns a live FPV target by ID.
func (s *Store) FPVTarget(id string) (model.ScreenFPVTarget, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return model.ScreenFPVTarget{}, false
	}
	s.fpvEventMu.Lock()
	s.mu.Lock()
	retired := s.pruneFPVLocked(time.Now())
	var result model.ScreenFPVTarget
	found := false
	for _, item := range s.fpv {
		if item.ID == id {
			result = cloneFPV(item)
			found = true
			break
		}
	}
	s.mu.Unlock()
	s.publishRetiredFPV(retired)
	s.fpvEventMu.Unlock()
	s.reportFPVArchiveDrops(false)
	return result, found
}

// SweepFPV retires expired FPV targets and retries one bounded archive batch.
func (s *Store) SweepFPV(ctx context.Context, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	s.fpvEventMu.Lock()
	s.mu.Lock()
	retired := s.pruneFPVLocked(now)
	s.mu.Unlock()
	s.publishRetiredFPV(retired)
	s.fpvEventMu.Unlock()
	s.reportFPVArchiveDrops(false)
	return s.flushFPVArchives(ctx, now, false)
}

// DrainFPV retires every live FPV target and synchronously flushes pending archives.
func (s *Store) DrainFPV(ctx context.Context) error {
	s.fpvEventMu.Lock()
	s.mu.Lock()
	retired := s.retireFPVLocked(s.fpv)
	clear(s.fpv)
	s.fpv = nil
	s.mu.Unlock()
	s.publishRetiredFPV(retired)
	s.fpvEventMu.Unlock()
	s.reportFPVArchiveDrops(true)
	return s.flushFPVArchives(ctx, time.Now(), true)
}

// FlushFPVArchives processes one bounded archive batch when retry backoff allows it.
func (s *Store) FlushFPVArchives(ctx context.Context) error {
	return s.flushFPVArchives(ctx, time.Now(), false)
}

func (s *Store) flushFPVArchives(ctx context.Context, now time.Time, force bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	select {
	case s.fpvArchiveGate <- struct{}{}:
		defer func() { <-s.fpvArchiveGate }()
	case <-ctx.Done():
		return ctx.Err()
	}

	s.mu.RLock()
	archiver := s.fpvArchiver
	pendingCount := len(s.pendingFPV)
	retryAt := s.fpvArchiveRetryAt
	s.mu.RUnlock()
	if archiver == nil || pendingCount == 0 || (!force && now.Before(retryAt)) {
		return nil
	}

	budget := fpvArchiveBatchSize
	if force {
		budget = pendingCount
	}
	archiveErrors := make([]error, 0)
	attempted := false
	hadFailure := false
	for budget > 0 {
		batch := s.nextFPVArchiveBatch(min(fpvArchiveBatchSize, budget))
		if len(batch) == 0 {
			break
		}
		attempted = true
		succeeded, failed, batchErr := archiveFPVBatch(ctx, archiver, batch)
		s.applyFPVArchiveBatch(succeeded, failed)
		if batchErr != nil {
			hadFailure = true
			archiveErrors = append(archiveErrors, batchErr)
		}
		budget -= len(batch)
		if !force || ctx.Err() != nil {
			break
		}
	}
	s.updateFPVArchiveBackoff(now, attempted, hadFailure)
	return errors.Join(archiveErrors...)
}

func (s *Store) nextFPVArchiveBatch(limit int) []model.ScreenFPVTarget {
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit = min(limit, len(s.pendingFPV))
	batch := make([]model.ScreenFPVTarget, limit)
	for index := range batch {
		batch[index] = cloneFPV(s.pendingFPV[(s.pendingFPVHead+index)%len(s.pendingFPV)])
	}
	return batch
}

func archiveFPVBatch(
	ctx context.Context,
	archiver FPVArchiver,
	batch []model.ScreenFPVTarget,
) (map[fpvArchiveKey]struct{}, map[fpvArchiveKey]struct{}, error) {
	succeeded := make(map[fpvArchiveKey]struct{}, len(batch))
	failed := make(map[fpvArchiveKey]struct{}, len(batch))
	archiveErrors := make([]error, 0)
	for _, target := range batch {
		key := archiveKey(target)
		if err := archiver.ArchiveFPVContext(ctx, cloneFPV(target)); err != nil {
			failed[key] = struct{}{}
			archiveErrors = append(archiveErrors, fmt.Errorf("archive FPV target %s: %w", target.ID, err))
			continue
		}
		succeeded[key] = struct{}{}
	}
	return succeeded, failed, errors.Join(archiveErrors...)
}

func (s *Store) applyFPVArchiveBatch(
	succeeded map[fpvArchiveKey]struct{},
	failed map[fpvArchiveKey]struct{},
) {
	s.mu.Lock()
	defer s.mu.Unlock()

	original := s.pendingFPV
	remaining := make([]model.ScreenFPVTarget, 0, len(original))
	retryTargets := make([]model.ScreenFPVTarget, 0, len(failed))
	for offset := 0; offset < len(original); offset++ {
		target := original[(s.pendingFPVHead+offset)%len(original)]
		key := archiveKey(target)
		if _, ok := succeeded[key]; ok {
			continue
		}
		if _, ok := failed[key]; ok {
			retryTargets = append(retryTargets, target)
			continue
		}
		remaining = append(remaining, target)
	}
	remaining = append(remaining, retryTargets...)
	clear(original)
	s.pendingFPV = remaining
	s.pendingFPVHead = 0
}

func (s *Store) updateFPVArchiveBackoff(now time.Time, attempted, failed bool) {
	if !attempted {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pendingFPV) == 0 || !failed {
		s.fpvArchiveFailures = 0
		s.fpvArchiveRetryAt = time.Time{}
		return
	}
	s.fpvArchiveFailures++
	s.fpvArchiveRetryAt = now.Add(fpvArchiveRetryDelay(s.fpvArchiveFailures))
}

func fpvArchiveRetryDelay(failures int) time.Duration {
	if failures <= 1 {
		return fpvArchiveRetryBase
	}
	delay := fpvArchiveRetryBase
	for attempt := 1; attempt < failures && delay < fpvArchiveRetryMax; attempt++ {
		if delay > fpvArchiveRetryMax/2 {
			return fpvArchiveRetryMax
		}
		delay *= 2
	}
	return min(delay, fpvArchiveRetryMax)
}

// Subscribe registers an event subscriber.
func (s *Store) Subscribe(buffer int) (<-chan model.Event, func()) {
	if buffer <= 0 {
		buffer = 16
	}
	ch := make(chan model.Event, buffer)

	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()

	unsubscribe := func() {
		s.mu.Lock()
		if _, ok := s.subscribers[ch]; ok {
			delete(s.subscribers, ch)
			close(ch)
		}
		s.mu.Unlock()
	}
	return ch, unsubscribe
}

// Publish broadcasts an event without blocking producers.
func (s *Store) Publish(evt model.Event) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if evt.Time.IsZero() {
		evt.Time = time.Now()
	}
	for ch := range s.subscribers {
		select {
		case ch <- evt:
		default:
		}
	}
}

func (s *Store) archiveExpiredPositions(archiver PositionArchiver) {
	expired := s.popExpiredPositions()
	if archiver == nil {
		return
	}
	for _, target := range expired {
		if err := archiver.ArchivePosition(target); err != nil {
			slog.Warn("归档定位入侵目标失败", "targetId", target.ID, "error", err)
		}
	}
}

func (s *Store) popExpiredPositions() []model.ScreenPositionTarget {
	s.mu.Lock()
	defer s.mu.Unlock()
	expired := s.expiredPositions
	s.expiredPositions = nil
	return expired
}

func (s *Store) findPositionLocked(target model.ScreenPositionTarget) int {
	for index, existing := range s.positions {
		if samePositionIdentity(existing, target) {
			return index
		}
	}
	return -1
}

func samePositionIdentity(left, right model.ScreenPositionTarget) bool {
	if isUncrackedDIDScreenPosition(left) != isUncrackedDIDScreenPosition(right) {
		return false
	}
	leftCorrelationID := strings.TrimSpace(left.CorrelationID)
	rightCorrelationID := strings.TrimSpace(right.CorrelationID)
	if leftCorrelationID != "" && leftCorrelationID == rightCorrelationID {
		return true
	}

	leftSerial := normalizePositionSerial(left.Source, left.Serial)
	rightSerial := normalizePositionSerial(right.Source, right.Serial)
	if leftSerial != "" && strings.EqualFold(leftSerial, rightSerial) {
		return true
	}
	return false
}

func (s *Store) removeUncrackedDIDScreenPositionByCorrelationIDLocked(correlationID string) (model.ScreenPositionTarget, bool) {
	for index, target := range s.positions {
		if strings.TrimSpace(target.CorrelationID) != correlationID || !isUncrackedDIDScreenPosition(target) {
			continue
		}
		removed := clonePosition(target)
		s.positions = slices.Delete(s.positions, index, index+1)
		return removed, true
	}
	return model.ScreenPositionTarget{}, false
}

func (s *Store) findFPVLocked(target model.ScreenFPVTarget) int {
	targetSN := strings.TrimSpace(target.DeviceSN)
	if targetSN != "" {
		for index, existing := range s.fpv {
			existingSN := strings.TrimSpace(existing.DeviceSN)
			if existingSN != "" && strings.EqualFold(existingSN, targetSN) {
				return index
			}
		}

		candidate := -1
		for index, existing := range s.fpv {
			if strings.TrimSpace(existing.DeviceSN) != "" || !sameFPVFrequencyAndType(existing, target) {
				continue
			}
			if candidate != -1 {
				return -1
			}
			candidate = index
		}
		return candidate
	}

	anonymousCandidate := -1
	knownCandidate := -1
	for index, existing := range s.fpv {
		if !sameFPVFrequencyAndType(existing, target) {
			continue
		}
		if strings.TrimSpace(existing.DeviceSN) == "" {
			if anonymousCandidate != -1 {
				return -1
			}
			anonymousCandidate = index
			continue
		}
		if knownCandidate != -1 {
			knownCandidate = -2
			continue
		}
		knownCandidate = index
	}
	if anonymousCandidate != -1 {
		return anonymousCandidate
	}
	if knownCandidate >= 0 {
		return knownCandidate
	}
	return -1
}

func (s *Store) hasFPVLocked(id string) bool {
	for _, target := range s.fpv {
		if target.ID == id {
			return true
		}
	}
	return false
}

func sameFPVFrequencyAndType(left, right model.ScreenFPVTarget) bool {
	return math.Abs(left.Frequency-right.Frequency) < 0.5 &&
		strings.EqualFold(left.SignalType, right.SignalType)
}

func (s *Store) prunePositionsLocked(now time.Time) {
	active := s.positions[:0]
	ttl := s.positionTTL
	if ttl <= 0 {
		ttl = defaultPositionTTL
	}
	for _, target := range s.positions {
		if now.Sub(target.LastSeen) <= ttl {
			active = append(active, target)
			continue
		}
		s.expiredPositions = append(s.expiredPositions, clonePosition(target))
	}
	clear(s.positions[len(active):])
	s.positions = active
}

func (s *Store) pruneFPVLocked(now time.Time) []model.ScreenFPVTarget {
	active := s.fpv[:0]
	retired := make([]model.ScreenFPVTarget, 0)
	for _, target := range s.fpv {
		if now.Sub(target.LastSeen) <= fpvTTL {
			active = append(active, target)
			continue
		}
		retired = append(retired, target)
	}
	clear(s.fpv[len(active):])
	s.fpv = active
	return s.retireFPVLocked(retired)
}

func (s *Store) retireFPVLocked(targets []model.ScreenFPVTarget) []model.ScreenFPVTarget {
	retired := make([]model.ScreenFPVTarget, 0, len(targets))
	for _, target := range targets {
		target = cloneFPV(target)
		retired = append(retired, target)
		if target.EverValid {
			s.enqueueFPVArchiveLocked(target)
		}
	}
	return retired
}

func (s *Store) enqueueFPVArchiveLocked(target model.ScreenFPVTarget) {
	target = cloneFPV(target)
	if len(s.pendingFPV) < fpvArchiveQueueLimit {
		s.pendingFPV = append(s.pendingFPV, target)
		return
	}
	s.pendingFPV[s.pendingFPVHead] = target
	s.pendingFPVHead = (s.pendingFPVHead + 1) % len(s.pendingFPV)
	s.fpvArchiveDropped++
	s.fpvArchiveDropsSinceLog++
}

func (s *Store) publishRetiredFPV(targets []model.ScreenFPVTarget) {
	for _, target := range targets {
		s.Publish(model.Event{
			Type:    screenFPVRemovedType,
			Time:    time.Now(),
			Payload: cloneFPV(target),
		})
	}
}

func (s *Store) reportFPVArchiveDrops(force bool) {
	now := time.Now()
	s.mu.Lock()
	if s.fpvArchiveDropsSinceLog == 0 ||
		(!force && !s.fpvArchiveLastDropLog.IsZero() && now.Sub(s.fpvArchiveLastDropLog) < fpvArchiveDropLogInterval) {
		s.mu.Unlock()
		return
	}
	dropped := s.fpvArchiveDropsSinceLog
	total := s.fpvArchiveDropped
	pending := len(s.pendingFPV)
	s.fpvArchiveDropsSinceLog = 0
	s.fpvArchiveLastDropLog = now
	s.mu.Unlock()

	slog.Warn(
		"FPV 待归档队列已满，已丢弃最旧候选",
		"dropped", dropped,
		"droppedTotal", total,
		"pending", pending,
		"capacity", fpvArchiveQueueLimit,
	)
}

func archiveKey(target model.ScreenFPVTarget) fpvArchiveKey {
	return fpvArchiveKey{id: target.ID, firstSeen: target.FirstSeen.UnixNano()}
}

func mergePosition(current, incoming model.ScreenPositionTarget) model.ScreenPositionTarget {
	current = sanitizePositionTarget(current)
	incoming = sanitizePositionTarget(incoming)
	current.CorrelationID = firstNonEmpty(incoming.CorrelationID, current.CorrelationID)
	keepDecodedFields := shouldKeepDecodedPositionFields(current, incoming)
	incomingIsCurrent := !incoming.LastSeen.Before(current.LastSeen)
	current.FirstSeen = earlierTime(current.FirstSeen, incoming.FirstSeen)
	current.Sources = appendSource(current.Sources, incoming.Source)
	current.Sources = appendSources(current.Sources, incoming.Sources)
	if incomingIsCurrent || current.Frequency == 0 {
		current.Frequency = firstNonZeroFloat(incoming.Frequency, current.Frequency)
	}
	if incomingIsCurrent || current.RSSI == 0 {
		current.RSSI = firstNonZeroFloat(incoming.RSSI, current.RSSI)
	}
	if incomingIsCurrent || strings.TrimSpace(current.Device) == "" {
		current.Device = firstNonEmpty(incoming.Device, current.Device)
	}
	current.LastSeen = laterTime(current.LastSeen, incoming.LastSeen)
	current.ReportedSerial = preferredReportedSerial(
		current.ReportedSerial,
		incoming.ReportedSerial,
		incoming.LastRecord.Serial,
		current.LastRecord.Serial,
		incoming.Serial,
		current.Serial,
	)
	hitIncrement := 1
	if incoming.HitCount > 0 {
		hitIncrement = incoming.HitCount
	}
	current.HitCount += hitIncrement
	current.FullDroneTrajectory = mergeTrajectories(current.FullDroneTrajectory, incoming.FullDroneTrajectory)
	current.FullPilotTrajectory = mergeTrajectories(current.FullPilotTrajectory, incoming.FullPilotTrajectory)
	if keepDecodedFields {
		return current
	}
	current.Serial = preferredPositionSerial(current, incoming)
	current.Model = preferredPositionModel(current.Model, incoming.Model)
	current.Source = firstNonEmpty(incoming.Source, current.Source)
	current.Drone = firstPoint(incoming.Drone, current.Drone)
	current.Pilot = firstPoint(incoming.Pilot, current.Pilot)
	current.Home = firstPoint(incoming.Home, current.Home)
	current.Height = firstFloat(incoming.Height, current.Height)
	current.Altitude = firstFloat(incoming.Altitude, current.Altitude)
	current.Speed = firstFloat(incoming.Speed, current.Speed)
	current.Cracked = current.Cracked || incoming.Cracked
	if incomingIsCurrent || current.LastRecord.Type == "" {
		current.LastRecord = incoming.LastRecord
	}
	return current
}

func (s *Store) absorbDecodedDIDPlaceholderLocked(target model.ScreenPositionTarget) model.ScreenPositionTarget {
	if !isDecodedDIDTarget(target) {
		return target
	}
	index := s.findDIDPlaceholderLocked(target.CorrelationID)
	if index == -1 {
		return target
	}
	placeholder := s.positions[index]
	s.positions = slices.Delete(s.positions, index, index+1)
	return mergePosition(placeholder, target)
}

func (s *Store) findDIDPlaceholderLocked(correlationID string) int {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return -1
	}
	for index, existing := range s.positions {
		if strings.TrimSpace(existing.CorrelationID) != correlationID {
			continue
		}
		if !existing.Cracked || isTemporaryDIDSerial(existing) || isPlaceholderPositionModel(existing.Model) {
			return index
		}
	}
	return -1
}

func laterTime(left, right time.Time) time.Time {
	if right.After(left) {
		return right
	}
	return left
}

func earlierTime(left, right time.Time) time.Time {
	if left.IsZero() {
		return right
	}
	if right.IsZero() || left.Before(right) {
		return left
	}
	return right
}

func shouldKeepDecodedPositionFields(current, incoming model.ScreenPositionTarget) bool {
	if !current.Cracked || incoming.Cracked {
		return false
	}
	currentCorrelationID := strings.TrimSpace(current.CorrelationID)
	incomingCorrelationID := strings.TrimSpace(incoming.CorrelationID)
	if currentCorrelationID != "" && incomingCorrelationID != "" && currentCorrelationID == incomingCorrelationID {
		return true
	}
	currentSerial := strings.TrimSpace(current.Serial)
	incomingSerial := strings.TrimSpace(incoming.Serial)
	currentSerial = normalizePositionSerial(current.Source, currentSerial)
	incomingSerial = normalizePositionSerial(incoming.Source, incomingSerial)
	return currentSerial != "" && incomingSerial != "" && strings.EqualFold(currentSerial, incomingSerial)
}

func appendTrajectory(
	current []model.ScreenPositionTrackPoint,
	point *model.ScreenPositionPoint,
	seenAt time.Time,
	speed *float64,
	height *float64,
) []model.ScreenPositionTrackPoint {
	if !validPositionPoint(point) {
		return current
	}
	next := model.ScreenPositionTrackPoint{
		Latitude:  point.Latitude,
		Longitude: point.Longitude,
		Speed:     cleanFloat(speed),
		Height:    cleanFloat(height),
		Time:      seenAt,
	}
	return appendTrajectoryPoint(current, next)
}

func mergeTrajectories(
	current []model.ScreenPositionTrackPoint,
	incoming []model.ScreenPositionTrackPoint,
) []model.ScreenPositionTrackPoint {
	current = cleanTrajectory(current)
	for _, item := range incoming {
		current = appendTrajectoryPoint(current, item)
	}
	return current
}

func appendTrajectoryPoint(
	current []model.ScreenPositionTrackPoint,
	item model.ScreenPositionTrackPoint,
) []model.ScreenPositionTrackPoint {
	if !validPositionCoordinate(item.Latitude, item.Longitude) {
		return current
	}
	item.Speed = cleanFloat(item.Speed)
	item.Height = cleanFloat(item.Height)
	if len(current) > 0 {
		last := current[len(current)-1]
		samePoint := last.Latitude == item.Latitude && last.Longitude == item.Longitude
		if samePoint && item.Time.Sub(last.Time) < time.Second {
			return current
		}
	}
	return append(current, item)
}

func sanitizePositionTarget(target model.ScreenPositionTarget) model.ScreenPositionTarget {
	target.ReportedSerial = normalizeReportedSerial(target.ReportedSerial)
	target.Drone = cleanPoint(target.Drone)
	target.Pilot = cleanPoint(target.Pilot)
	target.Home = cleanPoint(target.Home)
	hasPositionPoint := target.Drone != nil || target.Pilot != nil || target.Home != nil
	target.Frequency = cleanNonZeroFloat(target.Frequency)
	target.RSSI = cleanNonZeroFloat(target.RSSI)
	target.Height = cleanTelemetryFloat(target.Height, hasPositionPoint)
	target.Altitude = cleanTelemetryFloat(target.Altitude, hasPositionPoint)
	target.Speed = cleanTelemetryFloat(target.Speed, hasPositionPoint)
	target.TrajectorySpeed = cleanFloat(target.TrajectorySpeed)
	target.TrajectoryHeight = cleanFloat(target.TrajectoryHeight)
	target.DroneTrajectory = cleanTrajectory(target.DroneTrajectory)
	target.PilotTrajectory = cleanTrajectory(target.PilotTrajectory)
	target.FullDroneTrajectory = cleanTrajectory(target.FullDroneTrajectory)
	target.FullPilotTrajectory = cleanTrajectory(target.FullPilotTrajectory)
	if len(target.FullDroneTrajectory) == 0 {
		target.FullDroneTrajectory = slices.Clone(target.DroneTrajectory)
	}
	if len(target.FullPilotTrajectory) == 0 {
		target.FullPilotTrajectory = slices.Clone(target.PilotTrajectory)
	}
	target.DroneTrajectory = nil
	target.PilotTrajectory = nil
	return target
}

func cleanPoint(point *model.ScreenPositionPoint) *model.ScreenPositionPoint {
	if !validPositionPoint(point) {
		return nil
	}
	return clonePoint(point)
}

func validPositionPoint(point *model.ScreenPositionPoint) bool {
	return point != nil && validPositionCoordinate(point.Latitude, point.Longitude)
}

func validPositionCoordinate(lat, lng float64) bool {
	return coordinate.IsValid(lng, lat)
}

func cleanFloat(value *float64) *float64 {
	if value == nil || !validFloat(*value) {
		return nil
	}
	return cloneFloat(value)
}

func cleanTelemetryFloat(value *float64, hasPositionPoint bool) *float64 {
	value = cleanFloat(value)
	if value == nil {
		return nil
	}
	if !hasPositionPoint && *value == 0 {
		return nil
	}
	return value
}

func cleanNonZeroFloat(value float64) float64 {
	if !validFloat(value) || value == 0 {
		return 0
	}
	return value
}

func validFloat(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func cleanTrajectory(points []model.ScreenPositionTrackPoint) []model.ScreenPositionTrackPoint {
	if len(points) == 0 {
		return nil
	}
	cleaned := points[:0]
	for _, point := range points {
		if !validPositionCoordinate(point.Latitude, point.Longitude) {
			continue
		}
		point.Speed = cleanFloat(point.Speed)
		point.Height = cleanFloat(point.Height)
		cleaned = append(cleaned, point)
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

func latestByPositionFirstSeen(items []model.ScreenPositionTarget, limit int) []model.ScreenPositionTarget {
	out := make([]model.ScreenPositionTarget, 0, len(items))
	for _, item := range items {
		out = append(out, clonePosition(item))
	}
	slices.SortFunc(out, func(a, b model.ScreenPositionTarget) int {
		if cmp := b.FirstSeen.Compare(a.FirstSeen); cmp != 0 {
			return cmp
		}
		return b.LastSeen.Compare(a.LastSeen)
	})
	return limitSlice(out, limit)
}

func latestByFPVLastSeen(items []model.ScreenFPVTarget, limit int) []model.ScreenFPVTarget {
	out := make([]model.ScreenFPVTarget, 0, len(items))
	for _, item := range items {
		out = append(out, cloneFPV(item))
	}
	slices.SortFunc(out, func(a, b model.ScreenFPVTarget) int {
		return b.LastSeen.Compare(a.LastSeen)
	})
	return limitSlice(out, limit)
}

func limitSlice[T any](items []T, limit int) []T {
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	return items[:limit]
}

func trimNewestPositions(items *[]model.ScreenPositionTarget, limit int) {
	if len(*items) <= limit {
		return
	}
	slices.SortFunc(*items, func(a, b model.ScreenPositionTarget) int {
		return b.LastSeen.Compare(a.LastSeen)
	})
	*items = (*items)[:limit]
}

func trimNewestFPV(items *[]model.ScreenFPVTarget, limit int) []model.ScreenFPVTarget {
	if len(*items) <= limit {
		return nil
	}
	slices.SortFunc(*items, func(a, b model.ScreenFPVTarget) int {
		return b.LastSeen.Compare(a.LastSeen)
	})
	retired := slices.Clone((*items)[limit:])
	clear((*items)[limit:])
	*items = (*items)[:limit]
	return retired
}

func withDeviceRelations(
	target model.ScreenPositionTarget,
	location model.ScreenDeviceLocationResponse,
) model.ScreenPositionTarget {
	if !location.Valid || location.Point == nil {
		return target
	}
	device := model.ScreenPositionPoint{
		Latitude:  location.Point.Latitude,
		Longitude: location.Point.Longitude,
	}
	if target.Pilot != nil {
		distance := distanceMeters(device, *target.Pilot)
		direction := bearingDegrees(device, *target.Pilot)
		target.PilotDistanceM = &distance
		target.DroneDirectionDeg = &direction
	}
	if target.Drone != nil {
		distance := distanceMeters(device, *target.Drone)
		direction := bearingDegrees(device, *target.Drone)
		target.DroneDistanceM = &distance
		target.DroneDirectionDeg = &direction
	}
	return target
}

func publicPosition(
	target model.ScreenPositionTarget,
	location model.ScreenDeviceLocationResponse,
) model.ScreenPositionTarget {
	target = clonePositionMetadata(target)
	target.DroneTrajectory = slices.Clone(target.FullDroneTrajectory)
	target.PilotTrajectory = slices.Clone(target.FullPilotTrajectory)
	target.FullDroneTrajectory = nil
	target.FullPilotTrajectory = nil
	target = withDeviceRelations(target, location)
	return target
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

func clonePosition(target model.ScreenPositionTarget) model.ScreenPositionTarget {
	target = clonePositionMetadata(target)
	target.DroneTrajectory = slices.Clone(target.DroneTrajectory)
	target.PilotTrajectory = slices.Clone(target.PilotTrajectory)
	target.FullDroneTrajectory = slices.Clone(target.FullDroneTrajectory)
	target.FullPilotTrajectory = slices.Clone(target.FullPilotTrajectory)
	return target
}

func clonePositionMetadata(target model.ScreenPositionTarget) model.ScreenPositionTarget {
	target.Drone = clonePoint(target.Drone)
	target.Pilot = clonePoint(target.Pilot)
	target.Home = clonePoint(target.Home)
	target.Height = cloneFloat(target.Height)
	target.Altitude = cloneFloat(target.Altitude)
	target.Speed = cloneFloat(target.Speed)
	target.PilotDistanceM = cloneFloat(target.PilotDistanceM)
	target.DroneDistanceM = cloneFloat(target.DroneDistanceM)
	target.DroneDirectionDeg = cloneFloat(target.DroneDirectionDeg)
	target.Sources = slices.Clone(target.Sources)
	return target
}

func cloneFPV(target model.ScreenFPVTarget) model.ScreenFPVTarget {
	return target
}

func cloneDeviceLocation(location model.ScreenDeviceLocationResponse) model.ScreenDeviceLocationResponse {
	location.Point = cloneGeoPoint(location.Point)
	location.RFTempC = cloneFloat(location.RFTempC)
	location.MainTempC = cloneFloat(location.MainTempC)
	location.UpdatedAt = cloneTime(location.UpdatedAt)
	return location
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func clonePoint(point *model.ScreenPositionPoint) *model.ScreenPositionPoint {
	if point == nil {
		return nil
	}
	next := *point
	return &next
}

func cloneGeoPoint(point *model.GeoPoint) *model.GeoPoint {
	if point == nil {
		return nil
	}
	next := *point
	return &next
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	next := *value
	return &next
}

func appendSource(current []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || slices.Contains(current, value) {
		return current
	}
	return append(current, value)
}

func appendSources(current []string, values []string) []string {
	for _, value := range values {
		current = appendSource(current, value)
	}
	return current
}

func normalizePositionSerial(source, serial string) string {
	serial = strings.Join(strings.Fields(strings.TrimSpace(serial)), "")
	if serial == "" {
		return ""
	}
	switch strings.ToUpper(strings.TrimSpace(source)) {
	case "RID", "RID_GB46750":
		for _, prefix := range []string{"1581"} {
			trimmed, ok := strings.CutPrefix(serial, prefix)
			if ok && len(trimmed) >= 12 {
				return trimmed
			}
		}
	}
	return serial
}

func normalizeReportedSerial(serial string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(serial)), "")
}

func preferredReportedSerial(values ...string) string {
	best := ""
	for _, value := range values {
		value = normalizeReportedSerial(value)
		if value == "" {
			continue
		}
		if betterReportedSerial(value, best) {
			best = value
		}
	}
	return best
}

func betterReportedSerial(candidate string, current string) bool {
	if current == "" {
		return true
	}
	if isFullRIDSerial(candidate) && !isFullRIDSerial(current) {
		return true
	}
	if !isTemporarySerialValue(candidate) && isTemporarySerialValue(current) {
		return true
	}
	return len(candidate) > len(current)
}

func isFullRIDSerial(serial string) bool {
	serial = strings.ToUpper(strings.TrimSpace(serial))
	return strings.HasPrefix(serial, "1581") && len(serial) > 16
}

func isTemporarySerialValue(serial string) bool {
	serial = strings.TrimSpace(serial)
	if len(serial) != 8 {
		return false
	}
	for _, ch := range serial {
		if !((ch >= '0' && ch <= '9') ||
			(ch >= 'a' && ch <= 'f') ||
			(ch >= 'A' && ch <= 'F')) {
			return false
		}
	}
	return true
}

func normalizePositionModel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	key := strings.ToLower(value)
	if before, _, ok := strings.Cut(key, "("); ok {
		key = before
	}
	key = strings.ReplaceAll(key, "-", " ")
	key = strings.ReplaceAll(key, "_", " ")
	key = strings.Join(strings.Fields(key), " ")
	key = strings.TrimPrefix(key, "dji ")
	key = strings.ReplaceAll(key, "mini4", "mini 4")
	key = strings.Join(strings.Fields(key), " ")

	switch key {
	case "mini 4 pro":
		return "Mini 4 Pro"
	case "dji drone":
		return "DJI-Drone"
	case "rid":
		return "RID"
	default:
		return value
	}
}

func preferredPositionSerial(current, incoming model.ScreenPositionTarget) string {
	currentSerial := strings.TrimSpace(current.Serial)
	incomingSerial := strings.TrimSpace(incoming.Serial)
	if currentSerial == "" {
		return incomingSerial
	}
	if incomingSerial == "" {
		return currentSerial
	}
	if !current.Cracked && incoming.Cracked {
		return incomingSerial
	}
	if isTemporaryDIDSerial(current) && !isTemporaryDIDSerial(incoming) {
		return incomingSerial
	}
	if isPlaceholderPositionModel(current.Model) && !isPlaceholderPositionModel(incoming.Model) {
		return incomingSerial
	}
	return currentSerial
}

func isTemporaryDIDSerial(target model.ScreenPositionTarget) bool {
	if !strings.EqualFold(strings.TrimSpace(target.Source), "dji_O:4") {
		return false
	}
	serial := strings.TrimSpace(target.Serial)
	if len(serial) != 8 {
		return false
	}
	for _, ch := range serial {
		if !((ch >= '0' && ch <= '9') ||
			(ch >= 'a' && ch <= 'f') ||
			(ch >= 'A' && ch <= 'F')) {
			return false
		}
	}
	return true
}

func isUncrackedDIDScreenPosition(target model.ScreenPositionTarget) bool {
	return !target.Cracked &&
		strings.TrimSpace(target.CorrelationID) != "" &&
		strings.EqualFold(strings.TrimSpace(target.Source), "dji_O:4") &&
		isPlaceholderPositionModel(target.Model)
}

func isDecodedDIDTarget(target model.ScreenPositionTarget) bool {
	return target.Cracked &&
		strings.TrimSpace(target.CorrelationID) != "" &&
		strings.EqualFold(strings.TrimSpace(target.Source), "dji_O:4") &&
		!isTemporaryDIDSerial(target) &&
		!isPlaceholderPositionModel(target.Model)
}

func preferredPositionModel(current, incoming string) string {
	current = strings.TrimSpace(current)
	incoming = strings.TrimSpace(incoming)
	if current == "" || isPlaceholderPositionModel(current) {
		return incoming
	}
	if incoming == "" || isPlaceholderPositionModel(incoming) {
		return current
	}
	if len(incoming) < len(current) {
		return incoming
	}
	return current
}

func isPlaceholderPositionModel(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "rid", "rid_gb46750", "unknown", "dji-drone":
		return true
	default:
		return false
	}
}

func firstPoint(primary, fallback *model.ScreenPositionPoint) *model.ScreenPositionPoint {
	if primary != nil {
		return clonePoint(primary)
	}
	return clonePoint(fallback)
}

func firstFloat(primary, fallback *float64) *float64 {
	if primary != nil {
		return cloneFloat(primary)
	}
	return cloneFloat(fallback)
}

func firstNonEmpty(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return strings.TrimSpace(primary)
	}
	return strings.TrimSpace(fallback)
}

func firstNonZeroFloat(primary, fallback float64) float64 {
	if primary != 0 {
		return primary
	}
	return fallback
}
