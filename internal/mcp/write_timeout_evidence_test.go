package mcp_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/roomflow"
)

// TestOperatorCompletionSurvivesCallerWriteTimeout is the regression for the
// incident this whole change exists to fix.
//
// Approval-queue room b2510de2-a7ea-4eae-a903-84d9b83410b2 stayed open about
// twenty-two minutes. The operator submitted successfully and SQLite durably
// recorded the result at 2026-09-04T12:57:17Z, but the caller's Streamable HTTP
// request had already blown past Tangent's 60-second WriteTimeout: curl exited
// 52 ("Empty reply from server") and both the captured response and stderr
// files were zero bytes. The human's answer survived; the caller had no result
// and no durable identity through that request.
//
// The test reproduces exactly that, deterministically, by compressing the write
// timeout to a few hundred milliseconds instead of waiting out a real one — and
// by deliberately mounting the MCP handler WITHOUT the deadline-clearing
// wrapper, which is what the v0.12 transport did. It then asserts the half the
// old system got right (the durable result) and the half it got wrong (the
// caller's ability to come back for it).
func TestOperatorCompletionSurvivesCallerWriteTimeout(t *testing.T) {
	const (
		writeTimeout = 300 * time.Millisecond
		window       = 4 * time.Second
		envelopeID   = "write-timeout-evidence-1"
	)

	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: window})
	defer rg.cleanup()
	fixture := fixtureByTool(t, "tangent.approval-queue")
	roomID, _ := createSession(t, rg, "write-timeout-evidence")

	// A transport with the v0.12 deadline behavior: the response write deadline
	// is never cleared, so a handler that outlives it loses its connection.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	legacyTransport := &http.Server{
		Handler:           rg.mcpSrv.HTTPHandler(),
		ReadHeaderTimeout: time.Second,
		WriteTimeout:      writeTimeout,
	}
	go func() { _ = legacyTransport.Serve(listener) }()
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = legacyTransport.Shutdown(shutdownCtx)
	})

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name": fixture.tool,
			"arguments": map[string]any{
				"envelope": map[string]any{
					"v": 1, "id": envelopeID, "type": fixture.envelopeType,
					"data": fixture.data, "meta": map[string]any{"roomID": roomID},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal tools/call: %v", err)
	}

	type callerResult struct {
		status int
		body   []byte
		err    error
	}
	callerDone := make(chan callerResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		request, requestErr := http.NewRequestWithContext(
			ctx, http.MethodPost, "http://"+listener.Addr().String()+"/", bytes.NewReader(body))
		if requestErr != nil {
			callerDone <- callerResult{err: requestErr}
			return
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		response, callErr := http.DefaultClient.Do(request)
		if callErr != nil {
			callerDone <- callerResult{err: callErr}
			return
		}
		defer func() { _ = response.Body.Close() }()
		payload, readErr := io.ReadAll(response.Body)
		callerDone <- callerResult{status: response.StatusCode, body: payload, err: readErr}
	}()

	// The caller's transport dies before the human answers. This is the exact
	// symptom the incident produced: nothing usable comes back.
	caller := <-callerDone
	if caller.err == nil && len(caller.body) > 0 {
		t.Fatalf("expected the compressed write timeout to truncate the caller response, got %d bytes: %s",
			len(caller.body), caller.body)
	}

	// The operator answers afterwards, exactly as they did in the incident.
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	frame := readWSFrame(t, conn, 5*time.Second)
	if frame["envelopeId"] != envelopeID {
		t.Fatalf("operator saw envelopeId %v, want %s", frame["envelopeId"], envelopeID)
	}
	response := fixture.participantResponse(envelopeID)
	response["revision"] = frame["revision"]
	writeWSFrame(t, conn, response)

	interactionID := singleInteractionID(t, rg, envelopeID)
	awaitInteractionState(t, rg, interactionID, "resolved", 5*time.Second)

	// What v0.12 already got right: the durable room result exists. The legacy
	// projection is written after the canonical resolution — deliberately, so
	// the projection can never be ahead of the record it projects — so this
	// waits for it rather than assuming the two land together.
	status, payload := awaitLegacyProjection(t, rg, roomID, envelopeID, 5*time.Second)
	if status != "submitted" || payload == "" {
		t.Fatalf("durable room result = status %q payload %q", status, payload)
	}

	// What v0.12 got wrong: the caller can now come back for it, by handle and
	// by retrying the identical invocation, and both return the same immutable
	// result the operator produced.
	stored := interactionResolutionPayload(t, rg, "tangent.interaction_get", map[string]any{
		"interaction_id":  interactionID,
		"requester_scope": roomflow.DefaultCaller.Scope,
	})
	retry := callWorkflow(t, rg, fixture, roomID, envelopeID, nil, nil)
	if retry.err != nil {
		t.Fatalf("retry transport err: %v", retry.err)
	}
	if retry.result.IsError {
		t.Fatalf("retry produced an MCP error: %s", extractText(t, retry.result))
	}
	if !sameJSON(t, stored, extractText(t, retry.result)) {
		t.Fatalf("recovered result diverged:\nstored: %s\nretry:  %s", stored, extractText(t, retry.result))
	}
	assertCounts(t, rg, interactionID, 1)
}

// TestLostCallerTransportNeverTerminalizes isolates the lifecycle half of the
// same claim: a caller that vanishes mid-wait changes nothing about the
// interaction it was waiting on.
func TestLostCallerTransportNeverTerminalizes(t *testing.T) {
	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: 10 * time.Second})
	defer rg.cleanup()
	fixture := fixtureByTool(t, "tangent.triage")
	roomID, _ := createSession(t, rg, "lost-transport")

	const envelopeID = "lost-transport-1"
	callCtx, cancelCall := context.WithCancel(context.Background())
	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		_, _ = rg.mcpClient.CallTool(callCtx, &mcpsdk.CallToolParams{
			Name: fixture.tool,
			Arguments: map[string]any{"envelope": map[string]any{
				"v": 1, "id": envelopeID, "type": fixture.envelopeType,
				"data": fixture.data, "meta": map[string]any{"roomID": roomID},
			}},
		})
	}()
	awaitPendingRoom(t, rg.mgr, roomID, 3*time.Second)
	cancelCall()
	<-callDone

	interactionID := singleInteractionID(t, rg, envelopeID)
	// Give any (incorrect) cancellation-on-disconnect a chance to land.
	time.Sleep(150 * time.Millisecond)
	if state := interactionState(t, rg, interactionID); state != "staged" && state != "presented" {
		t.Fatalf("caller disconnect changed the interaction lifecycle: state = %q", state)
	}

	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	frame := readWSFrame(t, conn, 3*time.Second)
	response := fixture.participantResponse(envelopeID)
	response["revision"] = frame["revision"]
	writeWSFrame(t, conn, response)
	awaitInteractionState(t, rg, interactionID, "resolved", 3*time.Second)
	assertCounts(t, rg, interactionID, 1)
}

// awaitLegacyProjection waits for the v0.12 envelopes row to catch up with the
// canonical resolution and returns its terminal status and payload.
func awaitLegacyProjection(
	t *testing.T,
	rg *sessionRig,
	roomID string,
	envelopeID string,
	timeout time.Duration,
) (string, string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var status string
		var payload sql.NullString
		if err := rg.db.QueryRow(`
SELECT status, response_payload FROM envelopes WHERE room_id = ? AND envelope_id = ?`,
			roomID, envelopeID).Scan(&status, &payload); err != nil {
			t.Fatalf("read legacy projection: %v", err)
		}
		if status != "pending" {
			return status, payload.String
		}
		if time.Now().After(deadline) {
			t.Fatalf("legacy projection stayed pending for %v", timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
