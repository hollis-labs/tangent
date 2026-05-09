package extensions

import (
	_ "embed"
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

const WhiteboardEnvelopeType = "tangent.whiteboard"

var whiteboardManifest = []byte(`type: tangent.whiteboard
version: "0.4"
description: "Whiteboard envelope: present a tldraw-backed board with persisted room snapshot hydration and a full-scene submit response."
responseKind: data
ui:
  component: WhiteboardView
`)

//go:embed whiteboard_schema.json
var whiteboardSchema []byte

func RegisterWhiteboard(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	reg := svc.Registry()
	if err := reg.RegisterTypeFromManifest(whiteboardManifest, whiteboardSchema, PluginID); err != nil {
		return fmt.Errorf("extensions: register whiteboard: %w", err)
	}
	return nil
}
