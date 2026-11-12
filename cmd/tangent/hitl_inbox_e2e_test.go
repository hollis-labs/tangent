package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/access"
	"github.com/hollis-labs/tangent/internal/hitl"
)

// ownerRaw is ownerDo that returns the status and the body, whatever they are.
func (h *host) ownerRaw(method, path string, body any) (int, []byte) {
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
	return res.StatusCode, out
}

func (h *host) ownerInbox() hitl.OperatorInbox {
	h.t.Helper()
	code, raw := h.ownerRaw("GET", "/api/hitl", nil)
	var inbox hitl.OperatorInbox
	if code != http.StatusOK || json.Unmarshal(raw, &inbox) != nil {
		h.t.Fatalf("GET /api/hitl = %d %s", code, raw)
	}
	return inbox
}

func (h *host) ownerItem(id string) hitl.OperatorItemView {
	h.t.Helper()
	code, raw := h.ownerRaw("GET", "/api/hitl/items/"+id, nil)
	var item hitl.OperatorItemView
	if code != http.StatusOK || json.Unmarshal(raw, &item) != nil {
		h.t.Fatalf("GET /api/hitl/items/%s = %d %s", id, code, raw)
	}
	return item
}

func hitlRequest(app, agent, key, title string, evidence ...any) map[string]any {
	request := map[string]any{
		"contract_version": "1.0", "kind": "approval", "idempotency_key": key,
		"title": title, "summary": "Exercise the durable HITL boundary.",
		"request": "Approve or deny this bounded test request.",
		"source":  map[string]any{"application_id": app, "agent_id": agent},
		"impact":  map[string]any{"approve": "Only a record is written.", "deny": "Only a record is written."},
	}
	if len(evidence) > 0 {
		request["evidence"] = evidence
	}
	return request
}

func callHITL(t *testing.T, session *mcpsdk.ClientSession, tool string, args map[string]any, out any) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil || res.IsError {
		t.Fatalf("%s: %v %s", tool, err, textOf(res))
	}
	if err = json.Unmarshal([]byte(textOf(res)), out); err != nil {
		t.Fatalf("%s: decode %s: %v", tool, textOf(res), err)
	}
}

// revisionStream reads the browser's /api/hitl/events as the owner's session.
type revisionStream struct {
	body   *bufio.Reader
	cancel context.CancelFunc
}

func (h *host) openRevisionStream() *revisionStream {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	req, _ := http.NewRequestWithContext(ctx, "GET", h.base+"/api/hitl/events", nil)
	req.Header.Set("Origin", h.base)
	res, err := (&http.Client{Jar: h.owner.Jar}).Do(req)
	if err != nil {
		cancel()
		h.t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		cancel()
		h.t.Fatalf("GET /api/hitl/events = %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	h.t.Cleanup(func() { cancel(); _ = res.Body.Close() })
	return &revisionStream{body: bufio.NewReader(res.Body), cancel: cancel}
}

func (s *revisionStream) next(t *testing.T) string {
	t.Helper()
	var event string
	for {
		line, err := s.body.ReadString('\n')
		if err != nil {
			t.Fatalf("read the event stream: %v", err)
		}
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "event:"); ok {
			event = strings.TrimSpace(after)
		} else if after, ok = strings.CutPrefix(line, "data:"); ok && event == "revision" {
			var payload struct {
				Revision string `json:"revision"`
			}
			if err = json.Unmarshal([]byte(after), &payload); err != nil {
				t.Fatalf("decode a revision event: %v", err)
			}
			return payload.Revision
		}
	}
}

// The durable inbox through the real host: an agent's key enqueues over /mcp, the signed-in owner
// sees the same items in FIFO order over HTTP and decides one, the agent reads the outcome back,
// and all of it survives stopping the server and starting it again on the same data directory.
func TestHITLInboxJoinsPublicMCPBrowserProjectionAndRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	base, tg, stop := startedOn(t, dir, true, nil, nil, nil)
	h := &host{t: t, base: base, tg: tg}
	h.owner = &http.Client{Timeout: 20 * time.Second}
	h.owner.Jar, _ = cookiejar.New(nil)
	h.ownerDo("POST", "/api/login", map[string]any{"username": "owner", "password": testOwnerPassword})
	key := h.key("hitl-agent", "tangent.mcp", access.View, access.Submit, access.Withdraw)

	stream := h.openRevisionStream()
	if revision := stream.next(t); revision != "empty" {
		t.Fatalf("the first event revision = %q, want empty", revision)
	}

	approval := hitlRequest("codex", "release-worker", "e2e-approval", "Approve joined release",
		map[string]any{"type": "text", "label": "Operator note", "content": "rollback window remains open"},
		map[string]any{"type": "tangent_reference", "label": "Inbox surface", "surface_id": hitl.DefaultSurfaceID},
	)
	second := hitlRequest("nanite", "warning-worker", "e2e-second", "Approve a second item")
	handles := make([]hitl.ItemHandle, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, request := range []map[string]any{approval, second} {
		session := h.mcpSession(key)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			callHITL(t, session, "tangent_hitl_enqueue", request, &handles[i])
		}()
	}
	close(start)
	wg.Wait()
	if handles[0].ItemID == handles[1].ItemID || handles[0].QueueSequence+handles[1].QueueSequence != 3 {
		t.Fatalf("concurrent enqueues collided or lost FIFO order: %+v", handles)
	}
	byApp := map[string]hitl.ItemHandle{}
	agent := h.mcpSession(key)
	var retry hitl.ItemHandle
	callHITL(t, agent, "tangent_hitl_enqueue", approval, &retry)
	for _, handle := range handles {
		if retry.ItemID == handle.ItemID {
			byApp["codex"] = handle
		}
	}
	if byApp["codex"].ItemID == "" || retry.QueueSequence != byApp["codex"].QueueSequence {
		t.Fatalf("an idempotent retry returned %+v, want the item it already made among %+v", retry, handles)
	}

	// The browser projection: FIFO, only for the signed-in owner.
	inbox := h.ownerInbox()
	if len(inbox.Pending) != 2 || inbox.Pending[0].QueueSequence != 1 || inbox.Pending[1].QueueSequence != 2 {
		t.Fatalf("the owner's pending inbox = %+v, want two items in queue order", inbox.Pending)
	}
	if got := h.call(key, "GET", "/api/hitl"); got != http.StatusForbidden {
		t.Errorf("an agent key read the browser inbox: %d", got)
	}
	if got := h.call("", "GET", "/api/hitl"); got != http.StatusUnauthorized {
		t.Errorf("an anonymous read of the browser inbox: %d", got)
	}
	if code, raw := h.ownerRaw("GET", "/api/hitl/items/"+retry.ItemID+"/evidence/1/reference", nil); code != http.StatusOK {
		t.Errorf("the owner's evidence reference read = %d %s", code, raw)
	}
	if after := h.ownerInbox(); after.Revision != inbox.Revision {
		t.Errorf("evidence reads changed the inbox revision: %q -> %q", inbox.Revision, after.Revision)
	}

	// The stream carries the current revision on reconnect and advances on a new item.
	stream = h.openRevisionStream()
	if revision := stream.next(t); revision != inbox.Revision {
		t.Fatalf("reconnect revision = %q, want %q", revision, inbox.Revision)
	}
	var third hitl.ItemHandle
	callHITL(t, agent, "tangent_hitl_enqueue", hitlRequest("cursor", "late-worker", "e2e-third", "Approve a third item"), &third)
	if revision := stream.next(t); revision == inbox.Revision || revision == "" {
		t.Fatalf("the stream did not advance from %q: %q", inbox.Revision, revision)
	}
	stream.cancel()

	// The owner decides the first item: present, then resolve against the presented revision.
	item := h.ownerItem(retry.ItemID)
	var presented hitl.OperatorItemView
	code, raw := h.ownerRaw("POST", "/api/hitl/items/"+retry.ItemID+"/present", map[string]any{
		"expected_revision": item.Revision, "presented_projection_revision": item.Revision,
	})
	if code != http.StatusOK || json.Unmarshal(raw, &presented) != nil || presented.PresentedProjectionRevision == nil {
		t.Fatalf("presenting an item = %d %s", code, raw)
	}
	response := json.RawMessage(`{"kind":"approval","decision":"approved","note":"release evidence reviewed"}`)
	code, raw = h.ownerRaw("POST", "/api/hitl/items/"+retry.ItemID+"/resolve", map[string]any{
		"expected_revision": presented.Revision, "presented_projection_revision": *presented.PresentedProjectionRevision, "response": response,
	})
	if code != http.StatusOK {
		t.Fatalf("the owner's resolve = %d %s", code, raw)
	}
	if code, _ = h.ownerRaw("POST", "/api/hitl/items/"+retry.ItemID+"/resolve", map[string]any{
		"expected_revision": presented.Revision, "presented_projection_revision": *presented.PresentedProjectionRevision, "response": response,
	}); code != http.StatusConflict {
		t.Errorf("a stale second resolve = %d, want 409", code)
	}
	const outcome = `{"decision":"approved","kind":"approval","note":"release evidence reviewed"}`
	read := func(session *mcpsdk.ClientSession, tool string) {
		t.Helper()
		args := map[string]any{"contract_version": "1.0", "item_id": retry.ItemID, "caller": map[string]any{"application_id": "codex", "principal_ref": "release-worker"}}
		if tool == "tangent_hitl_await" {
			args["wait_ms"] = 0
		}
		var got hitl.RetrievalResult
		callHITL(t, session, tool, args, &got)
		if got.Item.TerminalOutcome == nil || got.Item.TerminalOutcome.Resolution == nil || string(got.Item.TerminalOutcome.Resolution.Response) != outcome {
			t.Fatalf("%s returned %+v, want the owner's decision", tool, got.Item)
		}
	}
	read(agent, "tangent_hitl_get")

	// An explicit withdrawal by the caller is a different thing from the browser going away.
	withdrawn := h.ownerItem(third.ItemID)
	var out hitl.TerminalOutcome
	callHITL(t, agent, "tangent_hitl_withdraw", map[string]any{
		"contract_version": "1.0", "item_id": third.ItemID, "caller": map[string]any{"application_id": "cursor", "principal_ref": "late-worker"},
		"expected_revision": withdrawn.Revision, "reason": "no longer needed",
	}, &out)
	if out.Cause != "caller_withdrawn" {
		t.Fatalf("a caller's withdrawal = %+v", out)
	}
	pendingID := handles[0].ItemID
	if pendingID == retry.ItemID {
		pendingID = handles[1].ItemID
	}

	stop()
	base, tg, _ = startedOn(t, dir, false, nil, nil, nil)
	h = &host{t: t, base: base, tg: tg, owner: &http.Client{Timeout: 20 * time.Second}}
	h.owner.Jar, _ = cookiejar.New(nil)
	h.ownerDo("POST", "/api/login", map[string]any{"username": "owner", "password": testOwnerPassword})

	after := h.ownerInbox()
	if len(after.Pending) != 1 || after.Pending[0].ItemID != pendingID {
		t.Fatalf("after the restart the owner's pending items = %+v, want only %s", after.Pending, pendingID)
	}
	if len(after.History) != 2 {
		t.Errorf("after the restart the owner's history has %d items, want the decided and the withdrawn one", len(after.History))
	}
	agent = h.mcpSession(key)
	read(agent, "tangent_hitl_get")
	read(agent, "tangent_hitl_await")
}
