package extensions

import (
	_ "embed"
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

const WizardEnvelopeType = "tangent.wizard"

var wizardManifest = []byte(`type: tangent.wizard
version: "0.12"
description: "Step-based wizard workflow with room-backed progress, branching selections, review, and completion."
responseKind: data
ui:
  component: WizardView
`)

//go:embed wizard_schema.json
var wizardSchema []byte

func RegisterWizard(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	if err := svc.RegisterTypeFromManifest(WizardEnvelopeType, wizardManifest, wizardSchema, PluginID); err != nil {
		return fmt.Errorf("extensions: register wizard: %w", err)
	}
	return nil
}
