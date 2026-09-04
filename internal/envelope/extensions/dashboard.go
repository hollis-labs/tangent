package extensions

import (
	_ "embed"
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

const DashboardEnvelopeType = "tangent.dashboard"

var dashboardManifest = []byte(`type: tangent.dashboard
version: "0.11"
description: "Dashboard envelope: room-backed tiles, layouts, and explicit refresh or update turns."
responseKind: data
ui:
  component: DashboardView
`)

//go:embed dashboard_schema.json
var dashboardSchema []byte

func RegisterDashboard(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	if err := svc.RegisterTypeFromManifest(DashboardEnvelopeType, dashboardManifest, dashboardSchema, PluginID); err != nil {
		return fmt.Errorf("extensions: register dashboard: %w", err)
	}
	return nil
}
