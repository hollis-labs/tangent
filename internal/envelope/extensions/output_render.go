package extensions

import (
	_ "embed"
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

const OutputRenderEnvelopeType = "tangent.output-render"

var outputRenderManifest = []byte(`type: tangent.output-render
version: "0.3"
description: "Output-render envelope: present the final markdown artifact with copy/export affordances and preserve the room's canonical final output."
responseKind: ack
ui:
  component: OutputRenderView
`)

//go:embed output_render_schema.json
var outputRenderSchema []byte

func RegisterOutputRender(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	reg := svc.Registry()
	if err := reg.RegisterTypeFromManifest(outputRenderManifest, outputRenderSchema, PluginID); err != nil {
		return fmt.Errorf("extensions: register output-render: %w", err)
	}
	return nil
}
