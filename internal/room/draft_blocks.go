package room

import (
	"fmt"
	"strings"
)

const (
	acceptedDraftBlocksKey = "accepted_blocks"
)

type DraftBlock struct {
	BlockID    string `json:"block_id"`
	EnvelopeID string `json:"envelope_id,omitempty"`
	Label      string `json:"label,omitempty"`
	Mode       string `json:"mode,omitempty"`
	Content    string `json:"content"`
	Decision   string `json:"decision,omitempty"`
	Feedback   string `json:"feedback,omitempty"`
	AcceptedAt string `json:"accepted_at,omitempty"`
}

type CurrentDraftView struct {
	Blocks     []DraftBlock `json:"blocks"`
	Markdown   string       `json:"markdown,omitempty"`
	BlockCount int          `json:"block_count"`
}

func (r *Room) AppendAcceptedDraftBlock(block DraftBlock) error {
	normalized, err := normalizeDraftBlock(block)
	if err != nil {
		return err
	}

	r.phaseMu.Lock()
	defer r.phaseMu.Unlock()

	nextOutputs := clonePhaseOutputs(r.phaseOutputs)
	blob := nextOutputs[DraftingPhaseID]
	switch {
	case blob.Version == 0:
		blob.Version = phaseOutputVersion
	case blob.Version != phaseOutputVersion:
		return fmt.Errorf("room: unsupported phase output version %d for %q", blob.Version, DraftingPhaseID)
	}
	blocks := readDraftBlocks(blob.Data[acceptedDraftBlocksKey])
	blocks = append(blocks, normalized)
	if blob.Data == nil {
		blob.Data = map[string]any{}
	}
	blob.Data[acceptedDraftBlocksKey] = draftBlocksAny(blocks)
	nextOutputs[DraftingPhaseID] = blob

	nextVisited := cloneStringSlice(r.phasesVisited)
	if err := r.persistPhaseState(r.currentPhase, nextVisited, nextOutputs); err != nil {
		return err
	}

	r.phasesVisited = nextVisited
	r.phaseOutputs = nextOutputs
	return nil
}

func (m *Manager) AppendAcceptedDraftBlock(roomID string, block DraftBlock) (PhaseState, error) {
	rm, ok := m.Get(roomID)
	if !ok {
		return PhaseState{}, fmt.Errorf("%w: %s", ErrRoomNotFound, roomID)
	}
	if err := rm.AppendAcceptedDraftBlock(block); err != nil {
		return PhaseState{}, err
	}
	return rm.PhaseState(), nil
}

func ProjectAcceptedDraftBlocks(state PhaseState) []DraftBlock {
	blob, ok := state.PhaseOutputs[DraftingPhaseID]
	if !ok || len(blob.Data) == 0 {
		return []DraftBlock{}
	}
	return readDraftBlocks(blob.Data[acceptedDraftBlocksKey])
}

func ProjectCurrentDraft(state PhaseState) *CurrentDraftView {
	accepted := ProjectAcceptedDraftBlocks(state)
	if len(accepted) == 0 {
		return nil
	}

	latest := make(map[string]DraftBlock, len(accepted))
	order := make([]string, 0, len(accepted))
	seen := make(map[string]bool, len(accepted))
	for _, block := range accepted {
		if !seen[block.BlockID] {
			order = append(order, block.BlockID)
			seen[block.BlockID] = true
		}
		latest[block.BlockID] = block
	}

	blocks := make([]DraftBlock, 0, len(order))
	parts := make([]string, 0, len(order))
	for _, blockID := range order {
		block := latest[blockID]
		blocks = append(blocks, block)
		if text := strings.TrimSpace(block.Content); text != "" {
			parts = append(parts, text)
		}
	}

	view := &CurrentDraftView{
		Blocks:     blocks,
		Markdown:   strings.Join(parts, "\n\n"),
		BlockCount: len(blocks),
	}
	if len(view.Blocks) == 0 {
		return nil
	}
	return view
}

func normalizeDraftBlock(block DraftBlock) (DraftBlock, error) {
	blockID, err := normalizePhaseOutputKey(block.BlockID)
	if err != nil {
		return DraftBlock{}, fmt.Errorf("%w: %q", ErrInvalidDraftBlockID, block.BlockID)
	}
	content := strings.TrimSpace(block.Content)
	if content == "" {
		return DraftBlock{}, ErrInvalidDraftBlockContent
	}

	normalized := DraftBlock{
		BlockID:    blockID,
		EnvelopeID: strings.TrimSpace(block.EnvelopeID),
		Label:      strings.TrimSpace(block.Label),
		Mode:       normalizeDraftBlockMode(block.Mode),
		Content:    content,
		Decision:   strings.TrimSpace(block.Decision),
		Feedback:   strings.TrimSpace(block.Feedback),
		AcceptedAt: strings.TrimSpace(block.AcceptedAt),
	}
	return normalized, nil
}

func normalizeDraftBlockMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "paragraph":
		return "paragraph"
	default:
		return "section"
	}
}

func readDraftBlocks(raw any) []DraftBlock {
	items, ok := raw.([]any)
	if !ok {
		return []DraftBlock{}
	}
	out := make([]DraftBlock, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		block, err := normalizeDraftBlock(DraftBlock{
			BlockID:    readString(record, "block_id"),
			EnvelopeID: readString(record, "envelope_id"),
			Label:      readString(record, "label"),
			Mode:       readString(record, "mode"),
			Content:    readString(record, "content"),
			Decision:   readString(record, "decision"),
			Feedback:   readString(record, "feedback"),
			AcceptedAt: readString(record, "accepted_at"),
		})
		if err != nil {
			continue
		}
		out = append(out, block)
	}
	return out
}

func draftBlocksAny(blocks []DraftBlock) []any {
	out := make([]any, 0, len(blocks))
	for _, block := range blocks {
		record := map[string]any{
			"block_id": block.BlockID,
			"mode":     block.Mode,
			"content":  block.Content,
		}
		if block.EnvelopeID != "" {
			record["envelope_id"] = block.EnvelopeID
		}
		if block.Label != "" {
			record["label"] = block.Label
		}
		if block.Decision != "" {
			record["decision"] = block.Decision
		}
		if block.Feedback != "" {
			record["feedback"] = block.Feedback
		}
		if block.AcceptedAt != "" {
			record["accepted_at"] = block.AcceptedAt
		}
		out = append(out, record)
	}
	return out
}
