package room

import (
	"fmt"
	"strings"
)

const (
	ProgressPanelPhaseID                    = "progress_panel"
	progressPanelIDKey                      = "panel_id"
	progressPanelItemsKey                   = "items"
	progressPanelUpdatesKey                 = "updates"
	progressPanelSummaryKey                 = "summary"
	progressPanelUpdatedAtKey               = "updated_at"
	progressPanelItemIDKey                  = "item_id"
	progressPanelItemLabelKey               = "label"
	progressPanelItemStatusKey              = "status"
	progressPanelItemDetailKey              = "detail"
	progressPanelItemCreatedAtKey           = "created_at"
	progressPanelItemUpdatedAtKey           = "updated_at"
	progressPanelItemCompletedAtKey         = "completed_at"
	progressPanelItemMetadataKey            = "metadata"
	progressPanelUpdateIDKey                = "update_id"
	progressPanelUpdateKindKey              = "kind"
	progressPanelUpdateItemIDKey            = "item_id"
	progressPanelUpdateStatusKey            = "status"
	progressPanelUpdateSummaryKey           = "summary"
	progressPanelUpdateCreatedAtKey         = "created_at"
	progressPanelUpdateCheckpointIDKey      = "checkpoint_id"
	progressPanelUpdateCheckpointLabelKey   = "checkpoint_label"
	progressPanelUpdateMetadataKey          = "metadata"
	progressPanelSummaryStatusKey           = "current_status"
	progressPanelSummaryHeadlineKey         = "headline"
	progressPanelSummaryDetailKey           = "detail"
	progressPanelSummaryLastUpdateKey       = "last_update_id"
	progressPanelSummaryCheckpointKey       = "last_checkpoint_id"
	progressPanelSummaryCheckpointLabelKey  = "last_checkpoint_label"
	progressPanelSummaryCompletedKey        = "completed_at"
	progressPanelSummaryCompletionResultKey = "completion_result"
	progressPanelUpdateKindStatus           = "status"
	progressPanelUpdateKindCheckpoint       = "checkpoint"
	progressPanelUpdateKindSummary          = "summary"
)

var allowedProgressStatuses = map[string]struct{}{
	"queued":    {},
	"running":   {},
	"paused":    {},
	"blocked":   {},
	"completed": {},
	"failed":    {},
	"cancelled": {},
}

type ProgressPanelItem struct {
	ItemID      string         `json:"item_id"`
	Label       string         `json:"label"`
	Status      string         `json:"status"`
	Detail      string         `json:"detail,omitempty"`
	CreatedAt   string         `json:"created_at,omitempty"`
	UpdatedAt   string         `json:"updated_at,omitempty"`
	CompletedAt string         `json:"completed_at,omitempty"`
	Metadata    map[string]any `json:"metadata"`
}

type ProgressPanelUpdate struct {
	UpdateID        string         `json:"update_id"`
	Kind            string         `json:"kind"`
	ItemID          string         `json:"item_id,omitempty"`
	Status          string         `json:"status,omitempty"`
	Summary         string         `json:"summary,omitempty"`
	CreatedAt       string         `json:"created_at,omitempty"`
	CheckpointID    string         `json:"checkpoint_id,omitempty"`
	CheckpointLabel string         `json:"checkpoint_label,omitempty"`
	Metadata        map[string]any `json:"metadata"`
}

type ProgressPanelCheckpoint struct {
	UpdateID     string `json:"update_id"`
	ItemID       string `json:"item_id,omitempty"`
	CheckpointID string `json:"checkpoint_id"`
	Label        string `json:"label"`
	Summary      string `json:"summary,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
}

type ProgressPanelSummary struct {
	CurrentStatus       string `json:"current_status,omitempty"`
	Headline            string `json:"headline,omitempty"`
	Detail              string `json:"detail,omitempty"`
	LastUpdateID        string `json:"last_update_id,omitempty"`
	LastCheckpointID    string `json:"last_checkpoint_id,omitempty"`
	LastCheckpointLabel string `json:"last_checkpoint_label,omitempty"`
	CompletedAt         string `json:"completed_at,omitempty"`
	CompletionResult    string `json:"completion_result,omitempty"`
}

type ProgressPanelStateView struct {
	PanelID     string                    `json:"panel_id"`
	Items       []ProgressPanelItem       `json:"items"`
	Updates     []ProgressPanelUpdate     `json:"updates"`
	Checkpoints []ProgressPanelCheckpoint `json:"checkpoints"`
	Summary     *ProgressPanelSummary     `json:"summary,omitempty"`
	UpdatedAt   string                    `json:"updated_at,omitempty"`
}

type ProgressPanelSnapshot struct {
	PanelID   string
	Items     []ProgressPanelItem
	Updates   []ProgressPanelUpdate
	Summary   *ProgressPanelSummary
	UpdatedAt string
}

func (r *Room) SaveProgressPanelSnapshot(snapshot ProgressPanelSnapshot) error {
	normalized, err := normalizeProgressPanelSnapshot(snapshot)
	if err != nil {
		return err
	}

	r.phaseMu.Lock()
	defer r.phaseMu.Unlock()

	nextOutputs := clonePhaseOutputs(r.phaseOutputs)
	nextOutputs[ProgressPanelPhaseID] = progressPanelBlobFromSnapshot(normalized)
	nextVisited := cloneStringSlice(r.phasesVisited)
	if err := r.persistPhaseState(r.currentPhase, nextVisited, nextOutputs); err != nil {
		return err
	}

	r.phasesVisited = nextVisited
	r.phaseOutputs = nextOutputs
	return nil
}

func (m *Manager) SaveProgressPanelSnapshot(roomID string, snapshot ProgressPanelSnapshot) (PhaseState, error) {
	rm, ok := m.Get(roomID)
	if !ok {
		return PhaseState{}, fmt.Errorf("%w: %s", ErrRoomNotFound, roomID)
	}
	if err := rm.SaveProgressPanelSnapshot(snapshot); err != nil {
		return PhaseState{}, err
	}
	return rm.PhaseState(), nil
}

func ProjectProgressPanelState(state PhaseState) *ProgressPanelStateView {
	return projectProgressPanelStateFromBlob(state.PhaseOutputs[ProgressPanelPhaseID])
}

func projectProgressPanelStateFromBlob(blob PhaseOutput) *ProgressPanelStateView {
	if len(blob.Data) == 0 {
		return nil
	}
	panelID := readString(blob.Data, progressPanelIDKey)
	if panelID == "" {
		return nil
	}
	view := &ProgressPanelStateView{
		PanelID:   panelID,
		Items:     readProgressPanelItems(blob.Data[progressPanelItemsKey]),
		Updates:   readProgressPanelUpdates(blob.Data[progressPanelUpdatesKey]),
		Summary:   readProgressPanelSummary(blob.Data[progressPanelSummaryKey]),
		UpdatedAt: readString(blob.Data, progressPanelUpdatedAtKey),
	}
	if view.Items == nil {
		view.Items = []ProgressPanelItem{}
	}
	if view.Updates == nil {
		view.Updates = []ProgressPanelUpdate{}
	}
	view.Checkpoints = progressPanelCheckpointsFromUpdates(view.Updates)
	if view.Checkpoints == nil {
		view.Checkpoints = []ProgressPanelCheckpoint{}
	}
	return view
}

func normalizeProgressPanelSnapshot(snapshot ProgressPanelSnapshot) (ProgressPanelSnapshot, error) {
	panelID := strings.TrimSpace(snapshot.PanelID)
	if panelID == "" {
		return ProgressPanelSnapshot{}, ErrInvalidProgressPanelID
	}
	items, itemIDs, err := normalizeProgressPanelItems(snapshot.Items)
	if err != nil {
		return ProgressPanelSnapshot{}, err
	}
	updates, err := normalizeProgressPanelUpdates(snapshot.Updates, itemIDs)
	if err != nil {
		return ProgressPanelSnapshot{}, err
	}
	summary, err := normalizeProgressPanelSummary(snapshot.Summary)
	if err != nil {
		return ProgressPanelSnapshot{}, err
	}
	return ProgressPanelSnapshot{
		PanelID:   panelID,
		Items:     items,
		Updates:   updates,
		Summary:   summary,
		UpdatedAt: strings.TrimSpace(snapshot.UpdatedAt),
	}, nil
}

func normalizeProgressPanelItems(items []ProgressPanelItem) ([]ProgressPanelItem, map[string]struct{}, error) {
	if len(items) == 0 {
		return []ProgressPanelItem{}, map[string]struct{}{}, nil
	}
	out := make([]ProgressPanelItem, 0, len(items))
	itemIDs := make(map[string]struct{}, len(items))
	for _, item := range items {
		itemID := strings.TrimSpace(item.ItemID)
		label := strings.TrimSpace(item.Label)
		status := strings.TrimSpace(item.Status)
		if itemID == "" || label == "" || !isAllowedProgressStatus(status) {
			return nil, nil, ErrInvalidProgressPanelItem
		}
		if _, exists := itemIDs[itemID]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate item_id %q", ErrInvalidProgressPanelItem, itemID)
		}
		metadata, err := normalizeObjectMap(item.Metadata, ErrInvalidProgressPanelItem)
		if err != nil {
			return nil, nil, err
		}
		itemIDs[itemID] = struct{}{}
		out = append(out, ProgressPanelItem{
			ItemID:      itemID,
			Label:       label,
			Status:      status,
			Detail:      strings.TrimSpace(item.Detail),
			CreatedAt:   strings.TrimSpace(item.CreatedAt),
			UpdatedAt:   strings.TrimSpace(item.UpdatedAt),
			CompletedAt: strings.TrimSpace(item.CompletedAt),
			Metadata:    metadata,
		})
	}
	return out, itemIDs, nil
}

func normalizeProgressPanelUpdates(updates []ProgressPanelUpdate, itemIDs map[string]struct{}) ([]ProgressPanelUpdate, error) {
	if len(updates) == 0 {
		return []ProgressPanelUpdate{}, nil
	}
	out := make([]ProgressPanelUpdate, 0, len(updates))
	updateIDs := make(map[string]struct{}, len(updates))
	for _, update := range updates {
		updateID := strings.TrimSpace(update.UpdateID)
		kind := strings.TrimSpace(update.Kind)
		itemID := strings.TrimSpace(update.ItemID)
		if updateID == "" || !isAllowedProgressUpdateKind(kind) {
			return nil, ErrInvalidProgressPanelUpdate
		}
		if _, exists := updateIDs[updateID]; exists {
			return nil, fmt.Errorf("%w: duplicate update_id %q", ErrInvalidProgressPanelUpdate, updateID)
		}
		metadata, err := normalizeObjectMap(update.Metadata, ErrInvalidProgressPanelUpdate)
		if err != nil {
			return nil, err
		}
		switch kind {
		case progressPanelUpdateKindStatus:
			if itemID == "" || !isAllowedProgressStatus(strings.TrimSpace(update.Status)) {
				return nil, ErrInvalidProgressPanelUpdate
			}
		case progressPanelUpdateKindCheckpoint:
			if itemID == "" || strings.TrimSpace(update.CheckpointID) == "" || strings.TrimSpace(update.CheckpointLabel) == "" {
				return nil, ErrInvalidProgressPanelUpdate
			}
		case progressPanelUpdateKindSummary:
			if strings.TrimSpace(update.Summary) == "" {
				return nil, ErrInvalidProgressPanelUpdate
			}
		}
		if itemID != "" {
			if _, ok := itemIDs[itemID]; !ok {
				return nil, fmt.Errorf("%w: unknown item_id %q", ErrInvalidProgressPanelUpdate, itemID)
			}
		}
		updateIDs[updateID] = struct{}{}
		out = append(out, ProgressPanelUpdate{
			UpdateID:        updateID,
			Kind:            kind,
			ItemID:          itemID,
			Status:          strings.TrimSpace(update.Status),
			Summary:         strings.TrimSpace(update.Summary),
			CreatedAt:       strings.TrimSpace(update.CreatedAt),
			CheckpointID:    strings.TrimSpace(update.CheckpointID),
			CheckpointLabel: strings.TrimSpace(update.CheckpointLabel),
			Metadata:        metadata,
		})
	}
	return out, nil
}

func normalizeProgressPanelSummary(summary *ProgressPanelSummary) (*ProgressPanelSummary, error) {
	if summary == nil {
		return nil, nil
	}
	currentStatus := strings.TrimSpace(summary.CurrentStatus)
	if currentStatus != "" && !isAllowedProgressStatus(currentStatus) {
		return nil, ErrInvalidProgressPanelSummary
	}
	return &ProgressPanelSummary{
		CurrentStatus:       currentStatus,
		Headline:            strings.TrimSpace(summary.Headline),
		Detail:              strings.TrimSpace(summary.Detail),
		LastUpdateID:        strings.TrimSpace(summary.LastUpdateID),
		LastCheckpointID:    strings.TrimSpace(summary.LastCheckpointID),
		LastCheckpointLabel: strings.TrimSpace(summary.LastCheckpointLabel),
		CompletedAt:         strings.TrimSpace(summary.CompletedAt),
		CompletionResult:    strings.TrimSpace(summary.CompletionResult),
	}, nil
}

func progressPanelBlobFromSnapshot(snapshot ProgressPanelSnapshot) PhaseOutput {
	record := map[string]any{
		progressPanelIDKey:        snapshot.PanelID,
		progressPanelItemsKey:     progressPanelItemsAny(snapshot.Items),
		progressPanelUpdatesKey:   progressPanelUpdatesAny(snapshot.Updates),
		progressPanelUpdatedAtKey: snapshot.UpdatedAt,
	}
	if summary := progressPanelSummaryAny(snapshot.Summary); len(summary) > 0 {
		record[progressPanelSummaryKey] = summary
	}
	return PhaseOutput{Version: phaseOutputVersion, Data: record}
}

func progressPanelItemsAny(items []ProgressPanelItem) []map[string]any {
	if len(items) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			progressPanelItemIDKey:          item.ItemID,
			progressPanelItemLabelKey:       item.Label,
			progressPanelItemStatusKey:      item.Status,
			progressPanelItemDetailKey:      item.Detail,
			progressPanelItemCreatedAtKey:   item.CreatedAt,
			progressPanelItemUpdatedAtKey:   item.UpdatedAt,
			progressPanelItemCompletedAtKey: item.CompletedAt,
			progressPanelItemMetadataKey:    cloneAnyMap(item.Metadata),
		}
		out = append(out, record)
	}
	return out
}

func progressPanelUpdatesAny(updates []ProgressPanelUpdate) []map[string]any {
	if len(updates) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(updates))
	for _, update := range updates {
		record := map[string]any{
			progressPanelUpdateIDKey:              update.UpdateID,
			progressPanelUpdateKindKey:            update.Kind,
			progressPanelUpdateItemIDKey:          update.ItemID,
			progressPanelUpdateStatusKey:          update.Status,
			progressPanelUpdateSummaryKey:         update.Summary,
			progressPanelUpdateCreatedAtKey:       update.CreatedAt,
			progressPanelUpdateCheckpointIDKey:    update.CheckpointID,
			progressPanelUpdateCheckpointLabelKey: update.CheckpointLabel,
			progressPanelUpdateMetadataKey:        cloneAnyMap(update.Metadata),
		}
		out = append(out, record)
	}
	return out
}

func progressPanelSummaryAny(summary *ProgressPanelSummary) map[string]any {
	if summary == nil {
		return nil
	}
	return map[string]any{
		progressPanelSummaryStatusKey:           summary.CurrentStatus,
		progressPanelSummaryHeadlineKey:         summary.Headline,
		progressPanelSummaryDetailKey:           summary.Detail,
		progressPanelSummaryLastUpdateKey:       summary.LastUpdateID,
		progressPanelSummaryCheckpointKey:       summary.LastCheckpointID,
		progressPanelSummaryCheckpointLabelKey:  summary.LastCheckpointLabel,
		progressPanelSummaryCompletedKey:        summary.CompletedAt,
		progressPanelSummaryCompletionResultKey: summary.CompletionResult,
	}
}

func readProgressPanelItems(raw any) []ProgressPanelItem {
	records := readObjectSlice(raw)
	if len(records) == 0 {
		return []ProgressPanelItem{}
	}
	out := make([]ProgressPanelItem, 0, len(records))
	for _, record := range records {
		out = append(out, ProgressPanelItem{
			ItemID:      readString(record, progressPanelItemIDKey),
			Label:       readString(record, progressPanelItemLabelKey),
			Status:      readString(record, progressPanelItemStatusKey),
			Detail:      readString(record, progressPanelItemDetailKey),
			CreatedAt:   readString(record, progressPanelItemCreatedAtKey),
			UpdatedAt:   readString(record, progressPanelItemUpdatedAtKey),
			CompletedAt: readString(record, progressPanelItemCompletedAtKey),
			Metadata:    readObjectMapValue(record[progressPanelItemMetadataKey]),
		})
	}
	return out
}

func readProgressPanelUpdates(raw any) []ProgressPanelUpdate {
	records := readObjectSlice(raw)
	if len(records) == 0 {
		return []ProgressPanelUpdate{}
	}
	out := make([]ProgressPanelUpdate, 0, len(records))
	for _, record := range records {
		out = append(out, ProgressPanelUpdate{
			UpdateID:        readString(record, progressPanelUpdateIDKey),
			Kind:            readString(record, progressPanelUpdateKindKey),
			ItemID:          readString(record, progressPanelUpdateItemIDKey),
			Status:          readString(record, progressPanelUpdateStatusKey),
			Summary:         readString(record, progressPanelUpdateSummaryKey),
			CreatedAt:       readString(record, progressPanelUpdateCreatedAtKey),
			CheckpointID:    readString(record, progressPanelUpdateCheckpointIDKey),
			CheckpointLabel: readString(record, progressPanelUpdateCheckpointLabelKey),
			Metadata:        readObjectMapValue(record[progressPanelUpdateMetadataKey]),
		})
	}
	return out
}

func readProgressPanelSummary(raw any) *ProgressPanelSummary {
	record := readObjectMapValue(raw)
	if len(record) == 0 {
		return nil
	}
	return &ProgressPanelSummary{
		CurrentStatus:       readString(record, progressPanelSummaryStatusKey),
		Headline:            readString(record, progressPanelSummaryHeadlineKey),
		Detail:              readString(record, progressPanelSummaryDetailKey),
		LastUpdateID:        readString(record, progressPanelSummaryLastUpdateKey),
		LastCheckpointID:    readString(record, progressPanelSummaryCheckpointKey),
		LastCheckpointLabel: readString(record, progressPanelSummaryCheckpointLabelKey),
		CompletedAt:         readString(record, progressPanelSummaryCompletedKey),
		CompletionResult:    readString(record, progressPanelSummaryCompletionResultKey),
	}
}

func progressPanelCheckpointsFromUpdates(updates []ProgressPanelUpdate) []ProgressPanelCheckpoint {
	if len(updates) == 0 {
		return []ProgressPanelCheckpoint{}
	}
	out := make([]ProgressPanelCheckpoint, 0, len(updates))
	for _, update := range updates {
		if update.Kind != progressPanelUpdateKindCheckpoint {
			continue
		}
		out = append(out, ProgressPanelCheckpoint{
			UpdateID:     update.UpdateID,
			ItemID:       update.ItemID,
			CheckpointID: update.CheckpointID,
			Label:        update.CheckpointLabel,
			Summary:      update.Summary,
			CreatedAt:    update.CreatedAt,
		})
	}
	return out
}

func isAllowedProgressStatus(status string) bool {
	_, ok := allowedProgressStatuses[status]
	return ok
}

func isAllowedProgressUpdateKind(kind string) bool {
	switch kind {
	case progressPanelUpdateKindStatus, progressPanelUpdateKindCheckpoint, progressPanelUpdateKindSummary:
		return true
	default:
		return false
	}
}
