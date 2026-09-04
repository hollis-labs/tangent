package extensions

import (
	_ "embed"
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

const BlockDraftEnvelopeType = "tangent.block-draft"

var blockDraftManifest = []byte(`type: tangent.block-draft
version: "0.3"
description: "Block-draft envelope: propose one draft block, capture accept/revise/inline-edit direction, and accumulate accepted blocks on the room."
responseKind: data
ui:
  component: BlockDraftView
`)

//go:embed block_draft_schema.json
var blockDraftSchema []byte

func RegisterBlockDraft(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	if err := svc.RegisterTypeFromManifest(BlockDraftEnvelopeType, blockDraftManifest, blockDraftSchema, PluginID); err != nil {
		return fmt.Errorf("extensions: register block-draft: %w", err)
	}
	return nil
}
