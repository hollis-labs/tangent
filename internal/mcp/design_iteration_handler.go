package mcp

import (
	"context"
	"fmt"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type designIterationInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

func (s *Server) handleDesignIteration(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args designIterationInput,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Envelope.Type != designIterationEnvelopeType {
		return toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf(
				"tangent.design-iteration rejects envelope type %q; want %q",
				args.Envelope.Type,
				designIterationEnvelopeType,
			),
		), nil, nil
	}

	roomID, roomResult, roomErr := s.resolveWorkflowRoom(ctx, "design-iteration", &args.Envelope)
	if roomErr != nil {
		return nil, nil, roomErr
	}
	if roomResult != nil {
		return roomResult, nil, nil
	}

	return s.advanceRoomEnvelope(ctx, roomID, &args.Envelope, nil, args.Completion)
}
