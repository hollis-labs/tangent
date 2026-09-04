// Package extensions registers Tangent-owned interaction definitions.
//
// Each kind ships as a package under packages/<package-id>/<kind>/: an
// authored manifest.yaml plus the schema files it references, embedded with
// //go:embed and registered through envelope.Service.RegisterDefinition.
// Tangent does NOT fork the upstream go-envelopes manifest — it hosts
// publisher-owned definitions beside the core catalog, and ADR 0003 §5 is
// explicit that a definition living in this tree because it is bundled with
// the release is still publisher-owned content Tangent hosts.
//
// The Go files here carry only wire-name constants and one-line registration
// entry points. Everything a definition declares — schemas, renderer binding,
// trust class, capabilities, custody, telemetry — lives in its manifest, so
// there is exactly one place to read and exactly one set of bytes to digest.
package extensions

import (
	"github.com/hollis-labs/tangent/internal/envelope"
)

// PluginID is the namespace recorded on every Tangent-registered TypeSpec and
// the publisher every shipped manifest declares. Used as the registration tag
// (so UnregisterPlugin can sweep the whole set), as the manifest's `publisher`
// — RegisterDefinition rejects a mismatch — and as a debugging aid in registry
// dumps.
const PluginID = "tangent"

// TriageEnvelopeType is the canonical wire name for the triage kind. The
// dotted-namespace form is required by go-envelopes' Registry — un-namespaced
// names are reserved for core types and rejected with ErrInvalidName.
//
// Triage envelope: ask a human to accept, reject, or annotate an item.
// Tangent v0.1 plugin-registered; planned for go-envelopes core in v0.3.
//
// ADR 0003 §6 labels it for the go-envelopes generic catalog; that is a
// destination and not a plan, so it ships bundled under the holding
// package until go-envelopes retains source bytes for core kinds and a
// second host consumes one.
const TriageEnvelopeType = "tangent.triage"

// RegisterTriage registers the triage definition from its shipped package. The
// manifest at packages/tangent.generic-candidate/triage/manifest.yaml is the
// single authored source; nothing about this kind is declared here.
//
// Registration is not idempotent within a process: go-envelopes rejects a
// duplicate name, which is the correct behavior for a boot-time call site.
// Production goes through RegisterAll; this entry point stays exported for
// tests that deliberately boot a partial registry.
func RegisterTriage(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, "tangent.generic-candidate", TriageEnvelopeType)
}
