package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/room"
)

func TestHITLToolsExposeContractAndAllFourDurableOperations(t *testing.T) {
	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "mcp-hitl.db"))
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
	for name, register := range map[string]func(*envelope.Service) error{
		"approval-queue": extensions.RegisterApprovalQueue,
		"hitl-item":      extensions.RegisterHITLItem,
	} {
		if registrationErr := register(envelopeService); registrationErr != nil {
			t.Fatalf("Register %s: %v", name, registrationErr)
		}
	}
	interactions, err := interaction.NewService(
		interaction.NewStore(database),
		interaction.NewEnvelopeDefinitionCatalog(envelopeService, tangentmcp.HostVersion),
		interaction.WithAwaitPollInterval(time.Millisecond),
		interaction.WithSurfaceAccessPolicy(hitl.SurfaceAccessPolicy{}),
	)
	if err != nil {
		t.Fatalf("interaction.NewService: %v", err)
	}
	hitlService, err := hitl.NewService(interactions)
	if err != nil {
		t.Fatalf("hitl.NewService: %v", err)
	}
	server, err := tangentmcp.New(
		envelopeService, envelope.NewDispatcher(envelopeService), room.NewManager(nil), "",
		tangentmcp.WithInteractionService(interactions), tangentmcp.WithHITLService(hitlService),
	)
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}
	client, closeClient := connectInteractionClient(t, server)
	defer closeClient()

	listed, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	// 25 legacy + session tools, 11 generic durable interaction tools, 3
	// definition-registry diagnostics, 4 HITL tools, and tangent.health_report
	// (CW-20260825-0066). Changing this number is a public-API change.
	if len(listed.Tools) != 44 {
		t.Fatalf("production MCP topology = %d tools, want 44", len(listed.Tools))
	}
	want := map[string]bool{
		"tangent.hitl_enqueue":   false,
		"tangent.hitl_get":       false,
		"tangent.hitl_await":     false,
		"tangent.hitl_withdraw":  false,
		"tangent.approval-queue": false,
	}
	for _, tool := range listed.Tools {
		if _, tracked := want[tool.Name]; tracked {
			want[tool.Name] = true
		}
		if tool.Name[:min(len(tool.Name), len("tangent.hitl_"))] == "tangent.hitl_" {
			schemaRaw, _ := json.Marshal(tool.InputSchema)
			var schema map[string]any
			_ = json.Unmarshal(schemaRaw, &schema)
			if schema["type"] != "object" {
				t.Errorf("%s input schema does not have an object root: %#v", tool.Name, tool.InputSchema)
			}
			outputRaw, _ := json.Marshal(tool.OutputSchema)
			var outputSchema map[string]any
			_ = json.Unmarshal(outputRaw, &outputSchema)
			if outputSchema["type"] != "object" {
				t.Errorf("%s output schema does not have an object root: %#v", tool.Name, tool.OutputSchema)
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %q not registered", name)
		}
	}

	reservedOpen, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.surface_open",
		Arguments: map[string]any{
			"surface_id":      hitl.DefaultSurfaceID,
			"caller":          map[string]any{"scope": "operator:local", "principal_ref": "forged-operator"},
			"idempotency_key": "reserve-before-hitl", "owner_scope": "operator:local",
		},
	})
	if err != nil || reservedOpen == nil || !reservedOpen.IsError {
		t.Fatalf("generic surface_open reserved HITL surface: %#v, %v", reservedOpen, err)
	}

	request := map[string]any{
		"contract_version": "1.0",
		"kind":             "approval",
		"idempotency_key":  "direct-mcp-1",
		"title":            "Approve direct MCP contract",
		"summary":          "Exercise every durable HITL MCP operation.",
		"request":          "Approve or deny the direct MCP contract.",
		"source": map[string]any{
			"application_id": "codex",
			"agent_id":       "worker-1",
		},
	}
	handle := callInteractionTool[hitl.ItemHandle](t, client, "tangent.hitl_enqueue", request)
	if handle.ItemID == "" || handle.SurfaceID != hitl.DefaultSurfaceID ||
		handle.QueueSequence != 1 || handle.QueuePosition == nil || *handle.QueuePosition != 1 {
		t.Fatalf("hitl_enqueue = %#v", handle)
	}
	retry := callInteractionTool[hitl.ItemHandle](t, client, "tangent.hitl_enqueue", request)
	if retry.ItemID != handle.ItemID || retry.QueueSequence != handle.QueueSequence {
		t.Fatalf("idempotent hitl_enqueue = %#v, want %#v", retry, handle)
	}
	replacementRequest := cloneMap(request)
	replacementRequest["idempotency_key"] = "direct-mcp-2"
	replacementRequest["title"] = "Replacement item"
	replacement := callInteractionTool[hitl.ItemHandle](t, client, "tangent.hitl_enqueue", replacementRequest)
	genericCaller := map[string]any{
		"scope": "direct-loopback:codex", "principal_ref": "worker-1",
	}
	for name, arguments := range map[string]map[string]any{
		"tangent.surface_get": {
			"surface_id": hitl.DefaultSurfaceID, "requester_scope": "operator:local",
		},
		"tangent.interaction_get": {
			"interaction_id": handle.ItemID, "requester_scope": "direct-loopback:codex",
		},
		"tangent.interaction_await": {
			"interaction_id": handle.ItemID, "requester_scope": "direct-loopback:codex", "maximum_wait_ms": 1,
		},
		"tangent.interaction_cancel": {
			"interaction_id": handle.ItemID, "expected_revision": handle.Revision,
			"requester": genericCaller, "cause": "caller_withdrawn",
		},
		"tangent.interaction_supersede": {
			"interaction_id": handle.ItemID, "expected_revision": handle.Revision,
			"replacement_interaction_id": replacement.ItemID, "requester": genericCaller,
		},
	} {
		genericResult, callErr := client.CallTool(context.Background(), &mcpsdk.CallToolParams{
			Name: name, Arguments: arguments,
		})
		if callErr != nil || genericResult == nil || !genericResult.IsError {
			t.Errorf("generic operation %s bypassed reserved HITL surface: %#v, %v", name, genericResult, callErr)
		}
	}
	contamination, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.interaction_submit",
		Arguments: map[string]any{
			"surface_id":      hitl.DefaultSurfaceID,
			"caller":          map[string]any{"scope": "generic-app", "principal_ref": "generic-agent"},
			"idempotency_key": "not-a-hitl-item",
			"definition":      map[string]any{"kind": extensions.HITLItemEnvelopeType, "version": "1.0"},
			"request":         request,
		},
	})
	if err != nil || contamination == nil || !contamination.IsError {
		t.Fatalf("generic interaction_submit contaminated HITL surface: %#v, %v", contamination, err)
	}
	reservedClose, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.surface_close",
		Arguments: map[string]any{
			"surface_id": hitl.DefaultSurfaceID, "expected_revision": 2,
			"requester":  map[string]any{"scope": "operator:local", "principal_ref": "forged-operator"},
			"policy_ref": "forged-close",
		},
	})
	if err != nil || reservedClose == nil || !reservedClose.IsError {
		t.Fatalf("generic surface_close closed HITL surface: %#v, %v", reservedClose, err)
	}

	changed := cloneMap(request)
	changed["title"] = "Changed immutable request"
	conflict, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.hitl_enqueue", Arguments: changed,
	})
	if err != nil {
		t.Fatalf("conflicting enqueue transport error: %v", err)
	}
	var conflictBody map[string]any
	if !conflict.IsError || json.Unmarshal([]byte(extractText(t, conflict)), &conflictBody) != nil ||
		conflictBody["code"] != "idempotency_conflict" || conflictBody["existing_item_id"] != handle.ItemID {
		t.Fatalf("conflicting enqueue = %s", extractText(t, conflict))
	}

	caller := map[string]any{"application_id": "codex", "principal_ref": "worker-2"}
	getCall, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.hitl_get", Arguments: map[string]any{
			"contract_version": "1.0", "item_id": handle.ItemID, "caller": caller,
		},
	})
	if err != nil || getCall.IsError || getCall.StructuredContent == nil {
		t.Fatalf("hitl_get structured call = %#v, %v", getCall, err)
	}
	structuredGet, _ := json.Marshal(getCall.StructuredContent)
	var get hitl.RetrievalResult
	if decodeErr := json.Unmarshal(structuredGet, &get); decodeErr != nil {
		t.Fatalf("decode hitl_get structuredContent: %v", decodeErr)
	}
	if get.Mode != "get" || get.WaitStatus != "not_waited" || get.Item.ItemID != handle.ItemID ||
		get.Item.State != interaction.InteractionStateStaged {
		t.Fatalf("hitl_get = %#v", get)
	}
	waited := callInteractionTool[hitl.RetrievalResult](t, client, "tangent.hitl_await", map[string]any{
		"contract_version": "1.0", "item_id": handle.ItemID, "caller": caller, "wait_ms": 0,
	})
	if waited.Mode != "await" || waited.WaitStatus != "timeout" || waited.Item.State != interaction.InteractionStateStaged {
		t.Fatalf("hitl_await timeout = %#v", waited)
	}

	unauthorized, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.hitl_get",
		Arguments: map[string]any{
			"contract_version": "1.0", "item_id": handle.ItemID,
			"caller": map[string]any{"application_id": "another-app"},
		},
	})
	if err != nil {
		t.Fatalf("unauthorized get transport error: %v", err)
	}
	if !unauthorized.IsError {
		t.Fatalf("unauthorized get unexpectedly succeeded: %s", extractText(t, unauthorized))
	}

	withdrawn := callInteractionTool[hitl.TerminalOutcome](t, client, "tangent.hitl_withdraw", map[string]any{
		"contract_version": "1.0", "item_id": handle.ItemID, "caller": caller,
		"expected_revision": handle.Revision, "reason": "No longer needed",
	})
	if withdrawn.State != interaction.InteractionStateCanceled ||
		withdrawn.Cause != interaction.TerminalCauseCallerWithdrawn {
		t.Fatalf("hitl_withdraw = %#v", withdrawn)
	}
	repeated := callInteractionTool[hitl.TerminalOutcome](t, client, "tangent.hitl_withdraw", map[string]any{
		"contract_version": "1.0", "item_id": handle.ItemID, "caller": caller,
	})
	if repeated.InteractionRevision != withdrawn.InteractionRevision || repeated.Cause != withdrawn.Cause {
		t.Fatalf("repeated hitl_withdraw = %#v, want %#v", repeated, withdrawn)
	}
	terminal := callInteractionTool[hitl.RetrievalResult](t, client, "tangent.hitl_get", map[string]any{
		"contract_version": "1.0", "item_id": handle.ItemID, "caller": caller,
	})
	if terminal.Item.QueuePosition != nil || terminal.Item.TerminalOutcome == nil ||
		terminal.Item.TerminalOutcome.Cause != interaction.TerminalCauseCallerWithdrawn {
		t.Fatalf("terminal hitl_get = %#v", terminal)
	}

	// Exercise the production stateless Streamable HTTP transport, not only
	// the SDK's in-memory transport. A new HTTP request resumes exclusively by
	// durable item handle and caller scope.
	httpServer := httptest.NewServer(server.HTTPHandler())
	defer httpServer.Close()
	httpResult := callHTTPMCP(t, httpServer.URL, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name": "tangent.hitl_get",
			"arguments": map[string]any{
				"contract_version": "1.0", "item_id": handle.ItemID, "caller": caller,
			},
		},
	})
	resultObject, _ := httpResult["result"].(map[string]any)
	content, _ := resultObject["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("HTTP hitl_get result = %#v", httpResult)
	}
	textBlock, _ := content[0].(map[string]any)
	var httpProjection hitl.RetrievalResult
	if err := json.Unmarshal([]byte(textBlock["text"].(string)), &httpProjection); err != nil ||
		httpProjection.Item.ItemID != handle.ItemID {
		t.Fatalf("HTTP hitl_get projection = %#v, %v", httpProjection, err)
	}
}

func cloneMap(value map[string]any) map[string]any {
	raw, _ := json.Marshal(value)
	var clone map[string]any
	_ = json.Unmarshal(raw, &clone)
	return clone
}

func callHTTPMCP(t *testing.T, endpoint string, payload map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal HTTP MCP payload: %v", err)
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("build HTTP MCP request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("HTTP MCP request: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read HTTP MCP response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("HTTP MCP status = %d, body %s", response.StatusCode, body)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode HTTP MCP response: %v (body %s)", err, body)
	}
	return decoded
}
