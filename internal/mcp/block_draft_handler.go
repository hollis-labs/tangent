package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/room"
)

type blockDraftInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

func (s *Server) handleBlockDraft(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args blockDraftInput,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Envelope.Type != blockDraftEnvelopeType {
		return toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf(
				"tangent.block_draft rejects envelope type %q; want %q",
				args.Envelope.Type,
				blockDraftEnvelopeType,
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
		s.logWorkflowRoomCreated("block-draft", roomID, args.Envelope.ID)
	} else {
		s.logWorkflowRoomReused("block-draft", roomID, args.Envelope.ID)
	}

	toolRes, payload, err := s.advanceRoomEnvelope(ctx, roomID, &args.Envelope, nil, args.Completion)
	if err != nil || toolRes == nil || toolRes.IsError {
		return toolRes, payload, err
	}

	resp, ok := payload.(*envelopes.Response)
	if !ok || resp == nil {
		return toolRes, payload, nil
	}
	if err := maybeStoreAcceptedDraftBlock(s.manager, roomID, &args.Envelope, resp); err != nil {
		return sessionPhaseStateError(roomID, err), nil, nil
	}
	return toolRes, payload, nil
}

func maybeStoreAcceptedDraftBlock(
	manager *room.Manager,
	roomID string,
	env *envelopes.Envelope,
	resp *envelopes.Response,
) error {
	if manager == nil || env == nil || resp == nil || resp.Status != envelopes.ResponseStatusSubmitted {
		return nil
	}
	if resp.Kind != envelopes.ResponseKindData {
		return nil
	}
	payload, _ := resp.Payload.(map[string]any)
	decision := strings.TrimSpace(readStringValue(payload, "decision"))
	switch decision {
	case "accept", "inline_edit":
	default:
		return nil
	}

	content := readStringValue(env.Data, "content")
	if decision == "inline_edit" {
		if edited := strings.TrimSpace(readStringValue(payload, "edited_text")); edited != "" {
			content = edited
		}
	}

	_, err := manager.AppendAcceptedDraftBlock(roomID, room.DraftBlock{
		BlockID:    readStringValue(env.Data, "block_id"),
		EnvelopeID: env.ID,
		Label:      readStringValue(env.Data, "label"),
		Mode:       readStringValue(env.Data, "mode"),
		Content:    content,
		Decision:   decision,
		Feedback:   readStringValue(payload, "feedback"),
		AcceptedAt: resp.CompletedAt,
	})
	return err
}

func readStringValue(record map[string]any, key string) string {
	if record == nil {
		return ""
	}
	value, _ := record[key].(string)
	return value
}
