package mcp_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/docs"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/turns"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const producerAToken = "test-only-producer-a-credential" // #nosec G101 -- synthetic test credential.
const producerBToken = "test-only-producer-b-credential" // #nosec G101 -- synthetic test credential.

type authSurfacePolicy struct{}

func (authSurfacePolicy) Authorize(surface, capability string) bool {
	return (hitl.SurfaceAccessPolicy{}).Authorize(surface, capability) && (turns.SurfaceAccessPolicy{}).Authorize(surface, capability) && (docs.SurfaceAccessPolicy{}).Authorize(surface, capability)
}
func (authSurfacePolicy) AuthorizeInboxRead(string) bool { return true }

func newCallerAuthServer(t *testing.T) (*tangentmcp.Server, *turns.Service) {
	t.Helper()
	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrationErr := tangentdb.RunMigrations(database); migrationErr != nil {
		t.Fatal(migrationErr)
	}
	env, err := envelope.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, register := range []func(*envelope.Service) error{extensions.RegisterHITLItem, extensions.RegisterAgentTurn, extensions.RegisterDocItem} {
		if registerErr := register(env); registerErr != nil {
			t.Fatal(registerErr)
		}
	}
	interactions, err := interaction.NewService(interaction.NewStore(database), interaction.NewEnvelopeDefinitionCatalog(env, tangentmcp.HostVersion), interaction.WithSurfaceAccessPolicy(authSurfacePolicy{}))
	if err != nil {
		t.Fatal(err)
	}
	hitlService, err := hitl.NewService(interactions)
	if err != nil {
		t.Fatal(err)
	}
	turnService, err := turns.NewService(interactions)
	if err != nil {
		t.Fatal(err)
	}
	docService, err := docs.NewService(interactions, docs.NewReadStore(database))
	if err != nil {
		t.Fatal(err)
	}
	auth, err := tangentmcp.ParseCallerAuth(fmt.Sprintf("alpha=%x,beta=%x", sha256.Sum256([]byte(producerAToken)), sha256.Sum256([]byte(producerBToken))))
	if err != nil {
		t.Fatal(err)
	}
	server, err := tangentmcp.New(env, envelope.NewDispatcher(env), room.NewManager(nil), "", tangentmcp.WithCallerAuth(auth), tangentmcp.WithInteractionService(interactions), tangentmcp.WithHITLService(hitlService), tangentmcp.WithTurnsService(turnService), tangentmcp.WithDocsService(docService))
	if err != nil {
		t.Fatal(err)
	}
	return server, turnService
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	if b.token != "" {
		clone.Header.Set("Authorization", "Bearer "+b.token)
		clone.Header.Set("X-Tangent-Via", "caddy")
	}
	return http.DefaultTransport.RoundTrip(clone)
}

func connectCallerAuthClient(ctx context.Context, t *testing.T, endpoint, transport, token string) *mcpsdk.ClientSession {
	t.Helper()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "caller-auth-test", Version: "v0.0.0"}, nil)
	httpClient := &http.Client{Transport: bearerTransport{token: token}}
	var wire mcpsdk.Transport = &mcpsdk.StreamableClientTransport{Endpoint: endpoint, HTTPClient: httpClient}
	if transport == "sse" {
		wire = &mcpsdk.SSEClientTransport{Endpoint: endpoint, HTTPClient: httpClient}
	}
	session, err := client.Connect(ctx, wire, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func requireAuthNotFound(ctx context.Context, t *testing.T, client *mcpsdk.ClientSession, tool string, args map[string]any) string {
	t.Helper()
	result, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil || result == nil || !result.IsError || !strings.Contains(extractText(t, result), "not_found") {
		t.Fatalf("%s cross-producer result = %v, %v", tool, result, err)
	}
	return extractText(t, result)
}

func TestCallerAuthProducerIsolationOverBothTransports(t *testing.T) {
	for _, transport := range []string{"http", "sse"} {
		t.Run(transport, func(t *testing.T) {
			server, turnService := newCallerAuthServer(t)
			handler := server.HTTPHandler()
			if transport == "sse" {
				handler = server.SSEHandler()
			}
			httpServer := httptest.NewServer(handler)
			t.Cleanup(httpServer.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			t.Cleanup(cancel)
			a := connectCallerAuthClient(ctx, t, httpServer.URL, transport, producerAToken)
			b := connectCallerAuthClient(ctx, t, httpServer.URL, transport, producerBToken)
			local := connectCallerAuthClient(ctx, t, httpServer.URL, transport, "")
			request := map[string]any{"contract_version": "1.0", "kind": "approval", "idempotency_key": "auth-test", "title": "Auth test", "summary": "Producer boundary", "request": "Approve test", "source": map[string]any{"application_id": "beta", "agent_id": "forged"}}
			item := callInteractionTool[hitl.ItemHandle](t, a, "tangent_hitl_enqueue", request)
			other := callInteractionTool[hitl.ItemHandle](t, b, "tangent_hitl_enqueue", request)
			if item.ItemID == other.ItemID {
				t.Fatal("idempotency crossed producers")
			}
			args := map[string]any{"contract_version": "1.0", "item_id": item.ItemID, "caller": map[string]any{"application_id": "alpha", "principal_ref": "forged"}}
			for _, client := range []*mcpsdk.ClientSession{b, local} {
				for _, tool := range []string{"tangent_hitl_get", "tangent_hitl_withdraw", "tangent_hitl_await"} {
					if client == b {
						requireAuthNotFound(ctx, t, client, tool, args)
					} else {
						result, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: tool, Arguments: args})
						if err != nil || result == nil || !result.IsError {
							t.Fatalf("local caller accessed gateway item: %v %v", result, err)
						}
					}
				}
				requireAuthNotFound(ctx, t, client, "tangent_inbox_get", map[string]any{"requester_scope": "gateway:alpha", "item_id": item.ItemID})
			}
			_ = callInteractionTool[hitl.RetrievalResult](t, a, "tangent_hitl_get", args)
			doc := callInteractionTool[docs.DocHandle](t, a, "tangent_docs_enqueue", map[string]any{
				"contract_version": "1.0", "idempotency_key": "doc-auth", "source": map[string]any{"agent_id": "forged", "application_id": "beta"},
				"title": "Auth doc", "summary": "Producer boundary", "content_markdown": "Private producer body", "requires_ack": false,
			})
			requireAuthNotFound(ctx, t, b, "tangent_inbox_get", map[string]any{"requester_scope": "gateway:alpha", "item_id": doc.ItemID})
			_ = callInteractionTool[map[string]any](t, a, "tangent_inbox_get", map[string]any{"requester_scope": "beta", "item_id": doc.ItemID})
			surface := callInteractionTool[interaction.SurfaceHandle](t, a, "tangent_surface_open", map[string]any{"caller": map[string]any{"scope": "beta", "principal_ref": "forged"}, "owner_scope": "gateway:beta", "idempotency_key": "auth-surface"})
			requireAuthNotFound(ctx, t, b, "tangent_surface_get", map[string]any{"surface_id": surface.SurfaceID, "requester_scope": "gateway:alpha"})
			_ = callInteractionTool[map[string]any](t, a, "tangent_surface_get", map[string]any{"surface_id": surface.SurfaceID, "requester_scope": "beta"})
			// A gateway cannot read or acknowledge another producer's turns by guessing
			// a session/item ID. Unknown IDs have the same refusal shape.
			turn := callInteractionTool[turns.TurnHandle](t, a, "tangent_turns_enqueue", turnRequest("auth-session", "auth-turn"))
			reply, err := turnService.Reply(ctx, turns.ReplyInput{ItemID: turn.ItemID, ExpectedRevision: turn.Revision, Action: "respond", ResponseText: "test reply"})
			if err != nil {
				t.Fatal(err)
			}
			foreignAwait := requireAuthNotFound(ctx, t, b, "tangent_turns_await", map[string]any{"session_id": "auth-session", "wait_ms": 0})
			missingAwait := requireAuthNotFound(ctx, t, b, "tangent_turns_await", map[string]any{"session_id": "unknown-session", "wait_ms": 0})
			foreignAck := requireAuthNotFound(ctx, t, b, "tangent_turns_ack", map[string]any{"item_id": turn.ItemID})
			missingAck := requireAuthNotFound(ctx, t, b, "tangent_turns_ack", map[string]any{"item_id": "unknown-item"})
			if foreignAwait != missingAwait || foreignAck != missingAck {
				t.Fatal("foreign and missing turn errors disclose existence")
			}
			result := callInteractionTool[turns.AwaitResult](t, a, "tangent_turns_await", map[string]any{"session_id": "auth-session", "wait_ms": 0})
			if len(result.Replies) != 1 || result.Replies[0].ItemID != turn.ItemID {
				t.Fatalf("producer await = %+v", result)
			}
			// Phase 1: tokenless local runner keeps the existing operator-wide flow.
			result = callInteractionTool[turns.AwaitResult](t, local, "tangent_turns_await", map[string]any{"session_id": "auth-session", "wait_ms": 0})
			if len(result.Replies) != 1 {
				t.Fatalf("local runner await = %+v", result)
			}
			_ = callInteractionTool[map[string]any](t, local, "tangent_turns_ack", map[string]any{"item_id": turn.ItemID, "reply_id": reply.Resolution.ResolutionID})
		})
	}
}

func TestCallerAuthLegacySessionCannotBePostedByAnotherProducer(t *testing.T) {
	server, _ := newCallerAuthServer(t)
	httpServer := httptest.NewServer(server.SSEHandler())
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+producerAToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	endpoint := ""
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			endpoint = strings.TrimPrefix(scanner.Text(), "data: ")
			break
		}
	}
	if endpoint == "" {
		t.Fatalf("missing SSE endpoint: %v", scanner.Err())
	}
	for _, token := range []string{producerBToken, "", producerAToken} {
		post, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`))
		if err != nil {
			t.Fatal(err)
		}
		post.Header.Set("Content-Type", "application/json")
		if token != "" {
			post.Header.Set("Authorization", "Bearer "+token)
		}
		result, err := http.DefaultClient.Do(post)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, result.Body)
		_ = result.Body.Close()
		want := http.StatusNotFound
		if token == producerAToken {
			want = http.StatusAccepted
		}
		if result.StatusCode != want {
			t.Fatalf("session POST status %d, want %d", result.StatusCode, want)
		}
	}
}

func TestCallerAuthRefusesProxyBeforeProtocolDispatch(t *testing.T) {
	configured, _ := newCallerAuthServer(t)
	unconfigured := newGatewayTestServer(t)
	for _, server := range []*tangentmcp.Server{configured, unconfigured} {
		for path, handler := range map[string]http.Handler{"/mcp": server.HTTPHandler(), "/sse": server.SSEHandler()} {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				for _, rpcMethod := range []string{"initialize", "tools/list"} {
					for _, authorization := range []string{"", "Bearer invalid"} {
						request := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q}`, rpcMethod)))
						request.RemoteAddr = "127.0.0.1:1234"
						request.Header.Set("X-Tangent-Via", "caddy")
						if authorization != "" {
							request.Header.Set("Authorization", authorization)
						}
						response := httptest.NewRecorder()
						handler.ServeHTTP(response, request)
						if response.Code != http.StatusUnauthorized {
							t.Fatalf("%s %s %s status = %d", method, path, rpcMethod, response.Code)
						}
					}
				}
			}
		}
	}
}
