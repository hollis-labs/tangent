package extensions

import (
	_ "embed"
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

const InterviewQuestionEnvelopeType = "tangent.interview-question"

var interviewQuestionManifest = []byte(`type: tangent.interview-question
version: "0.3"
description: "Interview-question envelope: ask one long-form question with optional quick-picks and explicit output-shape prompting."
responseKind: data
ui:
  component: InterviewQuestionView
`)

//go:embed interview_question_schema.json
var interviewQuestionSchema []byte

func RegisterInterviewQuestion(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	reg := svc.Registry()
	if err := reg.RegisterTypeFromManifest(interviewQuestionManifest, interviewQuestionSchema, PluginID); err != nil {
		return fmt.Errorf("extensions: register interview-question: %w", err)
	}
	return nil
}
