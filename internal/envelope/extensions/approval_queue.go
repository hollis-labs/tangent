package extensions

import (
	"github.com/hollis-labs/tangent/internal/envelope"
)

// ApprovalQueueEnvelopeType is the canonical wire name for the approval-queue kind. The
// dotted-namespace form is required by go-envelopes' Registry — un-namespaced
// names are reserved for core types and rejected with ErrInvalidName.
//
// Approval queue envelope: serialized review of queued items with
// explicit accept, reject, or defer decisions, evidence panes, and
// durable audit export metadata.
//
// ADR 0003 §6 assigns it to the Tangent review package: reusable, but
// its queue, evidence, and audit semantics are host-shaped.
const ApprovalQueueEnvelopeType = "tangent.approval-queue"

// RegisterApprovalQueue registers the approval-queue definition from its shipped package. The
// manifest at packages/tangent.review/approval-queue/manifest.yaml is the
// single authored source; nothing about this kind is declared here.
//
// Registration is not idempotent within a process: go-envelopes rejects a
// duplicate name, which is the correct behavior for a boot-time call site.
// Production goes through RegisterAll; this entry point stays exported for
// tests that deliberately boot a partial registry.
func RegisterApprovalQueue(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, "tangent.review", ApprovalQueueEnvelopeType)
}
