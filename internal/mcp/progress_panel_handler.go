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

type progressPanelInput struct {
	Envelope envelopes.Envelope `json:"envelope"`
}

type progressPanelSubmitDraft struct {
	PanelID         string `json:"panel_id"`
	ItemID          string `json:"item_id"`
	Status          string `json:"status"`
	Summary         string `json:"summary,omitempty"`
	CheckpointLabel string `json:"checkpoint_label,omitempty"`
}

type progressPanelSubmitPayload struct {
	PanelID      string `json:"panel_id"`
	Outcome      string `json:"outcome"`
	ItemID       string `json:"item_id"`
	Status       string `json:"status"`
	UpdateID     string `json:"update_id"`
	CheckpointID string `json:"checkpoint_id,omitempty"`
	Summary      string `json:"summary,omitempty"`
}

func (s *Server) handleProgressPanel(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args progressPanelInput,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Envelope.Type != progressPanelEnvelopeType {
		return toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf("tangent.progress-panel rejects envelope type %q; want %q", args.Envelope.Type, progressPanelEnvelopeType),
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
		s.logWorkflowRoomCreated("progress-panel", roomID, args.Envelope.ID)
	} else {
		s.logWorkflowRoomReused("progress-panel", roomID, args.Envelope.ID)
	}

	phaseState, found, err := s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("load room phase state: %v", err)), nil, nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	snapshot := progressPanelSnapshotFromEnvelope(args.Envelope, room.ProjectProgressPanelState(phaseState))
	if _, saveErr := s.manager.SaveProgressPanelSnapshot(roomID, snapshot); saveErr != nil {
		return sessionPhaseStateError(roomID, saveErr), nil, nil
	}

	phaseState, found, err = s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("reload room progress-panel state: %v", err)), nil, nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	return s.advanceRoomEnvelope(
		ctx,
		roomID,
		buildVisibleProgressPanelEnvelope(&args.Envelope, room.ProjectProgressPanelState(phaseState)),
	)
}

func progressPanelSnapshotFromEnvelope(env envelopes.Envelope, persisted *room.ProgressPanelStateView) room.ProgressPanelSnapshot {
	data := env.Data
	panelID := readStringValue(data, "panel_id")
	if panelID == "" && persisted != nil {
		panelID = persisted.PanelID
	}
	reusePersisted := persisted != nil && persisted.PanelID != "" && persisted.PanelID == panelID

	items := readProgressPanelItemsValue(data["items"])
	if reusePersisted {
		items = persisted.Items
	} else if len(items) == 0 && persisted != nil {
		items = persisted.Items
	}
	updates := readProgressPanelUpdatesValue(data["updates"])
	if reusePersisted {
		updates = persisted.Updates
	} else if len(updates) == 0 && persisted != nil {
		updates = persisted.Updates
	}
	summary := readProgressPanelSummaryValue(data["summary"])
	if reusePersisted && persisted.Summary != nil {
		summary = cloneProgressPanelSummary(persisted.Summary)
	}

	return room.ProgressPanelSnapshot{
		PanelID:   panelID,
		Items:     items,
		Updates:   updates,
		Summary:   summary,
		UpdatedAt: nowRFC3339(),
	}
}

func buildVisibleProgressPanelEnvelope(env *envelopes.Envelope, view *room.ProgressPanelStateView) *envelopes.Envelope {
	clone := cloneEnvelopeForDispatch(env)
	if clone.Data == nil {
		clone.Data = map[string]any{}
	}
	if view == nil {
		return clone
	}
	data := cloneAnyMapForDispatch(clone.Data)
	data["panel_id"] = view.PanelID
	data["items"] = progressPanelItemsAnyForDispatch(view.Items)
	data["updates"] = progressPanelUpdatesAnyForDispatch(view.Updates)
	data["checkpoints"] = progressPanelCheckpointsAnyForDispatch(view.Checkpoints)
	if view.Summary != nil {
		data["summary"] = progressPanelSummaryAnyForDispatch(view.Summary)
	}
	if view.UpdatedAt != "" {
		data["updated_at"] = view.UpdatedAt
	}
	clone.Data = data
	return clone
}

func (s *Server) normalizeProgressPanelSubmitResponse(
	roomID string,
	_ *envelopes.Envelope,
	resp *envelopes.Response,
) (*envelopes.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("progress-panel response is required")
	}
	if resp.Kind != envelopes.ResponseKindData {
		return nil, fmt.Errorf("progress-panel kind must be %q", envelopes.ResponseKindData)
	}
	if resp.Status != envelopes.ResponseStatusSubmitted {
		return nil, fmt.Errorf("progress-panel status must be %q", envelopes.ResponseStatusSubmitted)
	}

	phaseState, found, err := s.manager.GetPhaseState(context.Background(), roomID)
	if err != nil {
		return nil, fmt.Errorf("progress-panel submit: load room state: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", room.ErrRoomNotFound, roomID)
	}
	persisted := room.ProjectProgressPanelState(phaseState)
	if persisted == nil {
		return nil, fmt.Errorf("room %q has no persisted progress-panel state", roomID)
	}

	draft, err := decodeProgressPanelSubmitDraft(resp.Payload)
	if err != nil {
		return nil, err
	}
	if draft.PanelID != persisted.PanelID {
		return nil, fmt.Errorf("payload.panel_id %q does not match room panel_id %q", draft.PanelID, persisted.PanelID)
	}

	items, index := cloneProgressPanelItems(persisted.Items), -1
	for i, item := range items {
		if item.ItemID == draft.ItemID {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, fmt.Errorf("%w: unknown item_id %q", room.ErrInvalidProgressPanelUpdate, draft.ItemID)
	}

	now := nowRFC3339()
	items[index].Status = draft.Status
	items[index].UpdatedAt = now
	items[index].Detail = draft.Summary
	if isProgressTerminalStatus(draft.Status) {
		items[index].CompletedAt = now
	}

	updates := cloneProgressPanelUpdates(persisted.Updates)
	statusUpdateID := nextProgressPanelUpdateID(persisted)
	updates = append(updates, room.ProgressPanelUpdate{
		UpdateID:  statusUpdateID,
		Kind:      "status",
		ItemID:    draft.ItemID,
		Status:    draft.Status,
		Summary:   draft.Summary,
		CreatedAt: now,
		Metadata:  map[string]any{},
	})
	checkpointID := ""
	if strings.TrimSpace(draft.CheckpointLabel) != "" {
		checkpointID = nextProgressPanelCheckpointID(persisted)
		updates = append(updates, room.ProgressPanelUpdate{
			UpdateID:        nextProgressPanelUpdateIDFromCount(persisted.PanelID, len(updates)+1),
			Kind:            "checkpoint",
			ItemID:          draft.ItemID,
			Summary:         draft.Summary,
			CreatedAt:       now,
			CheckpointID:    checkpointID,
			CheckpointLabel: draft.CheckpointLabel,
			Metadata:        map[string]any{},
		})
	}

	summary := cloneProgressPanelSummary(persisted.Summary)
	if summary == nil {
		summary = &room.ProgressPanelSummary{}
	}
	summary.CurrentStatus = draft.Status
	summary.Detail = draft.Summary
	summary.LastUpdateID = statusUpdateID
	summary.LastCheckpointID = checkpointID
	summary.Headline = buildProgressPanelHeadline(items)
	if isProgressTerminalStatus(draft.Status) {
		summary.CompletedAt = now
	}

	snapshot := room.ProgressPanelSnapshot{
		PanelID:   persisted.PanelID,
		Items:     items,
		Updates:   updates,
		Summary:   summary,
		UpdatedAt: now,
	}
	if _, err := s.manager.SaveProgressPanelSnapshot(roomID, snapshot); err != nil {
		return nil, err
	}

	normalized := &envelopes.Response{
		V:           envelopes.ProtocolVersion,
		EnvelopeID:  resp.EnvelopeID,
		Kind:        envelopes.ResponseKindData,
		Status:      envelopes.ResponseStatusSubmitted,
		CompletedAt: resp.CompletedAt,
		Payload: progressPanelSubmitPayload{
			PanelID:      draft.PanelID,
			Outcome:      "accepted",
			ItemID:       draft.ItemID,
			Status:       draft.Status,
			UpdateID:     statusUpdateID,
			CheckpointID: checkpointID,
			Summary:      draft.Summary,
		},
	}
	if normalized.CompletedAt == "" {
		normalized.CompletedAt = now
	}
	return normalized, nil
}

func decodeProgressPanelSubmitDraft(payload any) (progressPanelSubmitDraft, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return progressPanelSubmitDraft{}, err
	}
	var draft progressPanelSubmitDraft
	if err := json.Unmarshal(raw, &draft); err != nil {
		return progressPanelSubmitDraft{}, err
	}
	draft.PanelID = strings.TrimSpace(draft.PanelID)
	draft.ItemID = strings.TrimSpace(draft.ItemID)
	draft.Status = strings.TrimSpace(draft.Status)
	draft.Summary = strings.TrimSpace(draft.Summary)
	draft.CheckpointLabel = strings.TrimSpace(draft.CheckpointLabel)
	if draft.PanelID == "" || draft.ItemID == "" || draft.Status == "" {
		return progressPanelSubmitDraft{}, fmt.Errorf("payload.panel_id, payload.item_id, and payload.status are required")
	}
	return draft, nil
}

func cloneProgressPanelItems(items []room.ProgressPanelItem) []room.ProgressPanelItem {
	if len(items) == 0 {
		return []room.ProgressPanelItem{}
	}
	out := make([]room.ProgressPanelItem, 0, len(items))
	for _, item := range items {
		out = append(out, room.ProgressPanelItem{
			ItemID:      item.ItemID,
			Label:       item.Label,
			Status:      item.Status,
			Detail:      item.Detail,
			CreatedAt:   item.CreatedAt,
			UpdatedAt:   item.UpdatedAt,
			CompletedAt: item.CompletedAt,
			Metadata:    cloneAnyMapForDispatch(item.Metadata),
		})
	}
	return out
}

func cloneProgressPanelUpdates(updates []room.ProgressPanelUpdate) []room.ProgressPanelUpdate {
	if len(updates) == 0 {
		return []room.ProgressPanelUpdate{}
	}
	out := make([]room.ProgressPanelUpdate, 0, len(updates))
	for _, update := range updates {
		out = append(out, room.ProgressPanelUpdate{
			UpdateID:        update.UpdateID,
			Kind:            update.Kind,
			ItemID:          update.ItemID,
			Status:          update.Status,
			Summary:         update.Summary,
			CreatedAt:       update.CreatedAt,
			CheckpointID:    update.CheckpointID,
			CheckpointLabel: update.CheckpointLabel,
			Metadata:        cloneAnyMapForDispatch(update.Metadata),
		})
	}
	return out
}

func cloneProgressPanelSummary(summary *room.ProgressPanelSummary) *room.ProgressPanelSummary {
	if summary == nil {
		return nil
	}
	return &room.ProgressPanelSummary{
		CurrentStatus:    summary.CurrentStatus,
		Headline:         summary.Headline,
		Detail:           summary.Detail,
		LastUpdateID:     summary.LastUpdateID,
		LastCheckpointID: summary.LastCheckpointID,
		CompletedAt:      summary.CompletedAt,
	}
}

func nextProgressPanelUpdateID(view *room.ProgressPanelStateView) string {
	return nextProgressPanelUpdateIDFromCount(view.PanelID, len(view.Updates)+1)
}

func nextProgressPanelUpdateIDFromCount(panelID string, count int) string {
	return fmt.Sprintf("%s-update-%03d", panelID, count)
}

func nextProgressPanelCheckpointID(view *room.ProgressPanelStateView) string {
	count := 0
	for _, update := range view.Updates {
		if update.Kind == "checkpoint" {
			count++
		}
	}
	return fmt.Sprintf("%s-checkpoint-%03d", view.PanelID, count+1)
}

func buildProgressPanelHeadline(items []room.ProgressPanelItem) string {
	counts := map[string]int{}
	for _, item := range items {
		counts[item.Status]++
	}
	switch {
	case counts["running"] > 0:
		return fmt.Sprintf("%d running", counts["running"])
	case counts["blocked"] > 0:
		return fmt.Sprintf("%d blocked", counts["blocked"])
	case counts["queued"] > 0:
		return fmt.Sprintf("%d queued", counts["queued"])
	case counts["completed"] == len(items) && len(items) > 0:
		return "All items completed"
	default:
		return fmt.Sprintf("%d items tracked", len(items))
	}
}

func isProgressTerminalStatus(status string) bool {
	switch status {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func readProgressPanelItemsValue(raw any) []room.ProgressPanelItem {
	records := readObjectSliceValue(raw)
	if len(records) == 0 {
		return []room.ProgressPanelItem{}
	}
	out := make([]room.ProgressPanelItem, 0, len(records))
	for _, record := range records {
		out = append(out, room.ProgressPanelItem{
			ItemID:      readStringValue(record, "item_id"),
			Label:       readStringValue(record, "label"),
			Status:      readStringValue(record, "status"),
			Detail:      readStringValue(record, "detail"),
			CreatedAt:   readStringValue(record, "created_at"),
			UpdatedAt:   readStringValue(record, "updated_at"),
			CompletedAt: readStringValue(record, "completed_at"),
			Metadata:    readObjectValue(record, "metadata"),
		})
	}
	return out
}

func readProgressPanelUpdatesValue(raw any) []room.ProgressPanelUpdate {
	records := readObjectSliceValue(raw)
	if len(records) == 0 {
		return []room.ProgressPanelUpdate{}
	}
	out := make([]room.ProgressPanelUpdate, 0, len(records))
	for _, record := range records {
		out = append(out, room.ProgressPanelUpdate{
			UpdateID:        readStringValue(record, "update_id"),
			Kind:            readStringValue(record, "kind"),
			ItemID:          readStringValue(record, "item_id"),
			Status:          readStringValue(record, "status"),
			Summary:         readStringValue(record, "summary"),
			CreatedAt:       readStringValue(record, "created_at"),
			CheckpointID:    readStringValue(record, "checkpoint_id"),
			CheckpointLabel: readStringValue(record, "checkpoint_label"),
			Metadata:        readObjectValue(record, "metadata"),
		})
	}
	return out
}

func readProgressPanelSummaryValue(raw any) *room.ProgressPanelSummary {
	record, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return &room.ProgressPanelSummary{
		CurrentStatus:    readStringValue(record, "current_status"),
		Headline:         readStringValue(record, "headline"),
		Detail:           readStringValue(record, "detail"),
		LastUpdateID:     readStringValue(record, "last_update_id"),
		LastCheckpointID: readStringValue(record, "last_checkpoint_id"),
		CompletedAt:      readStringValue(record, "completed_at"),
	}
}

func progressPanelItemsAnyForDispatch(items []room.ProgressPanelItem) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"item_id":      item.ItemID,
			"label":        item.Label,
			"status":       item.Status,
			"detail":       item.Detail,
			"created_at":   item.CreatedAt,
			"updated_at":   item.UpdatedAt,
			"completed_at": item.CompletedAt,
			"metadata":     cloneAnyMapForDispatch(item.Metadata),
		})
	}
	return out
}

func progressPanelUpdatesAnyForDispatch(updates []room.ProgressPanelUpdate) []map[string]any {
	out := make([]map[string]any, 0, len(updates))
	for _, update := range updates {
		out = append(out, map[string]any{
			"update_id":        update.UpdateID,
			"kind":             update.Kind,
			"item_id":          update.ItemID,
			"status":           update.Status,
			"summary":          update.Summary,
			"created_at":       update.CreatedAt,
			"checkpoint_id":    update.CheckpointID,
			"checkpoint_label": update.CheckpointLabel,
			"metadata":         cloneAnyMapForDispatch(update.Metadata),
		})
	}
	return out
}

func progressPanelCheckpointsAnyForDispatch(checkpoints []room.ProgressPanelCheckpoint) []map[string]any {
	out := make([]map[string]any, 0, len(checkpoints))
	for _, checkpoint := range checkpoints {
		out = append(out, map[string]any{
			"update_id":     checkpoint.UpdateID,
			"item_id":       checkpoint.ItemID,
			"checkpoint_id": checkpoint.CheckpointID,
			"label":         checkpoint.Label,
			"summary":       checkpoint.Summary,
			"created_at":    checkpoint.CreatedAt,
		})
	}
	return out
}

func progressPanelSummaryAnyForDispatch(summary *room.ProgressPanelSummary) map[string]any {
	if summary == nil {
		return nil
	}
	return map[string]any{
		"current_status":     summary.CurrentStatus,
		"headline":           summary.Headline,
		"detail":             summary.Detail,
		"last_update_id":     summary.LastUpdateID,
		"last_checkpoint_id": summary.LastCheckpointID,
		"completed_at":       summary.CompletedAt,
	}
}
