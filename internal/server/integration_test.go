package server_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/server"
	tangentws "github.com/hollis-labs/tangent/internal/ws"
)

// rig wires up the full Tangent server in-process, exposing the
// MCP-side client session, the WS handler URL, and the room manager.
// Each sub-test gets its own rig to avoid cross-talk.
type rig struct {
	mgr       *room.Manager
	mcpClient *mcpsdk.ClientSession
	httpURL   string
	cleanup   func()
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := context.Background()

	envSvc, err := envelope.New(ctx)
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if regErr := extensions.RegisterTriage(envSvc); regErr != nil {
		t.Fatalf("RegisterTriage: %v", regErr)
	}
	dispatcher := envelope.NewDispatcher(envSvc)
	mgr := room.NewManager(nil)
	logger := slog.New(slog.NewTextHandler(testLogWriter{t}, &slog.HandlerOptions{Level: slog.LevelWarn}))

	wsHandler := tangentws.New(mgr, logger)
	wsHandler.SetOriginPatterns([]string{"*"})

	mcpSrv, err := tangentmcp.New(envSvc, dispatcher, mgr, "")
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}

	triageHandler := tangentmcp.NewTriageHandler(mgr, logger, "")
	if regErr := tangentmcp.RegisterTriageOnDispatcher(dispatcher, triageHandler); regErr != nil {
		t.Fatalf("RegisterTriageOnDispatcher: %v", regErr)
	}

	httpSrv, err := server.New(server.Config{
		Port:        0, // ignored — we wrap the mux ourselves below.
		Logger:      logger,
		Envelope:    envSvc,
		MCP:         mcpSrv,
		WSHandler:   wsHandler,
		RoomManager: mgr,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	_ = httpSrv // we don't ListenAndServe; we test the WS handler via its own httptest server below.

	// Spin up an httptest server for the WS endpoint. We don't need
	// the full router for the integration test — the MCP client uses
	// in-memory transport, and the WS client connects directly to the
	// /ws handler. (Routing tests live in server_test, not here.)
	wsSrv := httptest.NewServer(wsHandler)

	// Connect the MCP client over the in-memory transport so we
	// exercise the same code path Claude Code would.
	serverT, clientT := mcpsdk.NewInMemoryTransports()
	serverSession, err := mcpSrv.MCP().Connect(ctx, serverT, nil)
	if err != nil {
		wsSrv.Close()
		t.Fatalf("mcp server.Connect: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "tangent-int-test", Version: "v0.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		_ = serverSession.Close()
		wsSrv.Close()
		t.Fatalf("mcp client.Connect: %v", err)
	}

	return &rig{
		mgr:       mgr,
		mcpClient: clientSession,
		httpURL:   wsSrv.URL,
		cleanup: func() {
			_ = clientSession.Close()
			_ = serverSession.Close()
			mgr.CloseAll("test cleanup")
			wsSrv.Close()
		},
	}
}

func (r *rig) wsURL(roomID string) string {
	return "ws" + strings.TrimPrefix(r.httpURL, "http") + "?roomID=" + roomID
}

// TestIntegration_TriageRoundTrip — the v0.1 happy path: MCP client
// calls tangent.triage, the handler creates a Room, the test acts as a
// browser, sends a response, and the MCP call returns the response.
func TestIntegration_TriageRoundTrip(t *testing.T) {
	rg := newRig(t)
	defer rg.cleanup()

	envelopeArg := triageEnvelopeArg("int-rt-1")

	// Kick off the MCP call asynchronously — we need to drive both
	// sides (MCP caller + WS browser) in parallel.
	mcpDone := make(chan struct{})
	var mcpRes *mcpsdk.CallToolResult
	var mcpErr error
	go func() {
		defer close(mcpDone)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		mcpRes, mcpErr = rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{
			Name:      "tangent.triage",
			Arguments: map[string]any{"envelope": envelopeArg},
		})
	}()

	// Wait for the room to appear (handler creates it before Push).
	rm := awaitRoom(t, rg.mgr, 2*time.Second)

	// "Browser" connects, reads the envelope, replies.
	clientConn, _, err := websocket.Dial(context.Background(), rg.wsURL(rm.ID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer clientConn.Close(websocket.StatusNormalClosure, "test done")

	frame := readFrame(t, clientConn, 3*time.Second)
	if frame["type"] != "envelope" {
		t.Fatalf("expected envelope, got %+v", frame)
	}
	if frame["envelopeId"] != "int-rt-1" {
		t.Fatalf("envelopeId = %v, want int-rt-1", frame["envelopeId"])
	}

	writeFrame(t, clientConn, map[string]any{
		"type":       "response",
		"envelopeId": "int-rt-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "int-rt-1",
			"kind":       "data",
			"status":     "submitted",
			"payload":    map[string]any{"accepted": true},
		},
	})

	<-mcpDone
	if mcpErr != nil {
		t.Fatalf("MCP CallTool: %v", mcpErr)
	}
	if mcpRes.IsError {
		t.Fatalf("expected success, got IsError=true: %v", textOf(mcpRes))
	}

	body := textOf(mcpRes)
	var resp envelopes.Response
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (body=%s)", err, body)
	}
	if resp.EnvelopeID != "int-rt-1" {
		t.Errorf("response EnvelopeID = %q, want int-rt-1", resp.EnvelopeID)
	}
	if resp.Kind != envelopes.ResponseKindData {
		t.Errorf("response kind = %q, want data", resp.Kind)
	}
	payload, _ := resp.Payload.(map[string]any)
	if accepted, _ := payload["accepted"].(bool); !accepted {
		t.Errorf("expected payload.accepted=true, got %v", resp.Payload)
	}
}

// TestIntegration_DisconnectFailsCall — closing the WS mid-call yields
// a structured error to the MCP caller (no hang).
func TestIntegration_DisconnectFailsCall(t *testing.T) {
	rg := newRig(t)
	defer rg.cleanup()

	envelopeArg := triageEnvelopeArg("int-drop-1")

	mcpDone := make(chan struct{})
	var mcpRes *mcpsdk.CallToolResult
	var mcpErr error
	go func() {
		defer close(mcpDone)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		mcpRes, mcpErr = rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{
			Name:      "tangent.triage",
			Arguments: map[string]any{"envelope": envelopeArg},
		})
	}()

	rm := awaitRoom(t, rg.mgr, 2*time.Second)
	clientConn, _, err := websocket.Dial(context.Background(), rg.wsURL(rm.ID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	_ = readFrame(t, clientConn, 3*time.Second)
	_ = clientConn.Close(websocket.StatusGoingAway, "tab closed")

	select {
	case <-mcpDone:
	case <-time.After(5 * time.Second):
		t.Fatal("MCP call hung after disconnect")
	}
	if mcpErr != nil {
		t.Fatalf("MCP CallTool transport error: %v", mcpErr)
	}
	if !mcpRes.IsError {
		t.Fatalf("expected IsError=true after disconnect, got success: %v", textOf(mcpRes))
	}
	body := textOf(mcpRes)
	if !strings.Contains(body, "ROOM_DISCONNECTED") {
		t.Errorf("expected ROOM_DISCONNECTED in body, got %q", body)
	}
}

// TestIntegration_Cancel — client sends cancel; MCP call returns a
// structured cancelled-response (kind=ack, status=cancelled).
func TestIntegration_Cancel(t *testing.T) {
	rg := newRig(t)
	defer rg.cleanup()

	envelopeArg := triageEnvelopeArg("int-cancel-1")

	mcpDone := make(chan struct{})
	var mcpRes *mcpsdk.CallToolResult
	var mcpErr error
	go func() {
		defer close(mcpDone)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		mcpRes, mcpErr = rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{
			Name:      "tangent.triage",
			Arguments: map[string]any{"envelope": envelopeArg},
		})
	}()

	rm := awaitRoom(t, rg.mgr, 2*time.Second)
	clientConn, _, err := websocket.Dial(context.Background(), rg.wsURL(rm.ID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer clientConn.Close(websocket.StatusNormalClosure, "test done")

	_ = readFrame(t, clientConn, 3*time.Second)
	writeFrame(t, clientConn, map[string]any{
		"type":       "cancel",
		"envelopeId": "int-cancel-1",
	})

	<-mcpDone
	if mcpErr != nil {
		t.Fatalf("MCP CallTool: %v", mcpErr)
	}
	if mcpRes.IsError {
		t.Fatalf("expected success (cancel synthesizes ack), got IsError=true: %v", textOf(mcpRes))
	}

	var resp envelopes.Response
	if err := json.Unmarshal([]byte(textOf(mcpRes)), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Kind != envelopes.ResponseKindAck {
		t.Errorf("kind = %q, want ack", resp.Kind)
	}
	if resp.Status != envelopes.ResponseStatusCancelled {
		t.Errorf("status = %q, want cancelled", resp.Status)
	}
}

// TestIntegration_ParallelRooms — two MCP calls in flight, two WS
// connections, no cross-talk; each returns its own response.
func TestIntegration_ParallelRooms(t *testing.T) {
	rg := newRig(t)
	defer rg.cleanup()

	type outcome struct {
		body  string
		err   error
		isErr bool
	}
	doneA := make(chan outcome, 1)
	doneB := make(chan outcome, 1)

	call := func(envID string, out chan<- outcome) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res, err := rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{
			Name:      "tangent.triage",
			Arguments: map[string]any{"envelope": triageEnvelopeArg(envID)},
		})
		oc := outcome{err: err}
		if res != nil {
			oc.isErr = res.IsError
			oc.body = textOf(res)
		}
		out <- oc
	}

	go call("int-par-A", doneA)
	go call("int-par-B", doneB)

	// Each call creates a room — wait until we have two distinct ones.
	roomAID, roomBID := awaitTwoRooms(t, rg.mgr, 3*time.Second)

	// Connect two clients, one per room. Use a WaitGroup so each side
	// drives its own envelope independently.
	var wg sync.WaitGroup
	wg.Add(2)
	driveRoom := func(roomID, envID string) {
		defer wg.Done()
		c, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
		if err != nil {
			t.Errorf("dial %s: %v", roomID, err)
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "done")

		frame := readFrame(t, c, 3*time.Second)
		gotID, _ := frame["envelopeId"].(string)
		if gotID != envID {
			t.Errorf("room %s expected envelope %q, got %q (cross-talk?)", roomID, envID, gotID)
		}
		writeFrame(t, c, map[string]any{
			"type":       "response",
			"envelopeId": envID,
			"response": map[string]any{
				"v":          1,
				"envelopeId": envID,
				"kind":       "data",
				"status":     "submitted",
				"payload":    map[string]any{"id": envID},
			},
		})
	}
	// We don't know which room hosts which envelope, but the read
	// frame tells us; drive both rooms and let the assertion catch
	// mis-routing.
	go driveRoom(roomAID, getRoomEnvelopeID(t, rg.mgr, roomAID))
	go driveRoom(roomBID, getRoomEnvelopeID(t, rg.mgr, roomBID))
	wg.Wait()

	resA := <-doneA
	resB := <-doneB
	for _, r := range []outcome{resA, resB} {
		if r.err != nil {
			t.Errorf("call err: %v", r.err)
		}
		if r.isErr {
			t.Errorf("expected success, got IsError=true: %s", r.body)
		}
	}
	// Validate the bodies are distinct.
	if resA.body == resB.body {
		t.Errorf("expected distinct response bodies for parallel calls; got identical:\n%s", resA.body)
	}
	if !strings.Contains(resA.body, "int-par-") {
		t.Errorf("body A missing envelopeId marker: %s", resA.body)
	}
}

// triageEnvelopeArg returns the envelope-arg map for a tangent.triage
// call. Centralized so tests stay focused on outcome, not shape.
func triageEnvelopeArg(id string) map[string]any {
	return map[string]any{
		"v":    1,
		"id":   id,
		"type": "tangent.triage",
		"data": map[string]any{
			"prompt": "is this thing on?",
			"items":  []any{"first", "second"},
		},
	}
}

// awaitRoom polls the manager until exactly one room exists or the
// timeout fires. Returns that room.
func awaitRoom(t *testing.T, mgr *room.Manager, timeout time.Duration) *room.Room {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ids := mgr.IDs()
		if len(ids) == 1 {
			r, _ := mgr.Get(ids[0])
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no room appeared within %v (got %d)", timeout, mgr.Len())
	return nil
}

// awaitTwoRooms waits for exactly two rooms to exist.
func awaitTwoRooms(t *testing.T, mgr *room.Manager, timeout time.Duration) (string, string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ids := mgr.IDs()
		if len(ids) >= 2 {
			return ids[0], ids[1]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("two rooms did not appear within %v (got %d)", timeout, mgr.Len())
	return "", ""
}

// getRoomEnvelopeID returns the envelopeID stored in the Room's Meta
// (set by NewTriageHandler when it creates the room).
func getRoomEnvelopeID(t *testing.T, mgr *room.Manager, roomID string) string {
	t.Helper()
	r, ok := mgr.Get(roomID)
	if !ok {
		t.Fatalf("room %s not found", roomID)
	}
	return r.MetaCopy()["envelopeID"]
}

// readFrame reads a single text frame and unmarshals.
func readFrame(t *testing.T, c *websocket.Conn, timeout time.Duration) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	mt, payload, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if mt != websocket.MessageText {
		t.Fatalf("expected text frame, got %v", mt)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

// writeFrame marshals and writes.
func writeFrame(t *testing.T, c *websocket.Conn, msg map[string]any) {
	t.Helper()
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// textOf extracts the first text-content block; integration helper.
func textOf(res *mcpsdk.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	tc, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		return ""
	}
	return tc.Text
}

// testLogWriter pipes slog output into t.Log so test output stays
// readable. Discarding the writes outright (io.Discard) hides
// signal during local debugging; using t.Log keeps it visible only
// when -v is set.
type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Helper()
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
