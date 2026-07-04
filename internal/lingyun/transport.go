package lingyun

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const defaultMQTTTimeout = 5 * time.Second

type transportConfig struct {
	Broker   string
	ClientID string
	Username string
	Password string
}

type messageHandler func(topic string, payload []byte)

type transport interface {
	Connect(context.Context, transportConfig) error
	Subscribe(context.Context, string, messageHandler) error
	Publish(context.Context, string, []byte) error
	Disconnect()
	Connected() bool
}

type pahoTransport struct {
	mu        sync.Mutex
	client    mqtt.Client
	connected atomic.Bool
}

func newPahoTransport() *pahoTransport {
	return &pahoTransport{}
}

func (t *pahoTransport) Connect(ctx context.Context, cfg transportConfig) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.client != nil {
		if t.client.IsConnected() {
			t.connected.Store(true)
			return nil
		}
		t.connected.Store(false)
		t.client.Disconnect(250)
		t.client = nil
	}
	opts := mqtt.NewClientOptions().
		AddBroker(normalizeBroker(cfg.Broker)).
		SetClientID(strings.TrimSpace(cfg.ClientID)).
		SetUsername(strings.TrimSpace(cfg.Username)).
		SetPassword(cfg.Password).
		SetAutoReconnect(true).
		SetConnectTimeout(defaultMQTTTimeout).
		SetOnConnectHandler(func(mqtt.Client) {
			t.connected.Store(true)
		}).
		SetConnectionLostHandler(func(mqtt.Client, error) {
			t.connected.Store(false)
		})
	client := mqtt.NewClient(opts)
	t.connected.Store(false)
	token := client.Connect()
	if !waitToken(ctx, token) {
		client.Disconnect(250)
		t.connected.Store(false)
		return fmt.Errorf("connect Lingyun MQTT timeout")
	}
	if err := token.Error(); err != nil {
		client.Disconnect(250)
		t.connected.Store(false)
		return fmt.Errorf("connect Lingyun MQTT: %w", err)
	}
	t.client = client
	t.connected.Store(true)
	return nil
}

func (t *pahoTransport) Subscribe(ctx context.Context, topic string, handler messageHandler) error {
	t.mu.Lock()
	client := t.client
	t.mu.Unlock()
	if client == nil || !client.IsConnected() {
		t.connected.Store(false)
		return fmt.Errorf("Lingyun MQTT is not connected")
	}
	token := client.Subscribe(topic, qos, func(_ mqtt.Client, message mqtt.Message) {
		handler(message.Topic(), message.Payload())
	})
	if !waitToken(ctx, token) {
		return fmt.Errorf("subscribe Lingyun MQTT timeout")
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("subscribe Lingyun MQTT: %w", err)
	}
	return nil
}

func (t *pahoTransport) Publish(ctx context.Context, topic string, payload []byte) error {
	t.mu.Lock()
	client := t.client
	t.mu.Unlock()
	if client == nil || !client.IsConnected() {
		t.connected.Store(false)
		return fmt.Errorf("Lingyun MQTT is not connected")
	}
	token := client.Publish(topic, qos, false, payload)
	if !waitToken(ctx, token) {
		return fmt.Errorf("publish Lingyun MQTT timeout")
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("publish Lingyun MQTT: %w", err)
	}
	return nil
}

func (t *pahoTransport) Disconnect() {
	t.mu.Lock()
	client := t.client
	t.client = nil
	t.connected.Store(false)
	t.mu.Unlock()
	if client != nil {
		client.Disconnect(250)
	}
}

func (t *pahoTransport) Connected() bool {
	return t.connected.Load()
}

func waitToken(ctx context.Context, token mqtt.Token) bool {
	timeout := defaultMQTTTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}
	done := make(chan struct{})
	go func() {
		token.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	case <-time.After(timeout):
		return false
	}
}

func normalizeBroker(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "://") {
		return raw
	}
	return "tcp://" + raw
}
