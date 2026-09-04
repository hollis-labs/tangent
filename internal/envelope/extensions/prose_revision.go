package extensions

import (
	"github.com/hollis-labs/tangent/internal/envelope"
)

// ProseRevisionEnvelopeType is the canonical wire name for the prose-revision kind. The
// dotted-namespace form is required by go-envelopes' Registry — un-namespaced
// names are reserved for core types and rejected with ErrInvalidName.
//
// Prose-revision envelope: review suggested edits through one unified
// review/copy/style lens and capture explicit per-suggestion outcomes.
//
// ADR 0003 §6 labels it an application package — publisher-owned
// editorial semantics — bundled only until a writing application exists
// to own it.
const ProseRevisionEnvelopeType = "tangent.prose-revision"

// RegisterProseRevision registers the prose-revision definition from its shipped package. The
// manifest at packages/tangent.writing/prose-revision/manifest.yaml is the
// single authored source; nothing about this kind is declared here.
//
// Registration is not idempotent within a process: go-envelopes rejects a
// duplicate name, which is the correct behavior for a boot-time call site.
// Production goes through RegisterAll; this entry point stays exported for
// tests that deliberately boot a partial registry.
func RegisterProseRevision(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, "tangent.writing", ProseRevisionEnvelopeType)
}
