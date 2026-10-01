package server_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/server"
)

// TestHITLInboxJoinsPublicMCPBrowserProjectionAndRestart is the cross-layer
// contract test for the durable inbox. It deliberately enters only through the
// public stateless MCP and browser HTTP surfaces, then checks the same facts in
// SQLite. Existing focused UI tests drive the production React route over this
// exact REST/SSE contract.
func TestHITLInboxJoinsPublicMCPBrowserProjectionAndRestart(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "joined-hitl.db")
	app := startJoinedHITLApp(t, databasePath)

	// Opening and detaching the browser event stream is presentation transport,
	// not cancellation authority. The initial event is a durable snapshot token.
	stream := openRevisionStream(t, app.baseURL)
	if revision := stream.next(t); revision != "empty" {
		t.Fatalf("initial SSE revision = %q, want empty", revision)
	}
	stream.close()

	approvalRequest := joinedApprovalRequest("joined-approval")
	attentionRequest := joinedAttentionRequest("joined-attention")
	type enqueueResult struct {
		handle hitl.ItemHandle
		err    error
	}
	start := make(chan struct{})
	results := make(chan enqueueResult, 2)
	for _, request := range []map[string]any{approvalRequest, attentionRequest} {
		request := request
		go func() {
			<-start
			var handle hitl.ItemHandle
			call, err := joinedMCPCall(app.baseURL, "tangent.hitl_enqueue", request)
			if err == nil {
				err = call.decode(&handle)
			}
			results <- enqueueResult{handle: handle, err: err}
		}()
	}
	close(start)
	first := <-results
	second := <-results
	for index, result := range []enqueueResult{first, second} {
		if result.err != nil {
			t.Fatalf("concurrent enqueue %d: %v", index, result.err)
		}
	}
	handles := []hitl.ItemHandle{first.handle, second.handle}
	sort.Slice(handles, func(i, j int) bool { return handles[i].QueueSequence < handles[j].QueueSequence })
	if handles[0].ItemID == handles[1].ItemID || handles[0].QueueSequence != 1 || handles[1].QueueSequence != 2 {
		t.Fatalf("concurrent handles collided or lost FIFO: %#v", handles)
	}

	approval := handleForSource(t, app.baseURL, handles, "codex")
	attention := handleForSource(t, app.baseURL, handles, "nanite")
	retryCall := mustJoinedMCPCall(t, app.baseURL, "tangent.hitl_enqueue", approvalRequest)
	var retry hitl.ItemHandle
	mustDecodeJoinedCall(t, retryCall, &retry)
	if retry.ItemID != approval.ItemID || retry.QueueSequence != approval.QueueSequence {
		t.Fatalf("idempotent retry = %#v, want %#v", retry, approval)
	}
	unauthorized, err := joinedMCPCall(app.baseURL, "tangent.hitl_get", map[string]any{
		"contract_version": "1.0", "item_id": approval.ItemID,
		"caller": map[string]any{"application_id": "wrong-application", "principal_ref": "intruder"},
	})
	if err != nil || !unauthorized.isError {
		t.Fatalf("wrong-application Get = %s, error=%v, isError=%v", unauthorized.content, err, unauthorized.isError)
	}
	var unauthorizedBody struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(unauthorized.content, &unauthorizedBody); err != nil || unauthorizedBody.Code != "unauthorized" {
		t.Fatalf("wrong-application Get error = %s, decode=%v", unauthorized.content, err)
	}

	inbox := getJoinedInbox(t, app.baseURL)
	assertJoinedFIFO(t, inbox, []hitl.ItemHandle{handles[0], handles[1]})
	assertJoinedSQLiteOrder(t, app.database, []hitl.ItemHandle{handles[0], handles[1]})
	assertJoinedSPARoute(t, app.baseURL)

	// Evidence is loaded in context and never acquires ambient authority. The
	// durable Tangent reference is readable; unregistered and unsafe artifact
	// capabilities remain explicit failures.
	beforeEvidenceRevision := inbox.Revision
	referenceResponse := joinedGET(t, app.baseURL+"/api/hitl/items/"+approval.ItemID+"/evidence/3/reference")
	if referenceResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(referenceResponse.Body)
		referenceResponse.Body.Close()
		t.Fatalf("Tangent reference status = %d: %s", referenceResponse.StatusCode, body)
	}
	var reference hitl.TangentReferenceEvidenceView
	decodeJoinedResponse(t, referenceResponse, &reference)
	if reference.Status != "available" || reference.Surface.SurfaceID != hitl.DefaultSurfaceID {
		t.Fatalf("Tangent reference = %#v", reference)
	}
	assertJoinedHTTPStatus(t, joinedGET(t, app.baseURL+"/api/hitl/items/"+approval.ItemID+"/evidence/4/preview"), http.StatusUnprocessableEntity)
	assertJoinedHTTPStatus(t, joinedGET(t, app.baseURL+"/api/hitl/items/"+approval.ItemID+"/evidence/5/preview"), http.StatusForbidden)
	if afterEvidence := getJoinedInbox(t, app.baseURL); afterEvidence.Revision != beforeEvidenceRevision {
		t.Fatalf("evidence reads mutated inbox revision: %q -> %q", beforeEvidenceRevision, afterEvidence.Revision)
	}

	// Reconnect receives the current durable revision. A later enqueue produces
	// another event without polling sleeps; the stream itself is the barrier.
	reconnected := openRevisionStream(t, app.baseURL)
	if revision := reconnected.next(t); revision != inbox.Revision {
		t.Fatalf("reconnect SSE revision = %q, want %q", revision, inbox.Revision)
	}
	raceRequest := joinedApprovalRequestFor("cursor", "race-worker", "joined-race", "Race operator and caller")
	raceCall := mustJoinedMCPCall(t, app.baseURL, "tangent.hitl_enqueue", raceRequest)
	var raced hitl.ItemHandle
	mustDecodeJoinedCall(t, raceCall, &raced)
	if revision := reconnected.next(t); revision == inbox.Revision || revision == "" {
		t.Fatalf("live SSE did not advance from %q: %q", inbox.Revision, revision)
	}
	reconnected.close()
	denyCall := mustJoinedMCPCall(t, app.baseURL, "tangent.hitl_enqueue", joinedApprovalRequestFor(
		"copilot", "deny-worker", "joined-deny", "Deny joined release",
	))
	var denied hitl.ItemHandle
	mustDecodeJoinedCall(t, denyCall, &denied)
	attentionNoteCall := mustJoinedMCPCall(t, app.baseURL, "tangent.hitl_enqueue", joinedAttentionRequestFor(
		"opencode", "note-worker", "joined-attention-note", "Log joined warning",
	))
	var attentionNote hitl.ItemHandle
	mustDecodeJoinedCall(t, attentionNoteCall, &attentionNote)
	attentionPlainCall := mustJoinedMCPCall(t, app.baseURL, "tangent.hitl_enqueue", joinedAttentionRequestFor(
		"aider", "plain-worker", "joined-attention-plain", "Acknowledge joined notice",
	))
	var attentionPlain hitl.ItemHandle
	mustDecodeJoinedCall(t, attentionPlainCall, &attentionPlain)

	// Mount the production React route against this live server. The source-linked
	// driver exercises FIFO/filter/keyboard semantics, evidence, presentation,
	// approval-with-note, attention reply, focus restoration, SSE, and live-region
	// announcements through the same browser adapter shipped in the SPA.
	sseProjectionHandle := runJoinedProductionBrowser(
		t, app.baseURL, approval.ItemID, denied.ItemID, attention.ItemID,
		attentionNote.ItemID, attentionPlain.ItemID,
	)
	approvalResolved := mustJoinedItem(t, app.baseURL, approval.ItemID)
	if approvalResolved.TerminalOutcome == nil || approvalResolved.TerminalOutcome.Resolution == nil ||
		string(approvalResolved.TerminalOutcome.Resolution.Response) != `{"decision":"approved","kind":"approval","note":"release evidence reviewed"}` {
		t.Fatalf("browser approval outcome = %#v", approvalResolved.TerminalOutcome)
	}
	attentionResolved := mustJoinedItem(t, app.baseURL, attention.ItemID)
	if attentionResolved.TerminalOutcome == nil || attentionResolved.TerminalOutcome.Resolution == nil ||
		string(attentionResolved.TerminalOutcome.Resolution.Response) != `{"decision":"acknowledged","kind":"attention","reply":"worker is safe"}` {
		t.Fatalf("browser attention outcome = %#v", attentionResolved.TerminalOutcome)
	}
	assertJoinedResolutionResponse(t, app.baseURL, denied.ItemID, `{"decision":"denied","kind":"approval"}`)
	assertJoinedResolutionResponse(t, app.baseURL, attentionNote.ItemID, `{"decision":"acknowledged","kind":"attention","note":"recorded for shift"}`)
	assertJoinedResolutionResponse(t, app.baseURL, attentionPlain.ItemID, `{"decision":"acknowledged","kind":"attention"}`)

	// The production route's final post-resolution handoff selects and presents
	// the sole remaining FIFO item before the driver exits. Continue from that
	// durable projection when racing distinct authorized channels. Exactly one
	// immutable terminal fact wins; every stale retry reports conflict.
	racePresented := mustJoinedItem(t, app.baseURL, raced.ItemID)
	if racePresented.State != interaction.InteractionStatePresented || racePresented.PresentedProjectionRevision == nil {
		t.Fatalf("post-resolution handoff did not present race item = %#v", racePresented)
	}
	type raceResult struct {
		channel string
		outcome hitl.TerminalOutcome
		status  int
		code    string
		err     error
	}
	raceStart := make(chan struct{})
	raceResults := make(chan raceResult, 2)
	go func() {
		<-raceStart
		outcome, status, code, err := resolveJoinedItemResult(
			app.baseURL, racePresented,
			json.RawMessage(`{"kind":"approval","decision":"denied","note":"browser winner"}`),
		)
		raceResults <- raceResult{channel: "browser", outcome: outcome, status: status, code: code, err: err}
	}()
	go func() {
		<-raceStart
		call, err := joinedMCPCall(app.baseURL, "tangent.hitl_withdraw", map[string]any{
			"contract_version": "1.0", "item_id": raced.ItemID,
			"caller":            map[string]any{"application_id": "cursor", "principal_ref": "race-worker"},
			"expected_revision": racePresented.Revision, "reason": "caller no longer needs a decision",
		})
		var outcome hitl.TerminalOutcome
		var code string
		status := http.StatusOK
		if err == nil {
			if call.isError {
				var problem struct {
					Code            string                `json:"code"`
					TerminalOutcome *hitl.TerminalOutcome `json:"terminal_outcome"`
				}
				err = json.Unmarshal(call.content, &problem)
				code = problem.Code
				if problem.TerminalOutcome != nil {
					outcome = *problem.TerminalOutcome
				}
				status = http.StatusConflict
			} else {
				err = call.decode(&outcome)
			}
		}
		raceResults <- raceResult{channel: "caller", outcome: outcome, status: status, code: code, err: err}
	}()
	close(raceStart)
	r1, r2 := <-raceResults, <-raceResults
	winners := 0
	var winner raceResult
	for _, result := range []raceResult{r1, r2} {
		if result.err != nil {
			t.Fatalf("%s race: %v", result.channel, result.err)
		}
		if result.status == http.StatusOK {
			winners++
			winner = result
		} else if result.status != http.StatusConflict {
			t.Fatalf("%s race status = %d", result.channel, result.status)
		} else if result.code != "stale_revision" && result.code != "terminal_conflict" {
			t.Fatalf("%s race loser code = %q, want stale_revision or terminal_conflict", result.channel, result.code)
		}
	}
	if winners != 1 {
		t.Fatalf("terminal race winners = %d: %#v %#v", winners, r1, r2)
	}
	for _, result := range []raceResult{r1, r2} {
		if result.status == http.StatusConflict &&
			(result.outcome.ItemID != raced.ItemID || result.outcome.State != winner.outcome.State ||
				result.outcome.Cause != winner.outcome.Cause) {
			t.Fatalf("%s race loser did not report immutable winner: loser=%#v winner=%#v", result.channel, result.outcome, winner.outcome)
		}
	}
	switch winner.channel {
	case "browser":
		if winner.outcome.State != interaction.InteractionStateResolved || winner.outcome.Resolution == nil ||
			string(winner.outcome.Resolution.Response) != `{"decision":"denied","kind":"approval","note":"browser winner"}` {
			t.Fatalf("browser race winner = %#v", winner.outcome)
		}
	case "caller":
		if winner.outcome.State != interaction.InteractionStateCanceled ||
			winner.outcome.Cause != interaction.TerminalCauseCallerWithdrawn {
			t.Fatalf("caller race winner = %#v", winner.outcome)
		}
	default:
		t.Fatalf("unknown race winner = %#v", winner)
	}
	_, staleStatus := resolveJoinedItem(
		t, app.baseURL, racePresented,
		json.RawMessage(`{"kind":"approval","decision":"approved","note":"stale overwrite"}`),
	)
	if staleStatus != http.StatusConflict {
		t.Fatalf("stale resolution status = %d, want 409", staleStatus)
	}
	racedTerminal := mustJoinedItem(t, app.baseURL, raced.ItemID)
	if racedTerminal.TerminalOutcome == nil || racedTerminal.Revision <= racePresented.Revision ||
		racedTerminal.State != winner.outcome.State || racedTerminal.TerminalOutcome.Cause != winner.outcome.Cause {
		t.Fatalf("terminal race projection = %#v", racedTerminal)
	}
	racedStoredState := string(racedTerminal.State)
	racedStoredCause := string(racedTerminal.TerminalOutcome.Cause)

	// An explicit withdrawal is different from browser/SSE detachment. A second
	// pending item remains staged across detachment and process shutdown.
	withdrawCall := mustJoinedMCPCall(t, app.baseURL, "tangent.hitl_enqueue", joinedApprovalRequestFor(
		"claude", "withdraw-worker", "joined-withdraw", "Withdraw explicitly",
	))
	var withdrawHandle hitl.ItemHandle
	mustDecodeJoinedCall(t, withdrawCall, &withdrawHandle)
	withdrawnCall := mustJoinedMCPCall(t, app.baseURL, "tangent.hitl_withdraw", map[string]any{
		"contract_version": "1.0", "item_id": withdrawHandle.ItemID,
		"caller":            map[string]any{"application_id": "claude", "principal_ref": "withdraw-worker"},
		"expected_revision": withdrawHandle.Revision, "reason": "explicit caller withdrawal",
	})
	var withdrawn hitl.TerminalOutcome
	mustDecodeJoinedCall(t, withdrawnCall, &withdrawn)
	if withdrawn.State != interaction.InteractionStateCanceled || withdrawn.Cause != interaction.TerminalCauseCallerWithdrawn {
		t.Fatalf("explicit withdrawal = %#v", withdrawn)
	}
	pendingCall := mustJoinedMCPCall(t, app.baseURL, "tangent.hitl_enqueue", joinedAttentionRequestFor(
		"gemini", "restart-worker", "joined-restart-pending", "Remain pending across restart",
	))
	var pending hitl.ItemHandle
	mustDecodeJoinedCall(t, pendingCall, &pending)
	detachedAgain := openRevisionStream(t, app.baseURL)
	_ = detachedAgain.next(t)
	detachedAgain.close()
	if item := mustJoinedItem(t, app.baseURL, pending.ItemID); item.State != interaction.InteractionStateStaged {
		t.Fatalf("transport detach changed pending item = %#v", item)
	}

	app.stop(t)
	app = startJoinedHITLApp(t, databasePath)

	afterRestart := getJoinedInbox(t, app.baseURL)
	if len(afterRestart.Pending) != 1 || afterRestart.Pending[0].ItemID != pending.ItemID ||
		afterRestart.Pending[0].QueuePosition == nil || *afterRestart.Pending[0].QueuePosition != 1 {
		t.Fatalf("restart pending projection = %#v", afterRestart.Pending)
	}
	if len(afterRestart.History) != 8 {
		t.Fatalf("restart history length = %d, want 8: %#v", len(afterRestart.History), afterRestart.History)
	}

	// Fresh stateless HTTP requests recover terminal results after the original
	// request and process are gone. Get and zero-wait Await are retrieval only.
	for _, expected := range []struct {
		handle   hitl.ItemHandle
		caller   string
		response string
	}{
		{approval, "codex", `{"decision":"approved","kind":"approval","note":"release evidence reviewed"}`},
		{attention, "nanite", `{"decision":"acknowledged","kind":"attention","reply":"worker is safe"}`},
	} {
		arguments := map[string]any{
			"contract_version": "1.0", "item_id": expected.handle.ItemID,
			"caller": map[string]any{"application_id": expected.caller, "principal_ref": "fresh-client"},
		}
		for _, operation := range []string{"tangent.hitl_get", "tangent.hitl_await"} {
			if operation == "tangent.hitl_await" {
				arguments["wait_ms"] = 0
			}
			call := mustJoinedMCPCall(t, app.baseURL, operation, arguments)
			var retrieval hitl.RetrievalResult
			mustDecodeJoinedCall(t, call, &retrieval)
			if retrieval.Item.TerminalOutcome == nil || retrieval.Item.TerminalOutcome.Resolution == nil ||
				string(retrieval.Item.TerminalOutcome.Resolution.Response) != expected.response {
				t.Fatalf("%s restart retrieval = %#v", operation, retrieval)
			}
		}
	}

	restartedReference := joinedGET(t, app.baseURL+"/api/hitl/items/"+approval.ItemID+"/evidence/3/reference")
	if restartedReference.StatusCode != http.StatusOK {
		assertJoinedHTTPStatus(t, restartedReference, http.StatusOK)
	}
	assertJoinedSQLiteTerminal(t, app.database, approval.ItemID, "resolved", "")
	assertJoinedSQLiteTerminal(t, app.database, attention.ItemID, "resolved", "")
	assertJoinedSQLiteTerminal(t, app.database, denied.ItemID, "resolved", "")
	assertJoinedSQLiteTerminal(t, app.database, attentionNote.ItemID, "resolved", "")
	assertJoinedSQLiteTerminal(t, app.database, attentionPlain.ItemID, "resolved", "")
	assertJoinedSQLiteTerminal(t, app.database, sseProjectionHandle.ItemID, "canceled", "caller_withdrawn")
	assertJoinedSQLiteTerminal(t, app.database, raced.ItemID, racedStoredState, racedStoredCause)
	assertJoinedSQLiteTerminal(t, app.database, withdrawHandle.ItemID, "canceled", "caller_withdrawn")
	assertJoinedSQLiteTerminal(t, app.database, pending.ItemID, "staged", "")
	assertJoinedSQLiteOrder(t, app.database, []hitl.ItemHandle{
		approval, attention, raced, denied, attentionNote, attentionPlain, sseProjectionHandle,
		withdrawHandle, pending,
	})
	var roomCount int
	if err := app.database.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM rooms`).Scan(&roomCount); err != nil || roomCount != 0 {
		t.Fatalf("joined HITL flow created rooms: count=%d err=%v", roomCount, err)
	}
}

type joinedHITLApp struct {
	database *sql.DB
	server   *server.Server
	listener net.Listener
	baseURL  string
	done     chan error
	stopped  atomic.Bool
}

type joinedBrowserDriverEvent struct {
	Event         string `json:"event"`
	Message       string `json:"message"`
	ItemID        string `json:"itemID"`
	QueueSequence int64  `json:"queueSequence"`
}

type joinedLockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (buffer *joinedLockedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.b.Write(data)
}

func (buffer *joinedLockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.b.String()
}

func startJoinedHITLApp(t *testing.T, databasePath string) *joinedHITLApp {
	t.Helper()
	database, openErr := tangentdb.Open(databasePath)
	if openErr != nil {
		t.Fatalf("db.Open: %v", openErr)
	}
	if migrationErr := tangentdb.RunMigrations(database); migrationErr != nil {
		_ = database.Close()
		t.Fatalf("db.RunMigrations: %v", migrationErr)
	}
	envelopeService, envelopeErr := envelope.New(context.Background())
	if envelopeErr != nil {
		_ = database.Close()
		t.Fatalf("envelope.New: %v", envelopeErr)
	}
	for name, register := range map[string]func(*envelope.Service) error{
		"approval-queue": extensions.RegisterApprovalQueue,
		"hitl-item":      extensions.RegisterHITLItem,
	} {
		if registrationErr := register(envelopeService); registrationErr != nil {
			_ = database.Close()
			t.Fatalf("register %s: %v", name, registrationErr)
		}
	}
	interactions, interactionErr := interaction.NewService(
		interaction.NewStore(database),
		interaction.NewEnvelopeDefinitionCatalog(envelopeService, tangentmcp.HostVersion),
		interaction.WithAwaitPollInterval(time.Millisecond),
		interaction.WithSurfaceAccessPolicy(hitl.SurfaceAccessPolicy{}),
	)
	if interactionErr != nil {
		_ = database.Close()
		t.Fatalf("interaction.NewService: %v", interactionErr)
	}
	if _, recoveryErr := interactions.RecoverAfterRestart(context.Background()); recoveryErr != nil {
		_ = database.Close()
		t.Fatalf("RecoverAfterRestart: %v", recoveryErr)
	}
	hitlService, hitlErr := hitl.NewService(interactions)
	if hitlErr != nil {
		_ = database.Close()
		t.Fatalf("hitl.NewService: %v", hitlErr)
	}
	manager := room.NewManager(database)
	if hydrateErr := manager.Hydrate(context.Background()); hydrateErr != nil {
		_ = database.Close()
		t.Fatalf("room.Hydrate: %v", hydrateErr)
	}
	mcpServer, mcpErr := tangentmcp.New(
		envelopeService, envelope.NewDispatcher(envelopeService), manager, "",
		tangentmcp.WithInteractionService(interactions), tangentmcp.WithHITLService(hitlService),
	)
	if mcpErr != nil {
		_ = database.Close()
		t.Fatalf("mcp.New: %v", mcpErr)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	httpServer, serverErr := server.New(server.Config{
		Port: 0, Logger: logger, Envelope: envelopeService, MCP: mcpServer,
		RoomManager: manager, HITL: hitlService, Inbox: interactions,
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
	app := &joinedHITLApp{
		database: database, server: httpServer, listener: listener,
		baseURL: "http://" + listener.Addr().String(), done: make(chan error, 1),
	}
	go func() { app.done <- httpServer.Serve(listener) }()
	t.Cleanup(app.stopIgnoringErrors)
	return app
}

func (app *joinedHITLApp) stop(t *testing.T) {
	t.Helper()
	if !app.stopped.CompareAndSwap(false, true) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownErr := app.server.Shutdown(ctx)
	if shutdownErr != nil {
		_ = app.listener.Close()
	}
	var serveErr error
	select {
	case serveErr = <-app.done:
	case <-ctx.Done():
		_ = app.listener.Close()
		serveErr = fmt.Errorf("server.Serve did not stop: %w", ctx.Err())
	}
	closeErr := app.database.Close()
	switch {
	case shutdownErr != nil:
		t.Fatalf("server.Shutdown: %v", shutdownErr)
	case serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed):
		t.Fatalf("server.Serve: %v", serveErr)
	case closeErr != nil:
		t.Fatalf("db.Close: %v", closeErr)
	}
}

func (app *joinedHITLApp) stopIgnoringErrors() {
	if !app.stopped.CompareAndSwap(false, true) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.server.Shutdown(ctx); err != nil {
		_ = app.listener.Close()
	}
	select {
	case <-app.done:
	case <-ctx.Done():
		_ = app.listener.Close()
	}
	_ = app.database.Close()
}

type joinedMCPResult struct {
	content []byte
	isError bool
}

func joinedMCPCall(baseURL, tool string, arguments map[string]any) (joinedMCPResult, error) {
	payload := map[string]any{
		"jsonrpc": "2.0", "id": fmt.Sprintf("joined-%s", tool), "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": arguments},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return joinedMCPResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/mcp", bytes.NewReader(raw))
	if err != nil {
		return joinedMCPResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return joinedMCPResult{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return joinedMCPResult{}, err
	}
	if response.StatusCode != http.StatusOK {
		return joinedMCPResult{}, fmt.Errorf("MCP status %d: %s", response.StatusCode, body)
	}
	var rpc struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &rpc); err != nil {
		return joinedMCPResult{}, fmt.Errorf("decode MCP JSON-RPC: %w", err)
	}
	if len(rpc.Error) > 0 && string(rpc.Error) != "null" {
		return joinedMCPResult{}, fmt.Errorf("MCP JSON-RPC error: %s", rpc.Error)
	}
	if len(rpc.Result.Content) != 1 {
		return joinedMCPResult{}, fmt.Errorf("MCP content blocks = %d: %s", len(rpc.Result.Content), body)
	}
	return joinedMCPResult{content: []byte(rpc.Result.Content[0].Text), isError: rpc.Result.IsError}, nil
}

func (result joinedMCPResult) decode(target any) error {
	if result.isError {
		return fmt.Errorf("MCP tool error: %s", result.content)
	}
	if err := json.Unmarshal(result.content, target); err != nil {
		return fmt.Errorf("decode MCP tool result: %w (%s)", err, result.content)
	}
	return nil
}

func mustJoinedMCPCall(t *testing.T, baseURL, tool string, arguments map[string]any) joinedMCPResult {
	t.Helper()
	result, err := joinedMCPCall(baseURL, tool, arguments)
	if err != nil || result.isError {
		t.Fatalf("%s = %s, error=%v, isError=%v", tool, result.content, err, result.isError)
	}
	return result
}

func mustDecodeJoinedCall(t *testing.T, result joinedMCPResult, target any) {
	t.Helper()
	if err := result.decode(target); err != nil {
		t.Fatal(err)
	}
}

func joinedApprovalRequest(key string) map[string]any {
	request := joinedApprovalRequestFor("codex", "release-worker", key, "Approve joined release")
	request["evidence"] = []any{
		map[string]any{"type": "markdown", "label": "Release notes", "content": "## Validation\n\n- checks passed\n- [unsafe](javascript:alert(1))"},
		map[string]any{"type": "text", "label": "Operator note", "content": "rollback window remains open"},
		map[string]any{"type": "diff", "label": "Version bump", "format": "unified", "base_label": "old", "head_label": "new", "content": "--- a/VERSION\n+++ b/VERSION\n@@ -1 +1 @@\n-old\n+new"},
		map[string]any{"type": "tangent_reference", "label": "Inbox surface", "surface_id": hitl.DefaultSurfaceID},
		map[string]any{"type": "artifact_ref", "label": "Report", "authority": "torque", "artifact_id": "artifact-0015", "digest": "sha256:abc", "media_type": "application/pdf", "retrieval_capability_id": "pdf-preview-v1"},
		map[string]any{"type": "artifact_ref", "label": "Unsafe", "authority": "file:", "artifact_id": "private-key", "digest": "sha256:def", "retrieval_capability_id": "pdf-preview-v1"},
	}
	return request
}

func joinedApprovalRequestFor(applicationID, agentID, key, title string) map[string]any {
	return map[string]any{
		"contract_version": "1.0", "kind": "approval", "idempotency_key": key,
		"title": title, "summary": "Exercise the joined durable HITL boundary.",
		"request": "Approve or deny this bounded test request.",
		"source":  map[string]any{"application_id": applicationID, "agent_id": agentID},
		"impact":  map[string]any{"approve": "Only a record is written.", "deny": "Only a record is written."},
	}
}

func joinedAttentionRequest(key string) map[string]any {
	return joinedAttentionRequestFor("nanite", "warning-worker", key, "Acknowledge joined warning")
}

func joinedAttentionRequestFor(applicationID, agentID, key, title string) map[string]any {
	return map[string]any{
		"contract_version": "1.0", "kind": "attention", "idempotency_key": key,
		"title": title, "summary": "Exercise persistent attention in the same FIFO.",
		"request": "Acknowledge the warning without taking a business action.",
		"source":  map[string]any{"application_id": applicationID, "agent_id": agentID},
		"action_labels": map[string]any{
			"acknowledge": "Mark seen", "acknowledge_with_note": "Log context", "reply": "Respond to worker",
		},
	}
}

func handleForSource(t *testing.T, baseURL string, handles []hitl.ItemHandle, applicationID string) hitl.ItemHandle {
	t.Helper()
	for _, handle := range handles {
		item := mustJoinedItem(t, baseURL, handle.ItemID)
		var request struct {
			Source struct {
				ApplicationID string `json:"application_id"`
			} `json:"source"`
		}
		if err := json.Unmarshal(item.RequestSnapshot, &request); err != nil {
			t.Fatalf("decode request snapshot: %v", err)
		}
		if request.Source.ApplicationID == applicationID {
			return handle
		}
	}
	t.Fatalf("no handle for application %q", applicationID)
	return hitl.ItemHandle{}
}

func getJoinedInbox(t *testing.T, baseURL string) hitl.OperatorInbox {
	t.Helper()
	response := joinedGET(t, baseURL+"/api/hitl")
	if response.StatusCode != http.StatusOK {
		assertJoinedHTTPStatus(t, response, http.StatusOK)
	}
	var inbox hitl.OperatorInbox
	decodeJoinedResponse(t, response, &inbox)
	return inbox
}

func mustJoinedItem(t *testing.T, baseURL, itemID string) hitl.OperatorItemView {
	t.Helper()
	response := joinedGET(t, baseURL+"/api/hitl/items/"+itemID)
	if response.StatusCode != http.StatusOK {
		assertJoinedHTTPStatus(t, response, http.StatusOK)
	}
	var item hitl.OperatorItemView
	decodeJoinedResponse(t, response, &item)
	return item
}

func resolveJoinedItem(
	t *testing.T,
	baseURL string,
	item hitl.OperatorItemView,
	response json.RawMessage,
) (hitl.TerminalOutcome, int) {
	t.Helper()
	outcome, status, _, err := resolveJoinedItemResult(baseURL, item, response)
	if err != nil {
		t.Fatal(err)
	}
	return outcome, status
}

func resolveJoinedItemResult(
	baseURL string,
	item hitl.OperatorItemView,
	response json.RawMessage,
) (hitl.TerminalOutcome, int, string, error) {
	if item.PresentedProjectionRevision == nil {
		return hitl.TerminalOutcome{}, 0, "", errors.New("missing presented projection revision")
	}
	requestBody := map[string]any{
		"expected_revision":             item.Revision,
		"presented_projection_revision": *item.PresentedProjectionRevision,
		"response":                      response,
	}
	raw, err := json.Marshal(requestBody)
	if err != nil {
		return hitl.TerminalOutcome{}, 0, "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/hitl/items/"+item.ItemID+"/resolve", bytes.NewReader(raw))
	if err != nil {
		return hitl.TerminalOutcome{}, 0, "", err
	}
	request.Header.Set("Content-Type", "application/json")
	responseHTTP, err := http.DefaultClient.Do(request)
	if err != nil {
		return hitl.TerminalOutcome{}, 0, "", err
	}
	defer responseHTTP.Body.Close()
	body, err := io.ReadAll(responseHTTP.Body)
	if err != nil {
		return hitl.TerminalOutcome{}, 0, "", err
	}
	var outcome hitl.TerminalOutcome
	var code string
	if responseHTTP.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, &outcome); err != nil {
			return outcome, responseHTTP.StatusCode, "", fmt.Errorf("decode resolve: %w", err)
		}
	} else {
		var problem struct {
			Code            string                `json:"code"`
			TerminalOutcome *hitl.TerminalOutcome `json:"terminal_outcome"`
		}
		if err := json.Unmarshal(body, &problem); err != nil {
			return outcome, responseHTTP.StatusCode, "", fmt.Errorf("decode resolve problem: %w", err)
		}
		code = problem.Code
		if problem.TerminalOutcome != nil {
			outcome = *problem.TerminalOutcome
		}
	}
	return outcome, responseHTTP.StatusCode, code, nil
}

func joinedGET(t *testing.T, target string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeJoinedResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode HTTP response: %v", err)
	}
}

func assertJoinedHTTPStatus(t *testing.T, response *http.Response, want int) {
	t.Helper()
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != want {
		t.Fatalf("HTTP status = %d, want %d: %s", response.StatusCode, want, body)
	}
}

func assertJoinedFIFO(t *testing.T, inbox hitl.OperatorInbox, want []hitl.ItemHandle) {
	t.Helper()
	if len(inbox.Pending) != len(want) {
		t.Fatalf("pending length = %d, want %d", len(inbox.Pending), len(want))
	}
	for index := range want {
		if inbox.Pending[index].ItemID != want[index].ItemID ||
			inbox.Pending[index].QueueSequence != want[index].QueueSequence ||
			inbox.Pending[index].QueuePosition == nil || *inbox.Pending[index].QueuePosition != int64(index+1) {
			t.Fatalf("pending[%d] = %#v, want %#v", index, inbox.Pending[index], want[index])
		}
	}
}

func assertJoinedSQLiteOrder(t *testing.T, database *sql.DB, handles []hitl.ItemHandle) {
	t.Helper()
	rows, err := database.QueryContext(context.Background(), `
SELECT id, surface_sequence FROM interactions
WHERE surface_id = ? ORDER BY surface_sequence`, hitl.DefaultSurfaceID)
	if err != nil {
		t.Fatalf("query SQLite FIFO: %v", err)
	}
	defer rows.Close()
	stored := make(map[string]int64)
	var sequences []int64
	for rows.Next() {
		var id string
		var sequence int64
		if err := rows.Scan(&id, &sequence); err != nil {
			t.Fatalf("scan SQLite FIFO: %v", err)
		}
		stored[id] = sequence
		sequences = append(sequences, sequence)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate SQLite FIFO: %v", err)
	}
	if len(stored) != len(handles) {
		t.Fatalf("stored interaction count = %d, want %d", len(stored), len(handles))
	}
	for index, sequence := range sequences {
		if sequence != int64(index+1) {
			t.Fatalf("stored sequences = %v", sequences)
		}
	}
	for _, handle := range handles {
		if stored[handle.ItemID] != handle.QueueSequence {
			t.Fatalf("stored sequence for %s = %d, want %d", handle.ItemID, stored[handle.ItemID], handle.QueueSequence)
		}
	}
}

func assertJoinedSQLiteTerminal(t *testing.T, database *sql.DB, itemID, state, cause string) {
	t.Helper()
	var storedState string
	var storedCause sql.NullString
	if err := database.QueryRowContext(context.Background(), `
SELECT lifecycle_state, terminal_cause FROM interactions WHERE id = ?`, itemID).Scan(&storedState, &storedCause); err != nil {
		t.Fatalf("query terminal %s: %v", itemID, err)
	}
	if storedState != state || storedCause.String != cause {
		t.Fatalf("stored terminal %s = %s/%q, want %s/%q", itemID, storedState, storedCause.String, state, cause)
	}
}

func assertJoinedSPARoute(t *testing.T, baseURL string) {
	t.Helper()
	response := joinedGET(t, baseURL+"/hitl")
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`<div id="root"></div>`)) {
		t.Fatalf("production /hitl route = %d: %s", response.StatusCode, body)
	}
}

func runJoinedProductionBrowser(
	t *testing.T,
	baseURL, approvalID, denialID, attentionID, attentionNoteID, attentionPlainID string,
) hitl.ItemHandle {
	t.Helper()
	repoRoot, pathErr := filepath.Abs(filepath.Join("..", ".."))
	if pathErr != nil {
		t.Fatalf("resolve repository root: %v", pathErr)
	}
	viteNode := viteNodeCLIPath(repoRoot)
	if _, statErr := os.Stat(viteNode); statErr != nil {
		t.Fatalf("production HITL browser driver requires ui/node_modules: %v", statErr)
	}
	if _, lookupErr := exec.LookPath("node"); lookupErr != nil {
		t.Fatalf("production HITL browser driver requires supported Node on PATH: %v", lookupErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	script := filepath.Join(repoRoot, "ui", "src", "test-drivers", "hitl-inbox-e2e.tsx")
	// All paths are derived from the checked-out repository; no user input is
	// interpreted as an executable path.
	//nolint:gosec
	command := exec.CommandContext(ctx, "node", viteNode, script)
	command.Dir = filepath.Join(repoRoot, "ui")
	stdin, stdinErr := command.StdinPipe()
	if stdinErr != nil {
		t.Fatalf("HITL browser driver stdin: %v", stdinErr)
	}
	stdout, stdoutErr := command.StdoutPipe()
	if stdoutErr != nil {
		t.Fatalf("HITL browser driver stdout: %v", stdoutErr)
	}
	var stderr joinedLockedBuffer
	command.Stderr = &stderr
	if startErr := command.Start(); startErr != nil {
		t.Fatalf("start HITL browser driver: %v", startErr)
	}
	var processFinished atomic.Bool
	t.Cleanup(func() {
		if !processFinished.CompareAndSwap(false, true) {
			return
		}
		_ = stdin.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	events := make(chan joinedBrowserDriverEvent, 16)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			var event joinedBrowserDriverEvent
			if decodeErr := json.Unmarshal(scanner.Bytes(), &event); decodeErr != nil {
				events <- joinedBrowserDriverEvent{Event: "driver-error", Message: fmt.Sprintf("decode %q: %v", scanner.Text(), decodeErr)}
				continue
			}
			events <- event
		}
		if scanErr := scanner.Err(); scanErr != nil {
			events <- joinedBrowserDriverEvent{Event: "driver-error", Message: scanErr.Error()}
		}
		close(events)
	}()
	waitEvent := func(want string) joinedBrowserDriverEvent {
		t.Helper()
		for {
			select {
			case event, ok := <-events:
				if !ok {
					t.Fatalf("HITL browser driver exited before %s: %s", want, stderr.String())
				}
				if event.Event == "driver-error" {
					t.Fatalf("HITL browser driver: %s; stderr: %s", event.Message, stderr.String())
				}
				if event.Event == want {
					return event
				}
			case <-ctx.Done():
				t.Fatalf("HITL browser driver timed out waiting for %s: %s", want, stderr.String())
			}
		}
	}
	_ = waitEvent("ready")
	raw, err := json.Marshal(map[string]any{
		"command": "run", "baseURL": baseURL, "approvalID": approvalID,
		"denialID": denialID, "attentionID": attentionID,
		"attentionNoteID": attentionNoteID, "attentionPlainID": attentionPlainID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Write(append(raw, '\n')); err != nil {
		t.Fatalf("write HITL browser command: %v", err)
	}
	completed := waitEvent("completed")
	if completed.ItemID == "" || completed.QueueSequence < 1 {
		t.Fatalf("HITL browser driver omitted SSE-only item handle: %#v", completed)
	}
	shutdown, _ := json.Marshal(map[string]any{"command": "shutdown"})
	if _, err := stdin.Write(append(shutdown, '\n')); err != nil {
		t.Fatalf("shutdown HITL browser driver: %v", err)
	}
	_ = stdin.Close()
	waitErr := command.Wait()
	processFinished.Store(true)
	if waitErr != nil {
		t.Fatalf("HITL browser driver exit: %v; stderr: %s", waitErr, stderr.String())
	}
	return hitl.ItemHandle{ItemID: completed.ItemID, QueueSequence: completed.QueueSequence}
}

func assertJoinedResolutionResponse(t *testing.T, baseURL, itemID, want string) {
	t.Helper()
	item := mustJoinedItem(t, baseURL, itemID)
	if item.TerminalOutcome == nil || item.TerminalOutcome.Resolution == nil ||
		string(item.TerminalOutcome.Resolution.Response) != want {
		t.Fatalf("resolution %s = %#v, want response %s", itemID, item.TerminalOutcome, want)
	}
}

type joinedRevisionStream struct {
	response *http.Response
	reader   *bufio.Reader
	cancel   context.CancelFunc
	once     sync.Once
}

func openRevisionStream(t *testing.T, baseURL string) *joinedRevisionStream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/hitl/events", nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		cancel()
		t.Fatalf("open SSE = %d/%q: %s", response.StatusCode, response.Header.Get("Content-Type"), body)
	}
	stream := &joinedRevisionStream{response: response, reader: bufio.NewReader(response.Body), cancel: cancel}
	t.Cleanup(stream.close)
	return stream
}

func (stream *joinedRevisionStream) next(t *testing.T) string {
	t.Helper()
	var event string
	for {
		line, err := stream.reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE event: %v", err)
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") && event == "revision" {
			var payload struct {
				Revision string `json:"revision"`
			}
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &payload); err != nil {
				t.Fatalf("decode SSE revision: %v", err)
			}
			return payload.Revision
		}
	}
}

func (stream *joinedRevisionStream) close() {
	stream.once.Do(func() {
		_ = stream.response.Body.Close()
		stream.cancel()
	})
}
