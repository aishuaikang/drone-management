package lingyun

import (
	"context"
	"encoding/json"
	"fmt"
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
	mu               sync.Mutex
	connected        bool
	connectErr       error
	connectCalls     int
	published        []publishedMessage
	subscriptions    map[string]messageHandler
	subscribeStarted chan string
	subscribeRelease <-chan struct{}
	publishStarted   chan string
	blockTopic       string
	blockRelease     <-chan struct{}
	blocked          bool
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

func (c *fakeInterferenceController) ListChannelsCached() []model.InterferenceChannel {
	return c.ListChannels()
}

func (c *fakeInterferenceController) ScreenStrikeState() model.ScreenStrikeState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *fakeInterferenceController) ScreenStrikeActive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state.Active
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

type cachedActiveInterferenceController struct {
	active     bool
	stateCalls int
	state      model.ScreenStrikeState
}

func (c *cachedActiveInterferenceController) ListChannels() []model.InterferenceChannel {
	return nil
}

func (c *cachedActiveInterferenceController) ListChannelsCached() []model.InterferenceChannel {
	return nil
}

func (c *cachedActiveInterferenceController) ScreenStrikeState() model.ScreenStrikeState {
	c.stateCalls++
	c.active = c.state.Active
	return c.state
}

func (c *cachedActiveInterferenceController) SetScreenStrike(req model.ScreenStrikeRequest) (model.ScreenStrikeState, error) {
	c.active = req.Enabled
	return model.ScreenStrikeState{Active: req.Enabled}, nil
}

func (c *cachedActiveInterferenceController) ScreenStrikeActive() bool {
	return c.active
}

type cachedChannelInterferenceController struct {
	listCalls int
	cached    []model.InterferenceChannel
}

func (c *cachedChannelInterferenceController) ListChannels() []model.InterferenceChannel {
	c.listCalls++
	return nil
}

func (c *cachedChannelInterferenceController) ScreenStrikeState() model.ScreenStrikeState {
	return model.ScreenStrikeState{}
}

func (c *cachedChannelInterferenceController) SetScreenStrike(req model.ScreenStrikeRequest) (model.ScreenStrikeState, error) {
	return model.ScreenStrikeState{Active: req.Enabled}, nil
}

func (c *cachedChannelInterferenceController) ListChannelsCached() []model.InterferenceChannel {
	return append([]model.InterferenceChannel{}, c.cached...)
}

func (c *cachedChannelInterferenceController) ScreenStrikeActive() bool {
	return false
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{subscriptions: map[string]messageHandler{}}
}

func (t *fakeTransport) Connect(context.Context, transportConfig) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connectCalls++
	if t.connectErr != nil {
		t.connected = false
		return t.connectErr
	}
	t.connected = true
	return nil
}

func (t *fakeTransport) Subscribe(_ context.Context, topic string, handler messageHandler) error {
	if t.subscribeStarted != nil {
		select {
		case t.subscribeStarted <- topic:
		default:
		}
	}
	if t.subscribeRelease != nil {
		<-t.subscribeRelease
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.subscriptions[topic] = handler
	return nil
}

func (t *fakeTransport) Publish(_ context.Context, topic string, payload []byte) error {
	if t.publishStarted != nil {
		select {
		case t.publishStarted <- topic:
		default:
		}
	}
	var release <-chan struct{}
	t.mu.Lock()
	if t.blockTopic != "" && strings.Contains(topic, t.blockTopic) && !t.blocked {
		t.blocked = true
		release = t.blockRelease
	}
	t.mu.Unlock()
	if release != nil {
		<-release
	}
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

func (t *fakeTransport) connectCallCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.connectCalls
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

func (t *fakeTransport) subscriptionTopics() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	topics := make([]string, 0, len(t.subscriptions))
	for topic := range t.subscriptions {
		topics = append(topics, topic)
	}
	return topics
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

	initialStatus := service.Status()
	if !initialStatus.Connecting || initialStatus.Connected {
		t.Fatalf("initial status = %#v, want connecting before first tick", initialStatus)
	}

	service.tick(context.Background())

	status := service.Status()
	if !status.Enabled || !status.Configured || !status.Connected || status.Connecting {
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
	for _, topic := range transport.subscriptionTopics() {
		if !strings.HasPrefix(topic, "bridge/DPTEST/device_control/") ||
			strings.HasPrefix(topic, "platform/") ||
			strings.Contains(topic, "/oe/") {
			t.Fatalf("non-intersection subscription topic = %s", topic)
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
	aoaRegisterTopic := deviceTopic(settings, aoaDef, aoaDevice, "device")
	ridStatusTopic := deviceTopic(settings, ridDef, ridDevice, "device_state")
	waitForPublishedTopicCount(t, transport, aoaRegisterTopic, 1)
	waitForPublishedTopicCount(t, transport, ridStatusTopic, 1)
	messages = transport.messages()
	if !hasTopic(messages, aoaRegisterTopic) ||
		!hasTopic(messages, ridStatusTopic) {
		t.Fatalf("published topics = %#v", messageTopics(messages))
	}
}

func TestServiceBacksOffAfterConnectFailure(t *testing.T) {
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	transport := newFakeTransport()
	transport.connectErr = fmt.Errorf("dial timeout")
	service := NewService(
		store.New(10, 10),
		model.UserSettings{Lingyun: testSettings()},
		WithTransport(transport),
		WithNow(func() time.Time { return now }),
	)

	service.tick(context.Background())

	status := service.Status()
	if status.Connected || status.Connecting || status.LastError == "" {
		t.Fatalf("status after failed connect = %#v, want disconnected with error", status)
	}
	if calls := transport.connectCallCount(); calls != 1 {
		t.Fatalf("connect calls = %d, want 1", calls)
	}

	now = now.Add(time.Second)
	service.tick(context.Background())
	if calls := transport.connectCallCount(); calls != 1 {
		t.Fatalf("connect calls during backoff = %d, want 1", calls)
	}
	status = service.Status()
	if status.Connecting {
		t.Fatalf("status during backoff = %#v, want not connecting", status)
	}

	now = now.Add(defaultMQTTRetryDelay)
	service.tick(context.Background())
	if calls := transport.connectCallCount(); calls != 2 {
		t.Fatalf("connect calls after backoff = %d, want 2", calls)
	}
}

func TestServiceConnectSubscribesControlsInParallel(t *testing.T) {
	started := make(chan string, 4)
	release := make(chan struct{})
	transport := newFakeTransport()
	transport.subscribeStarted = started
	transport.subscribeRelease = release
	service := NewService(
		store.New(10, 10),
		model.UserSettings{Lingyun: testSettings()},
		WithTransport(transport),
	)

	done := make(chan struct{})
	go func() {
		service.tick(context.Background())
		close(done)
	}()

	topics := map[string]struct{}{}
	for i := 0; i < 4; i++ {
		select {
		case topic := <-started:
			topics[topic] = struct{}{}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for parallel subscriptions; started topics = %#v", topics)
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("tick did not finish after releasing subscriptions")
	}
	if len(topics) != 4 {
		t.Fatalf("started subscription topics = %#v, want 4 unique topics", topics)
	}
}

func TestServiceStatusIncludesPublishLogs(t *testing.T) {
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
	device, ok := lingyunDevice(settings, model.LingyunDeviceAOA)
	if !ok {
		t.Fatal("AOA device missing")
	}
	def, _ := definitionByType(model.LingyunDeviceAOA)
	topic := deviceTopic(settings, def, device, "device")
	waitForPublishedTopicCount(t, transport, topic, 1)
	log := waitForDevicePublishLog(t, service, model.LingyunDeviceAOA, "device", topic)
	if !log.Success || !log.At.Equal(now) || log.Error != "" {
		t.Fatalf("publish log = %#v, want successful AOA registration at %s", log, now)
	}
	message, ok := findMessage(transport.messages(), topic)
	if !ok {
		t.Fatalf("published message missing on %s", topic)
	}
	if log.Payload != string(message.payload) {
		t.Fatalf("publish log payload = %s, want %s", log.Payload, string(message.payload))
	}
}

func TestServiceStatusUsesCachedInterferenceState(t *testing.T) {
	controller := &cachedActiveInterferenceController{
		active: true,
		state:  model.ScreenStrikeState{Active: false},
	}
	service := NewService(
		store.New(10, 10),
		model.UserSettings{Lingyun: testSettings()},
		WithInterferenceController(controller),
	)

	status := service.Status()
	var ifrStatus *model.LingyunDeviceStatus
	for index := range status.Devices {
		if status.Devices[index].Type == model.LingyunDeviceInterference {
			ifrStatus = &status.Devices[index]
			break
		}
	}
	if ifrStatus == nil {
		t.Fatalf("IFR device status missing: %#v", status.Devices)
	}
	if ifrStatus.WorkState != 1 {
		t.Fatalf("IFR workState = %d, want 1 from cached active strike state", ifrStatus.WorkState)
	}
	if controller.stateCalls != 0 {
		t.Fatalf("ScreenStrikeState calls = %d, want 0", controller.stateCalls)
	}
}

func TestServiceBackgroundWorkStateUsesCachedInterferenceState(t *testing.T) {
	controller := &cachedActiveInterferenceController{
		active: true,
		state:  model.ScreenStrikeState{Active: false},
	}
	service := NewService(
		store.New(10, 10),
		model.UserSettings{Lingyun: testSettings()},
		WithInterferenceController(controller),
	)
	settings := service.settingsSnapshot()
	device, ok := lingyunDevice(settings, model.LingyunDeviceInterference)
	if !ok {
		t.Fatal("interference device missing")
	}

	if interval := service.statusInterval(settings, device); interval != time.Second {
		t.Fatalf("status interval = %s, want active interference interval", interval)
	}
	if workState := service.currentWorkState(device); workState != 1 {
		t.Fatalf("work state = %d, want cached active state", workState)
	}
	if controller.stateCalls != 0 {
		t.Fatalf("ScreenStrikeState calls = %d, want no synchronous relay refresh", controller.stateCalls)
	}
}

func TestServiceInterferenceBandsUseCachedChannels(t *testing.T) {
	controller := &cachedChannelInterferenceController{
		cached: []model.InterferenceChannel{{Bands: []string{"2.4", "5.8"}}},
	}
	service := NewService(
		store.New(10, 10),
		model.UserSettings{Lingyun: testSettings()},
		WithInterferenceController(controller),
	)

	device := service.deviceWithCurrentInterferenceBands(model.LingyunDeviceSettings{
		Type: model.LingyunDeviceInterference,
	})
	if controller.listCalls != 0 {
		t.Fatalf("ListChannels calls = %d, want 0", controller.listCalls)
	}
	if len(device.Bands) != 2 || device.Bands[0] != "2.4G" || device.Bands[1] != "5.8G" {
		t.Fatalf("bands = %#v, want cached formatted bands", device.Bands)
	}
}

func TestServiceSettingsChangePublishesImmediately(t *testing.T) {
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	nowMu := sync.Mutex{}
	currentNow := now
	setNow := func(next time.Time) {
		nowMu.Lock()
		defer nowMu.Unlock()
		currentNow = next
	}
	nowFunc := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return currentNow
	}
	transport := newFakeTransport()
	service := NewService(
		store.New(10, 10),
		model.UserSettings{Lingyun: testSettings()},
		WithTransport(transport),
		WithNow(nowFunc),
	)

	service.tick(context.Background())

	settings := service.settingsSnapshot()
	device, ok := lingyunDevice(settings, model.LingyunDeviceAOA)
	if !ok {
		t.Fatal("AOA device missing")
	}
	def, _ := definitionByType(model.LingyunDeviceAOA)
	oldTopic := deviceTopic(settings, def, device, "device")
	waitForPublishedTopicCount(t, transport, oldTopic, 1)

	settings.ProviderCode = "DPNEW"
	service.ApplySettings(model.UserSettings{Lingyun: settings})
	setNow(now.Add(time.Second))
	service.tick(context.Background())

	nextSettings := service.settingsSnapshot()
	nextDevice, ok := lingyunDevice(nextSettings, model.LingyunDeviceAOA)
	if !ok {
		t.Fatal("AOA device missing after settings change")
	}
	nextTopic := deviceTopic(nextSettings, def, nextDevice, "device")
	waitForPublishedTopicCount(t, transport, nextTopic, 1)
}

func TestServiceSettingsChangePublishesWhileOldPublishInFlight(t *testing.T) {
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	nowMu := sync.Mutex{}
	currentNow := now
	setNow := func(next time.Time) {
		nowMu.Lock()
		defer nowMu.Unlock()
		currentNow = next
	}
	nowFunc := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return currentNow
	}
	transport := newFakeTransport()
	started := make(chan string, 16)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	transport.publishStarted = started
	transport.blockTopic = "/device/aoa/"
	transport.blockRelease = release
	service := NewService(
		store.New(10, 10),
		model.UserSettings{Lingyun: testSettings()},
		WithTransport(transport),
		WithNow(nowFunc),
	)

	settings := service.settingsSnapshot()
	device, ok := lingyunDevice(settings, model.LingyunDeviceAOA)
	if !ok {
		t.Fatal("AOA device missing")
	}
	def, _ := definitionByType(model.LingyunDeviceAOA)
	oldTopic := deviceTopic(settings, def, device, "device")

	service.tick(context.Background())
	waitForStartedTopics(t, started, oldTopic)

	settings.ProviderCode = "DPNEW"
	service.ApplySettings(model.UserSettings{Lingyun: settings})
	setNow(now.Add(time.Second))
	service.tick(context.Background())

	nextSettings := service.settingsSnapshot()
	nextDevice, ok := lingyunDevice(nextSettings, model.LingyunDeviceAOA)
	if !ok {
		t.Fatal("AOA device missing after settings change")
	}
	nextTopic := deviceTopic(nextSettings, def, nextDevice, "device")
	waitForStartedTopics(t, started, nextTopic)
	waitForPublishedTopicCount(t, transport, nextTopic, 1)
	waitForDevicePublishLog(t, service, model.LingyunDeviceAOA, "device", nextTopic)

	releaseOnce.Do(func() { close(release) })
	waitForPublishedTopicCount(t, transport, oldTopic, 1)
	assertNoDevicePublishLog(t, service, model.LingyunDeviceAOA, "device", oldTopic)
}

func TestServiceTickPublishesDevicesInParallel(t *testing.T) {
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	nowMu := sync.Mutex{}
	currentNow := now
	setNow := func(next time.Time) {
		nowMu.Lock()
		defer nowMu.Unlock()
		currentNow = next
	}
	nowFunc := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return currentNow
	}
	transport := newFakeTransport()
	started := make(chan string, 16)
	release := make(chan struct{})
	transport.publishStarted = started
	transport.blockTopic = "/device/aoa/"
	transport.blockRelease = release
	service := NewService(
		store.New(10, 10),
		model.UserSettings{Lingyun: testSettings()},
		WithTransport(transport),
		WithNow(nowFunc),
	)

	settings := service.settingsSnapshot()
	aoaDevice, ok := lingyunDevice(settings, model.LingyunDeviceAOA)
	if !ok {
		t.Fatal("AOA device missing")
	}
	dcdDevice, ok := lingyunDevice(settings, model.LingyunDeviceDCD)
	if !ok {
		t.Fatal("DCD device missing")
	}
	ridDevice, ok := lingyunDevice(settings, model.LingyunDeviceRemoteID)
	if !ok {
		t.Fatal("RID device missing")
	}
	ifrDevice, ok := lingyunDevice(settings, model.LingyunDeviceInterference)
	if !ok {
		t.Fatal("IFR device missing")
	}
	aoaDef, _ := definitionByType(model.LingyunDeviceAOA)
	dcdDef, _ := definitionByType(model.LingyunDeviceDCD)
	ridDef, _ := definitionByType(model.LingyunDeviceRemoteID)
	ifrDef, _ := definitionByType(model.LingyunDeviceInterference)
	aoaRegisterTopic := deviceTopic(settings, aoaDef, aoaDevice, "device")
	dcdRegisterTopic := deviceTopic(settings, dcdDef, dcdDevice, "device")
	ridRegisterTopic := deviceTopic(settings, ridDef, ridDevice, "device")
	ifrRegisterTopic := deviceTopic(settings, ifrDef, ifrDevice, "device")
	dcdStatusTopic := deviceTopic(settings, dcdDef, dcdDevice, "device_state")

	done := make(chan struct{})
	go func() {
		service.tick(context.Background())
		close(done)
	}()
	waitForStartedTopics(t, started, aoaRegisterTopic, dcdRegisterTopic, ridRegisterTopic, ifrRegisterTopic)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("tick waited for blocked AOA publish")
	}
	waitForPublishedTopicCount(t, transport, dcdRegisterTopic, 1)
	waitForPublishedTopicCount(t, transport, ridRegisterTopic, 1)
	waitForPublishedTopicCount(t, transport, ifrRegisterTopic, 1)
	waitForPublishedTopicCount(t, transport, dcdStatusTopic, 1)
	waitForDevicePublishIdle(t, service, model.LingyunDeviceDCD)
	drainStartedTopics(started)
	beforeDCDStatus := countTopic(transport.messages(), dcdStatusTopic)
	setNow(now.Add(11 * time.Second))
	service.tick(context.Background())
	waitForStartedTopics(t, started, dcdStatusTopic)
	waitForPublishedTopicCount(t, transport, dcdStatusTopic, beforeDCDStatus+1)
	if countTopic(transport.messages(), dcdStatusTopic) <= beforeDCDStatus {
		t.Fatalf("DCD status was not published on a later tick while AOA was blocked: %#v", messageTopics(transport.messages()))
	}
	close(release)
	waitForPublishedTopicCount(t, transport, aoaRegisterTopic, 1)
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
	topic := deviceTopic(settings, def, device, "device")
	waitForPublishedTopicCount(t, transport, topic, 1)
	message, ok := findMessage(transport.messages(), topic)
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
	topic := deviceTopic(settings, def, device, "device")
	waitForPublishedTopicCount(t, transport, topic, 1)
	message, ok := findMessage(transport.messages(), topic)
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

func TestServiceApplySettingsUsesConfiguredClientID(t *testing.T) {
	service := NewService(
		store.New(10, 10),
		model.UserSettings{
			Lingyun: model.LingyunSettings{
				Enabled:      true,
				Broker:       "127.0.0.1:1883",
				ClientID:     "persisted-client",
				ProviderCode: "DPTEST",
			},
		},
		WithTransport(newFakeTransport()),
	)

	first := service.settingsSnapshot().ClientID
	if first != "persisted-client" {
		t.Fatalf("client ID = %q, want persisted-client", first)
	}
	if status := service.Status(); status.ClientID != first {
		t.Fatalf("status client ID = %q, want %q", status.ClientID, first)
	}
	service.ApplySettings(model.UserSettings{
		Lingyun: model.LingyunSettings{
			Enabled:      true,
			Broker:       "127.0.0.1:1883",
			ClientID:     "another-persisted-client",
			ProviderCode: "DPTEST",
		},
	})
	if second := service.settingsSnapshot().ClientID; second != "another-persisted-client" {
		t.Fatalf("client ID = %q, want another-persisted-client", second)
	}
}

func TestServiceApplySettingsGeneratesClientIDWhenMissing(t *testing.T) {
	service := NewService(
		store.New(10, 10),
		model.UserSettings{},
		WithTransport(newFakeTransport()),
	)

	clientID := service.settingsSnapshot().ClientID
	if !strings.HasPrefix(clientID, model.DefaultLingyunClientIDPrefix) {
		t.Fatalf("client ID = %q, want prefix %q", clientID, model.DefaultLingyunClientIDPrefix)
	}
	if status := service.Status(); status.ClientID != clientID {
		t.Fatalf("status client ID = %q, want %q", status.ClientID, clientID)
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
	responseTopic := deviceTopic(settings, def, device, "device_control_resp")
	last, ok := findLastMessage(transport.messages(), responseTopic)
	if !ok {
		t.Fatal("control response was not published")
	}
	if last.topic != responseTopic {
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

func TestServiceRejectsNonIntersectionControlCommands(t *testing.T) {
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	transport := newFakeTransport()
	controller := &fakeInterferenceController{
		channels: []model.InterferenceChannel{
			{ID: "io6", Label: "2.4G", Bands: []string{"2.4"}},
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
	if transport.deliver("platform/DPTEST/control/aoa/AOA01", []byte(`{"command":"StartDetecting"}`)) {
		t.Fatal("platform control topic must not be subscribed")
	}

	dcdDevice, ok := lingyunDevice(settings, model.LingyunDeviceDCD)
	if !ok {
		t.Fatal("DCD device missing")
	}
	dcdDef, _ := definitionByType(model.LingyunDeviceDCD)
	for _, cmd := range []int{30000, 30001, 30002, 30003} {
		deliverControl(t, transport, controlTopic(settings, dcdDef, dcdDevice), 20+int64(cmd), 1, cmd)
		response := lastControlResponse(t, transport)
		if response.Data.Code != 1 || response.Data.OperationCmd != cmd {
			t.Fatalf("DCD response for cmd %d = %#v", cmd, response)
		}
	}

	ifrDevice, ok := lingyunDevice(settings, model.LingyunDeviceInterference)
	if !ok {
		t.Fatal("IFR device missing")
	}
	ifrDef, _ := definitionByType(model.LingyunDeviceInterference)
	for _, cmd := range []int{30000, 30001, 30002, 30003, 60004, 60005, 60100, 60101} {
		deliverControl(t, transport, controlTopic(settings, ifrDef, ifrDevice), 40+int64(cmd), 1, cmd)
		response := lastControlResponse(t, transport)
		if response.Data.Code != 1 || response.Data.OperationCmd != cmd {
			t.Fatalf("IFR response for cmd %d = %#v", cmd, response)
		}
	}
	if requests := controller.Requests(); len(requests) != 0 {
		t.Fatalf("unsupported commands changed interference state: %#v", requests)
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

func TestServiceDoesNotPublishInterferenceData(t *testing.T) {
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	transport := newFakeTransport()
	service := NewService(
		store.New(10, 10),
		model.UserSettings{Lingyun: testSettings()},
		WithTransport(transport),
		WithNow(func() time.Time { return now }),
	)
	settings := service.settingsSnapshot()
	device, ok := lingyunDevice(settings, model.LingyunDeviceInterference)
	if !ok {
		t.Fatal("IFR device missing")
	}
	def, _ := definitionByType(model.LingyunDeviceInterference)
	service.addPending(model.LingyunDeviceInterference, senseDataObject{
		ObjectID: "IFR-TARGET",
		Time:     now.UnixMilli(),
		Extension: dataExtension{
			ObjectType: uavObjectType,
		},
	})

	if err := service.flushDevice(context.Background(), settings, service.settingsKeySnapshot(), def, device); err != nil {
		t.Fatalf("flushDevice(IFR) error = %v", err)
	}
	if _, ok := findMessage(transport.messages(), deviceTopic(settings, def, device, "device_data")); ok {
		t.Fatal("IFR device_data must not be published in common protocol")
	}
}

func TestServiceFlushDeviceAddsRequiredSpeedOutsideAOA(t *testing.T) {
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	transport := newFakeTransport()
	service := NewService(
		store.New(10, 10),
		model.UserSettings{Lingyun: testSettings()},
		WithTransport(transport),
		WithNow(func() time.Time { return now }),
	)
	settings := service.settingsSnapshot()
	dcdDevice, ok := lingyunDevice(settings, model.LingyunDeviceDCD)
	if !ok {
		t.Fatal("DCD device missing")
	}
	aoaDevice, ok := lingyunDevice(settings, model.LingyunDeviceAOA)
	if !ok {
		t.Fatal("AOA device missing")
	}
	dcdDef, _ := definitionByType(model.LingyunDeviceDCD)
	aoaDef, _ := definitionByType(model.LingyunDeviceAOA)
	lon := 114.500168
	lat := 22.661026
	service.addPending(model.LingyunDeviceDCD, senseDataObject{
		ObjectID:  "DCD-MISSING-SPEED",
		Longitude: &lon,
		Latitude:  &lat,
		Time:      now.UnixMilli(),
		Extension: dataExtension{ObjectType: uavObjectType, UAVSN: "DCD-MISSING-SPEED"},
	})
	service.addPending(model.LingyunDeviceAOA, senseDataObject{
		ObjectID:  "AOA-MISSING-SPEED",
		Time:      now.UnixMilli(),
		Extension: dataExtension{ObjectType: uavObjectType, UAVSN: "AOA-MISSING-SPEED"},
	})

	if err := service.flushDevice(context.Background(), settings, service.settingsKeySnapshot(), dcdDef, dcdDevice); err != nil {
		t.Fatalf("flushDevice(DCD) error = %v", err)
	}
	if err := service.flushDevice(context.Background(), settings, service.settingsKeySnapshot(), aoaDef, aoaDevice); err != nil {
		t.Fatalf("flushDevice(AOA) error = %v", err)
	}

	dcdMessage, ok := findMessage(transport.messages(), deviceTopic(settings, dcdDef, dcdDevice, "device_data"))
	if !ok {
		t.Fatalf("DCD device_data not published; topics = %#v", messageTopics(transport.messages()))
	}
	var dcdPayload dataPayload
	if err := json.Unmarshal(dcdMessage.payload, &dcdPayload); err != nil {
		t.Fatalf("decode DCD payload: %v", err)
	}
	if len(dcdPayload.Objects) != 1 || dcdPayload.Objects[0].Speed == nil || *dcdPayload.Objects[0].Speed != 0 {
		t.Fatalf("DCD speed = %#v, want explicit 0", dcdPayload.Objects)
	}

	aoaMessage, ok := findMessage(transport.messages(), deviceTopic(settings, aoaDef, aoaDevice, "device_data"))
	if !ok {
		t.Fatalf("AOA device_data not published; topics = %#v", messageTopics(transport.messages()))
	}
	var aoaPayload dataPayload
	if err := json.Unmarshal(aoaMessage.payload, &aoaPayload); err != nil {
		t.Fatalf("decode AOA payload: %v", err)
	}
	if len(aoaPayload.Objects) != 1 || aoaPayload.Objects[0].Speed != nil {
		t.Fatalf("AOA speed = %#v, want omitted", aoaPayload.Objects)
	}
}

func TestServiceRoutesRIDAndDJIOPositionDataToRIDAndDCD(t *testing.T) {
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	transport := newFakeTransport()
	service := NewService(
		store.New(10, 10),
		model.UserSettings{Lingyun: testSettings()},
		WithTransport(transport),
		WithNow(func() time.Time { return now }),
	)
	settings := service.settingsSnapshot()
	ridDevice, ok := lingyunDevice(settings, model.LingyunDeviceRemoteID)
	if !ok {
		t.Fatal("RID device missing")
	}
	dcdDevice, ok := lingyunDevice(settings, model.LingyunDeviceDCD)
	if !ok {
		t.Fatal("DCD device missing")
	}
	ridDef, _ := definitionByType(model.LingyunDeviceRemoteID)
	dcdDef, _ := definitionByType(model.LingyunDeviceDCD)
	targets := []model.ScreenPositionTarget{
		{
			Source: "RID",
			Serial: "RID-SN",
			Model:  "RID",
			Drone:  &model.ScreenPositionPoint{Latitude: 22.1, Longitude: 113.9},
		},
		{
			Source: "dji_O:4",
			Serial: "DJI-SN",
			Model:  "DJI Mini 4 Pro",
			Drone:  &model.ScreenPositionPoint{Latitude: 22.2, Longitude: 114.1},
		},
		{
			Source: "dji_O:4",
			Serial: "PLACEHOLDER-SN",
			Model:  "DJI-Drone",
			Drone:  &model.ScreenPositionPoint{Latitude: 22.3, Longitude: 114.2},
		},
	}

	for _, target := range targets {
		service.handleEvent(model.Event{Type: eventPositionUpdated, Payload: target})
	}
	if err := service.flushDevice(context.Background(), settings, service.settingsKeySnapshot(), ridDef, ridDevice); err != nil {
		t.Fatalf("flushDevice(RID) error = %v", err)
	}
	if err := service.flushDevice(context.Background(), settings, service.settingsKeySnapshot(), dcdDef, dcdDevice); err != nil {
		t.Fatalf("flushDevice(DCD) error = %v", err)
	}

	ridTopic := deviceTopic(settings, ridDef, ridDevice, "device_data")
	dcdTopic := deviceTopic(settings, dcdDef, dcdDevice, "device_data")
	ridMessage, ok := findMessage(transport.messages(), ridTopic)
	if !ok {
		t.Fatalf("RID device_data not published; topics = %#v", messageTopics(transport.messages()))
	}
	dcdMessage, ok := findMessage(transport.messages(), dcdTopic)
	if !ok {
		t.Fatalf("DCD device_data not published; topics = %#v", messageTopics(transport.messages()))
	}
	for topic, message := range map[string]publishedMessage{ridTopic: ridMessage, dcdTopic: dcdMessage} {
		var payload dataPayload
		if err := json.Unmarshal(message.payload, &payload); err != nil {
			t.Fatalf("decode %s payload: %v", topic, err)
		}
		if len(payload.Objects) != 2 {
			t.Fatalf("%s objects = %#v, want RID and decoded dji_O targets only", topic, payload.Objects)
		}
		for _, object := range payload.Objects {
			if object.Speed == nil {
				t.Fatalf("%s object %s is missing required speed", topic, object.ObjectID)
			}
			if object.Extension.UAVModel == "DJI-Drone" || object.Extension.UAVSN == "PLACEHOLDER-SN" {
				t.Fatalf("%s included DJI-Drone placeholder object: %#v", topic, object)
			}
		}
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

func countTopic(messages []publishedMessage, topic string) int {
	count := 0
	for _, message := range messages {
		if message.topic == topic {
			count++
		}
	}
	return count
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

func findLastMessage(messages []publishedMessage, topic string) (publishedMessage, bool) {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].topic == topic {
			return messages[index], true
		}
	}
	return publishedMessage{}, false
}

func deliverControl(t *testing.T, transport *fakeTransport, topic string, msgNo int64, operationType int, operationCmd int) {
	t.Helper()
	payload := []byte(fmt.Sprintf(
		`{"head":{"msgNo":%d,"time":1773281000000},"data":{"operationType":%d,"operationCmd":%d}}`,
		msgNo,
		operationType,
		operationCmd,
	))
	if !transport.deliver(topic, payload) {
		t.Fatalf("control topic was not subscribed: %s", topic)
	}
}

func lastControlResponse(t *testing.T, transport *fakeTransport) controlResponseEnvelope {
	t.Helper()
	messages := transport.messages()
	var last publishedMessage
	found := false
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.Contains(messages[index].topic, "/device_control_resp/") {
			last = messages[index]
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no control response published")
	}
	var response controlResponseEnvelope
	if err := json.Unmarshal(last.payload, &response); err != nil {
		t.Fatalf("decode response from %s: %v", last.topic, err)
	}
	return response
}

func waitForStartedTopics(t *testing.T, started <-chan string, topics ...string) {
	t.Helper()
	pending := make(map[string]struct{}, len(topics))
	for _, topic := range topics {
		pending[topic] = struct{}{}
	}
	timeout := time.After(time.Second)
	for len(pending) > 0 {
		select {
		case got := <-started:
			delete(pending, got)
		case <-timeout:
			t.Fatalf("timed out waiting for publish starts %#v", pending)
		}
	}
}

func waitForPublishedTopicCount(t *testing.T, transport *fakeTransport, topic string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if countTopic(transport.messages(), topic) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d publishes to %s; got %#v", want, topic, messageTopics(transport.messages()))
}

func waitForDevicePublishLog(t *testing.T, service *Service, deviceType string, kind string, topic string) model.LingyunPublishLog {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status := service.Status()
		for _, device := range status.Devices {
			if device.Type != deviceType {
				continue
			}
			for _, log := range device.PublishLogs {
				if log.Kind == kind && log.Topic == topic {
					return log
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s publish log on %s", kind, topic)
	return model.LingyunPublishLog{}
}

func assertNoDevicePublishLog(t *testing.T, service *Service, deviceType string, kind string, topic string) {
	t.Helper()
	status := service.Status()
	for _, device := range status.Devices {
		if device.Type != deviceType {
			continue
		}
		for _, log := range device.PublishLogs {
			if log.Kind == kind && log.Topic == topic {
				t.Fatalf("unexpected stale publish log for %s on %s: %#v", kind, topic, log)
			}
		}
	}
}

func waitForDevicePublishIdle(t *testing.T, service *Service, deviceType string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		service.mu.RLock()
		state := service.states[deviceType]
		inFlight := state != nil && state.PublishInFlight
		service.mu.RUnlock()
		if !inFlight {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s publish job to finish", deviceType)
}

func drainStartedTopics(started <-chan string) {
	for {
		select {
		case <-started:
		default:
			return
		}
	}
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
