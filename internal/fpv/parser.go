package fpv

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"drone-management/internal/model"
)

var (
	format01Pattern = regexp.MustCompile(`^F(\d{4})R(\d{3})T(\d{2})C(\d{3})$`)
	format2Pattern  = regexp.MustCompile(`^F(\d{4})R(\d{3})T=([^#]{2,20})#C(\d{3})$`)
	format3Pattern  = regexp.MustCompile(`^\$\s*ALM\s+F=(\d{4})MHz\s+R=(\d{3})\s+T=([^\x00\r\n ]{2,20})\x00?\s*$`)
)

// ParseASCII parses A3-F9 ASCII alarm formats 0, 1, 2, and 3.
func ParseASCII(raw string, receivedAt time.Time) (model.ScreenFPVTarget, error) {
	line := strings.TrimSpace(strings.TrimRight(raw, "\x00"))
	if receivedAt.IsZero() {
		receivedAt = time.Now()
	}

	if matches := format01Pattern.FindStringSubmatch(line); matches != nil {
		frequency := mustParseFloat(matches[1])
		rssi := mustParseFloat(matches[2])
		code := matches[3]
		checksum := mustParseInt(matches[4])
		if !validASCIIAlarmChecksum(line, checksum) {
			return model.ScreenFPVTarget{}, fmt.Errorf("invalid checksum")
		}
		signalType := signalTypeFromCode(code)
		valid := signalType == "FPV"
		return fpvTarget("ascii-code", line, frequency, rssi, signalType, valid, "", receivedAt), nil
	}

	if matches := format2Pattern.FindStringSubmatch(line); matches != nil {
		frequency := mustParseFloat(matches[1])
		rssi := mustParseFloat(matches[2])
		signalType := strings.TrimSpace(matches[3])
		checksum := mustParseInt(matches[4])
		if !validASCIIAlarmChecksum(line, checksum) {
			return model.ScreenFPVTarget{}, fmt.Errorf("invalid checksum")
		}
		valid := strings.EqualFold(signalType, "FPV")
		return fpvTarget("ascii-name-checksum", line, frequency, rssi, signalType, valid, "", receivedAt), nil
	}

	if matches := format3Pattern.FindStringSubmatch(line); matches != nil {
		frequency := mustParseFloat(matches[1])
		rssi := mustParseFloat(matches[2])
		signalType := strings.TrimSpace(matches[3])
		valid := strings.EqualFold(signalType, "FPV")
		return fpvTarget("ascii-name", line, frequency, rssi, signalType, valid, "", receivedAt), nil
	}

	return model.ScreenFPVTarget{}, fmt.Errorf("unsupported ascii alarm")
}

// ParseFormat4 parses the 8-byte simplified HEX alarm frame.
func ParseFormat4(frame []byte, receivedAt time.Time) (model.ScreenFPVTarget, error) {
	if len(frame) != 8 {
		return model.ScreenFPVTarget{}, fmt.Errorf("format 4 requires 8 bytes")
	}
	if frame[0] != 0xfe {
		return model.ScreenFPVTarget{}, fmt.Errorf("invalid format 4 header")
	}
	frequency := float64(int(frame[1])<<8 | int(frame[2]))
	rssi := float64(frame[5])
	valid := frame[6] == 0x00
	raw := strings.ToUpper(hex.EncodeToString(frame))
	return fpvTarget("hex-8", raw, frequency, rssi, "FPV", valid, "", receivedAt), nil
}

// ParseFormat5 parses the 16-byte complete HEX alarm frame.
func ParseFormat5(frame []byte, receivedAt time.Time) (model.ScreenFPVTarget, error) {
	if len(frame) != 16 {
		return model.ScreenFPVTarget{}, fmt.Errorf("format 5 requires 16 bytes")
	}
	headerOK := frame[0] == 0x1f && (frame[1] == 0x02 || frame[1] == 0xe2)
	if !headerOK || frame[5] != 0x06 || frame[6] != 0x11 || frame[15] != 0x03 {
		return model.ScreenFPVTarget{}, fmt.Errorf("invalid format 5 envelope")
	}
	expectedChecksum := int(frame[13])<<8 | int(frame[14])
	var actualChecksum int
	for _, value := range frame[2:13] {
		actualChecksum += int(value)
	}
	if actualChecksum != expectedChecksum {
		return model.ScreenFPVTarget{}, fmt.Errorf("invalid checksum")
	}

	frequency := float64(int(frame[8])<<8 | int(frame[9]))
	rssiByte := frame[11]
	if frame[12] != 0 {
		// The guide text and example disagree on D11/D12. Prefer the
		// example-compatible non-zero field while still accepting text-style frames.
		rssiByte = frame[12]
	}
	signalType := signalTypeFromCode(fmt.Sprintf("%02X", frame[7]))
	valid := signalType == "FPV"
	deviceSN := fmt.Sprintf("%02X%02X%02X", frame[2], frame[3], frame[4])
	raw := strings.ToUpper(hex.EncodeToString(frame))
	return fpvTarget("hex-16", raw, frequency, float64(rssiByte), signalType, valid, deviceSN, receivedAt), nil
}

func fpvTarget(
	format string,
	raw string,
	frequency float64,
	rssi float64,
	signalType string,
	valid bool,
	deviceSN string,
	receivedAt time.Time,
) model.ScreenFPVTarget {
	if signalType == "" {
		signalType = "UNKNOWN"
	}
	record := model.ScreenFPVLastRecord{
		Format:     format,
		ReceivedAt: receivedAt,
		Frequency:  frequency,
		RSSI:       rssi,
		SignalType: signalType,
		Valid:      valid,
		DeviceSN:   deviceSN,
		Raw:        raw,
	}
	return model.ScreenFPVTarget{
		Frequency:  frequency,
		RSSI:       rssi,
		SignalType: signalType,
		Valid:      valid,
		DeviceSN:   deviceSN,
		Format:     format,
		FirstSeen:  receivedAt,
		LastSeen:   receivedAt,
		LastRecord: record,
	}
}

func signalTypeFromCode(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "03", "20":
		return "FPV"
	case "00":
		return "UNKNOWN"
	default:
		return "UNKNOWN"
	}
}

func validASCIIAlarmChecksum(line string, expected int) bool {
	checksumIndex := strings.LastIndex(line, "C")
	if checksumIndex <= 0 {
		return false
	}
	var actual int
	for _, value := range []byte(line[:checksumIndex]) {
		actual += int(value)
	}
	return actual%256 == expected
}

func mustParseFloat(raw string) float64 {
	value, _ := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	return value
}

func mustParseInt(raw string) int {
	value, _ := strconv.Atoi(strings.TrimSpace(raw))
	return value
}
