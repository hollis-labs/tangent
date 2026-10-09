package mcp

import (
	"encoding/json"
	"testing"

	envelopes "github.com/hollis-labs/libs/ui-go/envelopes"

	"github.com/hollis-labs/tangent/internal/room"
)

func TestNormalizeProgressPanelSubmitResponseClearsCompletionMetadataOnNonTerminalUpdate(t *testing.T) {
	manager := room.NewManager(nil)
	rm := manager.Create(nil)
	if err := rm.SaveProgressPanelSnapshot(room.ProgressPanelSnapshot{
		PanelID: "panel-complete",
		Items: []room.ProgressPanelItem{
			{
				ItemID:      "item-1",
				Label:       "Write summary",
				Status:      "completed",
				Detail:      "Done.",
				CreatedAt:   "2026-05-10T00:00:00Z",
				UpdatedAt:   "2026-05-10T00:00:00Z",
				CompletedAt: "2026-05-10T00:00:00Z",
				Metadata:    map[string]any{},
			},
		},
		Updates: []room.ProgressPanelUpdate{
			{
				UpdateID:  "panel-complete-update-001",
				Kind:      "status",
				ItemID:    "item-1",
				Status:    "completed",
				Summary:   "Done.",
				CreatedAt: "2026-05-10T00:00:00Z",
				Metadata:  map[string]any{},
			},
		},
		Summary: &room.ProgressPanelSummary{
			CurrentStatus:    "completed",
			Headline:         "1 completed",
			Detail:           "Done.",
			LastUpdateID:     "panel-complete-update-001",
			CompletedAt:      "2026-05-10T00:00:00Z",
			CompletionResult: "completed",
		},
		UpdatedAt: "2026-05-10T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed progress-panel snapshot: %v", err)
	}

	srv := &Server{manager: manager}
	resp := &envelopes.Response{
		V:          envelopes.ProtocolVersion,
		EnvelopeID: "progress-complete-2",
		Kind:       envelopes.ResponseKindData,
		Status:     envelopes.ResponseStatusSubmitted,
		Payload: map[string]any{
			"panel_id": "panel-complete",
			"item_id":  "item-1",
			"status":   "running",
			"summary":  "Reopened for follow-up.",
		},
	}

	normalized, err := srv.normalizeProgressPanelSubmitResponse(rm.ID, nil, resp)
	if err != nil {
		t.Fatalf("normalizeProgressPanelSubmitResponse: %v", err)
	}
	if normalized == nil {
		t.Fatal("normalized response is nil")
	}

	state, found, err := manager.GetPhaseState(t.Context(), rm.ID)
	if err != nil {
		t.Fatalf("GetPhaseState: %v", err)
	}
	if !found {
		t.Fatalf("room %q not found", rm.ID)
	}
	view := room.ProjectProgressPanelState(state)
	if view == nil {
		t.Fatal("progress_panel view is nil")
	}
	if len(view.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(view.Items))
	}
	if got := view.Items[0].Status; got != "running" {
		t.Fatalf("item status = %q, want running", got)
	}
	if got := view.Items[0].CompletedAt; got != "" {
		t.Fatalf("item completed_at = %q, want empty", got)
	}
	if view.Summary == nil {
		t.Fatal("summary is nil")
	}
	if got := view.Summary.CurrentStatus; got != "running" {
		t.Fatalf("summary current_status = %q, want running", got)
	}
	if got := view.Summary.CompletedAt; got != "" {
		t.Fatalf("summary completed_at = %q, want empty", got)
	}
	if got := view.Summary.CompletionResult; got != "" {
		t.Fatalf("summary completion_result = %q, want empty", got)
	}
	if got := view.Summary.LastUpdateID; got != "panel-complete-update-002" {
		t.Fatalf("summary last_update_id = %q, want panel-complete-update-002", got)
	}

	var payload progressPanelSubmitPayload
	raw, err := json.Marshal(normalized.Payload)
	if err != nil {
		t.Fatalf("marshal normalized payload: %v", err)
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal normalized payload: %v", err)
	}
	if payload.Outcome != "accepted" {
		t.Fatalf("payload outcome = %q, want accepted", payload.Outcome)
	}
	if payload.UpdateID != "panel-complete-update-002" {
		t.Fatalf("payload update_id = %q, want panel-complete-update-002", payload.UpdateID)
	}
}
