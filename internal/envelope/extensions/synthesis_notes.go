package extensions

import (
	_ "embed"
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

const SynthesisNotesEnvelopeType = "tangent.synthesis-notes"

var synthesisNotesManifest = []byte(`type: tangent.synthesis-notes
version: "0.3"
description: "Synthesis-notes envelope: persist private working notes, optionally carry an outline artifact, and expose only the phase-gated preview to the user."
responseKind: ack
ui:
  component: SynthesisNotesView
`)

//go:embed synthesis_notes_schema.json
var synthesisNotesSchema []byte

func RegisterSynthesisNotes(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	reg := svc.Registry()
	if err := reg.RegisterTypeFromManifest(synthesisNotesManifest, synthesisNotesSchema, PluginID); err != nil {
		return fmt.Errorf("extensions: register synthesis-notes: %w", err)
	}
	return nil
}
