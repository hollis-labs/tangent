package room

import (
	"fmt"
	"strings"
)

const (
	DashboardPhaseID = "dashboard"

	dashboardIDKey             = "dashboard_id"
	dashboardTitleKey          = "title"
	dashboardTilesKey          = "tiles"
	dashboardLayoutKey         = "layout"
	dashboardSavedLayoutsKey   = "saved_layouts"
	dashboardActiveLayoutIDKey = "active_layout_id"
	dashboardQueryStateKey     = "query_state"
	dashboardSummaryKey        = "summary"
	dashboardUpdatedAtKey      = "updated_at"

	dashboardTileIDKey       = "tile_id"
	dashboardTileKindKey     = "kind"
	dashboardTileTitleKey    = "title"
	dashboardTileSubtitleKey = "subtitle"
	dashboardTileStatusKey   = "status"
	dashboardTileValueKey    = "value"
	dashboardTileUnitKey     = "unit"
	dashboardTileSummaryKey  = "summary"
	dashboardTileRoomIDKey   = "room_id"
	dashboardTileWorkflowKey = "workflow"
	dashboardTileArtifactKey = "artifact_ref"
	dashboardTileMetadataKey = "metadata"

	dashboardLayoutTileIDKey = "tile_id"
	dashboardLayoutXKey      = "x"
	dashboardLayoutYKey      = "y"
	dashboardLayoutWKey      = "w"
	dashboardLayoutHKey      = "h"

	dashboardSavedLayoutIDKey          = "layout_id"
	dashboardSavedLayoutNameKey        = "name"
	dashboardSavedLayoutDescriptionKey = "description"
	dashboardSavedLayoutTilesKey       = "tiles"
	dashboardSavedLayoutDefaultKey     = "is_default"
	dashboardSavedLayoutUpdatedAtKey   = "updated_at"

	dashboardQuerySearchKey        = "search"
	dashboardQueryScopeKey         = "scope"
	dashboardQueryGroupByKey       = "group_by"
	dashboardQueryFiltersKey       = "filters"
	dashboardQueryRangeKey         = "range"
	dashboardQuerySortKey          = "sort"
	dashboardQueryFilterIDKey      = "filter_id"
	dashboardQueryFilterLabelKey   = "label"
	dashboardQueryFilterOperator   = "operator"
	dashboardQueryFilterValuesKey  = "values"
	dashboardQueryRangeKindKey     = "kind"
	dashboardQueryRangePresetKey   = "preset"
	dashboardQueryRangeFromKey     = "from"
	dashboardQueryRangeToKey       = "to"
	dashboardQueryRangeTimezoneKey = "timezone"
	dashboardQuerySortFieldKey     = "field"
	dashboardQuerySortDirKey       = "direction"

	dashboardSummaryHeadlineKey           = "headline"
	dashboardSummaryDetailKey             = "detail"
	dashboardSummaryStatusKey             = "status"
	dashboardSummaryTileCountKey          = "tile_count"
	dashboardSummaryActiveRoomCountKey    = "active_room_count"
	dashboardSummaryLastRefreshAtKey      = "last_refresh_at"
	dashboardSummaryAcceptedSnapshotIDKey = "accepted_snapshot_id"
	dashboardSummaryAcceptedSnapshotAtKey = "accepted_snapshot_at"
)

var allowedDashboardSortDirections = map[string]struct{}{
	"asc":  {},
	"desc": {},
}

type DashboardTile struct {
	TileID      string         `json:"tile_id"`
	Kind        string         `json:"kind"`
	Title       string         `json:"title"`
	Subtitle    string         `json:"subtitle,omitempty"`
	Status      string         `json:"status,omitempty"`
	Value       string         `json:"value,omitempty"`
	Unit        string         `json:"unit,omitempty"`
	Summary     string         `json:"summary,omitempty"`
	RoomID      string         `json:"room_id,omitempty"`
	Workflow    string         `json:"workflow,omitempty"`
	ArtifactRef string         `json:"artifact_ref,omitempty"`
	Metadata    map[string]any `json:"metadata"`
}

type DashboardTilePlacement struct {
	TileID string `json:"tile_id"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	W      int    `json:"w"`
	H      int    `json:"h"`
}

type DashboardSavedLayout struct {
	LayoutID    string                   `json:"layout_id"`
	Name        string                   `json:"name"`
	Description string                   `json:"description,omitempty"`
	Tiles       []DashboardTilePlacement `json:"tiles"`
	IsDefault   bool                     `json:"is_default,omitempty"`
	UpdatedAt   string                   `json:"updated_at,omitempty"`
}

type DashboardFilterState struct {
	FilterID string   `json:"filter_id"`
	Label    string   `json:"label,omitempty"`
	Operator string   `json:"operator,omitempty"`
	Values   []string `json:"values"`
}

type DashboardRangeState struct {
	Kind     string `json:"kind,omitempty"`
	Preset   string `json:"preset,omitempty"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

type DashboardSortState struct {
	Field     string `json:"field"`
	Direction string `json:"direction,omitempty"`
}

type DashboardQueryState struct {
	Search  string                 `json:"search,omitempty"`
	Scope   string                 `json:"scope,omitempty"`
	GroupBy string                 `json:"group_by,omitempty"`
	Filters []DashboardFilterState `json:"filters"`
	Range   *DashboardRangeState   `json:"range,omitempty"`
	Sort    []DashboardSortState   `json:"sort"`
}

type DashboardSummary struct {
	Headline           string `json:"headline,omitempty"`
	Detail             string `json:"detail,omitempty"`
	Status             string `json:"status,omitempty"`
	TileCount          int    `json:"tile_count,omitempty"`
	ActiveRoomCount    int    `json:"active_room_count,omitempty"`
	LastRefreshAt      string `json:"last_refresh_at,omitempty"`
	AcceptedSnapshotID string `json:"accepted_snapshot_id,omitempty"`
	AcceptedSnapshotAt string `json:"accepted_snapshot_at,omitempty"`
}

type DashboardStateView struct {
	DashboardID    string                   `json:"dashboard_id"`
	Title          string                   `json:"title,omitempty"`
	Tiles          []DashboardTile          `json:"tiles"`
	Layout         []DashboardTilePlacement `json:"layout"`
	SavedLayouts   []DashboardSavedLayout   `json:"saved_layouts"`
	ActiveLayoutID string                   `json:"active_layout_id,omitempty"`
	QueryState     *DashboardQueryState     `json:"query_state,omitempty"`
	Summary        *DashboardSummary        `json:"summary,omitempty"`
	UpdatedAt      string                   `json:"updated_at,omitempty"`
}

type DashboardSnapshot struct {
	DashboardID    string
	Title          string
	Tiles          []DashboardTile
	Layout         []DashboardTilePlacement
	SavedLayouts   []DashboardSavedLayout
	ActiveLayoutID string
	QueryState     *DashboardQueryState
	Summary        *DashboardSummary
	UpdatedAt      string
}

func (r *Room) SaveDashboardSnapshot(snapshot DashboardSnapshot) error {
	normalized, err := normalizeDashboardSnapshot(snapshot)
	if err != nil {
		return err
	}

	r.phaseMu.Lock()
	defer r.phaseMu.Unlock()

	nextOutputs := clonePhaseOutputs(r.phaseOutputs)
	nextOutputs[DashboardPhaseID] = dashboardBlobFromSnapshot(normalized)
	nextVisited := cloneStringSlice(r.phasesVisited)
	if err := r.persistPhaseState(r.currentPhase, nextVisited, nextOutputs); err != nil {
		return err
	}

	r.phasesVisited = nextVisited
	r.phaseOutputs = nextOutputs
	return nil
}

func (m *Manager) SaveDashboardSnapshot(roomID string, snapshot DashboardSnapshot) (PhaseState, error) {
	rm, ok := m.Get(roomID)
	if !ok {
		return PhaseState{}, fmt.Errorf("%w: %s", ErrRoomNotFound, roomID)
	}
	if err := rm.SaveDashboardSnapshot(snapshot); err != nil {
		return PhaseState{}, err
	}
	return rm.PhaseState(), nil
}

func ProjectDashboardState(state PhaseState) *DashboardStateView {
	return projectDashboardStateFromBlob(state.PhaseOutputs[DashboardPhaseID])
}

func projectDashboardStateFromBlob(blob PhaseOutput) *DashboardStateView {
	if len(blob.Data) == 0 {
		return nil
	}
	dashboardID := readString(blob.Data, dashboardIDKey)
	if dashboardID == "" {
		return nil
	}
	view := &DashboardStateView{
		DashboardID:    dashboardID,
		Title:          readString(blob.Data, dashboardTitleKey),
		Tiles:          readDashboardTiles(blob.Data[dashboardTilesKey]),
		Layout:         readDashboardTilePlacements(blob.Data[dashboardLayoutKey]),
		SavedLayouts:   readDashboardSavedLayouts(blob.Data[dashboardSavedLayoutsKey]),
		ActiveLayoutID: readString(blob.Data, dashboardActiveLayoutIDKey),
		QueryState:     readDashboardQueryState(blob.Data[dashboardQueryStateKey]),
		Summary:        readDashboardSummary(blob.Data[dashboardSummaryKey]),
		UpdatedAt:      readString(blob.Data, dashboardUpdatedAtKey),
	}
	if view.Tiles == nil {
		view.Tiles = []DashboardTile{}
	}
	if view.Layout == nil {
		view.Layout = []DashboardTilePlacement{}
	}
	if view.SavedLayouts == nil {
		view.SavedLayouts = []DashboardSavedLayout{}
	}
	return view
}

func normalizeDashboardSnapshot(snapshot DashboardSnapshot) (DashboardSnapshot, error) {
	dashboardID := strings.TrimSpace(snapshot.DashboardID)
	if dashboardID == "" {
		return DashboardSnapshot{}, ErrInvalidDashboardID
	}
	tiles, tileIDs, err := normalizeDashboardTiles(snapshot.Tiles)
	if err != nil {
		return DashboardSnapshot{}, err
	}
	layout, err := normalizeDashboardTilePlacements(snapshot.Layout, tileIDs, ErrInvalidDashboardLayout)
	if err != nil {
		return DashboardSnapshot{}, err
	}
	savedLayouts, savedLayoutIDs, err := normalizeDashboardSavedLayouts(snapshot.SavedLayouts, tileIDs)
	if err != nil {
		return DashboardSnapshot{}, err
	}
	activeLayoutID := strings.TrimSpace(snapshot.ActiveLayoutID)
	if activeLayoutID != "" {
		if _, ok := savedLayoutIDs[activeLayoutID]; !ok {
			return DashboardSnapshot{}, fmt.Errorf("%w: unknown active_layout_id %q", ErrInvalidDashboardLayout, activeLayoutID)
		}
	}
	queryState, err := normalizeDashboardQueryState(snapshot.QueryState)
	if err != nil {
		return DashboardSnapshot{}, err
	}
	summary, err := normalizeDashboardSummary(snapshot.Summary, len(tiles))
	if err != nil {
		return DashboardSnapshot{}, err
	}
	return DashboardSnapshot{
		DashboardID:    dashboardID,
		Title:          strings.TrimSpace(snapshot.Title),
		Tiles:          tiles,
		Layout:         layout,
		SavedLayouts:   savedLayouts,
		ActiveLayoutID: activeLayoutID,
		QueryState:     queryState,
		Summary:        summary,
		UpdatedAt:      strings.TrimSpace(snapshot.UpdatedAt),
	}, nil
}

func normalizeDashboardTiles(items []DashboardTile) ([]DashboardTile, map[string]struct{}, error) {
	if len(items) == 0 {
		return []DashboardTile{}, map[string]struct{}{}, nil
	}
	out := make([]DashboardTile, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		tileID := strings.TrimSpace(item.TileID)
		kind := strings.TrimSpace(item.Kind)
		title := strings.TrimSpace(item.Title)
		if tileID == "" || kind == "" || title == "" {
			return nil, nil, ErrInvalidDashboardTile
		}
		if _, exists := seen[tileID]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate tile_id %q", ErrInvalidDashboardTile, tileID)
		}
		metadata, err := normalizeFormMap(item.Metadata, ErrInvalidDashboardTile)
		if err != nil {
			return nil, nil, err
		}
		seen[tileID] = struct{}{}
		out = append(out, DashboardTile{
			TileID:      tileID,
			Kind:        kind,
			Title:       title,
			Subtitle:    strings.TrimSpace(item.Subtitle),
			Status:      strings.TrimSpace(item.Status),
			Value:       strings.TrimSpace(item.Value),
			Unit:        strings.TrimSpace(item.Unit),
			Summary:     strings.TrimSpace(item.Summary),
			RoomID:      strings.TrimSpace(item.RoomID),
			Workflow:    strings.TrimSpace(item.Workflow),
			ArtifactRef: strings.TrimSpace(item.ArtifactRef),
			Metadata:    metadata,
		})
	}
	return out, seen, nil
}

func normalizeDashboardTilePlacements(
	items []DashboardTilePlacement,
	validTileIDs map[string]struct{},
	invalidErr error,
) ([]DashboardTilePlacement, error) {
	if len(items) == 0 {
		return []DashboardTilePlacement{}, nil
	}
	out := make([]DashboardTilePlacement, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		tileID := strings.TrimSpace(item.TileID)
		if tileID == "" || item.X < 0 || item.Y < 0 || item.W <= 0 || item.H <= 0 {
			return nil, invalidErr
		}
		if _, ok := validTileIDs[tileID]; !ok {
			return nil, fmt.Errorf("%w: unknown tile_id %q", invalidErr, tileID)
		}
		if _, exists := seen[tileID]; exists {
			return nil, fmt.Errorf("%w: duplicate tile_id %q", invalidErr, tileID)
		}
		seen[tileID] = struct{}{}
		out = append(out, DashboardTilePlacement{
			TileID: tileID,
			X:      item.X,
			Y:      item.Y,
			W:      item.W,
			H:      item.H,
		})
	}
	return out, nil
}

func normalizeDashboardSavedLayouts(
	items []DashboardSavedLayout,
	validTileIDs map[string]struct{},
) ([]DashboardSavedLayout, map[string]struct{}, error) {
	if len(items) == 0 {
		return []DashboardSavedLayout{}, map[string]struct{}{}, nil
	}
	out := make([]DashboardSavedLayout, 0, len(items))
	seenIDs := make(map[string]struct{}, len(items))
	for _, item := range items {
		layoutID := strings.TrimSpace(item.LayoutID)
		name := strings.TrimSpace(item.Name)
		if layoutID == "" || name == "" {
			return nil, nil, ErrInvalidDashboardSavedLayout
		}
		if _, exists := seenIDs[layoutID]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate layout_id %q", ErrInvalidDashboardSavedLayout, layoutID)
		}
		tiles, err := normalizeDashboardTilePlacements(item.Tiles, validTileIDs, ErrInvalidDashboardSavedLayout)
		if err != nil {
			return nil, nil, err
		}
		seenIDs[layoutID] = struct{}{}
		out = append(out, DashboardSavedLayout{
			LayoutID:    layoutID,
			Name:        name,
			Description: strings.TrimSpace(item.Description),
			Tiles:       tiles,
			IsDefault:   item.IsDefault,
			UpdatedAt:   strings.TrimSpace(item.UpdatedAt),
		})
	}
	return out, seenIDs, nil
}

func normalizeDashboardQueryState(state *DashboardQueryState) (*DashboardQueryState, error) {
	if state == nil {
		return nil, nil
	}
	filters := make([]DashboardFilterState, 0, len(state.Filters))
	seenFilterIDs := make(map[string]struct{}, len(state.Filters))
	for _, filter := range state.Filters {
		filterID := strings.TrimSpace(filter.FilterID)
		if filterID == "" {
			return nil, ErrInvalidDashboardQueryState
		}
		if _, exists := seenFilterIDs[filterID]; exists {
			return nil, fmt.Errorf("%w: duplicate filter_id %q", ErrInvalidDashboardQueryState, filterID)
		}
		seenFilterIDs[filterID] = struct{}{}
		values := normalizeStringSlice(filter.Values)
		operator := strings.TrimSpace(filter.Operator)
		if operator == "" {
			operator = "in"
		}
		filters = append(filters, DashboardFilterState{
			FilterID: filterID,
			Label:    strings.TrimSpace(filter.Label),
			Operator: operator,
			Values:   values,
		})
	}

	sorts := make([]DashboardSortState, 0, len(state.Sort))
	for _, item := range state.Sort {
		field := strings.TrimSpace(item.Field)
		if field == "" {
			return nil, ErrInvalidDashboardQueryState
		}
		direction := strings.TrimSpace(item.Direction)
		if direction == "" {
			direction = "asc"
		}
		if _, ok := allowedDashboardSortDirections[direction]; !ok {
			return nil, ErrInvalidDashboardQueryState
		}
		sorts = append(sorts, DashboardSortState{Field: field, Direction: direction})
	}

	var rng *DashboardRangeState
	if state.Range != nil {
		rng = &DashboardRangeState{
			Kind:     strings.TrimSpace(state.Range.Kind),
			Preset:   strings.TrimSpace(state.Range.Preset),
			From:     strings.TrimSpace(state.Range.From),
			To:       strings.TrimSpace(state.Range.To),
			Timezone: strings.TrimSpace(state.Range.Timezone),
		}
		if rng.Kind == "" && rng.Preset == "" && rng.From == "" && rng.To == "" && rng.Timezone == "" {
			rng = nil
		}
	}

	return &DashboardQueryState{
		Search:  strings.TrimSpace(state.Search),
		Scope:   strings.TrimSpace(state.Scope),
		GroupBy: strings.TrimSpace(state.GroupBy),
		Filters: filters,
		Range:   rng,
		Sort:    sorts,
	}, nil
}

func normalizeDashboardSummary(summary *DashboardSummary, tileCount int) (*DashboardSummary, error) {
	if summary == nil {
		return nil, nil
	}
	if summary.TileCount < 0 || summary.ActiveRoomCount < 0 {
		return nil, ErrInvalidDashboardSummary
	}
	normalizedTileCount := summary.TileCount
	if normalizedTileCount == 0 && tileCount > 0 {
		normalizedTileCount = tileCount
	}
	return &DashboardSummary{
		Headline:           strings.TrimSpace(summary.Headline),
		Detail:             strings.TrimSpace(summary.Detail),
		Status:             strings.TrimSpace(summary.Status),
		TileCount:          normalizedTileCount,
		ActiveRoomCount:    summary.ActiveRoomCount,
		LastRefreshAt:      strings.TrimSpace(summary.LastRefreshAt),
		AcceptedSnapshotID: strings.TrimSpace(summary.AcceptedSnapshotID),
		AcceptedSnapshotAt: strings.TrimSpace(summary.AcceptedSnapshotAt),
	}, nil
}

func dashboardBlobFromSnapshot(snapshot DashboardSnapshot) PhaseOutput {
	record := map[string]any{
		dashboardIDKey:           snapshot.DashboardID,
		dashboardTitleKey:        snapshot.Title,
		dashboardTilesKey:        dashboardTilesAny(snapshot.Tiles),
		dashboardLayoutKey:       dashboardTilePlacementsAny(snapshot.Layout),
		dashboardSavedLayoutsKey: dashboardSavedLayoutsAny(snapshot.SavedLayouts),
		dashboardUpdatedAtKey:    snapshot.UpdatedAt,
	}
	if snapshot.ActiveLayoutID != "" {
		record[dashboardActiveLayoutIDKey] = snapshot.ActiveLayoutID
	}
	if queryState := dashboardQueryStateAny(snapshot.QueryState); len(queryState) > 0 {
		record[dashboardQueryStateKey] = queryState
	}
	if summary := dashboardSummaryAny(snapshot.Summary); len(summary) > 0 {
		record[dashboardSummaryKey] = summary
	}
	return PhaseOutput{Version: phaseOutputVersion, Data: record}
}

func dashboardTilesAny(items []DashboardTile) []map[string]any {
	if len(items) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			dashboardTileIDKey:       item.TileID,
			dashboardTileKindKey:     item.Kind,
			dashboardTileTitleKey:    item.Title,
			dashboardTileSubtitleKey: item.Subtitle,
			dashboardTileStatusKey:   item.Status,
			dashboardTileValueKey:    item.Value,
			dashboardTileUnitKey:     item.Unit,
			dashboardTileSummaryKey:  item.Summary,
			dashboardTileRoomIDKey:   item.RoomID,
			dashboardTileWorkflowKey: item.Workflow,
			dashboardTileArtifactKey: item.ArtifactRef,
			dashboardTileMetadataKey: cloneAnyMap(item.Metadata),
		})
	}
	return out
}

func dashboardTilePlacementsAny(items []DashboardTilePlacement) []map[string]any {
	if len(items) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			dashboardLayoutTileIDKey: item.TileID,
			dashboardLayoutXKey:      item.X,
			dashboardLayoutYKey:      item.Y,
			dashboardLayoutWKey:      item.W,
			dashboardLayoutHKey:      item.H,
		})
	}
	return out
}

func dashboardSavedLayoutsAny(items []DashboardSavedLayout) []map[string]any {
	if len(items) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			dashboardSavedLayoutIDKey:          item.LayoutID,
			dashboardSavedLayoutNameKey:        item.Name,
			dashboardSavedLayoutDescriptionKey: item.Description,
			dashboardSavedLayoutTilesKey:       dashboardTilePlacementsAny(item.Tiles),
			dashboardSavedLayoutDefaultKey:     item.IsDefault,
			dashboardSavedLayoutUpdatedAtKey:   item.UpdatedAt,
		})
	}
	return out
}

func dashboardQueryStateAny(state *DashboardQueryState) map[string]any {
	if state == nil {
		return map[string]any{}
	}
	out := map[string]any{
		dashboardQuerySearchKey:  state.Search,
		dashboardQueryScopeKey:   state.Scope,
		dashboardQueryGroupByKey: state.GroupBy,
		dashboardQueryFiltersKey: dashboardFiltersAny(state.Filters),
		dashboardQuerySortKey:    dashboardSortsAny(state.Sort),
	}
	if state.Range != nil {
		out[dashboardQueryRangeKey] = dashboardRangeAny(state.Range)
	}
	return out
}

func dashboardFiltersAny(filters []DashboardFilterState) []map[string]any {
	if len(filters) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(filters))
	for _, filter := range filters {
		out = append(out, map[string]any{
			dashboardQueryFilterIDKey:     filter.FilterID,
			dashboardQueryFilterLabelKey:  filter.Label,
			dashboardQueryFilterOperator:  filter.Operator,
			dashboardQueryFilterValuesKey: cloneStringAny(filter.Values),
		})
	}
	return out
}

func dashboardRangeAny(rng *DashboardRangeState) map[string]any {
	if rng == nil {
		return map[string]any{}
	}
	return map[string]any{
		dashboardQueryRangeKindKey:     rng.Kind,
		dashboardQueryRangePresetKey:   rng.Preset,
		dashboardQueryRangeFromKey:     rng.From,
		dashboardQueryRangeToKey:       rng.To,
		dashboardQueryRangeTimezoneKey: rng.Timezone,
	}
}

func dashboardSortsAny(items []DashboardSortState) []map[string]any {
	if len(items) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			dashboardQuerySortFieldKey: item.Field,
			dashboardQuerySortDirKey:   item.Direction,
		})
	}
	return out
}

func dashboardSummaryAny(summary *DashboardSummary) map[string]any {
	if summary == nil {
		return map[string]any{}
	}
	return map[string]any{
		dashboardSummaryHeadlineKey:           summary.Headline,
		dashboardSummaryDetailKey:             summary.Detail,
		dashboardSummaryStatusKey:             summary.Status,
		dashboardSummaryTileCountKey:          summary.TileCount,
		dashboardSummaryActiveRoomCountKey:    summary.ActiveRoomCount,
		dashboardSummaryLastRefreshAtKey:      summary.LastRefreshAt,
		dashboardSummaryAcceptedSnapshotIDKey: summary.AcceptedSnapshotID,
		dashboardSummaryAcceptedSnapshotAtKey: summary.AcceptedSnapshotAt,
	}
}

func readDashboardTiles(raw any) []DashboardTile {
	records := readDashboardRecords(raw)
	if records == nil {
		return nil
	}
	out := make([]DashboardTile, 0, len(records))
	for _, m := range records {
		out = append(out, DashboardTile{
			TileID:      readString(m, dashboardTileIDKey),
			Kind:        readString(m, dashboardTileKindKey),
			Title:       readString(m, dashboardTileTitleKey),
			Subtitle:    readString(m, dashboardTileSubtitleKey),
			Status:      readString(m, dashboardTileStatusKey),
			Value:       readString(m, dashboardTileValueKey),
			Unit:        readString(m, dashboardTileUnitKey),
			Summary:     readString(m, dashboardTileSummaryKey),
			RoomID:      readString(m, dashboardTileRoomIDKey),
			Workflow:    readString(m, dashboardTileWorkflowKey),
			ArtifactRef: readString(m, dashboardTileArtifactKey),
			Metadata:    readObjectValueMap(m[dashboardTileMetadataKey]),
		})
	}
	return out
}

func readDashboardTilePlacements(raw any) []DashboardTilePlacement {
	records := readDashboardRecords(raw)
	if records == nil {
		return nil
	}
	out := make([]DashboardTilePlacement, 0, len(records))
	for _, m := range records {
		out = append(out, DashboardTilePlacement{
			TileID: readString(m, dashboardLayoutTileIDKey),
			X:      readDashboardInt(m[dashboardLayoutXKey]),
			Y:      readDashboardInt(m[dashboardLayoutYKey]),
			W:      readDashboardInt(m[dashboardLayoutWKey]),
			H:      readDashboardInt(m[dashboardLayoutHKey]),
		})
	}
	return out
}

func readDashboardSavedLayouts(raw any) []DashboardSavedLayout {
	records := readDashboardRecords(raw)
	if records == nil {
		return nil
	}
	out := make([]DashboardSavedLayout, 0, len(records))
	for _, m := range records {
		out = append(out, DashboardSavedLayout{
			LayoutID:    readString(m, dashboardSavedLayoutIDKey),
			Name:        readString(m, dashboardSavedLayoutNameKey),
			Description: readString(m, dashboardSavedLayoutDescriptionKey),
			Tiles:       readDashboardTilePlacements(m[dashboardSavedLayoutTilesKey]),
			IsDefault:   readDashboardBool(m[dashboardSavedLayoutDefaultKey]),
			UpdatedAt:   readString(m, dashboardSavedLayoutUpdatedAtKey),
		})
	}
	return out
}

func readDashboardQueryState(raw any) *DashboardQueryState {
	record, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	state := &DashboardQueryState{
		Search:  readString(record, dashboardQuerySearchKey),
		Scope:   readString(record, dashboardQueryScopeKey),
		GroupBy: readString(record, dashboardQueryGroupByKey),
		Filters: readDashboardFilters(record[dashboardQueryFiltersKey]),
		Range:   readDashboardRange(record[dashboardQueryRangeKey]),
		Sort:    readDashboardSorts(record[dashboardQuerySortKey]),
	}
	if state.Filters == nil {
		state.Filters = []DashboardFilterState{}
	}
	if state.Sort == nil {
		state.Sort = []DashboardSortState{}
	}
	return state
}

func readDashboardFilters(raw any) []DashboardFilterState {
	records := readDashboardRecords(raw)
	if records == nil {
		return nil
	}
	out := make([]DashboardFilterState, 0, len(records))
	for _, m := range records {
		out = append(out, DashboardFilterState{
			FilterID: readString(m, dashboardQueryFilterIDKey),
			Label:    readString(m, dashboardQueryFilterLabelKey),
			Operator: readString(m, dashboardQueryFilterOperator),
			Values:   readStringSliceValue(m[dashboardQueryFilterValuesKey]),
		})
	}
	return out
}

func readDashboardRange(raw any) *DashboardRangeState {
	record, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return &DashboardRangeState{
		Kind:     readString(record, dashboardQueryRangeKindKey),
		Preset:   readString(record, dashboardQueryRangePresetKey),
		From:     readString(record, dashboardQueryRangeFromKey),
		To:       readString(record, dashboardQueryRangeToKey),
		Timezone: readString(record, dashboardQueryRangeTimezoneKey),
	}
}

func readDashboardSorts(raw any) []DashboardSortState {
	records := readDashboardRecords(raw)
	if records == nil {
		return nil
	}
	out := make([]DashboardSortState, 0, len(records))
	for _, m := range records {
		out = append(out, DashboardSortState{
			Field:     readString(m, dashboardQuerySortFieldKey),
			Direction: readString(m, dashboardQuerySortDirKey),
		})
	}
	return out
}

func readDashboardSummary(raw any) *DashboardSummary {
	record, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return &DashboardSummary{
		Headline:           readString(record, dashboardSummaryHeadlineKey),
		Detail:             readString(record, dashboardSummaryDetailKey),
		Status:             readString(record, dashboardSummaryStatusKey),
		TileCount:          readDashboardInt(record[dashboardSummaryTileCountKey]),
		ActiveRoomCount:    readDashboardInt(record[dashboardSummaryActiveRoomCountKey]),
		LastRefreshAt:      readString(record, dashboardSummaryLastRefreshAtKey),
		AcceptedSnapshotID: readString(record, dashboardSummaryAcceptedSnapshotIDKey),
		AcceptedSnapshotAt: readString(record, dashboardSummaryAcceptedSnapshotAtKey),
	}
}

func readDashboardInt(raw any) int {
	switch typed := raw.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func readDashboardBool(raw any) bool {
	value, _ := raw.(bool)
	return value
}

func readDashboardRecords(raw any) []map[string]any {
	switch typed := raw.(type) {
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, record := range typed {
			m, ok := record.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, m)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(typed))
		copy(out, typed)
		return out
	default:
		return nil
	}
}
