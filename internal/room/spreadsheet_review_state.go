package room

import (
	"fmt"
	"strings"
)

const (
	SpreadsheetReviewPhaseID            = "spreadsheet-review"
	spreadsheetReviewTableIDKey         = "table_id"
	spreadsheetReviewColumnsKey         = "columns"
	spreadsheetReviewRowsKey            = "rows"
	spreadsheetReviewQueryStateKey      = "query_state"
	spreadsheetReviewNotesKey           = "notes"
	spreadsheetReviewUpdatedAtKey       = "updated_at"
	spreadsheetReviewSavedViewsKey      = "saved_views"
	spreadsheetReviewSavedViewNameKey   = "name"
	spreadsheetReviewVisibleColumnsKey  = "visible_columns"
	spreadsheetReviewSortKey            = "sort"
	spreadsheetReviewFiltersKey         = "filters"
	spreadsheetReviewSearchKey          = "search"
	spreadsheetReviewPageKey            = "page"
	spreadsheetReviewRowActionsKey      = "row_actions"
	spreadsheetReviewActionIDKey        = "action_id"
	spreadsheetReviewSelectedRowIDsKey  = "selected_row_ids"
	spreadsheetReviewSelectedRowsKey    = "selected_rows"
	spreadsheetReviewExportRefsKey      = "export_refs"
	spreadsheetReviewRowActionLabelKey  = "label"
	spreadsheetReviewRowActionIDKey     = "id"
	spreadsheetReviewRowActionDescKey   = "description"
	spreadsheetReviewExportNameKey      = "name"
	spreadsheetReviewExportMIMETypeKey  = "mime_type"
	spreadsheetReviewExportKindKey      = "kind"
	spreadsheetReviewExportCreatedAtKey = "created_at"
	spreadsheetReviewExportRowCountKey  = "row_count"
	spreadsheetReviewExportColCountKey  = "column_count"
	spreadsheetReviewExportSizeBytesKey = "size_bytes"
)

type SpreadsheetReviewSavedView struct {
	Name       string         `json:"name"`
	QueryState map[string]any `json:"query_state"`
}

type SpreadsheetReviewRowAction struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type SpreadsheetReviewExportRef struct {
	Name        string `json:"name"`
	MIMEType    string `json:"mime_type,omitempty"`
	Kind        string `json:"kind,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	RowCount    int    `json:"row_count,omitempty"`
	ColumnCount int    `json:"column_count,omitempty"`
	SizeBytes   int    `json:"size_bytes,omitempty"`
}

type SpreadsheetReviewStateView struct {
	TableID        string                       `json:"table_id"`
	Columns        []map[string]any             `json:"columns"`
	Rows           []map[string]any             `json:"rows"`
	QueryState     map[string]any               `json:"query_state"`
	Notes          string                       `json:"notes,omitempty"`
	UpdatedAt      string                       `json:"updated_at,omitempty"`
	SavedViews     []SpreadsheetReviewSavedView `json:"saved_views"`
	RowActions     []SpreadsheetReviewRowAction `json:"row_actions"`
	SelectedRowIDs []string                     `json:"selected_row_ids"`
	SelectedRows   []map[string]any             `json:"selected_rows"`
	ActionID       string                       `json:"action_id,omitempty"`
	ExportRefs     []SpreadsheetReviewExportRef `json:"export_refs"`
}

type SpreadsheetReviewSnapshot struct {
	TableID        string
	Columns        []map[string]any
	Rows           []map[string]any
	QueryState     map[string]any
	Notes          string
	UpdatedAt      string
	SavedViews     []SpreadsheetReviewSavedView
	RowActions     []SpreadsheetReviewRowAction
	SelectedRowIDs []string
	SelectedRows   []map[string]any
	ActionID       string
	ExportRefs     []SpreadsheetReviewExportRef
}

func (r *Room) SaveSpreadsheetReviewSnapshot(snapshot SpreadsheetReviewSnapshot) error {
	normalized, err := normalizeSpreadsheetReviewSnapshot(snapshot)
	if err != nil {
		return err
	}

	r.phaseMu.Lock()
	defer r.phaseMu.Unlock()

	nextOutputs := clonePhaseOutputs(r.phaseOutputs)
	blob, err := spreadsheetReviewBlobFromSnapshot(nextOutputs[SpreadsheetReviewPhaseID], normalized)
	if err != nil {
		return err
	}
	nextOutputs[SpreadsheetReviewPhaseID] = blob

	nextVisited := cloneStringSlice(r.phasesVisited)
	if err := r.persistPhaseState(r.currentPhase, nextVisited, nextOutputs); err != nil {
		return err
	}

	r.phasesVisited = nextVisited
	r.phaseOutputs = nextOutputs
	return nil
}

func (r *Room) SaveSpreadsheetReviewSavedViews(tableID string, savedViews []SpreadsheetReviewSavedView) error {
	r.phaseMu.Lock()
	defer r.phaseMu.Unlock()

	current := projectSpreadsheetReviewStateFromBlob(r.phaseOutputs[SpreadsheetReviewPhaseID])
	if current == nil {
		return ErrInvalidSpreadsheetTableID
	}
	if strings.TrimSpace(tableID) != current.TableID {
		return fmt.Errorf("%w: %q", ErrInvalidSpreadsheetTableID, tableID)
	}
	normalizedSavedViews, err := normalizeSpreadsheetSavedViews(savedViews)
	if err != nil {
		return err
	}

	nextOutputs := clonePhaseOutputs(r.phaseOutputs)
	blob, err := spreadsheetReviewBlobFromSnapshot(nextOutputs[SpreadsheetReviewPhaseID], SpreadsheetReviewSnapshot{
		TableID:        current.TableID,
		Columns:        current.Columns,
		Rows:           current.Rows,
		QueryState:     current.QueryState,
		Notes:          current.Notes,
		UpdatedAt:      current.UpdatedAt,
		SavedViews:     normalizedSavedViews,
		RowActions:     current.RowActions,
		SelectedRowIDs: current.SelectedRowIDs,
		SelectedRows:   current.SelectedRows,
		ActionID:       current.ActionID,
		ExportRefs:     current.ExportRefs,
	})
	if err != nil {
		return err
	}
	nextOutputs[SpreadsheetReviewPhaseID] = blob

	nextVisited := cloneStringSlice(r.phasesVisited)
	if err := r.persistPhaseState(r.currentPhase, nextVisited, nextOutputs); err != nil {
		return err
	}

	r.phasesVisited = nextVisited
	r.phaseOutputs = nextOutputs
	return nil
}

func (m *Manager) SaveSpreadsheetReviewSnapshot(roomID string, snapshot SpreadsheetReviewSnapshot) (PhaseState, error) {
	rm, ok := m.Get(roomID)
	if !ok {
		return PhaseState{}, fmt.Errorf("%w: %s", ErrRoomNotFound, roomID)
	}
	if err := rm.SaveSpreadsheetReviewSnapshot(snapshot); err != nil {
		return PhaseState{}, err
	}
	return rm.PhaseState(), nil
}

func ProjectSpreadsheetReviewState(state PhaseState) *SpreadsheetReviewStateView {
	return projectSpreadsheetReviewStateFromBlob(state.PhaseOutputs[SpreadsheetReviewPhaseID])
}

func projectSpreadsheetReviewStateFromBlob(blob PhaseOutput) *SpreadsheetReviewStateView {
	if len(blob.Data) == 0 {
		return nil
	}
	tableID := readString(blob.Data, spreadsheetReviewTableIDKey)
	if tableID == "" {
		return nil
	}
	view := &SpreadsheetReviewStateView{
		TableID:        tableID,
		Columns:        readObjectSlice(blob.Data[spreadsheetReviewColumnsKey]),
		Rows:           readObjectSlice(blob.Data[spreadsheetReviewRowsKey]),
		QueryState:     readSpreadsheetReviewQueryState(blob.Data[spreadsheetReviewQueryStateKey]),
		Notes:          readString(blob.Data, spreadsheetReviewNotesKey),
		UpdatedAt:      readString(blob.Data, spreadsheetReviewUpdatedAtKey),
		SavedViews:     readSpreadsheetReviewSavedViews(blob.Data[spreadsheetReviewSavedViewsKey]),
		RowActions:     readSpreadsheetReviewRowActions(blob.Data[spreadsheetReviewRowActionsKey]),
		SelectedRowIDs: readStringSliceValue(blob.Data[spreadsheetReviewSelectedRowIDsKey]),
		SelectedRows:   readObjectSlice(blob.Data[spreadsheetReviewSelectedRowsKey]),
		ActionID:       readString(blob.Data, spreadsheetReviewActionIDKey),
		ExportRefs:     readSpreadsheetReviewExportRefs(blob.Data[spreadsheetReviewExportRefsKey]),
	}
	if view.Columns == nil {
		view.Columns = []map[string]any{}
	}
	if view.Rows == nil {
		view.Rows = []map[string]any{}
	}
	if view.QueryState == nil {
		view.QueryState = map[string]any{}
	}
	if view.SavedViews == nil {
		view.SavedViews = []SpreadsheetReviewSavedView{}
	}
	if view.RowActions == nil {
		view.RowActions = []SpreadsheetReviewRowAction{}
	}
	if view.SelectedRowIDs == nil {
		view.SelectedRowIDs = []string{}
	}
	if view.SelectedRows == nil {
		view.SelectedRows = []map[string]any{}
	}
	if view.ExportRefs == nil {
		view.ExportRefs = []SpreadsheetReviewExportRef{}
	}
	return view
}

func normalizeSpreadsheetReviewSnapshot(snapshot SpreadsheetReviewSnapshot) (SpreadsheetReviewSnapshot, error) {
	tableID := strings.TrimSpace(snapshot.TableID)
	if tableID == "" {
		return SpreadsheetReviewSnapshot{}, ErrInvalidSpreadsheetTableID
	}
	columns, err := normalizeSpreadsheetObjectSlice(snapshot.Columns, ErrInvalidSpreadsheetColumn)
	if err != nil {
		return SpreadsheetReviewSnapshot{}, err
	}
	rows, err := normalizeSpreadsheetObjectSlice(snapshot.Rows, ErrInvalidSpreadsheetRow)
	if err != nil {
		return SpreadsheetReviewSnapshot{}, err
	}
	selectedRows, err := normalizeSpreadsheetObjectSlice(snapshot.SelectedRows, ErrInvalidSpreadsheetRow)
	if err != nil {
		return SpreadsheetReviewSnapshot{}, err
	}
	savedViews, err := normalizeSpreadsheetSavedViews(snapshot.SavedViews)
	if err != nil {
		return SpreadsheetReviewSnapshot{}, err
	}
	rowActions, err := normalizeSpreadsheetRowActions(snapshot.RowActions)
	if err != nil {
		return SpreadsheetReviewSnapshot{}, err
	}
	exportRefs, err := normalizeSpreadsheetExportRefs(snapshot.ExportRefs)
	if err != nil {
		return SpreadsheetReviewSnapshot{}, err
	}
	selectedRowIDs := normalizeStringSlice(snapshot.SelectedRowIDs)
	actionID := strings.TrimSpace(snapshot.ActionID)
	if actionID != "" && !spreadsheetActionExists(rowActions, actionID) {
		return SpreadsheetReviewSnapshot{}, fmt.Errorf("%w: %q", ErrInvalidSpreadsheetActionID, actionID)
	}

	return SpreadsheetReviewSnapshot{
		TableID:        tableID,
		Columns:        columns,
		Rows:           rows,
		QueryState:     normalizeSpreadsheetReviewQueryState(snapshot.QueryState),
		Notes:          strings.TrimSpace(snapshot.Notes),
		UpdatedAt:      strings.TrimSpace(snapshot.UpdatedAt),
		SavedViews:     savedViews,
		RowActions:     rowActions,
		SelectedRowIDs: selectedRowIDs,
		SelectedRows:   selectedRows,
		ActionID:       actionID,
		ExportRefs:     exportRefs,
	}, nil
}

func spreadsheetReviewBlobFromSnapshot(blob PhaseOutput, snapshot SpreadsheetReviewSnapshot) (PhaseOutput, error) {
	switch {
	case blob.Version == 0:
		blob.Version = phaseOutputVersion
	case blob.Version != phaseOutputVersion:
		return PhaseOutput{}, fmt.Errorf("room: unsupported phase output version %d for %q", blob.Version, SpreadsheetReviewPhaseID)
	}
	blob.Data = map[string]any{
		spreadsheetReviewTableIDKey:        snapshot.TableID,
		spreadsheetReviewColumnsKey:        cloneObjectSlice(snapshot.Columns),
		spreadsheetReviewRowsKey:           cloneObjectSlice(snapshot.Rows),
		spreadsheetReviewQueryStateKey:     cloneAnyMap(snapshot.QueryState),
		spreadsheetReviewNotesKey:          snapshot.Notes,
		spreadsheetReviewUpdatedAtKey:      snapshot.UpdatedAt,
		spreadsheetReviewSavedViewsKey:     spreadsheetReviewSavedViewsAny(snapshot.SavedViews),
		spreadsheetReviewRowActionsKey:     spreadsheetReviewRowActionsAny(snapshot.RowActions),
		spreadsheetReviewSelectedRowIDsKey: cloneStringAny(snapshot.SelectedRowIDs),
		spreadsheetReviewSelectedRowsKey:   cloneObjectSlice(snapshot.SelectedRows),
		spreadsheetReviewActionIDKey:       snapshot.ActionID,
		spreadsheetReviewExportRefsKey:     spreadsheetReviewExportRefsAny(snapshot.ExportRefs),
	}
	return blob, nil
}

func normalizeSpreadsheetObjectSlice(items []map[string]any, invalidErr error) ([]map[string]any, error) {
	if len(items) == 0 {
		return []map[string]any{}, nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		normalized, err := normalizeJSONValue(item)
		if err != nil {
			return nil, fmt.Errorf("room: normalize spreadsheet object: %w", err)
		}
		record, ok := normalized.(map[string]any)
		if !ok {
			return nil, invalidErr
		}
		out = append(out, record)
	}
	return out, nil
}

func normalizeSpreadsheetSavedViews(views []SpreadsheetReviewSavedView) ([]SpreadsheetReviewSavedView, error) {
	if len(views) == 0 {
		return []SpreadsheetReviewSavedView{}, nil
	}
	out := make([]SpreadsheetReviewSavedView, 0, len(views))
	for _, view := range views {
		name := strings.TrimSpace(view.Name)
		if name == "" {
			return nil, ErrInvalidSpreadsheetSavedView
		}
		out = append(out, SpreadsheetReviewSavedView{
			Name:       name,
			QueryState: normalizeSpreadsheetReviewQueryState(view.QueryState),
		})
	}
	return out, nil
}

func normalizeSpreadsheetRowActions(actions []SpreadsheetReviewRowAction) ([]SpreadsheetReviewRowAction, error) {
	if len(actions) == 0 {
		return []SpreadsheetReviewRowAction{}, nil
	}
	out := make([]SpreadsheetReviewRowAction, 0, len(actions))
	seen := map[string]struct{}{}
	for _, action := range actions {
		id := strings.TrimSpace(action.ID)
		if id == "" {
			return nil, ErrInvalidSpreadsheetRowAction
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("%w: duplicate %q", ErrInvalidSpreadsheetRowAction, id)
		}
		seen[id] = struct{}{}
		label := strings.TrimSpace(action.Label)
		if label == "" {
			label = id
		}
		out = append(out, SpreadsheetReviewRowAction{
			ID:          id,
			Label:       label,
			Description: strings.TrimSpace(action.Description),
		})
	}
	return out, nil
}

func normalizeSpreadsheetExportRefs(refs []SpreadsheetReviewExportRef) ([]SpreadsheetReviewExportRef, error) {
	if len(refs) == 0 {
		return []SpreadsheetReviewExportRef{}, nil
	}
	out := make([]SpreadsheetReviewExportRef, 0, len(refs))
	for _, ref := range refs {
		name := strings.TrimSpace(ref.Name)
		if name == "" {
			return nil, ErrInvalidSpreadsheetExportRef
		}
		rowCount := ref.RowCount
		columnCount := ref.ColumnCount
		sizeBytes := ref.SizeBytes
		if rowCount < 0 || columnCount < 0 || sizeBytes < 0 {
			return nil, ErrInvalidSpreadsheetExportRef
		}
		out = append(out, SpreadsheetReviewExportRef{
			Name:        name,
			MIMEType:    strings.TrimSpace(ref.MIMEType),
			Kind:        strings.TrimSpace(ref.Kind),
			CreatedAt:   strings.TrimSpace(ref.CreatedAt),
			RowCount:    rowCount,
			ColumnCount: columnCount,
			SizeBytes:   sizeBytes,
		})
	}
	return out, nil
}

func normalizeSpreadsheetReviewQueryState(raw map[string]any) map[string]any {
	out := map[string]any{}
	if len(raw) == 0 {
		return out
	}
	if sort := readObjectSlice(raw[spreadsheetReviewSortKey]); len(sort) > 0 {
		out[spreadsheetReviewSortKey] = cloneObjectSlice(sort)
	}
	if filters := readObjectSlice(raw[spreadsheetReviewFiltersKey]); len(filters) > 0 {
		out[spreadsheetReviewFiltersKey] = cloneObjectSlice(filters)
	}
	if search := strings.TrimSpace(readString(raw, spreadsheetReviewSearchKey)); search != "" {
		out[spreadsheetReviewSearchKey] = search
	}
	if visibleColumns := readStringSlice(raw, spreadsheetReviewVisibleColumnsKey); len(visibleColumns) > 0 {
		out[spreadsheetReviewVisibleColumnsKey] = visibleColumns
	}
	if page := readObject(raw, spreadsheetReviewPageKey); len(page) > 0 {
		out[spreadsheetReviewPageKey] = page
	}
	return out
}

func readSpreadsheetReviewQueryState(raw any) map[string]any {
	record, ok := raw.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return normalizeSpreadsheetReviewQueryState(record)
}

func readSpreadsheetReviewSavedViews(raw any) []SpreadsheetReviewSavedView {
	records := readObjectSlice(raw)
	out := make([]SpreadsheetReviewSavedView, 0, len(records))
	for _, record := range records {
		name := strings.TrimSpace(readString(record, spreadsheetReviewSavedViewNameKey))
		if name == "" {
			continue
		}
		out = append(out, SpreadsheetReviewSavedView{
			Name:       name,
			QueryState: readSpreadsheetReviewQueryState(record[spreadsheetReviewQueryStateKey]),
		})
	}
	return out
}

func readSpreadsheetReviewRowActions(raw any) []SpreadsheetReviewRowAction {
	records := readObjectSlice(raw)
	out := make([]SpreadsheetReviewRowAction, 0, len(records))
	for _, record := range records {
		id := strings.TrimSpace(readString(record, spreadsheetReviewRowActionIDKey))
		if id == "" {
			continue
		}
		label := strings.TrimSpace(readString(record, spreadsheetReviewRowActionLabelKey))
		if label == "" {
			label = id
		}
		out = append(out, SpreadsheetReviewRowAction{
			ID:          id,
			Label:       label,
			Description: strings.TrimSpace(readString(record, spreadsheetReviewRowActionDescKey)),
		})
	}
	return out
}

func readSpreadsheetReviewExportRefs(raw any) []SpreadsheetReviewExportRef {
	records := readObjectSlice(raw)
	out := make([]SpreadsheetReviewExportRef, 0, len(records))
	for _, record := range records {
		name := strings.TrimSpace(readString(record, spreadsheetReviewExportNameKey))
		if name == "" {
			continue
		}
		out = append(out, SpreadsheetReviewExportRef{
			Name:        name,
			MIMEType:    strings.TrimSpace(readString(record, spreadsheetReviewExportMIMETypeKey)),
			Kind:        strings.TrimSpace(readString(record, spreadsheetReviewExportKindKey)),
			CreatedAt:   strings.TrimSpace(readString(record, spreadsheetReviewExportCreatedAtKey)),
			RowCount:    int(readNumber(record, spreadsheetReviewExportRowCountKey)),
			ColumnCount: int(readNumber(record, spreadsheetReviewExportColCountKey)),
			SizeBytes:   int(readNumber(record, spreadsheetReviewExportSizeBytesKey)),
		})
	}
	return out
}

func spreadsheetReviewSavedViewsAny(views []SpreadsheetReviewSavedView) []map[string]any {
	if len(views) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(views))
	for _, view := range views {
		out = append(out, map[string]any{
			spreadsheetReviewSavedViewNameKey: nameOrEmpty(view.Name),
			spreadsheetReviewQueryStateKey:    cloneAnyMap(view.QueryState),
		})
	}
	return out
}

func spreadsheetReviewRowActionsAny(actions []SpreadsheetReviewRowAction) []map[string]any {
	if len(actions) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(actions))
	for _, action := range actions {
		record := map[string]any{
			spreadsheetReviewRowActionIDKey:    action.ID,
			spreadsheetReviewRowActionLabelKey: action.Label,
		}
		if action.Description != "" {
			record[spreadsheetReviewRowActionDescKey] = action.Description
		}
		out = append(out, record)
	}
	return out
}

func spreadsheetReviewExportRefsAny(refs []SpreadsheetReviewExportRef) []map[string]any {
	if len(refs) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		record := map[string]any{
			spreadsheetReviewExportNameKey: ref.Name,
		}
		if ref.MIMEType != "" {
			record[spreadsheetReviewExportMIMETypeKey] = ref.MIMEType
		}
		if ref.Kind != "" {
			record[spreadsheetReviewExportKindKey] = ref.Kind
		}
		if ref.CreatedAt != "" {
			record[spreadsheetReviewExportCreatedAtKey] = ref.CreatedAt
		}
		if ref.RowCount > 0 {
			record[spreadsheetReviewExportRowCountKey] = ref.RowCount
		}
		if ref.ColumnCount > 0 {
			record[spreadsheetReviewExportColCountKey] = ref.ColumnCount
		}
		if ref.SizeBytes > 0 {
			record[spreadsheetReviewExportSizeBytesKey] = ref.SizeBytes
		}
		out = append(out, record)
	}
	return out
}

func readObject(record map[string]any, key string) map[string]any {
	raw, ok := record[key]
	if !ok {
		return map[string]any{}
	}
	value, ok := raw.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return cloneAnyMap(value)
}

func readObjectSlice(raw any) []map[string]any {
	switch items := raw.(type) {
	case []map[string]any:
		return cloneObjectSlice(items)
	case []any:
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, cloneAnyMap(record))
		}
		return out
	default:
		return []map[string]any{}
	}
}

func readStringSlice(record map[string]any, key string) []string {
	raw, ok := record[key]
	if !ok {
		return []string{}
	}
	return readStringSliceValue(raw)
}

func readStringSliceValue(raw any) []string {
	switch items := raw.(type) {
	case []string:
		return normalizeStringSlice(items)
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			value, ok := item.(string)
			if !ok {
				continue
			}
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			out = append(out, value)
		}
		return out
	default:
		return []string{}
	}
}

func normalizeStringSlice(items []string) []string {
	if len(items) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		value := strings.TrimSpace(item)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func cloneObjectSlice(in []map[string]any) []map[string]any {
	if len(in) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(in))
	for _, item := range in {
		out = append(out, cloneAnyMap(item))
	}
	return out
}

func cloneStringAny(in []string) []any {
	if len(in) == 0 {
		return []any{}
	}
	out := make([]any, 0, len(in))
	for _, item := range in {
		out = append(out, item)
	}
	return out
}

func spreadsheetActionExists(actions []SpreadsheetReviewRowAction, actionID string) bool {
	if actionID == "" {
		return true
	}
	for _, action := range actions {
		if action.ID == actionID {
			return true
		}
	}
	return false
}

func readNumber(record map[string]any, key string) float64 {
	if record == nil {
		return 0
	}
	switch value := record[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case int32:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return 0
	}
}

func nameOrEmpty(v string) string {
	return strings.TrimSpace(v)
}
