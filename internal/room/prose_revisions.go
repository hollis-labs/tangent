package room

import (
	"fmt"
	"strings"
)

const (
	RevisionPhaseID          = "revision"
	proseRevisionOutcomesKey = "prose_revision_outcomes"
)

type ProseRevisionSuggestion struct {
	ID            string `json:"id"`
	Label         string `json:"label,omitempty"`
	OriginalText  string `json:"original_text,omitempty"`
	SuggestedText string `json:"suggested_text"`
	Reason        string `json:"reason,omitempty"`
}

type ProseRevisionSuggestionOutcome struct {
	SuggestionID string `json:"suggestion_id"`
	Decision     string `json:"decision"`
	Comment      string `json:"comment,omitempty"`
}

type ProseRevisionOutcome struct {
	RevisionID     string                           `json:"revision_id"`
	EnvelopeID     string                           `json:"envelope_id,omitempty"`
	Lens           string                           `json:"lens"`
	BlockID        string                           `json:"block_id,omitempty"`
	Label          string                           `json:"label,omitempty"`
	Summary        string                           `json:"summary,omitempty"`
	SourceText     string                           `json:"source_text"`
	Suggestions    []ProseRevisionSuggestion        `json:"suggestions"`
	Outcomes       []ProseRevisionSuggestionOutcome `json:"outcomes"`
	GeneralComment string                           `json:"general_comment,omitempty"`
	CompletedAt    string                           `json:"completed_at,omitempty"`
}

func (r *Room) AppendProseRevisionOutcome(outcome ProseRevisionOutcome) error {
	normalized, err := normalizeProseRevisionOutcome(outcome)
	if err != nil {
		return err
	}

	r.phaseMu.Lock()
	defer r.phaseMu.Unlock()

	nextOutputs := clonePhaseOutputs(r.phaseOutputs)
	blob := nextOutputs[RevisionPhaseID]
	switch {
	case blob.Version == 0:
		blob.Version = phaseOutputVersion
	case blob.Version != phaseOutputVersion:
		return fmt.Errorf("room: unsupported phase output version %d for %q", blob.Version, RevisionPhaseID)
	}
	outcomes := readProseRevisionOutcomes(blob.Data[proseRevisionOutcomesKey])
	outcomes = append(outcomes, normalized)
	if blob.Data == nil {
		blob.Data = map[string]any{}
	}
	blob.Data[proseRevisionOutcomesKey] = proseRevisionOutcomesAny(outcomes)
	nextOutputs[RevisionPhaseID] = blob

	nextVisited := cloneStringSlice(r.phasesVisited)
	if err := r.persistPhaseState(r.currentPhase, nextVisited, nextOutputs); err != nil {
		return err
	}

	r.phasesVisited = nextVisited
	r.phaseOutputs = nextOutputs
	return nil
}

func (m *Manager) AppendProseRevisionOutcome(roomID string, outcome ProseRevisionOutcome) (PhaseState, error) {
	rm, ok := m.Get(roomID)
	if !ok {
		return PhaseState{}, fmt.Errorf("%w: %s", ErrRoomNotFound, roomID)
	}
	if err := rm.AppendProseRevisionOutcome(outcome); err != nil {
		return PhaseState{}, err
	}
	return rm.PhaseState(), nil
}

func ProjectProseRevisionOutcomes(state PhaseState) []ProseRevisionOutcome {
	blob, ok := state.PhaseOutputs[RevisionPhaseID]
	if !ok || len(blob.Data) == 0 {
		return []ProseRevisionOutcome{}
	}
	return readProseRevisionOutcomes(blob.Data[proseRevisionOutcomesKey])
}

func normalizeProseRevisionOutcome(outcome ProseRevisionOutcome) (ProseRevisionOutcome, error) {
	revisionID := strings.TrimSpace(outcome.RevisionID)
	if revisionID == "" {
		return ProseRevisionOutcome{}, ErrInvalidProseRevisionID
	}
	lens := normalizeProseRevisionLens(outcome.Lens)
	if lens == "" {
		return ProseRevisionOutcome{}, ErrInvalidProseRevisionLens
	}
	sourceText := strings.TrimSpace(outcome.SourceText)
	if sourceText == "" {
		return ProseRevisionOutcome{}, ErrInvalidProseRevisionSourceText
	}
	suggestions, err := normalizeProseRevisionSuggestions(outcome.Suggestions)
	if err != nil {
		return ProseRevisionOutcome{}, err
	}
	outcomes, err := normalizeProseRevisionSuggestionOutcomes(outcome.Outcomes)
	if err != nil {
		return ProseRevisionOutcome{}, err
	}
	if len(suggestions) == 0 || len(outcomes) == 0 {
		return ProseRevisionOutcome{}, ErrInvalidProseRevisionOutcome
	}
	allowed := make(map[string]bool, len(suggestions))
	for _, suggestion := range suggestions {
		allowed[suggestion.ID] = true
	}
	for _, item := range outcomes {
		if !allowed[item.SuggestionID] {
			return ProseRevisionOutcome{}, ErrInvalidProseRevisionSuggestionID
		}
	}

	return ProseRevisionOutcome{
		RevisionID:     revisionID,
		EnvelopeID:     strings.TrimSpace(outcome.EnvelopeID),
		Lens:           lens,
		BlockID:        strings.TrimSpace(outcome.BlockID),
		Label:          strings.TrimSpace(outcome.Label),
		Summary:        strings.TrimSpace(outcome.Summary),
		SourceText:     sourceText,
		Suggestions:    suggestions,
		Outcomes:       outcomes,
		GeneralComment: strings.TrimSpace(outcome.GeneralComment),
		CompletedAt:    strings.TrimSpace(outcome.CompletedAt),
	}, nil
}

func normalizeProseRevisionLens(lens string) string {
	switch strings.TrimSpace(lens) {
	case "review":
		return "review"
	case "copy":
		return "copy"
	case "style":
		return "style"
	default:
		return ""
	}
}

func normalizeProseRevisionSuggestions(in []ProseRevisionSuggestion) ([]ProseRevisionSuggestion, error) {
	if len(in) == 0 {
		return nil, ErrInvalidProseRevisionSuggestionID
	}
	out := make([]ProseRevisionSuggestion, 0, len(in))
	seen := map[string]bool{}
	for _, item := range in {
		id := strings.TrimSpace(item.ID)
		if id == "" || seen[id] {
			return nil, ErrInvalidProseRevisionSuggestionID
		}
		text := strings.TrimSpace(item.SuggestedText)
		if text == "" {
			return nil, ErrInvalidProseRevisionSuggestionText
		}
		seen[id] = true
		out = append(out, ProseRevisionSuggestion{
			ID:            id,
			Label:         strings.TrimSpace(item.Label),
			OriginalText:  strings.TrimSpace(item.OriginalText),
			SuggestedText: text,
			Reason:        strings.TrimSpace(item.Reason),
		})
	}
	return out, nil
}

func normalizeProseRevisionSuggestionOutcomes(in []ProseRevisionSuggestionOutcome) ([]ProseRevisionSuggestionOutcome, error) {
	if len(in) == 0 {
		return nil, ErrInvalidProseRevisionOutcome
	}
	out := make([]ProseRevisionSuggestionOutcome, 0, len(in))
	seen := map[string]bool{}
	for _, item := range in {
		suggestionID := strings.TrimSpace(item.SuggestionID)
		if suggestionID == "" || seen[suggestionID] {
			return nil, ErrInvalidProseRevisionSuggestionID
		}
		decision := normalizeProseRevisionDecision(item.Decision)
		if decision == "" {
			return nil, ErrInvalidProseRevisionDecision
		}
		comment := strings.TrimSpace(item.Comment)
		if decision == "comment" && comment == "" {
			return nil, ErrInvalidProseRevisionOutcome
		}
		seen[suggestionID] = true
		out = append(out, ProseRevisionSuggestionOutcome{
			SuggestionID: suggestionID,
			Decision:     decision,
			Comment:      comment,
		})
	}
	return out, nil
}

func normalizeProseRevisionDecision(decision string) string {
	switch strings.TrimSpace(decision) {
	case "accept":
		return "accept"
	case "reject":
		return "reject"
	case "comment":
		return "comment"
	default:
		return ""
	}
}

func readProseRevisionOutcomes(raw any) []ProseRevisionOutcome {
	items, ok := raw.([]any)
	if !ok {
		return []ProseRevisionOutcome{}
	}
	out := make([]ProseRevisionOutcome, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		outcome, err := normalizeProseRevisionOutcome(ProseRevisionOutcome{
			RevisionID:     readString(record, "revision_id"),
			EnvelopeID:     readString(record, "envelope_id"),
			Lens:           readString(record, "lens"),
			BlockID:        readString(record, "block_id"),
			Label:          readString(record, "label"),
			Summary:        readString(record, "summary"),
			SourceText:     readString(record, "source_text"),
			Suggestions:    readProseRevisionSuggestions(record["suggestions"]),
			Outcomes:       readProseRevisionSuggestionOutcomes(record["outcomes"]),
			GeneralComment: readString(record, "general_comment"),
			CompletedAt:    readString(record, "completed_at"),
		})
		if err != nil {
			continue
		}
		out = append(out, outcome)
	}
	return out
}

func readProseRevisionSuggestions(raw any) []ProseRevisionSuggestion {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]ProseRevisionSuggestion, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, ProseRevisionSuggestion{
			ID:            readString(record, "id"),
			Label:         readString(record, "label"),
			OriginalText:  readString(record, "original_text"),
			SuggestedText: readString(record, "suggested_text"),
			Reason:        readString(record, "reason"),
		})
	}
	return out
}

func readProseRevisionSuggestionOutcomes(raw any) []ProseRevisionSuggestionOutcome {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]ProseRevisionSuggestionOutcome, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, ProseRevisionSuggestionOutcome{
			SuggestionID: readString(record, "suggestion_id"),
			Decision:     readString(record, "decision"),
			Comment:      readString(record, "comment"),
		})
	}
	return out
}

func proseRevisionOutcomesAny(outcomes []ProseRevisionOutcome) []any {
	out := make([]any, 0, len(outcomes))
	for _, item := range outcomes {
		record := map[string]any{
			"revision_id": item.RevisionID,
			"lens":        item.Lens,
			"source_text": item.SourceText,
			"suggestions": proseRevisionSuggestionsAny(item.Suggestions),
			"outcomes":    proseRevisionSuggestionOutcomesAny(item.Outcomes),
		}
		if item.EnvelopeID != "" {
			record["envelope_id"] = item.EnvelopeID
		}
		if item.BlockID != "" {
			record["block_id"] = item.BlockID
		}
		if item.Label != "" {
			record["label"] = item.Label
		}
		if item.Summary != "" {
			record["summary"] = item.Summary
		}
		if item.GeneralComment != "" {
			record["general_comment"] = item.GeneralComment
		}
		if item.CompletedAt != "" {
			record["completed_at"] = item.CompletedAt
		}
		out = append(out, record)
	}
	return out
}

func proseRevisionSuggestionsAny(items []ProseRevisionSuggestion) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			"id":             item.ID,
			"suggested_text": item.SuggestedText,
		}
		if item.Label != "" {
			record["label"] = item.Label
		}
		if item.OriginalText != "" {
			record["original_text"] = item.OriginalText
		}
		if item.Reason != "" {
			record["reason"] = item.Reason
		}
		out = append(out, record)
	}
	return out
}

func proseRevisionSuggestionOutcomesAny(items []ProseRevisionSuggestionOutcome) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			"suggestion_id": item.SuggestionID,
			"decision":      item.Decision,
		}
		if item.Comment != "" {
			record["comment"] = item.Comment
		}
		out = append(out, record)
	}
	return out
}
