package counterstrike

import (
	"fmt"
	"strings"
	"time"

	"drone-management/internal/model"
)

const (
	protocolName        = "counter-strike-v1"
	controlResponsePath = "platform/counter/strike/up"
	legacyControlPath   = "platform/counter/strike/down"
)

type operatorInfo struct {
	UserID   string `json:"userId,omitempty"`
	UserName string `json:"userName,omitempty"`
}

type strikeParams struct {
	FreqList []string `json:"freqList,omitempty"`
	Power    *float64 `json:"power,omitempty"`
}

type controlEnvelope struct {
	Cmd            string       `json:"cmd"`
	Version        string       `json:"version"`
	TaskID         string       `json:"taskId"`
	Timestamp      int64        `json:"timestamp"`
	DeviceTypeAbbr string       `json:"deviceTypeAbbr"`
	DeviceID       string       `json:"deviceId"`
	Duration       int          `json:"duration,omitempty"`
	Operator       operatorInfo `json:"operator,omitempty"`
	Params         strikeParams `json:"params"`
}

type controlResponse struct {
	Cmd          string         `json:"cmd"`
	TaskID       string         `json:"taskId"`
	DeviceID     string         `json:"deviceId"`
	Timestamp    int64          `json:"timestamp"`
	Code         int            `json:"code"`
	Message      string         `json:"message,omitempty"`
	StrikeParams map[string]any `json:"strikeParams,omitempty"`
}

type registrationPayload struct {
	ProviderCode    string                         `json:"providerCode"`
	DeviceID        string                         `json:"deviceId"`
	DeviceName      string                         `json:"deviceName"`
	DeviceLongitude float64                        `json:"deviceLongitude"`
	DeviceLatitude  float64                        `json:"deviceLatitude"`
	DeviceAltitude  float64                        `json:"deviceAltitude"`
	DeviceTypeAbbr  string                         `json:"deviceTypeAbbr"`
	InstallMode     int                            `json:"installMode"`
	WorkState       int                            `json:"workState"`
	Extension       registrationInterferenceFields `json:"extension"`
	SupFun          []int                          `json:"supFun"`
	DeviceSpec      model.LingyunDeviceSpec        `json:"deviceSpec"`
	Version         string                         `json:"ver"`
}

type registrationInterferenceFields struct {
	InterferenceTypes            []int    `json:"ifrTypes"`
	AntennaType                  int      `json:"antennaType"`
	ActiveAntennaType            int      `json:"activeAntennaType"`
	CountermeasureRange          float64  `json:"countermeasureRange"`
	Bands                        []string `json:"bands"`
	HorizontalCoverageStartAngle float64  `json:"horizontalCoverageStartAngle"`
	HorizontalCoverageEndAngle   float64  `json:"horizontalCoverageEndAngle"`
	VerticalCoverageStartAngle   float64  `json:"verticalCoverageStartAngle"`
	VerticalCoverageEndAngle     float64  `json:"verticalCoverageEndAngle"`
}

type statusPayload struct {
	DeviceID   string                `json:"deviceId"`
	WorkState  int                   `json:"workState"`
	WorkTemp   float64               `json:"workTemp"`
	AlarmState int                   `json:"alarmState"`
	AlarmInfo  *string               `json:"alarmInfo"`
	MobileExt  *mobileExtension      `json:"mobileExt,omitempty"`
	Extension  statusExtensionFields `json:"extension"`
}

type mobileExtension struct {
	DeviceLongitude float64 `json:"deviceLongitude"`
	DeviceLatitude  float64 `json:"deviceLatitude"`
}

type statusExtensionFields struct {
	HorizontalCoverageStartAngle float64 `json:"horizontalCoverageStartAngle"`
	HorizontalCoverageEndAngle   float64 `json:"horizontalCoverageEndAngle"`
	VerticalCoverageStartAngle   float64 `json:"verticalCoverageStartAngle"`
	VerticalCoverageEndAngle     float64 `json:"verticalCoverageEndAngle"`
	DeviceLongitude              float64 `json:"deviceLongitude"`
	DeviceLatitude               float64 `json:"deviceLatitude"`
}

func registrationTopic(settings model.CounterStrikeSettings) string {
	return fmt.Sprintf("bridge/%s/device/%s/%s",
		strings.TrimSpace(settings.BridgeCode),
		strings.TrimSpace(settings.Device.DeviceTypeAbbr),
		strings.TrimSpace(settings.Device.DeviceID),
	)
}

func statusTopic(settings model.CounterStrikeSettings) string {
	return fmt.Sprintf("bridge/%s/device_state/%s/%s",
		strings.TrimSpace(settings.BridgeCode),
		strings.TrimSpace(settings.Device.DeviceTypeAbbr),
		strings.TrimSpace(settings.Device.DeviceID),
	)
}

func controlTopic(settings model.CounterStrikeSettings) string {
	return fmt.Sprintf("platform/%s/counter_device_control/%s/%s",
		strings.TrimSpace(settings.ProviderCode),
		strings.TrimSpace(settings.Device.DeviceTypeAbbr),
		strings.TrimSpace(settings.Device.DeviceID),
	)
}

func buildRegistrationPayload(settings model.CounterStrikeSettings, workState int) registrationPayload {
	device := settings.Device
	version := strings.TrimSpace(settings.ProtocolVersion)
	if !strings.HasPrefix(strings.ToUpper(version), "V") {
		version = "V" + version
	}
	return registrationPayload{
		ProviderCode:    strings.TrimSpace(settings.ProviderCode),
		DeviceID:        strings.TrimSpace(device.DeviceID),
		DeviceName:      strings.TrimSpace(device.DeviceName),
		DeviceLongitude: device.DeviceLongitude,
		DeviceLatitude:  device.DeviceLatitude,
		DeviceAltitude:  device.DeviceAltitude,
		DeviceTypeAbbr:  strings.TrimSpace(device.DeviceTypeAbbr),
		InstallMode:     device.InstallMode,
		WorkState:       workState,
		Extension: registrationInterferenceFields{
			InterferenceTypes:            append([]int(nil), device.InterferenceTypes...),
			AntennaType:                  device.AntennaType,
			ActiveAntennaType:            device.ActiveAntennaType,
			CountermeasureRange:          device.CountermeasureRange,
			Bands:                        append([]string(nil), device.Bands...),
			HorizontalCoverageStartAngle: device.HorizontalCoverageStartAngle,
			HorizontalCoverageEndAngle:   device.HorizontalCoverageEndAngle,
			VerticalCoverageStartAngle:   device.VerticalCoverageStartAngle,
			VerticalCoverageEndAngle:     device.VerticalCoverageEndAngle,
		},
		SupFun:     []int{60001, 60002, 60003},
		DeviceSpec: device.DeviceSpec,
		Version:    version,
	}
}

func buildStatusPayload(device model.CounterStrikeDeviceSettings, workState int) statusPayload {
	payload := statusPayload{
		DeviceID:   strings.TrimSpace(device.DeviceID),
		WorkState:  workState,
		AlarmState: 0,
		Extension: statusExtensionFields{
			HorizontalCoverageStartAngle: device.HorizontalCoverageStartAngle,
			HorizontalCoverageEndAngle:   device.HorizontalCoverageEndAngle,
			VerticalCoverageStartAngle:   device.VerticalCoverageStartAngle,
			VerticalCoverageEndAngle:     device.VerticalCoverageEndAngle,
			DeviceLongitude:              device.DeviceLongitude,
			DeviceLatitude:               device.DeviceLatitude,
		},
	}
	if device.InstallMode == 1 {
		payload.MobileExt = &mobileExtension{
			DeviceLongitude: device.DeviceLongitude,
			DeviceLatitude:  device.DeviceLatitude,
		}
	}
	return payload
}

func buildControlResponse(req controlEnvelope, code int, message string, applied map[string]any, now time.Time) controlResponse {
	cmd := strings.TrimSpace(req.Cmd)
	if cmd == "" {
		cmd = "strike.open"
	}
	return controlResponse{
		Cmd:          cmd + ".resp",
		TaskID:       strings.TrimSpace(req.TaskID),
		DeviceID:     strings.TrimSpace(req.DeviceID),
		Timestamp:    now.UnixMilli(),
		Code:         code,
		Message:      message,
		StrikeParams: applied,
	}
}
