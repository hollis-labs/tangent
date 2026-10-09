package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	envelopes "github.com/hollis-labs/libs/ui-go/envelopes"

	"github.com/hollis-labs/tangent/internal/room"
)

type diffReviewInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

type diffReviewSubmitDraft struct {
	ReviewID    string                       `json:"review_id"`
	CurrentFile string                       `json:"current_file,omitempty"`
	FilterState map[string]any               `json:"filter_state,omitempty"`
	Decisions   []room.DiffReviewDecision    `json:"decisions,omitempty"`
	Comments    map[string]string            `json:"comments,omitempty"`
	Summary     *room.DiffReviewSummary      `json:"summary,omitempty"`
	BeforeRef   *room.DiffReviewArtifactRef  `json:"before_ref,omitempty"`
	AfterRef    *room.DiffReviewArtifactRef  `json:"after_ref,omitempty"`
	ExportRefs  []room.DiffReviewArtifactRef `json:"export_refs,omitempty"`
}

type diffReviewSubmitPayload struct {
	ReviewID    string                       `json:"review_id"`
	CurrentFile string                       `json:"current_file,omitempty"`
	FilterState map[string]any               `json:"filter_state,omitempty"`
	Decisions   []room.DiffReviewDecision    `json:"decisions"`
	Comments    map[string]string            `json:"comments"`
	Summary     *room.DiffReviewSummary      `json:"summary,omitempty"`
	BeforeRef   *room.DiffReviewArtifactRef  `json:"before_ref,omitempty"`
	AfterRef    *room.DiffReviewArtifactRef  `json:"after_ref,omitempty"`
	ExportRefs  []room.DiffReviewArtifactRef `json:"export_refs,omitempty"`
}

func (s *Server) handleDiffReview(
	ctx context.Context,
	args diffReviewInput,
) (any, error) {
	if args.Envelope.Type != diffReviewEnvelopeType {
		return nil, toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf("tangent.diff-review rejects envelope type %q; want %q", args.Envelope.Type, diffReviewEnvelopeType),
		)
	}

	roomID, err := s.resolveWorkflowRoom(ctx, "diff-review", &args.Envelope)
	if err != nil {
		return nil, err
	}

	phaseState, found, err := s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return nil, toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("load room phase state: %v", err))
	}
	if !found {
		return nil, toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID))
	}

	snapshot := diffReviewSnapshotFromEnvelope(args.Envelope, room.ProjectDiffReviewState(phaseState))
	if _, saveErr := s.manager.SaveDiffReviewSnapshot(roomID, snapshot); saveErr != nil {
		return nil, sessionPhaseStateError(roomID, saveErr)
	}

	phaseState, found, err = s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return nil, toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("reload room diff-review state: %v", err))
	}
	if !found {
		return nil, toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID))
	}

	return s.advanceRoomEnvelope(
		ctx,
		roomID,
		&args.Envelope,
		buildVisibleDiffReviewEnvelope(&args.Envelope, room.ProjectDiffReviewState(phaseState)),
		args.Completion,
	)
}

func diffReviewSnapshotFromEnvelope(env envelopes.Envelope, persisted *room.DiffReviewStateView) room.DiffReviewSnapshot {
	data := env.Data
	reviewID := readStringValue(data, "review_id")
	if reviewID == "" && persisted != nil {
		reviewID = persisted.ReviewID
	}
	reusePersisted := persisted != nil && persisted.ReviewID != "" && persisted.ReviewID == reviewID

	files := readObjectSliceValue(data["files"])
	if reusePersisted {
		files = persisted.Files
	} else if len(files) == 0 && persisted != nil {
		files = persisted.Files
	}
	currentFile := readStringValue(data, "current_file")
	if reusePersisted {
		currentFile = persisted.CurrentFile
	}
	filterState := readObjectValue(data, "filter_state")
	if reusePersisted && len(persisted.FilterState) > 0 {
		filterState = persisted.FilterState
	}
	beforeRef := readDiffReviewArtifactRefValue(data["before_ref"])
	afterRef := readDiffReviewArtifactRefValue(data["after_ref"])
	if reusePersisted {
		beforeRef = cloneDiffReviewArtifactRef(persisted.BeforeRef)
		afterRef = cloneDiffReviewArtifactRef(persisted.AfterRef)
	}
	snapshot := room.DiffReviewSnapshot{
		ReviewID:    reviewID,
		Files:       files,
		CurrentFile: currentFile,
		FilterState: filterState,
		UpdatedAt:   nowRFC3339(),
		BeforeRef:   beforeRef,
		AfterRef:    afterRef,
	}
	if reusePersisted {
		snapshot.Decisions = persisted.Decisions
		snapshot.Comments = persisted.Comments
		snapshot.Summary = persisted.Summary
		snapshot.ExportRefs = persisted.ExportRefs
	}
	return snapshot
}

func buildVisibleDiffReviewEnvelope(env *envelopes.Envelope, view *room.DiffReviewStateView) *envelopes.Envelope {
	clone := cloneEnvelopeForDispatch(env)
	if clone.Data == nil {
		clone.Data = map[string]any{}
	}
	if view == nil {
		return clone
	}
	data := cloneAnyMapForDispatch(clone.Data)
	data["review_id"] = view.ReviewID
	data["files"] = cloneObjectSliceForDispatch(view.Files)
	data["current_file"] = view.CurrentFile
	data["filter_state"] = cloneAnyMapForDispatch(view.FilterState)
	data["decisions"] = diffReviewDecisionsAnyForDispatch(view.Decisions)
	data["comments"] = diffReviewCommentsAnyForDispatch(view.Comments)
	data["export_refs"] = diffReviewArtifactRefsAnyForDispatch(view.ExportRefs)
	if view.UpdatedAt != "" {
		data["updated_at"] = view.UpdatedAt
	}
	if view.Summary != nil {
		data["summary"] = diffReviewSummaryAnyForDispatch(view.Summary)
	}
	if view.BeforeRef != nil {
		data["before_ref"] = diffReviewArtifactRefAnyForDispatch(view.BeforeRef)
	}
	if view.AfterRef != nil {
		data["after_ref"] = diffReviewArtifactRefAnyForDispatch(view.AfterRef)
	}
	clone.Data = data
	return clone
}

func (s *Server) normalizeDiffReviewSubmitResponse(
	roomID string,
	_ *envelopes.Envelope,
	resp *envelopes.Response,
) (*envelopes.Response, error) {
	if resp == nil {
		return nil, diffReviewResponseValidationError("response is required")
	}
	if resp.Kind != envelopes.ResponseKindData {
		return nil, diffReviewResponseValidationError("kind must be %q", envelopes.ResponseKindData)
	}
	if resp.Status != envelopes.ResponseStatusSubmitted {
		return nil, diffReviewResponseValidationError("status must be %q", envelopes.ResponseStatusSubmitted)
	}

	phaseState, found, err := s.manager.GetPhaseState(context.Background(), roomID)
	if err != nil {
		return nil, fmt.Errorf("diff-review submit: load room state: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", room.ErrRoomNotFound, roomID)
	}
	persisted := room.ProjectDiffReviewState(phaseState)
	if persisted == nil {
		return nil, diffReviewResponseValidationError("room %q has no persisted diff-review state", roomID)
	}

	draft, err := decodeDiffReviewSubmitDraft(resp.Payload)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(draft.ReviewID) == "" {
		return nil, diffReviewResponseValidationError("payload.review_id is required")
	}
	if draft.ReviewID != persisted.ReviewID {
		return nil, diffReviewResponseValidationError("payload.review_id %q does not match room review_id %q", draft.ReviewID, persisted.ReviewID)
	}

	validTargets, fileIDs, err := roomTargetsFromView(persisted)
	if err != nil {
		return nil, err
	}
	decisions, err := normalizeDiffReviewDecisionsForSubmit(draft.Decisions, validTargets)
	if err != nil {
		return nil, err
	}
	comments, err := normalizeDiffReviewCommentsForSubmit(draft.Comments, validTargets)
	if err != nil {
		return nil, err
	}
	currentFile := strings.TrimSpace(draft.CurrentFile)
	if currentFile == "" {
		currentFile = persisted.CurrentFile
	}
	if currentFile != "" {
		if _, ok := fileIDs[currentFile]; !ok {
			return nil, diffReviewResponseValidationError("payload.current_file %q is unknown", currentFile)
		}
	}
	filterState := draft.FilterState
	if len(filterState) == 0 {
		filterState = persisted.FilterState
	}
	summary := draft.Summary
	if summary == nil {
		summary = buildDiffReviewSummary(persisted.Files, decisions, comments, draft.ExportRefs)
	}
	exportRefs := draft.ExportRefs
	if len(exportRefs) == 0 {
		exportRefs = persisted.ExportRefs
	}
	beforeRef := draft.BeforeRef
	if beforeRef == nil {
		beforeRef = persisted.BeforeRef
	}
	afterRef := draft.AfterRef
	if afterRef == nil {
		afterRef = persisted.AfterRef
	}

	snapshot := room.DiffReviewSnapshot{
		ReviewID:    persisted.ReviewID,
		Files:       persisted.Files,
		CurrentFile: currentFile,
		FilterState: filterState,
		Decisions:   decisions,
		Comments:    comments,
		UpdatedAt:   nowRFC3339(),
		Summary:     summary,
		BeforeRef:   beforeRef,
		AfterRef:    afterRef,
		ExportRefs:  exportRefs,
	}
	if _, err := s.manager.SaveDiffReviewSnapshot(roomID, snapshot); err != nil {
		return nil, err
	}

	normalized := &envelopes.Response{
		V:           envelopes.ProtocolVersion,
		EnvelopeID:  resp.EnvelopeID,
		Kind:        envelopes.ResponseKindData,
		Status:      envelopes.ResponseStatusSubmitted,
		CompletedAt: resp.CompletedAt,
		Payload: diffReviewSubmitPayload{
			ReviewID:    persisted.ReviewID,
			CurrentFile: currentFile,
			FilterState: filterState,
			Decisions:   decisions,
			Comments:    comments,
			Summary:     summary,
			BeforeRef:   beforeRef,
			AfterRef:    afterRef,
			ExportRefs:  exportRefs,
		},
	}
	if normalized.CompletedAt == "" {
		normalized.CompletedAt = nowRFC3339()
	}
	return normalized, nil
}

func decodeDiffReviewSubmitDraft(raw any) (diffReviewSubmitDraft, error) {
	if raw == nil {
		return diffReviewSubmitDraft{}, diffReviewResponseValidationError("payload is required")
	}
	record, ok := raw.(map[string]any)
	if !ok {
		return diffReviewSubmitDraft{}, diffReviewResponseValidationError("payload must be JSON-shaped")
	}
	var draft diffReviewSubmitDraft
	encoded, err := json.Marshal(record)
	if err != nil {
		return diffReviewSubmitDraft{}, diffReviewResponseValidationError("payload is invalid: %v", err)
	}
	if err := json.Unmarshal(encoded, &draft); err != nil {
		return diffReviewSubmitDraft{}, diffReviewResponseValidationError("payload is invalid: %v", err)
	}
	return draft, nil
}

func diffReviewResponseValidationError(format string, args ...any) error {
	return fmt.Errorf("diff-review submit response: %w: %s", envelopes.ErrSchemaValidation, fmt.Sprintf(format, args...))
}

func roomTargetsFromView(view *room.DiffReviewStateView) (map[string]struct{}, map[string]struct{}, error) {
	validTargets := map[string]struct{}{}
	fileIDs := map[string]struct{}{}
	for _, file := range view.Files {
		fileID := strings.TrimSpace(readStringValue(file, "id"))
		if fileID == "" {
			return nil, nil, diffReviewResponseValidationError("persisted files contain a blank file id")
		}
		fileIDs[fileID] = struct{}{}
		validTargets[fileID] = struct{}{}
		for _, hunk := range readObjectSliceValue(file["hunks"]) {
			hunkID := strings.TrimSpace(readStringValue(hunk, "id"))
			if hunkID == "" {
				continue
			}
			validTargets[fileID+"::"+hunkID] = struct{}{}
		}
	}
	return validTargets, fileIDs, nil
}

func normalizeDiffReviewDecisionsForSubmit(items []room.DiffReviewDecision, validTargets map[string]struct{}) ([]room.DiffReviewDecision, error) {
	if len(items) == 0 {
		return []room.DiffReviewDecision{}, nil
	}
	out := make([]room.DiffReviewDecision, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		fileID := strings.TrimSpace(item.FileID)
		if fileID == "" {
			return nil, diffReviewResponseValidationError("payload.decisions.file_id is required")
		}
		hunkID := strings.TrimSpace(item.HunkID)
		target := fileID
		if hunkID != "" {
			target += "::" + hunkID
		}
		if _, ok := validTargets[target]; !ok {
			return nil, diffReviewResponseValidationError("payload.decisions contains unknown target %q", target)
		}
		if _, exists := seen[target]; exists {
			return nil, diffReviewResponseValidationError("payload.decisions contains duplicate target %q", target)
		}
		seen[target] = struct{}{}
		decision := normalizeDiffReviewDecisionValueForSubmit(item.Decision)
		if decision == "" {
			return nil, diffReviewResponseValidationError("payload.decisions[%q].decision is invalid", target)
		}
		out = append(out, room.DiffReviewDecision{
			FileID:    fileID,
			HunkID:    hunkID,
			Decision:  decision,
			Comment:   strings.TrimSpace(item.Comment),
			ActionID:  strings.TrimSpace(item.ActionID),
			DecidedAt: strings.TrimSpace(item.DecidedAt),
		})
	}
	now := nowRFC3339()
	for i := range out {
		if out[i].DecidedAt == "" {
			out[i].DecidedAt = now
		}
	}
	return out, nil
}

func normalizeDiffReviewCommentsForSubmit(items map[string]string, validTargets map[string]struct{}) (map[string]string, error) {
	if len(items) == 0 {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(items))
	for key, value := range items {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := validTargets[key]; !ok {
			return nil, diffReviewResponseValidationError("payload.comments contains unknown target %q", key)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out[key] = value
	}
	return out, nil
}

func normalizeDiffReviewDecisionValueForSubmit(value string) string {
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

func buildDiffReviewSummary(files []map[string]any, decisions []room.DiffReviewDecision, comments map[string]string, exportRefs []room.DiffReviewArtifactRef) *room.DiffReviewSummary {
	fileDecisions := map[string]struct{}{}
	decisionSummary := map[string]int{}
	targets := map[string]struct{}{}
	decisionTargets := map[string]struct{}{}
	for _, file := range files {
		fileID := strings.TrimSpace(readStringValue(file, "id"))
		if fileID == "" {
			continue
		}
		hunks := readObjectSliceValue(file["hunks"])
		if len(hunks) == 0 {
			targets[fileID] = struct{}{}
			continue
		}
		for _, hunk := range hunks {
			hunkID := strings.TrimSpace(readStringValue(hunk, "id"))
			if hunkID == "" {
				continue
			}
			targets[fileID+"::"+hunkID] = struct{}{}
		}
	}
	for _, decision := range decisions {
		fileDecisions[decision.FileID] = struct{}{}
		decisionSummary[decision.Decision]++
		target := decision.FileID
		if decision.HunkID != "" {
			target += "::" + decision.HunkID
		}
		decisionTargets[target] = struct{}{}
	}
	pendingCount := 0
	for target := range targets {
		if _, ok := decisionTargets[target]; !ok {
			pendingCount++
		}
	}
	summary := &room.DiffReviewSummary{
		SubmittedAt:       nowRFC3339(),
		DecisionFileCount: len(fileDecisions),
		DecisionHunkCount: len(decisions),
		CommentCount:      len(comments),
		PendingCount:      pendingCount,
		DecisionSummary:   decisionSummary,
	}
	if len(exportRefs) > 0 {
		name := strings.TrimSpace(exportRefs[len(exportRefs)-1].Name)
		summary.ExportName = name
	}
	return summary
}

func readDiffReviewArtifactRefValue(raw any) *room.DiffReviewArtifactRef {
	record, _ := raw.(map[string]any)
	if len(record) == 0 {
		return nil
	}
	ref := &room.DiffReviewArtifactRef{
		ArtifactID: readStringValue(record, "artifact_id"),
		Name:       readStringValue(record, "name"),
		URI:        readStringValue(record, "uri"),
		MIMEType:   readStringValue(record, "mime_type"),
		Kind:       readStringValue(record, "kind"),
	}
	if size, ok := record["size_bytes"].(float64); ok {
		ref.SizeBytes = int(size)
	}
	if ref.ArtifactID == "" && ref.Name == "" && ref.URI == "" {
		return nil
	}
	return ref
}

func cloneDiffReviewArtifactRef(ref *room.DiffReviewArtifactRef) *room.DiffReviewArtifactRef {
	if ref == nil {
		return nil
	}
	refCopy := *ref
	return &refCopy
}

func diffReviewDecisionsAnyForDispatch(items []room.DiffReviewDecision) []map[string]any {
	return roomDiffReviewDecisionsAny(items)
}

func diffReviewCommentsAnyForDispatch(items map[string]string) map[string]any {
	out := make(map[string]any, len(items))
	for key, value := range items {
		out[key] = value
	}
	return out
}

func diffReviewArtifactRefAnyForDispatch(ref *room.DiffReviewArtifactRef) map[string]any {
	if ref == nil {
		return map[string]any{}
	}
	record := map[string]any{}
	if ref.ArtifactID != "" {
		record["artifact_id"] = ref.ArtifactID
	}
	if ref.Name != "" {
		record["name"] = ref.Name
	}
	if ref.URI != "" {
		record["uri"] = ref.URI
	}
	if ref.MIMEType != "" {
		record["mime_type"] = ref.MIMEType
	}
	if ref.Kind != "" {
		record["kind"] = ref.Kind
	}
	if ref.SizeBytes > 0 {
		record["size_bytes"] = ref.SizeBytes
	}
	return record
}

func diffReviewArtifactRefsAnyForDispatch(items []room.DiffReviewArtifactRef) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, diffReviewArtifactRefAnyForDispatch(&item))
	}
	return out
}

func diffReviewSummaryAnyForDispatch(summary *room.DiffReviewSummary) map[string]any {
	if summary == nil {
		return map[string]any{}
	}
	record := map[string]any{}
	if summary.SubmittedAt != "" {
		record["submitted_at"] = summary.SubmittedAt
	}
	if summary.DecisionFileCount > 0 {
		record["decision_file_count"] = summary.DecisionFileCount
	}
	if summary.DecisionHunkCount > 0 {
		record["decision_hunk_count"] = summary.DecisionHunkCount
	}
	if summary.CommentCount > 0 {
		record["comment_count"] = summary.CommentCount
	}
	if summary.PendingCount > 0 {
		record["pending_count"] = summary.PendingCount
	}
	if summary.ExportText != "" {
		record["export_text"] = summary.ExportText
	}
	if summary.ExportName != "" {
		record["export_name"] = summary.ExportName
	}
	if len(summary.DecisionSummary) > 0 {
		decisionSummary := make(map[string]any, len(summary.DecisionSummary))
		for key, value := range summary.DecisionSummary {
			decisionSummary[key] = value
		}
		record["decision_summary"] = decisionSummary
	}
	return record
}

func roomDiffReviewDecisionsAny(items []room.DiffReviewDecision) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			"file_id":  item.FileID,
			"decision": item.Decision,
		}
		if item.HunkID != "" {
			record["hunk_id"] = item.HunkID
		}
		if item.Comment != "" {
			record["comment"] = item.Comment
		}
		if item.ActionID != "" {
			record["action_id"] = item.ActionID
		}
		if item.DecidedAt != "" {
			record["decided_at"] = item.DecidedAt
		}
		out = append(out, record)
	}
	return out
}
