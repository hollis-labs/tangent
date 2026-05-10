package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/room"
)

type dashboardInput struct {
	Envelope envelopes.Envelope `json:"envelope"`
}

func (s *Server) handleDashboard(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args dashboardInput,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Envelope.Type != dashboardEnvelopeType {
		return toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf("tangent.dashboard rejects envelope type %q; want %q", args.Envelope.Type, dashboardEnvelopeType),
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
		s.logWorkflowRoomCreated("dashboard", roomID, args.Envelope.ID)
	} else {
		s.logWorkflowRoomReused("dashboard", roomID, args.Envelope.ID)
	}

	phaseState, found, err := s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("load room phase state: %v", err)), nil, nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	snapshot := dashboardSnapshotFromEnvelope(args.Envelope, room.ProjectDashboardState(phaseState))
	if _, saveErr := s.manager.SaveDashboardSnapshot(roomID, snapshot); saveErr != nil {
		return sessionPhaseStateError(roomID, saveErr), nil, nil
	}

	phaseState, found, err = s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("reload room dashboard state: %v", err)), nil, nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	return s.advanceRoomEnvelope(
		ctx,
		roomID,
		buildVisibleDashboardEnvelope(&args.Envelope, room.ProjectDashboardState(phaseState)),
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

	updatedAt := readStringValue(data, "updated_at")
	if updatedAt == "" {
		updatedAt = nowRFC3339()
	}

	return room.DashboardSnapshot{
		DashboardID:    dashboardID,
		Title:          title,
		Tiles:          tiles,
		Layout:         layout,
		SavedLayouts:   savedLayouts,
		ActiveLayoutID: activeLayoutID,
		QueryState:     queryState,
		Summary:        summary,
		UpdatedAt:      updatedAt,
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
	if view.UpdatedAt != "" {
		data["updated_at"] = view.UpdatedAt
	}
	clone.Data = data
	return clone
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

func cloneDashboardSummary(summary *room.DashboardSummary) *room.DashboardSummary {
	if summary == nil {
		return nil
	}
	out := *summary
	return &out
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
