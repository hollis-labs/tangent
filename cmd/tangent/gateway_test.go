package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	gmcpclient "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/client"

	"github.com/hollis-labs/tangent/internal/access"
)

const gatewayHost = "tangent.pilot.example.test"

// gatewayHostFor is a host configured the way the live one will be for the Tether gateway: the
// gateway's key named for the producer, listed as a producer and a trusted forwarder, and the
// public name allowed.
func gatewayHostFor(t *testing.T) *host {
	t.Helper()
	base, tg := startedFull(t, nil, nil, map[string]any{
		"allowed_hosts":      []any{gatewayHost},
		"gateway_producers":  []any{"tether-gateway"},
		"trusted_forwarders": []any{"tether-gateway"},
	})
	jar, _ := cookiejar.New(nil)
	h := &host{t: t, base: base, tg: tg, owner: &http.Client{Jar: jar, Timeout: 20 * time.Second}}
	h.ownerDo("POST", "/api/login", map[string]any{"username": "owner", "password": testOwnerPassword})
	return h
}

// The gateway reaches Tangent through the local TLS proxy: the public Host, X-Forwarded-Proto
// https from a loopback peer, its key, and the X-Tether-* attestation of the session it forwards
// for. The host check strips Forwarded, X-Forwarded-Host, X-Forwarded-For and X-Real-IP; it must
// leave the attestation, which the caller middleware reads.
func TestProxiedGatewayKeepsItsForwardedSessionHeaders(t *testing.T) {
	h := gatewayHostFor(t)
	key := h.key("tether-gateway", "tangent.mcp", access.View, access.Submit)
	const session = "0123abcd-4567-89ab-cdef-0123456789ab"
	enqueue := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tangent_turns_enqueue","arguments":{
		"contract_version":"1.0","session_id":"sess-gw","turn_id":"t1","idempotency_key":"gw:sess-gw:t1","kind":"question",
		"source":{"agent_id":"a1","application_id":"tether","agent_label":"A"},"title":"Which?","content":"x"}}}`
	post := func(headers map[string]string, host string) (int, string) {
		t.Helper()
		// A session first (initialize), as a client does.
		do := func(body, sid string) (*http.Response, string) {
			req, _ := http.NewRequest("POST", h.base+"/mcp", strings.NewReader(body))
			req.Host = host
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("X-Forwarded-Proto", "https")
			req.Header.Set("X-Forwarded-For", "203.0.113.9")
			if sid != "" {
				req.Header.Set("Mcp-Session-Id", sid)
			}
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			raw, _ := io.ReadAll(res.Body)
			return res, string(raw)
		}
		res, ibody := do(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"tether-proxy","version":"0"}}}`, "")
		if res.StatusCode != http.StatusOK {
			return res.StatusCode, ibody
		}
		sid := res.Header.Get("Mcp-Session-Id")
		_, _ = do(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, sid)
		res, body := do(enqueue, sid)
		return res.StatusCode, body
	}
	attest := map[string]string{"X-Forwarded-User-Id": "session:" + session, "X-Tether-Session-Id": session, "X-Tether-Agent-Urn": "msg://agent/x/y"}

	if code, body := post(attest, gatewayHost); code != http.StatusOK || strings.Contains(body, "invalid forwarded caller context") || !strings.Contains(body, "item_id") {
		t.Fatalf("a proxied, attested call: %d %.300s", code, body)
	}
	// The attestation is read, not ignored: an inconsistent one is refused whole.
	bad := map[string]string{"X-Forwarded-User-Id": "session:someone-else", "X-Tether-Session-Id": session}
	if _, body := post(bad, gatewayHost); !strings.Contains(body, "invalid forwarded caller context") {
		t.Fatalf("an inconsistent attestation was accepted: %.300s", body)
	}
	// The public name is allowed; a name that is not configured is refused.
	if code, _ := post(attest, "elsewhere.example.test"); code != http.StatusForbidden && code != http.StatusMisdirectedRequest && code != http.StatusBadRequest {
		t.Fatalf("an unlisted Host reached /mcp: %d", code)
	}
}

// Tether builds its http upstreams with the libs go-mcp client over the go-sdk Streamable
// transport. This drives the real host with that same client (identity tether-proxy): initialize,
// tools/list and one tool call, against the module as it answers today (G6b).
func TestGoMCPClientOverStreamableHTTP(t *testing.T) {
	h := newHost(t)
	key := h.key("tether-proxy", "tangent.mcp", access.View)
	pool := gmcpclient.NewPool(
		gmcpclient.WithIdentity("tether-proxy", "0"),
		gmcpclient.WithHTTPClient(func(map[string]string, int) *http.Client {
			return &http.Client{Transport: bearer{key}}
		}),
	)
	t.Cleanup(func() { _ = pool.Close() })
	if err := pool.Register("tangent", gmcpclient.ServerConfig{Transport: gmcpclient.TransportHTTP, URL: h.base + "/mcp"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	listed, err := pool.ListTools(ctx, "tangent")
	if err != nil || len(listed.Tools) == 0 {
		t.Fatalf("tools/list: %v, %v", listed, err)
	}
	var view string
	for _, tool := range listed.Tools {
		if scope, ok := h.tg.toolScope(tool.Name); ok && scope == access.View && strings.HasSuffix(tool.Name, "_list") {
			view = tool.Name
			break
		}
	}
	if view == "" {
		t.Fatal("no view-scoped list tool is published")
	}
	res, _, err := pool.CallTool(ctx, "tangent", view, map[string]any{})
	if err != nil || res == nil {
		t.Fatalf("%s: %v", view, err)
	}
	var out any
	if err := json.Unmarshal([]byte(textOf(res)), &out); err != nil && res.IsError {
		t.Fatalf("%s answered an error: %s", view, textOf(res))
	}
}

// A client with no key, as a plugin binary that was not reinstalled is, is refused at /mcp.
func TestMCPWithoutACredentialIsRefused(t *testing.T) {
	h := newHost(t)
	if got := h.call("", "POST", "/mcp"); got != http.StatusUnauthorized {
		t.Fatalf("/mcp without a credential = %d, want 401", got)
	}
}

func TestTheStdioCommandSaysWhatToUseInstead(t *testing.T) {
	err := stdioCmd().RunE(nil, nil)
	if err == nil || !strings.Contains(err.Error(), "/mcp") || !strings.Contains(err.Error(), "stdio") {
		t.Fatalf("tangent mcp = %v, want an error naming stdio and the HTTP endpoint", err)
	}
}
