package mcp

import (
	"context"
	"fmt"

	envelopes "github.com/hollis-labs/libs/ui-go/envelopes"
)

type designIterationInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

func (s *Server) handleDesignIteration(
	ctx context.Context,
	args designIterationInput,
) (any, error) {
	if args.Envelope.Type != designIterationEnvelopeType {
		return nil, toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf(
				"tangent.design-iteration rejects envelope type %q; want %q",
				args.Envelope.Type,
				designIterationEnvelopeType,
			),
		)
	}

	roomID, err := s.resolveWorkflowRoom(ctx, "design-iteration", &args.Envelope)
	if err != nil {
		return nil, err
	}

	return s.advanceRoomEnvelope(ctx, roomID, &args.Envelope, nil, args.Completion)
}
