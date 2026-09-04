package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/room"
)

type outputRenderInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

func (s *Server) handleOutputRender(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args outputRenderInput,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Envelope.Type != outputRenderEnvelopeType {
		return toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf(
				"tangent.output_render rejects envelope type %q; want %q",
				args.Envelope.Type,
				outputRenderEnvelopeType,
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
		s.logWorkflowRoomCreated("output-render", roomID, args.Envelope.ID)
	} else {
		s.logWorkflowRoomReused("output-render", roomID, args.Envelope.ID)
	}

	if _, err := s.manager.SetFinalOutput(roomID, room.FinalOutputView{
		Title:     readStringValue(args.Envelope.Data, "title"),
		Markdown:  readStringValue(args.Envelope.Data, "markdown"),
		Filename:  readStringValue(args.Envelope.Data, "filename"),
		Format:    readStringValue(args.Envelope.Data, "format"),
		Summary:   readStringValue(args.Envelope.Data, "summary"),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return sessionPhaseStateError(roomID, err), nil, nil
	}

	phaseState, found, err := s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("load room phase state: %v", err)), nil, nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	return s.advanceRoomEnvelope(ctx, roomID, &args.Envelope, buildVisibleOutputRenderEnvelope(&args.Envelope, room.ProjectFinalOutput(phaseState)), args.Completion)
}

func buildVisibleOutputRenderEnvelope(env *envelopes.Envelope, view *room.FinalOutputView) *envelopes.Envelope {
	clone := cloneEnvelopeForDispatch(env)
	if view == nil {
		return clone
	}
	data := map[string]any{
		"markdown": view.Markdown,
		"format":   view.Format,
	}
	if view.Title != "" {
		data["title"] = view.Title
	}
	if view.Filename != "" {
		data["filename"] = view.Filename
	}
	if view.Summary != "" {
		data["summary"] = view.Summary
	}
	clone.Data = data
	return clone
}
