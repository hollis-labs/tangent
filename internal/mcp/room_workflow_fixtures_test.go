package mcp_test

import (
	"context"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fixedCompletedAt pins the participant-supplied completion instant so two
// runs of the same fixture differ only where Tangent itself injects a
// timestamp. It is the instant from the live incident this task is built
// around: the operator's answer landed durably at 12:57:17Z while the caller's
// request had already died.
const fixedCompletedAt = "2026-09-04T12:57:17Z"

// workflowFixture is one shipped room-backed workflow: how a caller invokes
// it, and what the participant sends back.
//
// The table below is the enumeration of the completion contract's blast
// radius. Every named room workflow plus tangent.session_advance appears here
// exactly once, so a workflow cannot be migrated without acquiring regression
// coverage, and a new one cannot be added without a fixture.
type workflowFixture struct {
	tool         string
	envelopeType string
	// roomArgument is true for tangent.session_advance, which names its room
	// as a top-level argument rather than through envelope meta.
	roomArgument bool
	data         map[string]any
	// response is the participant submission body, minus the envelope id which
	// is filled in per call.
	response map[string]any
}

func shippedRoomWorkflows() []workflowFixture {
	return []workflowFixture{
		{
			tool: "tangent.triage", envelopeType: "tangent.triage",
			data: map[string]any{"prompt": "triage the inbox", "items": []any{"item"}},
			response: map[string]any{
				"kind": "data", "status": "submitted",
				"payload": map[string]any{"decision": "keep"},
			},
		},
		{
			tool: "tangent.app-board", envelopeType: "tangent.app-board",
			data: map[string]any{
				"board_id": "board-1",
				"title":    "Active work",
				"cards": []any{
					map[string]any{
						"id": "CW-1", "title": "First card",
						"fields": map[string]any{"status": "doing"},
					},
				},
				"columns": []any{
					map[string]any{"id": "doing", "label": "Doing", "card_ids": []any{"CW-1"}},
				},
			},
			response: map[string]any{
				"kind": "data", "status": "submitted",
				"payload": map[string]any{
					"board_id": "board-1", "action_id": "open", "card_id": "CW-1",
				},
			},
		},
		{
			tool: "tangent.session_advance", envelopeType: "tangent.triage", roomArgument: true,
			data: map[string]any{"prompt": "advance the session", "items": []any{"item"}},
			response: map[string]any{
				"kind": "data", "status": "submitted",
				"payload": map[string]any{"decision": "advance"},
			},
		},
		{
			tool: "tangent.feedback", envelopeType: "tangent.feedback",
			data: map[string]any{"questions": []any{
				map[string]any{"id": "q1", "type": "text", "label": "What changed?", "required": true},
			}},
			response: map[string]any{
				"kind": "data", "status": "submitted",
				"payload": map[string]any{"answers": []any{
					map[string]any{"questionId": "q1", "value": "Ship it"},
				}},
			},
		},
		{
			tool: "tangent.form-collect", envelopeType: "tangent.form-collect",
			data: map[string]any{
				"form_id": "form-1",
				"schema": map[string]any{"fields": []any{
					map[string]any{"field_id": "scope", "label": "Scope", "kind": "text"},
				}},
			},
			response: map[string]any{
				"kind": "data", "status": "submitted",
				"payload": map[string]any{
					"form_id":   "form-1",
					"action_id": "submit",
					"answers":   map[string]any{"scope": "everything"},
					"notes":     "looks right",
				},
			},
		},
		{
			tool: "tangent.design-iteration", envelopeType: "tangent.design-iteration",
			data: map[string]any{
				"variant_id": "variant-1",
				"html":       `<button id="hero">Hero</button>`,
				"prompts": []any{
					map[string]any{"id": "hero", "kind": "click-region", "label": "Hero", "selector": "#hero"},
				},
			},
			response: map[string]any{
				"kind": "data", "status": "submitted",
				"payload": map[string]any{
					"variant_id": "variant-1", "action_id": "hero",
					"action_kind": "click-region", "value": "primary card",
				},
			},
		},
		{
			tool: "tangent.interview_question", envelopeType: "tangent.interview-question",
			data: map[string]any{
				"prompt": "Tell me what matters most here.", "helper_text": "Be specific.",
				"thread_id": "thread-1", "topic_label": "Priorities",
				"choices": []any{
					map[string]any{"id": "quality", "label": "Quality"},
					map[string]any{"id": "time", "label": "Time"},
				},
				"output_shape": map[string]any{"label": "Preferred output shape"},
			},
			response: map[string]any{
				"kind": "data", "status": "submitted",
				"payload": map[string]any{
					"answer_text": "Quality first.", "selected_choice_id": "quality",
					"thread_id": "thread-1", "topic_label": "Priorities",
					"output_shape_signal": "prose",
				},
			},
		},
		{
			tool: "tangent.block_draft", envelopeType: "tangent.block-draft",
			data: map[string]any{
				"block_id": "intro", "mode": "section", "label": "intro",
				"content": "Original intro paragraph.", "rationale": "Lead with the clearest point.",
				"outline_hint": "Outline item",
			},
			response: map[string]any{
				"kind": "data", "status": "submitted",
				"payload": map[string]any{"decision": "accept", "block_id": "intro", "mode": "section"},
			},
		},
		{
			tool: "tangent.prose_revision", envelopeType: "tangent.prose-revision",
			data: map[string]any{
				"lens": "review", "revision_id": "opening-pass", "block_id": "intro",
				"source_text": "Original opening paragraph.",
				"suggestions": []any{
					map[string]any{"id": "s1", "label": "Lead with the claim", "suggested_text": "Lead with the main claim."},
					map[string]any{"id": "s2", "label": "Trim the example", "suggested_text": "Keep one example."},
				},
			},
			response: map[string]any{
				"kind": "data", "status": "submitted",
				"payload": map[string]any{
					"lens": "review", "revision_id": "opening-pass", "block_id": "intro",
					"outcomes": []any{
						map[string]any{"suggestion_id": "s1", "decision": "accept"},
						map[string]any{"suggestion_id": "s2", "decision": "comment", "comment": "Shorten it."},
					},
					"general_comment": "Keep the structure fix.",
				},
			},
		},
		{
			tool: "tangent.output_render", envelopeType: "tangent.output-render",
			data: map[string]any{
				"title": "Final draft", "markdown": "# Final draft\n\nBody copy.",
				"filename": "final-draft.md", "format": "markdown", "summary": "Final polished copy.",
			},
			response: map[string]any{"kind": "ack", "status": "submitted"},
		},
		{
			tool: "tangent.whiteboard", envelopeType: "tangent.whiteboard",
			data: map[string]any{
				"board_id": "board-1", "title": "Whiteboard", "intent": "Map the layout",
				"scene": map[string]any{
					"document": map[string]any{"pages": []any{map[string]any{"id": "page:1"}}},
					"session":  map[string]any{"currentPageId": "page:1"},
				},
			},
			response: map[string]any{
				"kind": "data", "status": "submitted",
				"payload": map[string]any{
					"board_id": "board-1",
					"scene": map[string]any{
						"document": map[string]any{"pages": []any{
							map[string]any{"id": "page:1"}, map[string]any{"id": "shape:1"},
						}},
					},
					"notes": "submitted board",
					"selection_summary": map[string]any{
						"count": 1, "ids": []any{"shape:1"}, "types": []any{"geo"},
					},
				},
			},
		},
		{
			tool: "tangent.dashboard", envelopeType: "tangent.dashboard",
			data: map[string]any{
				"dashboard_id": "dashboard-1", "title": "Ops dashboard",
				"tiles": []any{
					map[string]any{"tile_id": "tile-open", "kind": "room_count", "title": "Open rooms", "value": "4"},
				},
				"layout": []any{
					map[string]any{"tile_id": "tile-open", "x": 0, "y": 0, "w": 2, "h": 1},
				},
				"saved_layouts": []any{
					map[string]any{
						"layout_id": "layout-default", "name": "Default", "is_default": true,
						"tiles": []any{map[string]any{"tile_id": "tile-open", "x": 0, "y": 0, "w": 2, "h": 1}},
					},
				},
				"active_layout_id": "layout-default",
				"summary":          map[string]any{"headline": "4 open rooms", "active_room_count": 4},
			},
			response: map[string]any{
				"kind": "data", "status": "submitted",
				"payload": map[string]any{
					"dashboard_id": "dashboard-1", "action": "refresh",
					"layout": []any{map[string]any{"tile_id": "tile-open", "x": 0, "y": 0, "w": 3, "h": 1}},
				},
			},
		},
		{
			tool: "tangent.file-picker", envelopeType: "tangent.file-picker",
			data: map[string]any{
				"picker_id": "picker-1",
				"browse_roots": []any{
					map[string]any{"root_id": "workspace", "label": "Workspace", "path": "/tmp/workspace"},
				},
				"files": []any{
					map[string]any{
						"artifact_id": "artifact-1", "name": "spec.md", "uri": "artifact://artifact-1",
						"root_id": "workspace", "relative_path": "docs/spec.md",
					},
				},
			},
			response: map[string]any{
				"kind": "data", "status": "submitted",
				"payload": map[string]any{
					"picker_id": "picker-1",
					"selected_refs": []any{
						map[string]any{
							"artifact_id": "artifact-1", "name": "spec.md", "uri": "artifact://artifact-1",
							"root_id": "workspace", "relative_path": "docs/spec.md",
						},
					},
					"query_state": map[string]any{
						"current_root_id": "workspace", "current_dir": "docs",
						"search": "spec", "sort": "path:asc",
					},
				},
			},
		},
		{
			tool: "tangent.progress-panel", envelopeType: "tangent.progress-panel",
			data: map[string]any{
				"panel_id": "panel-1",
				"items": []any{
					map[string]any{"item_id": "item-1", "label": "Scan repo", "status": "running"},
					map[string]any{"item_id": "item-2", "label": "Write summary", "status": "queued"},
				},
				"summary": map[string]any{"current_status": "running", "headline": "1 running"},
			},
			response: map[string]any{
				"kind": "data", "status": "submitted",
				"payload": map[string]any{
					"panel_id": "panel-1", "item_id": "item-2", "status": "running",
					"summary": "Started drafting the final summary.", "checkpoint_label": "Summary started",
				},
			},
		},
		{
			tool: "tangent.wizard", envelopeType: "tangent.wizard",
			data: map[string]any{
				"wizard_id": "wizard-1", "title": "Release wizard",
				"steps": []any{
					map[string]any{
						"step_id": "step-scope", "title": "Scope", "kind": "form",
						"fields": map[string]any{"fields": []any{
							map[string]any{"field_id": "scope", "label": "Scope", "kind": "textarea"},
						}},
						"branches": []any{
							map[string]any{"branch_id": "review", "label": "Review", "target_step_id": "step-review"},
						},
					},
					map[string]any{"step_id": "step-review", "title": "Review", "kind": "review"},
				},
				"current_step_id": "step-scope",
			},
			response: map[string]any{
				"kind": "data", "status": "partial",
				"payload": map[string]any{
					"wizard_id": "wizard-1", "title": "Release wizard", "current_step_id": "step-review",
					"progress": []any{
						map[string]any{
							"step_id": "step-scope", "status": "completed", "revision_id": "rev-001",
							"response": map[string]any{"scope": "wizard envelope"}, "summary": "Scope saved",
						},
					},
					"branch_selections": []any{
						map[string]any{"step_id": "step-scope", "option_id": "review"},
					},
					"summary": map[string]any{
						"status": "in_progress", "completed_step_count": 1,
						"total_step_count": 2, "current_step_id": "step-review",
					},
				},
			},
		},
		{
			tool: "tangent.diff-review", envelopeType: "tangent.diff-review",
			data: map[string]any{
				"review_id": "review-1",
				"files": []any{
					map[string]any{
						"id": "file-1", "path": "pkg/app.go",
						"hunks": []any{map[string]any{"id": "hunk-1", "header": "@@ -1,2 +1,2 @@"}},
					},
				},
				"before_ref": map[string]any{"artifact_id": "artifact-before", "name": "before.patch"},
				"after_ref":  map[string]any{"artifact_id": "artifact-after", "name": "after.patch"},
			},
			response: map[string]any{
				"kind": "data", "status": "submitted",
				"payload": map[string]any{
					"review_id": "review-1", "current_file": "file-1",
					"filter_state": map[string]any{"search": "pkg", "decision": "pending"},
					"decisions": []any{
						map[string]any{"file_id": "file-1", "hunk_id": "hunk-1", "decision": "accept"},
					},
					"comments": map[string]any{"file-1::hunk-1": "Looks good."},
					"summary":  map[string]any{"export_name": "review-1-summary.md"},
				},
			},
		},
		{
			tool: "tangent.spreadsheet-review", envelopeType: "tangent.spreadsheet-review",
			data: map[string]any{
				"table_id": "table-1", "title": "Spreadsheet review",
				"columns": []any{
					map[string]any{"id": "name", "label": "Name"},
					map[string]any{"id": "status", "label": "Status"},
				},
				"rows": []any{
					map[string]any{"id": "row-1", "name": "Alpha", "status": "open"},
				},
				"row_actions": []any{map[string]any{"id": "approve", "label": "Approve"}},
			},
			response: map[string]any{
				"kind": "data", "status": "submitted",
				"payload": map[string]any{
					"table_id": "table-1", "selected_row_ids": []any{"row-1"},
					"selected_rows": []any{map[string]any{"id": "row-1", "name": "Alpha", "status": "open"}},
					"query_state":   map[string]any{"search": "Alpha"},
					"notes":         "first pass", "action_id": "approve",
				},
			},
		},
		{
			tool: "tangent.approval-queue", envelopeType: "tangent.approval-queue",
			data: map[string]any{
				"queue_id": "queue-1",
				"items": []any{
					map[string]any{
						"id": "item-1", "title": "Update dependency", "summary": "Low-risk patch release",
						"description": "Bump package A",
						"action_options": []any{
							map[string]any{"id": "merge", "label": "Merge"},
						},
					},
				},
			},
			response: map[string]any{
				"kind": "data", "status": "submitted",
				"payload": map[string]any{
					"queue_id": "queue-1", "current_index": 0, "notes": "first pass",
					"decisions": []any{
						map[string]any{"item_id": "item-1", "decision": "accept", "action_id": "merge", "comment": "safe"},
					},
				},
			},
		},
		{
			tool: "tangent.synthesis_notes", envelopeType: "tangent.synthesis-notes",
			data: map[string]any{
				"private_notes": "Anchor the draft in workflow constraints first.",
				"summary":       "Lead with workflow constraints.",
				"outline_state": "present",
				"outline": map[string]any{
					"title": "Draft outline",
					"items": []any{
						map[string]any{"label": "Goal", "description": "Frame the writing target."},
					},
				},
			},
			response: map[string]any{"kind": "ack", "status": "submitted"},
		},
	}
}

// callWorkflow invokes one fixture's tool. completion is nil for the default
// wait mode.
func callWorkflow(
	t *testing.T,
	rg *sessionRig,
	fixture workflowFixture,
	roomID string,
	envelopeID string,
	completion map[string]any,
	data map[string]any,
) advanceResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if data == nil {
		data = fixture.data
	}
	envelope := map[string]any{
		"v": 1, "id": envelopeID, "type": fixture.envelopeType, "data": data,
	}
	arguments := map[string]any{"envelope": envelope}
	if fixture.roomArgument {
		arguments["roomID"] = roomID
	} else {
		envelope["meta"] = map[string]any{"roomID": roomID}
	}
	if completion != nil {
		arguments["completion"] = completion
	}
	res, err := rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{Name: fixture.tool, Arguments: arguments})
	return advanceResult{result: res, err: err}
}

// participantResponse renders one fixture's submission as the WebSocket frame
// a browser sends.
func (f workflowFixture) participantResponse(envelopeID string) map[string]any {
	response := map[string]any{"v": 1, "envelopeId": envelopeID, "completedAt": fixedCompletedAt}
	for key, value := range f.response {
		response[key] = value
	}
	return map[string]any{"type": "response", "envelopeId": envelopeID, "response": response}
}
