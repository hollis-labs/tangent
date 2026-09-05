package extensions

import (
	"github.com/hollis-labs/tangent/internal/envelope"
)

// WhiteboardEnvelopeType is the canonical wire name for the whiteboard kind. The
// dotted-namespace form is required by go-envelopes' Registry — un-namespaced
// names are reserved for core types and rejected with ErrInvalidName.
//
// Whiteboard envelope: present a tldraw-backed board with persisted room
// snapshot hydration and a full-scene submit response.
//
// ADR 0003 §6 assigns it to the Tangent canvas package: presentation
// Tangent hosts inside its own React tree.
const WhiteboardEnvelopeType = "tangent.whiteboard"

// RegisterWhiteboard registers the whiteboard definition from its shipped package. The
// manifest at packages/tangent.canvas/whiteboard/manifest.yaml is the
// single authored source; nothing about this kind is declared here.
//
// Registration is not idempotent within a process: go-envelopes rejects a
// duplicate name, which is the correct behavior for a boot-time call site.
// Production goes through RegisterAll; this entry point stays exported for
// tests that deliberately boot a partial registry.
func RegisterWhiteboard(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, "tangent.canvas", WhiteboardEnvelopeType)
}
