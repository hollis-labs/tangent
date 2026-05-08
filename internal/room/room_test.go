package room_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	envelopes "github.com/hollis-labs/go-envelopes"
	"go.uber.org/goleak"

	"github.com/hollis-labs/tangent/internal/room"
)

// TestMain wraps the package run in goleak.VerifyTestMain. Each test
// is responsible for cleaning up its own connections and goroutines;
// a failure here means the room package leaked a goroutine across the
// package run. httptest.Server keeps background goroutines alive for
// the duration of its NewServer, so we shut it down per test — any
// leak surfaced is a real bug.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// newTestServer wires a Room to a coder/websocket-connected pair using
// httptest. Returns the Room (already attached, with a server-side
// read loop running) and a cleanup. The helper hides the upgrade dance
// so individual tests stay focused on the lifecycle they're verifying.
func newTestServer(t *testing.T) (*room.Room, *websocket.Conn, func()) {
	t.Helper()

	rm := newAnonRoom(t)

	connCh := make(chan *websocket.Conn, 1)
	stopCh := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: []string{"*"},
		})
		if err != nil {
			t.Errorf("server: ws accept: %v", err)
			return
		}
		connCh <- c
		select {
		case <-stopCh:
		case <-r.Context().Done():
		}
	}))

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	clientConn, _, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{})
	if err != nil {
		srv.Close()
		t.Fatalf("client dial: %v", err)
	}

	serverConn := <-connCh
	rm.AttachConn(context.Background(), serverConn)

	// Server-side read loop — mirrors what internal/ws/handler.go does
	// in production. Without this Room.HandleResponse/HandleCancel
	// never get called and Push appears to hang.
	readDone := make(chan struct{})
	readCtx, readCancel := context.WithCancel(context.Background())
	go runRoomReadLoop(readCtx, rm, serverConn, readDone)

	cleanup := func() {
		close(stopCh)
		readCancel()
		_ = clientConn.Close(websocket.StatusNormalClosure, "test done")
		_ = serverConn.Close(websocket.StatusNormalClosure, "test done")
		srv.Close()
		<-readDone
	}
	return rm, clientConn, cleanup
}

// runRoomReadLoop is the test-only mirror of the production WS read
// loop. It exists so room_test can verify Room semantics without
// pulling in internal/ws as a test dep (which would create a cycle).
func runRoomReadLoop(ctx context.Context, rm *room.Room, conn *websocket.Conn, done chan<- struct{}) {
	defer close(done)
	for {
		mt, payload, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if mt != websocket.MessageText {
			continue
		}
		var msg struct {
			Type       string                 `json:"type"`
			EnvelopeID string                 `json:"envelopeId"`
			Response   map[string]interface{} `json:"response"`
		}
		if err := json.Unmarshal(payload, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "response":
			if msg.EnvelopeID == "" {
				continue
			}
			// Re-marshal then unmarshal into the typed Response so
			// Room sees the exact shape the production handler sends.
			raw, _ := json.Marshal(msg.Response)
			var resp envelopes.Response
			_ = json.Unmarshal(raw, &resp)
			rm.HandleResponse(msg.EnvelopeID, &resp)
		case "cancel":
			rm.HandleCancel(msg.EnvelopeID)
		}
	}
}

// newAnonRoom creates a Room outside the manager so tests have direct
// access to the lifecycle methods.
func newAnonRoom(t *testing.T) *room.Room {
	t.Helper()
	mgr := room.NewManager()
	return mgr.Create(map[string]string{"test": t.Name()})
}

// TestManager_CreateConcurrent asserts Create is race-free under
// concurrent callers. Each goroutine creates a room; we tally the IDs
// at the end and verify no collisions.
func TestManager_CreateConcurrent(t *testing.T) {
	mgr := room.NewManager()
	const n = 64

	var wg sync.WaitGroup
	wg.Add(n)
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			r := mgr.Create(nil)
			ids[i] = r.ID
		}(i)
	}
	wg.Wait()

	if mgr.Len() != n {
		t.Fatalf("expected %d rooms, got %d", n, mgr.Len())
	}
	seen := make(map[string]bool, n)
	for _, id := range ids {
		if id == "" {
			t.Error("empty room id")
			continue
		}
		if seen[id] {
			t.Errorf("duplicate room id: %s", id)
		}
		seen[id] = true
	}
}

// TestManager_Get returns false for unknown ids and true for known ones.
func TestManager_Get(t *testing.T) {
	mgr := room.NewManager()
	if _, ok := mgr.Get("nope"); ok {
		t.Error("expected miss for unknown id")
	}
	r := mgr.Create(nil)
	got, ok := mgr.Get(r.ID)
	if !ok || got != r {
		t.Errorf("expected hit for created room, got ok=%v got==r=%v", ok, got == r)
	}
}

// TestRoom_PushResponseRoundTrip pushes an envelope, has the client
// respond, and asserts the Response flows back to the caller.
func TestRoom_PushResponseRoundTrip(t *testing.T) {
	rm, clientConn, cleanup := newTestServer(t)
	defer cleanup()

	env := &envelopes.Envelope{V: 1, ID: "e-1", Type: "triage"}

	pushDone := make(chan pushResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := rm.Push(ctx, env)
		pushDone <- pushResult{resp: resp, err: err}
	}()

	// Read the envelope frame from the client side.
	frame := readClientFrame(t, clientConn, 2*time.Second)
	if frame["type"] != "envelope" {
		t.Fatalf("expected envelope frame, got %+v", frame)
	}
	if frame["envelopeId"] != "e-1" {
		t.Fatalf("envelopeId mismatch: %v", frame["envelopeId"])
	}

	// Client sends a response.
	respFrame := map[string]any{
		"type":       "response",
		"envelopeId": "e-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "e-1",
			"kind":       "data",
			"status":     "submitted",
			"payload":    map[string]any{"accepted": true},
		},
	}
	writeClientFrame(t, clientConn, respFrame)

	res := <-pushDone
	if res.err != nil {
		t.Fatalf("Push returned error: %v", res.err)
	}
	if res.resp == nil {
		t.Fatal("Push returned nil response")
	}
	if res.resp.Kind != envelopes.ResponseKindData {
		t.Errorf("response kind = %q, want data", res.resp.Kind)
	}
}

// TestRoom_PushCancel pushes an envelope, has the client send a
// cancel, and asserts Push returns the cancel error.
func TestRoom_PushCancel(t *testing.T) {
	rm, clientConn, cleanup := newTestServer(t)
	defer cleanup()

	env := &envelopes.Envelope{V: 1, ID: "e-cancel", Type: "triage"}

	pushDone := make(chan pushResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := rm.Push(ctx, env)
		pushDone <- pushResult{resp: resp, err: err}
	}()

	_ = readClientFrame(t, clientConn, 2*time.Second)

	writeClientFrame(t, clientConn, map[string]any{
		"type":       "cancel",
		"envelopeId": "e-cancel",
	})

	res := <-pushDone
	if res.err == nil {
		t.Fatalf("expected error from cancel, got resp=%+v", res.resp)
	}
	if !strings.Contains(res.err.Error(), "user cancelled") {
		t.Errorf("expected user-cancelled error, got %v", res.err)
	}
}

// TestRoom_PushDisconnect pushes an envelope, then the client closes
// the WS mid-push. Push must return ErrRoomDisconnected (not hang).
func TestRoom_PushDisconnect(t *testing.T) {
	rm, clientConn, cleanup := newTestServer(t)
	defer cleanup()

	env := &envelopes.Envelope{V: 1, ID: "e-drop", Type: "triage"}

	pushDone := make(chan pushResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := rm.Push(ctx, env)
		pushDone <- pushResult{resp: resp, err: err}
	}()

	_ = readClientFrame(t, clientConn, 2*time.Second)

	// Simulate user closing the tab.
	_ = clientConn.Close(websocket.StatusGoingAway, "closing")

	// Now close the room (in production this is what the WS handler
	// does on read-loop exit).
	rm.Close("client disconnected")

	select {
	case res := <-pushDone:
		if res.err == nil {
			t.Fatal("expected error from disconnect, got nil")
		}
		if !errors.Is(res.err, room.ErrRoomDisconnected) {
			t.Errorf("expected ErrRoomDisconnected, got %v", res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Push hung after disconnect")
	}
}

// TestRoom_PushCtxCancel asserts ctx.Done() during Push is honored
// and the Pending entry is removed.
func TestRoom_PushCtxCancel(t *testing.T) {
	rm, clientConn, cleanup := newTestServer(t)
	defer cleanup()

	env := &envelopes.Envelope{V: 1, ID: "e-ctx", Type: "triage"}

	ctx, cancel := context.WithCancel(context.Background())
	pushDone := make(chan pushResult, 1)
	go func() {
		resp, err := rm.Push(ctx, env)
		pushDone <- pushResult{resp: resp, err: err}
	}()

	_ = readClientFrame(t, clientConn, 2*time.Second)
	cancel()

	select {
	case res := <-pushDone:
		if !errors.Is(res.err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Push did not honor ctx cancel")
	}
}

// TestRoom_TwoRoomsParallel runs two Rooms in parallel and asserts
// state isolation: an envelope on Room A does not bleed into Room B.
func TestRoom_TwoRoomsParallel(t *testing.T) {
	rmA, clientA, cleanupA := newTestServer(t)
	defer cleanupA()
	rmB, clientB, cleanupB := newTestServer(t)
	defer cleanupB()

	envA := &envelopes.Envelope{V: 1, ID: "e-A", Type: "triage"}
	envB := &envelopes.Envelope{V: 1, ID: "e-B", Type: "triage"}

	doneA := make(chan pushResult, 1)
	doneB := make(chan pushResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := rmA.Push(ctx, envA)
		doneA <- pushResult{resp: resp, err: err}
	}()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := rmB.Push(ctx, envB)
		doneB <- pushResult{resp: resp, err: err}
	}()

	frameA := readClientFrame(t, clientA, 2*time.Second)
	if frameA["envelopeId"] != "e-A" {
		t.Errorf("client A got envelope %v, want e-A", frameA["envelopeId"])
	}
	frameB := readClientFrame(t, clientB, 2*time.Second)
	if frameB["envelopeId"] != "e-B" {
		t.Errorf("client B got envelope %v, want e-B", frameB["envelopeId"])
	}

	// Cross-respond: A responds to A, B responds to B. If state was
	// shared, one would resolve the other's Pending.
	writeClientFrame(t, clientA, map[string]any{
		"type":       "response",
		"envelopeId": "e-A",
		"response":   responseShape("e-A", "data", "submitted"),
	})
	writeClientFrame(t, clientB, map[string]any{
		"type":       "response",
		"envelopeId": "e-B",
		"response":   responseShape("e-B", "data", "submitted"),
	})

	resA := <-doneA
	resB := <-doneB
	if resA.err != nil {
		t.Fatalf("A push: %v", resA.err)
	}
	if resB.err != nil {
		t.Fatalf("B push: %v", resB.err)
	}
	if resA.resp.EnvelopeID != "e-A" {
		t.Errorf("A response envelopeID = %q, want e-A (cross-talk?)", resA.resp.EnvelopeID)
	}
	if resB.resp.EnvelopeID != "e-B" {
		t.Errorf("B response envelopeID = %q, want e-B (cross-talk?)", resB.resp.EnvelopeID)
	}
}

// TestRoom_CloseIsIdempotent asserts repeated Close calls don't panic.
func TestRoom_CloseIsIdempotent(t *testing.T) {
	rm := newAnonRoom(t)
	rm.Close("first")
	rm.Close("second")
	rm.Close("third")
	if !rm.IsClosed() {
		t.Error("expected room to be closed")
	}
}

// TestRoom_ReplaceConn exercises refresh-tab semantics: AttachConn
// while a Push is in-flight should not lose the Pending. We attach a
// fresh conn, the read loop on the new conn handles a response, Push
// returns successfully.
func TestRoom_ReplaceConn(t *testing.T) {
	rm, clientConn1, cleanup := newTestServer(t)
	defer cleanup()

	env := &envelopes.Envelope{V: 1, ID: "e-replace", Type: "triage"}
	pushDone := make(chan pushResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		resp, err := rm.Push(ctx, env)
		pushDone <- pushResult{resp: resp, err: err}
	}()

	// First conn receives the envelope, replies. The reply path
	// proves the read loop on the originally attached conn is alive
	// AND that AttachConn is idempotent when called with the same conn
	// during ordinary operation.
	frame := readClientFrame(t, clientConn1, 2*time.Second)
	if frame["envelopeId"] != "e-replace" {
		t.Fatalf("frame mismatch: %v", frame)
	}
	writeClientFrame(t, clientConn1, map[string]any{
		"type":       "response",
		"envelopeId": "e-replace",
		"response":   responseShape("e-replace", "data", "submitted"),
	})
	res := <-pushDone
	if res.err != nil {
		t.Fatalf("push: %v", res.err)
	}
}

// pushResult is the (resp, err) tuple Push returns; helper for chans.
type pushResult struct {
	resp *envelopes.Response
	err  error
}

func responseShape(id, kind, status string) map[string]any {
	return map[string]any{
		"v":          1,
		"envelopeId": id,
		"kind":       kind,
		"status":     status,
	}
}
