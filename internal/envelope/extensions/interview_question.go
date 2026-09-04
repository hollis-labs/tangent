package extensions

import (
	"github.com/hollis-labs/tangent/internal/envelope"
)

// InterviewQuestionEnvelopeType is the canonical wire name for the interview-question kind. The
// dotted-namespace form is required by go-envelopes' Registry — un-namespaced
// names are reserved for core types and rejected with ErrInvalidName.
//
// Interview-question envelope: ask one long-form question with optional
// quick-picks and explicit output-shape prompting.
//
// ADR 0003 §6 labels it for the go-envelopes generic catalog; that is a
// destination and not a plan, so it ships bundled under the holding
// package until go-envelopes retains source bytes for core kinds and a
// second host consumes one.
const InterviewQuestionEnvelopeType = "tangent.interview-question"

// RegisterInterviewQuestion registers the interview-question definition from its shipped package. The
// manifest at packages/tangent.generic-candidate/interview-question/manifest.yaml is the
// single authored source; nothing about this kind is declared here.
//
// Registration is not idempotent within a process: go-envelopes rejects a
// duplicate name, which is the correct behavior for a boot-time call site.
// Production goes through RegisterAll; this entry point stays exported for
// tests that deliberately boot a partial registry.
func RegisterInterviewQuestion(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, "tangent.generic-candidate", InterviewQuestionEnvelopeType)
}
