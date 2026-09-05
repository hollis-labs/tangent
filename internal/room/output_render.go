package room

import (
	"fmt"
	"strings"
)

const (
	OutputPhaseID       = "output"
	outputTitleKey      = "title"
	outputMarkdownKey   = "markdown"
	outputFilenameKey   = "filename"
	outputFormatKey     = "format"
	outputSummaryKey    = "summary"
	outputUpdatedAtKey  = "updated_at"
	defaultOutputFormat = "markdown"
)

type FinalOutputView struct {
	Title     string `json:"title,omitempty"`
	Markdown  string `json:"markdown"`
	Filename  string `json:"filename,omitempty"`
	Format    string `json:"format"`
	Summary   string `json:"summary,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	WordCount int    `json:"word_count"`
}

func (r *Room) SetFinalOutput(view FinalOutputView) error {
	normalized, err := normalizeFinalOutput(view)
	if err != nil {
		return err
	}

	entries := map[string]any{
		outputMarkdownKey: normalized.Markdown,
		outputFormatKey:   normalized.Format,
	}
	if normalized.Title != "" {
		entries[outputTitleKey] = normalized.Title
	}
	if normalized.Filename != "" {
		entries[outputFilenameKey] = normalized.Filename
	}
	if normalized.Summary != "" {
		entries[outputSummaryKey] = normalized.Summary
	}
	if normalized.UpdatedAt != "" {
		entries[outputUpdatedAtKey] = normalized.UpdatedAt
	}

	r.phaseMu.Lock()
	defer r.phaseMu.Unlock()

	nextOutputs := clonePhaseOutputs(r.phaseOutputs)
	blob := nextOutputs[OutputPhaseID]
	switch {
	case blob.Version == 0:
		blob.Version = phaseOutputVersion
	case blob.Version != phaseOutputVersion:
		return fmt.Errorf("room: unsupported phase output version %d for %q", blob.Version, OutputPhaseID)
	}
	if blob.Data == nil {
		blob.Data = map[string]any{}
	}
	for key, value := range entries {
		blob.Data[key] = value
	}
	nextOutputs[OutputPhaseID] = blob

	nextVisited := cloneStringSlice(r.phasesVisited)
	if err := r.persistPhaseState(r.currentPhase, nextVisited, nextOutputs); err != nil {
		return err
	}

	r.phasesVisited = nextVisited
	r.phaseOutputs = nextOutputs
	return nil
}

func (m *Manager) SetFinalOutput(roomID string, view FinalOutputView) (PhaseState, error) {
	rm, ok := m.Get(roomID)
	if !ok {
		return PhaseState{}, fmt.Errorf("%w: %s", ErrRoomNotFound, roomID)
	}
	if err := rm.SetFinalOutput(view); err != nil {
		return PhaseState{}, err
	}
	return rm.PhaseState(), nil
}

func ProjectFinalOutput(state PhaseState) *FinalOutputView {
	blob, ok := state.PhaseOutputs[OutputPhaseID]
	if !ok || len(blob.Data) == 0 {
		return nil
	}
	view, err := normalizeFinalOutput(FinalOutputView{
		Title:     readString(blob.Data, outputTitleKey),
		Markdown:  readString(blob.Data, outputMarkdownKey),
		Filename:  readString(blob.Data, outputFilenameKey),
		Format:    readString(blob.Data, outputFormatKey),
		Summary:   readString(blob.Data, outputSummaryKey),
		UpdatedAt: readString(blob.Data, outputUpdatedAtKey),
	})
	if err != nil {
		return nil
	}
	return &view
}

func normalizeFinalOutput(view FinalOutputView) (FinalOutputView, error) {
	markdown := strings.TrimSpace(view.Markdown)
	if markdown == "" {
		return FinalOutputView{}, ErrInvalidFinalOutputMarkdown
	}
	format := strings.TrimSpace(view.Format)
	if format == "" {
		format = defaultOutputFormat
	}
	if format != defaultOutputFormat {
		return FinalOutputView{}, ErrInvalidFinalOutputFormat
	}
	return FinalOutputView{
		Title:    strings.TrimSpace(view.Title),
		Markdown: markdown,
		// A caller-declared download name is sanitized rather than trusted:
		// it reaches `link.download` in the SPA, and until now the caller
		// chose the name a file landed under in the operator's Downloads
		// folder, separators and all. See room.SafeExportFilename.
		Filename:  SafeExportFilename(view.Filename),
		Format:    format,
		Summary:   strings.TrimSpace(view.Summary),
		UpdatedAt: strings.TrimSpace(view.UpdatedAt),
		WordCount: countWords(markdown),
	}, nil
}

func countWords(markdown string) int {
	if markdown == "" {
		return 0
	}
	return len(strings.Fields(markdown))
}
