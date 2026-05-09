package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/room"
)

const maxSpreadsheetSelectedRowSummaries = 20

type spreadsheetReviewInput struct {
	Envelope envelopes.Envelope `json:"envelope"`
}

type spreadsheetReviewSubmitDraft struct {
	TableID        string                            `json:"table_id"`
	SelectedRowIDs []string                          `json:"selected_row_ids,omitempty"`
	SelectedRows   []map[string]any                  `json:"selected_rows,omitempty"`
	QueryState     map[string]any                    `json:"query_state,omitempty"`
	Notes          string                            `json:"notes,omitempty"`
	ActionID       string                            `json:"action_id,omitempty"`
	SavedViews     []room.SpreadsheetReviewSavedView `json:"saved_views,omitempty"`
	ExportRefs     []room.SpreadsheetReviewExportRef `json:"export_refs,omitempty"`
}

type spreadsheetReviewSubmitPayload struct {
	TableID        string           `json:"table_id"`
	SelectedRowIDs []string         `json:"selected_row_ids"`
	SelectedRows   []map[string]any `json:"selected_rows"`
	QueryState     map[string]any   `json:"query_state"`
	Notes          string           `json:"notes,omitempty"`
	ActionID       string           `json:"action_id,omitempty"`
}

func (s *Server) handleSpreadsheetReview(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args spreadsheetReviewInput,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Envelope.Type != spreadsheetReviewEnvelopeType {
		return toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf(
				"tangent.spreadsheet-review rejects envelope type %q; want %q",
				args.Envelope.Type,
				spreadsheetReviewEnvelopeType,
			),
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
			return toolErrorResult(
				envelopes.ErrorCodeHostError,
				fmt.Sprintf("decode session_create result: %v", err),
			), nil, nil
		}
		roomID = created.RoomID
		s.logWorkflowRoomCreated("spreadsheet-review", roomID, args.Envelope.ID)
	} else {
		s.logWorkflowRoomReused("spreadsheet-review", roomID, args.Envelope.ID)
	}

	phaseState, found, err := s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("load room phase state: %v", err)), nil, nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	snapshot := spreadsheetReviewSnapshotFromEnvelope(args.Envelope, room.ProjectSpreadsheetReviewState(phaseState))
	if _, saveErr := s.manager.SaveSpreadsheetReviewSnapshot(roomID, snapshot); saveErr != nil {
		return sessionPhaseStateError(roomID, saveErr), nil, nil
	}

	phaseState, found, err = s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("reload room spreadsheet state: %v", err)), nil, nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	return s.advanceRoomEnvelope(
		ctx,
		roomID,
		buildVisibleSpreadsheetReviewEnvelope(&args.Envelope, room.ProjectSpreadsheetReviewState(phaseState)),
	)
}

func spreadsheetReviewSnapshotFromEnvelope(env envelopes.Envelope, persisted *room.SpreadsheetReviewStateView) room.SpreadsheetReviewSnapshot {
	data := env.Data
	tableID := readStringValue(data, "table_id")
	if tableID == "" && persisted != nil {
		tableID = persisted.TableID
	}
	reusePersisted := persisted != nil && persisted.TableID != "" && persisted.TableID == tableID

	columns := readObjectSliceValue(data["columns"])
	if len(columns) == 0 && persisted != nil {
		columns = persisted.Columns
	}
	rows := readObjectSliceValue(data["rows"])
	if len(rows) == 0 && persisted != nil {
		rows = persisted.Rows
	}
	queryState := readObjectValue(data, "query_state")
	if reusePersisted && len(persisted.QueryState) > 0 {
		queryState = persisted.QueryState
	}
	notes := readStringValue(data, "notes")
	if reusePersisted {
		notes = persisted.Notes
	}
	rowActions := readSpreadsheetReviewRowActionsValue(data["row_actions"])
	if len(rowActions) == 0 && persisted != nil {
		rowActions = persisted.RowActions
	}

	snapshot := room.SpreadsheetReviewSnapshot{
		TableID:    tableID,
		Columns:    columns,
		Rows:       rows,
		QueryState: queryState,
		Notes:      notes,
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
		RowActions: rowActions,
	}
	if reusePersisted {
		snapshot.SavedViews = persisted.SavedViews
		snapshot.SelectedRowIDs = persisted.SelectedRowIDs
		snapshot.SelectedRows = persisted.SelectedRows
		snapshot.ActionID = persisted.ActionID
		snapshot.ExportRefs = persisted.ExportRefs
	}
	return snapshot
}

func buildVisibleSpreadsheetReviewEnvelope(
	env *envelopes.Envelope,
	view *room.SpreadsheetReviewStateView,
) *envelopes.Envelope {
	clone := cloneEnvelopeForDispatch(env)
	if clone.Data == nil {
		clone.Data = map[string]any{}
	}
	if view == nil {
		return clone
	}

	data := cloneAnyMapForDispatch(clone.Data)
	data["table_id"] = view.TableID
	data["columns"] = cloneObjectSliceForDispatch(view.Columns)
	data["rows"] = cloneObjectSliceForDispatch(view.Rows)
	data["query_state"] = cloneAnyMapForDispatch(view.QueryState)
	data["saved_views"] = spreadsheetSavedViewsAny(view.SavedViews)
	data["row_actions"] = spreadsheetRowActionsAny(view.RowActions)
	data["selected_row_ids"] = cloneStringSliceForDispatch(view.SelectedRowIDs)
	data["selected_rows"] = cloneObjectSliceForDispatch(view.SelectedRows)
	data["export_refs"] = spreadsheetExportRefsAny(view.ExportRefs)
	if view.Notes != "" {
		data["notes"] = view.Notes
	}
	if view.UpdatedAt != "" {
		data["updated_at"] = view.UpdatedAt
	}
	if view.ActionID != "" {
		data["action_id"] = view.ActionID
	}
	clone.Data = data
	return clone
}

func (s *Server) normalizeSpreadsheetReviewSubmitResponse(
	roomID string,
	_ *envelopes.Envelope,
	resp *envelopes.Response,
) (*envelopes.Response, error) {
	if resp == nil {
		return nil, spreadsheetReviewResponseValidationError("response is required")
	}
	if resp.Kind != envelopes.ResponseKindData {
		return nil, spreadsheetReviewResponseValidationError("kind must be %q", envelopes.ResponseKindData)
	}
	if resp.Status != envelopes.ResponseStatusSubmitted {
		return nil, spreadsheetReviewResponseValidationError("status must be %q", envelopes.ResponseStatusSubmitted)
	}

	phaseState, found, err := s.manager.GetPhaseState(context.Background(), roomID)
	if err != nil {
		return nil, fmt.Errorf("spreadsheet-review submit: load room state: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", room.ErrRoomNotFound, roomID)
	}
	persisted := room.ProjectSpreadsheetReviewState(phaseState)
	if persisted == nil {
		return nil, spreadsheetReviewResponseValidationError("room %q has no persisted spreadsheet state", roomID)
	}

	draft, err := decodeSpreadsheetReviewSubmitDraft(resp.Payload)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(draft.TableID) == "" {
		return nil, spreadsheetReviewResponseValidationError("payload.table_id is required")
	}
	if draft.TableID != persisted.TableID {
		return nil, spreadsheetReviewResponseValidationError(
			"payload.table_id %q does not match room table_id %q",
			draft.TableID,
			persisted.TableID,
		)
	}

	selectedRowIDs := normalizeSpreadsheetSelectedRowIDs(draft.SelectedRowIDs)
	selectedRows := normalizeSpreadsheetSelectedRowsForSubmit(persisted.Rows, draft.SelectedRows, selectedRowIDs)
	savedViews := draft.SavedViews
	if len(savedViews) == 0 {
		savedViews = persisted.SavedViews
	}
	exportRefs := draft.ExportRefs
	if len(exportRefs) == 0 {
		exportRefs = persisted.ExportRefs
	}

	snapshot := room.SpreadsheetReviewSnapshot{
		TableID:        persisted.TableID,
		Columns:        persisted.Columns,
		Rows:           persisted.Rows,
		QueryState:     draft.QueryState,
		Notes:          draft.Notes,
		UpdatedAt:      nowRFC3339(),
		SavedViews:     savedViews,
		RowActions:     persisted.RowActions,
		SelectedRowIDs: selectedRowIDs,
		SelectedRows:   selectedRows,
		ActionID:       draft.ActionID,
		ExportRefs:     exportRefs,
	}
	if len(snapshot.QueryState) == 0 {
		snapshot.QueryState = persisted.QueryState
	}
	if snapshot.Notes == "" && persisted.Notes == "" {
		snapshot.Notes = ""
	}
	if _, err := s.manager.SaveSpreadsheetReviewSnapshot(roomID, snapshot); err != nil {
		return nil, err
	}

	normalized := &envelopes.Response{
		V:           envelopes.ProtocolVersion,
		EnvelopeID:  resp.EnvelopeID,
		Kind:        envelopes.ResponseKindData,
		Status:      envelopes.ResponseStatusSubmitted,
		CompletedAt: resp.CompletedAt,
		Payload: spreadsheetReviewSubmitPayload{
			TableID:        persisted.TableID,
			SelectedRowIDs: selectedRowIDs,
			SelectedRows:   selectedRows,
			QueryState:     snapshot.QueryState,
			Notes:          snapshot.Notes,
			ActionID:       snapshot.ActionID,
		},
	}
	if normalized.CompletedAt == "" {
		normalized.CompletedAt = nowRFC3339()
	}
	return normalized, nil
}

func decodeSpreadsheetReviewSubmitDraft(raw any) (spreadsheetReviewSubmitDraft, error) {
	if raw == nil {
		return spreadsheetReviewSubmitDraft{}, spreadsheetReviewResponseValidationError("payload is required")
	}
	record, ok := raw.(map[string]any)
	if !ok {
		return spreadsheetReviewSubmitDraft{}, spreadsheetReviewResponseValidationError("payload must be JSON-shaped")
	}
	var draft spreadsheetReviewSubmitDraft
	encoded, err := json.Marshal(record)
	if err != nil {
		return spreadsheetReviewSubmitDraft{}, spreadsheetReviewResponseValidationError("payload is invalid: %v", err)
	}
	if err := json.Unmarshal(encoded, &draft); err != nil {
		return spreadsheetReviewSubmitDraft{}, spreadsheetReviewResponseValidationError("payload is invalid: %v", err)
	}
	return draft, nil
}

func spreadsheetReviewResponseValidationError(format string, args ...any) error {
	return fmt.Errorf(
		"spreadsheet-review submit response: %w: %s",
		envelopes.ErrSchemaValidation,
		fmt.Sprintf(format, args...),
	)
}

func normalizeSpreadsheetSelectedRowIDs(ids []string) []string {
	if len(ids) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func normalizeSpreadsheetSelectedRowsForSubmit(
	allRows []map[string]any,
	draftRows []map[string]any,
	selectedRowIDs []string,
) []map[string]any {
	if len(draftRows) > 0 {
		trimmed := make([]map[string]any, 0, min(len(draftRows), maxSpreadsheetSelectedRowSummaries))
		for _, row := range draftRows[:min(len(draftRows), maxSpreadsheetSelectedRowSummaries)] {
			trimmed = append(trimmed, compactSpreadsheetRow(row))
		}
		return trimmed
	}
	if len(selectedRowIDs) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, min(len(selectedRowIDs), maxSpreadsheetSelectedRowSummaries))
	for _, id := range selectedRowIDs {
		idx := slices.IndexFunc(allRows, func(row map[string]any) bool {
			return readStringValue(row, "id") == id
		})
		if idx < 0 {
			continue
		}
		out = append(out, compactSpreadsheetRow(allRows[idx]))
		if len(out) >= maxSpreadsheetSelectedRowSummaries {
			break
		}
	}
	return out
}

func compactSpreadsheetRow(row map[string]any) map[string]any {
	if len(row) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(row))
	for key, value := range row {
		switch typed := value.(type) {
		case string, float64, float32, int, int32, int64, bool:
			out[key] = typed
		default:
			if value == nil {
				out[key] = nil
			}
		}
	}
	return out
}

func readObjectSliceValue(raw any) []map[string]any {
	items, _ := raw.([]any)
	if len(items) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, cloneAnyMapForDispatch(record))
	}
	return out
}

func readSpreadsheetReviewRowActionsValue(raw any) []room.SpreadsheetReviewRowAction {
	items, _ := raw.([]any)
	if len(items) == 0 {
		return []room.SpreadsheetReviewRowAction{}
	}
	out := make([]room.SpreadsheetReviewRowAction, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := strings.TrimSpace(readStringValue(record, "id"))
		if id == "" {
			continue
		}
		label := strings.TrimSpace(readStringValue(record, "label"))
		if label == "" {
			label = id
		}
		out = append(out, room.SpreadsheetReviewRowAction{
			ID:          id,
			Label:       label,
			Description: strings.TrimSpace(readStringValue(record, "description")),
		})
	}
	return out
}

func spreadsheetSavedViewsAny(views []room.SpreadsheetReviewSavedView) []any {
	out := make([]any, 0, len(views))
	for _, view := range views {
		out = append(out, map[string]any{
			"name":        view.Name,
			"query_state": cloneAnyMapForDispatch(view.QueryState),
		})
	}
	return out
}

func spreadsheetRowActionsAny(actions []room.SpreadsheetReviewRowAction) []any {
	out := make([]any, 0, len(actions))
	for _, action := range actions {
		record := map[string]any{
			"id":    action.ID,
			"label": action.Label,
		}
		if action.Description != "" {
			record["description"] = action.Description
		}
		out = append(out, record)
	}
	return out
}

func spreadsheetExportRefsAny(refs []room.SpreadsheetReviewExportRef) []any {
	out := make([]any, 0, len(refs))
	for _, ref := range refs {
		record := map[string]any{
			"name": ref.Name,
		}
		if ref.MIMEType != "" {
			record["mime_type"] = ref.MIMEType
		}
		if ref.Kind != "" {
			record["kind"] = ref.Kind
		}
		if ref.CreatedAt != "" {
			record["created_at"] = ref.CreatedAt
		}
		if ref.RowCount > 0 {
			record["row_count"] = ref.RowCount
		}
		if ref.ColumnCount > 0 {
			record["column_count"] = ref.ColumnCount
		}
		if ref.SizeBytes > 0 {
			record["size_bytes"] = ref.SizeBytes
		}
		out = append(out, record)
	}
	return out
}

func cloneObjectSliceForDispatch(rows []map[string]any) []any {
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, cloneAnyMapForDispatch(row))
	}
	return out
}

func cloneStringSliceForDispatch(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}
