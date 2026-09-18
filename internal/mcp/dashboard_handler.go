package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	envelopes "github.com/hollis-labs/go-envelopes"

	"github.com/hollis-labs/tangent/internal/room"
)

type dashboardInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

type dashboardSubmitDraft struct {
	DashboardID    string                        `json:"dashboard_id"`
	Action         string                        `json:"action"`
	Note           string                        `json:"note,omitempty"`
	Layout         []room.DashboardTilePlacement `json:"layout,omitempty"`
	SavedLayouts   []room.DashboardSavedLayout   `json:"saved_layouts,omitempty"`
	ActiveLayoutID string                        `json:"active_layout_id,omitempty"`
	QueryState     *room.DashboardQueryState     `json:"query_state,omitempty"`
}

type dashboardAcceptedPayload struct {
	DashboardID string `json:"dashboard_id"`
	Outcome     string `json:"outcome"`
	Action      string `json:"action"`
	SnapshotID  string `json:"snapshot_id"`
	AcceptedAt  string `json:"accepted_at"`
	Note        string `json:"note,omitempty"`
}

type dashboardRejectedPayload struct {
	DashboardID string                 `json:"dashboard_id"`
	Outcome     string                 `json:"outcome"`
	Action      string                 `json:"action,omitempty"`
	Errors      []dashboardSubmitError `json:"errors"`
}

type dashboardSubmitError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handleDashboard(
	ctx context.Context,
	args dashboardInput,
) (any, error) {
	if args.Envelope.Type != dashboardEnvelopeType {
		return nil, toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf("tangent.dashboard rejects envelope type %q; want %q", args.Envelope.Type, dashboardEnvelopeType),
		)
	}

	roomID, err := s.resolveWorkflowRoom(ctx, "dashboard", &args.Envelope)
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

	snapshot := dashboardSnapshotFromEnvelope(args.Envelope, room.ProjectDashboardState(phaseState))
	if _, saveErr := s.manager.SaveDashboardSnapshot(roomID, snapshot); saveErr != nil {
		return nil, sessionPhaseStateError(roomID, saveErr)
	}

	phaseState, found, err = s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return nil, toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("reload room dashboard state: %v", err))
	}
	if !found {
		return nil, toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID))
	}

	return s.advanceRoomEnvelope(
		ctx,
		roomID,
		&args.Envelope,
		buildVisibleDashboardEnvelope(&args.Envelope, room.ProjectDashboardState(phaseState)),
		args.Completion,
	)
}

func dashboardSnapshotFromEnvelope(
	env envelopes.Envelope,
	persisted *room.DashboardStateView,
) room.DashboardSnapshot {
	data := env.Data
	dashboardID := readStringValue(data, "dashboard_id")
	if dashboardID == "" && persisted != nil {
		dashboardID = persisted.DashboardID
	}
	reusePersisted := persisted != nil && persisted.DashboardID != "" && persisted.DashboardID == dashboardID

	title := readStringValue(data, "title")
	if reusePersisted && title == "" {
		title = persisted.Title
	}

	tiles := readDashboardTilesValue(data["tiles"])
	if reusePersisted && len(tiles) == 0 {
		tiles = persisted.Tiles
	}

	layout := readDashboardLayoutValue(data["layout"])
	if reusePersisted && len(layout) == 0 {
		layout = persisted.Layout
	}

	savedLayouts := readDashboardSavedLayoutsValue(data["saved_layouts"])
	if reusePersisted && len(savedLayouts) == 0 {
		savedLayouts = persisted.SavedLayouts
	}

	activeLayoutID := readStringValue(data, "active_layout_id")
	if reusePersisted && activeLayoutID == "" {
		activeLayoutID = persisted.ActiveLayoutID
	}

	queryState := readDashboardQueryStateValue(data["query_state"])
	if reusePersisted && queryState == nil {
		queryState = cloneDashboardQueryState(persisted.QueryState)
	}

	summary := readDashboardSummaryValue(data["summary"])
	if reusePersisted && summary == nil {
		summary = cloneDashboardSummary(persisted.Summary)
	}

	snapshotHistory := readDashboardSnapshotHistoryValue(data["snapshot_history"])
	if reusePersisted && len(snapshotHistory) == 0 {
		snapshotHistory = cloneDashboardSnapshotHistory(persisted.SnapshotHistory)
	}

	exportState := readDashboardExportStateValue(data["export_state"])
	if reusePersisted && exportState == nil {
		exportState = cloneDashboardExportState(persisted.ExportState)
	}

	updatedAt := readStringValue(data, "updated_at")
	if updatedAt == "" {
		updatedAt = nowRFC3339()
	}

	return room.DashboardSnapshot{
		DashboardID:     dashboardID,
		Title:           title,
		Tiles:           tiles,
		Layout:          layout,
		SavedLayouts:    savedLayouts,
		ActiveLayoutID:  activeLayoutID,
		QueryState:      queryState,
		Summary:         summary,
		SnapshotHistory: snapshotHistory,
		ExportState:     exportState,
		UpdatedAt:       updatedAt,
	}
}

func buildVisibleDashboardEnvelope(
	env *envelopes.Envelope,
	view *room.DashboardStateView,
) *envelopes.Envelope {
	clone := cloneEnvelopeForDispatch(env)
	if clone.Data == nil {
		clone.Data = map[string]any{}
	}
	if view == nil {
		return clone
	}
	data := cloneAnyMapForDispatch(clone.Data)
	data["dashboard_id"] = view.DashboardID
	data["title"] = view.Title
	data["tiles"] = dashboardTilesAnyForDispatch(view.Tiles)
	data["layout"] = dashboardLayoutAnyForDispatch(view.Layout)
	data["saved_layouts"] = dashboardSavedLayoutsAnyForDispatch(view.SavedLayouts)
	if view.ActiveLayoutID != "" {
		data["active_layout_id"] = view.ActiveLayoutID
	}
	if view.QueryState != nil {
		data["query_state"] = dashboardQueryStateAnyForDispatch(view.QueryState)
	}
	if view.Summary != nil {
		data["summary"] = dashboardSummaryAnyForDispatch(view.Summary)
	}
	data["snapshot_history"] = dashboardSnapshotHistoryAnyForDispatch(view.SnapshotHistory)
	if view.ExportState != nil {
		data["export_state"] = dashboardExportStateAnyForDispatch(view.ExportState)
	}
	if view.UpdatedAt != "" {
		data["updated_at"] = view.UpdatedAt
	}
	clone.Data = data
	return clone
}

func (s *Server) normalizeDashboardSubmitResponse(
	roomID string,
	_ *envelopes.Envelope,
	resp *envelopes.Response,
) (*envelopes.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("dashboard response is required")
	}
	if resp.Kind != envelopes.ResponseKindData {
		return nil, fmt.Errorf("dashboard kind must be %q", envelopes.ResponseKindData)
	}
	if resp.Status != envelopes.ResponseStatusSubmitted {
		return nil, fmt.Errorf("dashboard status must be %q", envelopes.ResponseStatusSubmitted)
	}

	phaseState, found, err := s.manager.GetPhaseState(context.Background(), roomID)
	if err != nil {
		return nil, fmt.Errorf("dashboard submit: load room state: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", room.ErrRoomNotFound, roomID)
	}
	persisted := room.ProjectDashboardState(phaseState)
	if persisted == nil {
		return nil, fmt.Errorf("room %q has no persisted dashboard state", roomID)
	}

	draft, err := decodeDashboardSubmitDraft(resp.Payload)
	if err != nil {
		return dashboardRejectedResponse(resp, persisted.DashboardID, "", "INVALID_PAYLOAD", err.Error()), nil
	}
	if draft.DashboardID != persisted.DashboardID {
		return dashboardRejectedResponse(
			resp,
			persisted.DashboardID,
			draft.Action,
			"INVALID_PAYLOAD",
			fmt.Sprintf("payload.dashboard_id %q does not match room dashboard_id %q", draft.DashboardID, persisted.DashboardID),
		), nil
	}
	if draft.Action != "refresh" && draft.Action != "update" {
		return dashboardRejectedResponse(
			resp,
			persisted.DashboardID,
			draft.Action,
			"INVALID_ACTION",
			fmt.Sprintf("payload.action %q must be refresh or update", draft.Action),
		), nil
	}

	now := nowRFC3339()
	snapshotID := nextDashboardSnapshotID(persisted)
	savedLayouts := cloneDashboardSavedLayouts(persisted.SavedLayouts)
	if draft.SavedLayouts != nil {
		savedLayouts = cloneDashboardSavedLayouts(draft.SavedLayouts)
	}
	activeLayoutID := persisted.ActiveLayoutID
	if draft.ActiveLayoutID != "" || draft.SavedLayouts != nil {
		activeLayoutID = draft.ActiveLayoutID
	}
	layout := cloneDashboardLayout(persisted.Layout)
	if draft.Layout != nil {
		layout = cloneDashboardLayout(draft.Layout)
	} else if activeLayoutID != "" {
		if active := findDashboardSavedLayout(savedLayouts, activeLayoutID); active != nil {
			layout = cloneDashboardLayout(active.Tiles)
		}
	}
	snapshotHistory := append(cloneDashboardSnapshotHistory(persisted.SnapshotHistory), room.DashboardSnapshotMeta{
		SnapshotID:     snapshotID,
		Action:         draft.Action,
		Note:           draft.Note,
		CreatedAt:      now,
		TileCount:      len(persisted.Tiles),
		ActiveLayoutID: activeLayoutID,
	})

	summary := cloneDashboardSummary(persisted.Summary)
	if summary == nil {
		summary = &room.DashboardSummary{}
	}
	summary.TileCount = len(persisted.Tiles)
	summary.AcceptedSnapshotID = snapshotID
	summary.AcceptedSnapshotAt = now
	if draft.Action == "refresh" {
		summary.LastRefreshAt = now
	}

	queryState := cloneDashboardQueryState(persisted.QueryState)
	if draft.QueryState != nil {
		queryState = cloneDashboardQueryState(draft.QueryState)
	}
	exportState := buildDashboardExportState(
		persisted.DashboardID,
		snapshotID,
		now,
		activeLayoutID,
		persisted.Tiles,
	)

	if _, err := s.manager.SaveDashboardSnapshot(roomID, room.DashboardSnapshot{
		DashboardID:     persisted.DashboardID,
		Title:           persisted.Title,
		Tiles:           persisted.Tiles,
		Layout:          layout,
		SavedLayouts:    savedLayouts,
		ActiveLayoutID:  activeLayoutID,
		QueryState:      queryState,
		Summary:         summary,
		SnapshotHistory: snapshotHistory,
		ExportState:     exportState,
		UpdatedAt:       now,
	}); err != nil {
		if isDashboardSnapshotValidationError(err) {
			return dashboardRejectedResponse(resp, persisted.DashboardID, draft.Action, "INVALID_STATE", err.Error()), nil
		}
		return nil, fmt.Errorf("dashboard submit: save room state: %w", err)
	}

	next := cloneDashboardResponse(resp)
	next.Payload = dashboardAcceptedPayload{
		DashboardID: persisted.DashboardID,
		Outcome:     "accepted",
		Action:      draft.Action,
		SnapshotID:  snapshotID,
		AcceptedAt:  now,
		Note:        draft.Note,
	}
	next.CompletedAt = now
	return next, nil
}

func readDashboardTilesValue(raw any) []room.DashboardTile {
	if raw == nil {
		return nil
	}
	var out []room.DashboardTile
	decodeJSONValue(raw, &out)
	return out
}

func readDashboardLayoutValue(raw any) []room.DashboardTilePlacement {
	if raw == nil {
		return nil
	}
	var out []room.DashboardTilePlacement
	decodeJSONValue(raw, &out)
	return out
}

func readDashboardSavedLayoutsValue(raw any) []room.DashboardSavedLayout {
	if raw == nil {
		return nil
	}
	var out []room.DashboardSavedLayout
	decodeJSONValue(raw, &out)
	return out
}

func readDashboardQueryStateValue(raw any) *room.DashboardQueryState {
	if raw == nil {
		return nil
	}
	var out room.DashboardQueryState
	if !decodeJSONValue(raw, &out) {
		return nil
	}
	return &out
}

func readDashboardSummaryValue(raw any) *room.DashboardSummary {
	if raw == nil {
		return nil
	}
	var out room.DashboardSummary
	if !decodeJSONValue(raw, &out) {
		return nil
	}
	return &out
}

func readDashboardSnapshotHistoryValue(raw any) []room.DashboardSnapshotMeta {
	if raw == nil {
		return nil
	}
	var out []room.DashboardSnapshotMeta
	decodeJSONValue(raw, &out)
	return out
}

func readDashboardExportStateValue(raw any) *room.DashboardExportState {
	if raw == nil {
		return nil
	}
	var out room.DashboardExportState
	if !decodeJSONValue(raw, &out) {
		return nil
	}
	return &out
}

func decodeJSONValue(raw any, target any) bool {
	buf, err := json.Marshal(raw)
	if err != nil {
		return false
	}
	return json.Unmarshal(buf, target) == nil
}

func cloneDashboardQueryState(state *room.DashboardQueryState) *room.DashboardQueryState {
	if state == nil {
		return nil
	}
	out := *state
	if len(state.Filters) > 0 {
		out.Filters = append([]room.DashboardFilterState(nil), state.Filters...)
	}
	if len(state.Sort) > 0 {
		out.Sort = append([]room.DashboardSortState(nil), state.Sort...)
	}
	if state.Range != nil {
		rangeCopy := *state.Range
		out.Range = &rangeCopy
	}
	return &out
}

func cloneDashboardLayout(items []room.DashboardTilePlacement) []room.DashboardTilePlacement {
	if items == nil {
		return nil
	}
	if len(items) == 0 {
		return []room.DashboardTilePlacement{}
	}
	out := make([]room.DashboardTilePlacement, len(items))
	copy(out, items)
	return out
}

func cloneDashboardSavedLayouts(items []room.DashboardSavedLayout) []room.DashboardSavedLayout {
	if items == nil {
		return nil
	}
	if len(items) == 0 {
		return []room.DashboardSavedLayout{}
	}
	out := make([]room.DashboardSavedLayout, len(items))
	for i, item := range items {
		out[i] = room.DashboardSavedLayout{
			LayoutID:    item.LayoutID,
			Name:        item.Name,
			Description: item.Description,
			Tiles:       cloneDashboardLayout(item.Tiles),
			IsDefault:   item.IsDefault,
			UpdatedAt:   item.UpdatedAt,
		}
	}
	return out
}

func cloneDashboardSummary(summary *room.DashboardSummary) *room.DashboardSummary {
	if summary == nil {
		return nil
	}
	out := *summary
	return &out
}

func cloneDashboardExportState(state *room.DashboardExportState) *room.DashboardExportState {
	if state == nil {
		return nil
	}
	out := *state
	if state.RoomRefs != nil {
		out.RoomRefs = append([]string(nil), state.RoomRefs...)
	}
	if state.ArtifactRefs != nil {
		out.ArtifactRefs = append([]string(nil), state.ArtifactRefs...)
	}
	return &out
}

func cloneDashboardSnapshotHistory(items []room.DashboardSnapshotMeta) []room.DashboardSnapshotMeta {
	if len(items) == 0 {
		return []room.DashboardSnapshotMeta{}
	}
	out := make([]room.DashboardSnapshotMeta, len(items))
	copy(out, items)
	return out
}

func cloneDashboardResponse(resp *envelopes.Response) *envelopes.Response {
	if resp == nil {
		return nil
	}
	out := *resp
	if resp.Handle != nil {
		handleCopy := *resp.Handle
		out.Handle = &handleCopy
	}
	if resp.Error != nil {
		errorCopy := *resp.Error
		out.Error = &errorCopy
	}
	if resp.Meta != nil {
		out.Meta = cloneAnyMapForDispatch(resp.Meta)
	}
	return &out
}

func decodeDashboardSubmitDraft(raw any) (dashboardSubmitDraft, error) {
	var draft dashboardSubmitDraft
	if !decodeJSONValue(raw, &draft) {
		return dashboardSubmitDraft{}, fmt.Errorf("payload must be an object")
	}
	return dashboardSubmitDraft{
		DashboardID:    readStringValue(map[string]any{"dashboard_id": draft.DashboardID}, "dashboard_id"),
		Action:         readStringValue(map[string]any{"action": draft.Action}, "action"),
		Note:           readStringValue(map[string]any{"note": draft.Note}, "note"),
		Layout:         cloneDashboardLayout(draft.Layout),
		SavedLayouts:   cloneDashboardSavedLayouts(draft.SavedLayouts),
		ActiveLayoutID: readStringValue(map[string]any{"active_layout_id": draft.ActiveLayoutID}, "active_layout_id"),
		QueryState:     cloneDashboardQueryState(draft.QueryState),
	}, nil
}

func findDashboardSavedLayout(
	items []room.DashboardSavedLayout,
	layoutID string,
) *room.DashboardSavedLayout {
	for i := range items {
		if items[i].LayoutID == layoutID {
			return &items[i]
		}
	}
	return nil
}

func isDashboardSnapshotValidationError(err error) bool {
	return errors.Is(err, room.ErrInvalidDashboardID) ||
		errors.Is(err, room.ErrInvalidDashboardTile) ||
		errors.Is(err, room.ErrInvalidDashboardLayout) ||
		errors.Is(err, room.ErrInvalidDashboardSavedLayout) ||
		errors.Is(err, room.ErrInvalidDashboardQueryState) ||
		errors.Is(err, room.ErrInvalidDashboardSummary) ||
		errors.Is(err, room.ErrInvalidDashboardSnapshot) ||
		errors.Is(err, room.ErrInvalidDashboardExport)
}

func dashboardRejectedResponse(
	resp *envelopes.Response,
	dashboardID string,
	action string,
	code string,
	message string,
) *envelopes.Response {
	next := cloneDashboardResponse(resp)
	next.Payload = dashboardRejectedPayload{
		DashboardID: dashboardID,
		Outcome:     "rejected",
		Action:      action,
		Errors: []dashboardSubmitError{{
			Code:    code,
			Message: message,
		}},
	}
	return next
}

func nextDashboardSnapshotID(view *room.DashboardStateView) string {
	count := 0
	if view != nil {
		count = len(view.SnapshotHistory)
	}
	return fmt.Sprintf("%s-snapshot-%03d", view.DashboardID, count+1)
}

func dashboardTilesAnyForDispatch(items []room.DashboardTile) []any {
	if len(items) == 0 {
		return []any{}
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"tile_id":      item.TileID,
			"kind":         item.Kind,
			"title":        item.Title,
			"subtitle":     item.Subtitle,
			"status":       item.Status,
			"value":        item.Value,
			"unit":         item.Unit,
			"summary":      item.Summary,
			"room_id":      item.RoomID,
			"workflow":     item.Workflow,
			"artifact_ref": item.ArtifactRef,
			"metadata":     cloneAnyMapForDispatch(item.Metadata),
		})
	}
	return out
}

func dashboardLayoutAnyForDispatch(items []room.DashboardTilePlacement) []any {
	if len(items) == 0 {
		return []any{}
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"tile_id": item.TileID,
			"x":       item.X,
			"y":       item.Y,
			"w":       item.W,
			"h":       item.H,
		})
	}
	return out
}

func dashboardSavedLayoutsAnyForDispatch(items []room.DashboardSavedLayout) []any {
	if len(items) == 0 {
		return []any{}
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"layout_id":   item.LayoutID,
			"name":        item.Name,
			"description": item.Description,
			"tiles":       dashboardLayoutAnyForDispatch(item.Tiles),
			"is_default":  item.IsDefault,
			"updated_at":  item.UpdatedAt,
		})
	}
	return out
}

func dashboardQueryStateAnyForDispatch(state *room.DashboardQueryState) map[string]any {
	if state == nil {
		return map[string]any{}
	}
	out := map[string]any{
		"search":   state.Search,
		"scope":    state.Scope,
		"group_by": state.GroupBy,
	}
	if len(state.Filters) > 0 {
		filters := make([]any, 0, len(state.Filters))
		for _, filter := range state.Filters {
			filters = append(filters, map[string]any{
				"filter_id": filter.FilterID,
				"label":     filter.Label,
				"operator":  filter.Operator,
				"values":    cloneStringSliceAnyForDispatch(filter.Values),
			})
		}
		out["filters"] = filters
	}
	if state.Range != nil {
		out["range"] = map[string]any{
			"kind":     state.Range.Kind,
			"preset":   state.Range.Preset,
			"from":     state.Range.From,
			"to":       state.Range.To,
			"timezone": state.Range.Timezone,
		}
	}
	if len(state.Sort) > 0 {
		sorts := make([]any, 0, len(state.Sort))
		for _, item := range state.Sort {
			sorts = append(sorts, map[string]any{
				"field":     item.Field,
				"direction": item.Direction,
			})
		}
		out["sort"] = sorts
	}
	return out
}

func dashboardSummaryAnyForDispatch(summary *room.DashboardSummary) map[string]any {
	if summary == nil {
		return map[string]any{}
	}
	return map[string]any{
		"headline":             summary.Headline,
		"detail":               summary.Detail,
		"status":               summary.Status,
		"tile_count":           summary.TileCount,
		"active_room_count":    summary.ActiveRoomCount,
		"last_refresh_at":      summary.LastRefreshAt,
		"accepted_snapshot_id": summary.AcceptedSnapshotID,
		"accepted_snapshot_at": summary.AcceptedSnapshotAt,
	}
}

func dashboardSnapshotHistoryAnyForDispatch(items []room.DashboardSnapshotMeta) []any {
	if len(items) == 0 {
		return []any{}
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"snapshot_id":      item.SnapshotID,
			"action":           item.Action,
			"note":             item.Note,
			"created_at":       item.CreatedAt,
			"tile_count":       item.TileCount,
			"active_layout_id": item.ActiveLayoutID,
		})
	}
	return out
}

func dashboardExportStateAnyForDispatch(state *room.DashboardExportState) map[string]any {
	if state == nil {
		return map[string]any{}
	}
	return map[string]any{
		"export_id":        state.ExportID,
		"snapshot_id":      state.SnapshotID,
		"generated_at":     state.GeneratedAt,
		"active_layout_id": state.ActiveLayoutID,
		"tile_count":       state.TileCount,
		"room_refs":        cloneStringSliceAnyForDispatch(state.RoomRefs),
		"artifact_refs":    cloneStringSliceAnyForDispatch(state.ArtifactRefs),
	}
}

func cloneStringSliceAnyForDispatch(in []string) []any {
	if len(in) == 0 {
		return []any{}
	}
	out := make([]any, len(in))
	for i, item := range in {
		out[i] = item
	}
	return out
}

func buildDashboardExportState(
	dashboardID string,
	snapshotID string,
	generatedAt string,
	activeLayoutID string,
	tiles []room.DashboardTile,
) *room.DashboardExportState {
	return &room.DashboardExportState{
		ExportID:       fmt.Sprintf("%s-export-%s", dashboardID, snapshotID),
		SnapshotID:     snapshotID,
		GeneratedAt:    generatedAt,
		ActiveLayoutID: activeLayoutID,
		TileCount:      len(tiles),
		RoomRefs:       uniqueDashboardStringRefs(tiles, func(tile room.DashboardTile) string { return tile.RoomID }),
		ArtifactRefs: uniqueDashboardStringRefs(tiles, func(tile room.DashboardTile) string {
			return tile.ArtifactRef
		}),
	}
}

func uniqueDashboardStringRefs(
	tiles []room.DashboardTile,
	read func(room.DashboardTile) string,
) []string {
	if len(tiles) == 0 {
		return []string{}
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tiles))
	for _, tile := range tiles {
		value := read(tile)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
