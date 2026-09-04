package room

import (
	"fmt"
	"strings"
)

const (
	DiffReviewPhaseID                   = "diff-review"
	diffReviewIDKey                     = "review_id"
	diffReviewFilesKey                  = "files"
	diffReviewCurrentFileKey            = "current_file"
	diffReviewFilterStateKey            = "filter_state"
	diffReviewDecisionsKey              = "decisions"
	diffReviewCommentsKey               = "comments"
	diffReviewUpdatedAtKey              = "updated_at"
	diffReviewSummaryKey                = "summary"
	diffReviewBeforeRefKey              = "before_ref"
	diffReviewAfterRefKey               = "after_ref"
	diffReviewExportRefsKey             = "export_refs"
	diffReviewDecisionFileIDKey         = "file_id"
	diffReviewDecisionHunkIDKey         = "hunk_id"
	diffReviewDecisionValueKey          = "decision"
	diffReviewDecisionCommentKey        = "comment"
	diffReviewDecisionActionIDKey       = "action_id"
	diffReviewDecisionDecidedAtKey      = "decided_at"
	diffReviewArtifactIDKey             = "artifact_id"
	diffReviewArtifactNameKey           = "name"
	diffReviewArtifactURIKey            = "uri"
	diffReviewArtifactMIMETypeKey       = "mime_type"
	diffReviewArtifactKindKey           = "kind"
	diffReviewArtifactSizeBytesKey      = "size_bytes"
	diffReviewSummarySubmittedAtKey     = "submitted_at"
	diffReviewSummaryDecisionFilesKey   = "decision_file_count"
	diffReviewSummaryDecisionHunksKey   = "decision_hunk_count"
	diffReviewSummaryCommentCountKey    = "comment_count"
	diffReviewSummaryExportTextKey      = "export_text"
	diffReviewSummaryExportNameKey      = "export_name"
	diffReviewSummaryPendingCountKey    = "pending_count"
	diffReviewSummaryDecisionSummaryKey = "decision_summary"
)

type DiffReviewDecision struct {
	FileID    string `json:"file_id"`
	HunkID    string `json:"hunk_id,omitempty"`
	Decision  string `json:"decision"`
	Comment   string `json:"comment,omitempty"`
	ActionID  string `json:"action_id,omitempty"`
	DecidedAt string `json:"decided_at,omitempty"`
}

type DiffReviewArtifactRef struct {
	ArtifactID string `json:"artifact_id,omitempty"`
	Name       string `json:"name,omitempty"`
	URI        string `json:"uri,omitempty"`
	MIMEType   string `json:"mime_type,omitempty"`
	Kind       string `json:"kind,omitempty"`
	SizeBytes  int    `json:"size_bytes,omitempty"`
}

type DiffReviewSummary struct {
	SubmittedAt       string         `json:"submitted_at,omitempty"`
	DecisionFileCount int            `json:"decision_file_count,omitempty"`
	DecisionHunkCount int            `json:"decision_hunk_count,omitempty"`
	CommentCount      int            `json:"comment_count,omitempty"`
	PendingCount      int            `json:"pending_count,omitempty"`
	DecisionSummary   map[string]int `json:"decision_summary,omitempty"`
	ExportText        string         `json:"export_text,omitempty"`
	ExportName        string         `json:"export_name,omitempty"`
}

type DiffReviewStateView struct {
	ReviewID    string                  `json:"review_id"`
	Files       []map[string]any        `json:"files"`
	CurrentFile string                  `json:"current_file,omitempty"`
	FilterState map[string]any          `json:"filter_state"`
	Decisions   []DiffReviewDecision    `json:"decisions"`
	Comments    map[string]string       `json:"comments"`
	UpdatedAt   string                  `json:"updated_at,omitempty"`
	Summary     *DiffReviewSummary      `json:"summary,omitempty"`
	BeforeRef   *DiffReviewArtifactRef  `json:"before_ref,omitempty"`
	AfterRef    *DiffReviewArtifactRef  `json:"after_ref,omitempty"`
	ExportRefs  []DiffReviewArtifactRef `json:"export_refs"`
}

type DiffReviewSnapshot struct {
	ReviewID    string
	Files       []map[string]any
	CurrentFile string
	FilterState map[string]any
	Decisions   []DiffReviewDecision
	Comments    map[string]string
	UpdatedAt   string
	Summary     *DiffReviewSummary
	BeforeRef   *DiffReviewArtifactRef
	AfterRef    *DiffReviewArtifactRef
	ExportRefs  []DiffReviewArtifactRef
}

func (r *Room) SaveDiffReviewSnapshot(snapshot DiffReviewSnapshot) error {
	normalized, err := normalizeDiffReviewSnapshot(snapshot)
	if err != nil {
		return err
	}

	r.phaseMu.Lock()
	defer r.phaseMu.Unlock()

	nextOutputs := clonePhaseOutputs(r.phaseOutputs)
	nextOutputs[DiffReviewPhaseID] = diffReviewBlobFromSnapshot(normalized)
	nextVisited := cloneStringSlice(r.phasesVisited)
	if err := r.persistPhaseState(r.currentPhase, nextVisited, nextOutputs); err != nil {
		return err
	}

	r.phasesVisited = nextVisited
	r.phaseOutputs = nextOutputs
	return nil
}

func (m *Manager) SaveDiffReviewSnapshot(roomID string, snapshot DiffReviewSnapshot) (PhaseState, error) {
	rm, ok := m.Get(roomID)
	if !ok {
		return PhaseState{}, fmt.Errorf("%w: %s", ErrRoomNotFound, roomID)
	}
	if err := rm.SaveDiffReviewSnapshot(snapshot); err != nil {
		return PhaseState{}, err
	}
	return rm.PhaseState(), nil
}

func ProjectDiffReviewState(state PhaseState) *DiffReviewStateView {
	return projectDiffReviewStateFromBlob(state.PhaseOutputs[DiffReviewPhaseID])
}

func projectDiffReviewStateFromBlob(blob PhaseOutput) *DiffReviewStateView {
	if len(blob.Data) == 0 {
		return nil
	}
	reviewID := readString(blob.Data, diffReviewIDKey)
	if reviewID == "" {
		return nil
	}
	view := &DiffReviewStateView{
		ReviewID:    reviewID,
		Files:       readObjectSlice(blob.Data[diffReviewFilesKey]),
		CurrentFile: readString(blob.Data, diffReviewCurrentFileKey),
		FilterState: readObjectMapValue(blob.Data[diffReviewFilterStateKey]),
		Decisions:   readDiffReviewDecisions(blob.Data[diffReviewDecisionsKey]),
		Comments:    readStringMap(blob.Data[diffReviewCommentsKey]),
		UpdatedAt:   readString(blob.Data, diffReviewUpdatedAtKey),
		ExportRefs:  readDiffReviewArtifactRefs(blob.Data[diffReviewExportRefsKey]),
	}
	if view.Files == nil {
		view.Files = []map[string]any{}
	}
	if view.FilterState == nil {
		view.FilterState = map[string]any{}
	}
	if view.Decisions == nil {
		view.Decisions = []DiffReviewDecision{}
	}
	if view.Comments == nil {
		view.Comments = map[string]string{}
	}
	if view.ExportRefs == nil {
		view.ExportRefs = []DiffReviewArtifactRef{}
	}
	if view.CurrentFile == "" && len(view.Files) > 0 {
		view.CurrentFile = strings.TrimSpace(readString(view.Files[0], "id"))
	}
	if summary := readDiffReviewSummary(blob.Data[diffReviewSummaryKey]); summary != nil {
		view.Summary = summary
	}
	if ref := readDiffReviewArtifactRef(blob.Data[diffReviewBeforeRefKey]); ref != nil {
		view.BeforeRef = ref
	}
	if ref := readDiffReviewArtifactRef(blob.Data[diffReviewAfterRefKey]); ref != nil {
		view.AfterRef = ref
	}
	return view
}

func normalizeDiffReviewSnapshot(snapshot DiffReviewSnapshot) (DiffReviewSnapshot, error) {
	reviewID := strings.TrimSpace(snapshot.ReviewID)
	if reviewID == "" {
		return DiffReviewSnapshot{}, ErrInvalidDiffReviewID
	}
	files, err := normalizeSpreadsheetObjectSlice(snapshot.Files, ErrInvalidDiffReviewFile)
	if err != nil {
		return DiffReviewSnapshot{}, err
	}
	validTargets, fileIDs, orderedFileIDs, err := collectDiffReviewTargets(files)
	if err != nil {
		return DiffReviewSnapshot{}, err
	}
	currentFile := strings.TrimSpace(snapshot.CurrentFile)
	if currentFile == "" && len(orderedFileIDs) > 0 {
		currentFile = orderedFileIDs[0]
	}
	if currentFile != "" {
		if _, ok := fileIDs[currentFile]; !ok {
			return DiffReviewSnapshot{}, fmt.Errorf("%w: unknown current_file %q", ErrInvalidDiffReviewFile, currentFile)
		}
	}
	filterState, err := normalizeObjectMap(snapshot.FilterState, ErrInvalidDiffReviewFilterState)
	if err != nil {
		return DiffReviewSnapshot{}, err
	}
	comments, err := normalizeDiffReviewComments(snapshot.Comments, validTargets)
	if err != nil {
		return DiffReviewSnapshot{}, err
	}
	decisions, err := normalizeDiffReviewDecisions(snapshot.Decisions, validTargets)
	if err != nil {
		return DiffReviewSnapshot{}, err
	}
	exportRefs, err := normalizeDiffReviewArtifactRefs(snapshot.ExportRefs)
	if err != nil {
		return DiffReviewSnapshot{}, err
	}
	beforeRef, err := normalizeDiffReviewArtifactRef(snapshot.BeforeRef)
	if err != nil {
		return DiffReviewSnapshot{}, err
	}
	afterRef, err := normalizeDiffReviewArtifactRef(snapshot.AfterRef)
	if err != nil {
		return DiffReviewSnapshot{}, err
	}
	return DiffReviewSnapshot{
		ReviewID:    reviewID,
		Files:       files,
		CurrentFile: currentFile,
		FilterState: filterState,
		Decisions:   decisions,
		Comments:    comments,
		UpdatedAt:   strings.TrimSpace(snapshot.UpdatedAt),
		Summary:     normalizeDiffReviewSummary(snapshot.Summary),
		BeforeRef:   beforeRef,
		AfterRef:    afterRef,
		ExportRefs:  exportRefs,
	}, nil
}

func diffReviewBlobFromSnapshot(snapshot DiffReviewSnapshot) PhaseOutput {
	record := map[string]any{
		diffReviewIDKey:          snapshot.ReviewID,
		diffReviewFilesKey:       cloneObjectSlice(snapshot.Files),
		diffReviewCurrentFileKey: snapshot.CurrentFile,
		diffReviewFilterStateKey: cloneAnyMap(snapshot.FilterState),
		diffReviewDecisionsKey:   diffReviewDecisionsAny(snapshot.Decisions),
		diffReviewCommentsKey:    diffReviewCommentsAny(snapshot.Comments),
		diffReviewUpdatedAtKey:   snapshot.UpdatedAt,
		diffReviewExportRefsKey:  diffReviewArtifactRefsAny(snapshot.ExportRefs),
	}
	if summary := diffReviewSummaryAny(snapshot.Summary); len(summary) > 0 {
		record[diffReviewSummaryKey] = summary
	}
	if before := diffReviewArtifactRefAny(snapshot.BeforeRef); len(before) > 0 {
		record[diffReviewBeforeRefKey] = before
	}
	if after := diffReviewArtifactRefAny(snapshot.AfterRef); len(after) > 0 {
		record[diffReviewAfterRefKey] = after
	}
	return PhaseOutput{Version: phaseOutputVersion, Data: record}
}

func collectDiffReviewTargets(files []map[string]any) (map[string]struct{}, map[string]struct{}, []string, error) {
	validTargets := map[string]struct{}{}
	fileIDs := map[string]struct{}{}
	orderedFileIDs := make([]string, 0, len(files))
	for _, file := range files {
		fileID := strings.TrimSpace(readString(file, "id"))
		if fileID == "" {
			return nil, nil, nil, ErrInvalidDiffReviewFile
		}
		if _, exists := fileIDs[fileID]; exists {
			return nil, nil, nil, fmt.Errorf("%w: duplicate file %q", ErrInvalidDiffReviewFile, fileID)
		}
		fileIDs[fileID] = struct{}{}
		orderedFileIDs = append(orderedFileIDs, fileID)
		validTargets[fileID] = struct{}{}
		hunks := readObjectSlice(file["hunks"])
		seenHunks := map[string]struct{}{}
		for _, hunk := range hunks {
			hunkID := strings.TrimSpace(readString(hunk, "id"))
			if hunkID == "" {
				continue
			}
			if _, exists := seenHunks[hunkID]; exists {
				return nil, nil, nil, fmt.Errorf("%w: duplicate hunk %q for file %q", ErrInvalidDiffReviewFile, hunkID, fileID)
			}
			seenHunks[hunkID] = struct{}{}
			validTargets[diffReviewCommentKey(fileID, hunkID)] = struct{}{}
		}
	}
	return validTargets, fileIDs, orderedFileIDs, nil
}

func normalizeDiffReviewComments(comments map[string]string, validTargets map[string]struct{}) (map[string]string, error) {
	if len(comments) == 0 {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(comments))
	for key, value := range comments {
		target := strings.TrimSpace(key)
		if target == "" {
			continue
		}
		if len(validTargets) > 0 {
			if _, ok := validTargets[target]; !ok {
				return nil, fmt.Errorf("%w: unknown comment target %q", ErrInvalidDiffReviewComment, target)
			}
		}
		text := strings.TrimSpace(value)
		if text == "" {
			continue
		}
		out[target] = text
	}
	return out, nil
}

func normalizeDiffReviewDecisions(items []DiffReviewDecision, validTargets map[string]struct{}) ([]DiffReviewDecision, error) {
	if len(items) == 0 {
		return []DiffReviewDecision{}, nil
	}
	out := make([]DiffReviewDecision, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		fileID := strings.TrimSpace(item.FileID)
		if fileID == "" {
			return nil, ErrInvalidDiffReviewDecision
		}
		hunkID := strings.TrimSpace(item.HunkID)
		target := diffReviewCommentKey(fileID, hunkID)
		if len(validTargets) > 0 {
			if _, ok := validTargets[target]; !ok {
				return nil, fmt.Errorf("%w: unknown target %q", ErrInvalidDiffReviewDecision, target)
			}
		}
		if _, exists := seen[target]; exists {
			return nil, fmt.Errorf("%w: duplicate target %q", ErrInvalidDiffReviewDecision, target)
		}
		seen[target] = struct{}{}
		decision := normalizeDiffReviewDecisionValue(item.Decision)
		if decision == "" {
			return nil, ErrInvalidDiffReviewDecision
		}
		out = append(out, DiffReviewDecision{
			FileID:    fileID,
			HunkID:    hunkID,
			Decision:  decision,
			Comment:   strings.TrimSpace(item.Comment),
			ActionID:  strings.TrimSpace(item.ActionID),
			DecidedAt: strings.TrimSpace(item.DecidedAt),
		})
	}
	return out, nil
}

func normalizeDiffReviewDecisionValue(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "accept", "approve":
		return "accept"
	case "reject", "request-changes", "request_changes":
		return "reject"
	case "comment", "comment-only", "comment_only":
		return "comment"
	default:
		return ""
	}
}

func normalizeDiffReviewArtifactRefs(refs []DiffReviewArtifactRef) ([]DiffReviewArtifactRef, error) {
	if len(refs) == 0 {
		return []DiffReviewArtifactRef{}, nil
	}
	out := make([]DiffReviewArtifactRef, 0, len(refs))
	for _, ref := range refs {
		normalized, err := normalizeDiffReviewArtifactRef(&ref)
		if err != nil {
			return nil, err
		}
		if normalized != nil {
			out = append(out, *normalized)
		}
	}
	return out, nil
}

func normalizeDiffReviewArtifactRef(ref *DiffReviewArtifactRef) (*DiffReviewArtifactRef, error) {
	if ref == nil {
		return nil, nil
	}
	out := &DiffReviewArtifactRef{
		ArtifactID: strings.TrimSpace(ref.ArtifactID),
		Name:       strings.TrimSpace(ref.Name),
		URI:        strings.TrimSpace(ref.URI),
		MIMEType:   strings.TrimSpace(ref.MIMEType),
		Kind:       strings.TrimSpace(ref.Kind),
		SizeBytes:  ref.SizeBytes,
	}
	if out.ArtifactID == "" && out.URI == "" && out.Name == "" {
		return nil, nil
	}
	if out.SizeBytes < 0 {
		return nil, ErrInvalidDiffReviewArtifactRef
	}
	return out, nil
}

func normalizeDiffReviewSummary(summary *DiffReviewSummary) *DiffReviewSummary {
	if summary == nil {
		return nil
	}
	out := &DiffReviewSummary{
		SubmittedAt:       strings.TrimSpace(summary.SubmittedAt),
		DecisionFileCount: max(summary.DecisionFileCount, 0),
		DecisionHunkCount: max(summary.DecisionHunkCount, 0),
		CommentCount:      max(summary.CommentCount, 0),
		PendingCount:      max(summary.PendingCount, 0),
		ExportText:        strings.TrimSpace(summary.ExportText),
		ExportName:        strings.TrimSpace(summary.ExportName),
	}
	if len(summary.DecisionSummary) > 0 {
		out.DecisionSummary = make(map[string]int, len(summary.DecisionSummary))
		for key, value := range summary.DecisionSummary {
			key = normalizeDiffReviewDecisionValue(key)
			if key == "" {
				continue
			}
			out.DecisionSummary[key] = max(value, 0)
		}
	}
	return out
}

func readDiffReviewDecisions(raw any) []DiffReviewDecision {
	records := readObjectSlice(raw)
	out := make([]DiffReviewDecision, 0, len(records))
	for _, record := range records {
		fileID := strings.TrimSpace(readString(record, diffReviewDecisionFileIDKey))
		decision := normalizeDiffReviewDecisionValue(readString(record, diffReviewDecisionValueKey))
		if fileID == "" || decision == "" {
			continue
		}
		out = append(out, DiffReviewDecision{
			FileID:    fileID,
			HunkID:    strings.TrimSpace(readString(record, diffReviewDecisionHunkIDKey)),
			Decision:  decision,
			Comment:   strings.TrimSpace(readString(record, diffReviewDecisionCommentKey)),
			ActionID:  strings.TrimSpace(readString(record, diffReviewDecisionActionIDKey)),
			DecidedAt: strings.TrimSpace(readString(record, diffReviewDecisionDecidedAtKey)),
		})
	}
	return out
}

func readStringMap(raw any) map[string]string {
	record, _ := raw.(map[string]any)
	if len(record) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(record))
	for key, value := range record {
		text, ok := value.(string)
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		text = strings.TrimSpace(text)
		if key == "" || text == "" {
			continue
		}
		out[key] = text
	}
	return out
}

func readDiffReviewSummary(raw any) *DiffReviewSummary {
	record, _ := raw.(map[string]any)
	if len(record) == 0 {
		return nil
	}
	summary := &DiffReviewSummary{
		SubmittedAt:       strings.TrimSpace(readString(record, diffReviewSummarySubmittedAtKey)),
		DecisionFileCount: int(readNumber(record, diffReviewSummaryDecisionFilesKey)),
		DecisionHunkCount: int(readNumber(record, diffReviewSummaryDecisionHunksKey)),
		CommentCount:      int(readNumber(record, diffReviewSummaryCommentCountKey)),
		PendingCount:      int(readNumber(record, diffReviewSummaryPendingCountKey)),
		ExportText:        strings.TrimSpace(readString(record, diffReviewSummaryExportTextKey)),
		ExportName:        strings.TrimSpace(readString(record, diffReviewSummaryExportNameKey)),
		DecisionSummary:   map[string]int{},
	}
	decisionSummary := readObjectMapValue(record[diffReviewSummaryDecisionSummaryKey])
	for key, value := range decisionSummary {
		normalized := normalizeDiffReviewDecisionValue(key)
		if normalized == "" {
			continue
		}
		switch typed := value.(type) {
		case float64:
			summary.DecisionSummary[normalized] = int(typed)
		case int:
			summary.DecisionSummary[normalized] = typed
		}
	}
	return summary
}

func readDiffReviewArtifactRef(raw any) *DiffReviewArtifactRef {
	record, _ := raw.(map[string]any)
	if len(record) == 0 {
		return nil
	}
	ref := &DiffReviewArtifactRef{
		ArtifactID: strings.TrimSpace(readString(record, diffReviewArtifactIDKey)),
		Name:       strings.TrimSpace(readString(record, diffReviewArtifactNameKey)),
		URI:        strings.TrimSpace(readString(record, diffReviewArtifactURIKey)),
		MIMEType:   strings.TrimSpace(readString(record, diffReviewArtifactMIMETypeKey)),
		Kind:       strings.TrimSpace(readString(record, diffReviewArtifactKindKey)),
		SizeBytes:  int(readNumber(record, diffReviewArtifactSizeBytesKey)),
	}
	if ref.ArtifactID == "" && ref.Name == "" && ref.URI == "" {
		return nil
	}
	return ref
}

func readDiffReviewArtifactRefs(raw any) []DiffReviewArtifactRef {
	records := readObjectSlice(raw)
	out := make([]DiffReviewArtifactRef, 0, len(records))
	for _, record := range records {
		if ref := readDiffReviewArtifactRef(record); ref != nil {
			out = append(out, *ref)
		}
	}
	return out
}

func diffReviewDecisionsAny(items []DiffReviewDecision) []map[string]any {
	if len(items) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			diffReviewDecisionFileIDKey: fileOrEmpty(item.FileID),
			diffReviewDecisionValueKey:  normalizeDiffReviewDecisionValue(item.Decision),
		}
		if item.HunkID != "" {
			record[diffReviewDecisionHunkIDKey] = item.HunkID
		}
		if item.Comment != "" {
			record[diffReviewDecisionCommentKey] = item.Comment
		}
		if item.ActionID != "" {
			record[diffReviewDecisionActionIDKey] = item.ActionID
		}
		if item.DecidedAt != "" {
			record[diffReviewDecisionDecidedAtKey] = item.DecidedAt
		}
		out = append(out, record)
	}
	return out
}

func diffReviewCommentsAny(items map[string]string) map[string]any {
	if len(items) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(items))
	for key, value := range items {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func diffReviewArtifactRefAny(ref *DiffReviewArtifactRef) map[string]any {
	if ref == nil {
		return map[string]any{}
	}
	record := map[string]any{}
	if ref.ArtifactID != "" {
		record[diffReviewArtifactIDKey] = ref.ArtifactID
	}
	if ref.Name != "" {
		record[diffReviewArtifactNameKey] = ref.Name
	}
	if ref.URI != "" {
		record[diffReviewArtifactURIKey] = ref.URI
	}
	if ref.MIMEType != "" {
		record[diffReviewArtifactMIMETypeKey] = ref.MIMEType
	}
	if ref.Kind != "" {
		record[diffReviewArtifactKindKey] = ref.Kind
	}
	if ref.SizeBytes > 0 {
		record[diffReviewArtifactSizeBytesKey] = ref.SizeBytes
	}
	return record
}

func diffReviewArtifactRefsAny(items []DiffReviewArtifactRef) []map[string]any {
	if len(items) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if record := diffReviewArtifactRefAny(&item); len(record) > 0 {
			out = append(out, record)
		}
	}
	return out
}

func diffReviewSummaryAny(summary *DiffReviewSummary) map[string]any {
	if summary == nil {
		return map[string]any{}
	}
	record := map[string]any{}
	if summary.SubmittedAt != "" {
		record[diffReviewSummarySubmittedAtKey] = summary.SubmittedAt
	}
	if summary.DecisionFileCount > 0 {
		record[diffReviewSummaryDecisionFilesKey] = summary.DecisionFileCount
	}
	if summary.DecisionHunkCount > 0 {
		record[diffReviewSummaryDecisionHunksKey] = summary.DecisionHunkCount
	}
	if summary.CommentCount > 0 {
		record[diffReviewSummaryCommentCountKey] = summary.CommentCount
	}
	if summary.PendingCount > 0 {
		record[diffReviewSummaryPendingCountKey] = summary.PendingCount
	}
	if summary.ExportText != "" {
		record[diffReviewSummaryExportTextKey] = summary.ExportText
	}
	if summary.ExportName != "" {
		record[diffReviewSummaryExportNameKey] = summary.ExportName
	}
	if len(summary.DecisionSummary) > 0 {
		decisionSummary := make(map[string]any, len(summary.DecisionSummary))
		for key, value := range summary.DecisionSummary {
			key = normalizeDiffReviewDecisionValue(key)
			if key == "" {
				continue
			}
			decisionSummary[key] = max(value, 0)
		}
		record[diffReviewSummaryDecisionSummaryKey] = decisionSummary
	}
	return record
}

func diffReviewCommentKey(fileID, hunkID string) string {
	fileID = strings.TrimSpace(fileID)
	hunkID = strings.TrimSpace(hunkID)
	if hunkID == "" {
		return fileID
	}
	return fileID + "::" + hunkID
}

func fileOrEmpty(value string) string {
	return strings.TrimSpace(value)
}
