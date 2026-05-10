package extensions

import (
	_ "embed"
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

const ApprovalQueueEnvelopeType = "tangent.approval-queue"

var approvalQueueManifest = []byte(`type: tangent.approval-queue
version: "0.7"
description: "Approval queue envelope: serialized review of queued items with explicit accept, reject, or defer decisions, evidence panes, and durable audit export metadata."
responseKind: data
ui:
  component: ApprovalQueueView
`)

//go:embed approval_queue_schema.json
var approvalQueueSchema []byte

func RegisterApprovalQueue(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	reg := svc.Registry()
	if err := reg.RegisterTypeFromManifest(approvalQueueManifest, approvalQueueSchema, PluginID); err != nil {
		return fmt.Errorf("extensions: register approval-queue: %w", err)
	}
	return nil
}
