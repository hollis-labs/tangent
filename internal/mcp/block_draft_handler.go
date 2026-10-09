package mcp

import (
	"context"
	"fmt"
	"strings"

	envelopes "github.com/hollis-labs/libs/ui-go/envelopes"

	"github.com/hollis-labs/tangent/internal/room"
)

type blockDraftInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

func (s *Server) handleBlockDraft(
	ctx context.Context,
	args blockDraftInput,
) (any, error) {
	if args.Envelope.Type != blockDraftEnvelopeType {
		return nil, toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf(
				"tangent.block_draft rejects envelope type %q; want %q",
				args.Envelope.Type,
				blockDraftEnvelopeType,
			),
		)
	}

	roomID, err := s.resolveWorkflowRoom(ctx, "block-draft", &args.Envelope)
	if err != nil {
		return nil, err
	}

	result, err := s.advanceRoomEnvelope(ctx, roomID, &args.Envelope, nil, args.Completion)
	if err != nil {
		return nil, err
	}

	resp, ok := result.(*envelopes.Response)
	if !ok || resp == nil {
		return result, nil
	}
	if err := maybeStoreAcceptedDraftBlock(s.manager, roomID, &args.Envelope, resp); err != nil {
		return nil, sessionPhaseStateError(roomID, err)
	}
	return result, nil
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
