package extensions

import (
	"github.com/hollis-labs/tangent/internal/envelope"
)

// OutputRenderEnvelopeType is the canonical wire name for the output-render kind. The
// dotted-namespace form is required by go-envelopes' Registry — un-namespaced
// names are reserved for core types and rejected with ErrInvalidName.
//
// Output-render envelope: present the final markdown artifact with
// copy/export affordances and preserve the room's canonical final
// output.
//
// ADR 0003 §6 labels it for the go-envelopes generic catalog; that is a
// destination and not a plan, so it ships bundled under the holding
// package until go-envelopes retains source bytes for core kinds and a
// second host consumes one.
const OutputRenderEnvelopeType = "tangent.output-render"

// RegisterOutputRender registers the output-render definition from its shipped package. The
// manifest at packages/tangent.generic-candidate/output-render/manifest.yaml is the
// single authored source; nothing about this kind is declared here.
//
// Registration is not idempotent within a process: go-envelopes rejects a
// duplicate name, which is the correct behavior for a boot-time call site.
// Production goes through RegisterAll; this entry point stays exported for
// tests that deliberately boot a partial registry.
func RegisterOutputRender(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, "tangent.generic-candidate", OutputRenderEnvelopeType)
}
