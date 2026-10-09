package mcp

import (
	"context"
	"fmt"

	envelopes "github.com/hollis-labs/libs/ui-go/envelopes"
)

type feedbackInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

func (s *Server) handleFeedback(
	ctx context.Context,
	args feedbackInput,
) (any, error) {
	if args.Envelope.Type != feedbackEnvelopeType {
		return nil, toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf("tangent.feedback rejects envelope type %q; want %q", args.Envelope.Type, feedbackEnvelopeType),
		)
	}

	roomID, err := s.resolveWorkflowRoom(ctx, "feedback", &args.Envelope)
	if err != nil {
		return nil, err
	}

	return s.advanceRoomEnvelope(ctx, roomID, &args.Envelope, nil, args.Completion)
}
