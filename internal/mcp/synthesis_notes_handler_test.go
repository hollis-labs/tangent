package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/room"
)

func TestSynthesisNotes_HiddenThenVisibleAfterDrafting(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "synthesis-notes")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	done := make(chan advanceResult, 1)
	go func() {
		done <- callSynthesisNotes(t, rg, roomID, "synth-env-1", "present")
	}()

	frame := readWSFrame(t, conn, 3*time.Second)
	assertSynthesisFrameHidden(t, frame)
	writeSynthesisAck(t, conn, "synth-env-1")

	res := <-done
	if res.err != nil {
		t.Fatalf("synthesis notes transport err: %v", res.err)
	}
	if res.result.IsError {
		t.Fatalf("synthesis notes IsError=true: %s", extractText(t, res.result))
	}

	state := getSynthesisState(t, rg, roomID)
	if state.SynthesisNotes == nil || state.SynthesisNotes.Visibility != "hidden" {
		t.Fatalf("hidden synthesis state = %+v", state.SynthesisNotes)
	}
	if state.SynthesisNotes.Summary != "" || state.SynthesisNotes.Outline != nil {
		t.Fatalf("hidden synthesis leaked visible fields: %+v", state.SynthesisNotes)
	}
	if got := state.PhaseOutputs[room.SynthesisPhaseID].Data["private_notes"]; got == nil {
		t.Fatalf("private notes missing from phase_outputs: %+v", state.PhaseOutputs)
	}

	advancePhase(t, rg, roomID, room.DraftingPhaseID)

	done = make(chan advanceResult, 1)
	go func() {
		done <- callSynthesisNotes(t, rg, roomID, "synth-env-2", "present")
	}()
	select {
	case early := <-done:
		if early.err != nil {
			t.Fatalf("visible synthesis notes early transport err: %v", early.err)
		}
		t.Fatalf("visible synthesis notes returned before WS frame: %s", extractText(t, early.result))
	case <-time.After(150 * time.Millisecond):
	}

	frame = readWSFrame(t, conn, 3*time.Second)
	assertSynthesisFrameVisible(t, frame)
	writeSynthesisAck(t, conn, "synth-env-2")

	res = <-done
	if res.err != nil {
		t.Fatalf("visible synthesis notes transport err: %v", res.err)
	}
	if res.result.IsError {
		t.Fatalf("visible synthesis notes IsError=true: %s", extractText(t, res.result))
	}

	state = getSynthesisState(t, rg, roomID)
	if state.SynthesisNotes == nil || state.SynthesisNotes.Visibility != "visible" {
		t.Fatalf("visible synthesis state = %+v", state.SynthesisNotes)
	}
	if state.SynthesisNotes.Summary == "" {
		t.Fatalf("visible synthesis summary missing: %+v", state.SynthesisNotes)
	}
	if state.SynthesisNotes.OutlineState != "present" || state.SynthesisNotes.Outline == nil {
		t.Fatalf("visible outline missing: %+v", state.SynthesisNotes)
	}
}

func TestSynthesisNotes_InputValidation(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	res, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.synthesis_notes",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":    1,
				"id":   "synth-invalid-1",
				"type": "tangent.synthesis-notes",
				"data": map[string]any{
					"private_notes": "outline requested without payload",
					"outline_state": "present",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("synthesis_notes: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true")
	}
	if got := extractText(t, res); got == "" {
		t.Fatal("expected validation error body")
	}
}

func callSynthesisNotes(
	t *testing.T,
	rg *sessionRig,
	roomID string,
	envelopeID string,
	outlineState string,
) advanceResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	data := map[string]any{
		"private_notes": "Interview synthesis: anchor the draft in workflow constraints first.",
		"summary":       "Lead with workflow constraints, then move into concrete examples.",
		"outline_state": outlineState,
	}
	if outlineState == "present" {
		data["outline"] = map[string]any{
			"title": "Draft outline",
			"items": []any{
				map[string]any{"label": "Goal", "description": "Frame the writing target."},
				map[string]any{"label": "Constraints", "description": "Define the scope limits."},
			},
		}
	}

	res, err := rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "tangent.synthesis_notes",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":     1,
				"id":    envelopeID,
				"type":  "tangent.synthesis-notes",
				"title": "Synthesis handoff",
				"data":  data,
				"meta": map[string]any{
					"roomID": roomID,
				},
			},
		},
	})
	return advanceResult{result: res, err: err}
}

func writeSynthesisAck(t *testing.T, conn *websocket.Conn, envelopeID string) {
	t.Helper()
	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": envelopeID,
		"response": map[string]any{
			"v":          1,
			"envelopeId": envelopeID,
			"kind":       "ack",
			"status":     "submitted",
		},
	})
}

func assertSynthesisFrameHidden(t *testing.T, frame map[string]any) {
	t.Helper()
	data := synthesisFrameData(t, frame)
	if data["visibility"] != "hidden" {
		t.Fatalf("visibility = %v, want hidden", data["visibility"])
	}
	if _, ok := data["private_notes"]; ok {
		t.Fatalf("private_notes leaked in hidden frame: %+v", data)
	}
	if _, ok := data["summary"]; ok {
		t.Fatalf("summary leaked in hidden frame: %+v", data)
	}
	if _, ok := data["outline"]; ok {
		t.Fatalf("outline leaked in hidden frame: %+v", data)
	}
}

func assertSynthesisFrameVisible(t *testing.T, frame map[string]any) {
	t.Helper()
	data := synthesisFrameData(t, frame)
	if data["visibility"] != "visible" {
		t.Fatalf("visibility = %v, want visible", data["visibility"])
	}
	if _, ok := data["private_notes"]; ok {
		t.Fatalf("private_notes leaked in visible frame: %+v", data)
	}
	if summary, _ := data["summary"].(string); summary == "" {
		t.Fatalf("visible summary missing: %+v", data)
	}
	if _, ok := data["outline"].(map[string]any); !ok {
		t.Fatalf("visible outline missing: %+v", data)
	}
}

func synthesisFrameData(t *testing.T, frame map[string]any) map[string]any {
	t.Helper()
	env, ok := frame["envelope"].(map[string]any)
	if !ok {
		t.Fatalf("frame missing envelope: %+v", frame)
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("frame missing data: %+v", frame)
	}
	return data
}

func advancePhase(t *testing.T, rg *sessionRig, roomID, phase string) {
	t.Helper()
	res, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.session_advance_phase",
		Arguments: map[string]any{
			"roomID":   roomID,
			"to_phase": phase,
		},
	})
	if err != nil {
		t.Fatalf("session_advance_phase: %v", err)
	}
	if res.IsError {
		t.Fatalf("session_advance_phase IsError=true: %s", extractText(t, res))
	}
}

type synthesisStatePayload struct {
	PhaseOutputs   map[string]room.PhaseOutput `json:"phase_outputs"`
	SynthesisNotes *room.SynthesisNotesView    `json:"synthesis_notes"`
}

func getSynthesisState(t *testing.T, rg *sessionRig, roomID string) synthesisStatePayload {
	t.Helper()
	res, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.session_get",
		Arguments: map[string]any{
			"roomID": roomID,
		},
	})
	if err != nil {
		t.Fatalf("session_get: %v", err)
	}
	if res.IsError {
		t.Fatalf("session_get IsError=true: %s", extractText(t, res))
	}
	var state synthesisStatePayload
	if err := json.Unmarshal([]byte(extractText(t, res)), &state); err != nil {
		t.Fatalf("unmarshal session_get: %v", err)
	}
	return state
}
