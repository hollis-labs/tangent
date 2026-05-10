package extensions

import (
	_ "embed"
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

const ProgressPanelEnvelopeType = "tangent.progress-panel"

var progressPanelManifest = []byte(`type: tangent.progress-panel
version: "0.10"
description: "Progress-panel envelope: room-backed progress items, summary state, and explicit operator updates."
responseKind: data
ui:
  component: ProgressPanelView
`)

//go:embed progress_panel_schema.json
var progressPanelSchema []byte

func RegisterProgressPanel(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	reg := svc.Registry()
	if err := reg.RegisterTypeFromManifest(progressPanelManifest, progressPanelSchema, PluginID); err != nil {
		return fmt.Errorf("extensions: register progress-panel: %w", err)
	}
	return nil
}
