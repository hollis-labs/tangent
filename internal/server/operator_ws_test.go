package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/hollis-labs/tangent/internal/docs"
	"github.com/hollis-labs/tangent/internal/server"
	"github.com/hollis-labs/tangent/internal/turns"
)

// dialRoom attaches to a room with a session cookie, as a browser tab does.
func (app *guardedApp) dialRoom(t *testing.T, roomID string, cookie *http.Cookie) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(app.ctx(t), app.wsURL(roomID),
		&websocket.DialOptions{HTTPHeader: http.Header{"Cookie": []string{cookie.String()}}})
	if err != nil {
		t.Fatalf("dial room: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

// errorFrames reads a socket for the rest of the test and forwards the code
// of every error frame. A single long-lived reader matters: cancelling a read
// closes a coder/websocket connection.
func errorFrames(t *testing.T, conn *websocket.Conn) <-chan string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	codes := make(chan string, 16)
	go func() {
		defer close(codes)
		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var decoded map[string]any
			if json.Unmarshal(raw, &decoded) == nil && decoded["type"] == "error" {
				code, _ := decoded["code"].(string)
				codes <- code
			}
		}
	}()
	return codes
}

// sendAnswer writes a response frame naming an envelope the room is not
// presenting: authorization runs first, so a capability refusal is the only
// error frame it can produce.
func sendAnswer(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	frame, _ := json.Marshal(map[string]any{
		"type": "response", "envelopeId": "no-such-envelope", "revision": 1,
		"response": map[string]any{
			"v": 1, "envelopeId": "no-such-envelope", "kind": "data", "status": "submitted",
			"payload": map[string]any{"ok": true},
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatalf("write response frame: %v", err)
	}
}

func nextCode(codes <-chan string, wait time.Duration) string {
	select {
	case code := <-codes:
		return code
	case <-time.After(wait):
		return ""
	}
}

func TestUnelevatedWebSocketAnswerIsRefused(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	roomID := app.manager.Create(map[string]string{"title": "unelevated"}).ID
	minted := app.openDocument(t, "/r/"+roomID)
	conn := app.dialRoom(t, roomID, minted)
	codes := errorFrames(t, conn)
	sendAnswer(t, conn)
	if code := nextCode(codes, 2*time.Second); code != "not_authorized" {
		t.Fatalf("unelevated answer frame error = %q, want not_authorized", code)
	}
}

// TestRevocationReachesALiveElevatedSocket: an elevated tab's socket loses
// resolve on its next answer after `tangent session revoke --all`.
func TestRevocationReachesALiveElevatedSocket(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	roomID := app.manager.Create(map[string]string{"title": "revoked"}).ID
	elevated := app.openElevatedDocument(t, "/r/"+roomID)
	conn := app.dialRoom(t, roomID, elevated)
	codes := errorFrames(t, conn)

	// Before revocation the answer passes authorization; the room then drops
	// it for naming no pending envelope, which is not a capability refusal.
	sendAnswer(t, conn)
	if code := nextCode(codes, 500*time.Millisecond); code == "not_authorized" {
		t.Fatal("an elevated socket was refused before revocation")
	}
	if _, err := app.sessions.RevokeAll(t.Context(), "operator-revoked"); err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}
	sendAnswer(t, conn)
	if code := nextCode(codes, 2*time.Second); code != "not_authorized" {
		t.Fatalf("answer after revoke --all error = %q, want not_authorized", code)
	}
}

// recordingDocs and recordingTurns stand in for the services behind the
// docs and turns routes. The gate refuses before either is reached; when it
// admits, they record the participant the handler passed.
type recordingDocs struct {
	server.DocsService
	mu        sync.Mutex
	assurance string
}

func (r *recordingDocs) Acknowledge(_ context.Context, input docs.AcknowledgeInput) (docs.DocItemView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.assurance = input.Participant.Assurance
	return docs.DocItemView{ItemID: input.ItemID}, nil
}

type recordingTurns struct {
	server.TurnsService
	mu        sync.Mutex
	assurance string
}

func (r *recordingTurns) Reply(_ context.Context, input turns.ReplyInput) (turns.TurnItemView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.assurance = input.Participant.Assurance
	return turns.TurnItemView{ItemID: input.ItemID}, nil
}

func TestDocsAcknowledgeAndTurnsReplyRequireElevation(t *testing.T) {
	t.Parallel()
	docsService := &recordingDocs{}
	turnsService := &recordingTurns{}
	app := startGuardedAppWith(t, func(cfg *server.Config) {
		cfg.Docs = docsService
		cfg.Turns = turnsService
	})
	ack := map[string]any{"expected_revision": 1, "note": "ok"}
	reply := map[string]any{"expected_revision": 1, "action": "respond", "response_text": "yes"}

	minted := app.openDocument(t, "/")
	status, body, _ := app.postJSON(t, "/api/docs/items/doc-1/acknowledge", minted, ack, true)
	assertCode(t, "unelevated docs acknowledge", status, body, http.StatusForbidden, "assurance_insufficient")
	status, body, _ = app.postJSON(t, "/api/turns/items/turn-1/reply", minted, reply, true)
	assertCode(t, "unelevated turns reply", status, body, http.StatusForbidden, "assurance_insufficient")
	if docsService.assurance != "" || turnsService.assurance != "" {
		t.Fatal("a refused request reached the service")
	}

	elevated := app.openElevatedDocument(t, "/")
	if status, body, _ = app.postJSON(t, "/api/docs/items/doc-1/acknowledge", elevated, ack, true); status != http.StatusOK {
		t.Fatalf("elevated docs acknowledge = %d %v", status, body)
	}
	if status, body, _ = app.postJSON(t, "/api/turns/items/turn-1/reply", elevated, reply, true); status != http.StatusOK {
		t.Fatalf("elevated turns reply = %d %v", status, body)
	}
	if docsService.assurance != "operator-token" || turnsService.assurance != "operator-token" {
		t.Fatalf("services received assurance docs=%q turns=%q, want operator-token",
			docsService.assurance, turnsService.assurance)
	}
}
