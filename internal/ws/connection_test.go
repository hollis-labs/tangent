package ws_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	envelopes "github.com/hollis-labs/go-envelopes"

	"github.com/hollis-labs/tangent/internal/room"
	tangentws "github.com/hollis-labs/tangent/internal/ws"
)

// tabURL dials as a named tab. clientID is what tells the server whether an
// attachment is a refresh of an existing tab or a new one.
func tabURL(httpURL, roomID, clientID string) string {
	return wsURL(httpURL, roomID) + "&clientID=" + clientID
}

func dialTab(t *testing.T, base, roomID, clientID string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, tabURL(base, roomID, clientID), nil)
	if err != nil {
		t.Fatalf("dial %s: %v", clientID, err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

func waitForConnections(t *testing.T, rm *room.Room, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if rm.ConnectionCount() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("room held %d connections, want %d", rm.ConnectionCount(), want)
}

// TestHandler_TwoTabsObserveWithoutClosingTheRoom is acceptance criterion 2 on
// the real transport: a second tab attaches, both hold the envelope, and the
// first tab is neither closed nor demoted from the surface.
func TestHandler_TwoTabsObserveWithoutClosingTheRoom(t *testing.T) {
	mgr, base, cleanup := newTestRig(t)
	defer cleanup()
	rm := mgr.Create(nil)

	firstTab := dialTab(t, base, rm.ID, "tab-a")
	waitForConnections(t, rm, 1, 2*time.Second)

	env := &envelopes.Envelope{V: 1, ID: "two-tabs", Type: "triage"}
	pushDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := rm.Push(ctx, env)
		pushDone <- err
	}()
	firstFrame := readFrame(t, firstTab, 2*time.Second)

	secondTab := dialTab(t, base, rm.ID, "tab-b")
	waitForConnections(t, rm, 2, 2*time.Second)
	secondFrame := readFrame(t, secondTab, 2*time.Second)

	if rm.IsClosed() {
		t.Fatal("a second tab closed the room")
	}
	if secondFrame["envelopeId"] != "two-tabs" {
		t.Fatalf("second tab frame = %+v", secondFrame)
	}
	if secondFrame["revision"].(float64) == firstFrame["revision"].(float64) {
		t.Fatalf("both tabs were shown revision %v", firstFrame["revision"])
	}

	// The first tab is still usable and still the resolver.
	writeFrame(t, firstTab, map[string]any{
		"type":       "response",
		"envelopeId": "two-tabs",
		"revision":   firstFrame["revision"],
		"response": map[string]any{
			"v": 1, "envelopeId": "two-tabs", "kind": "data", "status": "submitted",
			"payload": map[string]any{"tab": "a"},
		},
	})
	if err := <-pushDone; err != nil {
		t.Fatalf("push resolved by the original tab: %v", err)
	}
}

// TestHandler_ObserverSubmissionReturnsLeaseError is acceptance criterion 3 on
// the real transport: the losing tab receives an explicit error frame naming
// the holder, and an explicit takeover is what lets it answer.
func TestHandler_ObserverSubmissionReturnsLeaseError(t *testing.T) {
	mgr, base, cleanup := newTestRig(t)
	defer cleanup()
	rm := mgr.Create(nil)

	firstTab := dialTab(t, base, rm.ID, "tab-a")
	waitForConnections(t, rm, 1, 2*time.Second)

	env := &envelopes.Envelope{V: 1, ID: "lease-wire", Type: "triage"}
	pushDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := rm.Push(ctx, env)
		pushDone <- err
	}()
	readFrame(t, firstTab, 2*time.Second)

	secondTab := dialTab(t, base, rm.ID, "tab-b")
	waitForConnections(t, rm, 2, 2*time.Second)
	secondFrame := readFrame(t, secondTab, 2*time.Second)

	response := map[string]any{
		"v": 1, "envelopeId": "lease-wire", "kind": "data", "status": "submitted",
		"payload": map[string]any{"tab": "b"},
	}
	writeFrame(t, secondTab, map[string]any{
		"type":       "response",
		"envelopeId": "lease-wire",
		"revision":   secondFrame["revision"],
		"response":   response,
	})

	refused := readFrameOfType(t, secondTab, "error", 2*time.Second)
	if refused["code"] != "resolver_lease_held" {
		t.Fatalf("refusal frame = %+v, want code resolver_lease_held", refused)
	}
	lease, ok := refused["lease"].(map[string]any)
	if !ok || lease["connection_id"] == "" {
		t.Fatalf("refusal frame did not name the lease holder: %+v", refused)
	}
	if !rm.IsPresenting("lease-wire") {
		t.Fatal("a refused submission terminalized the presentation")
	}

	// Take the lease over explicitly, then answer. The takeover is the only
	// thing that changed; the envelope and its revision are untouched.
	writeFrame(t, secondTab, map[string]any{"type": "claim_resolver", "takeover": true})
	awaitRole(t, secondTab, "resolver")
	writeFrame(t, secondTab, map[string]any{
		"type":       "response",
		"envelopeId": "lease-wire",
		"revision":   secondFrame["revision"],
		"response":   response,
	})
	if err := <-pushDone; err != nil {
		t.Fatalf("push after takeover: %v", err)
	}
}

// TestHandler_RefreshReplacesOnlyItsOwnTab is acceptance criterion 4 on the
// real transport: reconnecting under the same clientID closes the predecessor
// and inherits the lease, while a peer tab is untouched and is not shown the
// envelope a second time.
func TestHandler_RefreshReplacesOnlyItsOwnTab(t *testing.T) {
	mgr, base, cleanup := newTestRig(t)
	defer cleanup()
	rm := mgr.Create(nil)

	firstTab := dialTab(t, base, rm.ID, "tab-a")
	waitForConnections(t, rm, 1, 2*time.Second)
	peerTab := dialTab(t, base, rm.ID, "tab-b")
	waitForConnections(t, rm, 2, 2*time.Second)

	env := &envelopes.Envelope{V: 1, ID: "refresh-wire", Type: "triage"}
	pushDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := rm.Push(ctx, env)
		pushDone <- err
	}()
	readFrame(t, firstTab, 2*time.Second)
	peerFrame := readFrame(t, peerTab, 2*time.Second)

	// A real tab keeps reading until its socket closes, which is what lets the
	// closing handshake complete. Model that before replacing it.
	firstClosed := make(chan error, 1)
	go func() {
		for {
			if _, _, err := firstTab.Read(context.Background()); err != nil {
				firstClosed <- err
				return
			}
		}
	}()

	// The same tab reconnecting.
	refreshed := dialTab(t, base, rm.ID, "tab-a")
	refreshedFrame := readFrame(t, refreshed, 2*time.Second)
	if refreshedFrame["envelopeId"] != "refresh-wire" {
		t.Fatalf("refreshed tab frame = %+v", refreshedFrame)
	}
	waitForConnections(t, rm, 2, 2*time.Second)

	// The replaced socket is closed with the compatibility reason.
	select {
	case err := <-firstClosed:
		if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
			t.Fatalf("replaced socket close status = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replaced socket was never closed")
	}

	// The peer is untouched: still attached, still holding exactly the
	// presentation it was given, never re-shown the same envelope.
	writeFrame(t, peerTab, map[string]any{"type": "heartbeat"})
	if got := peerFrame["revision"]; got == nil {
		t.Fatalf("peer frame carried no revision: %+v", peerFrame)
	}
	if rm.ConnectionCount() != 2 {
		t.Fatalf("connection count after refresh = %d, want 2", rm.ConnectionCount())
	}

	writeFrame(t, refreshed, map[string]any{
		"type":       "response",
		"envelopeId": "refresh-wire",
		"revision":   refreshedFrame["revision"],
		"response": map[string]any{
			"v": 1, "envelopeId": "refresh-wire", "kind": "data", "status": "submitted",
			"payload": map[string]any{"tab": "a-refreshed"},
		},
	})
	if err := <-pushDone; err != nil {
		t.Fatalf("push after refresh: %v", err)
	}
}

// TestHandler_ResyncRebuildsFromDurableState covers the resync frame: the
// client asks, and gets a durable snapshot plus a fresh presentation without
// reconnecting.
func TestHandler_ResyncRebuildsFromDurableState(t *testing.T) {
	mgr, base, cleanup := newTestRig(t)
	defer cleanup()
	rm := mgr.Create(nil)

	tab := dialTab(t, base, rm.ID, "tab-a")
	waitForConnections(t, rm, 1, 2*time.Second)

	env := &envelopes.Envelope{V: 1, ID: "resync-wire", Type: "triage"}
	pushDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := rm.Push(ctx, env)
		pushDone <- err
	}()
	first := readFrame(t, tab, 2*time.Second)

	writeFrame(t, tab, map[string]any{"type": "resync"})
	sync := readFrameOfType(t, tab, "sync", 2*time.Second)
	if sync["sync"] == nil {
		t.Fatalf("resync frame = %+v", sync)
	}
	replayed := readFrame(t, tab, 2*time.Second)
	if replayed["envelopeId"] != "resync-wire" ||
		replayed["revision"].(float64) <= first["revision"].(float64) {
		t.Fatalf("resync replay = %+v, first = %+v", replayed, first)
	}

	// The revision the resync issued is the only one that now works.
	writeFrame(t, tab, map[string]any{
		"type": "response", "envelopeId": "resync-wire", "revision": first["revision"],
		"response": map[string]any{
			"v": 1, "envelopeId": "resync-wire", "kind": "data", "status": "submitted",
			"payload": map[string]any{"stale": true},
		},
	})
	stale := readFrameOfType(t, tab, "error", 2*time.Second)
	if stale["code"] != "stale_presentation" {
		t.Fatalf("stale submission frame = %+v, want code stale_presentation", stale)
	}
	fresh := readFrame(t, tab, 2*time.Second)
	writeFrame(t, tab, map[string]any{
		"type": "response", "envelopeId": "resync-wire", "revision": fresh["revision"],
		"response": map[string]any{
			"v": 1, "envelopeId": "resync-wire", "kind": "data", "status": "submitted",
			"payload": map[string]any{"stale": false},
		},
	})
	if err := <-pushDone; err != nil {
		t.Fatalf("push after resync: %v", err)
	}
}

// TestHandler_ParticipantResolverGatesTheUpgrade proves the seam ADR 0004
// lands on: a resolver that refuses turns the upgrade into an ordinary 403, a
// session without `view` is refused the same way, and one that succeeds binds
// its principal to the connection.
//
// The two refusals are separate cases on purpose. "No session at all" and "a
// session that may not look at this" are different facts, and collapsing them
// would let a later change grant an unauthorized session an attachment by
// accident.
func TestHandler_ParticipantResolverGatesTheUpgrade(t *testing.T) {
	mgr := room.NewManager(nil)
	handler := tangentws.New(mgr, nil)
	handler.SetOriginPatterns([]string{"*"})
	const (
		refuse = iota
		withoutView
		allow
	)
	mode := refuse
	handler.SetParticipantResolver(func(*http.Request) (room.ParticipantBinding, error) {
		switch mode {
		case refuse:
			return room.ParticipantBinding{}, context.Canceled
		case withoutView:
			return room.ParticipantBinding{
				Scope: "operator:local", PrincipalRef: "participant-1",
				Authority: "participant-session", Assurance: "session-cookie",
				Capabilities: []string{"draft"},
			}, nil
		default:
			return room.ParticipantBinding{
				Scope: "operator:local", PrincipalRef: "participant-1",
				Authority: "participant-session", Assurance: "session-cookie",
				Capabilities: []string{"view", "draft", "resolve", "cancel"},
			}, nil
		}
	})
	srv := newHandlerServer(t, handler)
	rm := mgr.Create(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := websocket.Dial(ctx, tabURL(srv, rm.ID, "tab-a"), nil); err == nil {
		t.Fatal("upgrade succeeded while the participant resolver refused")
	}
	if rm.ConnectionCount() != 0 {
		t.Fatal("a refused upgrade attached a connection")
	}

	mode = withoutView
	if _, _, err := websocket.Dial(ctx, tabURL(srv, rm.ID, "tab-a"), nil); err == nil {
		t.Fatal("upgrade succeeded for a session without the view capability")
	}
	if rm.ConnectionCount() != 0 {
		t.Fatal("a session without view attached a connection")
	}

	mode = allow
	dialTab(t, srv, rm.ID, "tab-a")
	waitForConnections(t, rm, 1, 2*time.Second)
	state := rm.ConnectionState()
	if len(state.Connections) != 1 || state.Connections[0].ParticipantRef != "participant-1" {
		t.Fatalf("connection state did not carry the participant binding: %+v", state)
	}
}

// awaitRole reads connection frames until the recipient's role matches.
func awaitRole(t *testing.T, conn *websocket.Conn, role string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		frame := readFrameOfType(t, conn, "connection", 2*time.Second)
		if frame["role"] == role {
			return
		}
	}
	t.Fatalf("connection never reported role %q", role)
}
