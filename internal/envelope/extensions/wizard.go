package extensions

import (
	"github.com/hollis-labs/tangent/internal/envelope"
)

// WizardEnvelopeType is the canonical wire name for the wizard kind. The
// dotted-namespace form is required by go-envelopes' Registry — un-namespaced
// names are reserved for core types and rejected with ErrInvalidName.
//
// Step-based wizard workflow with room-backed progress, branching
// selections, review, and completion.
//
// ADR 0003 §6 assigns it to the Tangent compound package while its step
// and branch state stays presentation navigation.
const WizardEnvelopeType = "tangent.wizard"

// RegisterWizard registers the wizard definition from its shipped package. The
// manifest at packages/tangent.compound/wizard/manifest.yaml is the
// single authored source; nothing about this kind is declared here.
//
// Registration is not idempotent within a process: go-envelopes rejects a
// duplicate name, which is the correct behavior for a boot-time call site.
// Production goes through RegisterAll; this entry point stays exported for
// tests that deliberately boot a partial registry.
func RegisterWizard(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, "tangent.compound", WizardEnvelopeType)
}
