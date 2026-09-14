package server_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/interaction"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/server"
	"github.com/hollis-labs/tangent/internal/turns"
)

type turnsTestApp struct {
	database *sql.DB
	server   *server.Server
	listener net.Listener
	baseURL  string
	done     chan error
}

func startTurnsTestApp(t *testing.T) *turnsTestApp {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "turns-server-test.db")
	database, err := tangentdb.Open(databasePath)
	if err != nil {
		t.Fatalf("tangentdb.Open: %v", err)
	}
	if migrationErr := tangentdb.RunMigrations(database); migrationErr != nil {
		_ = database.Close()
		t.Fatalf("RunMigrations: %v", migrationErr)
	}
	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		_ = database.Close()
		t.Fatalf("envelope.New: %v", err)
	}
	if registrationErr := extensions.RegisterAgentTurn(envelopeService); registrationErr != nil {
		_ = database.Close()
		t.Fatalf("RegisterAgentTurn: %v", registrationErr)
	}
	interactions, err := interaction.NewService(
		interaction.NewStore(database),
		interaction.NewEnvelopeDefinitionCatalog(envelopeService, tangentmcp.HostVersion),
		interaction.WithSurfaceAccessPolicy(turns.SurfaceAccessPolicy{}),
	)
	if err != nil {
		_ = database.Close()
		t.Fatalf("interaction.NewService: %v", err)
	}
	turnsService, err := turns.NewService(interactions)
	if err != nil {
		_ = database.Close()
		t.Fatalf("turns.NewService: %v", err)
	}
	manager := room.NewManager(database)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	httpServer, serverErr := server.New(server.Config{
		Port:        0,
		Logger:      logger,
		Envelope:    envelopeService,
		RoomManager: manager,
		Turns:       turnsService,
	})
	if serverErr != nil {
		_ = database.Close()
		t.Fatalf("server.New: %v", serverErr)
	}
	listener, listenErr := httpServer.Listen()
	if listenErr != nil {
		_ = database.Close()
		t.Fatalf("server.Listen: %v", listenErr)
	}
	app := &turnsTestApp{
		database: database,
		server:   httpServer,
		listener: listener,
		baseURL:  "http://" + listener.Addr().String(),
		done:     make(chan error, 1),
	}
	go func() { app.done <- httpServer.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
		_ = database.Close()
	})
	return app
}

func testTurnRequest(sessionID, turnID, kind, title string) []byte {
	payload, _ := json.Marshal(map[string]any{
		"contract_version": "1.0",
		"session_id":       sessionID,
		"turn_id":          turnID,
		"idempotency_key":  fmt.Sprintf("tether:%s:%s", sessionID, turnID),
		"kind":             kind,
		"source": map[string]any{
			"agent_id":       "worker-agent",
			"agent_label":    "Worker",
			"application_id": "test-app",
		},
		"title":   title,
		"content": "Operator decision required to continue execution.",
		"options": []map[string]any{
			{"label": "Approve", "value": "opt_approve", "recommended": true},
			{"label": "Reject", "value": "opt_reject"},
		},
		"correlations": map[string]any{
			"task_id": "CW-20260913-0019",
		},
	})
	return payload
}

func TestTurnsHTTP_InboxEmptyAndEvents(t *testing.T) {
	app := startTurnsTestApp(t)

	// GET /api/turns should return empty inbox
	resp, err := http.Get(app.baseURL + "/api/turns")
	if err != nil {
		t.Fatalf("GET /api/turns: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var inbox turns.TurnsInbox
	if err := json.NewDecoder(resp.Body).Decode(&inbox); err != nil {
		t.Fatalf("decode inbox: %v", err)
	}
	if inbox.TotalPending != 0 || inbox.TotalTerminal != 0 {
		t.Errorf("inbox counts = %d pending, %d terminal; want 0, 0", inbox.TotalPending, inbox.TotalTerminal)
	}

	// GET /api/turns/events should send initial revision
	req, err := http.NewRequest(http.MethodGet, app.baseURL+"/api/turns/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	eventResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/turns/events: %v", err)
	}
	defer eventResp.Body.Close()

	if eventResp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d, want 200", eventResp.StatusCode)
	}
	scanner := bufio.NewScanner(eventResp.Body)
	var foundRevision bool
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: {") && strings.Contains(line, "revision") {
			foundRevision = true
			break
		}
	}
	if !foundRevision {
		t.Errorf("expected revision SSE event from /api/turns/events")
	}
}

func TestTurnsHTTP_EnqueueInspectReplyAndAck(t *testing.T) {
	app := startTurnsTestApp(t)

	// 1. Enqueue turn
	turnPayload := testTurnRequest("session-42", "turn-1", "approval", "Approve Production Release")
	enqueueResp, err := http.Post(app.baseURL+"/api/turns/enqueue", "application/json", bytes.NewReader(turnPayload))
	if err != nil {
		t.Fatalf("POST /api/turns/enqueue: %v", err)
	}
	defer enqueueResp.Body.Close()
	if enqueueResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(enqueueResp.Body)
		t.Fatalf("enqueue status = %d, want 201; body = %s", enqueueResp.StatusCode, string(body))
	}
	var handle turns.TurnHandle
	if err := json.NewDecoder(enqueueResp.Body).Decode(&handle); err != nil {
		t.Fatalf("decode handle: %v", err)
	}
	if handle.TurnID != "turn-1" || handle.SessionID != "session-42" {
		t.Errorf("handle mismatch: %+v", handle)
	}
	if handle.QueueSequence != 1 {
		t.Errorf("queue sequence = %d, want 1", handle.QueueSequence)
	}

	// 2. Inspect turn
	itemResp, err := http.Get(app.baseURL + "/api/turns/items/" + handle.ItemID)
	if err != nil {
		t.Fatalf("GET /api/turns/items/{id}: %v", err)
	}
	defer itemResp.Body.Close()
	if itemResp.StatusCode != http.StatusOK {
		t.Fatalf("item status = %d, want 200", itemResp.StatusCode)
	}
	var itemView turns.TurnItemView
	if err := json.NewDecoder(itemResp.Body).Decode(&itemView); err != nil {
		t.Fatalf("decode item view: %v", err)
	}
	if itemView.State != interaction.InteractionStatePresented {
		t.Errorf("state = %s, want presented", itemView.State)
	}
	if itemView.DeliveryState != interaction.DeliveryStateQueued {
		t.Errorf("delivery state = %s, want queued", itemView.DeliveryState)
	}

	// 3. Reply to turn
	replyBody, _ := json.Marshal(map[string]any{
		"expected_revision": itemView.Revision,
		"action":            "approve",
		"response_text":     "Deploy looks good, approved.",
		"selected_option":   "opt_approve",
		"note":              "Confirmed by operator",
	})
	replyResp, err := http.Post(app.baseURL+"/api/turns/items/"+handle.ItemID+"/reply", "application/json", bytes.NewReader(replyBody))
	if err != nil {
		t.Fatalf("POST reply: %v", err)
	}
	defer replyResp.Body.Close()
	if replyResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(replyResp.Body)
		t.Fatalf("reply status = %d, want 200; body = %s", replyResp.StatusCode, string(body))
	}
	var repliedView turns.TurnItemView
	if err := json.NewDecoder(replyResp.Body).Decode(&repliedView); err != nil {
		t.Fatalf("decode reply view: %v", err)
	}
	if repliedView.State != interaction.InteractionStateResolved {
		t.Errorf("state = %s, want resolved", repliedView.State)
	}
	if repliedView.Resolution == nil || repliedView.Resolution.Action != "approve" {
		t.Errorf("resolution mismatch: %+v", repliedView.Resolution)
	}

	// 4. Tether pulls session replies
	repliesResp, err := http.Get(app.baseURL + "/api/turns/sessions/session-42/replies")
	if err != nil {
		t.Fatalf("GET session replies: %v", err)
	}
	defer repliesResp.Body.Close()
	if repliesResp.StatusCode != http.StatusOK {
		t.Fatalf("replies status = %d, want 200", repliesResp.StatusCode)
	}
	var sessionResult struct {
		ContractVersion string               `json:"contract_version"`
		SessionID       string               `json:"session_id"`
		Replies         []turns.TurnItemView `json:"replies"`
	}
	if err := json.NewDecoder(repliesResp.Body).Decode(&sessionResult); err != nil {
		t.Fatalf("decode session result: %v", err)
	}
	if len(sessionResult.Replies) != 1 {
		t.Fatalf("replies count = %d, want 1", len(sessionResult.Replies))
	}

	// 5. Tether acknowledges delivery
	ackBody, _ := json.Marshal(map[string]any{
		"reply_id": sessionResult.Replies[0].Resolution.ResolutionID,
	})
	ackResp, err := http.Post(app.baseURL+"/api/turns/items/"+handle.ItemID+"/ack", "application/json", bytes.NewReader(ackBody))
	if err != nil {
		t.Fatalf("POST ack: %v", err)
	}
	defer ackResp.Body.Close()
	if ackResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(ackResp.Body)
		t.Fatalf("ack status = %d, want 200; body = %s", ackResp.StatusCode, string(body))
	}

	// 6. Inspect to confirm acknowledged
	finalResp, err := http.Get(app.baseURL + "/api/turns/items/" + handle.ItemID)
	if err != nil {
		t.Fatalf("GET final item: %v", err)
	}
	defer finalResp.Body.Close()
	var finalView turns.TurnItemView
	if err := json.NewDecoder(finalResp.Body).Decode(&finalView); err != nil {
		t.Fatalf("decode final view: %v", err)
	}
	if finalView.DeliveryState != interaction.DeliveryStateAcknowledged {
		t.Errorf("delivery state = %s, want acknowledged", finalView.DeliveryState)
	}
}

func TestTurnsHTTP_DismissCancellation(t *testing.T) {
	app := startTurnsTestApp(t)

	turnPayload := testTurnRequest("session-99", "turn-1", "question", "Clarification Question")
	enqueueResp, err := http.Post(app.baseURL+"/api/turns/enqueue", "application/json", bytes.NewReader(turnPayload))
	if err != nil {
		t.Fatalf("POST enqueue: %v", err)
	}
	defer enqueueResp.Body.Close()
	var handle turns.TurnHandle
	_ = json.NewDecoder(enqueueResp.Body).Decode(&handle)

	dismissBody, _ := json.Marshal(map[string]any{
		"expected_revision": handle.Revision,
		"reason":            "Operator skipped question",
	})
	dismissResp, err := http.Post(app.baseURL+"/api/turns/items/"+handle.ItemID+"/dismiss", "application/json", bytes.NewReader(dismissBody))
	if err != nil {
		t.Fatalf("POST dismiss: %v", err)
	}
	defer dismissResp.Body.Close()
	if dismissResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(dismissResp.Body)
		t.Fatalf("dismiss status = %d, want 200; body = %s", dismissResp.StatusCode, string(body))
	}
	var dismissedView turns.TurnItemView
	_ = json.NewDecoder(dismissResp.Body).Decode(&dismissedView)
	if dismissedView.State != interaction.InteractionStateCanceled {
		t.Errorf("state = %s, want canceled", dismissedView.State)
	}
}
