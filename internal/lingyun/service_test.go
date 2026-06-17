package lingyun

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"drone-management/internal/model"
	"drone-management/internal/store"
)

type publishedMessage struct {
	topic   string
	payload []byte
}

type fakeTransport struct {
	mu            sync.Mutex
	connected     bool
	published     []publishedMessage
	subscriptions map[string]messageHandler
}

type fakeInterferenceController struct {
	mu       sync.Mutex
	channels []model.InterferenceChannel
	state    model.ScreenStrikeState
	requests []model.ScreenStrikeRequest
	err      error
}

func (c *fakeInterferenceController) ListChannels() []model.InterferenceChannel {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]model.InterferenceChannel, len(c.channels))
	copy(out, c.channels)
	return out
}

func (c *fakeInterferenceController) ScreenStrikeState() model.ScreenStrikeState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *fakeInterferenceController) SetScreenStrike(req model.ScreenStrikeRequest) (model.ScreenStrikeState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, model.ScreenStrikeRequest{
		Enabled:         req.Enabled,
		ChannelIDs:      append([]string(nil), req.ChannelIDs...),
		DurationSeconds: req.DurationSeconds,
	})
	if c.err != nil {
		return c.state, c.err
	}
	c.state.Active = req.Enabled
	c.state.ChannelIDs = append([]string(nil), req.ChannelIDs...)
	c.state.DurationSeconds = req.DurationSeconds
	return c.state, nil
}

func (c *fakeInterferenceController) Requests() []model.ScreenStrikeRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]model.ScreenStrikeRequest, len(c.requests))
	copy(out, c.requests)
	return out
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{subscriptions: map[string]messageHandler{}}
}

func (t *fakeTransport) Connect(context.Context, transportConfig) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connected = true
	return nil
}

func (t *fakeTransport) Subscribe(_ context.Context, topic string, handler messageHandler) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.subscriptions[topic] = handler
	return nil
}

func (t *fakeTransport) Publish(_ context.Context, topic string, payload []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.published = append(t.published, publishedMessage{
		topic:   topic,
		payload: append([]byte(nil), payload...),
	})
	return nil
}

func (t *fakeTransport) Disconnect() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connected = false
}

func (t *fakeTransport) Connected() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.connected
}

func (t *fakeTransport) deliver(topic string, payload []byte) bool {
	t.mu.Lock()
	handler := t.subscriptions[topic]
	t.mu.Unlock()
	if handler == nil {
		return false
	}
	handler(topic, payload)
	return true
}

func (t *fakeTransport) messages() []publishedMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]publishedMessage, len(t.published))
	copy(out, t.published)
	return out
}

func TestServiceTickPublishesRegistrationStatusAndSubscribesControls(t *testing.T) {
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	transport := newFakeTransport()
	service := NewService(
		store.New(10, 10),
		model.UserSettings{Lingyun: testSettings()},
		WithTransport(transport),
		WithNow(func() time.Time { return now }),
	)

	service.tick(context.Background())

	status := service.Status()
	if !status.Enabled || !status.Configured || !status.Connected {
		t.Fatalf("status = %#v", status)
	}
	settings := service.settingsSnapshot()
	for _, device := range settings.Devices {
		def, ok := definitionByType(device.Type)
		if !ok {
			t.Fatalf("missing definition for %s", device.Type)
		}
		topic := controlTopic(settings, def, device)
		if !transport.deliver(topic, []byte(`{"head":{"msgNo":1},"data":{"operationType":1,"operationCmd":`+itoa(def.OperationCmd)+`}}`)) {
			t.Fatalf("missing subscription for %s", topic)
		}
	}
	messages := transport.messages()
	aoaDevice, ok := lingyunDevice(settings, model.LingyunDeviceAOA)
	if !ok {
		t.Fatal("AOA device missing")
	}
	ridDevice, ok := lingyunDevice(settings, model.LingyunDeviceRemoteID)
	if !ok {
		t.Fatal("RID device missing")
	}
	aoaDef, _ := definitionByType(model.LingyunDeviceAOA)
	ridDef, _ := definitionByType(model.LingyunDeviceRemoteID)
	if !hasTopic(messages, deviceTopic(settings, aoaDef, aoaDevice, "device")) ||
		!hasTopic(messages, deviceTopic(settings, ridDef, ridDevice, "device_state")) {
		t.Fatalf("published topics = %#v", messageTopics(messages))
	}
}

func TestServiceRegistrationUsesCurrentDeviceLocation(t *testing.T) {
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	transport := newFakeTransport()
	state := store.New(10, 10)
	state.SetManualDeviceLocationAt(model.GeoPoint{Latitude: 39.1234, Longitude: 116.5678}, now)
	service := NewService(
		state,
		model.UserSettings{Lingyun: testSettings()},
		WithTransport(transport),
		WithNow(func() time.Time { return now }),
	)

	service.tick(context.Background())

	settings := service.settingsSnapshot()
	device, ok := lingyunDevice(settings, model.LingyunDeviceAOA)
	if !ok {
		t.Fatal("AOA device missing")
	}
	def, _ := definitionByType(model.LingyunDeviceAOA)
	message, ok := findMessage(transport.messages(), deviceTopic(settings, def, device, "device"))
	if !ok {
		t.Fatal("AOA registration was not published")
	}
	var payload struct {
		DeviceLongitude float64                  `json:"deviceLongitude"`
		DeviceLatitude  float64                  `json:"deviceLatitude"`
		Extension       detectionDeviceExtension `json:"extension"`
	}
	if err := json.Unmarshal(message.payload, &payload); err != nil {
		t.Fatalf("decode registration payload: %v", err)
	}
	if payload.DeviceLongitude != 116.5678 || payload.DeviceLatitude != 39.1234 {
		t.Fatalf("device location = %.4f/%.4f, want 116.5678/39.1234", payload.DeviceLongitude, payload.DeviceLatitude)
	}
	if payload.Extension.BandWidth != model.DefaultLingyunBandWidth {
		t.Fatalf("registration bandWidth = %q, want %q", payload.Extension.BandWidth, model.DefaultLingyunBandWidth)
	}
}

func TestServiceInterferenceRegistrationUsesLocalChannelBands(t *testing.T) {
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	transport := newFakeTransport()
	controller := &fakeInterferenceController{
		channels: []model.InterferenceChannel{
			{ID: "io1", Bands: []string{"433"}},
			{ID: "io6", Bands: []string{"2.4"}},
			{ID: "io8", Bands: []string{"5.8"}},
			{ID: "reserved", Bands: []string{"9.9"}, Reserved: true},
		},
	}
	service := NewService(
		store.New(10, 10),
		model.UserSettings{Lingyun: testSettings()},
		WithTransport(transport),
		WithInterferenceController(controller),
		WithNow(func() time.Time { return now }),
	)

	service.tick(context.Background())

	settings := service.settingsSnapshot()
	device, ok := lingyunDevice(settings, model.LingyunDeviceInterference)
	if !ok {
		t.Fatal("IFR device missing")
	}
	def, _ := definitionByType(model.LingyunDeviceInterference)
	message, ok := findMessage(transport.messages(), deviceTopic(settings, def, device, "device"))
	if !ok {
		t.Fatal("IFR registration was not published")
	}
	var payload struct {
		Extension interferenceDeviceExtension `json:"extension"`
	}
	if err := json.Unmarshal(message.payload, &payload); err != nil {
		t.Fatalf("decode registration payload: %v", err)
	}
	if strings.Join(payload.Extension.Bands, ",") != "433M,2.4G,5.8G" {
		t.Fatalf("IFR bands = %#v, want local channel bands", payload.Extension.Bands)
	}
}

func TestServiceApplySettingsGeneratesAndReusesClientID(t *testing.T) {
	service := NewService(
		store.New(10, 10),
		model.UserSettings{
			Lingyun: model.LingyunSettings{
				Enabled:      true,
				Broker:       "127.0.0.1:1883",
				ProviderCode: "DPTEST",
			},
		},
		WithTransport(newFakeTransport()),
	)

	first := service.settingsSnapshot().ClientID
	if !strings.HasPrefix(first, model.DefaultLingyunClientIDPrefix) {
		t.Fatalf("client ID = %q, want prefix %q", first, model.DefaultLingyunClientIDPrefix)
	}
	service.ApplySettings(model.UserSettings{
		Lingyun: model.LingyunSettings{
			Enabled:      true,
			Broker:       "127.0.0.1:1883",
			ProviderCode: "DPTEST",
		},
	})
	if second := service.settingsSnapshot().ClientID; second != first {
		t.Fatalf("client ID changed from %q to %q", first, second)
	}
}

func TestServiceControlStopDisablesReportingAndResponds(t *testing.T) {
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	transport := newFakeTransport()
	service := NewService(
		store.New(10, 10),
		model.UserSettings{Lingyun: testSettings()},
		WithTransport(transport),
		WithNow(func() time.Time { return now }),
	)
	service.tick(context.Background())
	settings := service.settingsSnapshot()
	device, ok := lingyunDevice(settings, model.LingyunDeviceDCD)
	if !ok {
		t.Fatal("DCD device missing")
	}
	def, _ := definitionByType(model.LingyunDeviceDCD)
	payload := []byte(`{"head":{"msgNo":7,"deviceId":"DCD01","time":1773281000000},"data":{"operationType":0,"operationCmd":110000}}`)
	if !transport.deliver(controlTopic(settings, def, device), payload) {
		t.Fatal("control topic was not subscribed")
	}

	status := service.Status()
	var dcd model.LingyunDeviceStatus
	for _, item := range status.Devices {
		if item.Type == model.LingyunDeviceDCD {
			dcd = item
			break
		}
	}
	if dcd.ReportingEnabled || dcd.WorkState != 0 || dcd.LastControlResult != "stopped" {
		t.Fatalf("DCD status = %#v", dcd)
	}
	messages := transport.messages()
	last := messages[len(messages)-1]
	if last.topic != deviceTopic(settings, def, device, "device_control_resp") {
		t.Fatalf("response topic = %s", last.topic)
	}
	var response controlResponseEnvelope
	if err := json.Unmarshal(last.payload, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Head.MsgNo != 7 || response.Data.Code != 0 || response.Data.OperationType != 0 {
		t.Fatalf("response = %#v", response)
	}
}

func TestServiceInterferenceControlStartsAndStopsStrike(t *testing.T) {
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	transport := newFakeTransport()
	controller := &fakeInterferenceController{
		channels: []model.InterferenceChannel{
			{ID: "io6", Label: "2.4G", Bands: []string{"2.4"}},
			{ID: "io8", Label: "5.8G", Bands: []string{"5.8"}},
		},
	}
	service := NewService(
		store.New(10, 10),
		model.UserSettings{Lingyun: testSettings()},
		WithTransport(transport),
		WithInterferenceController(controller),
		WithNow(func() time.Time { return now }),
	)
	service.tick(context.Background())

	settings := service.settingsSnapshot()
	device, ok := lingyunDevice(settings, model.LingyunDeviceInterference)
	if !ok {
		t.Fatal("IFR device missing")
	}
	def, _ := definitionByType(model.LingyunDeviceInterference)
	startPayload := []byte(`{"head":{"msgNo":8,"deviceId":"IFR01","time":1773281000000},"data":{"operationType":1,"operationCmd":60002,"operationParams":{"bands":["2.4G","5.8G"],"duration":60}}}`)
	if !transport.deliver(controlTopic(settings, def, device), startPayload) {
		t.Fatal("interference control topic was not subscribed")
	}
	requests := controller.Requests()
	if len(requests) != 1 || !requests[0].Enabled || requests[0].DurationSeconds != 60 || strings.Join(requests[0].ChannelIDs, ",") != "io6,io8" {
		t.Fatalf("start requests = %#v", requests)
	}
	status := service.Status()
	var ifr model.LingyunDeviceStatus
	for _, item := range status.Devices {
		if item.Type == model.LingyunDeviceInterference {
			ifr = item
			break
		}
	}
	if ifr.WorkState != 1 || ifr.LastControlResult != "started" {
		t.Fatalf("IFR status after start = %#v", ifr)
	}

	stopPayload := []byte(`{"head":{"msgNo":9,"deviceId":"IFR01","time":1773281005000},"data":{"operationType":0,"operationCmd":60001}}`)
	if !transport.deliver(controlTopic(settings, def, device), stopPayload) {
		t.Fatal("interference control topic was not subscribed")
	}
	requests = controller.Requests()
	if len(requests) != 2 || requests[1].Enabled {
		t.Fatalf("stop requests = %#v", requests)
	}
	status = service.Status()
	for _, item := range status.Devices {
		if item.Type == model.LingyunDeviceInterference {
			if item.WorkState != 0 || item.LastControlResult != "stopped" {
				t.Fatalf("IFR status after stop = %#v", item)
			}
			return
		}
	}
	t.Fatal("IFR status missing")
}

func testSettings() model.LingyunSettings {
	return model.LingyunSettingsWithDefaults(model.LingyunSettings{
		Enabled:      true,
		Broker:       "127.0.0.1:1883",
		ClientID:     "client-1",
		ProviderCode: "DPTEST",
		Devices: []model.LingyunDeviceSettings{
			{Type: model.LingyunDeviceAOA, Enabled: true, DeviceID: "AOA01", DeviceName: "AOA"},
			{Type: model.LingyunDeviceDCD, Enabled: true, DeviceID: "DCD01", DeviceName: "DCD"},
			{Type: model.LingyunDeviceRemoteID, Enabled: true, DeviceID: "RID01", DeviceName: "RID"},
			{Type: model.LingyunDeviceInterference, Enabled: true, DeviceID: "IFR01", DeviceName: "IFR"},
		},
	})
}

func hasTopic(messages []publishedMessage, topic string) bool {
	for _, message := range messages {
		if message.topic == topic {
			return true
		}
	}
	return false
}

func messageTopics(messages []publishedMessage) []string {
	topics := make([]string, 0, len(messages))
	for _, message := range messages {
		topics = append(topics, message.topic)
	}
	return topics
}

func findMessage(messages []publishedMessage, topic string) (publishedMessage, bool) {
	for _, message := range messages {
		if message.topic == topic {
			return message, true
		}
	}
	return publishedMessage{}, false
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
