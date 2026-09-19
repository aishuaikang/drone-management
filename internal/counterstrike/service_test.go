package counterstrike

import (
	"context"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"drone-management/internal/model"
	"drone-management/internal/protocol"
	"drone-management/internal/store"
	"github.com/tjfoc/gmsm/sm4"
)

type fakeTransport struct {
	mu             sync.Mutex
	connected      bool
	connectErr     error
	connects       int
	connectStarted chan struct{}
	connectRelease <-chan struct{}
	subscribes     int
	subscribers    map[string]messageHandler
	published      []fakePublish
	loopback       bool
}

type fakePublish struct {
	topic   string
	payload []byte
}

func (t *fakeTransport) Connect(context.Context, transportConfig) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connects++
	if t.connectStarted != nil {
		select {
		case t.connectStarted <- struct{}{}:
		default:
		}
	}
	if t.connectRelease != nil {
		<-t.connectRelease
	}
	if t.connectErr != nil {
		t.connected = false
		return t.connectErr
	}
	t.connected = true
	return nil
}

func (t *fakeTransport) Subscribe(_ context.Context, topic string, handler messageHandler) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.subscribers == nil {
		t.subscribers = map[string]messageHandler{}
	}
	t.subscribes++
	t.subscribers[topic] = handler
	return nil
}

func (t *fakeTransport) Publish(_ context.Context, topic string, payload []byte) error {
	t.mu.Lock()
	t.published = append(t.published, fakePublish{topic: topic, payload: append([]byte(nil), payload...)})
	handler := t.subscribers[topic]
	loopback := t.loopback
	t.mu.Unlock()
	if loopback && handler != nil {
		handler(topic, append([]byte(nil), payload...))
	}
	return nil
}

func (t *fakeTransport) Disconnect() {
	t.mu.Lock()
	t.connected = false
	t.mu.Unlock()
}

func (t *fakeTransport) Connected() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.connected
}

func (t *fakeTransport) deliver(topic string, payload []byte) {
	t.mu.Lock()
	handler := t.subscribers[topic]
	t.mu.Unlock()
	if handler != nil {
		handler(topic, payload)
	}
}

func (t *fakeTransport) publishes(topic string) []fakePublish {
	t.mu.Lock()
	defer t.mu.Unlock()
	items := make([]fakePublish, 0)
	for _, item := range t.published {
		if item.topic == topic {
			items = append(items, item)
		}
	}
	return items
}

type fakeInterference struct {
	requests []model.ScreenStrikeRequest
}

func (c *fakeInterference) ListChannels() []model.InterferenceChannel {
	return []model.InterferenceChannel{
		{ID: "ch-24", Label: "2.4G", Bands: []string{"2400M"}},
		{ID: "ch-58", Label: "5.8G", Bands: []string{"5800M"}},
	}
}
func (c *fakeInterference) ListChannelsCached() []model.InterferenceChannel { return c.ListChannels() }
func (c *fakeInterference) ScreenStrikeActive() bool {
	return len(c.requests) > 0 && c.requests[len(c.requests)-1].Enabled
}
func (c *fakeInterference) SetScreenStrike(req model.ScreenStrikeRequest) (model.ScreenStrikeState, error) {
	c.requests = append(c.requests, req)
	return model.ScreenStrikeState{Active: req.Enabled}, nil
}

func TestServicePublishesEncryptedRegistrationAndHandlesIdempotentStrikeOpen(t *testing.T) {
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	transport := &fakeTransport{}
	interference := &fakeInterference{}
	settings := validTestSettings()
	service := NewService(
		store.New(10, 10),
		model.UserSettings{CounterStrike: settings},
		WithTransport(transport),
		WithInterferenceController(interference),
		WithNow(func() time.Time { return now }),
	)
	service.tick(context.Background())

	registrations := transport.publishes(registrationTopic(settings))
	if len(registrations) != 1 {
		t.Fatalf("registration publishes = %d, want 1", len(registrations))
	}
	plain := decryptSM4ForTest(t, registrations[0].payload, settings.SM4Key, settings.SM4IV)
	var registration registrationPayload
	if err := json.Unmarshal(plain, &registration); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	if registration.DeviceID != settings.Device.DeviceID || registration.DeviceTypeAbbr != "fffd" {
		t.Fatalf("registration = %#v", registration)
	}

	request := controlEnvelope{
		Cmd: "strike.open", Version: "1.0", TaskID: "task-1", Timestamp: now.UnixMilli(),
		DeviceTypeAbbr: "fffd", DeviceID: settings.Device.DeviceID, Duration: 75,
		Params: strikeParams{FreqList: []string{"2400M", "5800M"}},
	}
	data, _ := json.Marshal(request)
	transport.deliver(controlTopic(settings), data)
	transport.deliver(controlTopic(settings), data)

	if len(interference.requests) != 1 {
		t.Fatalf("strike requests = %d, want one idempotent execution", len(interference.requests))
	}
	got := interference.requests[0]
	if !got.Enabled || got.DurationSeconds != 75 || len(got.ChannelIDs) != 2 {
		t.Fatalf("strike request = %#v", got)
	}
	responses := transport.publishes(controlResponsePath)
	if len(responses) != 2 {
		t.Fatalf("response publishes = %d, want response for original and duplicate", len(responses))
	}
	var response controlResponse
	if err := json.Unmarshal(responses[0].payload, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Cmd != "strike.open.resp" || response.Code != 0 || response.TaskID != "task-1" {
		t.Fatalf("response = %#v", response)
	}
	if got := response.StrikeParams["freqList"]; got == nil {
		t.Fatalf("response freqList is missing: %#v", response.StrikeParams)
	}
}

func TestServiceRejectsUnsupportedPowerWithoutStartingStrike(t *testing.T) {
	transport := &fakeTransport{}
	interference := &fakeInterference{}
	settings := validTestSettings()
	service := NewService(
		store.New(10, 10),
		model.UserSettings{CounterStrike: settings},
		WithTransport(transport),
		WithInterferenceController(interference),
	)
	service.tick(context.Background())
	power := 0.5
	request := controlEnvelope{
		Cmd: "strike.open", Version: "1.0", TaskID: "task-power",
		DeviceTypeAbbr: settings.Device.DeviceTypeAbbr, DeviceID: settings.Device.DeviceID,
		Params: strikeParams{Power: &power},
	}
	data, _ := json.Marshal(request)
	transport.deliver(controlTopic(settings), data)

	if len(interference.requests) != 0 {
		t.Fatalf("strike requests = %d, want no hardware request", len(interference.requests))
	}
	responses := transport.publishes(controlResponsePath)
	if len(responses) != 1 {
		t.Fatalf("response publishes = %d, want 1", len(responses))
	}
	var response controlResponse
	if err := json.Unmarshal(responses[0].payload, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code == 0 || response.TaskID != "task-power" {
		t.Fatalf("response = %#v, want a failure for unsupported power", response)
	}
}

func TestServiceIgnoresOtherDevicesOnLegacyControlTopic(t *testing.T) {
	transport := &fakeTransport{}
	interference := &fakeInterference{}
	settings := validTestSettings()
	service := NewService(
		store.New(10, 10),
		model.UserSettings{CounterStrike: settings},
		WithTransport(transport),
		WithInterferenceController(interference),
	)
	service.tick(context.Background())
	request := controlEnvelope{
		Cmd: "strike.open", Version: "1.0", TaskID: "task-other",
		DeviceTypeAbbr: settings.Device.DeviceTypeAbbr, DeviceID: "fffd-OTHER-000001",
	}
	data, _ := json.Marshal(request)
	transport.deliver(legacyControlPath, data)

	if len(interference.requests) != 0 || len(transport.publishes(controlResponsePath)) != 0 {
		t.Fatalf("other-device legacy command was not ignored: requests=%d responses=%d", len(interference.requests), len(transport.publishes(controlResponsePath)))
	}
	records := service.DebugRecords(protocol.MaxDebugRecords)
	if len(records) == 0 || records[0].Direction != protocol.DebugDirectionInbound || records[0].Outcome != protocol.DebugOutcomeIgnored {
		t.Fatalf("ignored inbound record = %#v", records)
	}
}

func TestProtocolBandMapping(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{input: "900M", want: "915"},
		{input: "915M", want: "915"},
		{input: "1500M", want: "1.5"},
		{input: "2400M", want: "2.4"},
		{input: "5800M", want: "5.8"},
	}
	for _, test := range tests {
		if got := normalizeBand(test.input); got != test.want {
			t.Errorf("normalizeBand(%q) = %q, want %q", test.input, got, test.want)
		}
	}
	if got := protocolBandLabel("915"); got != "915M" {
		t.Fatalf("protocolBandLabel(915) = %q, want 915M", got)
	}
}

func TestConfigurationCompleteRequiresValidSM4Material(t *testing.T) {
	settings := validTestSettings()
	if !counterStrikeConfigurationComplete(settings) {
		t.Fatal("valid settings are not configured")
	}
	settings.SM4Key = "short"
	if counterStrikeConfigurationComplete(settings) {
		t.Fatal("short SM4 key must be rejected")
	}
}

func TestServiceStatusKeepsCompleteConfigurationWhenDisabled(t *testing.T) {
	settings := validTestSettings()
	settings.Enabled = false
	service := NewService(
		store.New(10, 10),
		model.UserSettings{CounterStrike: settings},
		WithTransport(&fakeTransport{}),
	)

	status := service.Status()
	if status.Enabled || !status.Configured || status.Connected || status.Connecting {
		t.Fatalf("disabled configured status = %#v", status)
	}
}

func TestServicePublishDebugSendsPlainAndEncryptedPayloads(t *testing.T) {
	transport := &fakeTransport{connected: true}
	settings := validTestSettings()
	service := NewService(
		store.New(10, 10),
		model.UserSettings{CounterStrike: settings},
		WithTransport(transport),
	)
	transport.connected = true

	plain := []byte(`{"cmd":"debug.plain"}`)
	if err := service.PublishDebug(context.Background(), "platform/debug/plain", plain, false); err != nil {
		t.Fatalf("PublishDebug(plain) error = %v", err)
	}
	encrypted := []byte(`{"cmd":"debug.encrypted"}`)
	if err := service.PublishDebug(context.Background(), "platform/debug/encrypted", encrypted, true); err != nil {
		t.Fatalf("PublishDebug(encrypted) error = %v", err)
	}

	plainPublishes := transport.publishes("platform/debug/plain")
	if len(plainPublishes) != 1 || string(plainPublishes[0].payload) != string(plain) {
		t.Fatalf("plain publish = %#v", plainPublishes)
	}
	encryptedPublishes := transport.publishes("platform/debug/encrypted")
	if len(encryptedPublishes) != 1 {
		t.Fatalf("encrypted publishes = %d, want 1", len(encryptedPublishes))
	}
	if got := decryptSM4ForTest(t, encryptedPublishes[0].payload, settings.SM4Key, settings.SM4IV); string(got) != string(encrypted) {
		t.Fatalf("decrypted encrypted payload = %q, want %q", got, encrypted)
	}
	records := service.DebugRecords(10)
	if len(records) != 2 {
		t.Fatalf("debug records = %#v", records)
	}
	if records[0].Payload != string(encrypted) || records[0].WirePayload != string(encryptedPublishes[0].payload) || records[0].Encoding != protocol.DebugEncodingSM4CBCBase64 {
		t.Fatalf("encrypted debug record = %#v", records[0])
	}
	if records[1].Payload != string(plain) || records[1].WirePayload != "" || records[1].Encoding != protocol.DebugEncodingPlainJSON {
		t.Fatalf("plain debug record = %#v", records[1])
	}
}

func TestServicePublishDebugRequiresConnection(t *testing.T) {
	service := NewService(
		store.New(10, 10),
		model.UserSettings{CounterStrike: validTestSettings()},
		WithTransport(&fakeTransport{}),
	)
	if err := service.PublishDebug(context.Background(), "platform/debug", []byte(`{"cmd":"debug"}`), false); err == nil {
		t.Fatal("PublishDebug should fail when MQTT is disconnected")
	}
	records := service.DebugRecords(1)
	if len(records) != 1 || records[0].Outcome != protocol.DebugOutcomeError || records[0].Message == "" {
		t.Fatalf("failed debug record = %#v", records)
	}
}

func TestServiceDebugLoopbackRecordsSendReceiveAndResponseChronologically(t *testing.T) {
	nowMu := sync.Mutex{}
	nextNow := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	now := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		value := nextNow
		nextNow = nextNow.Add(time.Millisecond)
		return value
	}
	transport := &fakeTransport{}
	settings := validTestSettings()
	service := NewService(
		store.New(10, 10),
		model.UserSettings{CounterStrike: settings},
		WithTransport(transport),
		WithInterferenceController(&fakeInterference{}),
		WithNow(now),
	)
	service.tick(context.Background())
	service.ClearDebugRecords()
	transport.mu.Lock()
	transport.loopback = true
	transport.mu.Unlock()

	request := controlEnvelope{
		Cmd: "strike.close", Version: "1.0", TaskID: "debug-loopback",
		DeviceTypeAbbr: settings.Device.DeviceTypeAbbr, DeviceID: settings.Device.DeviceID,
	}
	payload, _ := json.Marshal(request)
	topic := controlTopic(settings)
	if err := service.PublishDebug(context.Background(), topic, payload, false); err != nil {
		t.Fatalf("PublishDebug() error = %v", err)
	}

	var sent, received, responded model.ProtocolDebugRecord
	var foundSent, foundReceived, foundResponded bool
	for _, record := range service.DebugRecords(protocol.MaxDebugRecords) {
		switch {
		case record.Source == protocol.DebugSourceManual && record.Topic == topic:
			sent, foundSent = record, true
		case record.Direction == protocol.DebugDirectionInbound && record.Topic == topic:
			received, foundReceived = record, true
		case record.Kind == "strike_response":
			responded, foundResponded = record, true
		}
	}
	if !foundSent || !foundReceived || !foundResponded {
		t.Fatalf("loopback records missing: sent=%v received=%v responded=%v records=%#v", foundSent, foundReceived, foundResponded, service.DebugRecords(protocol.MaxDebugRecords))
	}
	if !sent.At.Before(received.At) || !received.At.Before(responded.At) {
		t.Fatalf("record chronology send=%v receive=%v response=%v", sent.At, received.At, responded.At)
	}
}

func TestServiceRecordsInboundControlsAndReconnects(t *testing.T) {
	transport := &fakeTransport{}
	settings := validTestSettings()
	service := NewService(
		store.New(10, 10),
		model.UserSettings{CounterStrike: settings},
		WithTransport(transport),
		WithInterferenceController(&fakeInterference{}),
	)
	service.tick(context.Background())
	request := controlEnvelope{
		Cmd: "strike.close", Version: "1.0", TaskID: "debug-close", Timestamp: time.Now().UnixMilli(),
		DeviceTypeAbbr: settings.Device.DeviceTypeAbbr, DeviceID: settings.Device.DeviceID,
	}
	payload, _ := json.Marshal(request)
	transport.deliver(controlTopic(settings), payload)
	transport.deliver(controlTopic(settings), []byte(`{"cmd":`))
	records := service.DebugRecords(protocol.MaxDebugRecords)
	var received, invalid bool
	for _, record := range records {
		if record.Direction == protocol.DebugDirectionInbound && record.Topic == controlTopic(settings) && record.Outcome == protocol.DebugOutcomeSuccess {
			received = true
		}
		if record.Direction == protocol.DebugDirectionInbound && record.Topic == controlTopic(settings) && record.Outcome == protocol.DebugOutcomeError {
			invalid = true
		}
	}
	if !received || !invalid {
		t.Fatalf("inbound control records missing: received=%v invalid=%v records=%#v", received, invalid, records)
	}

	service.mu.Lock()
	service.nextConnectAttemptAt = time.Now().Add(time.Hour)
	service.mu.Unlock()
	if err := service.Reconnect(); err != nil {
		t.Fatalf("Reconnect() error = %v", err)
	}
	status := service.Status()
	if status.Connected || !status.Connecting || transport.Connected() {
		t.Fatalf("status after reconnect = %#v, transport connected = %v", status, transport.Connected())
	}
	transport.mu.Lock()
	connectsBefore := transport.connects
	subscribesBefore := transport.subscribes
	transport.mu.Unlock()
	service.tick(context.Background())
	status = service.Status()
	if !status.Connected || status.Connecting || status.LastError != "" {
		t.Fatalf("status after reconnect tick = %#v", status)
	}
	transport.mu.Lock()
	connectsAfter := transport.connects
	subscribesAfter := transport.subscribes
	transport.connectErr = errors.New("forced reconnect failure")
	transport.mu.Unlock()
	if connectsAfter != connectsBefore+1 || subscribesAfter != subscribesBefore+2 {
		t.Fatalf("reconnect calls connect=%d->%d subscribe=%d->%d", connectsBefore, connectsAfter, subscribesBefore, subscribesAfter)
	}
	if err := service.Reconnect(); err != nil {
		t.Fatalf("second Reconnect() error = %v", err)
	}
	service.tick(context.Background())
	status = service.Status()
	if status.Connected || status.Connecting || status.LastError != "forced reconnect failure" {
		t.Fatalf("status after failed reconnect = %#v", status)
	}
}

func TestServiceReconnectInvalidatesInFlightConnectFailure(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	transport := &fakeTransport{
		connectErr:     errors.New("stale connect failure"),
		connectStarted: started,
		connectRelease: release,
	}
	service := NewService(
		store.New(10, 10),
		model.UserSettings{CounterStrike: validTestSettings()},
		WithTransport(transport),
	)
	service.mu.RLock()
	initialGeneration := service.connectionGeneration
	service.mu.RUnlock()

	tickDone := make(chan struct{})
	go func() {
		service.tick(context.Background())
		close(tickDone)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for connect")
	}
	reconnectDone := make(chan error, 1)
	go func() { reconnectDone <- service.Reconnect() }()
	deadline := time.Now().Add(time.Second)
	for {
		service.mu.RLock()
		generation := service.connectionGeneration
		service.mu.RUnlock()
		if generation > initialGeneration {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reconnect did not invalidate the in-flight generation")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	select {
	case err := <-reconnectDone:
		if err != nil {
			t.Fatalf("Reconnect() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Reconnect() did not finish")
	}
	select {
	case <-tickDone:
	case <-time.After(time.Second):
		t.Fatal("stale tick did not finish")
	}

	status := service.Status()
	service.mu.RLock()
	subscribed := service.subscribed
	nextAttempt := service.nextConnectAttemptAt
	service.mu.RUnlock()
	if subscribed || !nextAttempt.IsZero() || status.Connected || !status.Connecting || status.LastError != "" {
		t.Fatalf("stale connect rewrote reconnect state: subscribed=%v nextAttempt=%v status=%#v", subscribed, nextAttempt, status)
	}
}

func validTestSettings() model.CounterStrikeSettings {
	return model.CounterStrikeSettingsWithDefaults(model.CounterStrikeSettings{
		Enabled:      true,
		Broker:       "tcp://127.0.0.1:1883",
		ClientID:     "counter-test",
		ProviderCode: "AB",
		BridgeCode:   model.DefaultCounterStrikeBridgeCode,
		SM4Key:       "0123456789abcdef",
		SM4IV:        "abcdef0123456789",
		Device: model.CounterStrikeDeviceSettings{
			Enabled:        true,
			DeviceTypeAbbr: "fffd",
			DeviceID:       "fffd-AB-000001",
			DeviceName:     "Test counter device",
			DeviceSpec: model.LingyunDeviceSpec{
				DevModel: "Test model",
				InstLoc:  "Test location",
			},
		},
	})
}

func decryptSM4ForTest(t *testing.T, encoded []byte, keyText, ivText string) []byte {
	t.Helper()
	ciphertext, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	key, _ := decodeSM4Material(keyText)
	iv, _ := decodeSM4Material(ivText)
	block, err := sm4.NewCipher(key)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)
	padding := int(plain[len(plain)-1])
	return plain[:len(plain)-padding]
}
