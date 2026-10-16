package mcp_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/turns"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const forwardedSessionA = "01234567-89ab-4cde-8fab-0123456789ab"
const forwardedSessionB = "11234567-89ab-4cde-8fab-0123456789ab"

type forwardedTransport struct {
	callsOnly      bool
	token, session string
	labels         http.Header
}

func (f forwardedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	attachIdentity := !f.callsOnly
	if f.callsOnly && r.Body != nil {
		raw, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			return nil, err
		}
		clone.Body = io.NopCloser(bytes.NewReader(raw))
		var rpc struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(raw, &rpc); err != nil {
			return nil, err
		}
		attachIdentity = rpc.Method == "tools/call"
	}
	clone.Header.Set("Authorization", "Bearer "+f.token)
	if attachIdentity && f.session != "" {
		clone.Header.Set("X-Forwarded-User-Id", "session:"+f.session)
		clone.Header.Set("X-Tether-Session-Id", f.session)
	}
	if attachIdentity {
		for k, vs := range f.labels {
			clone.Header[k] = append([]string(nil), vs...)
		}
	}
	return http.DefaultTransport.RoundTrip(clone)
}
func connectForwarded(ctx context.Context, t *testing.T, endpoint, transport string, f forwardedTransport) *mcpsdk.ClientSession {
	t.Helper()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "session-scope-test", Version: "v0.0.0"}, nil)
	// Match the live Tether gateway: HTTP initialization/discovery use the
	// parent scope; only tools/call carries the verified session headers.
	f.callsOnly = transport == "http"
	httpClient := &http.Client{Transport: f}
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

func TestForwardedSessionIsolationOverBothTransports(t *testing.T) {
	for _, transport := range []string{"http", "sse"} {
		t.Run(transport, func(t *testing.T) {
			server, turnService, database := newCallerAuthServerWithForwarders(t, "alpha")
			handler := server.HTTPHandler()
			if transport == "sse" {
				handler = server.SSEHandler()
			}
			httpServer := httptest.NewServer(handler)
			t.Cleanup(httpServer.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			t.Cleanup(cancel)
			a := connectForwarded(ctx, t, httpServer.URL, transport, forwardedTransport{token: producerAToken, session: forwardedSessionA, labels: http.Header{"X-Tether-Agent-Urn": {"agent:trusted"}, "X-Tether-Project-Id": {"project:trusted"}}})
			b := connectForwarded(ctx, t, httpServer.URL, transport, forwardedTransport{token: producerAToken, session: forwardedSessionB})
			parent := connectForwarded(ctx, t, httpServer.URL, transport, forwardedTransport{token: producerAToken})
			untrusted := connectForwarded(ctx, t, httpServer.URL, transport, forwardedTransport{token: producerBToken, session: forwardedSessionA})
			request := map[string]any{"contract_version": "1.0", "kind": "approval", "idempotency_key": "same-key", "title": "Session private", "summary": "Scope boundary", "request": "Approve test", "source": map[string]any{"application_id": "gateway:alpha/session:" + forwardedSessionB, "agent_id": "agent:forged"}}
			first := callInteractionTool[hitl.ItemHandle](t, a, "tangent_hitl_enqueue", request)
			relabeled := connectForwarded(ctx, t, httpServer.URL, transport, forwardedTransport{token: producerAToken, session: forwardedSessionA, labels: http.Header{"X-Tether-Agent-Urn": {"agent:changed"}}})
			again := callInteractionTool[hitl.ItemHandle](t, relabeled, "tangent_hitl_enqueue", request)
			if first.ItemID != again.ItemID {
				t.Fatal("same session idempotency changed")
			}
			for _, other := range []*mcpsdk.ClientSession{b, parent, untrusted} {
				created := callInteractionTool[hitl.ItemHandle](t, other, "tangent_hitl_enqueue", request)
				if created.ItemID == first.ItemID {
					t.Fatal("idempotency crossed session authority")
				}
				args := map[string]any{"contract_version": "1.0", "item_id": first.ItemID, "caller": map[string]any{"application_id": "gateway:alpha/session:" + forwardedSessionA, "principal_ref": "agent:trusted"}}
				for _, tool := range []string{"tangent_hitl_get", "tangent_hitl_await", "tangent_hitl_withdraw"} {
					requireAuthNotFound(ctx, t, other, tool, args)
				}
				requireAuthNotFound(ctx, t, other, "tangent_inbox_get", map[string]any{"requester_scope": "gateway:alpha/session:" + forwardedSessionA, "item_id": first.ItemID})
				// Isolation is symmetric, including the parent fallback.
				requireAuthNotFound(ctx, t, a, "tangent_hitl_get", map[string]any{"contract_version": "1.0", "item_id": created.ItemID, "caller": map[string]any{"application_id": "alpha"}})
			}
			_ = callInteractionTool[hitl.RetrievalResult](t, a, "tangent_hitl_get", map[string]any{"contract_version": "1.0", "item_id": first.ItemID, "caller": map[string]any{"application_id": "forged"}})
			surface := callInteractionTool[interaction.SurfaceHandle](t, a, "tangent_surface_open", map[string]any{
				"caller":      map[string]any{"scope": "gateway:alpha", "principal_ref": "forged"},
				"owner_scope": "gateway:alpha", "idempotency_key": "session-surface",
				"metadata": map[string]any{"forwarder_attribution": map[string]any{"agent_urn": "agent:forged"}},
			})
			for _, other := range []*mcpsdk.ClientSession{b, parent, untrusted} {
				requireAuthNotFound(ctx, t, other, "tangent_surface_get", map[string]any{"surface_id": surface.SurfaceID, "requester_scope": "gateway:alpha/session:" + forwardedSessionA})
				for _, tool := range []string{"tangent_inbox_list", "tangent_inbox_search"} {
					args := map[string]any{"requester_scope": "gateway:alpha/session:" + forwardedSessionA}
					if tool == "tangent_inbox_search" {
						args["query"] = "Scope boundary"
					}
					page := callInteractionTool[interaction.InboxReadPage](t, other, tool, args)
					for _, item := range page.Items {
						if item.ItemID == first.ItemID {
							t.Fatalf("%s disclosed another session's item", tool)
						}
					}
				}
			}
			var surfaceAudit string
			if err := database.QueryRow(`SELECT metadata FROM surface_events WHERE surface_id=? AND event_type='surface.created'`, surface.SurfaceID).Scan(&surfaceAudit); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(surfaceAudit, "agent:trusted") || strings.Contains(surfaceAudit, "agent:forged") {
				t.Fatalf("argument forged host surface audit: %s", surfaceAudit)
			}
			var metadata string
			if err := database.QueryRow(`SELECT metadata FROM interaction_events WHERE interaction_id=? AND event_type='interaction.submitted'`, first.ItemID).Scan(&metadata); err != nil {
				t.Fatal(err)
			}
			var audit struct {
				Forwarder interaction.ForwarderAttribution `json:"forwarder_attribution"`
			}
			if err := json.Unmarshal([]byte(metadata), &audit); err != nil {
				t.Fatal(err)
			}
			if audit.Forwarder.Forwarder != "alpha" || audit.Forwarder.SessionID != forwardedSessionA || audit.Forwarder.AgentURN != "agent:trusted" || audit.Forwarder.ProjectID != "project:trusted" {
				t.Fatalf("host attribution=%+v", audit.Forwarder)
			}
			turn := callInteractionTool[turns.TurnHandle](t, a, "tangent_turns_enqueue", turnRequest("shared-session-label", "session-turn"))
			reply, err := turnService.Reply(ctx, turns.ReplyInput{ItemID: turn.ItemID, ExpectedRevision: turn.Revision, Action: "respond", ResponseText: "session-private reply"})
			if err != nil {
				t.Fatal(err)
			}
			for _, other := range []*mcpsdk.ClientSession{b, parent} {
				requireAuthNotFound(ctx, t, other, "tangent_turns_await", map[string]any{"session_id": "shared-session-label", "wait_ms": 0})
				requireAuthNotFound(ctx, t, other, "tangent_turns_ack", map[string]any{"item_id": turn.ItemID, "reply_id": reply.Resolution.ResolutionID})
			}
			result := callInteractionTool[turns.AwaitResult](t, a, "tangent_turns_await", map[string]any{"session_id": "shared-session-label", "wait_ms": 0})
			if len(result.Replies) != 1 || result.Replies[0].ItemID != turn.ItemID {
				t.Fatal("foreign ack changed owner reply")
			}
			_ = callInteractionTool[map[string]any](t, a, "tangent_turns_ack", map[string]any{"item_id": turn.ItemID, "reply_id": reply.Resolution.ResolutionID})
		})
	}
}

func TestForwardedSSESessionCannotChangeAuthority(t *testing.T) {
	server, _, _ := newCallerAuthServerWithForwarders(t, "alpha")
	httpServer := httptest.NewServer(server.SSEHandler())
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	owner := &http.Client{Transport: forwardedTransport{token: producerAToken, session: forwardedSessionA}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL, nil)
	resp, err := owner.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	endpoint := ""
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			endpoint = strings.TrimPrefix(scanner.Text(), "data: ")
			break
		}
	}
	if endpoint == "" {
		t.Fatalf("missing endpoint: %v", scanner.Err())
	}
	for _, session := range []string{forwardedSessionB, "", forwardedSessionA} {
		client := &http.Client{Transport: forwardedTransport{token: producerAToken, session: session}}
		post, _ := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`))
		post.Header.Set("Content-Type", "application/json")
		response, err := client.Do(post)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		want := http.StatusNotFound
		if session == forwardedSessionA {
			want = http.StatusAccepted
		}
		if response.StatusCode != want {
			t.Fatalf("session %q POST=%d want=%d", session, response.StatusCode, want)
		}
	}
}

func TestForwardedHeadersMalformedBeforeBothTransports(t *testing.T) {
	server, _, _ := newCallerAuthServerWithForwarders(t, "alpha")
	for _, handler := range []http.Handler{server.HTTPHandler(), server.SSEHandler()} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			req := httptest.NewRequest(method, "http://127.0.0.1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
			req.Header.Set("Authorization", "Bearer "+producerAToken)
			req.Header.Set("X-Tether-Session-Id", forwardedSessionA)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != 400 || response.Body.String() != "Invalid forwarded caller context\n" {
				t.Fatalf("malformed headers response=%d %s", response.Code, response.Body.String())
			}
		}
	}
}

func TestForwardedSessionOwnsRoomAndOneTimeSecret(t *testing.T) {
	for _, transport := range []string{"http", "sse"} {
		t.Run(transport, func(t *testing.T) {
			auth, err := tangentmcp.ParseCallerAuth(fmt.Sprintf("alpha=%x", sha256.Sum256([]byte(producerAToken))), "alpha")
			if err != nil {
				t.Fatal(err)
			}
			rg := newSessionRigWith(t, sessionRigOptions{durable: true, callerAuth: auth})
			t.Cleanup(rg.cleanup)
			handler := rg.mcpSrv.HTTPHandler()
			if transport == "sse" {
				handler = rg.mcpSrv.SSEHandler()
			}
			httpServer := httptest.NewServer(handler)
			t.Cleanup(httpServer.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			t.Cleanup(cancel)
			a := connectForwarded(ctx, t, httpServer.URL, transport, forwardedTransport{token: producerAToken, session: forwardedSessionA})
			b := connectForwarded(ctx, t, httpServer.URL, transport, forwardedTransport{token: producerAToken, session: forwardedSessionB})
			parent := connectForwarded(ctx, t, httpServer.URL, transport, forwardedTransport{token: producerAToken})
			created := callInteractionTool[map[string]any](t, a, "tangent_session_create", map[string]any{"title": "session private", "meta": map[string]any{}})
			roomID := created["roomID"].(string)
			for _, other := range []*mcpsdk.ClientSession{b, parent} {
				for name, args := range map[string]map[string]any{
					"tangent_session_get":              {"roomID": roomID},
					"tangent_session_close":            {"roomID": roomID, "status": "closed"},
					"tangent_session_phase_advance":    {"roomID": roomID, "to_phase": "review", "reason": "forged"},
					"tangent_session_phase_output_set": {"roomID": roomID, "phase": "review", "key": "forged", "value": "forged"},
				} {
					requireAuthNotFound(ctx, t, other, name, args)
				}
				rooms := callInteractionTool[struct {
					Rooms []json.RawMessage `json:"rooms"`
				}](t, other, "tangent_session_list", map[string]any{"active_only": false})
				if len(rooms.Rooms) != 0 {
					t.Fatal("session room visible to foreign authority")
				}
			}
			conn, _, err := websocket.Dial(ctx, rg.wsURL(roomID), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			const envelopeID = "forwarded-secret"
			request := map[string]any{"envelope": map[string]any{"v": 1, "id": envelopeID, "type": "tangent.form-collect", "meta": map[string]any{"roomID": roomID}, "data": map[string]any{
				"form_id": "session-secret", "schema": map[string]any{"fields": []any{map[string]any{"id": "api_key", "label": "API key", "type": "password", "secret": true}}},
			}}, "completion": map[string]any{"mode": "async"}}
			_ = callInteractionTool[map[string]any](t, a, "tangent_form_collect", request)
			frame := readWSFrame(t, conn, 10*time.Second)
			writeWSFrame(t, conn, map[string]any{"type": "response", "envelopeId": envelopeID, "revision": frame["revision"], "response": map[string]any{
				"v": 1, "envelopeId": envelopeID, "kind": "data", "status": "submitted", "completedAt": fixedCompletedAt,
				"payload": map[string]any{"form_id": "session-secret", "answers": map[string]any{"api_key": formSecretCanary}},
			}})
			interactionID := singleInteractionIDIn(t, rg, roomID, envelopeID)
			deadline := time.Now().Add(10 * time.Second)
			for {
				var state string
				if queryErr := rg.db.QueryRow(`SELECT lifecycle_state FROM interactions WHERE id=?`, interactionID).Scan(&state); queryErr != nil {
					t.Fatal(queryErr)
				}
				if state == "resolved" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("form did not resolve: %s", state)
				}
				time.Sleep(10 * time.Millisecond)
			}
			args := map[string]any{"interaction_id": interactionID, "requester_scope": "gateway:alpha/session:" + forwardedSessionA}
			for _, other := range []*mcpsdk.ClientSession{b, parent} {
				requireAuthNotFound(ctx, t, other, "tangent_interaction_get", args)
				requireAuthNotFound(ctx, t, other, "tangent_interaction_await", map[string]any{"interaction_id": interactionID, "requester_scope": args["requester_scope"], "maximum_wait_ms": 5000})
			}
			first, err := a.CallTool(ctx, &mcpsdk.CallToolParams{Name: "tangent_interaction_get", Arguments: args})
			if err != nil || first.IsError || !strings.Contains(extractText(t, first), formSecretCanary) {
				t.Fatalf("owner did not receive secret: %v %+v", err, first)
			}
			second, err := a.CallTool(ctx, &mcpsdk.CallToolParams{Name: "tangent_interaction_get", Arguments: args})
			if err != nil || second.IsError || strings.Contains(extractText(t, second), formSecretCanary) || !strings.Contains(extractText(t, second), "form.secret-field") {
				t.Fatalf("secret replay: %v %+v", err, second)
			}
			assertNoCanaryInTables(t, rg.db, formSecretCanary)
		})
	}
}
