package extensions

import (
	"github.com/hollis-labs/tangent/internal/envelope"
)

// BlockDraftEnvelopeType is the canonical wire name for the block-draft kind. The
// dotted-namespace form is required by go-envelopes' Registry — un-namespaced
// names are reserved for core types and rejected with ErrInvalidName.
//
// Block-draft envelope: propose one draft block, capture
// accept/revise/inline-edit direction, and accumulate accepted blocks on
// the room.
//
// ADR 0003 §6 labels it an application package — publisher-owned
// editorial semantics — bundled only until a writing application exists
// to own it.
const BlockDraftEnvelopeType = "tangent.block-draft"

// RegisterBlockDraft registers the block-draft definition from its shipped package. The
// manifest at packages/tangent.writing/block-draft/manifest.yaml is the
// single authored source; nothing about this kind is declared here.
//
// Registration is not idempotent within a process: go-envelopes rejects a
// duplicate name, which is the correct behavior for a boot-time call site.
// Production goes through RegisterAll; this entry point stays exported for
// tests that deliberately boot a partial registry.
func RegisterBlockDraft(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, "tangent.writing", BlockDraftEnvelopeType)
}
