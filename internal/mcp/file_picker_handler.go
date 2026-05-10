package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/room"
)

type filePickerInput struct {
	Envelope envelopes.Envelope `json:"envelope"`
}

type filePickerSubmitDraft struct {
	PickerID     string                       `json:"picker_id"`
	SelectedRefs []room.FilePickerArtifactRef `json:"selected_refs"`
	QueryState   map[string]any               `json:"query_state"`
}

type filePickerSubmitPayload struct {
	PickerID     string                       `json:"picker_id"`
	SelectedRefs []room.FilePickerArtifactRef `json:"selected_refs"`
	QueryState   map[string]any               `json:"query_state"`
}

func (s *Server) handleFilePicker(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args filePickerInput,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Envelope.Type != filePickerEnvelopeType {
		return toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf("tangent.file-picker rejects envelope type %q; want %q", args.Envelope.Type, filePickerEnvelopeType),
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
			return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("decode session_create result: %v", err)), nil, nil
		}
		roomID = created.RoomID
		s.logWorkflowRoomCreated("file-picker", roomID, args.Envelope.ID)
	} else {
		s.logWorkflowRoomReused("file-picker", roomID, args.Envelope.ID)
	}

	phaseState, found, err := s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("load room phase state: %v", err)), nil, nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	snapshot := filePickerSnapshotFromEnvelope(args.Envelope, room.ProjectFilePickerState(phaseState))
	if _, saveErr := s.manager.SaveFilePickerSnapshot(roomID, snapshot); saveErr != nil {
		return sessionPhaseStateError(roomID, saveErr), nil, nil
	}

	phaseState, found, err = s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("reload room file-picker state: %v", err)), nil, nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	return s.advanceRoomEnvelope(
		ctx,
		roomID,
		buildVisibleFilePickerEnvelope(&args.Envelope, room.ProjectFilePickerState(phaseState)),
	)
}

func filePickerSnapshotFromEnvelope(env envelopes.Envelope, persisted *room.FilePickerStateView) room.FilePickerSnapshot {
	data := env.Data
	pickerID := readStringValue(data, "picker_id")
	if pickerID == "" && persisted != nil {
		pickerID = persisted.PickerID
	}
	reusePersisted := persisted != nil && persisted.PickerID != "" && persisted.PickerID == pickerID

	browseRoots := readFilePickerBrowseRootsValue(data["browse_roots"])
	if reusePersisted {
		browseRoots = persisted.BrowseRoots
	} else if len(browseRoots) == 0 && persisted != nil {
		browseRoots = persisted.BrowseRoots
	}
	selectedRefs := readFilePickerArtifactRefsValue(data["selected_refs"])
	if reusePersisted {
		selectedRefs = persisted.SelectedRefs
	} else if len(selectedRefs) == 0 && persisted != nil {
		selectedRefs = persisted.SelectedRefs
	}
	queryState := readObjectValue(data, "query_state")
	if reusePersisted && len(persisted.QueryState) > 0 {
		queryState = persisted.QueryState
	}

	snapshot := room.FilePickerSnapshot{
		PickerID:     pickerID,
		BrowseRoots:  browseRoots,
		SelectedRefs: selectedRefs,
		QueryState:   queryState,
		UpdatedAt:    nowRFC3339(),
	}
	if reusePersisted {
		snapshot.SelectionRevisions = persisted.SelectionRevisions
	}
	return snapshot
}

func buildVisibleFilePickerEnvelope(env *envelopes.Envelope, view *room.FilePickerStateView) *envelopes.Envelope {
	clone := cloneEnvelopeForDispatch(env)
	if clone.Data == nil {
		clone.Data = map[string]any{}
	}
	if view == nil {
		return clone
	}
	data := cloneAnyMapForDispatch(clone.Data)
	data["picker_id"] = view.PickerID
	data["browse_roots"] = filePickerBrowseRootsAnyForDispatch(view.BrowseRoots)
	data["selected_refs"] = filePickerArtifactRefsAnyForDispatch(view.SelectedRefs)
	data["query_state"] = cloneAnyMapForDispatch(view.QueryState)
	data["selection_revisions"] = filePickerSelectionRevisionsAnyForDispatch(view.SelectionRevisions)
	if view.UpdatedAt != "" {
		data["updated_at"] = view.UpdatedAt
	}
	clone.Data = data
	return clone
}

func (s *Server) normalizeFilePickerSubmitResponse(
	roomID string,
	_ *envelopes.Envelope,
	resp *envelopes.Response,
) (*envelopes.Response, error) {
	if resp == nil {
		return nil, filePickerResponseValidationError("response is required")
	}
	if resp.Kind != envelopes.ResponseKindData {
		return nil, filePickerResponseValidationError("kind must be %q", envelopes.ResponseKindData)
	}
	if resp.Status != envelopes.ResponseStatusSubmitted {
		return nil, filePickerResponseValidationError("status must be %q", envelopes.ResponseStatusSubmitted)
	}

	phaseState, found, err := s.manager.GetPhaseState(context.Background(), roomID)
	if err != nil {
		return nil, fmt.Errorf("file-picker submit: load room state: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", room.ErrRoomNotFound, roomID)
	}
	persisted := room.ProjectFilePickerState(phaseState)
	if persisted == nil {
		return nil, filePickerResponseValidationError("room %q has no persisted file-picker state", roomID)
	}

	draft, err := decodeFilePickerSubmitDraft(resp.Payload)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(draft.PickerID) == "" {
		return nil, filePickerResponseValidationError("payload.picker_id is required")
	}
	if draft.PickerID != persisted.PickerID {
		return nil, filePickerResponseValidationError("payload.picker_id %q does not match room picker_id %q", draft.PickerID, persisted.PickerID)
	}

	snapshot := room.FilePickerSnapshot{
		PickerID:     persisted.PickerID,
		BrowseRoots:  persisted.BrowseRoots,
		SelectedRefs: draft.SelectedRefs,
		QueryState:   draft.QueryState,
		UpdatedAt:    nowRFC3339(),
		SelectionRevisions: append(
			append([]room.FilePickerSelectionRevision{}, persisted.SelectionRevisions...),
			room.FilePickerSelectionRevision{
				SubmittedAt:   nowRFC3339(),
				SelectedCount: len(draft.SelectedRefs),
			},
		),
	}
	if snapshot.QueryState == nil {
		snapshot.QueryState = persisted.QueryState
	}
	if _, err := s.manager.SaveFilePickerSnapshot(roomID, snapshot); err != nil {
		return nil, err
	}

	normalized := &envelopes.Response{
		V:           envelopes.ProtocolVersion,
		EnvelopeID:  resp.EnvelopeID,
		Kind:        envelopes.ResponseKindData,
		Status:      envelopes.ResponseStatusSubmitted,
		CompletedAt: resp.CompletedAt,
		Payload: filePickerSubmitPayload{
			PickerID:     persisted.PickerID,
			SelectedRefs: snapshot.SelectedRefs,
			QueryState:   snapshot.QueryState,
		},
	}
	if normalized.CompletedAt == "" {
		normalized.CompletedAt = nowRFC3339()
	}
	return normalized, nil
}

func decodeFilePickerSubmitDraft(raw any) (filePickerSubmitDraft, error) {
	body, err := json.Marshal(raw)
	if err != nil {
		return filePickerSubmitDraft{}, filePickerResponseValidationError("payload must be a JSON object")
	}
	var record map[string]any
	if err := json.Unmarshal(body, &record); err != nil || record == nil {
		return filePickerSubmitDraft{}, filePickerResponseValidationError("payload must be a JSON object")
	}
	draft := filePickerSubmitDraft{
		PickerID:     readStringValue(record, "picker_id"),
		SelectedRefs: readFilePickerArtifactRefsValue(record["selected_refs"]),
		QueryState:   readObjectValue(record, "query_state"),
	}
	return draft, nil
}

func readFilePickerBrowseRootsValue(raw any) []room.FilePickerBrowseRoot {
	items, _ := raw.([]any)
	out := make([]room.FilePickerBrowseRoot, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, room.FilePickerBrowseRoot{
			RootID: readStringValue(record, "root_id"),
			Label:  readStringValue(record, "label"),
			Path:   readStringValue(record, "path"),
			Kind:   readStringValue(record, "kind"),
		})
	}
	return out
}

func readFilePickerArtifactRefsValue(raw any) []room.FilePickerArtifactRef {
	items, _ := raw.([]any)
	out := make([]room.FilePickerArtifactRef, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, room.FilePickerArtifactRef{
			ArtifactID:   readStringValue(record, "artifact_id"),
			Name:         readStringValue(record, "name"),
			URI:          readStringValue(record, "uri"),
			MIMEType:     readStringValue(record, "mime_type"),
			Kind:         readStringValue(record, "kind"),
			SizeBytes:    readIntValue(record["size_bytes"]),
			RootID:       readStringValue(record, "root_id"),
			RelativePath: readStringValue(record, "relative_path"),
		})
	}
	return out
}

func filePickerBrowseRootsAnyForDispatch(items []room.FilePickerBrowseRoot) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			"root_id": item.RootID,
			"label":   item.Label,
			"path":    item.Path,
			"kind":    item.Kind,
		}
		out = append(out, record)
	}
	return out
}

func filePickerArtifactRefsAnyForDispatch(items []room.FilePickerArtifactRef) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			"artifact_id":   item.ArtifactID,
			"uri":           item.URI,
			"root_id":       item.RootID,
			"relative_path": item.RelativePath,
			"kind":          item.Kind,
		}
		if item.Name != "" {
			record["name"] = item.Name
		}
		if item.MIMEType != "" {
			record["mime_type"] = item.MIMEType
		}
		if item.SizeBytes > 0 {
			record["size_bytes"] = item.SizeBytes
		}
		out = append(out, record)
	}
	return out
}

func filePickerSelectionRevisionsAnyForDispatch(items []room.FilePickerSelectionRevision) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			"submitted_at":   item.SubmittedAt,
			"selected_count": item.SelectedCount,
		}
		if item.SelectionRevisionID != "" {
			record["selection_revision_id"] = item.SelectionRevisionID
		}
		out = append(out, record)
	}
	return out
}

func filePickerResponseValidationError(format string, args ...any) error {
	return fmt.Errorf("file-picker submit response: %w: %s", envelopes.ErrSchemaValidation, fmt.Sprintf(format, args...))
}
