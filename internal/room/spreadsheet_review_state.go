package room

import (
	"fmt"
	"strings"
)

const (
	SpreadsheetReviewPhaseID           = "spreadsheet-review"
	spreadsheetReviewTableIDKey        = "table_id"
	spreadsheetReviewColumnsKey        = "columns"
	spreadsheetReviewRowsKey           = "rows"
	spreadsheetReviewQueryStateKey     = "query_state"
	spreadsheetReviewNotesKey          = "notes"
	spreadsheetReviewUpdatedAtKey      = "updated_at"
	spreadsheetReviewSavedViewsKey     = "saved_views"
	spreadsheetReviewSavedViewNameKey  = "name"
	spreadsheetReviewVisibleColumnsKey = "visible_columns"
	spreadsheetReviewSortKey           = "sort"
	spreadsheetReviewFiltersKey        = "filters"
	spreadsheetReviewSearchKey         = "search"
	spreadsheetReviewPageKey           = "page"
)

type SpreadsheetReviewSavedView struct {
	Name       string         `json:"name"`
	QueryState map[string]any `json:"query_state"`
}

type SpreadsheetReviewStateView struct {
	TableID    string                       `json:"table_id"`
	Columns    []map[string]any             `json:"columns"`
	Rows       []map[string]any             `json:"rows"`
	QueryState map[string]any               `json:"query_state"`
	Notes      string                       `json:"notes,omitempty"`
	UpdatedAt  string                       `json:"updated_at,omitempty"`
	SavedViews []SpreadsheetReviewSavedView `json:"saved_views"`
}

type SpreadsheetReviewSnapshot struct {
	TableID    string
	Columns    []map[string]any
	Rows       []map[string]any
	QueryState map[string]any
	Notes      string
	UpdatedAt  string
	SavedViews []SpreadsheetReviewSavedView
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
		TableID:    current.TableID,
		Columns:    current.Columns,
		Rows:       current.Rows,
		QueryState: current.QueryState,
		Notes:      current.Notes,
		UpdatedAt:  current.UpdatedAt,
		SavedViews: normalizedSavedViews,
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
		TableID:    tableID,
		Columns:    readObjectSlice(blob.Data[spreadsheetReviewColumnsKey]),
		Rows:       readObjectSlice(blob.Data[spreadsheetReviewRowsKey]),
		QueryState: readSpreadsheetReviewQueryState(blob.Data[spreadsheetReviewQueryStateKey]),
		Notes:      readString(blob.Data, spreadsheetReviewNotesKey),
		UpdatedAt:  readString(blob.Data, spreadsheetReviewUpdatedAtKey),
		SavedViews: readSpreadsheetReviewSavedViews(blob.Data[spreadsheetReviewSavedViewsKey]),
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
	savedViews, err := normalizeSpreadsheetSavedViews(snapshot.SavedViews)
	if err != nil {
		return SpreadsheetReviewSnapshot{}, err
	}

	return SpreadsheetReviewSnapshot{
		TableID:    tableID,
		Columns:    columns,
		Rows:       rows,
		QueryState: normalizeSpreadsheetReviewQueryState(snapshot.QueryState),
		Notes:      strings.TrimSpace(snapshot.Notes),
		UpdatedAt:  strings.TrimSpace(snapshot.UpdatedAt),
		SavedViews: savedViews,
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
		spreadsheetReviewTableIDKey:    snapshot.TableID,
		spreadsheetReviewColumnsKey:    cloneObjectSlice(snapshot.Columns),
		spreadsheetReviewRowsKey:       cloneObjectSlice(snapshot.Rows),
		spreadsheetReviewQueryStateKey: cloneAnyMap(snapshot.QueryState),
		spreadsheetReviewNotesKey:      snapshot.Notes,
		spreadsheetReviewUpdatedAtKey:  snapshot.UpdatedAt,
		spreadsheetReviewSavedViewsKey: spreadsheetReviewSavedViewsAny(snapshot.SavedViews),
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
	switch items := raw.(type) {
	case []string:
		out := make([]string, 0, len(items))
		for _, value := range items {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			out = append(out, value)
		}
		return out
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

func nameOrEmpty(v string) string {
	return strings.TrimSpace(v)
}
