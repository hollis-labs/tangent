package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type feedbackInput struct {
	Envelope envelopes.Envelope `json:"envelope"`
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

	roomID, reused := metaString(args.Envelope.Meta, "roomID")
	if !reused || roomID == "" {
		createRes, _, err := s.handleSessionCreate(ctx, nil, sessionCreateInput{
			Meta: map[string]any{
				"envelopeID":   args.Envelope.ID,
				"envelopeType": args.Envelope.Type,
			},
		})
		if err != nil {
			return nil, nil, err
		}
		if createRes.IsError {
			return createRes, nil, nil
		}
		var created sessionCreateResult
		if err := json.Unmarshal([]byte(extractToolText(createRes)), &created); err != nil {
			return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("decode session_create result: %v", err)), nil, nil
		}
		roomID = created.RoomID
		s.logWorkflowRoomCreated("feedback", roomID, args.Envelope.ID)
	} else {
		s.logWorkflowRoomReused("feedback", roomID, args.Envelope.ID)
	}

	return s.advanceRoomEnvelope(ctx, roomID, &args.Envelope)
}
