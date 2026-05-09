package extensions

import (
	_ "embed"
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

const ProseRevisionEnvelopeType = "tangent.prose-revision"

var proseRevisionManifest = []byte(`type: tangent.prose-revision
version: "0.3"
description: "Prose-revision envelope: review suggested edits through one unified review/copy/style lens and capture explicit per-suggestion outcomes."
responseKind: data
ui:
  component: ProseRevisionView
`)

//go:embed prose_revision_schema.json
var proseRevisionSchema []byte

func RegisterProseRevision(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	reg := svc.Registry()
	if err := reg.RegisterTypeFromManifest(proseRevisionManifest, proseRevisionSchema, PluginID); err != nil {
		return fmt.Errorf("extensions: register prose-revision: %w", err)
	}
	return nil
}
