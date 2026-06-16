// Package protocol defines the small extension point used by outbound/inbound
// protocol connectors.
package protocol

import (
	"context"

	"drone-management/internal/model"
)

// Connector is implemented by protocol integrations that run beside the core app.
type Connector interface {
	Name() string
	Run(context.Context)
	ApplySettings(model.UserSettings)
}
