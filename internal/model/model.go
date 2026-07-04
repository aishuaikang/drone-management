// Package model defines API DTOs and runtime event payloads.
package model

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"math"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"
)

// LocaleMeta describes supported frontend locales.
type LocaleMeta struct {
	Default    string   `json:"defaultLocale"`
	Supported  []string `json:"supportedLocales"`
	Namespaces []string `json:"namespaces"`
}

// LicenseInfo describes the local license status and decoded license metadata.
type LicenseInfo struct {
	DeviceSN      string     `json:"deviceSn,omitempty"`
	Customer      string     `json:"customer,omitempty"`
	IssuedAt      *time.Time `json:"issuedAt,omitempty"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	IsPermanent   bool       `json:"isPermanent"`
	RemainingDays int        `json:"remainingDays,omitempty"`
	Valid         bool       `json:"valid"`
	Code          string     `json:"code,omitempty"`
	Message       string     `json:"message,omitempty"`
}

// LicenseUploadResponse returns the latest license status after activation.
type LicenseUploadResponse struct {
	License LicenseInfo `json:"license"`
	Message string      `json:"message"`
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
	IntrusionRetentionDays    *int                          `json:"intrusionRetentionDays,omitempty"`
	ScreenTitle               string                        `json:"screenTitle,omitempty"`
	PositionExpireSeconds     *int                          `json:"positionExpireSeconds,omitempty"`
	PositionTCPPort           *int                          `json:"positionTCPPort,omitempty"`
	FPVTCPPort                *int                          `json:"fpvTCPPort,omitempty"`
	Lingyun                   LingyunSettings               `json:"lingyun,omitempty"`
	ScreenStrikeChannelLabels []string                      `json:"screenStrikeChannelLabels,omitempty"`
	ScreenStrikeUnattended    *ScreenStrikeUnattendedConfig `json:"screenStrikeUnattended,omitempty"`
	WarningZoneEnabled        *bool                         `json:"warningZoneEnabled,omitempty"`
	WarningZoneRadiusMeters   *float64                      `json:"warningZoneRadiusMeters,omitempty"`
	WarningZones              []WarningZone                 `json:"warningZones,omitempty"`
	Whitelist                 []WhitelistItem               `json:"whitelist,omitempty"`
}

const (
	LingyunDeviceAOA          = "aoa"
	LingyunDeviceDCD          = "dcd"
	LingyunDeviceRemoteID     = "rid"
	LingyunDeviceInterference = "ifr"
)

const (
	DefaultLingyunProtocolVersion       = "V1.3"
	DefaultLingyunPublishMinIntervalSec = 1
	DefaultLingyunRegisterIntervalSec   = 300
	DefaultLingyunStatusIntervalSec     = 10
	DefaultLingyunBandWidth             = "20MHz"
	DefaultLingyunClientIDPrefix        = "drone-management-lingyun-"
	DefaultLingyunDeviceSNPrefix        = "drone-management-"
)

// LingyunSettings stores China Mobile Lingyun protocol configuration.
type LingyunSettings struct {
	Enabled                   bool                    `json:"enabled"`
	Broker                    string                  `json:"broker"`
	ClientID                  string                  `json:"clientId"`
	Username                  string                  `json:"username"`
	Password                  string                  `json:"password"`
	ProviderCode              string                  `json:"providerCode"`
	ProtocolVersion           string                  `json:"protocolVersion"`
	PublishMinIntervalSeconds int                     `json:"publishMinIntervalSeconds"`
	RegisterIntervalSeconds   int                     `json:"registerIntervalSeconds"`
	StatusIntervalSeconds     int                     `json:"statusIntervalSeconds"`
	Devices                   []LingyunDeviceSettings `json:"devices"`
}

// LingyunDeviceSettings stores one logical Lingyun device definition.
type LingyunDeviceSettings struct {
	Type                         string            `json:"type"`
	Enabled                      bool              `json:"enabled"`
	DeviceID                     string            `json:"deviceId"`
	DeviceName                   string            `json:"deviceName"`
	DeviceLongitude              float64           `json:"deviceLongitude"`
	DeviceLatitude               float64           `json:"deviceLatitude"`
	DeviceAltitude               float64           `json:"deviceAltitude"`
	InstallMode                  int               `json:"installMode"`
	DetectionRange               float64           `json:"detectionRange"`
	HorizontalCoverageStartAngle float64           `json:"horizontalCoverageStartAngle"`
	HorizontalCoverageEndAngle   float64           `json:"horizontalCoverageEndAngle"`
	DetectionFrequency           []string          `json:"detectionFrequency"`
	BandWidth                    string            `json:"bandWidth"`
	CountermeasureRange          float64           `json:"countermeasureRange"`
	VerticalCoverageStartAngle   float64           `json:"verticalCoverageStartAngle"`
	VerticalCoverageEndAngle     float64           `json:"verticalCoverageEndAngle"`
	Bands                        []string          `json:"bands"`
	InterferenceTypes            []int             `json:"ifrTypes"`
	AntennaType                  int               `json:"antennaType"`
	ActiveAntennaType            int               `json:"activeAntennaType"`
	DeviceSpec                   LingyunDeviceSpec `json:"deviceSpec"`
}

// LingyunDeviceSpec stores public static device specification fields.
type LingyunDeviceSpec struct {
	DevModel   string `json:"devModel"`
	DevMfr     string `json:"devMfr"`
	DevSN      string `json:"devSN"`
	DevHWVer   string `json:"devHWVer"`
	DevSoftVer string `json:"devSoftVer"`
	InstLoc    string `json:"instLoc"`
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
	settings.Lingyun = LingyunSettingsWithSystemDeviceIdentity(settings.Lingyun)
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

// LingyunSettingsWithDefaults fills optional Lingyun protocol settings.
func LingyunSettingsWithDefaults(settings LingyunSettings) LingyunSettings {
	settings.ClientID = strings.TrimSpace(settings.ClientID)
	settings.ProtocolVersion = DefaultLingyunProtocolVersion
	if settings.PublishMinIntervalSeconds <= 0 {
		settings.PublishMinIntervalSeconds = DefaultLingyunPublishMinIntervalSec
	}
	if settings.RegisterIntervalSeconds <= 0 {
		settings.RegisterIntervalSeconds = DefaultLingyunRegisterIntervalSec
	}
	if settings.StatusIntervalSeconds <= 0 {
		settings.StatusIntervalSeconds = DefaultLingyunStatusIntervalSec
	}

	byType := map[string]LingyunDeviceSettings{}
	for _, device := range settings.Devices {
		device = LingyunDeviceSettingsWithDefaults(device)
		if device.Type == "" {
			continue
		}
		byType[device.Type] = device
	}

	settings.Devices = []LingyunDeviceSettings{
		lingyunDeviceWithOverride(defaultLingyunDevice(LingyunDeviceAOA), byType[LingyunDeviceAOA]),
		lingyunDeviceWithOverride(defaultLingyunDevice(LingyunDeviceDCD), byType[LingyunDeviceDCD]),
		lingyunDeviceWithOverride(defaultLingyunDevice(LingyunDeviceRemoteID), byType[LingyunDeviceRemoteID]),
		lingyunDeviceWithOverride(defaultLingyunDevice(LingyunDeviceInterference), byType[LingyunDeviceInterference]),
	}
	return settings
}

// LingyunSettingsWithGeneratedClientID fills defaults and creates a stable MQTT client ID when omitted.
func LingyunSettingsWithGeneratedClientID(settings LingyunSettings) LingyunSettings {
	settings = LingyunSettingsWithDefaults(settings)
	if settings.ClientID == "" {
		settings.ClientID = NewLingyunClientID()
	}
	return settings
}

// NewLingyunClientID returns a random MQTT client ID for Lingyun connections.
func NewLingyunClientID() string {
	var bytes [6]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return DefaultLingyunClientIDPrefix + hex.EncodeToString(bytes[:])
	}
	return DefaultLingyunClientIDPrefix + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// LingyunSettingsWithSystemDeviceIdentity fills empty Lingyun device IDs and serials from the host MAC address.
func LingyunSettingsWithSystemDeviceIdentity(settings LingyunSettings) LingyunSettings {
	return LingyunSettingsWithDeviceIdentity(settings, NewLingyunDeviceSN())
}

// LingyunSettingsWithDeviceIdentity fills empty Lingyun device IDs and serials with identity.
func LingyunSettingsWithDeviceIdentity(settings LingyunSettings, identity string) LingyunSettings {
	settings = LingyunSettingsWithDefaults(settings)
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return settings
	}
	for index := range settings.Devices {
		settings.Devices[index].DeviceID = identity
		settings.Devices[index].DeviceSpec.DevSN = identity
	}
	return settings
}

// LingyunSettingsWithDeviceLocation fills all logical Lingyun device coordinates with point.
func LingyunSettingsWithDeviceLocation(settings LingyunSettings, point *GeoPoint) LingyunSettings {
	if point == nil || !validLingyunGeoPoint(*point) {
		return settings
	}
	for index := range settings.Devices {
		settings.Devices[index].DeviceLongitude = point.Longitude
		settings.Devices[index].DeviceLatitude = point.Latitude
	}
	return settings
}

func validLingyunGeoPoint(point GeoPoint) bool {
	return !math.IsNaN(point.Latitude) &&
		!math.IsNaN(point.Longitude) &&
		!math.IsInf(point.Latitude, 0) &&
		!math.IsInf(point.Longitude, 0) &&
		point.Latitude >= -90 &&
		point.Latitude <= 90 &&
		point.Longitude >= -180 &&
		point.Longitude <= 180
}

// NewLingyunDeviceSN returns a stable device serial derived from the host MAC address.
func NewLingyunDeviceSN() string {
	return lingyunDeviceSNFromHardwareAddr(firstSystemHardwareAddr())
}

func lingyunDeviceSNFromHardwareAddr(addr net.HardwareAddr) string {
	if len(addr) == 0 || hardwareAddrAllZero(addr) {
		return ""
	}
	return DefaultLingyunDeviceSNPrefix + strings.ToUpper(hex.EncodeToString(addr))
}

func firstSystemHardwareAddr() net.HardwareAddr {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	return stableHardwareAddrFromInterfaces(interfaces)
}

type hardwareAddrCandidate struct {
	name   string
	addr   net.HardwareAddr
	global bool
}

func stableHardwareAddrFromInterfaces(interfaces []net.Interface) net.HardwareAddr {
	candidates := make([]hardwareAddrCandidate, 0, len(interfaces))
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 ||
			len(iface.HardwareAddr) == 0 ||
			hardwareAddrAllZero(iface.HardwareAddr) ||
			hardwareAddrIsMulticast(iface.HardwareAddr) {
			continue
		}
		candidates = append(candidates, hardwareAddrCandidate{
			name:   iface.Name,
			addr:   append(net.HardwareAddr(nil), iface.HardwareAddr...),
			global: hardwareAddrIsGloballyAdministered(iface.HardwareAddr),
		})
	}
	if len(candidates) == 0 {
		return nil
	}
	slices.SortFunc(candidates, func(a, b hardwareAddrCandidate) int {
		if a.global != b.global {
			if a.global {
				return -1
			}
			return 1
		}
		if cmp := slices.Compare(a.addr, b.addr); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.name, b.name)
	})
	return append(net.HardwareAddr(nil), candidates[0].addr...)
}

func hardwareAddrAllZero(addr net.HardwareAddr) bool {
	for _, part := range addr {
		if part != 0 {
			return false
		}
	}
	return true
}

func hardwareAddrIsMulticast(addr net.HardwareAddr) bool {
	return len(addr) > 0 && addr[0]&0x01 != 0
}

func hardwareAddrIsGloballyAdministered(addr net.HardwareAddr) bool {
	return len(addr) > 0 && addr[0]&0x02 == 0
}

// LingyunDeviceSettingsWithDefaults fills one logical device definition.
func LingyunDeviceSettingsWithDefaults(device LingyunDeviceSettings) LingyunDeviceSettings {
	switch device.Type {
	case LingyunDeviceAOA, LingyunDeviceDCD, LingyunDeviceRemoteID, LingyunDeviceInterference:
	default:
		return LingyunDeviceSettings{}
	}
	if device.BandWidth == "" {
		device.BandWidth = DefaultLingyunBandWidth
	}
	if lingyunDetectionRangeUsesDefault(device.DetectionRange) {
		device.DetectionRange = lingyunDefaultDetectionRange(device.Type)
	}
	switch device.InstallMode {
	case 0, 1:
	default:
		device.InstallMode = 0
	}
	if device.HorizontalCoverageStartAngle == 0 && device.HorizontalCoverageEndAngle == 0 {
		device.HorizontalCoverageStartAngle = 0
		device.HorizontalCoverageEndAngle = 360
	}
	if device.Type == LingyunDeviceInterference {
		if lingyunCountermeasureRangeUsesDefault(device.CountermeasureRange) {
			device.CountermeasureRange = lingyunDefaultCountermeasureRange()
		}
		if device.VerticalCoverageStartAngle == 0 && device.VerticalCoverageEndAngle == 0 {
			device.VerticalCoverageStartAngle = -90
			device.VerticalCoverageEndAngle = 90
		}
		if len(device.Bands) == 0 {
			device.Bands = lingyunDefaultInterferenceBands()
		}
		if len(device.InterferenceTypes) == 0 {
			device.InterferenceTypes = []int{0, 1, 2}
		}
		device.AntennaType = 1
		device.ActiveAntennaType = 1
	}
	return device
}

func defaultLingyunDevice(deviceType string) LingyunDeviceSettings {
	device := LingyunDeviceSettings{
		Type:                         deviceType,
		Enabled:                      true,
		DetectionRange:               lingyunDefaultDetectionRange(deviceType),
		HorizontalCoverageStartAngle: 0,
		HorizontalCoverageEndAngle:   360,
		BandWidth:                    DefaultLingyunBandWidth,
		DeviceSpec: LingyunDeviceSpec{
			DevModel:   "Drone Management",
			DevHWVer:   "unknown",
			DevSoftVer: "unknown",
		},
	}
	switch deviceType {
	case LingyunDeviceAOA:
		device.DeviceName = "Drone Management AOA"
	case LingyunDeviceDCD:
		device.DeviceName = "Drone Management 协议破解"
	case LingyunDeviceRemoteID:
		device.DeviceName = "Drone Management RemoteID"
	case LingyunDeviceInterference:
		device.DeviceName = "Drone Management 干扰设备"
		device.CountermeasureRange = lingyunDefaultCountermeasureRange()
		device.VerticalCoverageStartAngle = -90
		device.VerticalCoverageEndAngle = 90
		device.Bands = lingyunDefaultInterferenceBands()
		device.InterferenceTypes = []int{0, 1, 2}
		device.AntennaType = 1
		device.ActiveAntennaType = 1
	}
	device.DetectionFrequency = lingyunDefaultDetectionFrequency(deviceType)
	return device
}

func lingyunDeviceWithOverride(defaults LingyunDeviceSettings, override LingyunDeviceSettings) LingyunDeviceSettings {
	if override.Type == "" {
		return defaults
	}
	override = LingyunDeviceSettingsWithDefaults(override)
	if override.DeviceName == "" {
		override.DeviceName = defaults.DeviceName
	}
	if override.BandWidth == "" {
		override.BandWidth = defaults.BandWidth
	}
	if lingyunDetectionRangeUsesDefault(override.DetectionRange) {
		override.DetectionRange = defaults.DetectionRange
	}
	if lingyunDetectionFrequencyUsesDefault(override.Type, override.DetectionFrequency) {
		override.DetectionFrequency = append([]string(nil), defaults.DetectionFrequency...)
	}
	if override.Type == LingyunDeviceInterference {
		if lingyunCountermeasureRangeUsesDefault(override.CountermeasureRange) {
			override.CountermeasureRange = defaults.CountermeasureRange
		}
		if override.VerticalCoverageStartAngle == 0 && override.VerticalCoverageEndAngle == 0 {
			override.VerticalCoverageStartAngle = defaults.VerticalCoverageStartAngle
			override.VerticalCoverageEndAngle = defaults.VerticalCoverageEndAngle
		}
		if len(override.Bands) == 0 {
			override.Bands = append([]string(nil), defaults.Bands...)
		}
		if len(override.InterferenceTypes) == 0 {
			override.InterferenceTypes = append([]int(nil), defaults.InterferenceTypes...)
		}
	}
	if override.DeviceSpec.DevModel == "" {
		override.DeviceSpec.DevModel = defaults.DeviceSpec.DevModel
	}
	if override.DeviceSpec.DevHWVer == "" {
		override.DeviceSpec.DevHWVer = defaults.DeviceSpec.DevHWVer
	}
	if override.DeviceSpec.DevSoftVer == "" {
		override.DeviceSpec.DevSoftVer = defaults.DeviceSpec.DevSoftVer
	}
	return override
}

func lingyunDefaultDetectionFrequency(deviceType string) []string {
	switch deviceType {
	case LingyunDeviceAOA:
		return []string{"400MHz-8GHz"}
	case LingyunDeviceDCD, LingyunDeviceRemoteID:
		return []string{"2.4GHz", "5.8GHz"}
	default:
		return []string{}
	}
}

func lingyunDefaultCountermeasureRange() float64 {
	return 3000
}

func lingyunCountermeasureRangeUsesDefault(value float64) bool {
	return value <= 0 || value == 1000
}

func lingyunDefaultInterferenceBands() []string {
	return []string{"433M", "915M", "1.2G", "1.4G", "1.5G", "2.4G", "5.2G", "5.8G"}
}

func lingyunDefaultDetectionRange(deviceType string) float64 {
	switch deviceType {
	case LingyunDeviceRemoteID:
		return 3000
	case LingyunDeviceAOA, LingyunDeviceDCD:
		return 5000
	default:
		return 1000
	}
}

func lingyunDetectionRangeUsesDefault(value float64) bool {
	return value <= 0 || value == 1000
}

func lingyunDetectionFrequencyUsesDefault(deviceType string, values []string) bool {
	if len(values) == 0 {
		return true
	}
	switch deviceType {
	case LingyunDeviceAOA:
		return sameLingyunStringList(values, []string{"2.4GHz", "5.8GHz"})
	case LingyunDeviceRemoteID:
		return sameLingyunStringList(values, []string{"2.4GHz"})
	default:
		return false
	}
}

func sameLingyunStringList(values, want []string) bool {
	if len(values) != len(want) {
		return false
	}
	for index := range values {
		if strings.TrimSpace(values[index]) != want[index] {
			return false
		}
	}
	return true
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
	Lingyun             LingyunStatus     `json:"lingyun"`
	ServerTime          time.Time         `json:"serverTime"`
}

// LingyunStatus describes the Lingyun protocol runtime state.
type LingyunStatus struct {
	Enabled    bool                  `json:"enabled"`
	Configured bool                  `json:"configured"`
	Connected  bool                  `json:"connected"`
	Broker     string                `json:"broker,omitempty"`
	LastError  string                `json:"lastError,omitempty"`
	UpdatedAt  *time.Time            `json:"updatedAt,omitempty"`
	Devices    []LingyunDeviceStatus `json:"devices,omitempty"`
}

// LingyunDeviceStatus describes one logical Lingyun device runtime state.
type LingyunDeviceStatus struct {
	Type              string              `json:"type"`
	Abbr              string              `json:"abbr"`
	DeviceID          string              `json:"deviceId,omitempty"`
	Enabled           bool                `json:"enabled"`
	ReportingEnabled  bool                `json:"reportingEnabled"`
	WorkState         int                 `json:"workState"`
	LastRegisterAt    *time.Time          `json:"lastRegisterAt,omitempty"`
	LastStatusAt      *time.Time          `json:"lastStatusAt,omitempty"`
	LastDataAt        *time.Time          `json:"lastDataAt,omitempty"`
	LastControlAt     *time.Time          `json:"lastControlAt,omitempty"`
	LastControlResult string              `json:"lastControlResult,omitempty"`
	LastError         string              `json:"lastError,omitempty"`
	PublishLogs       []LingyunPublishLog `json:"publishLogs,omitempty"`
}

// LingyunPublishLog describes one recent MQTT publish attempt for a logical Lingyun device.
type LingyunPublishLog struct {
	Kind    string    `json:"kind"`
	Topic   string    `json:"topic"`
	Success bool      `json:"success"`
	At      time.Time `json:"at"`
	Error   string    `json:"error,omitempty"`
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

// ScreenStrikeUnattendedConfig persists unattended strike configuration.
type ScreenStrikeUnattendedConfig struct {
	Enabled         bool     `json:"enabled"`
	ChannelIDs      []string `json:"channelIds"`
	DurationSeconds int      `json:"durationSeconds"`
}

// ScreenStrikeUnattendedState describes the unattended strike loop.
type ScreenStrikeUnattendedState struct {
	Enabled         bool       `json:"enabled"`
	ChannelIDs      []string   `json:"channelIds"`
	DurationSeconds int        `json:"durationSeconds"`
	Phase           string     `json:"phase"`
	TargetPresent   bool       `json:"targetPresent"`
	LastCheckedAt   *time.Time `json:"lastCheckedAt,omitempty"`
	NextCheckAt     *time.Time `json:"nextCheckAt,omitempty"`
	LastError       string     `json:"lastError,omitempty"`
}

// ScreenStrikeState describes current screen interference control state.
type ScreenStrikeState struct {
	Active           bool                        `json:"active"`
	ChannelIDs       []string                    `json:"channelIds"`
	DurationSeconds  int                         `json:"durationSeconds"`
	RemainingSeconds int                         `json:"remainingSeconds"`
	StartedAt        *time.Time                  `json:"startedAt,omitempty"`
	Channels         []InterferenceChannel       `json:"channels"`
	Unattended       ScreenStrikeUnattendedState `json:"unattended"`
}

// ScreenStrikeResponse returns screen interference state and a user-facing message.
type ScreenStrikeResponse struct {
	State   ScreenStrikeState `json:"state"`
	Message string            `json:"message"`
}

// InterferenceReportStatus describes one interference report lifecycle.
type InterferenceReportStatus string

// InterferenceOperationType identifies how an interference operation was started.
type InterferenceOperationType string

const (
	InterferenceReportStatusRunning   InterferenceReportStatus = "running"
	InterferenceReportStatusCompleted InterferenceReportStatus = "completed"
	InterferenceReportStatusFailed    InterferenceReportStatus = "failed"
	InterferenceReportStatusAbnormal  InterferenceReportStatus = "abnormal"

	InterferenceOperationManual     InterferenceOperationType = "manual"
	InterferenceOperationUnattended InterferenceOperationType = "unattended"
)

// InterferenceReportSummary is the list item for interference reports.
type InterferenceReportSummary struct {
	ID                       string                    `json:"id"`
	Status                   InterferenceReportStatus  `json:"status"`
	OperationType            InterferenceOperationType `json:"operationType"`
	StartedAt                time.Time                 `json:"startedAt"`
	EndedAt                  *time.Time                `json:"endedAt,omitempty"`
	DurationSeconds          int64                     `json:"durationSeconds"`
	RequestedDurationSeconds int                       `json:"requestedDurationSeconds,omitempty"`
	ChannelIDs               []string                  `json:"channelIds,omitempty"`
	ChannelLabels            []string                  `json:"channelLabels,omitempty"`
	ChannelOutputs           []int                     `json:"channelOutputs,omitempty"`
	Summary                  string                    `json:"summary,omitempty"`
	LastError                string                    `json:"lastError,omitempty"`
	AbnormalReason           string                    `json:"abnormalReason,omitempty"`
	CreatedAt                time.Time                 `json:"createdAt"`
	UpdatedAt                time.Time                 `json:"updatedAt"`
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
