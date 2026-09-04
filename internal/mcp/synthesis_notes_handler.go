package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/room"
)

type synthesisNotesInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

func (s *Server) handleSynthesisNotes(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args synthesisNotesInput,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Envelope.Type != synthesisNotesEnvelopeType {
		return toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf(
				"tangent.synthesis_notes rejects envelope type %q; want %q",
				args.Envelope.Type,
				synthesisNotesEnvelopeType,
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
		s.logWorkflowRoomCreated("synthesis-notes", roomID, args.Envelope.ID)
	} else {
		s.logWorkflowRoomReused("synthesis-notes", roomID, args.Envelope.ID)
	}

	if err := storeSynthesisOutputs(s.manager, roomID, args.Envelope.Data); err != nil {
		return sessionPhaseStateError(roomID, err), nil, nil
	}

	phaseState, found, err := s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("load room phase state: %v", err)), nil, nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	safeEnvelope := buildVisibleSynthesisEnvelope(&args.Envelope, room.ProjectSynthesisNotes(phaseState))
	return s.advanceRoomEnvelope(ctx, roomID, &args.Envelope, safeEnvelope, args.Completion)
}

func storeSynthesisOutputs(manager *room.Manager, roomID string, data map[string]any) error {
	entries := map[string]any{
		"private_notes": data["private_notes"],
	}
	if value, ok := data["summary"]; ok {
		entries["summary"] = value
	}
	outlineState := "absent"
	if value, ok := data["outline_state"].(string); ok && value != "" {
		outlineState = value
	}
	entries["outline_state"] = outlineState
	if value, ok := data["outline"]; ok && value != nil {
		entries["outline"] = value
	}

	for key, value := range entries {
		if _, err := manager.SetPhaseOutput(roomID, room.SynthesisPhaseID, key, value); err != nil {
			return err
		}
	}
	return nil
}

func buildVisibleSynthesisEnvelope(env *envelopes.Envelope, view *room.SynthesisNotesView) *envelopes.Envelope {
	clone := cloneEnvelopeForDispatch(env)
	data := map[string]any{}
	if view == nil {
		data["visibility"] = "hidden"
	} else {
		data["visibility"] = view.Visibility
		if view.HasPrivateNotes {
			data["has_private_notes"] = true
		}
		if view.Visibility == "visible" && view.OutlineState != "" {
			data["outline_state"] = view.OutlineState
		}
		if view.Visibility == "visible" {
			if view.Summary != "" {
				data["summary"] = view.Summary
			}
			if outline := synthesisOutlineMap(view.Outline); outline != nil {
				data["outline"] = outline
			}
		}
	}
	clone.Data = data
	return clone
}

func cloneEnvelopeForDispatch(env *envelopes.Envelope) *envelopes.Envelope {
	if env == nil {
		return nil
	}
	return &envelopes.Envelope{
		V:            env.V,
		ID:           env.ID,
		Type:         env.Type,
		TypeVersion:  env.TypeVersion,
		Title:        env.Title,
		Context:      env.Context,
		Presentation: env.Presentation,
		Data:         cloneAnyMapForDispatch(env.Data),
		Trace:        env.Trace,
		Meta:         cloneAnyMapForDispatch(env.Meta),
	}
}

func cloneAnyMapForDispatch(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func synthesisOutlineMap(outline *room.SynthesisOutline) map[string]any {
	if outline == nil {
		return nil
	}
	items := make([]any, 0, len(outline.Items))
	for _, item := range outline.Items {
		record := map[string]any{}
		if item.Label != "" {
			record["label"] = item.Label
		}
		if item.Description != "" {
			record["description"] = item.Description
		}
		if len(record) > 0 {
			items = append(items, record)
		}
	}
	record := map[string]any{}
	if outline.Title != "" {
		record["title"] = outline.Title
	}
	if len(items) > 0 {
		record["items"] = items
	}
	if len(record) == 0 {
		return nil
	}
	return record
}
