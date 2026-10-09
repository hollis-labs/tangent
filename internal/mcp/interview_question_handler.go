package mcp

import (
	"context"
	"fmt"

	envelopes "github.com/hollis-labs/libs/ui-go/envelopes"
)

type interviewQuestionInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

func (s *Server) handleInterviewQuestion(
	ctx context.Context,
	args interviewQuestionInput,
) (any, error) {
	if args.Envelope.Type != interviewQuestionEnvelopeType {
		return nil, toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf(
				"tangent.interview_question rejects envelope type %q; want %q",
				args.Envelope.Type,
				interviewQuestionEnvelopeType,
			),
		)
	}

	roomID, err := s.resolveWorkflowRoom(ctx, "interview-question", &args.Envelope)
	if err != nil {
		return nil, err
	}

	return s.advanceRoomEnvelope(ctx, roomID, &args.Envelope, nil, args.Completion)
}
