package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/room"
)

type proseRevisionInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

func (s *Server) handleProseRevision(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args proseRevisionInput,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Envelope.Type != proseRevisionEnvelopeType {
		return toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf(
				"tangent.prose_revision rejects envelope type %q; want %q",
				args.Envelope.Type,
				proseRevisionEnvelopeType,
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
		s.logWorkflowRoomCreated("prose-revision", roomID, args.Envelope.ID)
	} else {
		s.logWorkflowRoomReused("prose-revision", roomID, args.Envelope.ID)
	}

	toolRes, payload, err := s.advanceRoomEnvelope(ctx, roomID, &args.Envelope, nil, args.Completion)
	if err != nil || toolRes == nil || toolRes.IsError {
		return toolRes, payload, err
	}

	resp, ok := payload.(*envelopes.Response)
	if !ok || resp == nil {
		return toolRes, payload, nil
	}
	if err := maybeStoreProseRevisionOutcome(s.manager, roomID, &args.Envelope, resp); err != nil {
		return sessionPhaseStateError(roomID, err), nil, nil
	}
	return toolRes, payload, nil
}

func maybeStoreProseRevisionOutcome(
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
	outcomes := buildProseRevisionSuggestionOutcomes(payload["outcomes"])
	if len(outcomes) == 0 {
		return nil
	}

	data := env.Data
	revisionID := readStringValue(data, "revision_id")
	if revisionID == "" {
		revisionID = env.ID
	}

	_, err := manager.AppendProseRevisionOutcome(roomID, room.ProseRevisionOutcome{
		RevisionID:     revisionID,
		EnvelopeID:     env.ID,
		Lens:           readStringValue(data, "lens"),
		BlockID:        readStringValue(data, "block_id"),
		Label:          readStringValue(data, "label"),
		Summary:        readStringValue(data, "summary"),
		SourceText:     readStringValue(data, "source_text"),
		Suggestions:    buildProseRevisionSuggestions(data["suggestions"]),
		Outcomes:       outcomes,
		GeneralComment: readStringValue(payload, "general_comment"),
		CompletedAt:    resp.CompletedAt,
	})
	return err
}

func buildProseRevisionSuggestions(raw any) []room.ProseRevisionSuggestion {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]room.ProseRevisionSuggestion, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, room.ProseRevisionSuggestion{
			ID:            readStringValue(record, "id"),
			Label:         readStringValue(record, "label"),
			OriginalText:  readStringValue(record, "original_text"),
			SuggestedText: readStringValue(record, "suggested_text"),
			Reason:        readStringValue(record, "reason"),
		})
	}
	return out
}

func buildProseRevisionSuggestionOutcomes(raw any) []room.ProseRevisionSuggestionOutcome {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]room.ProseRevisionSuggestionOutcome, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, room.ProseRevisionSuggestionOutcome{
			SuggestionID: readStringValue(record, "suggestion_id"),
			Decision:     readStringValue(record, "decision"),
			Comment:      readStringValue(record, "comment"),
		})
	}
	return out
}
