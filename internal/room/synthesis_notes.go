package room

const (
	SynthesisPhaseID          = "synthesis"
	DraftingPhaseID           = "drafting"
	synthesisPrivateNotesKey  = "private_notes"
	synthesisSummaryKey       = "summary"
	synthesisOutlineKey       = "outline"
	synthesisOutlineStateKey  = "outline_state"
	synthesisVisibilityHidden = "hidden"
	synthesisVisibilityShown  = "visible"
)

type SynthesisOutlineItem struct {
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

type SynthesisOutline struct {
	Title string                 `json:"title,omitempty"`
	Items []SynthesisOutlineItem `json:"items,omitempty"`
}

type SynthesisNotesView struct {
	Visibility      string            `json:"visibility"`
	HasPrivateNotes bool              `json:"has_private_notes,omitempty"`
	Summary         string            `json:"summary,omitempty"`
	OutlineState    string            `json:"outline_state,omitempty"`
	Outline         *SynthesisOutline `json:"outline,omitempty"`
}

func ProjectSynthesisNotes(state PhaseState) *SynthesisNotesView {
	blob, ok := state.PhaseOutputs[SynthesisPhaseID]
	if !ok || len(blob.Data) == 0 {
		return nil
	}

	view := &SynthesisNotesView{
		Visibility:   synthesisVisibilityHidden,
		OutlineState: synthesisOutlineState(blob.Data),
	}
	if readString(blob.Data, synthesisPrivateNotesKey) != "" {
		view.HasPrivateNotes = true
	}

	if synthesisNotesVisible(state) {
		view.Visibility = synthesisVisibilityShown
		view.Summary = readString(blob.Data, synthesisSummaryKey)
		if view.OutlineState == "present" {
			view.Outline = readSynthesisOutline(blob.Data[synthesisOutlineKey])
		}
	}

	if !view.HasPrivateNotes && view.Summary == "" && view.OutlineState == "" && view.Outline == nil {
		return nil
	}
	return view
}

func synthesisNotesVisible(state PhaseState) bool {
	if state.CurrentPhase == DraftingPhaseID {
		return true
	}
	for _, phaseID := range state.PhasesVisited {
		if phaseID == DraftingPhaseID {
			return true
		}
	}
	return false
}

func synthesisOutlineState(data map[string]any) string {
	state := readString(data, synthesisOutlineStateKey)
	switch state {
	case "present", "skipped", "absent":
		return state
	}
	if readSynthesisOutline(data[synthesisOutlineKey]) != nil {
		return "present"
	}
	return "absent"
}

func readSynthesisOutline(raw any) *SynthesisOutline {
	record, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	outline := &SynthesisOutline{
		Title: readString(record, "title"),
		Items: readSynthesisOutlineItems(record["items"]),
	}
	if outline.Title == "" && len(outline.Items) == 0 {
		return nil
	}
	return outline
}

func readSynthesisOutlineItems(raw any) []SynthesisOutlineItem {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]SynthesisOutlineItem, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		entry := SynthesisOutlineItem{
			Label:       readString(record, "label"),
			Description: readString(record, "description"),
		}
		if entry.Label == "" && entry.Description == "" {
			continue
		}
		out = append(out, entry)
	}
	return out
}
