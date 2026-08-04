package position

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"drone-management/internal/coordinate"
	"drone-management/internal/diddecrypt"
	"drone-management/internal/model"
)

// ParsedMessage is the normalized result of one ddsT1 line.
type ParsedMessage struct {
	Kind         string
	Position     *model.ScreenPositionTarget
	Location     *model.ScreenDeviceLocationResponse
	DeviceInfo   *PositionDeviceInfo
	EncryptedDID *diddecrypt.Packet
	ParseError   string
}

// PositionDeviceInfo is the identity reported by a ddsT1 device_info frame.
type PositionDeviceInfo struct {
	DeviceName   string
	FirmwareTime string
}

type djiOParseResult struct {
	target       model.ScreenPositionTarget
	hasTarget    bool
	encryptedDID *diddecrypt.Packet
}

var ignoredDIDAirDataLogs sync.Map

// ParseLine parses one ddsT1 text message.
func ParseLine(raw string, receivedAt time.Time) (ParsedMessage, bool) {
	line := strings.TrimSpace(raw)
	if line == "" {
		return ParsedMessage{}, false
	}
	if receivedAt.IsZero() {
		receivedAt = time.Now()
	}
	if line == "=" {
		return ParsedMessage{Kind: "heartbeat"}, true
	}

	fields := splitCSVLine(line)
	if len(fields) == 0 {
		return ParsedMessage{}, false
	}

	switch fields[0] {
	case "device_info":
		info, err := parsePositionDeviceInfo(fields)
		if err != nil {
			return ParsedMessage{Kind: "device_info", ParseError: err.Error()}, true
		}
		return ParsedMessage{Kind: "device_info", DeviceInfo: &info}, true
	case "device_status":
		location, ok := parseDeviceStatus(fields, line, receivedAt)
		if !ok {
			return ParsedMessage{Kind: "device_status", ParseError: "invalid device_status fields"}, true
		}
		return ParsedMessage{Kind: "device_status", Location: &location}, true
	case "RID":
		target, ok := parseRID(fields, line, receivedAt)
		if !ok {
			return ParsedMessage{Kind: "RID", ParseError: "invalid RID fields"}, true
		}
		return ParsedMessage{Kind: "RID", Position: &target}, true
	case "RID_GB46750":
		target, err := parseRIDGB46750(fields, line, receivedAt)
		if err != nil {
			return ParsedMessage{Kind: "RID_GB46750", ParseError: err.Error()}, true
		}
		return ParsedMessage{Kind: "RID_GB46750", Position: &target}, true
	case "dji_O":
		result, ok := parseDJIO(line, receivedAt)
		if !ok {
			return ParsedMessage{Kind: "dji_O", ParseError: "invalid dji_O fields"}, true
		}
		parsed := ParsedMessage{Kind: "dji_O", EncryptedDID: result.encryptedDID}
		if result.hasTarget {
			parsed.Position = &result.target
		}
		return parsed, true
	default:
		return ParsedMessage{Kind: "unknown", ParseError: "unknown message prefix " + strconv.Quote(fields[0])}, true
	}
}

func parsePositionDeviceInfo(fields []string) (PositionDeviceInfo, error) {
	if len(fields) < 3 {
		return PositionDeviceInfo{}, fmt.Errorf("device_info field count is %d, expected at least 3", len(fields))
	}
	return PositionDeviceInfo{
		DeviceName:   strings.TrimSpace(fields[1]),
		FirmwareTime: strings.TrimSpace(fields[2]),
	}, nil
}

func parseDeviceStatus(
	fields []string,
	raw string,
	receivedAt time.Time,
) (model.ScreenDeviceLocationResponse, bool) {
	if len(fields) < 6 {
		return model.ScreenDeviceLocationResponse{}, false
	}
	rfTemp := parseOptionalFloat(fields[1])
	mainTemp := parseOptionalFloat(fields[2])
	lng, lngOK := parseFloat(fields[3])
	lat, latOK := parseFloat(fields[4])
	locked := strings.TrimSpace(fields[5]) != "0"

	location := model.ScreenDeviceLocationResponse{
		Source:     "ddsT1",
		UpdatedAt:  &receivedAt,
		Valid:      locked && latOK && lngOK && coordinate.IsValid(lng, lat),
		Locked:     locked,
		RFTempC:    rfTemp,
		MainTempC:  mainTemp,
		LastStatus: raw,
	}
	if location.Valid {
		location.Point = &model.GeoPoint{Latitude: lat, Longitude: lng}
	}
	return location, true
}

func parseRID(fields []string, raw string, receivedAt time.Time) (model.ScreenPositionTarget, bool) {
	if len(fields) < 13 {
		return model.ScreenPositionTarget{}, false
	}
	serial := strings.TrimSpace(fields[1])
	if serial == "" {
		return model.ScreenPositionTarget{}, false
	}
	modelName := strings.TrimSpace(fields[2])
	if modelName == "" {
		modelName = "RID"
	}
	home := parseLatLngPair(fields[3], fields[4])
	drone := parseLatLngPair(fields[5], fields[6])
	height := parseOptionalFloat(fields[7])
	altitude := parseOptionalFloat(fields[8])
	frequency := parseFloatDefault(fields[9])
	speed := parseOptionalFloat(fields[10])
	rssi := parseFloatDefault(fields[11])

	data := map[string]string{
		"model":      fields[2],
		"deviceTime": fields[12],
	}
	dataJSON, _ := json.Marshal(data)
	target := model.ScreenPositionTarget{
		Serial:           serial,
		Model:            modelName,
		Source:           "RID",
		Frequency:        frequency,
		RSSI:             rssi,
		Drone:            drone,
		Home:             home,
		Height:           height,
		Altitude:         altitude,
		Speed:            speed,
		TrajectorySpeed:  speed,
		TrajectoryHeight: height,
		Cracked:          true,
		FirstSeen:        receivedAt,
		LastSeen:         receivedAt,
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "RID",
			ReceivedAt: receivedAt,
			Serial:     serial,
			Model:      modelName,
			Frequency:  frequency,
			RSSI:       rssi,
			Cracked:    true,
			Raw:        raw,
			Data:       dataJSON,
		},
	}
	return target, true
}

func parseRIDGB46750(fields []string, raw string, receivedAt time.Time) (model.ScreenPositionTarget, error) {
	if len(fields) != 18 && len(fields) != 19 {
		return model.ScreenPositionTarget{}, fmt.Errorf("RID_GB46750 field count is %d, expected 18 or 19", len(fields))
	}
	serial := strings.TrimSpace(fields[1])
	if serial == "" {
		return model.ScreenPositionTarget{}, fmt.Errorf("RID_GB46750 product_id is empty")
	}
	if err := validateOptionalInt(fields[3], "uav_category"); err != nil {
		return model.ScreenPositionTarget{}, err
	}
	stationLat, err := parseOptionalFloatStrict(fields[4])
	if err != nil {
		return model.ScreenPositionTarget{}, fmt.Errorf("invalid station_lat: %w", err)
	}
	stationLon, err := parseOptionalFloatStrict(fields[5])
	if err != nil {
		return model.ScreenPositionTarget{}, fmt.Errorf("invalid station_lon: %w", err)
	}
	uavLat, err := parseOptionalFloatStrict(fields[6])
	if err != nil {
		return model.ScreenPositionTarget{}, fmt.Errorf("invalid uav_lat: %w", err)
	}
	uavLon, err := parseOptionalFloatStrict(fields[7])
	if err != nil {
		return model.ScreenPositionTarget{}, fmt.Errorf("invalid uav_lon: %w", err)
	}
	height, err := parseOptionalFloatStrict(fields[8])
	if err != nil {
		return model.ScreenPositionTarget{}, fmt.Errorf("invalid relative_height: %w", err)
	}
	altitude, err := parseOptionalFloatStrict(fields[9])
	if err != nil {
		return model.ScreenPositionTarget{}, fmt.Errorf("invalid altitude: %w", err)
	}
	speed, err := parseOptionalFloatStrict(fields[10])
	if err != nil {
		return model.ScreenPositionTarget{}, fmt.Errorf("invalid ground_speed: %w", err)
	}
	if _, err := parseOptionalFloatStrict(fields[11]); err != nil {
		return model.ScreenPositionTarget{}, fmt.Errorf("invalid vertical_speed: %w", err)
	}
	if _, err := parseOptionalFloatStrict(fields[12]); err != nil {
		return model.ScreenPositionTarget{}, fmt.Errorf("invalid track_angle: %w", err)
	}
	if err := validateOptionalInt(fields[13], "status"); err != nil {
		return model.ScreenPositionTarget{}, err
	}
	frequency, err := parseOptionalFloatStrict(fields[14])
	if err != nil {
		return model.ScreenPositionTarget{}, fmt.Errorf("invalid frequency: %w", err)
	}
	rssi, err := parseOptionalFloatStrict(fields[15])
	if err != nil {
		return model.ScreenPositionTarget{}, fmt.Errorf("invalid rssi: %w", err)
	}
	if err := validateOptionalInt64(fields[16], "gb_timestamp_ms"); err != nil {
		return model.ScreenPositionTarget{}, err
	}
	if err := validateOptionalInt64(fields[17], "recv_time_ms"); err != nil {
		return model.ScreenPositionTarget{}, err
	}

	modelName := "RID_GB46750"
	productModel := ""
	if len(fields) == 19 {
		productModel = strings.TrimSpace(fields[18])
		if productModel != "" {
			modelName = productModel
		}
	}
	data := map[string]string{
		"productId":          fields[1],
		"registrationIdTail": fields[2],
		"uavCategory":        fields[3],
		"stationLat":         fields[4],
		"stationLon":         fields[5],
		"uavLat":             fields[6],
		"uavLon":             fields[7],
		"relativeHeight":     fields[8],
		"altitude":           fields[9],
		"groundSpeed":        fields[10],
		"verticalSpeed":      fields[11],
		"trackAngle":         fields[12],
		"status":             fields[13],
		"frequency":          fields[14],
		"rssi":               fields[15],
		"gbTimestampMs":      fields[16],
		"recvTimeMs":         fields[17],
		"productModel":       productModel,
	}
	dataJSON, _ := json.Marshal(data)
	target := model.ScreenPositionTarget{
		Serial:           serial,
		Model:            modelName,
		Source:           "RID_GB46750",
		Frequency:        optionalFloatValue(frequency),
		RSSI:             optionalFloatValue(rssi),
		Device:           serial,
		Drone:            optionalCoordinatePoint(uavLat, uavLon),
		Home:             optionalCoordinatePoint(stationLat, stationLon),
		Height:           height,
		Altitude:         altitude,
		Speed:            speed,
		TrajectorySpeed:  speed,
		TrajectoryHeight: height,
		Cracked:          true,
		FirstSeen:        receivedAt,
		LastSeen:         receivedAt,
		LastRecord: model.ScreenPositionLastRecord{
			Type:       "RID_GB46750",
			ReceivedAt: receivedAt,
			Device:     serial,
			Serial:     serial,
			Model:      modelName,
			Frequency:  optionalFloatValue(frequency),
			RSSI:       optionalFloatValue(rssi),
			Cracked:    true,
			Raw:        raw,
			Data:       dataJSON,
		},
	}
	return target, nil
}

func parseDJIO(raw string, receivedAt time.Time) (djiOParseResult, bool) {
	head, airData, _ := strings.Cut(strings.TrimSpace(raw), ";")
	fields := splitCSVLine(head)
	if len(fields) < 15 || fields[0] != "dji_O" {
		return djiOParseResult{}, false
	}

	linkKind := strings.TrimSpace(fields[1])
	encrypted := linkKind == "4"
	frequency := parseFloatDefault(fields[2])
	rssi := parseFloatDefault(fields[3])
	modelName := normalizeModel(fields[4])
	serial := strings.TrimSpace(fields[5])
	encryptedDID := (*diddecrypt.Packet)(nil)
	if encrypted {
		encryptedDID = parseDJIODIDPacket(airData, frequency, rssi)
		if encryptedDID == nil {
			logIgnoredDIDAirData(airData, frequency, rssi)
		}
	}
	if serial == "" {
		return encryptedFallbackParseResult(encryptedDID, airData, raw, receivedAt), true
	}
	if isDJIOSerialOnlyFrame(fields) {
		return serialOnlyDJIOParseResult(fields, airData, encryptedDID, raw, receivedAt), true
	}

	drone := parseLngLatPair(fields[6], fields[7])
	pilot := parseLngLatPair(fields[8], fields[9])
	home := parseLngLatPair(fields[10], fields[11])
	altitude, height := parseDJIAltitudeHeight(fields[12], encrypted)
	speed := parseDJISpeed(fields[13], encrypted)
	gpsTime := strings.TrimSpace(fields[14])
	uuid := ""
	if encrypted && len(fields) >= 16 {
		uuid = strings.TrimSpace(fields[15])
	}

	correlationID := uuid
	if encryptedDID != nil {
		correlationID = didCorrelationID(encryptedDID.EncryptedID)
	}
	cracked := !encrypted || drone != nil || pilot != nil || home != nil
	data := map[string]string{
		"linkKind":    linkKind,
		"gpsTime":     gpsTime,
		"uuid":        uuid,
		"airData":     strings.TrimSpace(airData),
		"encryptedID": "",
	}
	if encryptedDID != nil {
		data["encryptedID"] = encryptedDID.EncryptedID
	}
	dataJSON, _ := json.Marshal(data)
	source := "dji_O:" + linkKind
	target := model.ScreenPositionTarget{
		CorrelationID:    correlationID,
		Serial:           serial,
		Model:            modelName,
		Source:           source,
		Frequency:        frequency,
		RSSI:             rssi,
		Drone:            drone,
		Pilot:            pilot,
		Home:             home,
		Height:           height,
		Altitude:         altitude,
		Speed:            speed,
		TrajectorySpeed:  speed,
		TrajectoryHeight: height,
		Cracked:          cracked,
		FirstSeen:        receivedAt,
		LastSeen:         receivedAt,
		LastRecord: model.ScreenPositionLastRecord{
			Type:       source,
			ReceivedAt: receivedAt,
			Serial:     serial,
			Model:      modelName,
			Frequency:  frequency,
			RSSI:       rssi,
			Cracked:    cracked,
			Raw:        raw,
			Data:       dataJSON,
		},
	}
	return djiOParseResult{target: target, hasTarget: true, encryptedDID: encryptedDID}, true
}

func serialOnlyDJIOParseResult(
	fields []string,
	airData string,
	packet *diddecrypt.Packet,
	raw string,
	receivedAt time.Time,
) djiOParseResult {
	linkKind := strings.TrimSpace(fields[1])
	frequency := parseFloatDefault(fields[2])
	rssi := parseFloatDefault(fields[3])
	modelName := normalizeModel(fields[4])
	serial := strings.TrimSpace(fields[5])
	gpsTime := ""
	if len(fields) > 14 {
		gpsTime = strings.TrimSpace(fields[14])
	}
	uuid := ""
	if len(fields) > 15 {
		uuid = strings.TrimSpace(fields[15])
	}
	correlationID := uuid
	if packet != nil {
		correlationID = didCorrelationID(packet.EncryptedID)
	}
	data := map[string]string{
		"linkKind":    linkKind,
		"gpsTime":     gpsTime,
		"uuid":        uuid,
		"airData":     strings.TrimSpace(airData),
		"encryptedID": "",
		"serialOnly":  "true",
	}
	if packet != nil {
		data["encryptedID"] = packet.EncryptedID
	}
	dataJSON, _ := json.Marshal(data)
	source := "dji_O:" + linkKind
	cracked := linkKind != "4"
	target := model.ScreenPositionTarget{
		CorrelationID: correlationID,
		Serial:        serial,
		Model:         modelName,
		Source:        source,
		Frequency:     frequency,
		RSSI:          rssi,
		Cracked:       cracked,
		FirstSeen:     receivedAt,
		LastSeen:      receivedAt,
		LastRecord: model.ScreenPositionLastRecord{
			Type:       source,
			ReceivedAt: receivedAt,
			Serial:     serial,
			Model:      modelName,
			Frequency:  frequency,
			RSSI:       rssi,
			Cracked:    cracked,
			Raw:        raw,
			Data:       dataJSON,
		},
	}
	return djiOParseResult{target: target, hasTarget: true, encryptedDID: packet}
}

func encryptedFallbackParseResult(packet *diddecrypt.Packet, airData string, raw string, receivedAt time.Time) djiOParseResult {
	if packet == nil {
		return djiOParseResult{}
	}
	target := diddecrypt.TargetFromDecryptResult(*packet, diddecrypt.DecryptResult{Model: diddecrypt.FallbackModel}, receivedAt, false)
	target.Drone = nil
	target.Pilot = nil
	target.Home = nil
	target.DroneTrajectory = nil
	target.PilotTrajectory = nil
	target.TrajectorySpeed = nil
	target.TrajectoryHeight = nil
	target.LastRecord.Raw = raw
	dataJSON, _ := json.Marshal(map[string]string{
		"linkKind":    "4",
		"airData":     strings.TrimSpace(airData),
		"encryptedID": packet.EncryptedID,
	})
	target.LastRecord.Data = dataJSON
	return djiOParseResult{target: target, hasTarget: true, encryptedDID: packet}
}

func parseDJIODIDPacket(
	airData string,
	frequency float64,
	rssi float64,
) *diddecrypt.Packet {
	hexStr := normalizeAirDataHex(airData)
	if len(hexStr) != 352 && len(hexStr) != 360 {
		return nil
	}
	encryptedID := encryptedIDFromDIDHex(hexStr)
	if encryptedID == "" {
		return nil
	}
	return &diddecrypt.Packet{
		Device:      encryptedID,
		EncryptedID: encryptedID,
		Freq:        frequency,
		RSSI:        rssi,
		Bytes:       hexStr,
	}
}

func logIgnoredDIDAirData(airData string, frequency float64, rssi float64) {
	hexStr := normalizeAirDataHex(airData)
	diagnostics := diagnoseDIDPacket(diddecrypt.Packet{Bytes: hexStr})
	reason := ignoredDIDAirDataReason(hexStr)
	dedupeKey := strings.Join([]string{
		reason,
		diagnostics.RawType,
		diagnostics.RawMagic,
		strconv.Itoa(len(hexStr)),
	}, "|")
	if _, loaded := ignoredDIDAirDataLogs.LoadOrStore(dedupeKey, struct{}{}); loaded {
		return
	}
	slog.Info(
		"dji_O,4 未提取 DID 包",
		"reason", reason,
		"hex_len", len(hexStr),
		"bytes", diagnostics.ByteLen,
		"raw_type", diagnostics.RawType,
		"raw_magic", diagnostics.RawMagic,
		"normalized_ok", diagnostics.NormalizedOK,
		"normalized_type", diagnostics.NormalizedType,
		"normalized_kind", diagnostics.NormalizedKind,
		"frequency", frequency,
		"rssi", rssi,
	)
}

func ignoredDIDAirDataReason(hexStr string) string {
	if hexStr == "" {
		return "empty_or_invalid_hex"
	}
	if len(hexStr) != 352 && len(hexStr) != 360 {
		return "unexpected_length"
	}
	if encryptedIDFromDIDHex(hexStr) == "" {
		return "missing_magic_or_id"
	}
	return "unknown"
}

func encryptedIDFromDIDHex(hexStr string) string {
	if len(hexStr) < 20 {
		return ""
	}
	magic := strings.ToLower(hexStr[4:12])
	if magic != "494e4650" && magic != "43525950" {
		return ""
	}
	return strings.ToLower(hexStr[12:20])
}

func isDJIOSerialOnlyFrame(fields []string) bool {
	if len(fields) < 14 {
		return false
	}
	for _, field := range fields[6:14] {
		if !isEmptyDJIOValue(field) {
			return false
		}
	}
	return true
}

func isEmptyDJIOValue(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	for _, part := range strings.Split(raw, "|") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, ok := parseFloat(part)
		if !ok || value != 0 {
			return false
		}
	}
	return true
}

func normalizeAirDataHex(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	tokens := strings.FieldsFunc(raw, isAirDataTokenSeparator)
	if len(tokens) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.Grow(len(raw))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if strings.HasPrefix(token, "0x") || strings.HasPrefix(token, "0X") {
			token = token[2:]
		}
		if token == "" {
			return ""
		}
		for index := 0; index < len(token); index++ {
			if !isHexByte(token[index]) {
				return ""
			}
		}
		token = strings.ToLower(token)
		if len(token) == 1 {
			builder.WriteByte('0')
			builder.WriteString(token)
			continue
		}
		if len(token)%2 != 0 {
			return ""
		}
		builder.WriteString(token)
	}
	if builder.Len() == 0 || builder.Len()%2 != 0 {
		return ""
	}
	return builder.String()
}

func isAirDataTokenSeparator(ch rune) bool {
	switch ch {
	case ' ', '\t', '\r', '\n', ',', ':', '-':
		return true
	default:
		return false
	}
}

func didCorrelationID(encryptedID string) string {
	encryptedID = strings.ToLower(strings.TrimSpace(encryptedID))
	if encryptedID == "" {
		return ""
	}
	return diddecrypt.O4Source + ":" + encryptedID
}

func parseDJIAltitudeHeight(raw string, encrypted bool) (*float64, *float64) {
	parts := strings.Split(raw, "|")
	if len(parts) != 2 {
		return nil, nil
	}
	altitude, altitudeOK := parseFloat(parts[0])
	height, heightOK := parseFloat(parts[1])
	if altitudeOK && !encrypted {
		altitude *= 10
	}
	var altitudePtr *float64
	if altitudeOK {
		altitudePtr = &altitude
	}
	var heightPtr *float64
	if heightOK {
		heightPtr = &height
	}
	return altitudePtr, heightPtr
}

func parseDJISpeed(raw string, encrypted bool) *float64 {
	parts := strings.Split(raw, "|")
	if len(parts) != 3 {
		return nil
	}
	east, eastOK := parseFloat(parts[0])
	north, northOK := parseFloat(parts[1])
	up, upOK := parseFloat(parts[2])
	if !eastOK || !northOK || !upOK {
		return nil
	}
	if !encrypted {
		east /= 100
		north /= 100
		up /= 100
	}
	speed := math.Sqrt(east*east + north*north + up*up)
	return &speed
}

func parseLngLatPair(lngRaw, latRaw string) *model.ScreenPositionPoint {
	lng, lngOK := parseFloat(lngRaw)
	lat, latOK := parseFloat(latRaw)
	if !latOK || !lngOK {
		return nil
	}
	return coordinatePoint(lat, lng)
}

func parseLatLngPair(latRaw, lngRaw string) *model.ScreenPositionPoint {
	lat, latOK := parseFloat(latRaw)
	lng, lngOK := parseFloat(lngRaw)
	if !latOK || !lngOK {
		return nil
	}
	return coordinatePoint(lat, lng)
}

func coordinatePoint(lat, lng float64) *model.ScreenPositionPoint {
	if !coordinate.IsValid(lng, lat) {
		return nil
	}
	return &model.ScreenPositionPoint{Latitude: lat, Longitude: lng}
}

func splitCSVLine(raw string) []string {
	parts := strings.Split(strings.TrimSuffix(strings.TrimSpace(raw), ";"), ",")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	return parts
}

func parseOptionalFloat(raw string) *float64 {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	value, ok := parseFloat(raw)
	if !ok {
		return nil
	}
	return &value
}

func parseOptionalFloatStrict(raw string) (*float64, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	value, ok := parseFloat(raw)
	if !ok {
		return nil, fmt.Errorf("invalid number %q", raw)
	}
	return &value, nil
}

func validateOptionalInt(raw, fieldName string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if _, err := strconv.Atoi(raw); err != nil {
		return fmt.Errorf("invalid %s %q", fieldName, raw)
	}
	return nil
}

func validateOptionalInt64(raw, fieldName string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if _, err := strconv.ParseInt(raw, 10, 64); err != nil {
		return fmt.Errorf("invalid %s %q", fieldName, raw)
	}
	return nil
}

func optionalCoordinatePoint(lat, lon *float64) *model.ScreenPositionPoint {
	if lat == nil || lon == nil {
		return nil
	}
	return coordinatePoint(*lat, *lon)
}

func optionalFloatValue(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func parseFloatDefault(raw string) float64 {
	value, _ := parseFloat(raw)
	return value
}

func parseFloat(raw string) (float64, bool) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

func normalizeModel(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "Unknown"
	}
	return value
}

func isHexByte(ch byte) bool {
	return (ch >= '0' && ch <= '9') ||
		(ch >= 'a' && ch <= 'f') ||
		(ch >= 'A' && ch <= 'F')
}

func toLowerHexByte(ch byte) byte {
	if ch >= 'A' && ch <= 'F' {
		return ch + ('a' - 'A')
	}
	return ch
}
