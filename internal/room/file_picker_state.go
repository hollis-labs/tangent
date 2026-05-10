package room

import (
	"fmt"
	"path"
	"strings"
)

const (
	FilePickerPhaseID                        = "file_picker"
	filePickerIDKey                          = "picker_id"
	filePickerBrowseRootsKey                 = "browse_roots"
	filePickerSelectedRefsKey                = "selected_refs"
	filePickerQueryStateKey                  = "query_state"
	filePickerSelectionRevisionsKey          = "selection_revisions"
	filePickerSubmissionSummaryKey           = "submission_summary"
	filePickerHandoffKey                     = "handoff"
	filePickerUpdatedAtKey                   = "updated_at"
	filePickerBrowseRootIDKey                = "root_id"
	filePickerBrowseRootLabelKey             = "label"
	filePickerBrowseRootPathKey              = "path"
	filePickerBrowseRootKindKey              = "kind"
	filePickerSelectedRefArtifactIDKey       = "artifact_id"
	filePickerSelectedRefNameKey             = "name"
	filePickerSelectedRefURIKey              = "uri"
	filePickerSelectedRefMIMETypeKey         = "mime_type"
	filePickerSelectedRefKindKey             = "kind"
	filePickerSelectedRefSizeBytesKey        = "size_bytes"
	filePickerSelectedRefRootIDKey           = "root_id"
	filePickerSelectedRefRelativePathKey     = "relative_path"
	filePickerSelectionRevisionIDKey         = "selection_revision_id"
	filePickerSelectionRevisionSubmittedAt   = "submitted_at"
	filePickerSelectionRevisionSelectedCount = "selected_count"
	filePickerSummarySelectionRevisionID     = "selection_revision_id"
	filePickerSummarySelectedNames           = "selected_names"
	filePickerSummarySelectedCount           = "selected_count"
	filePickerSummarySubmittedAt             = "submitted_at"
	filePickerHandoffRevisionID              = "selection_revision_id"
	filePickerHandoffArtifactRefs            = "artifact_refs"
	filePickerHandoffSummary                 = "summary"
)

type FilePickerBrowseRoot struct {
	RootID string `json:"root_id"`
	Label  string `json:"label,omitempty"`
	Path   string `json:"path"`
	Kind   string `json:"kind,omitempty"`
}

type FilePickerArtifactRef struct {
	ArtifactID   string `json:"artifact_id,omitempty"`
	Name         string `json:"name,omitempty"`
	URI          string `json:"uri,omitempty"`
	MIMEType     string `json:"mime_type,omitempty"`
	Kind         string `json:"kind,omitempty"`
	SizeBytes    int    `json:"size_bytes,omitempty"`
	RootID       string `json:"root_id"`
	RelativePath string `json:"relative_path"`
}

type FilePickerSelectionRevision struct {
	SelectionRevisionID string `json:"selection_revision_id,omitempty"`
	SubmittedAt         string `json:"submitted_at,omitempty"`
	SelectedCount       int    `json:"selected_count,omitempty"`
}

type FilePickerSubmissionSummary struct {
	SelectionRevisionID string   `json:"selection_revision_id,omitempty"`
	SelectedNames       []string `json:"selected_names"`
	SelectedCount       int      `json:"selected_count,omitempty"`
	SubmittedAt         string   `json:"submitted_at,omitempty"`
}

type FilePickerHandoff struct {
	SelectionRevisionID string                       `json:"selection_revision_id,omitempty"`
	ArtifactRefs        []FilePickerArtifactRef      `json:"artifact_refs"`
	Summary             *FilePickerSubmissionSummary `json:"summary,omitempty"`
}

type FilePickerStateView struct {
	PickerID           string                        `json:"picker_id"`
	BrowseRoots        []FilePickerBrowseRoot        `json:"browse_roots"`
	SelectedRefs       []FilePickerArtifactRef       `json:"selected_refs"`
	QueryState         map[string]any                `json:"query_state"`
	SelectionRevisions []FilePickerSelectionRevision `json:"selection_revisions"`
	SubmissionSummary  *FilePickerSubmissionSummary  `json:"submission_summary,omitempty"`
	Handoff            *FilePickerHandoff            `json:"handoff,omitempty"`
	UpdatedAt          string                        `json:"updated_at,omitempty"`
}

type FilePickerSnapshot struct {
	PickerID           string
	BrowseRoots        []FilePickerBrowseRoot
	SelectedRefs       []FilePickerArtifactRef
	QueryState         map[string]any
	SelectionRevisions []FilePickerSelectionRevision
	SubmissionSummary  *FilePickerSubmissionSummary
	Handoff            *FilePickerHandoff
	UpdatedAt          string
}

func (r *Room) SaveFilePickerSnapshot(snapshot FilePickerSnapshot) error {
	normalized, err := normalizeFilePickerSnapshot(snapshot)
	if err != nil {
		return err
	}

	r.phaseMu.Lock()
	defer r.phaseMu.Unlock()

	nextOutputs := clonePhaseOutputs(r.phaseOutputs)
	nextOutputs[FilePickerPhaseID] = filePickerBlobFromSnapshot(normalized)
	nextVisited := cloneStringSlice(r.phasesVisited)
	if err := r.persistPhaseState(r.currentPhase, nextVisited, nextOutputs); err != nil {
		return err
	}

	r.phasesVisited = nextVisited
	r.phaseOutputs = nextOutputs
	return nil
}

func (m *Manager) SaveFilePickerSnapshot(roomID string, snapshot FilePickerSnapshot) (PhaseState, error) {
	rm, ok := m.Get(roomID)
	if !ok {
		return PhaseState{}, fmt.Errorf("%w: %s", ErrRoomNotFound, roomID)
	}
	if err := rm.SaveFilePickerSnapshot(snapshot); err != nil {
		return PhaseState{}, err
	}
	return rm.PhaseState(), nil
}

func ProjectFilePickerState(state PhaseState) *FilePickerStateView {
	return projectFilePickerStateFromBlob(state.PhaseOutputs[FilePickerPhaseID])
}

func projectFilePickerStateFromBlob(blob PhaseOutput) *FilePickerStateView {
	if len(blob.Data) == 0 {
		return nil
	}
	pickerID := readString(blob.Data, filePickerIDKey)
	if pickerID == "" {
		return nil
	}
	view := &FilePickerStateView{
		PickerID:           pickerID,
		BrowseRoots:        readFilePickerBrowseRoots(blob.Data[filePickerBrowseRootsKey]),
		SelectedRefs:       readFilePickerArtifactRefs(blob.Data[filePickerSelectedRefsKey]),
		QueryState:         readFilePickerQueryState(blob.Data[filePickerQueryStateKey]),
		SelectionRevisions: readFilePickerSelectionRevisions(blob.Data[filePickerSelectionRevisionsKey]),
		SubmissionSummary:  readFilePickerSubmissionSummary(blob.Data[filePickerSubmissionSummaryKey]),
		Handoff:            readFilePickerHandoff(blob.Data[filePickerHandoffKey]),
		UpdatedAt:          readString(blob.Data, filePickerUpdatedAtKey),
	}
	if view.BrowseRoots == nil {
		view.BrowseRoots = []FilePickerBrowseRoot{}
	}
	if view.SelectedRefs == nil {
		view.SelectedRefs = []FilePickerArtifactRef{}
	}
	if view.QueryState == nil {
		view.QueryState = map[string]any{}
	}
	if view.SelectionRevisions == nil {
		view.SelectionRevisions = []FilePickerSelectionRevision{}
	}
	return view
}

func normalizeFilePickerSnapshot(snapshot FilePickerSnapshot) (FilePickerSnapshot, error) {
	pickerID := strings.TrimSpace(snapshot.PickerID)
	if pickerID == "" {
		return FilePickerSnapshot{}, ErrInvalidFilePickerID
	}
	browseRoots, err := normalizeFilePickerBrowseRoots(snapshot.BrowseRoots)
	if err != nil {
		return FilePickerSnapshot{}, err
	}
	rootIDs := make(map[string]struct{}, len(browseRoots))
	for _, root := range browseRoots {
		rootIDs[root.RootID] = struct{}{}
	}
	selectedRefs, err := normalizeFilePickerArtifactRefs(snapshot.SelectedRefs, rootIDs)
	if err != nil {
		return FilePickerSnapshot{}, err
	}
	queryState, err := normalizeFormMap(snapshot.QueryState, ErrInvalidFilePickerQueryState)
	if err != nil {
		return FilePickerSnapshot{}, err
	}
	revisions, err := normalizeFilePickerSelectionRevisions(snapshot.SelectionRevisions)
	if err != nil {
		return FilePickerSnapshot{}, err
	}
	return FilePickerSnapshot{
		PickerID:           pickerID,
		BrowseRoots:        browseRoots,
		SelectedRefs:       selectedRefs,
		QueryState:         queryState,
		SelectionRevisions: revisions,
		SubmissionSummary:  normalizeFilePickerSubmissionSummary(snapshot.SubmissionSummary),
		Handoff:            normalizeFilePickerHandoff(snapshot.Handoff, rootIDs),
		UpdatedAt:          strings.TrimSpace(snapshot.UpdatedAt),
	}, nil
}

func normalizeFilePickerBrowseRoots(items []FilePickerBrowseRoot) ([]FilePickerBrowseRoot, error) {
	if len(items) == 0 {
		return []FilePickerBrowseRoot{}, nil
	}
	out := make([]FilePickerBrowseRoot, 0, len(items))
	seenIDs := map[string]struct{}{}
	seenPaths := map[string]struct{}{}
	for _, item := range items {
		rootID := strings.TrimSpace(item.RootID)
		rootPath := strings.TrimSpace(item.Path)
		if rootID == "" || rootPath == "" {
			return nil, ErrInvalidFilePickerBrowseRoot
		}
		if _, exists := seenIDs[rootID]; exists {
			return nil, fmt.Errorf("%w: duplicate root_id %q", ErrInvalidFilePickerBrowseRoot, rootID)
		}
		if _, exists := seenPaths[rootPath]; exists {
			return nil, fmt.Errorf("%w: duplicate path %q", ErrInvalidFilePickerBrowseRoot, rootPath)
		}
		seenIDs[rootID] = struct{}{}
		seenPaths[rootPath] = struct{}{}
		kind := strings.TrimSpace(item.Kind)
		if kind == "" {
			kind = "directory"
		}
		if kind != "directory" {
			return nil, ErrInvalidFilePickerBrowseRoot
		}
		label := strings.TrimSpace(item.Label)
		if label == "" {
			label = rootID
		}
		out = append(out, FilePickerBrowseRoot{
			RootID: rootID,
			Label:  label,
			Path:   rootPath,
			Kind:   kind,
		})
	}
	return out, nil
}

func normalizeFilePickerArtifactRefs(items []FilePickerArtifactRef, validRootIDs map[string]struct{}) ([]FilePickerArtifactRef, error) {
	if len(items) == 0 {
		return []FilePickerArtifactRef{}, nil
	}
	out := make([]FilePickerArtifactRef, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		normalized, key, err := normalizeFilePickerArtifactRef(item, validRootIDs)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: duplicate selection %q", ErrInvalidFilePickerSelectionRef, key)
		}
		seen[key] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

func normalizeFilePickerArtifactRef(item FilePickerArtifactRef, validRootIDs map[string]struct{}) (FilePickerArtifactRef, string, error) {
	normalized := FilePickerArtifactRef{
		ArtifactID:   strings.TrimSpace(item.ArtifactID),
		Name:         strings.TrimSpace(item.Name),
		URI:          strings.TrimSpace(item.URI),
		MIMEType:     strings.TrimSpace(item.MIMEType),
		Kind:         strings.TrimSpace(item.Kind),
		SizeBytes:    item.SizeBytes,
		RootID:       strings.TrimSpace(item.RootID),
		RelativePath: strings.TrimSpace(item.RelativePath),
	}
	if normalized.RootID == "" || normalized.RelativePath == "" {
		return FilePickerArtifactRef{}, "", ErrInvalidFilePickerSelectionRef
	}
	if len(validRootIDs) > 0 {
		if _, ok := validRootIDs[normalized.RootID]; !ok {
			return FilePickerArtifactRef{}, "", fmt.Errorf("%w: unknown root_id %q", ErrInvalidFilePickerSelectionRef, normalized.RootID)
		}
	}
	if normalized.SizeBytes < 0 {
		return FilePickerArtifactRef{}, "", ErrInvalidFilePickerSelectionRef
	}
	cleanRelativePath, err := normalizeFilePickerRelativePath(normalized.RelativePath)
	if err != nil {
		return FilePickerArtifactRef{}, "", err
	}
	normalized.RelativePath = cleanRelativePath
	if normalized.URI == "" && normalized.ArtifactID != "" {
		normalized.URI = "artifact://" + normalized.ArtifactID
	}
	if normalized.ArtifactID == "" && strings.HasPrefix(normalized.URI, "artifact://") {
		normalized.ArtifactID = strings.TrimPrefix(normalized.URI, "artifact://")
	}
	if normalized.ArtifactID == "" || normalized.URI == "" {
		return FilePickerArtifactRef{}, "", ErrInvalidFilePickerSelectionRef
	}
	if strings.HasPrefix(normalized.URI, "blob:") || strings.HasPrefix(normalized.URI, "data:") || strings.HasPrefix(normalized.URI, "file:") {
		return FilePickerArtifactRef{}, "", ErrInvalidFilePickerSelectionRef
	}
	if !strings.HasPrefix(normalized.URI, "artifact://") {
		return FilePickerArtifactRef{}, "", ErrInvalidFilePickerSelectionRef
	}
	if normalized.Kind == "" {
		normalized.Kind = "file"
	}
	if normalized.Kind != "file" {
		return FilePickerArtifactRef{}, "", ErrInvalidFilePickerSelectionRef
	}
	return normalized, normalized.RootID + ":" + normalized.RelativePath, nil
}

func normalizeFilePickerRelativePath(value string) (string, error) {
	cleaned := path.Clean(strings.ReplaceAll(value, "\\", "/"))
	if cleaned == "." || cleaned == "" || strings.HasPrefix(cleaned, "/") || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", ErrInvalidFilePickerSelectionRef
	}
	return cleaned, nil
}

func normalizeFilePickerSelectionRevisions(items []FilePickerSelectionRevision) ([]FilePickerSelectionRevision, error) {
	if len(items) == 0 {
		return []FilePickerSelectionRevision{}, nil
	}
	out := make([]FilePickerSelectionRevision, 0, len(items))
	prevID := ""
	for _, item := range items {
		revisionID := strings.TrimSpace(item.SelectionRevisionID)
		if revisionID != "" && revisionID == prevID {
			return nil, fmt.Errorf("%w: duplicate selection_revision_id %q", ErrInvalidFilePickerSelectionRevision, revisionID)
		}
		prevID = revisionID
		if item.SelectedCount < 0 {
			return nil, ErrInvalidFilePickerSelectionRevision
		}
		out = append(out, FilePickerSelectionRevision{
			SelectionRevisionID: revisionID,
			SubmittedAt:         strings.TrimSpace(item.SubmittedAt),
			SelectedCount:       item.SelectedCount,
		})
	}
	return out, nil
}

func normalizeFilePickerSubmissionSummary(summary *FilePickerSubmissionSummary) *FilePickerSubmissionSummary {
	if summary == nil {
		return nil
	}
	out := &FilePickerSubmissionSummary{
		SelectionRevisionID: strings.TrimSpace(summary.SelectionRevisionID),
		SelectedCount:       max(summary.SelectedCount, 0),
		SubmittedAt:         strings.TrimSpace(summary.SubmittedAt),
		SelectedNames:       []string{},
	}
	for _, name := range summary.SelectedNames {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		out.SelectedNames = append(out.SelectedNames, trimmed)
	}
	return out
}

func normalizeFilePickerHandoff(handoff *FilePickerHandoff, validRootIDs map[string]struct{}) *FilePickerHandoff {
	if handoff == nil {
		return nil
	}
	refs, err := normalizeFilePickerArtifactRefs(handoff.ArtifactRefs, validRootIDs)
	if err != nil {
		return nil
	}
	return &FilePickerHandoff{
		SelectionRevisionID: strings.TrimSpace(handoff.SelectionRevisionID),
		ArtifactRefs:        refs,
		Summary:             normalizeFilePickerSubmissionSummary(handoff.Summary),
	}
}

func filePickerBlobFromSnapshot(snapshot FilePickerSnapshot) PhaseOutput {
	data := map[string]any{
		filePickerIDKey:                 snapshot.PickerID,
		filePickerBrowseRootsKey:        filePickerBrowseRootsAny(snapshot.BrowseRoots),
		filePickerSelectedRefsKey:       filePickerArtifactRefsAny(snapshot.SelectedRefs),
		filePickerQueryStateKey:         cloneAnyMap(snapshot.QueryState),
		filePickerSelectionRevisionsKey: filePickerSelectionRevisionsAny(snapshot.SelectionRevisions),
	}
	if summary := filePickerSubmissionSummaryAny(snapshot.SubmissionSummary); len(summary) > 0 {
		data[filePickerSubmissionSummaryKey] = summary
	}
	if handoff := filePickerHandoffAny(snapshot.Handoff); len(handoff) > 0 {
		data[filePickerHandoffKey] = handoff
	}
	if snapshot.UpdatedAt != "" {
		data[filePickerUpdatedAtKey] = snapshot.UpdatedAt
	}
	return PhaseOutput{
		Version: phaseOutputVersion,
		Data:    data,
	}
}

func filePickerBrowseRootsAny(items []FilePickerBrowseRoot) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			filePickerBrowseRootIDKey:    item.RootID,
			filePickerBrowseRootLabelKey: item.Label,
			filePickerBrowseRootPathKey:  item.Path,
			filePickerBrowseRootKindKey:  item.Kind,
		}
		out = append(out, record)
	}
	return out
}

func filePickerArtifactRefsAny(items []FilePickerArtifactRef) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			filePickerSelectedRefArtifactIDKey:   item.ArtifactID,
			filePickerSelectedRefURIKey:          item.URI,
			filePickerSelectedRefRootIDKey:       item.RootID,
			filePickerSelectedRefRelativePathKey: item.RelativePath,
			filePickerSelectedRefKindKey:         item.Kind,
		}
		if item.Name != "" {
			record[filePickerSelectedRefNameKey] = item.Name
		}
		if item.MIMEType != "" {
			record[filePickerSelectedRefMIMETypeKey] = item.MIMEType
		}
		if item.SizeBytes > 0 {
			record[filePickerSelectedRefSizeBytesKey] = item.SizeBytes
		}
		out = append(out, record)
	}
	return out
}

func filePickerSelectionRevisionsAny(items []FilePickerSelectionRevision) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			filePickerSelectionRevisionSubmittedAt:   item.SubmittedAt,
			filePickerSelectionRevisionSelectedCount: item.SelectedCount,
		}
		if item.SelectionRevisionID != "" {
			record[filePickerSelectionRevisionIDKey] = item.SelectionRevisionID
		}
		out = append(out, record)
	}
	return out
}

func filePickerSubmissionSummaryAny(summary *FilePickerSubmissionSummary) map[string]any {
	if summary == nil {
		return map[string]any{}
	}
	record := map[string]any{
		filePickerSummarySelectedNames: cloneStringSlice(summary.SelectedNames),
		filePickerSummarySelectedCount: summary.SelectedCount,
	}
	if summary.SelectionRevisionID != "" {
		record[filePickerSummarySelectionRevisionID] = summary.SelectionRevisionID
	}
	if summary.SubmittedAt != "" {
		record[filePickerSummarySubmittedAt] = summary.SubmittedAt
	}
	return record
}

func filePickerHandoffAny(handoff *FilePickerHandoff) map[string]any {
	if handoff == nil {
		return map[string]any{}
	}
	record := map[string]any{
		filePickerHandoffArtifactRefs: filePickerArtifactRefsAny(handoff.ArtifactRefs),
	}
	if handoff.SelectionRevisionID != "" {
		record[filePickerHandoffRevisionID] = handoff.SelectionRevisionID
	}
	if summary := filePickerSubmissionSummaryAny(handoff.Summary); len(summary) > 0 {
		record[filePickerHandoffSummary] = summary
	}
	return record
}

func readFilePickerBrowseRoots(raw any) []FilePickerBrowseRoot {
	records := readObjectSlice(raw)
	out := make([]FilePickerBrowseRoot, 0, len(records))
	for _, record := range records {
		root, err := normalizeFilePickerBrowseRoots([]FilePickerBrowseRoot{{
			RootID: readString(record, filePickerBrowseRootIDKey),
			Label:  readString(record, filePickerBrowseRootLabelKey),
			Path:   readString(record, filePickerBrowseRootPathKey),
			Kind:   readString(record, filePickerBrowseRootKindKey),
		}})
		if err != nil || len(root) == 0 {
			continue
		}
		out = append(out, root[0])
	}
	return out
}

func readFilePickerArtifactRefs(raw any) []FilePickerArtifactRef {
	records := readObjectSlice(raw)
	out := make([]FilePickerArtifactRef, 0, len(records))
	for _, record := range records {
		ref, _, err := normalizeFilePickerArtifactRef(FilePickerArtifactRef{
			ArtifactID:   readString(record, filePickerSelectedRefArtifactIDKey),
			Name:         readString(record, filePickerSelectedRefNameKey),
			URI:          readString(record, filePickerSelectedRefURIKey),
			MIMEType:     readString(record, filePickerSelectedRefMIMETypeKey),
			Kind:         readString(record, filePickerSelectedRefKindKey),
			SizeBytes:    readInt(record, filePickerSelectedRefSizeBytesKey),
			RootID:       readString(record, filePickerSelectedRefRootIDKey),
			RelativePath: readString(record, filePickerSelectedRefRelativePathKey),
		}, nil)
		if err != nil {
			continue
		}
		out = append(out, ref)
	}
	return out
}

func readFilePickerSelectionRevisions(raw any) []FilePickerSelectionRevision {
	records := readObjectSlice(raw)
	out := make([]FilePickerSelectionRevision, 0, len(records))
	for _, record := range records {
		revision := FilePickerSelectionRevision{
			SelectionRevisionID: readString(record, filePickerSelectionRevisionIDKey),
			SubmittedAt:         readString(record, filePickerSelectionRevisionSubmittedAt),
			SelectedCount:       readInt(record, filePickerSelectionRevisionSelectedCount),
		}
		if revision.SelectedCount < 0 {
			continue
		}
		out = append(out, revision)
	}
	return out
}

func readFilePickerSubmissionSummary(raw any) *FilePickerSubmissionSummary {
	record, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return normalizeFilePickerSubmissionSummary(&FilePickerSubmissionSummary{
		SelectionRevisionID: readString(record, filePickerSummarySelectionRevisionID),
		SelectedNames:       readStringSlice(record, filePickerSummarySelectedNames),
		SelectedCount:       readInt(record, filePickerSummarySelectedCount),
		SubmittedAt:         readString(record, filePickerSummarySubmittedAt),
	})
}

func readFilePickerHandoff(raw any) *FilePickerHandoff {
	record, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return &FilePickerHandoff{
		SelectionRevisionID: readString(record, filePickerHandoffRevisionID),
		ArtifactRefs:        readFilePickerArtifactRefs(record[filePickerHandoffArtifactRefs]),
		Summary:             readFilePickerSubmissionSummary(record[filePickerHandoffSummary]),
	}
}

func readFilePickerQueryState(raw any) map[string]any {
	record, ok := raw.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	out, err := normalizeFormMap(record, ErrInvalidFilePickerQueryState)
	if err != nil {
		return map[string]any{}
	}
	return out
}
