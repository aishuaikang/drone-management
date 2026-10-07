package interference

import (
	"context"
	"log/slog"
	"time"

	"drone-management/internal/model"
)

// SetRelayMonitor installs the read-only device status monitor before Run.
func (s *Service) SetRelayMonitor(monitor func(context.Context, func(RelayStateUpdate))) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.relayMonitor = monitor
}

// Run observes device-side operations even when no screen/API is being viewed.
func (s *Service) Run(ctx context.Context) {
	s.mu.RLock()
	monitor := s.relayMonitor
	s.mu.RUnlock()
	if monitor == nil {
		<-ctx.Done()
		return
	}
	monitor(ctx, func(update RelayStateUpdate) {
		s.ObserveRelayState(update)
		if update.Error == nil && !update.Inputs {
			s.retryPendingStrikeStop()
		}
	})
}

// ObserveRelayState records actual DO transitions; DI alone never starts a
// report. Each externally controlled output has an independent lifecycle.
func (s *Service) ObserveRelayState(update RelayStateUpdate) {
	if update.Inputs && update.Error == nil {
		return
	}
	if update.Time.IsZero() {
		update.Time = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	fullyObserved := update.Error == nil
	observed := make(map[string]bool, len(s.order))
	for _, id := range s.screenStrikeChannelIDsLocked() {
		channel := s.channels[id]
		// Monitoring callbacks can wait behind a newer ASCII read. The read wins
		// timestamp ties, but monitor frames from one TCP read must keep their order.
		if !update.Time.After(channel.readAt) || update.Time.Before(channel.relayObservedAt) ||
			(!channel.commandedAt.IsZero() && !update.Time.After(channel.commandedAt)) {
			fullyObserved = false
			continue
		}
		if update.Error != nil {
			channel.relayObservedAt = update.Time
			channel.enabled = false
			channel.actualLevel = "unknown"
			channel.status = "error"
			channel.lastError = update.Error.Error()
			s.cacheChannel(channel.dto())
			observed[id] = true
			continue
		}
		if channel.def.Output < 1 || channel.def.Output > len(update.Values) {
			fullyObserved = false
			continue
		}
		value := update.Values[channel.def.Output-1]
		if value != 0 && value != 1 {
			fullyObserved = false
			continue
		}
		dto := applyActualLevel(channel.dto(), value)
		channel.relayObservedAt = update.Time
		channel.enabled, channel.actualLevel, channel.status = dto.Enabled, dto.ActualLevel, dto.Status
		channel.lastError = ""
		s.cacheChannel(channel.dto())
		observed[id] = true
	}
	if len(observed) == 0 {
		return
	}
	s.syncScreenStrikeActiveLocked()
	// Update all channels before capturing report evidence, so one full snapshot
	// cannot contain a mixture of the old and new states.
	if update.Error == nil {
		for _, id := range s.screenStrikeChannelIDsLocked() {
			channel := s.channels[id]
			if !observed[id] {
				continue
			}
			if !channel.enabled {
				channel.softwareControlled = false
				s.finishExternalReportLocked(id, model.InterferenceReportStatusCompleted, "", update.Time)
			} else if !channel.softwareControlled {
				s.startExternalReportLocked(id, update.Time)
			}
		}
	}
	snapshot := screenStrikeSnapshot{
		state: s.screenStrikeCachedStateLocked(update.Time), fullyObserved: fullyObserved,
	}
	s.finishCompletedReportIfInactiveLocked(snapshot, update.Time)
	s.publishScreenStrikeLocked(s.screenStrikeCachedStateLocked(update.Time))
}

func (s *Service) startExternalReportLocked(id string, startedAt time.Time) {
	if s.reports == nil {
		return
	}
	if _, exists := s.externalReports[id]; exists {
		return
	}
	// A platform command may still own this channel when its relay-side timer
	// has not yet been observed as finished.
	if s.activeReport != nil {
		for _, owned := range s.activeReport.ChannelIDs {
			if owned == id {
				return
			}
		}
	}
	ids := []string{id}
	labels, outputs := s.reportChannelMetadataLocked(ids)
	state := s.screenStrikeCachedStateLocked(startedAt)
	state.DurationSeconds = 0
	state.RemainingSeconds = 0
	state.StartedAt = &startedAt
	report, err := s.reports.CreateRunning(model.InterferenceReport{
		InterferenceReportSummary: model.InterferenceReportSummary{
			Status:        model.InterferenceReportStatusRunning,
			OperationType: model.InterferenceOperationExternal,
			StartedAt:     startedAt,
			ChannelIDs:    ids, ChannelLabels: labels, ChannelOutputs: outputs,
			Summary: interferenceReportSummary(labels, 0),
		},
		StartState: cloneStrikeState(&state),
	})
	if err != nil {
		slog.Warn("创建现场控制干扰报告失败", "channel", id, "error", err)
		return
	}
	s.externalReports[id] = cloneInterferenceReport(report)
}

func (s *Service) finishExternalReportLocked(id string, status model.InterferenceReportStatus, reason string, endedAt time.Time) {
	report, exists := s.externalReports[id]
	if !exists || s.reports == nil {
		return
	}
	if endedAt.Before(report.StartedAt) {
		return
	}
	report.Status = status
	report.EndedAt = &endedAt
	report.DurationSeconds = int64(endedAt.Sub(report.StartedAt) / time.Second)
	report.AbnormalReason = reason
	state := s.screenStrikeCachedStateLocked(endedAt)
	report.EndState = cloneStrikeState(&state)
	if err := s.reports.Update(report); err != nil {
		slog.Warn("更新现场控制干扰报告失败", "channel", id, "error", err)
		return
	}
	delete(s.externalReports, id)
}

func (s *Service) externalStartedAtLocked() *time.Time {
	var startedAt *time.Time
	for id, report := range s.externalReports {
		if channel := s.channels[id]; channel != nil && channel.enabled &&
			(startedAt == nil || report.StartedAt.Before(*startedAt)) {
			value := report.StartedAt
			startedAt = &value
		}
	}
	return startedAt
}
