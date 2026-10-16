package hitl

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/catalog"

	"github.com/hollis-labs/tangent/internal/events"
	"github.com/hollis-labs/tangent/internal/interaction"
)

var evtID = regexp.MustCompile(`^evt_[0-9a-f]{32}$`)

type outboxEvent struct {
	row events.Row
	env runtimeevents.Event
}

func outboxEvents(t *testing.T, db *sql.DB) []outboxEvent {
	t.Helper()
	rows, err := db.Query(`SELECT event_id, stream, kind, COALESCE(subject_type,''), COALESCE(subject_id,''), envelope_json FROM event_outbox ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []outboxEvent
	for rows.Next() {
		var e outboxEvent
		var raw string
		if err = rows.Scan(&e.row.EventID, &e.row.Stream, &e.row.Kind, &e.row.SubjectType, &e.row.SubjectID, &raw); err != nil {
			t.Fatal(err)
		}
		e.row.EnvelopeJSON = []byte(raw)
		if err = json.Unmarshal([]byte(raw), &e.env); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func kindsOf(evs []outboxEvent) []string {
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.row.Kind)
	}
	return kinds
}

func payloadOf(t *testing.T, e outboxEvent) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(e.env.Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func presentApproval(t *testing.T, svc *Service, caller interaction.ActorBinding, key string) (ItemHandle, int64) {
	t.Helper()
	handle, err := svc.Enqueue(context.Background(), EnqueueInput{Request: hitlRequest("app", "agent", key, "Secret title"), Caller: caller})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	presented, err := svc.Present(context.Background(), PresentInput{ItemID: handle.ItemID, ExpectedRevision: handle.Revision, PresentedProjectionRevision: 2})
	if err != nil {
		t.Fatalf("Present: %v", err)
	}
	return handle, presented.Revision
}

func TestApprovalLifecycleEmitsOneEventPerTransition(t *testing.T) {
	ctx := context.Background()
	caller := directTestCaller("app", "agent")
	resolveWith := func(t *testing.T, svc *Service, h ItemHandle, rev int64, body string) {
		t.Helper()
		if _, err := svc.Resolve(ctx, ResolveInput{ItemID: h.ItemID, ExpectedRevision: rev, PresentedProjectionRevision: 2, Response: json.RawMessage(body)}); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
	}
	cases := []struct {
		name    string
		outcome string
		kind    string // resolver payload kind
		drive   func(t *testing.T, svc *Service, db *sql.DB)
	}{
		{"approve", "approved", "operator", func(t *testing.T, svc *Service, _ *sql.DB) {
			h, rev := presentApproval(t, svc, caller, "k")
			resolveWith(t, svc, h, rev, `{"kind":"approval","decision":"approved","note":"NOTE-CANARY"}`)
		}},
		{"deny", "denied", "operator", func(t *testing.T, svc *Service, _ *sql.DB) {
			h, rev := presentApproval(t, svc, caller, "k")
			resolveWith(t, svc, h, rev, `{"kind":"approval","decision":"denied"}`)
		}},
		{"withdraw", "withdrawn", "agent", func(t *testing.T, svc *Service, _ *sql.DB) {
			h, _ := presentApproval(t, svc, caller, "k")
			if _, err := svc.Withdraw(ctx, WithdrawInput{ItemID: h.ItemID, Caller: caller, Reason: "REASON-CANARY"}); err != nil {
				t.Fatal(err)
			}
		}},
		{"expire sweeper", "expired", "service", func(t *testing.T, svc *Service, _ *sql.DB) {
			if _, err := svc.Enqueue(ctx, EnqueueInput{Request: expiringRequest(t, "k", time.Now().Add(time.Hour)), Caller: caller}); err != nil {
				t.Fatal(err)
			}
			svc.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
			if n, err := svc.ExpireDue(ctx); err != nil || n != 1 {
				t.Fatalf("ExpireDue = %d, %v", n, err)
			}
		}},
		{"expire lazy", "expired", "service", func(t *testing.T, svc *Service, _ *sql.DB) {
			h, err := svc.Enqueue(ctx, EnqueueInput{Request: expiringRequest(t, "k", time.Now().Add(-time.Minute)), Caller: caller})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = svc.Get(ctx, GetInput{ItemID: h.ItemID, Caller: caller}); err != nil {
				t.Fatal(err)
			}
			// a second read of the already-expired item must not emit again
			if _, err = svc.Get(ctx, GetInput{ItemID: h.ItemID, Caller: caller}); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, db, _ := newTestService(t, "lifecycle.db")
			tc.drive(t, svc, db)
			evs := outboxEvents(t, db)
			if got := kindsOf(evs); len(got) != 2 || got[0] != catalog.KindApprovalRequested || got[1] != catalog.KindApprovalResolved {
				t.Fatalf("kinds = %v, want [requested resolved]", got)
			}
			if p := payloadOf(t, evs[1]); p["outcome"] != tc.outcome {
				t.Fatalf("outcome = %v, want %s (payload %v)", p["outcome"], tc.outcome, p)
			} else if r, _ := p["resolver"].(map[string]any); r["kind"] != tc.kind {
				t.Fatalf("resolver = %v, want kind %s", r, tc.kind)
			}
			if evs[0].env.Subject != evs[1].env.Subject || evs[0].row.SubjectID == "" || evs[0].row.Stream != events.DefaultStream {
				t.Fatalf("subject/stream mismatch: %+v / %+v", evs[0].row, evs[1].row)
			}
			if evs[1].env.Class.Severity != map[bool]string{true: "warning", false: "info"}[tc.outcome == "expired"] {
				t.Fatalf("severity = %q", evs[1].env.Class.Severity)
			}
			for _, e := range evs {
				if strings.Contains(string(e.row.EnvelopeJSON), "CANARY") || strings.Contains(string(e.row.EnvelopeJSON), "Secret title") {
					t.Fatalf("free text leaked into %s: %s", e.row.Kind, e.row.EnvelopeJSON)
				}
			}
			// a second terminal attempt is a conflict and writes nothing
			if len(evs) != len(outboxEvents(t, db)) {
				t.Fatal("outbox grew on read")
			}
		})
	}
}

func TestApprovalEnvelopeStamp(t *testing.T) {
	svc, _, db, _ := newTestService(t, "stamp.db")
	caller := directTestCaller("app", "agent")
	h, rev := presentApproval(t, svc, caller, "k")
	if _, err := svc.Resolve(context.Background(), ResolveInput{ItemID: h.ItemID, ExpectedRevision: rev, PresentedProjectionRevision: 2, Response: json.RawMessage(`{"kind":"approval","decision":"approved"}`)}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var prevSeq uint64
	for _, e := range outboxEvents(t, db) {
		ev := e.env
		if !evtID.MatchString(ev.ID) || ev.ID != e.row.EventID || seen[ev.ID] {
			t.Fatalf("bad or duplicate event id %q", ev.ID)
		}
		seen[ev.ID] = true
		if ev.SessionID != "" || ev.Subject != "msg://approval/tangent/"+h.ItemID || ev.Sequence <= prevSeq {
			t.Fatalf("subject/session/sequence wrong: %+v", ev)
		}
		prevSeq = ev.Sequence
		if err := ev.Validate(); err != nil {
			t.Fatal(err)
		}
		c := ev.Class
		if c.Category != "approval" || c.Sensitivity != "private" || c.Visibility != events.Visibility || !c.Sanitized || c.Redactions != 0 ||
			!strings.HasPrefix(c.PayloadSchema, "approval.") || !strings.HasSuffix(c.PayloadSchema, "@1") {
			t.Fatalf("class = %+v", c)
		}
		if ev.Actor == nil || ev.Actor.Verified != "operator-session" || ev.Actor.URN == "" || ev.Corr.CorrelationID != h.ItemID {
			t.Fatalf("actor/corr = %+v / %+v", ev.Actor, ev.Corr)
		}
		if ev.RecordedAt != nil || ev.Cursor != "" {
			t.Fatal("producer must not set recorded_at or cursor")
		}
	}
	if len(seen) != 2 {
		t.Fatalf("events = %d", len(seen))
	}
}

func TestNoEventForIdempotentReenqueueConflictOrOtherKinds(t *testing.T) {
	ctx := context.Background()
	svc, _, db, _ := newTestService(t, "quiet.db")
	caller := directTestCaller("app", "agent")
	request := hitlRequest("app", "agent", "same", "T")
	h, err := svc.Enqueue(ctx, EnqueueInput{Request: request, Caller: caller})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Enqueue(ctx, EnqueueInput{Request: request, Caller: caller}); err != nil {
		t.Fatal(err)
	}
	if got := len(outboxEvents(t, db)); got != 1 {
		t.Fatalf("idempotent re-enqueue wrote events: %d rows", got)
	}
	if _, err = svc.Withdraw(ctx, WithdrawInput{ItemID: h.ItemID, Caller: caller}); err != nil {
		t.Fatal(err)
	}
	// the same withdraw again is idempotent; a resolve loses to the terminal outcome
	if _, err = svc.Withdraw(ctx, WithdrawInput{ItemID: h.ItemID, Caller: caller}); err != nil {
		t.Fatal(err)
	}
	var conflict *TerminalConflictError
	if _, err = svc.Resolve(ctx, ResolveInput{ItemID: h.ItemID, ExpectedRevision: 1, PresentedProjectionRevision: 1, Response: json.RawMessage(`{"kind":"approval","decision":"approved"}`)}); !errors.As(err, &conflict) {
		t.Fatalf("resolve after withdraw = %v, want terminal conflict", err)
	}
	if got := kindsOf(outboxEvents(t, db)); len(got) != 2 {
		t.Fatalf("events = %v, want requested+resolved only", got)
	}
	if _, err = svc.Enqueue(ctx, EnqueueInput{Request: attentionRequest("app", "agent", "att", "A"), Caller: caller}); err != nil {
		t.Fatal(err)
	}
	if got := len(outboxEvents(t, db)); got != 2 {
		t.Fatalf("attention item emitted approval events: %d rows", got)
	}
}

type failingObserver struct{ err error }

func (f failingObserver) ObserveTransition(context.Context, *sql.Tx, interaction.Transition) error {
	return f.err
}

func TestEventWriteIsAtomicWithTransition(t *testing.T) {
	ctx := context.Background()
	svc, _, db, _, store := newTestServiceStore(t, filepath.Join(t.TempDir(), "atomic.db"))
	caller := directTestCaller("app", "agent")
	boom := errors.New("outbox unavailable")

	store.SetTransitionObserver(failingObserver{boom})
	if _, err := svc.Enqueue(ctx, EnqueueInput{Request: hitlRequest("app", "agent", "a", "T"), Caller: caller}); !errors.Is(err, boom) {
		t.Fatalf("Enqueue with failing observer = %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM interactions`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("interactions after failed create = %d, %v (transition must roll back)", n, err)
	}

	store.SetTransitionObserver(EventObserver{})
	h, rev := presentApproval(t, svc, caller, "b")
	store.SetTransitionObserver(failingObserver{boom})
	if _, err := svc.Resolve(ctx, ResolveInput{ItemID: h.ItemID, ExpectedRevision: rev, PresentedProjectionRevision: 2, Response: json.RawMessage(`{"kind":"approval","decision":"approved"}`)}); !errors.Is(err, boom) {
		t.Fatalf("Resolve with failing observer = %v", err)
	}
	var state string
	if err := db.QueryRow(`SELECT lifecycle_state FROM interactions WHERE id = ?`, h.ItemID).Scan(&state); err != nil || state == "resolved" {
		t.Fatalf("state after failed resolve = %q, %v", state, err)
	}
	if got := kindsOf(outboxEvents(t, db)); len(got) != 1 {
		t.Fatalf("outbox = %v, want only the committed requested event", got)
	}
	// recovery: the same resolve now succeeds and writes exactly one resolved row
	store.SetTransitionObserver(EventObserver{})
	if _, err := svc.Resolve(ctx, ResolveInput{ItemID: h.ItemID, ExpectedRevision: rev, PresentedProjectionRevision: 2, Response: json.RawMessage(`{"kind":"approval","decision":"approved"}`)}); err != nil {
		t.Fatal(err)
	}
	if got := kindsOf(outboxEvents(t, db)); len(got) != 2 {
		t.Fatalf("outbox after recovery = %v", got)
	}
}
