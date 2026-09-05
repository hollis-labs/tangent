package extensions

import (
	"github.com/hollis-labs/tangent/internal/envelope"
)

// DesignIterationEnvelopeType is the canonical wire name for the design-iteration kind. The
// dotted-namespace form is required by go-envelopes' Registry — un-namespaced
// names are reserved for core types and rejected with ErrInvalidName.
//
// Design-iteration envelope: render sandboxed HTML, collect click/input
// actions, and iterate through multiple variants in one room.
//
// ADR 0003 §6 assigns it to the Tangent canvas package: presentation
// Tangent hosts inside its own React tree.
const DesignIterationEnvelopeType = "tangent.design-iteration"

// RegisterDesignIteration registers the design-iteration definition from its shipped package. The
// manifest at packages/tangent.canvas/design-iteration/manifest.yaml is the
// single authored source; nothing about this kind is declared here.
//
// Registration is not idempotent within a process: go-envelopes rejects a
// duplicate name, which is the correct behavior for a boot-time call site.
// Production goes through RegisterAll; this entry point stays exported for
// tests that deliberately boot a partial registry.
func RegisterDesignIteration(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, "tangent.canvas", DesignIterationEnvelopeType)
}
