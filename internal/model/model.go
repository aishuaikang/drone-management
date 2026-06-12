// Package model defines API DTOs and runtime event payloads.
package model

import (
	"encoding/json"
	"time"
)

// LocaleMeta describes supported frontend locales.
type LocaleMeta struct {
	Default    string   `json:"defaultLocale"`
	Supported  []string `json:"supportedLocales"`
	Namespaces []string `json:"namespaces"`
}

// OfflineMapStatus describes the currently installed offline map package.
type OfflineMapStatus struct {
	Available  bool       `json:"available"`
	TileCount  int        `json:"tileCount"`
	UploadedAt *time.Time `json:"uploadedAt,omitempty"`
	SourceFile string     `json:"sourceFile,omitempty"`
	Path       string     `json:"path,omitempty"`
	Message    string     `json:"message,omitempty"`
}

// OfflineMapUploadLog describes one stage of an offline map upload.
type OfflineMapUploadLog struct {
	Stage     string    `json:"stage"`
	Message   string    `json:"message"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Detail    string    `json:"detail,omitempty"`
}

// OfflineMapUploadResponse returns offline map installation status after upload.
type OfflineMapUploadResponse struct {
	Map     OfflineMapStatus      `json:"map"`
	Message string                `json:"message"`
	Logs    []OfflineMapUploadLog `json:"logs"`
}

// GeoPoint describes a WGS84 coordinate.
type GeoPoint struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// UserSettings stores operator-managed public settings.
type UserSettings struct {
	IntrusionRetentionDays    *int            `json:"intrusionRetentionDays,omitempty"`
	ScreenTitle               string          `json:"screenTitle,omitempty"`
	PositionExpireSeconds     *int            `json:"positionExpireSeconds,omitempty"`
	PositionTCPPort           *int            `json:"positionTCPPort,omitempty"`
	FPVTCPPort                *int            `json:"fpvTCPPort,omitempty"`
	ScreenStrikeChannelLabels []string        `json:"screenStrikeChannelLabels,omitempty"`
	WarningZoneEnabled        *bool           `json:"warningZoneEnabled,omitempty"`
	WarningZoneRadiusMeters   *float64        `json:"warningZoneRadiusMeters,omitempty"`
	WarningZones              []WarningZone   `json:"warningZones,omitempty"`
	Whitelist                 []WhitelistItem `json:"whitelist,omitempty"`
}

// WarningZone describes a user-defined map circle used to scope live alarms.
type WarningZone struct {
	ID           string    `json:"id"`
	Center       GeoPoint  `json:"center"`
	RadiusMeters float64   `json:"radiusMeters"`
	CreatedAt    time.Time `json:"createdAt,omitempty"`
}

// WhitelistItem describes a target identity allowed on the screen.
type WhitelistItem struct {
	Serial    string    `json:"serial"`
	Model     string    `json:"model,omitempty"`
	Source    string    `json:"source,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

const (
	DefaultIntrusionRetentionDays  = 90
	DefaultPositionExpireSeconds   = 5
	MinPositionExpireSeconds       = 1
	MaxPositionExpireSeconds       = 3600
	MinTCPPort                     = 1
	MaxTCPPort                     = 65535
	DefaultWarningZoneRadiusMeters = 500.0
	MinWarningZoneRadiusMeters     = 10
	MaxWarningZoneRadiusMeters     = 50000
)

// UserSettingsWithDefaults fills optional user settings with public defaults.
func UserSettingsWithDefaults(settings UserSettings) UserSettings {
	if settings.IntrusionRetentionDays == nil {
		days := DefaultIntrusionRetentionDays
		settings.IntrusionRetentionDays = &days
	}
	if settings.PositionExpireSeconds == nil ||
		*settings.PositionExpireSeconds < MinPositionExpireSeconds ||
		*settings.PositionExpireSeconds > MaxPositionExpireSeconds {
		seconds := DefaultPositionExpireSeconds
		settings.PositionExpireSeconds = &seconds
	}
	if settings.Whitelist == nil {
		settings.Whitelist = []WhitelistItem{}
	}
	if settings.PositionTCPPort != nil &&
		(*settings.PositionTCPPort < MinTCPPort || *settings.PositionTCPPort > MaxTCPPort) {
		settings.PositionTCPPort = nil
	}
	if settings.FPVTCPPort != nil &&
		(*settings.FPVTCPPort < MinTCPPort || *settings.FPVTCPPort > MaxTCPPort) {
		settings.FPVTCPPort = nil
	}
	if settings.PositionTCPPort != nil && settings.FPVTCPPort != nil && *settings.PositionTCPPort == *settings.FPVTCPPort {
		settings.PositionTCPPort = nil
		settings.FPVTCPPort = nil
	}
	legacyZones := settings.WarningZones
	if settings.WarningZoneEnabled == nil {
		enabled := false
		if len(legacyZones) > 0 {
			enabled = true
		}
		settings.WarningZoneEnabled = &enabled
	}
	if settings.WarningZoneRadiusMeters == nil ||
		*settings.WarningZoneRadiusMeters < MinWarningZoneRadiusMeters ||
		*settings.WarningZoneRadiusMeters > MaxWarningZoneRadiusMeters {
		radius := DefaultWarningZoneRadiusMeters
		if len(legacyZones) > 0 &&
			legacyZones[0].RadiusMeters >= MinWarningZoneRadiusMeters &&
			legacyZones[0].RadiusMeters <= MaxWarningZoneRadiusMeters {
			radius = legacyZones[0].RadiusMeters
		}
		settings.WarningZoneRadiusMeters = &radius
	}
	settings.WarningZones = nil
	return settings
}

// UserSettingsIntrusionRetentionDays returns the effective intrusion retention setting.
func UserSettingsIntrusionRetentionDays(settings UserSettings) int {
	if settings.IntrusionRetentionDays == nil {
		return DefaultIntrusionRetentionDays
	}
	return *settings.IntrusionRetentionDays
}

// UserSettingsPositionExpireSeconds returns the effective live positioning target TTL setting.
func UserSettingsPositionExpireSeconds(settings UserSettings) int {
	if settings.PositionExpireSeconds == nil {
		return DefaultPositionExpireSeconds
	}
	return *settings.PositionExpireSeconds
}

// ScreenTCPPortRequest updates the ingest TCP listener ports.
type ScreenTCPPortRequest struct {
	PositionTCPPort int `json:"positionTCPPort"`
	FPVTCPPort      int `json:"fpvTCPPort"`
}

// TCPListenerStatus describes one ingest TCP server.
type TCPListenerStatus struct {
	Address         string     `json:"address"`
	Host            string     `json:"host"`
	Port            int        `json:"port"`
	Listening       bool       `json:"listening"`
	ListenError     string     `json:"listenError,omitempty"`
	SourceConnected bool       `json:"sourceConnected"`
	ClientAddress   string     `json:"clientAddress,omitempty"`
	UpdatedAt       *time.Time `json:"updatedAt,omitempty"`
}

// TCPClientStatus describes one outbound TCP client target.
type TCPClientStatus struct {
	Address      string     `json:"address"`
	Host         string     `json:"host"`
	Port         int        `json:"port"`
	Connected    bool       `json:"connected"`
	ConnectError string     `json:"connectError,omitempty"`
	UpdatedAt    *time.Time `json:"updatedAt,omitempty"`
}

// ScreenRuntimeStatus returns the network edition runtime state.
type ScreenRuntimeStatus struct {
	Position            TCPListenerStatus `json:"position"`
	FPV                 TCPListenerStatus `json:"fpv"`
	Interference        TCPClientStatus   `json:"interference"`
	DeviceTargetAddress string            `json:"deviceTargetAddress"`
	FPVVideo            FPVVideoStatus    `json:"fpvVideo"`
}

// FPVVideoStatus describes the configured FPV video playback endpoint.
type FPVVideoStatus struct {
	Enabled         bool       `json:"enabled"`
	PlaybackURL     string     `json:"playbackUrl,omitempty"`
	PlaybackType    string     `json:"playbackType,omitempty"`
	Active          bool       `json:"active"`
	ActiveFrequency int        `json:"activeFrequency,omitempty"`
	ActiveSince     *time.Time `json:"activeSince,omitempty"`
}

// ScreenDeviceLocationResponse returns the latest receiver/device location.
type ScreenDeviceLocationResponse struct {
	Source     string     `json:"source"`
	Point      *GeoPoint  `json:"point,omitempty"`
	UpdatedAt  *time.Time `json:"updatedAt,omitempty"`
	Valid      bool       `json:"valid"`
	Locked     bool       `json:"locked"`
	RFTempC    *float64   `json:"rfTempC,omitempty"`
	MainTempC  *float64   `json:"mainTempC,omitempty"`
	LastStatus string     `json:"lastStatus,omitempty"`
}

// ScreenManualDeviceLocationRequest sets a fallback receiver/device location.
type ScreenManualDeviceLocationRequest struct {
	Point GeoPoint `json:"point"`
}

// ScreenPositionPoint describes a coordinate in a target record.
type ScreenPositionPoint struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// ScreenPositionTrackPoint describes one trajectory sample.
type ScreenPositionTrackPoint struct {
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Speed     *float64  `json:"speed,omitempty"`
	Height    *float64  `json:"height,omitempty"`
	Time      time.Time `json:"time"`
}

// ScreenPositionLastRecord is the latest public parse summary for a target.
type ScreenPositionLastRecord struct {
	Type       string          `json:"type"`
	ReceivedAt time.Time       `json:"receivedAt"`
	Device     string          `json:"device,omitempty"`
	Serial     string          `json:"serial,omitempty"`
	Model      string          `json:"model,omitempty"`
	Frequency  float64         `json:"frequency,omitempty"`
	RSSI       float64         `json:"rssi,omitempty"`
	Cracked    bool            `json:"cracked,omitempty"`
	Raw        string          `json:"raw,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
}

// ScreenPositionTarget is the merged positioning-list target.
type ScreenPositionTarget struct {
	ID                  string                     `json:"id"`
	CorrelationID       string                     `json:"correlationId,omitempty"`
	Serial              string                     `json:"serial"`
	Model               string                     `json:"model"`
	Source              string                     `json:"source"`
	Sources             []string                   `json:"sources,omitempty"`
	Frequency           float64                    `json:"frequency,omitempty"`
	RSSI                float64                    `json:"rssi,omitempty"`
	Device              string                     `json:"device,omitempty"`
	Drone               *ScreenPositionPoint       `json:"drone,omitempty"`
	Pilot               *ScreenPositionPoint       `json:"pilot,omitempty"`
	Home                *ScreenPositionPoint       `json:"home,omitempty"`
	DroneTrajectory     []ScreenPositionTrackPoint `json:"droneTrajectory,omitempty"`
	PilotTrajectory     []ScreenPositionTrackPoint `json:"pilotTrajectory,omitempty"`
	FullDroneTrajectory []ScreenPositionTrackPoint `json:"-"`
	FullPilotTrajectory []ScreenPositionTrackPoint `json:"-"`
	TrajectorySpeed     *float64                   `json:"-"`
	TrajectoryHeight    *float64                   `json:"-"`
	Height              *float64                   `json:"height,omitempty"`
	Altitude            *float64                   `json:"altitude,omitempty"`
	Speed               *float64                   `json:"speed,omitempty"`
	Cracked             bool                       `json:"cracked,omitempty"`
	FirstSeen           time.Time                  `json:"firstSeen"`
	LastSeen            time.Time                  `json:"lastSeen"`
	HitCount            int                        `json:"hitCount"`
	LastRecord          ScreenPositionLastRecord   `json:"lastRecord"`
	PilotDistanceM      *float64                   `json:"pilotDistanceM,omitempty"`
	DroneDistanceM      *float64                   `json:"droneDistanceM,omitempty"`
	DroneDirectionDeg   *float64                   `json:"droneDirectionDeg,omitempty"`
}

// ScreenFPVLastRecord is the latest public parse summary for an FPV signal.
type ScreenFPVLastRecord struct {
	Format     string    `json:"format"`
	ReceivedAt time.Time `json:"receivedAt"`
	Frequency  float64   `json:"frequency"`
	RSSI       float64   `json:"rssi"`
	SignalType string    `json:"signalType"`
	Valid      bool      `json:"valid"`
	DeviceSN   string    `json:"deviceSn,omitempty"`
	Raw        string    `json:"raw,omitempty"`
}

// ScreenFPVTarget is the merged FPV signal-list target.
type ScreenFPVTarget struct {
	ID         string              `json:"id"`
	Frequency  float64             `json:"frequency"`
	RSSI       float64             `json:"rssi"`
	SignalType string              `json:"signalType"`
	Valid      bool                `json:"valid"`
	DeviceSN   string              `json:"deviceSn,omitempty"`
	Format     string              `json:"format"`
	FirstSeen  time.Time           `json:"firstSeen"`
	LastSeen   time.Time           `json:"lastSeen"`
	HitCount   int                 `json:"hitCount"`
	LastRecord ScreenFPVLastRecord `json:"lastRecord"`
}

// FPVVideoRecordStatus describes whether a recorded FPV video file is ready.
type FPVVideoRecordStatus string

const (
	FPVVideoRecordStatusReady  FPVVideoRecordStatus = "ready"
	FPVVideoRecordStatusFailed FPVVideoRecordStatus = "failed"
)

// FPVVideoRecord stores one FPV video recording produced during a viewing session.
type FPVVideoRecord struct {
	ID              string               `json:"id"`
	TargetID        string               `json:"targetId,omitempty"`
	Frequency       float64              `json:"frequency"`
	RSSI            float64              `json:"rssi"`
	SignalType      string               `json:"signalType,omitempty"`
	DeviceSN        string               `json:"deviceSn,omitempty"`
	StartedAt       time.Time            `json:"startedAt"`
	EndedAt         time.Time            `json:"endedAt"`
	DurationSeconds int64                `json:"durationSeconds"`
	Status          FPVVideoRecordStatus `json:"status"`
	FileName        string               `json:"fileName,omitempty"`
	FileSizeBytes   int64                `json:"fileSizeBytes,omitempty"`
	FileURL         string               `json:"fileUrl,omitempty"`
	Error           string               `json:"error,omitempty"`
	LastRecord      ScreenFPVLastRecord  `json:"lastRecord"`
}

// FPVVideoRecordDeleteRequest deletes selected FPV video records and files.
type FPVVideoRecordDeleteRequest struct {
	IDs []string `json:"ids"`
}

// FPVVideoRecordDeleteResponse reports how many FPV video records were deleted.
type FPVVideoRecordDeleteResponse struct {
	Deleted int64 `json:"deleted"`
}

// InterferenceChannel describes one relay-backed interference control channel.
type InterferenceChannel struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Output       int      `json:"output"`
	Bands        []string `json:"bands"`
	Reserved     bool     `json:"reserved"`
	Enabled      bool     `json:"enabled"`
	ActualLevel  string   `json:"actualLevel"`
	DesiredLevel string   `json:"desiredLevel"`
	Status       string   `json:"status"`
	LastError    string   `json:"lastError,omitempty"`
}

// InterferenceChannelStateRequest updates whether an interference channel is enabled.
type InterferenceChannelStateRequest struct {
	Enabled bool `json:"enabled"`
}

// InterferenceChannelStateResponse returns an updated interference channel.
type InterferenceChannelStateResponse struct {
	Channel InterferenceChannel `json:"channel"`
	Message string              `json:"message"`
}

// ScreenStrikeRequest controls the interference channels shown on the screen.
type ScreenStrikeRequest struct {
	Enabled         bool     `json:"enabled"`
	ChannelIDs      []string `json:"channelIds"`
	DurationSeconds int      `json:"durationSeconds"`
}

// ScreenStrikeState describes current screen interference control state.
type ScreenStrikeState struct {
	Active           bool                  `json:"active"`
	ChannelIDs       []string              `json:"channelIds"`
	DurationSeconds  int                   `json:"durationSeconds"`
	RemainingSeconds int                   `json:"remainingSeconds"`
	StartedAt        *time.Time            `json:"startedAt,omitempty"`
	Channels         []InterferenceChannel `json:"channels"`
}

// ScreenStrikeResponse returns screen interference state and a user-facing message.
type ScreenStrikeResponse struct {
	State   ScreenStrikeState `json:"state"`
	Message string            `json:"message"`
}

// InterferenceReportStatus describes one interference report lifecycle.
type InterferenceReportStatus string

const (
	InterferenceReportStatusRunning   InterferenceReportStatus = "running"
	InterferenceReportStatusCompleted InterferenceReportStatus = "completed"
	InterferenceReportStatusFailed    InterferenceReportStatus = "failed"
	InterferenceReportStatusAbnormal  InterferenceReportStatus = "abnormal"
)

// InterferenceReportSummary is the list item for interference reports.
type InterferenceReportSummary struct {
	ID                       string                   `json:"id"`
	Status                   InterferenceReportStatus `json:"status"`
	StartedAt                time.Time                `json:"startedAt"`
	EndedAt                  *time.Time               `json:"endedAt,omitempty"`
	DurationSeconds          int64                    `json:"durationSeconds"`
	RequestedDurationSeconds int                      `json:"requestedDurationSeconds,omitempty"`
	ChannelIDs               []string                 `json:"channelIds,omitempty"`
	ChannelLabels            []string                 `json:"channelLabels,omitempty"`
	ChannelOutputs           []int                    `json:"channelOutputs,omitempty"`
	Summary                  string                   `json:"summary,omitempty"`
	LastError                string                   `json:"lastError,omitempty"`
	AbnormalReason           string                   `json:"abnormalReason,omitempty"`
	CreatedAt                time.Time                `json:"createdAt"`
	UpdatedAt                time.Time                `json:"updatedAt"`
}

// InterferenceReport stores evidence for one screen interference operation.
type InterferenceReport struct {
	InterferenceReportSummary
	Request    ScreenStrikeRequest `json:"request"`
	StartState *ScreenStrikeState  `json:"startState,omitempty"`
	EndState   *ScreenStrikeState  `json:"endState,omitempty"`
}

// InterferenceReportDeleteResponse reports how many interference reports were deleted.
type InterferenceReportDeleteResponse struct {
	Deleted int64 `json:"deleted"`
}

// IntrusionTargetType identifies the archived target source type.
type IntrusionTargetType string

const (
	IntrusionTargetTypePosition IntrusionTargetType = "position"
)

// IntrusionRecord stores a disappeared positioning target.
type IntrusionRecord struct {
	ID                 string                        `json:"id"`
	TargetID           string                        `json:"targetId"`
	TargetType         IntrusionTargetType           `json:"targetType"`
	Model              string                        `json:"model,omitempty"`
	Serial             string                        `json:"serial,omitempty"`
	Device             string                        `json:"device,omitempty"`
	Frequency          float64                       `json:"frequency,omitempty"`
	RSSI               float64                       `json:"rssi,omitempty"`
	FirstSeen          time.Time                     `json:"firstSeen"`
	LastSeen           time.Time                     `json:"lastSeen"`
	DurationSeconds    int64                         `json:"durationSeconds"`
	HitCount           int                           `json:"hitCount"`
	Source             string                        `json:"source,omitempty"`
	Sources            []string                      `json:"sources,omitempty"`
	Cracked            bool                          `json:"cracked,omitempty"`
	DeviceLocation     *ScreenDeviceLocationResponse `json:"deviceLocation,omitempty"`
	Drone              *ScreenPositionPoint          `json:"drone,omitempty"`
	Pilot              *ScreenPositionPoint          `json:"pilot,omitempty"`
	Home               *ScreenPositionPoint          `json:"home,omitempty"`
	DroneTrajectory    []ScreenPositionTrackPoint    `json:"droneTrajectory,omitempty"`
	PilotTrajectory    []ScreenPositionTrackPoint    `json:"pilotTrajectory,omitempty"`
	PilotDistanceM     *float64                      `json:"pilotDistanceM,omitempty"`
	DroneDistanceM     *float64                      `json:"droneDistanceM,omitempty"`
	DroneDirectionDeg  *float64                      `json:"droneDirectionDeg,omitempty"`
	DeviceDirectionDeg *float64                      `json:"deviceDirectionDeg,omitempty"`
	Height             *float64                      `json:"height,omitempty"`
	Altitude           *float64                      `json:"altitude,omitempty"`
	Speed              *float64                      `json:"speed,omitempty"`
	LastRecord         ScreenPositionLastRecord      `json:"lastRecord"`
	ArchivedAt         time.Time                     `json:"archivedAt"`
}

// IntrusionDeleteRequest deletes selected intrusion records.
type IntrusionDeleteRequest struct {
	IDs []string `json:"ids"`
}

// IntrusionDeleteResponse reports how many intrusion records were deleted.
type IntrusionDeleteResponse struct {
	Deleted int64 `json:"deleted"`
}

// Event is an SSE payload wrapper.
type Event struct {
	Type    string    `json:"type"`
	Time    time.Time `json:"time"`
	Payload any       `json:"payload,omitempty"`
}

// ListResponse returns a list with count metadata.
type ListResponse[T any] struct {
	Items      []T  `json:"items"`
	Count      int  `json:"count"`
	HasMore    bool `json:"hasMore,omitempty"`
	NextOffset int  `json:"nextOffset,omitempty"`
}
