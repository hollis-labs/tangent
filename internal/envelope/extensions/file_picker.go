package extensions

import (
	"github.com/hollis-labs/tangent/internal/envelope"
)

// FilePickerEnvelopeType is the canonical wire name for the file-picker kind. The
// dotted-namespace form is required by go-envelopes' Registry — un-namespaced
// names are reserved for core types and rejected with ErrInvalidName.
//
// File-picker envelope: room-backed local artifact selection with
// allowed roots and explicit submit.
//
// ADR 0003 §6 assigns it to the Tangent workspace package: it is
// capability-bearing by nature.
const FilePickerEnvelopeType = "tangent.file-picker"

// RegisterFilePicker registers the file-picker definition from its shipped package. The
// manifest at packages/tangent.workspace/file-picker/manifest.yaml is the
// single authored source; nothing about this kind is declared here.
//
// Registration is not idempotent within a process: go-envelopes rejects a
// duplicate name, which is the correct behavior for a boot-time call site.
// Production goes through RegisterAll; this entry point stays exported for
// tests that deliberately boot a partial registry.
func RegisterFilePicker(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, "tangent.workspace", FilePickerEnvelopeType)
}
