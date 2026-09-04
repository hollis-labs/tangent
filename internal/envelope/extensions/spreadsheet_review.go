package extensions

import (
	"github.com/hollis-labs/tangent/internal/envelope"
)

// SpreadsheetReviewEnvelopeType is the canonical wire name for the spreadsheet-review kind. The
// dotted-namespace form is required by go-envelopes' Registry — un-namespaced
// names are reserved for core types and rejected with ErrInvalidName.
//
// Spreadsheet review envelope: render canonical agent-provided rows in a
// persistent room with explicit submit, saved views, and CSV export
// metadata.
//
// ADR 0003 §6 assigns it to the Tangent review package: reusable, but
// its queue, evidence, and audit semantics are host-shaped.
const SpreadsheetReviewEnvelopeType = "tangent.spreadsheet-review"

// RegisterSpreadsheetReview registers the spreadsheet-review definition from its shipped package. The
// manifest at packages/tangent.review/spreadsheet-review/manifest.yaml is the
// single authored source; nothing about this kind is declared here.
//
// Registration is not idempotent within a process: go-envelopes rejects a
// duplicate name, which is the correct behavior for a boot-time call site.
// Production goes through RegisterAll; this entry point stays exported for
// tests that deliberately boot a partial registry.
func RegisterSpreadsheetReview(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, "tangent.review", SpreadsheetReviewEnvelopeType)
}
