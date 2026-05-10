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

type FilePickerStateView struct {
	PickerID           string                        `json:"picker_id"`
	BrowseRoots        []FilePickerBrowseRoot        `json:"browse_roots"`
	SelectedRefs       []FilePickerArtifactRef       `json:"selected_refs"`
	QueryState         map[string]any                `json:"query_state"`
	SelectionRevisions []FilePickerSelectionRevision `json:"selection_revisions"`
	UpdatedAt          string                        `json:"updated_at,omitempty"`
}

type FilePickerSnapshot struct {
	PickerID           string
	BrowseRoots        []FilePickerBrowseRoot
	SelectedRefs       []FilePickerArtifactRef
	QueryState         map[string]any
	SelectionRevisions []FilePickerSelectionRevision
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

func filePickerBlobFromSnapshot(snapshot FilePickerSnapshot) PhaseOutput {
	data := map[string]any{
		filePickerIDKey:                 snapshot.PickerID,
		filePickerBrowseRootsKey:        filePickerBrowseRootsAny(snapshot.BrowseRoots),
		filePickerSelectedRefsKey:       filePickerArtifactRefsAny(snapshot.SelectedRefs),
		filePickerQueryStateKey:         cloneAnyMap(snapshot.QueryState),
		filePickerSelectionRevisionsKey: filePickerSelectionRevisionsAny(snapshot.SelectionRevisions),
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
