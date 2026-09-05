package mcp

import (
	"context"
	"fmt"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type interviewQuestionInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

func (s *Server) handleInterviewQuestion(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args interviewQuestionInput,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Envelope.Type != interviewQuestionEnvelopeType {
		return toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf(
				"tangent.interview_question rejects envelope type %q; want %q",
				args.Envelope.Type,
				interviewQuestionEnvelopeType,
			),
		), nil, nil
	}

	roomID, roomResult, roomErr := s.resolveWorkflowRoom(ctx, "interview-question", &args.Envelope)
	if roomErr != nil {
		return nil, nil, roomErr
	}
	if roomResult != nil {
		return roomResult, nil, nil
	}

	return s.advanceRoomEnvelope(ctx, roomID, &args.Envelope, nil, args.Completion)
}
