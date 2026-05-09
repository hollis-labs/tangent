package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type designIterationInput struct {
	Envelope envelopes.Envelope `json:"envelope"`
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

	roomID, reused := metaRoomID(args.Envelope.Meta)
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
			return toolErrorResult(
				envelopes.ErrorCodeHostError,
				fmt.Sprintf("decode session_create result: %v", err),
			), nil, nil
		}
		roomID = created.RoomID
		s.logWorkflowRoomCreated("design-iteration", roomID, args.Envelope.ID)
	} else {
		s.logWorkflowRoomReused("design-iteration", roomID, args.Envelope.ID)
	}

	return s.advanceRoomEnvelope(ctx, roomID, &args.Envelope)
}
