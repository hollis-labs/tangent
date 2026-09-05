package mcp

import (
	"context"
	"fmt"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type feedbackInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

func (s *Server) handleFeedback(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args feedbackInput,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Envelope.Type != feedbackEnvelopeType {
		return toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf("tangent.feedback rejects envelope type %q; want %q", args.Envelope.Type, feedbackEnvelopeType),
		), nil, nil
	}

	roomID, roomResult, roomErr := s.resolveWorkflowRoom(ctx, "feedback", &args.Envelope)
	if roomErr != nil {
		return nil, nil, roomErr
	}
	if roomResult != nil {
		return roomResult, nil, nil
	}

	return s.advanceRoomEnvelope(ctx, roomID, &args.Envelope, nil, args.Completion)
}
