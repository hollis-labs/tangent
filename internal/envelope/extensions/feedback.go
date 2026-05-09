package extensions

import (
	_ "embed"
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

const FeedbackEnvelopeType = "tangent.feedback"

var feedbackManifest = []byte(`type: tangent.feedback
version: "0.2"
description: "Feedback envelope: ask a human to answer a short structured questionnaire."
responseKind: data
ui:
  component: FeedbackView
`)

//go:embed feedback_schema.json
var feedbackSchema []byte

func RegisterFeedback(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	reg := svc.Registry()
	if err := reg.RegisterTypeFromManifest(feedbackManifest, feedbackSchema, PluginID); err != nil {
		return fmt.Errorf("extensions: register feedback: %w", err)
	}
	return nil
}
