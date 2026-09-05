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
	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/health"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/interactionpkg"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/packages"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/roomflow"
	"github.com/hollis-labs/tangent/internal/telemetry"
	tangentws "github.com/hollis-labs/tangent/internal/ws"
)

type sessionRig struct {
	db        *sql.DB
	dbPath    string
	mgr       *room.Manager
	mcpSrv    *tangentmcp.Server
	envSvc    *envelope.Service
	inter     *interaction.Service
	recorder  *telemetry.Recorder
	observed  *telemetry.SQLStore
	mcpClient *mcpsdk.ClientSession
	httpURL   string
	cleanup   func()
	// shutdown tears the process-local topology down without closing any room.
	// It is how a crash or restart is simulated: durable records survive, every
	// in-memory structure does not.
	shutdown func()
}

// sessionRigOptions selects the topology under test.
//
// The zero value is the v0.12 legacy topology: no durable interaction service,
// so every named workflow takes the blocking path. It is deliberately retained
// as the compatibility reference the durable path is measured against.
type sessionRigOptions struct {
	// durable installs the interaction service, which is the only switch that
	// routes room workflows through the canonical completion adapter.
	durable bool
	// window compresses the wait-mode compatibility window so the pending
	// receipt path is exercised without a wall-clock wait.
	window time.Duration
	// dbPath reuses an existing database file, which is how a process restart
	// is simulated: same durable records, entirely new in-memory state.
	dbPath string
	// withoutPackages boots the rig with no interaction packages installed,
	// which is how a build that shipped without a package — or an operator who
	// removed one — is exercised. The definitions still register; only the
	// behavior is gone.
	withoutPackages bool
	// disabledPackages turns named packages off after registration, the
	// runtime toggle rather than the removal.
	disabledPackages []string
}

func newSessionRig(t *testing.T) *sessionRig {
	t.Helper()
	return newSessionRigWith(t, sessionRigOptions{})
}

func newSessionRigWith(t *testing.T, options sessionRigOptions) *sessionRig {
	t.Helper()
	ctx := context.Background()

	dbPath := options.dbPath
	if dbPath == "" {
		dbPath = t.TempDir() + "/tangent.db"
	}
	db, err := tangentdb.Open(dbPath)
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
	if regErr := extensions.RegisterFeedback(envSvc); regErr != nil {
		t.Fatalf("RegisterFeedback: %v", regErr)
	}
	if regErr := extensions.RegisterFormCollect(envSvc); regErr != nil {
		t.Fatalf("RegisterFormCollect: %v", regErr)
	}
	if regErr := extensions.RegisterDesignIteration(envSvc); regErr != nil {
		t.Fatalf("RegisterDesignIteration: %v", regErr)
	}
	if regErr := extensions.RegisterInterviewQuestion(envSvc); regErr != nil {
		t.Fatalf("RegisterInterviewQuestion: %v", regErr)
	}
	if regErr := extensions.RegisterBlockDraft(envSvc); regErr != nil {
		t.Fatalf("RegisterBlockDraft: %v", regErr)
	}
	if regErr := extensions.RegisterProseRevision(envSvc); regErr != nil {
		t.Fatalf("RegisterProseRevision: %v", regErr)
	}
	if regErr := extensions.RegisterOutputRender(envSvc); regErr != nil {
		t.Fatalf("RegisterOutputRender: %v", regErr)
	}
	if regErr := extensions.RegisterWhiteboard(envSvc); regErr != nil {
		t.Fatalf("RegisterWhiteboard: %v", regErr)
	}
	if regErr := extensions.RegisterDashboard(envSvc); regErr != nil {
		t.Fatalf("RegisterDashboard: %v", regErr)
	}
	if regErr := extensions.RegisterFilePicker(envSvc); regErr != nil {
		t.Fatalf("RegisterFilePicker: %v", regErr)
	}
	if regErr := extensions.RegisterProgressPanel(envSvc); regErr != nil {
		t.Fatalf("RegisterProgressPanel: %v", regErr)
	}
	if regErr := extensions.RegisterWizard(envSvc); regErr != nil {
		t.Fatalf("RegisterWizard: %v", regErr)
	}
	if regErr := extensions.RegisterDiffReview(envSvc); regErr != nil {
		t.Fatalf("RegisterDiffReview: %v", regErr)
	}
	if regErr := extensions.RegisterSpreadsheetReview(envSvc); regErr != nil {
		t.Fatalf("RegisterSpreadsheetReview: %v", regErr)
	}
	if regErr := extensions.RegisterApprovalQueue(envSvc); regErr != nil {
		t.Fatalf("RegisterApprovalQueue: %v", regErr)
	}
	if regErr := extensions.RegisterSynthesisNotes(envSvc); regErr != nil {
		t.Fatalf("RegisterSynthesisNotes: %v", regErr)
	}
	dispatcher := envelope.NewDispatcher(envSvc)
	mgr := room.NewManager(db)
	logger := slog.New(slog.NewTextHandler(testLogWriter{t}, &slog.HandlerOptions{Level: slog.LevelWarn}))

	wsHandler := tangentws.New(mgr, logger)
	wsHandler.SetOriginPatterns([]string{"*"})
	wsSrv := httptest.NewServer(wsHandler)

	// Telemetry is wired exactly as production wires it, over the same database
	// handle, so every test in this package exercises the recording path rather
	// than a build that happens to have it switched off.
	observed, observedErr := telemetry.NewSQLStore(db)
	if observedErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("telemetry.NewSQLStore: %v", observedErr)
	}
	recorder := telemetry.New(telemetry.WithSink(observed), telemetry.WithLogger(logger))
	wsHandler.SetTelemetry(recorder)
	// The health reporter is wired here for the same reason telemetry is: the
	// correlation identifiers a non-passing report publishes are part of the
	// contract this package tests, and a rig whose health tool answers
	// `health_unavailable` cannot exercise them.
	healthReporter := health.NewReporter(
		health.WithDatabase(db),
		health.WithDefinitionRegistry(envSvc),
		health.WithTelemetry(recorder),
	)

	var interactions *interaction.Service
	serverOptions := []tangentmcp.Option{
		tangentmcp.WithTelemetry(recorder, observed),
		tangentmcp.WithHealthReporter(healthReporter),
	}
	if options.durable {
		interactions, err = interaction.NewService(
			interaction.NewStore(db),
			interaction.NewEnvelopeDefinitionCatalog(envSvc, tangentmcp.HostVersion),
			interaction.WithAwaitPollInterval(5*time.Millisecond),
			interaction.WithSurfaceAccessPolicy(hitl.SurfaceAccessPolicy{}),
			interaction.WithDeliveryWorkerPolicy(roomflow.DeliveryWorkerPolicy{}),
			interaction.WithTelemetry(recorder),
		)
		if err != nil {
			wsSrv.Close()
			_ = tangentdb.Close(db)
			t.Fatalf("interaction.NewService: %v", err)
		}
		serverOptions = append(serverOptions, tangentmcp.WithInteractionService(interactions))
		if options.window > 0 {
			serverOptions = append(serverOptions, tangentmcp.WithCompatibilityWindow(options.window))
		}
	}

	// The shipped interaction packages, installed exactly as production
	// installs them. A packaged kind fails closed without them, which is the
	// intended behavior and is asserted directly by
	// TestPackagedKindFailsClosedWithoutItsPackage.
	interactionPackages := interactionpkg.NewRegistry()
	if !options.withoutPackages {
		if regErr := packages.RegisterAll(interactionPackages); regErr != nil {
			wsSrv.Close()
			_ = tangentdb.Close(db)
			t.Fatalf("packages.RegisterAll: %v", regErr)
		}
	}
	for _, kind := range options.disabledPackages {
		interactionPackages.SetEnabled(kind, false)
	}
	serverOptions = append(serverOptions, tangentmcp.WithInteractionPackages(interactionPackages))

	mcpSrv, err := tangentmcp.New(envSvc, dispatcher, mgr, "", serverOptions...)
	if err != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("mcp.New: %v", err)
	}
	if options.durable {
		if hydrateErr := mgr.Hydrate(ctx); hydrateErr != nil {
			wsSrv.Close()
			_ = tangentdb.Close(db)
			t.Fatalf("manager.Hydrate: %v", hydrateErr)
		}
		if _, restoreErr := mcpSrv.RestoreRoomPresentations(ctx); restoreErr != nil {
			wsSrv.Close()
			_ = tangentdb.Close(db)
			t.Fatalf("RestoreRoomPresentations: %v", restoreErr)
		}
	}

	triageHandler := tangentmcp.NewTriageHandler(mgr, logger, "")
	if regErr := tangentmcp.RegisterTriageOnDispatcher(dispatcher, triageHandler); regErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("RegisterTriageOnDispatcher: %v", regErr)
	}
	if regErr := tangentmcp.RegisterFormCollectOnDispatcher(dispatcher, triageHandler); regErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("RegisterFormCollectOnDispatcher: %v", regErr)
	}
	if regErr := tangentmcp.RegisterFeedbackOnDispatcher(dispatcher, triageHandler); regErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("RegisterFeedbackOnDispatcher: %v", regErr)
	}
	if regErr := tangentmcp.RegisterDesignIterationOnDispatcher(dispatcher, triageHandler); regErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("RegisterDesignIterationOnDispatcher: %v", regErr)
	}
	if regErr := tangentmcp.RegisterInterviewQuestionOnDispatcher(dispatcher, triageHandler); regErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("RegisterInterviewQuestionOnDispatcher: %v", regErr)
	}
	if regErr := tangentmcp.RegisterBlockDraftOnDispatcher(dispatcher, triageHandler); regErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("RegisterBlockDraftOnDispatcher: %v", regErr)
	}
	if regErr := tangentmcp.RegisterProseRevisionOnDispatcher(dispatcher, triageHandler); regErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("RegisterProseRevisionOnDispatcher: %v", regErr)
	}
	if regErr := tangentmcp.RegisterOutputRenderOnDispatcher(dispatcher, triageHandler); regErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("RegisterOutputRenderOnDispatcher: %v", regErr)
	}
	if regErr := tangentmcp.RegisterWhiteboardOnDispatcher(dispatcher, triageHandler); regErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("RegisterWhiteboardOnDispatcher: %v", regErr)
	}
	if regErr := tangentmcp.RegisterDashboardOnDispatcher(dispatcher, triageHandler); regErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("RegisterDashboardOnDispatcher: %v", regErr)
	}
	if regErr := tangentmcp.RegisterFilePickerOnDispatcher(dispatcher, triageHandler); regErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("RegisterFilePickerOnDispatcher: %v", regErr)
	}
	if regErr := tangentmcp.RegisterProgressPanelOnDispatcher(dispatcher, triageHandler); regErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("RegisterProgressPanelOnDispatcher: %v", regErr)
	}
	if regErr := tangentmcp.RegisterWizardOnDispatcher(dispatcher, triageHandler); regErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("RegisterWizardOnDispatcher: %v", regErr)
	}
	if regErr := tangentmcp.RegisterDiffReviewOnDispatcher(dispatcher, triageHandler); regErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("RegisterDiffReviewOnDispatcher: %v", regErr)
	}
	if regErr := tangentmcp.RegisterSpreadsheetReviewOnDispatcher(dispatcher, triageHandler); regErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("RegisterSpreadsheetReviewOnDispatcher: %v", regErr)
	}
	if regErr := tangentmcp.RegisterApprovalQueueOnDispatcher(dispatcher, triageHandler); regErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("RegisterApprovalQueueOnDispatcher: %v", regErr)
	}
	if regErr := tangentmcp.RegisterSynthesisNotesOnDispatcher(dispatcher, triageHandler); regErr != nil {
		wsSrv.Close()
		_ = tangentdb.Close(db)
		t.Fatalf("RegisterSynthesisNotesOnDispatcher: %v", regErr)
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
		dbPath:    dbPath,
		mgr:       mgr,
		mcpSrv:    mcpSrv,
		envSvc:    envSvc,
		inter:     interactions,
		recorder:  recorder,
		observed:  observed,
		mcpClient: clientSession,
		httpURL:   wsSrv.URL,
		cleanup: func() {
			_ = clientSession.Close()
			_ = serverSession.Close()
			mgr.CloseAll("test cleanup")
			wsSrv.Close()
			_ = tangentdb.Close(db)
		},
		shutdown: func() {
			_ = clientSession.Close()
			_ = serverSession.Close()
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
		Status           string                      `json:"status"`
		Phase            string                      `json:"phase"`
		CurrentPhase     string                      `json:"current_phase"`
		PhasesVisited    []string                    `json:"phases_visited"`
		PhaseOutputs     map[string]room.PhaseOutput `json:"phase_outputs"`
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
	if state.Status != "active" {
		t.Fatalf("status = %q, want active", state.Status)
	}
	if state.CurrentPhase != "" || len(state.PhasesVisited) != 0 || len(state.PhaseOutputs) != 0 {
		t.Fatalf("unexpected initial phase state: %+v", state)
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

func TestSession_List(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomA, _ := createSession(t, rg, "room-A")
	_, _ = createSession(t, rg, "room-B")

	res, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "tangent.session_list",
		Arguments: map[string]any{"active_only": true},
	})
	if err != nil {
		t.Fatalf("session_list: %v", err)
	}
	if res.IsError {
		t.Fatalf("session_list IsError=true: %s", extractText(t, res))
	}
	var listed struct {
		Rooms []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"rooms"`
	}
	if err := json.Unmarshal([]byte(extractText(t, res)), &listed); err != nil {
		t.Fatalf("unmarshal session_list: %v", err)
	}
	if len(listed.Rooms) != 2 {
		t.Fatalf("room count = %d, want 2", len(listed.Rooms))
	}
	foundA := false
	for _, item := range listed.Rooms {
		if item.ID == roomA && item.Title == "room-A" {
			foundA = true
		}
	}
	if !foundA {
		t.Fatalf("room-A not found in session_list: %+v", listed.Rooms)
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

func TestSession_PhaseToolsAndGet(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "phases")

	phaseRes, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.session_advance_phase",
		Arguments: map[string]any{
			"roomID":   roomID,
			"to_phase": "intake",
			"reason":   "start",
		},
	})
	if err != nil {
		t.Fatalf("session_advance_phase intake: %v", err)
	}
	if phaseRes.IsError {
		t.Fatalf("session_advance_phase intake IsError=true: %s", extractText(t, phaseRes))
	}

	outputRes, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.session_set_phase_output",
		Arguments: map[string]any{
			"roomID": roomID,
			"phase":  "intake",
			"key":    "notes",
			"value":  map[string]any{"items": []any{"alpha", "beta"}},
		},
	})
	if err != nil {
		t.Fatalf("session_set_phase_output: %v", err)
	}
	if outputRes.IsError {
		t.Fatalf("session_set_phase_output IsError=true: %s", extractText(t, outputRes))
	}

	for _, phaseID := range []string{"draft", "intake"} {
		res, callErr := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
			Name: "tangent.session_advance_phase",
			Arguments: map[string]any{
				"roomID":   roomID,
				"to_phase": phaseID,
			},
		})
		if callErr != nil {
			t.Fatalf("session_advance_phase %s: %v", phaseID, callErr)
		}
		if res.IsError {
			t.Fatalf("session_advance_phase %s IsError=true: %s", phaseID, extractText(t, res))
		}
	}

	getRes, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "tangent.session_get",
		Arguments: map[string]any{"roomID": roomID},
	})
	if err != nil {
		t.Fatalf("session_get after phase tools: %v", err)
	}
	if getRes.IsError {
		t.Fatalf("session_get after phase tools IsError=true: %s", extractText(t, getRes))
	}

	var state struct {
		CurrentPhase  string                      `json:"current_phase"`
		PhasesVisited []string                    `json:"phases_visited"`
		PhaseOutputs  map[string]room.PhaseOutput `json:"phase_outputs"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get after phase tools: %v", err)
	}
	if state.CurrentPhase != "intake" {
		t.Fatalf("current_phase = %q, want intake", state.CurrentPhase)
	}
	if got, want := state.PhasesVisited, []string{"intake", "draft", "intake"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("phases_visited = %#v, want %#v", got, want)
	}
	blob := state.PhaseOutputs["intake"]
	if blob.Version != 1 {
		t.Fatalf("intake output version = %d, want 1", blob.Version)
	}
	notes, ok := blob.Data["notes"].(map[string]any)
	if !ok {
		t.Fatalf("phase output notes has type %T, want map[string]any", blob.Data["notes"])
	}
	items, ok := notes["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("phase output notes items = %#v, want len 2", notes["items"])
	}
}

func TestSession_PhaseToolsRejectInvalidPhaseID(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "bad-phase")

	for _, tc := range []struct {
		name     string
		tool     string
		args     map[string]any
		wantCode string
	}{
		{
			name:     "advance empty",
			tool:     "tangent.session_advance_phase",
			args:     map[string]any{"roomID": roomID, "to_phase": "   "},
			wantCode: envelopes.ErrorCodeValidationFailed,
		},
		{
			name:     "set output empty key",
			tool:     "tangent.session_set_phase_output",
			args:     map[string]any{"roomID": roomID, "phase": "draft", "key": " ", "value": "x"},
			wantCode: envelopes.ErrorCodeValidationFailed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
				Name:      tc.tool,
				Arguments: tc.args,
			})
			if err != nil {
				t.Fatalf("%s: %v", tc.tool, err)
			}
			if !res.IsError {
				t.Fatalf("%s expected IsError=true", tc.tool)
			}
			if !strings.Contains(extractText(t, res), tc.wantCode) {
				t.Fatalf("%s body = %s", tc.tool, extractText(t, res))
			}
		})
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

func TestSession_GetIncludesWhiteboardProjection(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "whiteboard")
	if _, err := rg.mgr.SaveWhiteboardSnapshot(roomID, room.WhiteboardSnapshot{
		BoardID: "board-1",
		SceneSnapshot: map[string]any{
			"document": map[string]any{
				"pages": []any{
					map[string]any{"id": "page:1"},
				},
			},
		},
		Assets: []room.WhiteboardAssetRef{
			{
				AssetID:    "asset-1",
				ArtifactID: "artifact-1",
				Source:     "artifact://artifact-1",
				URI:        "artifact://artifact-1",
				Kind:       "reference_image",
				MIMEType:   "image/png",
			},
		},
		ExportRefs: []room.WhiteboardExportRef{
			{
				Name:      "board-1-r1.png",
				MIMEType:  "image/png",
				Kind:      "png",
				CreatedAt: "2026-05-09T19:40:10Z",
				Width:     1200,
				Height:    800,
			},
		},
		Notes:     "seed board",
		UpdatedAt: "2026-05-09T19:40:00Z",
		Revision: &room.WhiteboardRevision{
			RevisionID: "rev-1",
			UpdatedAt:  "2026-05-09T19:40:00Z",
			Summary:    "seed",
			SceneSize:  1,
			AssetCount: 1,
		},
	}); err != nil {
		t.Fatalf("SaveWhiteboardSnapshot: %v", err)
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
		Whiteboard *room.WhiteboardStateView `json:"whiteboard"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get: %v", err)
	}
	if state.Whiteboard == nil {
		t.Fatal("whiteboard projection missing")
	}
	if got := state.Whiteboard.BoardID; got != "board-1" {
		t.Fatalf("board_id = %q, want board-1", got)
	}
	if got := len(state.Whiteboard.RevisionHistory); got != 1 {
		t.Fatalf("revision_history len = %d, want 1", got)
	}
	if got := state.Whiteboard.Assets[0].ArtifactID; got != "artifact-1" {
		t.Fatalf("artifact_id = %q, want artifact-1", got)
	}
	if got := state.Whiteboard.Assets[0].URI; got != "artifact://artifact-1" {
		t.Fatalf("asset uri = %q, want artifact://artifact-1", got)
	}
	if got := state.Whiteboard.ExportRefs[0].Kind; got != "png" {
		t.Fatalf("export kind = %q, want png", got)
	}
}

func TestSession_GetIncludesFilePickerProjection(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "file-picker")
	if _, err := rg.mgr.SaveFilePickerSnapshot(roomID, room.FilePickerSnapshot{
		PickerID: "picker-1",
		BrowseRoots: []room.FilePickerBrowseRoot{
			{RootID: "workspace", Label: "Workspace", Path: "/tmp/workspace"},
		},
		SelectedRefs: []room.FilePickerArtifactRef{
			{
				ArtifactID:   "artifact-1",
				Name:         "spec.md",
				URI:          "artifact://artifact-1",
				MIMEType:     "text/markdown",
				RootID:       "workspace",
				RelativePath: "docs/spec.md",
			},
		},
		QueryState: map[string]any{
			"search":      "spec",
			"current_dir": "docs",
		},
		SelectionRevisions: []room.FilePickerSelectionRevision{
			{
				SubmittedAt:   "2026-05-09T20:35:00Z",
				SelectedCount: 1,
			},
		},
		SubmissionSummary: &room.FilePickerSubmissionSummary{
			SelectionRevisionID: "picker-1-rev-001",
			SelectedNames:       []string{"spec.md"},
			SelectedCount:       1,
			SubmittedAt:         "2026-05-09T20:35:00Z",
		},
		Handoff: &room.FilePickerHandoff{
			SelectionRevisionID: "picker-1-rev-001",
			ArtifactRefs: []room.FilePickerArtifactRef{
				{
					ArtifactID:   "artifact-1",
					Name:         "spec.md",
					URI:          "artifact://artifact-1",
					MIMEType:     "text/markdown",
					RootID:       "workspace",
					RelativePath: "docs/spec.md",
				},
			},
			Summary: &room.FilePickerSubmissionSummary{
				SelectionRevisionID: "picker-1-rev-001",
				SelectedNames:       []string{"spec.md"},
				SelectedCount:       1,
				SubmittedAt:         "2026-05-09T20:35:00Z",
			},
		},
		UpdatedAt: "2026-05-09T20:35:00Z",
	}); err != nil {
		t.Fatalf("SaveFilePickerSnapshot: %v", err)
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
		FilePicker *room.FilePickerStateView `json:"file_picker"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get: %v", err)
	}
	if state.FilePicker == nil {
		t.Fatal("file_picker projection missing")
	}
	if got := state.FilePicker.PickerID; got != "picker-1" {
		t.Fatalf("picker_id = %q, want picker-1", got)
	}
	if got := state.FilePicker.SelectedRefs[0].URI; got != "artifact://artifact-1" {
		t.Fatalf("selected_refs[0].uri = %q, want artifact://artifact-1", got)
	}
	if got := state.FilePicker.QueryState["search"]; got != "spec" {
		t.Fatalf("query_state.search = %v, want spec", got)
	}
	if got := state.FilePicker.SelectionRevisions[0].SelectedCount; got != 1 {
		t.Fatalf("selection_revisions[0].selected_count = %d, want 1", got)
	}
	if state.FilePicker.SubmissionSummary == nil || state.FilePicker.SubmissionSummary.SelectedCount != 1 {
		t.Fatalf("submission_summary = %#v, want selected_count 1", state.FilePicker.SubmissionSummary)
	}
	if state.FilePicker.Handoff == nil || len(state.FilePicker.Handoff.ArtifactRefs) != 1 {
		t.Fatalf("handoff = %#v, want one artifact ref", state.FilePicker.Handoff)
	}
}

func TestSession_GetIncludesDashboardProjection(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "dashboard")
	if _, err := rg.mgr.SaveDashboardSnapshot(roomID, room.DashboardSnapshot{
		DashboardID: "dashboard-1",
		Title:       "Ops",
		Tiles: []room.DashboardTile{
			{
				TileID:   "tile-open",
				Kind:     "room_count",
				Title:    "Open rooms",
				Status:   "healthy",
				Value:    "4",
				Metadata: map[string]any{"owner": "codex"},
			},
		},
		Layout: []room.DashboardTilePlacement{
			{TileID: "tile-open", X: 0, Y: 0, W: 2, H: 1},
		},
		SavedLayouts: []room.DashboardSavedLayout{
			{
				LayoutID:  "layout-default",
				Name:      "Default",
				IsDefault: true,
				Tiles: []room.DashboardTilePlacement{
					{TileID: "tile-open", X: 0, Y: 0, W: 2, H: 1},
				},
			},
		},
		ActiveLayoutID: "layout-default",
		QueryState: &room.DashboardQueryState{
			Search: "open",
			Filters: []room.DashboardFilterState{
				{FilterID: "status", Operator: "in", Values: []string{"running"}},
			},
			Sort: []room.DashboardSortState{
				{Field: "updated_at", Direction: "desc"},
			},
		},
		Summary: &room.DashboardSummary{
			Headline:           "4 open rooms",
			ActiveRoomCount:    4,
			AcceptedSnapshotID: "dashboard-1-snapshot-001",
		},
		UpdatedAt: "2026-05-09T22:15:00Z",
	}); err != nil {
		t.Fatalf("SaveDashboardSnapshot: %v", err)
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
		Dashboard *room.DashboardStateView `json:"dashboard"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get: %v", err)
	}
	if state.Dashboard == nil {
		t.Fatal("dashboard projection missing")
	}
	if got := state.Dashboard.DashboardID; got != "dashboard-1" {
		t.Fatalf("dashboard_id = %q, want dashboard-1", got)
	}
	if got := state.Dashboard.Tiles[0].Metadata["owner"]; got != "codex" {
		t.Fatalf("tiles[0].metadata.owner = %v, want codex", got)
	}
	if got := state.Dashboard.ActiveLayoutID; got != "layout-default" {
		t.Fatalf("active_layout_id = %q, want layout-default", got)
	}
	if got := state.Dashboard.QueryState.Sort[0].Direction; got != "desc" {
		t.Fatalf("sort[0].direction = %q, want desc", got)
	}
	if state.Dashboard.Summary == nil || state.Dashboard.Summary.AcceptedSnapshotID != "dashboard-1-snapshot-001" {
		t.Fatalf("summary = %#v, want accepted_snapshot_id dashboard-1-snapshot-001", state.Dashboard.Summary)
	}
}

func TestSession_GetIncludesWizardProjection(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "wizard")
	if _, err := rg.mgr.SaveWizardSnapshot(roomID, room.WizardSnapshot{
		WizardID:      "wizard-1",
		Title:         "Release wizard",
		CurrentStepID: "step-review",
		Steps: []room.WizardStep{
			{
				StepID: "step-scope",
				Title:  "Scope",
				Kind:   "form",
				Branches: []room.WizardStepBranch{
					{BranchID: "review", Label: "Review", TargetStepID: "step-review", Metadata: map[string]any{}},
				},
				Metadata: map[string]any{"owner": "codex"},
			},
			{StepID: "step-review", Title: "Review", Kind: "review", Metadata: map[string]any{}},
		},
		Progress: []room.WizardStepProgress{
			{
				StepID:     "step-scope",
				Status:     "completed",
				RevisionID: "rev-001",
				Response:   map[string]any{"scope": "wizard state"},
			},
		},
		BranchSelections: []room.WizardBranchSelection{
			{StepID: "step-scope", OptionID: "review"},
		},
		Summary: &room.WizardSummary{
			Status:             "in_progress",
			CompletedStepCount: 1,
			TotalStepCount:     2,
			CurrentStepID:      "step-review",
		},
		UpdatedAt: "2026-05-10T04:20:00Z",
	}); err != nil {
		t.Fatalf("SaveWizardSnapshot: %v", err)
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
		Wizard *room.WizardStateView `json:"wizard"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get: %v", err)
	}
	if state.Wizard == nil {
		t.Fatal("wizard projection missing")
	}
	if got := state.Wizard.WizardID; got != "wizard-1" {
		t.Fatalf("wizard_id = %q, want wizard-1", got)
	}
	if got := state.Wizard.Steps[0].Metadata["owner"]; got != "codex" {
		t.Fatalf("steps[0].metadata.owner = %v, want codex", got)
	}
	if got := state.Wizard.BranchSelections[0].TargetStepID; got != "step-review" {
		t.Fatalf("branch_selections[0].target_step_id = %q, want step-review", got)
	}
	if state.Wizard.Summary == nil || state.Wizard.Summary.CurrentStepID != "step-review" {
		t.Fatalf("summary = %#v, want current_step_id step-review", state.Wizard.Summary)
	}
}

func TestSession_GetIncludesProgressPanelProjection(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "progress")
	if _, err := rg.mgr.SaveProgressPanelSnapshot(roomID, room.ProgressPanelSnapshot{
		PanelID: "panel-1",
		Items: []room.ProgressPanelItem{
			{
				ItemID:    "item-1",
				Label:     "Scan repo",
				Status:    "running",
				CreatedAt: "2026-05-09T21:00:00Z",
				Metadata:  map[string]any{"owner": "codex"},
			},
		},
		Updates: []room.ProgressPanelUpdate{
			{
				UpdateID:  "upd-001",
				Kind:      "status",
				ItemID:    "item-1",
				Status:    "running",
				Summary:   "Started scan",
				CreatedAt: "2026-05-09T21:01:00Z",
				Metadata:  map[string]any{"source": "agent"},
			},
			{
				UpdateID:        "upd-002",
				Kind:            "checkpoint",
				ItemID:          "item-1",
				CheckpointID:    "cp-001",
				CheckpointLabel: "Repo indexed",
				Summary:         "Indexed 24 files",
				CreatedAt:       "2026-05-09T21:02:00Z",
				Metadata:        map[string]any{},
			},
		},
		Summary: &room.ProgressPanelSummary{
			CurrentStatus:    "running",
			Headline:         "1 active item",
			LastUpdateID:     "upd-002",
			LastCheckpointID: "cp-001",
		},
		UpdatedAt: "2026-05-09T21:02:00Z",
	}); err != nil {
		t.Fatalf("SaveProgressPanelSnapshot: %v", err)
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
		ProgressPanel *room.ProgressPanelStateView `json:"progress_panel"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get: %v", err)
	}
	if state.ProgressPanel == nil {
		t.Fatal("progress_panel projection missing")
	}
	if got := state.ProgressPanel.PanelID; got != "panel-1" {
		t.Fatalf("panel_id = %q, want panel-1", got)
	}
	if got := state.ProgressPanel.Items[0].Metadata["owner"]; got != "codex" {
		t.Fatalf("items[0].metadata.owner = %v, want codex", got)
	}
	if got := len(state.ProgressPanel.Updates); got != 2 {
		t.Fatalf("updates len = %d, want 2", got)
	}
	if got := len(state.ProgressPanel.Checkpoints); got != 1 {
		t.Fatalf("checkpoints len = %d, want 1", got)
	}
	if got := state.ProgressPanel.Checkpoints[0].Label; got != "Repo indexed" {
		t.Fatalf("checkpoints[0].label = %q, want Repo indexed", got)
	}
	if state.ProgressPanel.Summary == nil || state.ProgressPanel.Summary.LastCheckpointID != "cp-001" {
		t.Fatalf("summary = %#v, want last_checkpoint_id cp-001", state.ProgressPanel.Summary)
	}
}

func TestSession_GetIncludesSpreadsheetReviewProjection(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "spreadsheet")
	if _, err := rg.mgr.SaveSpreadsheetReviewSnapshot(roomID, room.SpreadsheetReviewSnapshot{
		TableID: "table-1",
		Columns: []map[string]any{
			{"id": "name", "label": "Name"},
			{"id": "status", "label": "Status"},
		},
		Rows: []map[string]any{
			{"id": "row-1", "name": "Alpha", "status": "open"},
		},
		QueryState: map[string]any{
			"search":          "Alpha",
			"visible_columns": []string{"name"},
		},
		Notes:     "seed spreadsheet",
		UpdatedAt: "2026-05-09T20:10:00Z",
		SavedViews: []room.SpreadsheetReviewSavedView{
			{
				Name: "Focus",
				QueryState: map[string]any{
					"search": "Alpha",
				},
			},
		},
	}); err != nil {
		t.Fatalf("SaveSpreadsheetReviewSnapshot: %v", err)
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
		SpreadsheetReview *room.SpreadsheetReviewStateView `json:"spreadsheet_review"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get: %v", err)
	}
	if state.SpreadsheetReview == nil {
		t.Fatal("spreadsheet_review projection missing")
	}
	if got := state.SpreadsheetReview.TableID; got != "table-1" {
		t.Fatalf("table_id = %q, want table-1", got)
	}
	if got := len(state.SpreadsheetReview.Columns); got != 2 {
		t.Fatalf("columns len = %d, want 2", got)
	}
	if got := state.SpreadsheetReview.QueryState["search"]; got != "Alpha" {
		t.Fatalf("query_state.search = %v, want Alpha", got)
	}
	if got := state.SpreadsheetReview.SavedViews[0].Name; got != "Focus" {
		t.Fatalf("saved_views[0].name = %q, want Focus", got)
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

// Connection-lifecycle frames ("connection", "sync") interleave with
// presentations by design — a client is told who else is attached and which
// durable revisions it holds independently of any envelope — so they are
// skipped here.
func readWSFrame(t *testing.T, conn *websocket.Conn, timeout time.Duration) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
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
		switch frame["type"] {
		case "connection", "sync":
			continue
		default:
			return frame
		}
	}
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
