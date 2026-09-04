package extensions

import (
	"github.com/hollis-labs/tangent/internal/envelope"
)

// DiffReviewEnvelopeType is the canonical wire name for the diff-review kind. The
// dotted-namespace form is required by go-envelopes' Registry — un-namespaced
// names are reserved for core types and rejected with ErrInvalidName.
//
// Diff-review envelope: room-backed before/after review with per-file
// and per-hunk decisions, comments, durable artifact refs, and explicit
// submit.
//
// ADR 0003 §6 assigns it to the Tangent review package: reusable, but
// its queue, evidence, and audit semantics are host-shaped.
const DiffReviewEnvelopeType = "tangent.diff-review"

// RegisterDiffReview registers the diff-review definition from its shipped package. The
// manifest at packages/tangent.review/diff-review/manifest.yaml is the
// single authored source; nothing about this kind is declared here.
//
// Registration is not idempotent within a process: go-envelopes rejects a
// duplicate name, which is the correct behavior for a boot-time call site.
// Production goes through RegisterAll; this entry point stays exported for
// tests that deliberately boot a partial registry.
func RegisterDiffReview(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, "tangent.review", DiffReviewEnvelopeType)
}
