package extensions

import (
	"github.com/hollis-labs/tangent/internal/envelope"
)

// DashboardEnvelopeType is the canonical wire name for the dashboard kind. The
// dotted-namespace form is required by go-envelopes' Registry — un-namespaced
// names are reserved for core types and rejected with ErrInvalidName.
//
// Dashboard envelope: room-backed tiles, layouts, and explicit refresh
// or update turns.
//
// ADR 0003 §6 assigns it to the Tangent canvas package: presentation
// Tangent hosts inside its own React tree.
const DashboardEnvelopeType = "tangent.dashboard"

// RegisterDashboard registers the dashboard definition from its shipped package. The
// manifest at packages/tangent.canvas/dashboard/manifest.yaml is the
// single authored source; nothing about this kind is declared here.
//
// Registration is not idempotent within a process: go-envelopes rejects a
// duplicate name, which is the correct behavior for a boot-time call site.
// Production goes through RegisterAll; this entry point stays exported for
// tests that deliberately boot a partial registry.
func RegisterDashboard(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, "tangent.canvas", DashboardEnvelopeType)
}
