package mcp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/room"
	tangentws "github.com/hollis-labs/tangent/internal/ws"
)

type sessionRig struct {
	db        *sql.DB
	mgr       *room.Manager
	mcpClient *mcpsdk.ClientSession
	httpURL   string
	cleanup   func()
}

func newSessionRig(t *testing.T) *sessionRig {
	t.Helper()
	ctx := context.Background()

	db, err := tangentdb.Open(t.TempDir() + "/tangent.db")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if migrateErr := tangentdb.RunMigrations(db); migrateErr != nil {
		_ = tangentdb.Close(db)
		t.Fatalf("db.RunMigrations: %v", migrateErr)
	}

	envSvc, err := envelope.New(ctx)
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if regErr := extensions.RegisterTriage(envSvc); regErr != nil {
		t.Fatalf("RegisterTriage: %v", regErr)
	}
	dispatcher := envelope.NewDispatcher(envSvc)
	mgr := room.NewManager(db)
	logger := slog.New(slog.NewTextHandler(testLogWriter{t}, &slog.HandlerOptions{Level: slog.LevelWarn}))

	wsHandler := tangentws.New(mgr, logger)
	wsHandler.SetOriginPatterns([]string{"*"})
	wsSrv := httptest.NewServer(wsHandler)

	mcpSrv, err := tangentmcp.New(envSvc, dispatcher, mgr, "")
	if err != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("mcp.New: %v", err)
	}

	triageHandler := tangentmcp.NewTriageHandler(mgr, logger, "")
	if regErr := tangentmcp.RegisterTriageOnDispatcher(dispatcher, triageHandler); regErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("RegisterTriageOnDispatcher: %v", regErr)
	}

	serverT, clientT := mcpsdk.NewInMemoryTransports()
	serverSession, err := mcpSrv.MCP().Connect(ctx, serverT, nil)
	if err != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("mcp server.Connect: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "tangent-session-test", Version: "v0.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		_ = serverSession.Close()
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("mcp client.Connect: %v", err)
	}

	return &sessionRig{
		db:        db,
		mgr:       mgr,
		mcpClient: clientSession,
		httpURL:   wsSrv.URL,
		cleanup: func() {
			_ = clientSession.Close()
			_ = serverSession.Close()
			mgr.CloseAll("test cleanup")
			wsSrv.Close()
			_ = tangentdb.Close(db)
		},
	}
}

func (r *sessionRig) wsURL(roomID string) string {
	return "ws" + strings.TrimPrefix(r.httpURL, "http") + "?roomID=" + roomID
}

func TestSession_Create_Advance_Get_Close(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "smoke")

	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	for i := 1; i <= 3; i++ {
		envID := "session-env-" + string(rune('0'+i))
		done := make(chan advanceResult, 1)
		go func(envelopeID string) {
			done <- callAdvance(t, rg, roomID, envelopeID)
		}(envID)

		frame := readWSFrame(t, conn, 3*time.Second)
		if frame["envelopeId"] != envID {
			t.Fatalf("envelopeId = %v, want %s", frame["envelopeId"], envID)
		}
		writeWSFrame(t, conn, map[string]any{
			"type":       "response",
			"envelopeId": envID,
			"response": map[string]any{
				"v":          1,
				"envelopeId": envID,
				"kind":       "data",
				"status":     "submitted",
				"payload":    map[string]any{"index": i},
			},
		})

		res := <-done
		if res.err != nil {
			t.Fatalf("advance %d transport err: %v", i, res.err)
		}
		if res.result.IsError {
			t.Fatalf("advance %d IsError=true: %s", i, extractText(t, res.result))
		}
	}

	getRes, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "tangent.session_get",
		Arguments: map[string]any{"roomID": roomID},
	})
	if err != nil {
		t.Fatalf("session_get: %v", err)
	}
	if getRes.IsError {
		t.Fatalf("session_get IsError=true: %s", extractText(t, getRes))
	}
	var state struct {
		Phase            string `json:"phase"`
		EnvelopesHistory []struct {
			Envelope struct {
				ID string `json:"id"`
			} `json:"envelope"`
		} `json:"envelopes_history"`
		CurrentEnvelope any `json:"current_envelope"`
	}
	if decodeErr := json.Unmarshal([]byte(extractText(t, getRes)), &state); decodeErr != nil {
		t.Fatalf("unmarshal session_get: %v", decodeErr)
	}
	if state.Phase != "active" {
		t.Fatalf("phase = %q, want active", state.Phase)
	}
	if len(state.EnvelopesHistory) != 3 {
		t.Fatalf("history len = %d, want 3", len(state.EnvelopesHistory))
	}
	if state.EnvelopesHistory[0].Envelope.ID != "session-env-1" || state.EnvelopesHistory[2].Envelope.ID != "session-env-3" {
		t.Fatalf("history ids out of order: %+v", state.EnvelopesHistory)
	}
	if state.CurrentEnvelope != nil {
		t.Fatalf("current_envelope = %#v, want nil", state.CurrentEnvelope)
	}

	closeRes, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.session_close",
		Arguments: map[string]any{
			"roomID": roomID,
			"status": "done",
		},
	})
	if err != nil {
		t.Fatalf("session_close: %v", err)
	}
	if closeRes.IsError {
		t.Fatalf("session_close IsError=true: %s", extractText(t, closeRes))
	}
	assertClosedRoomRow(t, rg.db, roomID, "done")
	if _, ok := rg.mgr.Get(roomID); ok {
		t.Fatalf("room %s still present after close", roomID)
	}
}

func TestSession_Advance_Busy(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "busy")
	firstDone := make(chan advanceResult, 1)
	go func() {
		firstDone <- callAdvance(t, rg, roomID, "busy-1")
	}()

	awaitPendingRoom(t, rg.mgr, roomID, 2*time.Second)

	secondRes, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.session_advance",
		Arguments: map[string]any{
			"roomID": roomID,
			"envelope": map[string]any{
				"v":    1,
				"id":   "busy-2",
				"type": "tangent.triage",
				"data": map[string]any{"items": []any{}},
			},
		},
	})
	if err != nil {
		t.Fatalf("second session_advance: %v", err)
	}
	if !secondRes.IsError {
		t.Fatalf("expected IsError=true for busy room, got success: %s", extractText(t, secondRes))
	}
	if !strings.Contains(extractText(t, secondRes), "SESSION_BUSY") {
		t.Fatalf("busy error body = %s", extractText(t, secondRes))
	}

	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	frame := readWSFrame(t, conn, 3*time.Second)
	if frame["envelopeId"] != "busy-1" {
		t.Fatalf("envelopeId = %v, want busy-1", frame["envelopeId"])
	}
	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": "busy-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "busy-1",
			"kind":       "ack",
			"status":     "submitted",
		},
	})

	firstRes := <-firstDone
	if firstRes.err != nil {
		t.Fatalf("first session_advance: %v", firstRes.err)
	}
	if firstRes.result.IsError {
		t.Fatalf("first session_advance IsError=true: %s", extractText(t, firstRes.result))
	}
}

func TestSession_Advance_UnknownRoom(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	res, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.session_advance",
		Arguments: map[string]any{
			"roomID": "does-not-exist",
			"envelope": map[string]any{
				"v":    1,
				"id":   "missing-1",
				"type": "tangent.triage",
				"data": map[string]any{"items": []any{}},
			},
		},
	})
	if err != nil {
		t.Fatalf("session_advance: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true for unknown room")
	}
	if !strings.Contains(extractText(t, res), "ROOM_NOT_FOUND") {
		t.Fatalf("unknown-room body = %s", extractText(t, res))
	}
}

func TestSession_TriageRegression(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	done := make(chan advanceResult, 1)
	go func() {
		res, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
			Name: "tangent.triage",
			Arguments: map[string]any{
				"envelope": map[string]any{
					"v":     1,
					"id":    "triage-reg-1",
					"type":  "tangent.triage",
					"title": "Regression",
					"data": map[string]any{
						"prompt": "keep v0.1 behavior",
						"items":  []any{"alpha"},
					},
				},
			},
		})
		done <- advanceResult{result: res, err: err}
	}()

	rm := awaitSingleRoom(t, rg.mgr, 2*time.Second)
	meta := rm.MetaCopy()
	if meta["envelopeID"] != "triage-reg-1" || meta["envelopeType"] != "tangent.triage" {
		t.Fatalf("room meta = %#v", meta)
	}

	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(rm.ID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	frame := readWSFrame(t, conn, 3*time.Second)
	if frame["envelopeId"] != "triage-reg-1" {
		t.Fatalf("envelopeId = %v, want triage-reg-1", frame["envelopeId"])
	}
	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": "triage-reg-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "triage-reg-1",
			"kind":       "data",
			"status":     "submitted",
			"payload":    map[string]any{"accepted": true},
		},
	})

	triageRes := <-done
	if triageRes.err != nil {
		t.Fatalf("triage transport err: %v", triageRes.err)
	}
	if triageRes.result.IsError {
		t.Fatalf("triage IsError=true: %s", extractText(t, triageRes.result))
	}

	getRes, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "tangent.session_get",
		Arguments: map[string]any{"roomID": rm.ID},
	})
	if err != nil {
		t.Fatalf("session_get after triage: %v", err)
	}
	if getRes.IsError {
		t.Fatalf("session_get after triage IsError=true: %s", extractText(t, getRes))
	}
	var state struct {
		EnvelopesHistory []any `json:"envelopes_history"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get after triage: %v", err)
	}
	if len(state.EnvelopesHistory) != 1 {
		t.Fatalf("triage history len = %d, want 1", len(state.EnvelopesHistory))
	}
}

type advanceResult struct {
	result *mcpsdk.CallToolResult
	err    error
}

func createSession(t *testing.T, rg *sessionRig, title string) (string, string) {
	t.Helper()
	res, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.session_create",
		Arguments: map[string]any{
			"title": title,
			"meta":  map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("session_create: %v", err)
	}
	if res.IsError {
		t.Fatalf("session_create IsError=true: %s", extractText(t, res))
	}
	var created struct {
		RoomID string `json:"roomID"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal([]byte(extractText(t, res)), &created); err != nil {
		t.Fatalf("unmarshal session_create: %v", err)
	}
	if created.RoomID == "" {
		t.Fatal("session_create returned empty roomID")
	}
	return created.RoomID, created.URL
}

func callAdvance(t *testing.T, rg *sessionRig, roomID, envelopeID string) advanceResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "tangent.session_advance",
		Arguments: map[string]any{
			"roomID": roomID,
			"envelope": map[string]any{
				"v":     1,
				"id":    envelopeID,
				"type":  "tangent.triage",
				"title": envelopeID,
				"data": map[string]any{
					"prompt": "session advance test",
					"items":  []any{"item"},
				},
			},
		},
	})
	return advanceResult{result: res, err: err}
}

func awaitPendingRoom(t *testing.T, mgr *room.Manager, roomID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		rm, ok := mgr.Get(roomID)
		if ok && rm.HasPending() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("room %s did not become pending within %v", roomID, timeout)
}

func awaitSingleRoom(t *testing.T, mgr *room.Manager, timeout time.Duration) *room.Room {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ids := mgr.IDs()
		if len(ids) == 1 {
			rm, ok := mgr.Get(ids[0])
			if ok {
				return rm
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected one room within %v (got %d)", timeout, mgr.Len())
	return nil
}

func readWSFrame(t *testing.T, conn *websocket.Conn, timeout time.Duration) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	mt, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	if mt != websocket.MessageText {
		t.Fatalf("expected text frame, got %v", mt)
	}
	var frame map[string]any
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatalf("unmarshal ws frame: %v", err)
	}
	return frame
}

func writeWSFrame(t *testing.T, conn *websocket.Conn, msg map[string]any) {
	t.Helper()
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal ws frame: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatalf("ws write: %v", err)
	}
}

func assertClosedRoomRow(t *testing.T, db *sql.DB, roomID, reason string) {
	t.Helper()
	var (
		id           string
		closedAt     sql.NullString
		closedReason sql.NullString
	)
	if err := db.QueryRow(`
SELECT id, closed_at, closed_reason
FROM rooms
WHERE id = ?`,
		roomID,
	).Scan(&id, &closedAt, &closedReason); err != nil {
		t.Fatalf("query room row: %v", err)
	}
	if id != roomID {
		t.Fatalf("row id = %q, want %q", id, roomID)
	}
	if !closedAt.Valid {
		t.Fatal("closed_at is NULL, want value")
	}
	if closedReason.String != reason {
		t.Fatalf("closed_reason = %q, want %q", closedReason.String, reason)
	}
}

type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}
