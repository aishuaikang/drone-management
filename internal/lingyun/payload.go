package lingyun

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"drone-management/internal/model"
)

const (
	protocolName = "lingyun"
	qos          = byte(0)
)

type deviceDefinition struct {
	Type         string
	Abbr         string
	DeviceType   int
	OperationCmd int
}

var deviceDefinitions = []deviceDefinition{
	{Type: model.LingyunDeviceAOA, Abbr: "aoa", DeviceType: 9, OperationCmd: 90000},
	{Type: model.LingyunDeviceDCD, Abbr: "dcd", DeviceType: 11, OperationCmd: 110000},
	{Type: model.LingyunDeviceRemoteID, Abbr: "rid", DeviceType: 102, OperationCmd: 1020000},
}

func definitionByType(deviceType string) (deviceDefinition, bool) {
	for _, def := range deviceDefinitions {
		if def.Type == deviceType {
			return def, true
		}
	}
	return deviceDefinition{}, false
}

func definitionByTopic(providerCode, topic string, settings model.LingyunSettings) (deviceDefinition, model.LingyunDeviceSettings, bool) {
	prefix := fmt.Sprintf("bridge/%s/device_control/", strings.TrimSpace(providerCode))
	if !strings.HasPrefix(topic, prefix) {
		return deviceDefinition{}, model.LingyunDeviceSettings{}, false
	}
	parts := strings.Split(strings.TrimPrefix(topic, prefix), "/")
	if len(parts) != 2 {
		return deviceDefinition{}, model.LingyunDeviceSettings{}, false
	}
	abbr, deviceID := parts[0], strings.TrimSpace(parts[1])
	for _, device := range settings.Devices {
		def, ok := definitionByType(device.Type)
		if ok && def.Abbr == abbr && strings.TrimSpace(device.DeviceID) == deviceID {
			return def, device, true
		}
	}
	return deviceDefinition{}, model.LingyunDeviceSettings{}, false
}

func deviceTopic(settings model.LingyunSettings, def deviceDefinition, device model.LingyunDeviceSettings, name string) string {
	return fmt.Sprintf(
		"bridge/%s/%s/%s/%s",
		strings.TrimSpace(settings.ProviderCode),
		name,
		def.Abbr,
		strings.TrimSpace(device.DeviceID),
	)
}

func controlTopic(settings model.LingyunSettings, def deviceDefinition, device model.LingyunDeviceSettings) string {
	return deviceTopic(settings, def, device, "device_control")
}

func controlResponseTopic(settings model.LingyunSettings, def deviceDefinition, device model.LingyunDeviceSettings) string {
	return deviceTopic(settings, def, device, "device_control_resp")
}

type devicePayload struct {
	ProviderCode    string                  `json:"providerCode"`
	DeviceID        string                  `json:"deviceId"`
	DeviceName      string                  `json:"deviceName"`
	DeviceLongitude float64                 `json:"deviceLongitude"`
	DeviceLatitude  float64                 `json:"deviceLatitude"`
	DeviceAltitude  float64                 `json:"deviceAltitude"`
	DeviceType      int                     `json:"deviceType"`
	InstallMode     int                     `json:"installMode"`
	WorkState       int                     `json:"workState"`
	Extension       deviceExtension         `json:"extension"`
	SupFun          []int                   `json:"supFun"`
	DeviceSpec      model.LingyunDeviceSpec `json:"deviceSpec"`
	ProtocolVersion string                  `json:"ver"`
}

type deviceExtension struct {
	DetectionRange               float64  `json:"detectionRange"`
	HorizontalCoverageStartAngle float64  `json:"horizontalCoverageStartAngle"`
	HorizontalCoverageEndAngle   float64  `json:"horizontalCoverageEndAngle"`
	DetectionFrequency           []string `json:"detectionFrequency,omitempty"`
	BandWidth                    string   `json:"bandWidth"`
}

type statusPayload struct {
	DeviceID   string           `json:"deviceId"`
	WorkState  int              `json:"workState"`
	WorkTemp   float64          `json:"workTemp"`
	AlarmState int              `json:"alarmState"`
	AlarmInfo  *string          `json:"alarmInfo"`
	MobileExt  *mobileExtension `json:"mobileExt,omitempty"`
}

type mobileExtension struct {
	DeviceLongitude float64 `json:"deviceLongitude"`
	DeviceLatitude  float64 `json:"deviceLatitude"`
}

type dataPayload struct {
	DeviceID        string            `json:"deviceId"`
	MsgCnt          int64             `json:"msgCnt"`
	PointTime       int64             `json:"ptTime"`
	ProtocolVersion string            `json:"ver"`
	Objects         []senseDataObject `json:"objects"`
}

type senseDataObject struct {
	ObjectID  string        `json:"objectId"`
	Longitude *float64      `json:"longitude,omitempty"`
	Latitude  *float64      `json:"latitude,omitempty"`
	Altitude  *float64      `json:"altitude,omitempty"`
	Height    *float64      `json:"height,omitempty"`
	Speed     *float64      `json:"speed,omitempty"`
	Time      int64         `json:"time"`
	Extension dataExtension `json:"extension"`
}

type dataExtension struct {
	ObjectType int      `json:"objectType"`
	Channel    string   `json:"channel"`
	BandWidth  string   `json:"bandWidth"`
	UAVModel   string   `json:"uavModel"`
	UAVSN      string   `json:"uavSN"`
	Direction  *float64 `json:"direction,omitempty"`
	PilotLon   *float64 `json:"pilotLon,omitempty"`
	PilotLat   *float64 `json:"pilotLat,omitempty"`
	PilotAlt   *float64 `json:"pilotAlt,omitempty"`
	Angle      *float64 `json:"angle,omitempty"`
	VSpeed     *float64 `json:"vSpeed,omitempty"`
	BaroAlt    *float64 `json:"baroAlt,omitempty"`
	UAVType    *int     `json:"uavType,omitempty"`
	Status     *int     `json:"status,omitempty"`
}

type controlEnvelope struct {
	Head controlHead `json:"head"`
	Data controlData `json:"data"`
}

type controlHead struct {
	MsgNo    int64  `json:"msgNo"`
	DeviceID string `json:"deviceId"`
	Time     int64  `json:"time"`
}

type controlData struct {
	OperationType   int             `json:"operationType"`
	OperationCmd    int             `json:"operationCmd"`
	OperationParams json.RawMessage `json:"operationParams,omitempty"`
}

type controlResponseEnvelope struct {
	Head controlHead         `json:"head"`
	Data controlResponseData `json:"data"`
}

type controlResponseData struct {
	Code          int    `json:"code"`
	Message       string `json:"msg,omitempty"`
	OperationType int    `json:"operationType"`
	OperationCmd  int    `json:"operationCmd"`
}

func buildDevicePayload(settings model.LingyunSettings, def deviceDefinition, device model.LingyunDeviceSettings, reporting bool) devicePayload {
	return devicePayload{
		ProviderCode:    strings.TrimSpace(settings.ProviderCode),
		DeviceID:        strings.TrimSpace(device.DeviceID),
		DeviceName:      strings.TrimSpace(device.DeviceName),
		DeviceLongitude: device.DeviceLongitude,
		DeviceLatitude:  device.DeviceLatitude,
		DeviceAltitude:  device.DeviceAltitude,
		DeviceType:      def.DeviceType,
		InstallMode:     device.InstallMode,
		WorkState:       workState(device.Enabled, reporting),
		Extension: deviceExtension{
			DetectionRange:               device.DetectionRange,
			HorizontalCoverageStartAngle: device.HorizontalCoverageStartAngle,
			HorizontalCoverageEndAngle:   device.HorizontalCoverageEndAngle,
			DetectionFrequency:           append([]string(nil), device.DetectionFrequency...),
			BandWidth:                    firstNonEmpty(device.BandWidth, model.DefaultLingyunBandWidth),
		},
		SupFun:          []int{def.OperationCmd},
		DeviceSpec:      device.DeviceSpec,
		ProtocolVersion: strings.TrimSpace(settings.ProtocolVersion),
	}
}

func buildStatusPayload(device model.LingyunDeviceSettings, reporting bool) statusPayload {
	payload := statusPayload{
		DeviceID:   strings.TrimSpace(device.DeviceID),
		WorkState:  workState(device.Enabled, reporting),
		WorkTemp:   0,
		AlarmState: 0,
	}
	if device.InstallMode == 1 {
		payload.MobileExt = &mobileExtension{
			DeviceLongitude: device.DeviceLongitude,
			DeviceLatitude:  device.DeviceLatitude,
		}
	}
	return payload
}

func buildControlResponse(req controlEnvelope, code int, message string, now time.Time) controlResponseEnvelope {
	if req.Head.Time == 0 {
		req.Head.Time = now.UnixMilli()
	}
	return controlResponseEnvelope{
		Head: req.Head,
		Data: controlResponseData{
			Code:          code,
			Message:       message,
			OperationType: req.Data.OperationType,
			OperationCmd:  req.Data.OperationCmd,
		},
	}
}

func workState(enabled, reporting bool) int {
	if enabled && reporting {
		return 1
	}
	return 0
}
