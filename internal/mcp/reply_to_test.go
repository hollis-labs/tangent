package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/replymail"
)

// Every room-backed tool must name the Tether address the operator's answer is
// mailed to. Without it an agent has to poll, which is what reply_to replaces.
func TestRoomWorkflowRequiresAReplyAddress(t *testing.T) {
	rg := newSessionRigWith(t, sessionRigOptions{
		durable: true, window: 5 * time.Second, requireReplyTo: true,
		dbPath: filepath.Join(t.TempDir(), "tangent.db"),
	})
	defer rg.cleanup()
	roomID, _ := createSession(t, rg, "reply-to")
	fixture := namedFixture(t, "tangent_form_collect")

	for name, completion := range map[string]map[string]any{
		"no completion":     nil,
		"no reply_to":       {"mode": "async"},
		"not a URN":         {"mode": "async", "reply_to": "agent-1"},
		"whitespace in URN": {"mode": "async", "reply_to": "msg://agent/a b"},
	} {
		result := callWorkflow(t, rg, fixture, roomID, "rt-"+strings.ReplaceAll(name, " ", "-"), completion, nil)
		if result.err == nil && (result.result == nil || !result.result.IsError) {
			t.Fatalf("%s: a call without a usable reply_to must be refused", name)
		}
	}

	ok := callWorkflow(t, rg, fixture, roomID, "rt-ok",
		map[string]any{"mode": "async", "reply_to": "msg://agent/agent-mux/agt_test000001"}, nil)
	if ok.err != nil || ok.result == nil || ok.result.IsError {
		t.Fatalf("a call with reply_to must be accepted: err=%v result=%+v", ok.err, ok.result)
	}

	var stored string
	if err := rg.db.QueryRow(`SELECT external_refs FROM interactions WHERE legacy_envelope_id = ?`, "rt-ok").Scan(&stored); err != nil {
		t.Fatalf("read stored interaction: %v", err)
	}
	if !strings.Contains(stored, `"reply_to":"msg://agent/agent-mux/agt_test000001"`) {
		t.Fatalf("reply_to must be stored on the interaction, got %s", stored)
	}
}

type fakeMailSender struct {
	mu   sync.Mutex
	sent []replymail.Message
}

func (f *fakeMailSender) Send(_ context.Context, msg replymail.Message) (replymail.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, msg)
	return replymail.Receipt{MessageID: "m-" + msg.IdempotencyKey, Reason: "offline"}, nil
}

func (f *fakeMailSender) snapshot() []replymail.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]replymail.Message(nil), f.sent...)
}

// The whole path: an agent asks with reply_to, the operator answers in the
// browser, the worker mails the answer, and a secret answer never leaves in it.
func TestOperatorAnswerIsMailedToTheAskingAgentWithoutSecrets(t *testing.T) {
	rg := newSessionRigWith(t, sessionRigOptions{
		durable: true, window: 30 * time.Second, requireReplyTo: true,
		dbPath: filepath.Join(t.TempDir(), "tangent.db"),
	})
	defer rg.cleanup()
	roomID, _ := createSession(t, rg, "reply-mail")

	sender := &fakeMailSender{}
	worker := replymail.New(interaction.NewStore(rg.db), sender, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Run(ctx)

	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	data := map[string]any{"form_id": "mail-probe", "schema": map[string]any{"fields": []any{
		map[string]any{"id": "pw", "type": "password", "label": "PW"},
		map[string]any{"id": "name", "type": "text", "label": "Name"},
	}}}
	answers := map[string]any{"pw": formSecretCanary + "-pw", "name": "ada"}
	const replyTo = "msg://agent/agent-mux/agt_test000002"
	done := make(chan advanceResult, 1)
	go func() {
		done <- callWorkflow(t, rg, namedFixture(t, "tangent_form_collect"), roomID, "mail-env",
			map[string]any{"mode": "async", "reply_to": replyTo}, data)
	}()
	frame := readWSFrame(t, conn, 10*time.Second)
	writeWSFrame(t, conn, map[string]any{
		"type": "response", "envelopeId": "mail-env", "revision": frame["revision"],
		"response": map[string]any{
			"v": 1, "envelopeId": "mail-env", "kind": "data", "status": "submitted",
			"completedAt": fixedCompletedAt,
			"payload":     map[string]any{"form_id": "mail-probe", "answers": answers},
		},
	})
	if r := <-done; r.err != nil || r.result.IsError {
		t.Fatalf("workflow: err=%v result=%+v", r.err, r.result)
	}

	var sent []replymail.Message
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if sent = sender.snapshot(); len(sent) > 0 {
			break
		}
	}
	if len(sent) != 1 {
		t.Fatalf("want exactly one reply mail, got %d", len(sent))
	}
	msg := sent[0]
	raw, _ := json.Marshal(msg.Payload)
	if msg.To != replyTo || msg.Kind != "response" || msg.IdempotencyKey == "" {
		t.Fatalf("message = %+v", msg)
	}
	if !strings.Contains(string(raw), `"name":"ada"`) || !strings.Contains(string(raw), "tangent.response") {
		t.Fatalf("the operator's answer must be in the mail, got %s", raw)
	}
	if strings.Contains(string(raw), formSecretCanary) {
		t.Fatalf("a secret answer leaked into the reply mail: %s", raw)
	}

	var state string
	if err := rg.db.QueryRow(`SELECT lifecycle_state FROM resolution_deliveries WHERE json_extract(destination_binding, '$.kind') = 'tether_mail'`).Scan(&state); err != nil || state != "delivered" {
		t.Fatalf("tether_mail delivery state = %q, %v", state, err)
	}
}
