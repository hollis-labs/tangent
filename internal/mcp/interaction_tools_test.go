package mcp_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/interaction"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/room"
)

func TestGenericInteractionToolsAreOptionalAndHandleBased(t *testing.T) {
	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "mcp-interactions.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrationErr := tangentdb.RunMigrations(database); migrationErr != nil {
		t.Fatalf("db.RunMigrations: %v", migrationErr)
	}

	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if registrationErr := extensions.RegisterTriage(envelopeService); registrationErr != nil {
		t.Fatalf("RegisterTriage: %v", registrationErr)
	}
	applicationService, err := interaction.NewService(
		interaction.NewStore(database),
		interaction.NewEnvelopeDefinitionCatalog(envelopeService, tangentmcp.HostVersion),
		interaction.WithAwaitPollInterval(2*time.Millisecond),
		interaction.WithMaximumAwait(time.Second),
	)
	if err != nil {
		t.Fatalf("interaction.NewService: %v", err)
	}
	dispatcher := envelope.NewDispatcher(envelopeService)
	server, err := tangentmcp.New(
		envelopeService,
		dispatcher,
		room.NewManager(nil),
		"",
		tangentmcp.WithInteractionService(applicationService),
	)
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}

	firstClient, closeFirst := connectInteractionClient(t, server)
	tools, err := firstClient.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	wantTools := map[string]bool{
		"tangent.interaction_list_kinds":         false,
		"tangent.interaction_resolve_definition": false,
		"tangent.surface_open":                   false,
		"tangent.surface_get":                    false,
		"tangent.surface_close":                  false,
		"tangent.interaction_submit":             false,
		"tangent.interaction_get":                false,
		"tangent.interaction_await":              false,
		"tangent.interaction_cancel":             false,
		"tangent.interaction_supersede":          false,
	}
	for _, tool := range tools.Tools {
		if _, ok := wantTools[tool.Name]; ok {
			wantTools[tool.Name] = true
		}
		if tool.Name == "tangent.interaction_save_draft" || tool.Name == "tangent.interaction_resolve" {
			t.Errorf("direct MCP exposed participant-authority tool %q", tool.Name)
		}
	}
	for name, found := range wantTools {
		if !found {
			t.Errorf("generic tool %q not registered", name)
		}
	}

	caller := map[string]any{
		"scope": "application:mcp-test", "principal_ref": "agent:mcp-test",
	}
	openArguments := map[string]any{
		"caller": caller, "idempotency_key": "open-1", "owner_scope": "operator:local",
		"metadata": map[string]any{"title": "MCP async surface"},
		"policy":   map[string]any{"close": "cancel-outstanding"},
	}
	opened := callInteractionTool[interaction.SurfaceHandle](t, firstClient, "tangent.surface_open", openArguments)
	if opened.SurfaceID == "" || opened.State != interaction.SurfaceStateActive || !opened.Created {
		t.Fatalf("surface_open = %#v", opened)
	}
	reopened := callInteractionTool[interaction.SurfaceHandle](t, firstClient, "tangent.surface_open", openArguments)
	if reopened.SurfaceID != opened.SurfaceID || reopened.Created {
		t.Fatalf("idempotent surface_open = %#v, want original", reopened)
	}

	submitted := callInteractionTool[interaction.InteractionHandle](t, firstClient, "tangent.interaction_submit", map[string]any{
		"surface_id": opened.SurfaceID, "caller": caller, "idempotency_key": "interaction-1",
		"definition": map[string]any{"kind": "tangent.triage", "version": "0.1"},
		"request":    map[string]any{"prompt": "Choose", "items": []any{"a", "b"}},
	})
	if submitted.InteractionID == "" || submitted.State != interaction.InteractionStateStaged ||
		submitted.Revision != 3 || !submitted.Created {
		t.Fatalf("interaction_submit did not return immediate stable handle: %#v", submitted)
	}
	overBound, overBoundErr := firstClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.interaction_await",
		Arguments: map[string]any{
			"interaction_id":  submitted.InteractionID,
			"requester_scope": "application:mcp-test",
			"maximum_wait_ms": 50_001,
		},
	})
	if overBoundErr == nil && (overBound == nil || !overBound.IsError) {
		t.Fatalf("interaction_await accepted transport-unsafe wait: %#v", overBound)
	}

	timedOut, err := firstClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.interaction_await",
		Arguments: map[string]any{
			"interaction_id":  submitted.InteractionID,
			"requester_scope": "application:mcp-test",
			"maximum_wait_ms": 10,
		},
	})
	if err != nil {
		t.Fatalf("interaction_await protocol error: %v", err)
	}
	if !timedOut.IsError || !strings.Contains(extractText(t, timedOut), "await_timeout") {
		t.Fatalf("interaction_await timeout = %s, isError=%v", extractText(t, timedOut), timedOut.IsError)
	}
	closeFirst()

	// A fresh client transport resumes entirely by durable handle; no MCP
	// session or original HTTP request identity participates in the lookup.
	secondClient, closeSecond := connectInteractionClient(t, server)
	defer closeSecond()
	current := callInteractionTool[interaction.TerminalOutcome](t, secondClient, "tangent.interaction_get", map[string]any{
		"interaction_id":        submitted.InteractionID,
		"requester_scope":       "application:mcp-test",
		"transport_correlation": map[string]any{"session": "second"},
	})
	if current.Interaction.State != interaction.InteractionStateStaged || current.Retrieval != nil {
		t.Fatalf("nonterminal interaction_get = %#v", current)
	}
	forbidden, err := secondClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.interaction_cancel",
		Arguments: map[string]any{
			"interaction_id": submitted.InteractionID, "expected_revision": submitted.Revision,
			"requester": caller, "cause": "administrator_canceled",
		},
	})
	if err != nil {
		t.Fatalf("forbidden interaction_cancel protocol error: %v", err)
	}
	if !forbidden.IsError || !strings.Contains(extractText(t, forbidden), "unauthorized") {
		t.Fatalf("direct MCP accepted administrator cancellation: %s", extractText(t, forbidden))
	}
	canceled := callInteractionTool[interaction.TerminalizeInteractionResult](t, secondClient, "tangent.interaction_cancel", map[string]any{
		"interaction_id": submitted.InteractionID, "expected_revision": submitted.Revision,
		"requester": caller, "cause": "caller_withdrawn", "reason": "caller no longer waiting",
	})
	if canceled.Interaction.State != interaction.InteractionStateCanceled ||
		canceled.Interaction.TerminalCause != interaction.TerminalCauseCallerWithdrawn {
		t.Fatalf("interaction_cancel = %#v", canceled)
	}
	terminal := callInteractionTool[interaction.TerminalOutcome](t, secondClient, "tangent.interaction_get", map[string]any{
		"interaction_id":        submitted.InteractionID,
		"requester_scope":       "application:mcp-test",
		"transport_correlation": map[string]any{"session": "second", "operation": "get"},
	})
	if terminal.Interaction.State != interaction.InteractionStateCanceled || terminal.Retrieval == nil ||
		len(terminal.Notifications) != 1 || terminal.Notifications[0].State != interaction.DeliveryStateQueued {
		t.Fatalf("terminal interaction_get = %#v", terminal)
	}
}

func TestResolvedInteractionSurvivesMCPDisconnectAndProcessRestart(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "mcp-restart.db")
	database, err := tangentdb.Open(databasePath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if migrationErr := tangentdb.RunMigrations(database); migrationErr != nil {
		t.Fatalf("db.RunMigrations: %v", migrationErr)
	}

	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if registrationErr := extensions.RegisterTriage(envelopeService); registrationErr != nil {
		t.Fatalf("RegisterTriage: %v", registrationErr)
	}
	applicationService, err := interaction.NewService(
		interaction.NewStore(database),
		interaction.NewEnvelopeDefinitionCatalog(envelopeService, tangentmcp.HostVersion),
		interaction.WithAwaitPollInterval(2*time.Millisecond),
		interaction.WithMaximumAwait(time.Second),
	)
	if err != nil {
		t.Fatalf("interaction.NewService: %v", err)
	}
	server, err := tangentmcp.New(
		envelopeService,
		envelope.NewDispatcher(envelopeService),
		room.NewManager(nil),
		"",
		tangentmcp.WithInteractionService(applicationService),
	)
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}

	caller := map[string]any{
		"scope": "application:mcp-restart", "principal_ref": "agent:mcp-restart",
	}
	firstClient, closeFirst := connectInteractionClient(t, server)
	opened := callInteractionTool[interaction.SurfaceHandle](t, firstClient, "tangent.surface_open", map[string]any{
		"caller": caller, "idempotency_key": "restart-open", "owner_scope": "operator:local",
	})
	submitted := callInteractionTool[interaction.InteractionHandle](t, firstClient, "tangent.interaction_submit", map[string]any{
		"surface_id": opened.SurfaceID, "caller": caller, "idempotency_key": "restart-submit",
		"definition": map[string]any{"kind": "tangent.triage", "version": "0.1"},
		"request":    map[string]any{"prompt": "Preserve this", "items": []any{"yes"}},
	})
	closeFirst()

	// The original MCP request/session is gone before the participant submits.
	// Resolution writes only to the durable interaction/outbox transaction.
	participant := interaction.ActorBinding{
		Scope: "operator:local", PrincipalRef: "local-operator",
		Authority: "loopback-ui", Assurance: "loopback-unverified",
	}
	presented, err := applicationService.AcknowledgePresentation(context.Background(), interaction.PresentInteractionInput{
		InteractionID: submitted.InteractionID, ExpectedRevision: submitted.Revision,
		PresentedProjectionRevision: 1, Participant: participant,
	})
	if err != nil {
		t.Fatalf("AcknowledgePresentation: %v", err)
	}
	resolved, err := applicationService.ResolveInteraction(context.Background(), interaction.ResolveInteractionInput{
		InteractionID: submitted.InteractionID, ExpectedInteractionRevision: presented.Revision,
		PresentedProjectionRevision: 1, Participant: participant, ResponseKind: "data",
		ResponsePayload: json.RawMessage(`{"decision":"preserved-after-disconnect"}`),
	})
	if err != nil {
		t.Fatalf("ResolveInteraction: %v", err)
	}
	if len(resolved.Deliveries) != 1 || resolved.Deliveries[0].State != interaction.DeliveryStateQueued {
		t.Fatalf("durable resolution delivery = %#v", resolved.Deliveries)
	}
	if closeErr := database.Close(); closeErr != nil {
		t.Fatalf("close pre-restart database: %v", closeErr)
	}

	restartedDB, err := tangentdb.Open(databasePath)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	t.Cleanup(func() { _ = restartedDB.Close() })
	if migrationErr := tangentdb.RunMigrations(restartedDB); migrationErr != nil {
		t.Fatalf("RunMigrations after restart: %v", migrationErr)
	}
	restartedService, err := interaction.NewService(
		interaction.NewStore(restartedDB),
		interaction.NewEnvelopeDefinitionCatalog(envelopeService, tangentmcp.HostVersion),
		interaction.WithAwaitPollInterval(2*time.Millisecond),
		interaction.WithMaximumAwait(time.Second),
	)
	if err != nil {
		t.Fatalf("interaction.NewService after restart: %v", err)
	}
	if _, recoveryErr := restartedService.RecoverAfterRestart(context.Background()); recoveryErr != nil {
		t.Fatalf("RecoverAfterRestart: %v", recoveryErr)
	}
	restartedServer, err := tangentmcp.New(
		envelopeService,
		envelope.NewDispatcher(envelopeService),
		room.NewManager(nil),
		"",
		tangentmcp.WithInteractionService(restartedService),
	)
	if err != nil {
		t.Fatalf("mcp.New after restart: %v", err)
	}
	secondClient, closeSecond := connectInteractionClient(t, restartedServer)
	defer closeSecond()
	outcome := callInteractionTool[interaction.TerminalOutcome](t, secondClient, "tangent.interaction_await", map[string]any{
		"interaction_id": submitted.InteractionID, "requester_scope": "application:mcp-restart",
		"maximum_wait_ms": 100, "transport_correlation": map[string]any{"session": "after-restart"},
	})
	if outcome.Resolution == nil || outcome.Resolution.ID != resolved.Resolution.ID ||
		string(outcome.Resolution.ResponsePayload) != `{"decision":"preserved-after-disconnect"}` ||
		outcome.Retrieval == nil {
		t.Fatalf("restarted MCP await outcome = %#v", outcome)
	}
}

func connectInteractionClient(t *testing.T, server *tangentmcp.Server) (*mcpsdk.ClientSession, func()) {
	t.Helper()
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.MCP().Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "async-test", Version: "v1"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		t.Fatalf("client.Connect: %v", err)
	}
	return clientSession, func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	}
}

func callInteractionTool[Output any](
	t *testing.T,
	client *mcpsdk.ClientSession,
	name string,
	arguments map[string]any,
) Output {
	t.Helper()
	result, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("CallTool %s returned error: %s", name, extractText(t, result))
	}
	var output Output
	if err := json.Unmarshal([]byte(extractText(t, result)), &output); err != nil {
		t.Fatalf("decode %s result: %v (body %s)", name, err, extractText(t, result))
	}
	return output
}
