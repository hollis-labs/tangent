package extensions

import (
	_ "embed"
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

const FilePickerEnvelopeType = "tangent.file-picker"

var filePickerManifest = []byte(`type: tangent.file-picker
version: "0.9"
description: "File-picker envelope: room-backed local artifact selection with allowed roots and explicit submit."
responseKind: data
ui:
  component: FilePickerView
`)

//go:embed file_picker_schema.json
var filePickerSchema []byte

func RegisterFilePicker(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	reg := svc.Registry()
	if err := reg.RegisterTypeFromManifest(filePickerManifest, filePickerSchema, PluginID); err != nil {
		return fmt.Errorf("extensions: register file-picker: %w", err)
	}
	return nil
}
