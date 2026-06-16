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
	var payload devicePayload
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
