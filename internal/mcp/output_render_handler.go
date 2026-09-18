package mcp

import (
	"context"
	"fmt"
	"time"

	envelopes "github.com/hollis-labs/go-envelopes"

	"github.com/hollis-labs/tangent/internal/room"
)

type outputRenderInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

func (s *Server) handleOutputRender(
	ctx context.Context,
	args outputRenderInput,
) (any, error) {
	if args.Envelope.Type != outputRenderEnvelopeType {
		return nil, toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf(
				"tangent.output_render rejects envelope type %q; want %q",
				args.Envelope.Type,
				outputRenderEnvelopeType,
			),
		)
	}

	roomID, roomErr := s.resolveWorkflowRoom(ctx, "output-render", &args.Envelope)
	if roomErr != nil {
		return nil, roomErr
	}

	if _, err := s.manager.SetFinalOutput(roomID, room.FinalOutputView{
		Title:     readStringValue(args.Envelope.Data, "title"),
		Markdown:  readStringValue(args.Envelope.Data, "markdown"),
		Filename:  readStringValue(args.Envelope.Data, "filename"),
		Format:    readStringValue(args.Envelope.Data, "format"),
		Summary:   readStringValue(args.Envelope.Data, "summary"),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return nil, sessionPhaseStateError(roomID, err)
	}

	phaseState, found, err := s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return nil, toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("load room phase state: %v", err))
	}
	if !found {
		return nil, toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID))
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
