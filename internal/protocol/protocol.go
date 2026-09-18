// Package protocol defines the small extension point used by outbound/inbound
// protocol connectors.
package protocol

import (
	"context"
	"sync"

	"drone-management/internal/model"
)

// Connector is implemented by protocol integrations that run beside the core app.
type Connector interface {
	Name() string
	Run(context.Context)
	ApplySettings(model.UserSettings)
}

// Manager owns the protocol connectors that run beside the core application.
// Connectors remain independently configurable and can be added without changing
// the lifecycle code in app.App.
type Manager struct {
	connectors []Connector
}

// NewManager creates a protocol manager and ignores nil connector entries.
func NewManager(connectors ...Connector) *Manager {
	manager := &Manager{}
	for _, connector := range connectors {
		if connector != nil {
			manager.connectors = append(manager.connectors, connector)
		}
	}
	return manager
}

// ApplySettings broadcasts one settings snapshot to every connector.
func (m *Manager) ApplySettings(settings model.UserSettings) {
	if m == nil {
		return
	}
	for _, connector := range m.connectors {
		connector.ApplySettings(settings)
	}
}

// Run starts all connectors and waits until they stop after ctx is cancelled.
func (m *Manager) Run(ctx context.Context) {
	if m == nil {
		return
	}
	var wg sync.WaitGroup
	for _, connector := range m.connectors {
		wg.Add(1)
		go func(connector Connector) {
			defer wg.Done()
			connector.Run(ctx)
		}(connector)
	}
	wg.Wait()
}
