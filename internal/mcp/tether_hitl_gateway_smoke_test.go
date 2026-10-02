//go:build tether_smoke

package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
)

// TestTetherNativeFlatHITLGateway is an opt-in cross-repository smoke. It
// builds the local sibling Tether checkout into a temporary path, starts its
// actual stdio gateway in `--only tangent` native-flat mode, discovers the
// live Tangent SSE tool surface, and forwards all four HITL operations. It
// never modifies Tether source or the user's catalog/state.
//
// Run from the Tangent repository with:
//
//	go test -tags tether_smoke -race ./internal/mcp \
//	  -run '^TestTetherNativeFlatHITLGateway$' -count=1 -v
func TestTetherNativeFlatHITLGateway(t *testing.T) {
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
	if err := extensions.RegisterHITLItem(envelopeService); err != nil {
		t.Fatalf("RegisterHITLItem: %v", err)
	}
	interactions, err := interaction.NewService(
		interaction.NewStore(database),
		interaction.NewEnvelopeDefinitionCatalog(envelopeService, HostVersion),
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
	// Register the exact production HITL tool definitions and handlers on a
	// minimal upstream server. Tether's curated mode selects servers, not tools;
	// using Tangent's full legacy catalog here makes this cross-repository smoke
	// spend most of its time sanitizing unrelated, very large workflow schemas.
	tangentServer := &Server{
		mcp: mcpsdk.NewServer(&mcpsdk.Implementation{
			Name: implementationName, Version: implementationVersion,
		}, nil),
		hitl: hitlService,
	}
	if err := tangentServer.registerHITLTools(); err != nil {
		t.Fatalf("registerHITLTools: %v", err)
	}
	var upstreamRequests synchronizedLog
	upstreamHandler := tangentServer.SSEHandler()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamRequests.append(request.Method + " " + request.URL.RequestURI())
		upstreamHandler.ServeHTTP(writer, request)
	}))
	defer upstream.Close()

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
	serverEntry := fmt.Sprintf("id: tangent\ntransport: sse\nurl: %s\nenabled: true\n", upstream.URL)
	if err := os.WriteFile(filepath.Join(catalogRoot, "mcp-servers", "tangent.yaml"), []byte(serverEntry), 0o600); err != nil {
		t.Fatalf("write Tether Tangent entry: %v", err)
	}

	tetherPath := filepath.Join(tempRoot, "tether")
	build := exec.Command("go", "build", "-o", tetherPath, "./cmd/tether")
	build.Dir = tetherRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build local Tether: %v\n%s", err, output)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, tetherPath, "--catalog", catalogRoot, "mcp", "--proxy", "--only", "tangent")
	// The sibling currently wraps slog.Default with go-otel in a way that can
	// recursively acquire the standard logger mutex on its first startup log.
	// Telemetry is irrelevant to this routing contract and disabling it keeps
	// the black-box gateway process deterministic without changing its proxy.
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
	gateway := &stdioGateway{
		input: stdin, output: bufio.NewReader(stdout), stderr: &stderr, upstreamRequests: &upstreamRequests,
	}

	gateway.call(t, 1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "tangent-tether-hitl-smoke", "version": "1"},
	})
	gateway.notify(t, "notifications/initialized", map[string]any{})
	listed := gateway.call(t, 2, "tools/list", map[string]any{})
	result, _ := listed["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	want := map[string]bool{
		"tangent.hitl_enqueue": false, "tangent.hitl_get": false,
		"tangent.hitl_await": false, "tangent.hitl_withdraw": false,
	}
	for _, rawTool := range tools {
		tool, _ := rawTool.(map[string]any)
		name, _ := tool["name"].(string)
		if _, tracked := want[name]; !tracked {
			continue
		}
		want[name] = true
		schema, _ := tool["inputSchema"].(map[string]any)
		if schema["type"] != "object" || schema["$defs"] == nil {
			t.Errorf("Tether advertised %s without object-root HITL definitions: %#v", name, schema)
		}
		outputSchema, _ := tool["outputSchema"].(map[string]any)
		if outputSchema["type"] != "object" || outputSchema["$defs"] == nil {
			t.Errorf("Tether advertised %s without object-root HITL output definitions: %#v", name, outputSchema)
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("Tether native-flat tools/list omitted %s; stderr:\n%s\nupstream requests:\n%s", name, stderr.String(), upstreamRequests.String())
		}
	}

	request := map[string]any{
		"contract_version": "1.0", "kind": "attention",
		"idempotency_key": "tether:tangent:CW-20260904-0016:docs-attention-v1",
		"title":           "Review HITL documentation result",
		"summary":         "The direct and gateway examples use the same strict v1 schema.",
		"request":         "Acknowledge the documentation result, optionally leaving a note or reply.",
		"source": map[string]any{
			"application_id": "tether-docs-smoke", "application_label": "Tether",
			"agent_id": "hitl-docs-gateway",
		},
		"correlations": map[string]any{
			"project": map[string]any{"authority": "torque", "id": "PRJ-20260825-0002"},
			"task":    map[string]any{"authority": "torque", "id": "CW-20260904-0016"},
			"session": map[string]any{"authority": "tether", "id": "session-hitl-docs-gateway"},
		},
		"evidence": []any{map[string]any{
			"type": "text", "label": "Gateway mode",
			"content": "Native-flat discovery preserves the object-root HITL tool schema.",
		}},
	}
	enqueued := gateway.callTool(t, 3, "tangent.hitl_enqueue", request, false)
	itemID, _ := enqueued["item_id"].(string)
	if itemID == "" {
		t.Fatalf("Tether enqueue returned no item_id: %#v", enqueued)
	}
	caller := map[string]any{"application_id": "tether-docs-smoke"}
	got := gateway.callTool(t, 4, "tangent.hitl_get", map[string]any{
		"contract_version": "1.0", "item_id": itemID, "caller": caller,
	}, false)
	if got["mode"] != "get" {
		t.Fatalf("Tether get projection = %#v", got)
	}
	waited := gateway.callTool(t, 5, "tangent.hitl_await", map[string]any{
		"contract_version": "1.0", "item_id": itemID, "caller": caller, "wait_ms": 0,
	}, false)
	if waited["wait_status"] != "timeout" {
		t.Fatalf("Tether await projection = %#v", waited)
	}
	withdrawn := gateway.callTool(t, 6, "tangent.hitl_withdraw", map[string]any{
		"contract_version": "1.0", "item_id": itemID, "caller": caller,
	}, false)
	if withdrawn["state"] != "canceled" || withdrawn["cause"] != "caller_withdrawn" {
		t.Fatalf("Tether withdraw projection = %#v", withdrawn)
	}
	errorBody := gateway.callTool(t, 7, "tangent.hitl_get", map[string]any{
		"contract_version": "1.0", "item_id": itemID,
		"caller": map[string]any{"application_id": "wrong-caller"},
	}, true)
	if errorBody["code"] != "unauthorized" {
		t.Fatalf("Tether did not preserve HITL error body: %#v", errorBody)
	}
}

type stdioGateway struct {
	input            io.Writer
	output           *bufio.Reader
	stderr           *synchronizedBuffer
	upstreamRequests *synchronizedLog
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

type synchronizedLog struct {
	mu      sync.Mutex
	entries []string
}

func (l *synchronizedLog) append(entry string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, entry)
}

func (l *synchronizedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.entries, "\n")
}

func (g *stdioGateway) notify(t *testing.T, method string, params any) {
	t.Helper()
	g.write(t, map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (g *stdioGateway) call(t *testing.T, id int, method string, params any) map[string]any {
	t.Helper()
	g.write(t, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	for {
		line, err := g.output.ReadBytes('\n')
		if err != nil {
			t.Fatalf(
				"read Tether response for %s: %v\nstderr:\n%s\nupstream requests:\n%s",
				method, err, g.stderr.String(), g.upstreamRequests.String(),
			)
		}
		var response map[string]any
		if err := json.Unmarshal(line, &response); err != nil {
			continue
		}
		if responseID, ok := response["id"].(float64); !ok || int(responseID) != id {
			continue
		}
		if rpcError := response["error"]; rpcError != nil {
			t.Fatalf(
				"Tether JSON-RPC %s error: %#v\nstderr:\n%s\nupstream requests:\n%s",
				method, rpcError, g.stderr.String(), g.upstreamRequests.String(),
			)
		}
		return response
	}
}

func (g *stdioGateway) callTool(t *testing.T, id int, name string, arguments map[string]any, wantError bool) map[string]any {
	t.Helper()
	response := g.call(t, id, "tools/call", map[string]any{"name": name, "arguments": arguments})
	result, _ := response["result"].(map[string]any)
	isError, _ := result["isError"].(bool)
	if isError != wantError {
		t.Fatalf("Tether %s isError=%v, want %v: %#v", name, isError, wantError, result)
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("Tether %s returned no content: %#v", name, result)
	}
	textBlock, _ := content[0].(map[string]any)
	text, _ := textBlock["text"].(string)
	var body map[string]any
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		t.Fatalf("decode Tether %s body: %v (%s)", name, err, text)
	}
	if !wantError {
		structured, ok := result["structuredContent"].(map[string]any)
		if !ok || !reflect.DeepEqual(structured, body) {
			t.Fatalf("Tether %s did not preserve structured output: body=%#v structured=%#v", name, body, structured)
		}
	}
	return body
}

func (g *stdioGateway) write(t *testing.T, message map[string]any) {
	t.Helper()
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal Tether request: %v", err)
	}
	if _, err := fmt.Fprintf(g.input, "%s\n", raw); err != nil {
		t.Fatalf("write Tether request: %v\nstderr:\n%s", err, g.stderr.String())
	}
}
