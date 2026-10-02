//go:build tether_smoke

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/roomflow"
	tangentws "github.com/hollis-labs/tangent/internal/ws"
)

// TestTetherNativeFlatRoomWorkflowCompletion is an opt-in cross-repository
// smoke for the room workflow completion contract.
//
// It matters because Tether's gateway speaks legacy SSE, which is the
// transport whose sessions the server-wide read deadline used to kill, and
// because a gateway is exactly the kind of caller whose transport dies long
// before a human finishes deciding. It drives the whole contract through the
// real gateway process: async receipt, operator answer, handle recovery,
// original-invocation retry, and idempotent acknowledgement.
//
// Run from the Tangent repository with:
//
//	go test -tags tether_smoke -race ./internal/mcp \
//	  -run '^TestTetherNativeFlatRoomWorkflowCompletion$' -count=1 -v
func TestTetherNativeFlatRoomWorkflowCompletion(t *testing.T) {
	tetherRoot := filepath.Clean(filepath.Join("..", "..", "..", "tether"))
	if _, err := os.Stat(filepath.Join(tetherRoot, "go.mod")); err != nil {
		t.Skipf("local Tether sibling is unavailable at %s: %v", tetherRoot, err)
	}

	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "tangent.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := tangentdb.RunMigrations(database); err != nil {
		t.Fatalf("db.RunMigrations: %v", err)
	}
	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if err := extensions.RegisterTriage(envelopeService); err != nil {
		t.Fatalf("RegisterTriage: %v", err)
	}
	interactions, err := interaction.NewService(
		interaction.NewStore(database),
		interaction.NewEnvelopeDefinitionCatalog(envelopeService, HostVersion),
		interaction.WithAwaitPollInterval(5*time.Millisecond),
		interaction.WithSurfaceAccessPolicy(hitl.SurfaceAccessPolicy{}),
		interaction.WithDeliveryWorkerPolicy(roomflow.DeliveryWorkerPolicy{}),
	)
	if err != nil {
		t.Fatalf("interaction.NewService: %v", err)
	}
	manager := room.NewManager(database)

	// A deliberately small upstream surface. Tether's curated mode selects
	// servers rather than tools, and sanitizing Tangent's full catalog would
	// make this cross-repository smoke spend its time on unrelated schemas.
	tangentServer := &Server{
		envSvc:     envelopeService,
		dispatcher: envelope.NewDispatcher(envelopeService),
		manager:    manager,
		mcp: mcpsdk.NewServer(&mcpsdk.Implementation{
			Name: implementationName, Version: implementationVersion,
		}, nil),
		interactions: interactions,
	}
	flow, err := roomflow.New(interactions, manager, tangentServer, "",
		roomflow.WithCompatibilityWindow(500*time.Millisecond),
		roomflow.WithLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))),
	)
	if err != nil {
		t.Fatalf("roomflow.New: %v", err)
	}
	tangentServer.roomflow = flow

	sessionCreateSchema, err := buildSchema(sessionCreateInputSchemaJSON, "session_create")
	if err != nil {
		t.Fatalf("build session_create schema: %v", err)
	}
	mcpsdk.AddTool(tangentServer.mcp, &mcpsdk.Tool{
		Name: "tangent.session_create", Description: "Create a Tangent room.",
		InputSchema: sessionCreateSchema,
	}, tangentServer.handleSessionCreate)
	triageSchema, err := buildRoomWorkflowSchema(triageInputSchemaJSON, "triage")
	if err != nil {
		t.Fatalf("build triage schema: %v", err)
	}
	mcpsdk.AddTool(tangentServer.mcp, &mcpsdk.Tool{
		Name: "tangent.triage", Description: "Dispatch a triage envelope.",
		InputSchema: triageSchema,
	}, tangentServer.handleTriage)
	if err := tangentServer.registerInteractionTools(); err != nil {
		t.Fatalf("registerInteractionTools: %v", err)
	}

	var upstreamRequests synchronizedLog
	upstreamHandler := tangentServer.SSEHandler()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamRequests.append(request.Method + " " + request.URL.RequestURI())
		upstreamHandler.ServeHTTP(writer, request)
	}))
	wsHandler := tangentws.New(manager, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	wsHandler.SetOriginPatterns([]string{"*"})
	wsServer := httptest.NewServer(wsHandler)
	// Registered before the gateway so cleanup runs in the other order: the
	// gateway process holds a long-lived SSE connection, and closing the
	// upstream first would block waiting for it.
	t.Cleanup(wsServer.Close)
	t.Cleanup(upstream.Close)

	gateway := startTetherGateway(t, tetherRoot, upstream.URL, &upstreamRequests)

	gateway.call(t, 1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "tangent-tether-roomflow-smoke", "version": "1"},
	})
	gateway.notify(t, "notifications/initialized", map[string]any{})

	listed := gateway.call(t, 2, "tools/list", map[string]any{})
	result, _ := listed["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	sawCompletion := false
	sawAcknowledge := false
	for _, rawTool := range tools {
		tool, _ := rawTool.(map[string]any)
		switch tool["name"] {
		case "tangent.triage":
			schema, _ := tool["inputSchema"].(map[string]any)
			properties, _ := schema["properties"].(map[string]any)
			if _, ok := properties["completion"].(map[string]any); ok {
				sawCompletion = true
			}
		case "tangent.interaction_acknowledge":
			sawAcknowledge = true
		}
	}
	if !sawCompletion {
		t.Fatalf("Tether did not preserve the completion selector on tangent.triage; upstream:\n%s",
			upstreamRequests.String())
	}
	if !sawAcknowledge {
		t.Fatal("Tether native-flat tools/list omitted tangent.interaction_acknowledge")
	}

	created := gateway.callTool(t, 3, "tangent.session_create", map[string]any{"title": "tether roomflow"}, false)
	roomID, _ := created["roomID"].(string)
	if roomID == "" {
		t.Fatalf("session_create returned no roomID: %#v", created)
	}

	const envelopeID = "tether-roomflow-1"
	triageArguments := map[string]any{
		"completion": map[string]any{"mode": "async"},
		"envelope": map[string]any{
			"v": 1, "id": envelopeID, "type": "tangent.triage",
			"data": map[string]any{"prompt": "Triage through Tether", "items": []any{"one"}},
			"meta": map[string]any{"roomID": roomID},
		},
	}
	receipt := gateway.callTool(t, 4, "tangent.triage", triageArguments, false)
	if receipt["status"] != "pending" {
		t.Fatalf("Tether async call did not return a pending receipt: %#v", receipt)
	}
	handle, _ := receipt["handle"].(map[string]any)
	interactionID, _ := handle["interaction_id"].(string)
	if interactionID == "" {
		t.Fatalf("Tether pending receipt carries no durable handle: %#v", receipt)
	}

	// The operator answers through the room's own WebSocket, entirely outside
	// the gateway's transport.
	conn, _, err := websocket.Dial(context.Background(),
		"ws"+strings.TrimPrefix(wsServer.URL, "http")+"?roomID="+roomID, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	frame := readTetherFrame(t, conn)
	if frame["envelopeId"] != envelopeID {
		t.Fatalf("operator saw envelopeId %v", frame["envelopeId"])
	}
	writeTetherFrame(t, conn, map[string]any{
		"type": "response", "envelopeId": envelopeID, "revision": frame["revision"],
		"response": map[string]any{
			"v": 1, "envelopeId": envelopeID, "kind": "data", "status": "submitted",
			"payload":     map[string]any{"decision": "keep"},
			"completedAt": "2026-09-04T12:57:17Z",
		},
	})

	// Handle recovery through the gateway.
	var stored map[string]any
	for attempt := range 40 {
		outcome := gateway.callTool(t, 5+attempt, "tangent.interaction_get", map[string]any{
			"interaction_id": interactionID, "requester_scope": roomflow.DefaultCaller.Scope,
		}, false)
		if resolution, ok := outcome["resolution"].(map[string]any); ok {
			stored, _ = resolution["response_payload"].(map[string]any)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if stored == nil {
		t.Fatalf("Tether never observed the durable resolution; upstream:\n%s", upstreamRequests.String())
	}
	if stored["envelopeId"] != envelopeID || stored["status"] != "submitted" {
		t.Fatalf("recovered response = %#v", stored)
	}

	// Retrying the original invocation through the gateway returns the same
	// immutable result rather than re-asking the operator.
	retried := gateway.callTool(t, 100, "tangent.triage", triageArguments, false)
	if retried["envelopeId"] != envelopeID || retried["status"] != "submitted" {
		t.Fatalf("Tether original-invocation retry = %#v", retried)
	}
	if fmt.Sprint(retried["payload"]) != fmt.Sprint(stored["payload"]) {
		t.Fatalf("Tether retry payload diverged: %#v vs %#v", retried["payload"], stored["payload"])
	}

	first := gateway.callTool(t, 101, "tangent.interaction_acknowledge", map[string]any{
		"interaction_id": interactionID, "requester_scope": roomflow.DefaultCaller.Scope,
	}, false)
	second := gateway.callTool(t, 102, "tangent.interaction_acknowledge", map[string]any{
		"interaction_id": interactionID, "requester_scope": roomflow.DefaultCaller.Scope,
	}, false)
	if first["created"] != true || second["created"] != false ||
		first["acknowledgement_id"] != second["acknowledgement_id"] {
		t.Fatalf("Tether acknowledgement is not idempotent: %#v then %#v", first, second)
	}
}

// startTetherGateway builds and starts the local Tether sibling's stdio
// gateway in native-flat `--only tangent` mode against the supplied upstream.
func startTetherGateway(
	t *testing.T,
	tetherRoot string,
	upstreamURL string,
	upstreamRequests *synchronizedLog,
) *stdioGateway {
	t.Helper()
	tempRoot := t.TempDir()
	catalogRoot := filepath.Join(tempRoot, "catalog")
	if err := os.MkdirAll(filepath.Join(catalogRoot, "mcp-servers"), 0o750); err != nil {
		t.Fatalf("create Tether catalog: %v", err)
	}
	global := fmt.Sprintf(`version: 0.1.0
catalog:
  defaults:
    state_db: %q
    workspace_root: %q
    temp_root: %q
`, filepath.Join(tempRoot, "state", "tether.db"), filepath.Join(tempRoot, "workspaces"), filepath.Join(tempRoot, "tmp"))
	if err := os.WriteFile(filepath.Join(catalogRoot, "global.yaml"), []byte(global), 0o600); err != nil {
		t.Fatalf("write Tether global catalog: %v", err)
	}
	serverEntry := fmt.Sprintf("id: tangent\ntransport: sse\nurl: %s\nenabled: true\n", upstreamURL)
	if err := os.WriteFile(filepath.Join(catalogRoot, "mcp-servers", "tangent.yaml"), []byte(serverEntry), 0o600); err != nil {
		t.Fatalf("write Tether Tangent entry: %v", err)
	}

	tetherPath := filepath.Join(tempRoot, "tether")
	build := exec.Command("go", "build", "-o", tetherPath, "./cmd/tether")
	build.Dir = tetherRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build local Tether: %v\n%s", err, output)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, tetherPath, "--catalog", catalogRoot, "mcp", "--proxy", "--only", "tangent")
	command.Env = append(os.Environ(), "HOLLIS_OTEL_DISABLED=1")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatalf("Tether stdin: %v", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("Tether stdout: %v", err)
	}
	var stderr synchronizedBuffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start Tether gateway: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	})
	return &stdioGateway{
		input: stdin, output: bufio.NewReader(stdout),
		stderr: &stderr, upstreamRequests: upstreamRequests,
	}
}

// readTetherFrame reads the next presentation frame, skipping the connection
// lifecycle frames the handler sends on attach.
func readTetherFrame(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("ws read: %v", err)
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

func writeTetherFrame(t *testing.T, conn *websocket.Conn, message map[string]any) {
	t.Helper()
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal ws frame: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatalf("ws write: %v", err)
	}
}
