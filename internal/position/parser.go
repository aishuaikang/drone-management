package position

import (
	"encoding/json"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"drone-management/internal/diddecrypt"
	"drone-management/internal/model"
)

// ParsedMessage is the normalized result of one ddsT1 line.
type ParsedMessage struct {
	Kind         string
	Position     *model.ScreenPositionTarget
	Location     *model.ScreenDeviceLocationResponse
	EncryptedDID *diddecrypt.Packet
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
		return ParsedMessage{Kind: "device_info"}, true
	case "device_status":
		location, ok := parseDeviceStatus(fields, line, receivedAt)
		if !ok {
			return ParsedMessage{Kind: "device_status"}, true
		}
		return ParsedMessage{Kind: "device_status", Location: &location}, true
	case "RID":
		target, ok := parseRID(fields, line, receivedAt)
		if !ok {
			return ParsedMessage{Kind: "RID"}, true
		}
		return ParsedMessage{Kind: "RID", Position: &target}, true
	case "dji_O":
		result, ok := parseDJIO(line, receivedAt)
		if !ok {
			return ParsedMessage{Kind: "dji_O"}, true
		}
		parsed := ParsedMessage{Kind: "dji_O", EncryptedDID: result.encryptedDID}
		if result.hasTarget {
			parsed.Position = &result.target
		}
		return parsed, true
	default:
		return ParsedMessage{Kind: "unknown"}, true
	}
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
		Valid:      locked && latOK && lngOK && validCoordinate(lat, lng),
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
		return encryptedFallbackParseResult(encryptedDID, raw, receivedAt), true
	}
	if isDJIOSerialOnlyFrame(fields) {
		return encryptedFallbackParseResult(encryptedDID, raw, receivedAt), true
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

func encryptedFallbackParseResult(packet *diddecrypt.Packet, raw string, receivedAt time.Time) djiOParseResult {
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
	return djiOParseResult{target: target, hasTarget: true, encryptedDID: packet}
}

func parseDJIODIDPacket(
	airData string,
	frequency float64,
	rssi float64,
) *diddecrypt.Packet {
	hexStr := normalizeAirDataHex(airData)
	if len(hexStr) != 352 {
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
	if len(hexStr) != 352 {
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
	if len(fields) <= 6 {
		return false
	}
	for _, field := range fields[6:] {
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
	if !validCoordinateRange(lat, lng) {
		return nil
	}
	return &model.ScreenPositionPoint{Latitude: lat, Longitude: lng}
}

func validCoordinate(lat, lng float64) bool {
	return validCoordinateRange(lat, lng) && !(lat == 0 && lng == 0)
}

func validCoordinateRange(lat, lng float64) bool {
	return !math.IsNaN(lat) &&
		!math.IsInf(lat, 0) &&
		!math.IsNaN(lng) &&
		!math.IsInf(lng, 0) &&
		lat >= -90 &&
		lat <= 90 &&
		lng >= -180 &&
		lng <= 180
}

func splitCSVLine(raw string) []string {
	parts := strings.Split(raw, ",")
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
