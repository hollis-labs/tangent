package extensions

import (
	_ "embed"
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

const FormCollectEnvelopeType = "tangent.form-collect"

var formCollectManifest = []byte(`type: tangent.form-collect
version: "0.6"
description: "Generalized schema-driven form workflow with room-backed persistence."
responseKind: data
ui:
  component: FormCollectView
`)

//go:embed form_collect_schema.json
var formCollectSchema []byte

func RegisterFormCollect(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	if err := svc.RegisterTypeFromManifest(FormCollectEnvelopeType, formCollectManifest, formCollectSchema, PluginID); err != nil {
		return fmt.Errorf("extensions: register form-collect: %w", err)
	}
	return nil
}
