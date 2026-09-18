package protocol

import (
	"context"
	"sync"
	"testing"

	"drone-management/internal/model"
)

type testConnector struct {
	name    string
	applied model.UserSettings
	ran     chan struct{}
}

func (c *testConnector) Name() string                              { return c.name }
func (c *testConnector) ApplySettings(settings model.UserSettings) { c.applied = settings }
func (c *testConnector) Run(ctx context.Context) {
	close(c.ran)
	<-ctx.Done()
}

func TestManagerBroadcastsSettingsAndRunsConnectors(t *testing.T) {
	first := &testConnector{name: "first", ran: make(chan struct{})}
	second := &testConnector{name: "second", ran: make(chan struct{})}
	manager := NewManager(first, nil, second)
	settings := model.UserSettings{ScreenTitle: "protocols"}
	manager.ApplySettings(settings)
	if first.applied.ScreenTitle != "protocols" || second.applied.ScreenTitle != "protocols" {
		t.Fatalf("settings were not broadcast: %#v %#v", first.applied, second.applied)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		manager.Run(ctx)
	}()
	<-first.ran
	<-second.ran
	cancel()
	wg.Wait()
}
