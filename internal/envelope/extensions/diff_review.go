package extensions

import (
	_ "embed"
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

const DiffReviewEnvelopeType = "tangent.diff-review"

var diffReviewManifest = []byte(`type: tangent.diff-review
version: "0.8"
description: "Diff-review envelope: room-backed before/after review with per-file and per-hunk decisions, comments, durable artifact refs, and explicit submit."
responseKind: data
ui:
  component: DiffReviewView
`)

//go:embed diff_review_schema.json
var diffReviewSchema []byte

func RegisterDiffReview(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	reg := svc.Registry()
	if err := reg.RegisterTypeFromManifest(diffReviewManifest, diffReviewSchema, PluginID); err != nil {
		return fmt.Errorf("extensions: register diff-review: %w", err)
	}
	return nil
}
