package extensions

import (
	_ "embed"
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

const DesignIterationEnvelopeType = "tangent.design-iteration"

var designIterationManifest = []byte(`type: tangent.design-iteration
version: "0.2"
description: "Design-iteration envelope: render sandboxed HTML, collect click/input actions, and iterate through multiple variants in one room."
responseKind: data
ui:
  component: DesignIterationView
`)

//go:embed design_iteration_schema.json
var designIterationSchema []byte

func RegisterDesignIteration(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	if err := svc.RegisterTypeFromManifest(DesignIterationEnvelopeType, designIterationManifest, designIterationSchema, PluginID); err != nil {
		return fmt.Errorf("extensions: register design-iteration: %w", err)
	}
	return nil
}
