package mcp_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/tangent/internal/channel"
	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/docs"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/relay"
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
	server, turns, _ := newCallerAuthServerWithForwarders(t, "")
	return server, turns
}

func newCallerAuthServerWithForwarders(t *testing.T, forwarders string) (*tangentmcp.Server, *turns.Service, *sql.DB) {
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
	for _, register := range []func(*envelope.Service) error{extensions.RegisterTriage, extensions.RegisterHITLItem, extensions.RegisterAgentTurn, extensions.RegisterDocItem} {
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
	auth, err := tangentmcp.ParseCallerAuth(fmt.Sprintf("alpha=%x,beta=%x", sha256.Sum256([]byte(producerAToken)), sha256.Sum256([]byte(producerBToken))), forwarders)
	if err != nil {
		t.Fatal(err)
	}
	channels, err := channel.NewStore(database)
	if err != nil {
		t.Fatal(err)
	}
	relayStore, err := relay.NewStore(database, channels)
	if err != nil {
		t.Fatal(err)
	}
	server, err := tangentmcp.New(env, envelope.NewDispatcher(env), room.NewManager(database), "", tangentmcp.WithRelay(channels, relayStore), tangentmcp.WithCallerAuth(auth), tangentmcp.WithInteractionService(interactions), tangentmcp.WithHITLService(hitlService), tangentmcp.WithTurnsService(turnService), tangentmcp.WithDocsService(docService))
	if err != nil {
		t.Fatal(err)
	}
	return server, turnService, database
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
	if err != nil || result == nil || !result.IsError || !strings.Contains(strings.ToLower(extractText(t, result)), "not_found") {
		if result != nil {
			t.Fatalf("%s cross-producer result = %s, %v", tool, extractText(t, result), err)
		}
		t.Fatalf("%s cross-producer transport error = %v", tool, err)
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

// Adapters without an end-to-end producer binding must never dispatch on
// behalf of authenticated callers, even when arguments name a valid local actor.
func TestCallerAuthRefusesRelayAndPluginOverBothTransports(t *testing.T) {
	for _, transport := range []string{"http", "sse"} {
		t.Run(transport, func(t *testing.T) {
			server, _ := newCallerAuthServer(t)
			var dispatches atomic.Int32
			if err := server.AddPluginTool(echoTool(func(context.Context, subprocess.MCPCallRequest) (subprocess.MCPCallResult, error) {
				dispatches.Add(1)
				return subprocess.MCPCallResult{Content: json.RawMessage(`{"ok":true}`)}, nil
			})); err != nil {
				t.Fatal(err)
			}
			handler := server.HTTPHandler()
			if transport == "sse" {
				handler = server.SSEHandler()
			}
			httpServer := httptest.NewServer(handler)
			t.Cleanup(httpServer.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			t.Cleanup(cancel)
			local := connectCallerAuthClient(ctx, t, httpServer.URL, transport, "")
			opened := callInteractionTool[map[string]any](t, local, "tangent_relay_channel_open", map[string]any{"title": "local relay"})
			channelID := opened["channel_id"]
			alpha := map[string]any{"application_id": "alpha", "agent_id": "alpha-agent"}
			beta := map[string]any{"application_id": "beta", "agent_id": "beta-agent"}
			for _, source := range []map[string]any{alpha, beta} {
				_ = callInteractionTool[map[string]any](t, local, "tangent_relay_attach", map[string]any{"channel_id": channelID, "source": source, "runtime": map[string]any{"authority": "test"}})
			}
			sent := callInteractionTool[map[string]any](t, local, "tangent_relay_send", map[string]any{"channel_id": channelID, "source": beta, "idempotency_key": "private-message", "body": "private relay body", "recipient_application_id": "alpha", "recipient_agent_id": "alpha-agent"})
			probes := map[string]map[string]any{
				"tangent_relay_channel_open":     {"title": "forbidden"},
				"tangent_relay_attach":           {"channel_id": channelID, "source": alpha, "runtime": map[string]any{"authority": "forged"}},
				"tangent_relay_detach":           {"channel_id": channelID, "source": alpha},
				"tangent_relay_send":             {"channel_id": channelID, "source": alpha, "idempotency_key": "forged", "body": "forged"},
				"tangent_relay_receive":          {"channel_id": channelID, "source": alpha, "wait_ms": 0},
				"tangent_relay_ack":              {"source": alpha, "exchange_id": sent["exchange_id"]},
				"tangent_relay_capabilities_get": {"channel_id": channelID, "application_id": "alpha", "agent_id": "alpha-agent"},
				"tangent.stub_board":             {"filter": "all"},
			}
			for _, token := range []string{producerAToken, producerBToken} {
				client := connectCallerAuthClient(ctx, t, httpServer.URL, transport, token)
				for name, args := range probes {
					result, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
					if err != nil || result == nil || !result.IsError || relayErrorCode(t, result) != "not_authorized" {
						t.Fatalf("%s: result=%v err=%v", name, result, err)
					}
				}
			}
			if dispatches.Load() != 0 {
				t.Fatal("gateway invoked plugin callback")
			}
			_ = callInteractionTool[map[string]any](t, local, "tangent.stub_board", map[string]any{"filter": "all"})
			if dispatches.Load() != 1 {
				t.Fatal("local plugin callback did not run")
			}
			// Refused receive/ack/detach left the local participant and unacked message intact.
			received := callInteractionTool[struct {
				Items []struct {
					ExchangeID string `json:"exchange_id"`
				} `json:"items"`
			}](t, local, "tangent_relay_receive", map[string]any{"channel_id": channelID, "source": alpha, "wait_ms": 0, "unacked_only": true})
			if len(received.Items) != 1 || received.Items[0].ExchangeID != sent["exchange_id"] {
				t.Fatalf("local inbox after refusals = %+v", received)
			}
			_ = callInteractionTool[map[string]any](t, local, "tangent_relay_ack", map[string]any{"source": alpha, "exchange_id": sent["exchange_id"]})
		})
	}
}

func TestCallerAuthRoomInteractionAndInboxOwnership(t *testing.T) {
	for _, transport := range []string{"http", "sse"} {
		t.Run(transport, func(t *testing.T) {
			server, _ := newCallerAuthServer(t)
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
			created := callInteractionTool[map[string]any](t, a, "tangent_session_create", map[string]any{"title": "private room", "meta": map[string]any{}})
			roomID := created["roomID"]
			for name, args := range map[string]map[string]any{
				"tangent_session_get":              {"roomID": roomID},
				"tangent_session_close":            {"roomID": roomID, "status": "closed"},
				"tangent_session_phase_advance":    {"roomID": roomID, "to_phase": "review", "reason": "forged"},
				"tangent_session_phase_output_set": {"roomID": roomID, "phase": "review", "key": "forged", "value": "forged"},
			} {
				requireAuthNotFound(ctx, t, b, name, args)
			}
			workflow := map[string]any{"envelope": map[string]any{"v": 1, "id": "private-workflow", "type": "tangent.triage", "data": map[string]any{"prompt": "Unique private prompt", "items": []any{"a", "b"}}, "meta": map[string]any{"roomID": roomID}}, "completion": map[string]any{"mode": "async"}}
			requireAuthNotFound(ctx, t, b, "tangent_triage", workflow)
			_ = callInteractionTool[map[string]any](t, a, "tangent_triage", workflow)
			advance := map[string]any{"roomID": roomID, "envelope": workflow["envelope"], "completion": workflow["completion"]}
			requireAuthNotFound(ctx, t, b, "tangent_session_advance", advance)
			_ = callInteractionTool[map[string]any](t, a, "tangent_session_get", map[string]any{"roomID": roomID})
			rooms := callInteractionTool[struct {
				Rooms []room.RoomSummary `json:"rooms"`
			}](t, b, "tangent_session_list", map[string]any{"active_only": false})
			if len(rooms.Rooms) != 0 {
				t.Fatalf("foreign rooms listed: %+v", rooms)
			}
			surface := callInteractionTool[interaction.SurfaceHandle](t, a, "tangent_surface_open", map[string]any{"caller": map[string]any{"scope": "beta", "principal_ref": "forged"}, "owner_scope": "gateway:beta", "idempotency_key": "private-surface"})
			submitted := callInteractionTool[interaction.InteractionHandle](t, a, "tangent_interaction_submit", map[string]any{"surface_id": surface.SurfaceID, "caller": map[string]any{"scope": "beta", "principal_ref": "forged"}, "idempotency_key": "private-interaction", "definition": map[string]any{"kind": "tangent.triage", "version": "0.2"}, "request": map[string]any{"prompt": "Unique private prompt", "items": []any{"a", "b"}}})
			for _, name := range []string{"tangent_interaction_get", "tangent_interaction_await", "tangent_interaction_ack"} {
				args := map[string]any{"interaction_id": submitted.InteractionID, "requester_scope": "gateway:alpha"}
				if name == "tangent_interaction_await" {
					args["maximum_wait_ms"] = 5000
				}
				requireAuthNotFound(ctx, t, b, name, args)
			}
			_ = callInteractionTool[interaction.TerminalOutcome](t, a, "tangent_interaction_get", map[string]any{"interaction_id": submitted.InteractionID, "requester_scope": "beta"})
			for _, name := range []string{"tangent_inbox_list", "tangent_inbox_search"} {
				args := map[string]any{"requester_scope": "gateway:alpha"}
				if name == "tangent_inbox_search" {
					args["query"] = "Unique private prompt"
				}
				foreign := callInteractionTool[interaction.InboxReadPage](t, b, name, args)
				if len(foreign.Items) != 0 {
					t.Fatalf("%s leaked items: %+v", name, foreign)
				}
				own := callInteractionTool[interaction.InboxReadPage](t, a, name, args)
				found := false
				for _, item := range own.Items {
					if item.ItemID == submitted.InteractionID {
						found = true
					}
				}
				if !found {
					t.Fatalf("%s omitted own interaction: %+v", name, own)
				}
			}
		})
	}
}

// Caddy adds forwarding headers even before an explicit X-Tangent-Via marker
// is deployed. Installing auth therefore requires provisioning clients first.
func TestCallerAuthRefusesCaddyForwardingWithoutMarker(t *testing.T) {
	server, _ := newCallerAuthServer(t)
	for path, handler := range map[string]http.Handler{"/mcp": server.HTTPHandler(), "/sse": server.SSEHandler()} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			request := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
			request.RemoteAddr = "127.0.0.1:1234"
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json, text/event-stream")
			request.Header.Set("X-Forwarded-For", "100.74.49.44")
			request.Header.Set("X-Forwarded-Proto", "https")
			request.Header.Set("X-Forwarded-Host", "tangent.hub.nanite.cloud")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") == "" {
				t.Fatalf("unmarked Caddy %s %s = %d, challenge=%q", method, path, response.Code, response.Header().Get("WWW-Authenticate"))
			}
		}
	}
}
