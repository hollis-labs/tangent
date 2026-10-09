package ws_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	envelopes "github.com/hollis-labs/libs/ui-go/envelopes"

	"github.com/hollis-labs/tangent/internal/room"
	tangentws "github.com/hollis-labs/tangent/internal/ws"
)

// newTestRig spins up an httptest server that fronts the WS handler.
// Returns the manager, the http URL (no scheme conversion), and a
// cleanup that shuts the server down.
func newTestRig(t *testing.T) (*room.Manager, string, func()) {
	t.Helper()
	mgr := room.NewManager(nil)
	h := tangentws.New(mgr, nil)
	h.SetOriginPatterns([]string{"*"})
	srv := httptest.NewServer(h)
	return mgr, srv.URL, srv.Close
}

// newHandlerServer fronts a caller-configured handler. Used by tests that
// install their own participant resolver.
func newHandlerServer(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func wsURL(httpURL, roomID string) string {
	u := "ws" + strings.TrimPrefix(httpURL, "http")
	if roomID == "" {
		return u
	}
	return u + "?roomID=" + roomID
}

// TestHandler_Missing400 — no roomID query → 400.
func TestHandler_Missing400(t *testing.T) {
	_, base, cleanup := newTestRig(t)
	defer cleanup()

	resp, err := http.Get(base) //nolint:gosec // base is httptest local URL
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 400, got %d (%s)", resp.StatusCode, string(body))
	}
}

// TestHandler_Unknown404 — roomID for non-existent room → 404.
func TestHandler_Unknown404(t *testing.T) {
	_, base, cleanup := newTestRig(t)
	defer cleanup()

	resp, err := http.Get(base + "?roomID=does-not-exist") //nolint:gosec // base is httptest local URL
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 404, got %d (%s)", resp.StatusCode, string(body))
	}
}

// TestHandler_UpgradeAndPush — connect to existing room, server pushes
// envelope, client receives JSON.
func TestHandler_UpgradeAndPush(t *testing.T) {
	mgr, base, cleanup := newTestRig(t)
	defer cleanup()

	rm := mgr.Create(nil)

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()
	clientConn, _, err := websocket.Dial(dialCtx, wsURL(base, rm.ID), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer clientConn.Close(websocket.StatusNormalClosure, "test done")

	// Wait for the handler to attach the conn before pushing.
	waitForConn(t, rm, 1*time.Second)

	env := &envelopes.Envelope{V: 1, ID: "h-1", Type: "triage"}
	pushDone := make(chan error, 1)
	respCh := make(chan *envelopes.Response, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := rm.Push(ctx, env)
		if resp != nil {
			respCh <- resp
		}
		pushDone <- err
	}()

	frame := readFrame(t, clientConn, 2*time.Second)
	if frame["type"] != "envelope" {
		t.Fatalf("expected envelope, got %+v", frame)
	}
	if frame["envelopeId"] != "h-1" {
		t.Fatalf("envelopeId mismatch: %v", frame["envelopeId"])
	}

	// Reply.
	writeFrame(t, clientConn, map[string]any{
		"type":       "response",
		"envelopeId": "h-1",
		"revision":   frame["revision"],
		"response": map[string]any{
			"v":          1,
			"envelopeId": "h-1",
			"kind":       "data",
			"status":     "submitted",
			"payload":    map[string]any{"ok": true},
		},
	})

	if err := <-pushDone; err != nil {
		t.Fatalf("push: %v", err)
	}
	resp := <-respCh
	if resp.EnvelopeID != "h-1" {
		t.Errorf("response EnvelopeID = %q, want h-1", resp.EnvelopeID)
	}
}

// TestHandler_DisconnectPreservesPending — client transport loss changes only
// connection state. A replacement connection receives the pending envelope at
// a newer revision and can resolve the original Push.
func TestHandler_DisconnectPreservesPending(t *testing.T) {
	mgr, base, cleanup := newTestRig(t)
	defer cleanup()

	rm := mgr.Create(nil)

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()
	clientConn, _, err := websocket.Dial(dialCtx, wsURL(base, rm.ID), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	waitForConn(t, rm, 1*time.Second)

	env := &envelopes.Envelope{V: 1, ID: "h-drop", Type: "triage"}
	pushDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, pushErr := rm.Push(ctx, env)
		pushDone <- pushErr
	}()

	first := readFrame(t, clientConn, 2*time.Second)
	// Client slams the door.
	_ = clientConn.Close(websocket.StatusGoingAway, "tab closed")

	replacement, _, err := websocket.Dial(dialCtx, wsURL(base, rm.ID), nil)
	if err != nil {
		t.Fatalf("replacement dial: %v", err)
	}
	defer replacement.Close(websocket.StatusNormalClosure, "test done")
	replayed := readFrame(t, replacement, 2*time.Second)
	if replayed["envelopeId"] != "h-drop" || replayed["revision"].(float64) <= first["revision"].(float64) {
		t.Fatalf("replayed frame = %+v, first = %+v", replayed, first)
	}
	writeFrame(t, replacement, map[string]any{
		"type":       "response",
		"envelopeId": "h-drop",
		"revision":   replayed["revision"],
		"response": map[string]any{
			"v":          1,
			"envelopeId": "h-drop",
			"kind":       "data",
			"status":     "submitted",
			"payload":    map[string]any{"reconnected": true},
		},
	})

	if err := <-pushDone; err != nil {
		t.Fatalf("Push after reconnect: %v", err)
	}
	if rm.IsClosed() || !rm.HasConn() {
		t.Fatalf("reconnect state: closed=%v connected=%v", rm.IsClosed(), rm.HasConn())
	}
}

// TestHandler_Cancel — client sends cancel; Push returns user-cancel error.
func TestHandler_Cancel(t *testing.T) {
	mgr, base, cleanup := newTestRig(t)
	defer cleanup()

	rm := mgr.Create(nil)
	clientConn, _, err := websocket.Dial(context.Background(), wsURL(base, rm.ID), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer clientConn.Close(websocket.StatusNormalClosure, "test done")

	waitForConn(t, rm, 1*time.Second)

	env := &envelopes.Envelope{V: 1, ID: "h-c", Type: "triage"}
	pushDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, err := rm.Push(ctx, env)
		pushDone <- err
	}()

	frame := readFrame(t, clientConn, 2*time.Second)
	writeFrame(t, clientConn, map[string]any{
		"type":       "cancel",
		"envelopeId": "h-c",
		"revision":   frame["revision"],
	})

	select {
	case err := <-pushDone:
		if err == nil || !strings.Contains(err.Error(), "user cancelled") {
			t.Errorf("expected user-cancel error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Push did not honor cancel")
	}
}

// waitForConn polls Room.HasConn until true or timeout. Used because
// AttachConn happens in a goroutine after the upgrade.
func waitForConn(t *testing.T, rm *room.Room, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if rm.HasConn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("room never received WS conn within %v", timeout)
}

// readFrame reads the next presentation frame, skipping the connection
// lifecycle frames the handler sends on attach. Tests that assert on those use
// readFrameOfType.
func readFrame(t *testing.T, c *websocket.Conn, timeout time.Duration) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		frame := readFrameCtx(ctx, t, c)
		switch frame["type"] {
		case "connection", "sync":
			continue
		default:
			return frame
		}
	}
}

// readFrameOfType reads frames until one of the requested type arrives.
func readFrameOfType(
	t *testing.T,
	c *websocket.Conn,
	frameType string,
	timeout time.Duration,
) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		if frame := readFrameCtx(ctx, t, c); frame["type"] == frameType {
			return frame
		}
	}
}

func readFrameCtx(ctx context.Context, t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	mt, payload, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if mt != websocket.MessageText {
		t.Fatalf("expected text frame, got %v", mt)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("unmarshal: %v (payload=%s)", err, string(payload))
	}
	return m
}

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
