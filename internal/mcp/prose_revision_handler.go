package mcp

import (
	"context"
	"fmt"

	envelopes "github.com/hollis-labs/go-envelopes"

	"github.com/hollis-labs/tangent/internal/room"
)

type proseRevisionInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

func (s *Server) handleProseRevision(
	ctx context.Context,
	args proseRevisionInput,
) (any, error) {
	if args.Envelope.Type != proseRevisionEnvelopeType {
		return nil, toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf(
				"tangent.prose_revision rejects envelope type %q; want %q",
				args.Envelope.Type,
				proseRevisionEnvelopeType,
			),
		)
	}

	roomID, err := s.resolveWorkflowRoom(ctx, "prose-revision", &args.Envelope)
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
	if err := maybeStoreProseRevisionOutcome(s.manager, roomID, &args.Envelope, resp); err != nil {
		return nil, sessionPhaseStateError(roomID, err)
	}
	return result, nil
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
