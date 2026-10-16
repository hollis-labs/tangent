package boot

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/participant"
	"github.com/hollis-labs/tangent/internal/server"
)

const e2eOperatorToken = "fixture-operator-token-7f3c9a1e5b2d4f60-a8e1c3b5d7f9"

type e2eApp struct {
	baseURL string
	cookie  *http.Cookie
	svc     *Services
}

func startE2E(t *testing.T, mutate func(*Config)) *e2eApp {
	t.Helper()
	root := t.TempDir()
	cfg := Config{
		DBPath:          filepath.Join(root, "tangent.db"),
		PluginDir:       filepath.Join(root, "plugins"),
		OperatorToken:   e2eOperatorToken,
		Logger:          slog.New(slog.DiscardHandler),
		eventsRelayTick: 25 * time.Millisecond,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	svc, srv, closer, err := Boot(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = closer.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		<-done
		_ = closer.Close()
	})
	app := &e2eApp{baseURL: "http://" + ln.Addr().String(), svc: svc}
	app.cookie = app.elevatedSession(t)
	return app
}

// elevatedSession mints a participant session the way a browser navigation
// does, then elevates it with the operator token so it may resolve approvals.
func (a *e2eApp) elevatedSession(t *testing.T) *http.Cookie {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, a.baseURL+"/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Site", "none")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	var minted *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == participant.CookieName && c.Value != "" {
			minted = c
		}
	}
	if minted == nil {
		t.Fatal("no participant session minted")
	}
	_, _, resp = a.post(t, server.ElevatePath, minted, map[string]any{"token": e2eOperatorToken})
	for _, c := range resp.Cookies() {
		if c.Name == participant.CookieName && c.Value != "" {
			return c
		}
	}
	t.Fatalf("elevation issued no session (status %d)", resp.StatusCode)
	return nil
}

func (a *e2eApp) post(t *testing.T, path string, cookie *http.Cookie, payload any) (int, map[string]any, *http.Response) {
	t.Helper()
	raw, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, a.baseURL+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", a.baseURL)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	body := map[string]any{}
	_ = json.Unmarshal(data, &body)
	return resp.StatusCode, body, resp
}

// enqueue calls tangent_hitl_enqueue over MCP, as an agent does.
func (a *e2eApp) enqueue(t *testing.T, key string) string {
	t.Helper()
	args := map[string]any{
		"contract_version": "1.0", "kind": "approval", "idempotency_key": key,
		"title": "Approve " + key, "summary": "Exercise the typed event stream.",
		"request": "Approve or deny this bounded test request.",
		"source":  map[string]any{"application_id": "codex", "agent_id": "release-worker"},
		"impact":  map[string]any{"approve": "Only a record is written.", "deny": "Only a record is written."},
	}
	raw, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "tangent_hitl_enqueue", "arguments": args},
	})
	req, _ := http.NewRequest(http.MethodPost, a.baseURL+"/mcp", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var rpc struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &rpc); err != nil || resp.StatusCode != http.StatusOK ||
		rpc.Result.IsError || len(rpc.Result.Content) != 1 {
		t.Fatalf("enqueue = %d %s (%v)", resp.StatusCode, body, err)
	}
	var handle struct {
		ItemID string `json:"item_id"`
	}
	if err := json.Unmarshal([]byte(rpc.Result.Content[0].Text), &handle); err != nil || handle.ItemID == "" {
		t.Fatalf("enqueue handle = %s (%v)", rpc.Result.Content[0].Text, err)
	}
	return handle.ItemID
}

// resolve presents and approves the item through the browser API.
func (a *e2eApp) resolve(t *testing.T, itemID string) {
	t.Helper()
	probe := func() (rev, presented int64) {
		req, _ := http.NewRequest(http.MethodGet, a.baseURL+"/api/hitl/items/"+itemID, nil)
		req.AddCookie(a.cookie)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var v struct {
			Revision                    int64 `json:"revision"`
			PresentedProjectionRevision int64 `json:"presented_projection_revision"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&v); err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("GET item = %d (%v)", resp.StatusCode, err)
		}
		return v.Revision, v.PresentedProjectionRevision
	}
	rev, _ := probe()
	if status, body, _ := a.post(t, "/api/hitl/items/"+itemID+"/present", a.cookie, map[string]any{
		"expected_revision": rev, "presented_projection_revision": rev, "connection_id": "e2e",
	}); status != http.StatusOK {
		t.Fatalf("present = %d %v", status, body)
	}
	rev, presented := probe()
	if status, body, _ := a.post(t, "/api/hitl/items/"+itemID+"/resolve", a.cookie, map[string]any{
		"expected_revision": rev, "presented_projection_revision": presented,
		"response": map[string]any{"kind": "approval", "decision": "approved"},
	}); status != http.StatusOK || body["state"] != "resolved" {
		t.Fatalf("resolve = %d %v", status, body)
	}
}

type sseFrame struct {
	ID, Event string
	Data      json.RawMessage
}

type sseStream struct {
	frames chan sseFrame
	cancel context.CancelFunc
}

func (a *e2eApp) openStream(t *testing.T, lastEventID string) *sseStream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/api/hitl/events", nil)
	req.AddCookie(a.cookie)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
		t.Fatalf("open stream = %d: %s", resp.StatusCode, body)
	}
	s := &sseStream{frames: make(chan sseFrame, 64), cancel: cancel}
	go func() {
		defer func() { _ = resp.Body.Close() }()
		defer close(s.frames)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
		var f sseFrame
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if f.Event != "" {
					s.frames <- f
				}
				f = sseFrame{}
			case strings.HasPrefix(line, "id:"):
				f.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			case strings.HasPrefix(line, "event:"):
				f.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				f.Data = json.RawMessage(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
	}()
	t.Cleanup(s.close)
	return s
}

func (s *sseStream) close() { s.cancel() }

// nextTyped returns the next approval.* frame, skipping legacy revision pings.
func (s *sseStream) nextTyped(t *testing.T) sseFrame {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case f, ok := <-s.frames:
			if !ok {
				t.Fatal("stream closed before a typed event arrived")
			}
			if strings.HasPrefix(f.Event, "approval.") {
				return f
			}
		case <-deadline:
			t.Fatal("no typed event within 15s")
		}
	}
}

// expectNoTyped asserts no approval.* frame arrives within the window.
func (s *sseStream) expectNoTyped(t *testing.T, window time.Duration) {
	t.Helper()
	deadline := time.After(window)
	for {
		select {
		case f, ok := <-s.frames:
			if !ok {
				return
			}
			if strings.HasPrefix(f.Event, "approval.") {
				t.Fatalf("unexpected typed event %s (%s)", f.Event, f.ID)
			}
		case <-deadline:
			return
		}
	}
}

var evtID = regexp.MustCompile(`^evt_[0-9a-f]{32}$`)

func checkEnvelope(t *testing.T, f sseFrame, wantKind, itemID string) {
	t.Helper()
	var env struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Subject string `json:"subject"`
	}
	if err := json.Unmarshal(f.Data, &env); err != nil {
		t.Fatalf("frame data is not an envelope: %v: %s", err, f.Data)
	}
	if f.Event != wantKind || env.Kind != wantKind || !evtID.MatchString(env.ID) ||
		env.Subject != "msg://approval/tangent/"+itemID {
		t.Fatalf("frame %s = %+v (want %s for %s)", f.ID, env, wantKind, itemID)
	}
	if !regexp.MustCompile(`^[0-9a-f]+:[0-9]+$`).MatchString(f.ID) || f.ID == env.ID {
		t.Fatalf("SSE id %q is not an <epoch>:<seq> cursor distinct from the envelope id", f.ID)
	}
}

// MCP enqueue -> browser resolve -> the SSE client sees approval.requested and
// approval.resolved; reconnecting with Last-Event-ID replays only what it
// missed, with no duplicates. With the post-commit wake off, delivery still
// works: the relay tick alone is enough.
func TestTypedApprovalEventsEndToEnd(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		// A one-hour tick means only the post-commit wake can deliver in time.
		"commit hook on":  func(c *Config) { c.eventsRelayTick = time.Hour },
		"commit hook off": func(c *Config) { c.eventsNoCommitHook = true },
	} {
		t.Run(name, func(t *testing.T) {
			app := startE2E(t, mutate)
			live := app.openStream(t, "")

			itemID := app.enqueue(t, "e2e-"+strings.ReplaceAll(name, " ", "-"))
			requested := live.nextTyped(t)
			checkEnvelope(t, requested, "approval.requested", itemID)

			app.resolve(t, itemID)
			resolved := live.nextTyped(t)
			checkEnvelope(t, resolved, "approval.resolved", itemID)
			if requested.ID == resolved.ID {
				t.Fatalf("both events carry cursor %s", requested.ID)
			}
			live.close()

			// Reconnect after the first event: only the second is replayed.
			again := app.openStream(t, requested.ID)
			replayed := again.nextTyped(t)
			if replayed.ID != resolved.ID || string(replayed.Data) != string(resolved.Data) {
				t.Fatalf("replay after %s = %s, want %s", requested.ID, replayed.ID, resolved.ID)
			}
			again.expectNoTyped(t, 300*time.Millisecond)
			again.close()

			// Reconnect at the head: nothing is replayed, nothing duplicated.
			head := app.openStream(t, resolved.ID)
			head.expectNoTyped(t, 300*time.Millisecond)

			var rows int
			if err := app.svc.DB.QueryRow(`SELECT COUNT(*) FROM stream_log_events`).Scan(&rows); err != nil || rows != 2 {
				t.Fatalf("journal rows = %d (%v), want exactly 2", rows, err)
			}
		})
	}
}

// The kill switch turns the typed stream off without a binary revert: no
// journal, no typed frames, but the outbox still records the transition so
// turning the stream back on loses nothing.
func TestEventsDisabledKillSwitch(t *testing.T) {
	app := startE2E(t, func(c *Config) { c.EventsDisabled = true })
	live := app.openStream(t, "")
	itemID := app.enqueue(t, "e2e-disabled")
	app.resolve(t, itemID)
	live.expectNoTyped(t, 500*time.Millisecond)

	var outbox, journal int
	if err := app.svc.DB.QueryRow(`SELECT COUNT(*) FROM event_outbox WHERE subject_id = ?`, itemID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if err := app.svc.DB.QueryRow(`SELECT COUNT(*) FROM stream_log_events`).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if outbox != 2 || journal != 0 {
		t.Fatalf("outbox rows = %d (want 2), journal rows = %d (want 0)", outbox, journal)
	}
}
