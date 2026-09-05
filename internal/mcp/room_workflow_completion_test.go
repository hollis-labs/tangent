package mcp_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/roomflow"
)

// testWindow is the compressed compatibility window used throughout this file.
// It stands in for the shipped 45 seconds so the pending-receipt path is
// exercised deterministically instead of by waiting out a real transport.
const testWindow = 250 * time.Millisecond

// pendingReceipt mirrors the normative receipt shape callers branch on.
type pendingReceipt struct {
	Status string `json:"status"`
	Handle struct {
		SurfaceID     string `json:"surface_id"`
		InteractionID string `json:"interaction_id"`
		RoomID        string `json:"room_id"`
		EnvelopeID    string `json:"envelope_id"`
		URL           string `json:"url"`
	} `json:"handle"`
	Resume struct {
		GetTool       string `json:"get_tool"`
		AwaitTool     string `json:"await_tool"`
		RetryOriginal bool   `json:"retry_original"`
	} `json:"resume"`
}

func newDurableRig(t *testing.T) *sessionRig {
	t.Helper()
	return newSessionRigWith(t, sessionRigOptions{durable: true, window: testWindow})
}

// TestRoomWorkflows_FastPathMatchesLegacyResponse runs every shipped room
// workflow and tangent.session_advance through both topologies and asserts the
// public success response is the same.
//
// Values Tangent itself stamps at submission time — server-injected instants
// and generated ids — are normalized before comparison, because two separate
// runs legitimately differ there. Everything a caller actually branches on is
// compared exactly.
func TestRoomWorkflows_FastPathMatchesLegacyResponse(t *testing.T) {
	for _, fixture := range shippedRoomWorkflows() {
		t.Run(fixture.tool, func(t *testing.T) {
			legacy := runFixtureToCompletion(t, newSessionRig(t), fixture, nil)
			durable := runFixtureToCompletion(t, newDurableRig(t), fixture, nil)
			if !reflect.DeepEqual(legacy, durable) {
				legacyRaw, _ := json.MarshalIndent(legacy, "", "  ")
				durableRaw, _ := json.MarshalIndent(durable, "", "  ")
				t.Fatalf("durable response diverged from v0.12\nlegacy:\n%s\ndurable:\n%s", legacyRaw, durableRaw)
			}
		})
	}
}

// TestRoomWorkflows_WaitReturnsPendingReceiptWithoutTerminalizing covers the
// exact failure the live approval-queue incident produced: the caller's wait
// runs out while the human is still deciding.
//
// The caller must receive a successful receipt, not an MCP error, and the
// interaction must be exactly as answerable afterwards as it was before.
func TestRoomWorkflows_WaitReturnsPendingReceiptWithoutTerminalizing(t *testing.T) {
	for _, fixture := range shippedRoomWorkflows() {
		t.Run(fixture.tool, func(t *testing.T) {
			rg := newDurableRig(t)
			defer rg.cleanup()
			roomID, _ := createSession(t, rg, fixture.tool)

			envelopeID := "pending-" + strings.ReplaceAll(fixture.tool, ".", "-")
			result := callWorkflow(t, rg, fixture, roomID, envelopeID, nil, nil)
			if result.err != nil {
				t.Fatalf("%s transport err: %v", fixture.tool, result.err)
			}
			if result.result.IsError {
				t.Fatalf("expired wait produced an MCP error: %s", extractText(t, result.result))
			}
			receipt := decodeReceipt(t, result.result)
			if receipt.Handle.RoomID != roomID || receipt.Handle.EnvelopeID != envelopeID {
				t.Fatalf("receipt handle = %+v, want room %q envelope %q", receipt.Handle, roomID, envelopeID)
			}
			if receipt.Resume.GetTool != "tangent.interaction_get" ||
				receipt.Resume.AwaitTool != "tangent.interaction_await" || !receipt.Resume.RetryOriginal {
				t.Fatalf("receipt resume = %+v", receipt.Resume)
			}
			if receipt.Handle.URL == "" || !strings.Contains(receipt.Handle.URL, roomID) {
				t.Fatalf("receipt url = %q, want a link to room %q", receipt.Handle.URL, roomID)
			}

			state := interactionState(t, rg, receipt.Handle.InteractionID)
			if state == "resolved" || state == "canceled" || state == "expired" ||
				state == "failed" || state == "superseded" {
				t.Fatalf("expired wait terminalized the interaction: state = %q", state)
			}
			if rm, ok := rg.mgr.Get(roomID); !ok || !rm.IsPresenting(envelopeID) {
				t.Fatal("expired wait retired the room presentation")
			}
		})
	}
}

// TestRoomWorkflows_AsyncReturnsReceiptImmediately asserts async mode never
// waits out the window.
func TestRoomWorkflows_AsyncReturnsReceiptImmediately(t *testing.T) {
	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: 30 * time.Second})
	defer rg.cleanup()
	roomID, _ := createSession(t, rg, "async")

	for _, fixture := range shippedRoomWorkflows() {
		t.Run(fixture.tool, func(t *testing.T) {
			envelopeID := "async-" + strings.ReplaceAll(fixture.tool, ".", "-")
			started := time.Now()
			result := callWorkflow(t, rg, fixture, roomID, envelopeID,
				map[string]any{"mode": "async"}, nil)
			elapsed := time.Since(started)
			if result.err != nil {
				t.Fatalf("%s transport err: %v", fixture.tool, result.err)
			}
			if result.result.IsError {
				t.Fatalf("async mode produced an MCP error: %s", extractText(t, result.result))
			}
			receipt := decodeReceipt(t, result.result)
			if receipt.Handle.InteractionID == "" {
				t.Fatalf("async receipt has no durable handle: %+v", receipt)
			}
			// The rig's window is 30s; returning in a fraction of that proves
			// async did not fall through to the wait path.
			if elapsed > 5*time.Second {
				t.Fatalf("async mode waited %v", elapsed)
			}
			// Retire this room's presentation so the next fixture is admitted.
			cancelInteractionForTest(t, rg, roomID, receipt.Handle.InteractionID)
		})
	}
}

// TestRoomWorkflow_HandleRecoversResultAfterTransportExpiry is the end-to-end
// answer to the incident. The caller's wait expires, the human answers
// afterwards, and every documented recovery route returns the same immutable
// result — byte for byte.
func TestRoomWorkflow_HandleRecoversResultAfterTransportExpiry(t *testing.T) {
	rg := newDurableRig(t)
	defer rg.cleanup()
	fixture := fixtureByTool(t, "tangent.approval-queue")
	roomID, _ := createSession(t, rg, "approval-queue")

	const envelopeID = "approval-recovery-1"
	first := callWorkflow(t, rg, fixture, roomID, envelopeID, nil, nil)
	if first.err != nil {
		t.Fatalf("first call transport err: %v", first.err)
	}
	receipt := decodeReceipt(t, first.result)

	// The operator opens the room only after the caller's transport is gone.
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	frame := readWSFrame(t, conn, 3*time.Second)
	if frame["envelopeId"] != envelopeID {
		t.Fatalf("replayed envelopeId = %v, want %s", frame["envelopeId"], envelopeID)
	}
	writeWSFrame(t, conn, fixture.participantResponse(envelopeID))
	awaitInteractionState(t, rg, receipt.Handle.InteractionID, "resolved", 3*time.Second)

	// Route 1: a fresh interaction_get by handle.
	viaGet := interactionResolutionPayload(t, rg, "tangent.interaction_get", map[string]any{
		"interaction_id":  receipt.Handle.InteractionID,
		"requester_scope": roomflow.DefaultCaller.Scope,
	})
	// Route 2: a bounded interaction_await by handle.
	viaAwait := interactionResolutionPayload(t, rg, "tangent.interaction_await", map[string]any{
		"interaction_id":  receipt.Handle.InteractionID,
		"requester_scope": roomflow.DefaultCaller.Scope,
		"maximum_wait_ms": 2000,
	})
	// Route 3: an identical retry of the original named invocation.
	retry := callWorkflow(t, rg, fixture, roomID, envelopeID, nil, nil)
	if retry.err != nil {
		t.Fatalf("retry transport err: %v", retry.err)
	}
	if retry.result.IsError {
		t.Fatalf("retry produced an MCP error: %s", extractText(t, retry.result))
	}
	viaRetry := extractText(t, retry.result)

	if viaGet != viaAwait {
		t.Fatalf("interaction_get and interaction_await disagree:\n%s\n%s", viaGet, viaAwait)
	}
	if !sameJSON(t, viaGet, viaRetry) {
		t.Fatalf("original-invocation retry diverged from the stored result:\nstored: %s\nretry:  %s", viaGet, viaRetry)
	}

	// Exactly one interaction and exactly one resolution exist for the identity.
	assertCounts(t, rg, receipt.Handle.InteractionID, 1)
}

// TestRoomWorkflow_ChangedPayloadIsADeterministicConflict asserts the identity
// rule: reusing caller scope + workflow kind + envelope id with a different
// payload can neither open a second interaction nor execute anything.
func TestRoomWorkflow_ChangedPayloadIsADeterministicConflict(t *testing.T) {
	rg := newDurableRig(t)
	defer rg.cleanup()
	fixture := fixtureByTool(t, "tangent.triage")
	roomID, _ := createSession(t, rg, "conflict")

	const envelopeID = "conflict-1"
	first := callWorkflow(t, rg, fixture, roomID, envelopeID, map[string]any{"mode": "async"}, nil)
	if first.err != nil || first.result.IsError {
		t.Fatalf("first call failed: err=%v body=%s", first.err, extractText(t, first.result))
	}
	receipt := decodeReceipt(t, first.result)

	changed := map[string]any{"prompt": "a different question entirely", "items": []any{"other"}}
	for attempt := range 2 {
		conflict := callWorkflow(t, rg, fixture, roomID, envelopeID, map[string]any{"mode": "async"}, changed)
		if conflict.err != nil {
			t.Fatalf("conflict attempt %d transport err: %v", attempt, conflict.err)
		}
		if !conflict.result.IsError {
			t.Fatalf("conflict attempt %d was accepted: %s", attempt, extractText(t, conflict.result))
		}
		if code := errorCodeOf(t, conflict.result); code != "IDEMPOTENCY_CONFLICT" {
			t.Fatalf("conflict attempt %d code = %q, want IDEMPOTENCY_CONFLICT", attempt, code)
		}
	}
	// Nothing new was opened and nothing was executed.
	assertCounts(t, rg, receipt.Handle.InteractionID, 1)
	if state := interactionState(t, rg, receipt.Handle.InteractionID); state == "resolved" {
		t.Fatal("a conflicting retry executed the interaction")
	}
}

// TestRoomWorkflow_RestartRebuildsPresentationFromCanonicalRecords proves the
// process-local pending map is not authoritative: the entire in-memory
// topology is thrown away and the room comes back from durable records alone.
func TestRoomWorkflow_RestartRebuildsPresentationFromCanonicalRecords(t *testing.T) {
	rg := newDurableRig(t)
	fixture := fixtureByTool(t, "tangent.diff-review")
	roomID, _ := createSession(t, rg, "restart")

	const envelopeID = "restart-1"
	first := callWorkflow(t, rg, fixture, roomID, envelopeID, map[string]any{"mode": "async"}, nil)
	if first.err != nil || first.result.IsError {
		t.Fatalf("first call failed: err=%v body=%s", first.err, extractText(t, first.result))
	}
	receipt := decodeReceipt(t, first.result)
	dbPath := rg.dbPath
	// Simulate a process restart: every in-memory structure is discarded and
	// no room is closed, so anything the rebuilt process shows had to come from
	// durable records.
	rg.shutdown()

	restarted := newSessionRigWith(t, sessionRigOptions{durable: true, window: testWindow, dbPath: dbPath})
	defer restarted.cleanup()

	rm, ok := restarted.mgr.Get(roomID)
	if !ok {
		t.Fatal("room did not survive restart")
	}
	if !rm.IsPresenting(envelopeID) {
		t.Fatal("restart did not rebuild the room presentation from canonical records")
	}
	if env := rm.CurrentEnvelope(); env == nil || env.ID != envelopeID || env.Type != fixture.envelopeType {
		t.Fatalf("rebuilt envelope = %+v", env)
	}

	// A browser attaching after the restart still resolves it, and the result
	// is retrievable by the handle issued before the restart.
	conn, _, err := websocket.Dial(context.Background(), restarted.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	frame := readWSFrame(t, conn, 3*time.Second)
	if frame["envelopeId"] != envelopeID {
		t.Fatalf("post-restart replay envelopeId = %v", frame["envelopeId"])
	}
	writeWSFrame(t, conn, fixture.participantResponse(envelopeID))
	awaitInteractionState(t, restarted, receipt.Handle.InteractionID, "resolved", 3*time.Second)

	retry := callWorkflow(t, restarted, fixture, roomID, envelopeID, nil, nil)
	if retry.err != nil || retry.result.IsError {
		t.Fatalf("post-restart retry failed: err=%v body=%s", retry.err, extractText(t, retry.result))
	}
	stored := interactionResolutionPayload(t, restarted, "tangent.interaction_get", map[string]any{
		"interaction_id":  receipt.Handle.InteractionID,
		"requester_scope": roomflow.DefaultCaller.Scope,
	})
	if !sameJSON(t, stored, extractText(t, retry.result)) {
		t.Fatalf("post-restart retry diverged:\nstored: %s\nretry:  %s", stored, extractText(t, retry.result))
	}
	assertCounts(t, restarted, receipt.Handle.InteractionID, 1)
}

// TestRoomWorkflow_BrowserDisconnectDoesNotTerminalize asserts socket loss is
// a presentation fact only.
func TestRoomWorkflow_BrowserDisconnectDoesNotTerminalize(t *testing.T) {
	rg := newDurableRig(t)
	defer rg.cleanup()
	fixture := fixtureByTool(t, "tangent.feedback")
	roomID, _ := createSession(t, rg, "disconnect")

	const envelopeID = "disconnect-1"
	first := callWorkflow(t, rg, fixture, roomID, envelopeID, map[string]any{"mode": "async"}, nil)
	if first.err != nil || first.result.IsError {
		t.Fatalf("first call failed: err=%v body=%s", first.err, extractText(t, first.result))
	}
	receipt := decodeReceipt(t, first.result)

	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	_ = readWSFrame(t, conn, 3*time.Second)
	_ = conn.Close(websocket.StatusNormalClosure, "operator closed the tab")

	// Give the read loop time to observe the close and detach.
	time.Sleep(100 * time.Millisecond)
	if state := interactionState(t, rg, receipt.Handle.InteractionID); state == "canceled" || state == "failed" {
		t.Fatalf("browser disconnect terminalized the interaction: %q", state)
	}

	reconnected, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws redial: %v", err)
	}
	defer reconnected.Close(websocket.StatusNormalClosure, "done")
	replayed := readWSFrame(t, reconnected, 3*time.Second)
	if replayed["envelopeId"] != envelopeID {
		t.Fatalf("reconnect replay envelopeId = %v", replayed["envelopeId"])
	}
	response := fixture.participantResponse(envelopeID)
	response["revision"] = replayed["revision"]
	writeWSFrame(t, reconnected, response)
	awaitInteractionState(t, rg, receipt.Handle.InteractionID, "resolved", 3*time.Second)
	assertCounts(t, rg, receipt.Handle.InteractionID, 1)
}

// TestRoomWorkflow_ParticipantCancelIsTheOnlyRoomTerminalizer asserts an
// explicit cancellation is terminal, replayable, and preserves the v0.12
// ack/cancelled response shape.
func TestRoomWorkflow_ParticipantCancelIsTheOnlyRoomTerminalizer(t *testing.T) {
	rg := newDurableRig(t)
	defer rg.cleanup()
	fixture := fixtureByTool(t, "tangent.triage")
	roomID, _ := createSession(t, rg, "cancel")

	const envelopeID = "cancel-1"
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	done := make(chan advanceResult, 1)
	go func() { done <- callWorkflow(t, rg, fixture, roomID, envelopeID, nil, nil) }()
	frame := readWSFrame(t, conn, 3*time.Second)
	writeWSFrame(t, conn, map[string]any{
		"type": "cancel", "envelopeId": envelopeID, "revision": frame["revision"],
	})
	result := <-done
	if result.err != nil {
		t.Fatalf("cancel transport err: %v", result.err)
	}
	if result.result.IsError {
		t.Fatalf("cancel produced an MCP error: %s", extractText(t, result.result))
	}
	var cancelled struct {
		Kind        string `json:"kind"`
		Status      string `json:"status"`
		EnvelopeID  string `json:"envelopeId"`
		CompletedAt string `json:"completedAt"`
	}
	if err := json.Unmarshal([]byte(extractText(t, result.result)), &cancelled); err != nil {
		t.Fatalf("unmarshal cancel result: %v", err)
	}
	if cancelled.Kind != "ack" || cancelled.Status != "cancelled" || cancelled.EnvelopeID != envelopeID {
		t.Fatalf("cancel response = %+v, want the v0.12 ack/cancelled shape", cancelled)
	}

	// The immutable cancellation replays identically, including its instant.
	retry := callWorkflow(t, rg, fixture, roomID, envelopeID, nil, nil)
	if retry.err != nil || retry.result.IsError {
		t.Fatalf("cancel retry failed: err=%v body=%s", retry.err, extractText(t, retry.result))
	}
	if !sameJSON(t, extractText(t, result.result), extractText(t, retry.result)) {
		t.Fatalf("cancel retry diverged:\nfirst: %s\nretry: %s",
			extractText(t, result.result), extractText(t, retry.result))
	}
}

// TestRoomWorkflow_AcknowledgementIsIdempotentAndSeparateFromRetrieval covers
// the delivery/retrieval/acknowledgement separation.
func TestRoomWorkflow_AcknowledgementIsIdempotentAndSeparateFromRetrieval(t *testing.T) {
	rg := newDurableRig(t)
	defer rg.cleanup()
	fixture := fixtureByTool(t, "tangent.triage")
	roomID, _ := createSession(t, rg, "ack")

	const envelopeID = "ack-1"
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	done := make(chan advanceResult, 1)
	go func() { done <- callWorkflow(t, rg, fixture, roomID, envelopeID, nil, nil) }()
	_ = readWSFrame(t, conn, 3*time.Second)
	writeWSFrame(t, conn, fixture.participantResponse(envelopeID))
	result := <-done
	if result.err != nil || result.result.IsError {
		t.Fatalf("workflow failed: err=%v body=%s", result.err, extractText(t, result.result))
	}
	interactionID := singleInteractionID(t, rg, envelopeID)

	// Retrieval and delivery both happened; acknowledgement did not.
	assertRowCount(t, rg, 1, `SELECT COUNT(*) FROM terminal_outcome_retrievals WHERE interaction_id = ?`, interactionID)
	assertRowCount(t, rg, 1, `
SELECT COUNT(*) FROM delivery_attempts a
JOIN resolution_deliveries d ON d.id = a.resolution_delivery_id
JOIN resolutions r ON r.id = d.resolution_id
WHERE r.interaction_id = ? AND a.status = 'delivered'`, interactionID)
	assertRowCount(t, rg, 0, `SELECT COUNT(*) FROM terminal_outcome_acknowledgements WHERE interaction_id = ?`, interactionID)

	// Reading the result again still does not acknowledge it.
	_ = interactionResolutionPayload(t, rg, "tangent.interaction_get", map[string]any{
		"interaction_id":  interactionID,
		"requester_scope": roomflow.DefaultCaller.Scope,
	})
	assertRowCount(t, rg, 0, `SELECT COUNT(*) FROM terminal_outcome_acknowledgements WHERE interaction_id = ?`, interactionID)

	var firstAck string
	for attempt := range 3 {
		ackRes, callErr := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
			Name: "tangent.interaction_acknowledge",
			Arguments: map[string]any{
				"interaction_id":  interactionID,
				"requester_scope": roomflow.DefaultCaller.Scope,
			},
		})
		if callErr != nil {
			t.Fatalf("acknowledge attempt %d: %v", attempt, callErr)
		}
		if ackRes.IsError {
			t.Fatalf("acknowledge attempt %d IsError: %s", attempt, extractText(t, ackRes))
		}
		var ack struct {
			AcknowledgementID string `json:"acknowledgement_id"`
			Created           bool   `json:"created"`
		}
		if err := json.Unmarshal([]byte(extractText(t, ackRes)), &ack); err != nil {
			t.Fatalf("unmarshal acknowledgement: %v", err)
		}
		if attempt == 0 {
			if !ack.Created {
				t.Fatal("first acknowledgement was not recorded as new")
			}
			firstAck = ack.AcknowledgementID
			continue
		}
		if ack.Created {
			t.Fatalf("acknowledgement attempt %d created a second record", attempt)
		}
		if ack.AcknowledgementID != firstAck {
			t.Fatalf("acknowledgement id changed: %q then %q", firstAck, ack.AcknowledgementID)
		}
	}
	assertRowCount(t, rg, 1, `SELECT COUNT(*) FROM terminal_outcome_acknowledgements WHERE interaction_id = ?`, interactionID)
	assertRowCount(t, rg, 1, `
SELECT COUNT(*) FROM resolution_deliveries d
JOIN resolutions r ON r.id = d.resolution_id
WHERE r.interaction_id = ? AND d.lifecycle_state = 'acknowledged'`, interactionID)
	assertRowCount(t, rg, 1, `
SELECT COUNT(*) FROM delivery_events e
JOIN resolution_deliveries d ON d.id = e.resolution_delivery_id
JOIN resolutions r ON r.id = d.resolution_id
WHERE r.interaction_id = ? AND e.event_type = 'delivery.acknowledged'`, interactionID)
}

// TestRoomWorkflow_ConcurrentRetriesShareOneOutcome hammers the identity from
// several goroutines at once. Exactly one interaction, one resolution, and one
// delivered attempt must exist no matter how the races land.
func TestRoomWorkflow_ConcurrentRetriesShareOneOutcome(t *testing.T) {
	rg := newDurableRig(t)
	defer rg.cleanup()
	fixture := fixtureByTool(t, "tangent.triage")
	roomID, _ := createSession(t, rg, "concurrent")

	const envelopeID = "concurrent-1"
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	first := callWorkflow(t, rg, fixture, roomID, envelopeID, map[string]any{"mode": "async"}, nil)
	if first.err != nil || first.result.IsError {
		t.Fatalf("first call failed: err=%v body=%s", first.err, extractText(t, first.result))
	}
	receipt := decodeReceipt(t, first.result)
	_ = readWSFrame(t, conn, 3*time.Second)
	writeWSFrame(t, conn, fixture.participantResponse(envelopeID))
	awaitInteractionState(t, rg, receipt.Handle.InteractionID, "resolved", 3*time.Second)

	const racers = 6
	bodies := make([]string, racers)
	var group sync.WaitGroup
	for index := range racers {
		group.Add(1)
		go func() {
			defer group.Done()
			retry := callWorkflow(t, rg, fixture, roomID, envelopeID, nil, nil)
			if retry.err != nil || retry.result.IsError {
				t.Errorf("racer %d failed: err=%v body=%s", index, retry.err, extractText(t, retry.result))
				return
			}
			bodies[index] = extractText(t, retry.result)
		}()
	}
	group.Wait()
	for index := 1; index < racers; index++ {
		if !sameJSON(t, bodies[0], bodies[index]) {
			t.Fatalf("racer %d saw a different result:\n%s\n%s", index, bodies[0], bodies[index])
		}
	}
	assertCounts(t, rg, receipt.Handle.InteractionID, 1)
	assertRowCount(t, rg, 1, `
SELECT COUNT(*) FROM delivery_attempts a
JOIN resolution_deliveries d ON d.id = a.resolution_delivery_id
JOIN resolutions r ON r.id = d.resolution_id
WHERE r.interaction_id = ?`, receipt.Handle.InteractionID)
}

// TestRoomWorkflow_LegacyProjectionIsNotTerminalAuthority asserts the v0.12
// envelopes row stays a projection: while the caller's wait has expired but
// the human has not answered, the row is still "pending" and the canonical
// record is still the only thing that says so.
func TestRoomWorkflow_LegacyProjectionIsNotTerminalAuthority(t *testing.T) {
	rg := newDurableRig(t)
	defer rg.cleanup()
	fixture := fixtureByTool(t, "tangent.triage")
	roomID, _ := createSession(t, rg, "projection")

	const envelopeID = "projection-1"
	result := callWorkflow(t, rg, fixture, roomID, envelopeID, nil, nil)
	if result.err != nil || result.result.IsError {
		t.Fatalf("call failed: err=%v body=%s", result.err, extractText(t, result.result))
	}
	receipt := decodeReceipt(t, result.result)

	var status string
	if err := rg.db.QueryRow(
		`SELECT status FROM envelopes WHERE room_id = ? AND envelope_id = ?`, roomID, envelopeID,
	).Scan(&status); err != nil {
		t.Fatalf("read legacy projection: %v", err)
	}
	if status != "pending" {
		t.Fatalf("expired wait wrote a terminal legacy projection: status = %q", status)
	}
	var projectedState string
	if err := rg.db.QueryRow(`
SELECT interaction_state FROM legacy_room_history_v12
WHERE room_id = ? AND envelope_id = ?`, roomID, envelopeID).Scan(&projectedState); err != nil {
		t.Fatalf("read compatibility projection: %v", err)
	}
	if projectedState == "" {
		t.Fatal("compatibility projection lost its canonical interaction state")
	}
	if state := interactionState(t, rg, receipt.Handle.InteractionID); state != projectedState {
		t.Fatalf("projection state %q disagrees with canonical state %q", projectedState, state)
	}
}

// ---- helpers -------------------------------------------------------------

func fixtureByTool(t *testing.T, tool string) workflowFixture {
	t.Helper()
	for _, fixture := range shippedRoomWorkflows() {
		if fixture.tool == tool {
			return fixture
		}
	}
	t.Fatalf("no fixture for %q", tool)
	return workflowFixture{}
}

// runFixtureToCompletion drives one workflow to a participant answer and
// returns the tool result with volatile values normalized.
func runFixtureToCompletion(
	t *testing.T,
	rg *sessionRig,
	fixture workflowFixture,
	completion map[string]any,
) any {
	t.Helper()
	defer rg.cleanup()
	roomID, _ := createSession(t, rg, fixture.tool)
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	const envelopeID = "parity-1"
	done := make(chan advanceResult, 1)
	go func() { done <- callWorkflow(t, rg, fixture, roomID, envelopeID, completion, nil) }()

	frame := readWSFrame(t, conn, 5*time.Second)
	if frame["envelopeId"] != envelopeID {
		t.Fatalf("envelopeId = %v, want %s", frame["envelopeId"], envelopeID)
	}
	response := fixture.participantResponse(envelopeID)
	response["revision"] = frame["revision"]
	writeWSFrame(t, conn, response)

	result := <-done
	if result.err != nil {
		t.Fatalf("%s transport err: %v", fixture.tool, result.err)
	}
	if result.result.IsError {
		t.Fatalf("%s IsError=true: %s", fixture.tool, extractText(t, result.result))
	}
	var decoded any
	if err := json.Unmarshal([]byte(extractText(t, result.result)), &decoded); err != nil {
		t.Fatalf("unmarshal %s result: %v", fixture.tool, err)
	}
	return normalizeVolatile(decoded)
}

// volatileGeneratedKeys are server-generated identifiers. Together with any
// key ending in "_at" (and the camelCase completedAt), they are the fields
// Tangent stamps at submission time; two independent runs of the same fixture
// legitimately differ there. Nothing a caller branches on is in this set.
var volatileGeneratedKeys = map[string]bool{
	"completedAt": true, "update_id": true, "checkpoint_id": true,
	"last_update_id": true, "last_checkpoint_id": true, "revision_id": true,
}

func isVolatileKey(key string) bool {
	return volatileGeneratedKeys[key] || strings.HasSuffix(key, "_at")
}

func normalizeVolatile(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if isVolatileKey(key) {
				out[key] = "<normalized>"
				continue
			}
			out[key] = normalizeVolatile(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = normalizeVolatile(child)
		}
		return out
	default:
		return value
	}
}

func decodeReceipt(t *testing.T, res *mcpsdk.CallToolResult) pendingReceipt {
	t.Helper()
	body := extractText(t, res)
	var receipt pendingReceipt
	if err := json.Unmarshal([]byte(body), &receipt); err != nil {
		t.Fatalf("unmarshal pending receipt: %v (body %s)", err, body)
	}
	if receipt.Status != "pending" {
		t.Fatalf("result is not a pending receipt: %s", body)
	}
	if receipt.Handle.InteractionID == "" || receipt.Handle.SurfaceID == "" {
		t.Fatalf("pending receipt has an incomplete handle: %s", body)
	}
	return receipt
}

func errorCodeOf(t *testing.T, res *mcpsdk.CallToolResult) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(extractText(t, res)), &body); err != nil {
		t.Fatalf("unmarshal tool error: %v", err)
	}
	return body.Error.Code
}

func interactionState(t *testing.T, rg *sessionRig, interactionID string) string {
	t.Helper()
	var state string
	if err := rg.db.QueryRow(
		`SELECT lifecycle_state FROM interactions WHERE id = ?`, interactionID,
	).Scan(&state); err != nil {
		t.Fatalf("read interaction state: %v", err)
	}
	return state
}

func awaitInteractionState(t *testing.T, rg *sessionRig, interactionID, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if interactionState(t, rg, interactionID) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("interaction %s did not reach %q within %v (state %q)",
		interactionID, want, timeout, interactionState(t, rg, interactionID))
}

func singleInteractionID(t *testing.T, rg *sessionRig, envelopeID string) string {
	t.Helper()
	var id string
	if err := rg.db.QueryRow(
		`SELECT id FROM interactions WHERE legacy_envelope_id = ?`, envelopeID,
	).Scan(&id); err != nil {
		t.Fatalf("read interaction id for envelope %q: %v", envelopeID, err)
	}
	return id
}

// interactionResolutionPayload calls a durable retrieval tool and returns the
// stored response payload verbatim.
func interactionResolutionPayload(
	t *testing.T,
	rg *sessionRig,
	tool string,
	arguments map[string]any,
) string {
	t.Helper()
	res, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: tool, Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	if res.IsError {
		t.Fatalf("%s IsError=true: %s", tool, extractText(t, res))
	}
	var outcome struct {
		Resolution *struct {
			ResponsePayload json.RawMessage `json:"response_payload"`
		} `json:"resolution"`
	}
	if err := json.Unmarshal([]byte(extractText(t, res)), &outcome); err != nil {
		t.Fatalf("unmarshal %s outcome: %v", tool, err)
	}
	if outcome.Resolution == nil {
		t.Fatalf("%s returned no resolution: %s", tool, extractText(t, res))
	}
	return string(outcome.Resolution.ResponsePayload)
}

func sameJSON(t *testing.T, left, right string) bool {
	t.Helper()
	var leftValue, rightValue any
	if err := json.Unmarshal([]byte(left), &leftValue); err != nil {
		t.Fatalf("unmarshal left: %v (%s)", err, left)
	}
	if err := json.Unmarshal([]byte(right), &rightValue); err != nil {
		t.Fatalf("unmarshal right: %v (%s)", err, right)
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

// assertCounts asserts exactly one interaction and (when resolved) exactly one
// resolution exist for the identity.
func assertCounts(t *testing.T, rg *sessionRig, interactionID string, wantInteractions int) {
	t.Helper()
	var idempotencyKey, callerScope string
	if err := rg.db.QueryRow(
		`SELECT idempotency_key, caller_scope FROM interactions WHERE id = ?`, interactionID,
	).Scan(&idempotencyKey, &callerScope); err != nil {
		t.Fatalf("read interaction identity: %v", err)
	}
	assertRowCount(t, rg, wantInteractions,
		`SELECT COUNT(*) FROM interactions WHERE caller_scope = ? AND idempotency_key = ?`,
		callerScope, idempotencyKey)
	var resolutions int
	if err := rg.db.QueryRow(
		`SELECT COUNT(*) FROM resolutions WHERE interaction_id = ?`, interactionID,
	).Scan(&resolutions); err != nil {
		t.Fatalf("count resolutions: %v", err)
	}
	if resolutions > 1 {
		t.Fatalf("interaction %s has %d resolutions, want at most one", interactionID, resolutions)
	}
}

func assertRowCount(t *testing.T, rg *sessionRig, want int, query string, args ...any) {
	t.Helper()
	var got int
	if err := rg.db.QueryRow(query, args...).Scan(&got); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	if got != want {
		t.Fatalf("count query %q = %d, want %d", query, got, want)
	}
}

// cancelInteractionForTest withdraws a caller-owned interaction so a shared
// room is free for the next fixture.
func cancelInteractionForTest(t *testing.T, rg *sessionRig, roomID, interactionID string) {
	t.Helper()
	var revision int64
	if err := rg.db.QueryRow(`SELECT revision FROM interactions WHERE id = ?`, interactionID).Scan(&revision); err != nil {
		t.Fatalf("read interaction revision: %v", err)
	}
	res, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.interaction_cancel",
		Arguments: map[string]any{
			"interaction_id":    interactionID,
			"expected_revision": revision,
			"requester": map[string]any{
				"scope":         roomflow.DefaultCaller.Scope,
				"principal_ref": roomflow.DefaultCaller.PrincipalRef,
			},
			"cause": "caller_withdrawn",
		},
	})
	if err != nil {
		t.Fatalf("interaction_cancel: %v", err)
	}
	if res.IsError {
		t.Fatalf("interaction_cancel IsError=true: %s", extractText(t, res))
	}
	if rm, ok := rg.mgr.Get(roomID); ok {
		rm.Release(interactionIDEnvelope(t, rg, interactionID))
	}
}

func interactionIDEnvelope(t *testing.T, rg *sessionRig, interactionID string) string {
	t.Helper()
	var envelopeID string
	if err := rg.db.QueryRow(
		`SELECT legacy_envelope_id FROM interactions WHERE id = ?`, interactionID,
	).Scan(&envelopeID); err != nil {
		t.Fatalf("read legacy envelope id: %v", err)
	}
	return envelopeID
}
