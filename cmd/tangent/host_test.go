package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/access"
	"github.com/hollis-labs/tangent/internal/server"
)

// host is the real stack: the identity module's sessions and keys, the auth guard in enforce
// mode, the route table and the tools, on a throwaway data directory.
type host struct {
	t     *testing.T
	base  string
	tg    *tangent
	owner *http.Client
}

func newHost(t *testing.T) *host {
	t.Helper()
	base, tg := started(t, nil)
	jar, _ := cookiejar.New(nil)
	h := &host{t: t, base: base, tg: tg, owner: &http.Client{Jar: jar, Timeout: 20 * time.Second}}
	// `started` made the owner offline; the session is an ordinary sign-in.
	h.ownerDo("POST", "/api/login", map[string]any{"username": "owner", "password": testOwnerPassword})
	return h
}

func (h *host) ownerDo(method, path string, body any) map[string]any {
	h.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.base+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "test")
	req.Header.Set("Origin", h.base)
	res, err := h.owner.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out, _ := io.ReadAll(res.Body)
	if res.StatusCode/100 != 2 {
		h.t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, out)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	return m
}

// key mints an API key the way the owner does and returns its secret.
func (h *host) key(name string, scopes ...string) string {
	h.t.Helper()
	k, _ := h.ownerDo("POST", "/api/identity/keys", map[string]any{"name": name, "scopes": scopes})["key"].(string)
	if k == "" {
		h.t.Fatalf("no key minted for %s", name)
	}
	return k
}

// call sends a request carrying a bearer key (none when key is empty) and returns the status.
func (h *host) call(key, method, path string) int {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.base+path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	res, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	return res.StatusCode
}

// concrete turns a route pattern's path into one a request can name.
func concrete(path string) string {
	for {
		i := strings.Index(path, "{")
		if i < 0 {
			return path
		}
		j := strings.Index(path[i:], "}")
		path = path[:i] + "x" + path[i+j+1:]
	}
}

// G7 at the host: a decision is made by the signed-in owner and by nothing an agent holds. Every
// kind of agent credential is tried on every route that decides, including a key that holds the
// decision scope by name, by "tangent.*" and by "*" (which the guard treats as holding it).
func TestEveryAgentCredentialIsRefusedOnEveryDecidingRoute(t *testing.T) {
	h := newHost(t)
	keys := map[string]string{
		"decision-scope": h.key("agent-resolve", access.Resolve),
		"app-wildcard":   h.key("agent-app", "tangent.*"),
		"everything":     h.key("agent-all", "*"),
		"view-and-draft": h.key("agent-view", access.View, access.Draft, access.Submit),
	}
	var deciding []server.Route
	for _, route := range h.tg.table.Load().Routes() {
		if route.UserOnly {
			deciding = append(deciding, route)
		}
	}
	if len(deciding) == 0 {
		t.Fatal("no route decides: the table lost its user-only routes")
	}
	for _, route := range deciding {
		path := concrete(route.Path())
		if got := h.call("", route.Method(), path); got != http.StatusUnauthorized {
			t.Errorf("%s with no credential = %d, want 401", route.Pattern, got)
		}
		for kind, key := range keys {
			if got := h.call(key, route.Method(), path); got != http.StatusForbidden {
				t.Errorf("%s with the %s key = %d, want 403", route.Pattern, kind, got)
			}
		}
	}
}

// The control: the same routes are open to the owner's session, so the refusals above are the
// gate and not a route that cannot be reached.
func TestTheOwnerSessionReachesTheDecidingRoutes(t *testing.T) {
	h := newHost(t)
	for _, route := range h.tg.table.Load().Routes() {
		if !route.UserOnly {
			continue
		}
		req, _ := http.NewRequest(route.Method(), h.base+concrete(route.Path()), strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Requested-With", "test")
		req.Header.Set("Origin", h.base)
		res, err := h.owner.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
			t.Errorf("%s refused the owner's session: %d", route.Pattern, res.StatusCode)
		}
	}
}

// bearer adds a key to every request of an HTTP client, as an MCP client library does.
type bearer struct{ key string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.key)
	return http.DefaultTransport.RoundTrip(r)
}

func (h *host) mcpSession(key string) *mcpsdk.ClientSession {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	h.t.Cleanup(cancel)
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "host-test", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:   h.base + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{key}},
	}, nil)
	if err != nil {
		h.t.Fatalf("connect: %v", err)
	}
	h.t.Cleanup(func() { _ = session.Close() })
	return session
}

func (h *host) tools(session *mcpsdk.ClientSession) []string {
	h.t.Helper()
	var names []string
	for tool, err := range session.Tools(context.Background(), nil) {
		if err != nil {
			h.t.Fatal(err)
		}
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

func golden(t *testing.T, file string) []string {
	t.Helper()
	raw, err := os.ReadFile("../../internal/mcp/testdata/" + file) // #nosec G304 -- a constant name inside the repository
	if err != nil {
		t.Fatal(err)
	}
	names := strings.Fields(string(raw))
	sort.Strings(names)
	return names
}

// The published tool names are a contract with every client: the full server, behind the real
// host, lists exactly the names recorded from the build that was running before the port.
func TestTheHostPublishesExactlyTheRecordedToolNames(t *testing.T) {
	h := newHost(t)
	got := strings.Join(h.tools(h.mcpSession(h.key("agent-all", "*"))), "\n")
	if want := strings.Join(golden(t, "tool_names.txt"), "\n"); got != want {
		t.Fatalf("the published tool names changed:\n got: %s\nwant: %s", got, want)
	}
}

// The deprecated aliases stay callable (not listed: strict clients refuse their spelling), and
// are authorized as the tool they name.
func TestTheHostKeepsTheRecordedAliasesCallable(t *testing.T) {
	h := newHost(t)
	session := h.mcpSession(h.key("agent-all", "*"))
	for _, alias := range golden(t, "tool_aliases.txt")[:3] {
		res, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: alias, Arguments: map[string]any{}})
		if err != nil {
			t.Fatalf("%s: %v", alias, err)
		}
		if res.IsError && strings.Contains(textOf(res), "forbidden") {
			t.Errorf("alias %s is refused to a key that holds every scope: %s", alias, textOf(res))
		}
	}
}

func textOf(res *mcpsdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// A key lists, and may call, only what its scopes allow. /mcp itself needs tangent.mcp.
func TestAKeySeesAndCallsOnlyWhatItsScopesAllow(t *testing.T) {
	h := newHost(t)
	if got := h.call(h.key("no-mcp", access.View), "POST", "/mcp"); got != http.StatusForbidden {
		t.Fatalf("a key without tangent.mcp reached /mcp: %d", got)
	}
	session := h.mcpSession(h.key("reader", "tangent.mcp", access.View))
	listed := h.tools(session)
	if len(listed) == 0 || len(listed) >= len(golden(t, "tool_names.txt")) {
		t.Fatalf("a view-only key lists %d of %d tools", len(listed), len(golden(t, "tool_names.txt")))
	}
	byName := map[string]bool{}
	for _, name := range listed {
		byName[name] = true
		if scope, ok := h.tg.toolScope(name); !ok || scope != access.View {
			t.Errorf("a view-only key is shown %s, which needs %q", name, scope)
		}
	}
	// Calling what it is not shown is refused, not just hidden.
	res, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "tangent_hitl_enqueue", Arguments: map[string]any{}})
	if err != nil || !res.IsError || !strings.Contains(textOf(res), "forbidden") {
		t.Fatalf("a view-only key called a submit tool: %v %+v", err, res)
	}
}

// G6b, against the module as it is today, from the request the main client sends: a POST with
// Accept "application/json, text/event-stream". The reply is recorded, not wished: if the
// framework changes how it frames a reply, this test says so and the notes change with it.
func TestG6bWhatTheModuleAnswersToTheMainClientsPost(t *testing.T) {
	h := newHost(t)
	key := h.key("agent-mcp", "tangent.mcp", access.View)
	post := func(accept string) (contentType, framing string) {
		body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"g6b","version":"0"}}}`
		req, _ := http.NewRequest("POST", h.base+"/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+key)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		raw, _ := io.ReadAll(res.Body)
		first, _, _ := strings.Cut(string(raw), "\n")
		t.Logf("Accept %q -> %d %s; first line %.40q", accept, res.StatusCode, res.Header.Get("Content-Type"), first)
		return res.Header.Get("Content-Type"), first
	}
	if ct, first := post("application/json, text/event-stream"); !strings.HasPrefix(ct, "text/event-stream") || first != "event: message" {
		t.Errorf("the main client's POST is answered %q starting %q; the notes say text/event-stream framed as one SSE message", ct, first)
	}
	post("application/json")
	post("")
}

// The whole loop through the real host: an agent holding an API key puts a question in the
// inbox, the signed-in owner (and only the owner) answers it, the agent collects the answer and
// acknowledges it.
func TestAnAgentKeyAsksAndTheOwnerAnswers(t *testing.T) {
	h := newHost(t)
	session := h.mcpSession(h.key("worker", "tangent.mcp", access.View, access.Draft, access.Submit, access.Deliver))
	ctx := context.Background()
	call := func(name string, args map[string]any) map[string]any {
		res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %s", name, err, textOf(res))
		}
		var out map[string]any
		_ = json.Unmarshal([]byte(textOf(res)), &out)
		return out
	}
	handle := call("tangent_turns_enqueue", map[string]any{
		"contract_version": "1.0", "session_id": "sess-host", "turn_id": "t1", "idempotency_key": "host:sess-host:t1",
		"kind": "question", "source": map[string]any{"agent_id": "worker-1", "application_id": "codex", "agent_label": "Worker"},
		"title": "Which branch?", "content": "main or release?",
		"options": []map[string]any{{"label": "main", "value": "main"}, {"label": "release", "value": "rel"}},
	})
	itemID, _ := handle["item_id"].(string)
	if itemID == "" {
		t.Fatalf("no item in %v", handle)
	}
	// The owner sees it in the inbox, and answers.
	inbox := h.ownerDo("GET", "/api/turns", nil)
	if pending, _ := inbox["pending"].([]any); len(pending) != 1 {
		t.Fatalf("the owner's inbox = %v", inbox)
	}
	item := h.ownerDo("GET", "/api/turns/items/"+itemID, nil)
	h.ownerDo("POST", "/api/turns/items/"+itemID+"/reply", map[string]any{
		"expected_revision": item["revision"], "action": "respond", "response_text": "main", "selected_option": "main",
	})
	got := call("tangent_turns_await", map[string]any{"session_id": "sess-host", "wait_ms": 5000})
	replies, _ := got["replies"].([]any)
	if len(replies) != 1 {
		t.Fatalf("the agent's await = %v", got)
	}
	replyID, _ := replies[0].(map[string]any)["reply_id"].(string)
	call("tangent_turns_ack", map[string]any{"item_id": itemID, "reply_id": replyID})
}
