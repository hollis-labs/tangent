package events

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/catalog"
)

func TestSanitizeDropsAndCountsUnknownKeys(t *testing.T) {
	raw, dropped, bad := Sanitize(catalog.KindApprovalRequested, map[string]any{
		"request_id": "i1", "hitl_ref": "msg://approval/tangent/i1", "class": "approval",
		"title": "free text", "note": "free text", "path": "/etc/passwd", "scope": map[string]any{"x": 1}, "actor": "forged",
	})
	if bad != "" || dropped != 5 {
		t.Fatalf("bad=%q dropped=%d payload=%s", bad, dropped, raw)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil || len(got) != 3 {
		t.Fatalf("payload = %v (%v)", got, err)
	}
	for _, leaked := range []string{"free text", "passwd", "forged"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("%q leaked: %s", leaked, raw)
		}
	}
}

func TestSanitizeFailsClosed(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{"request_id": "i1", "hitl_ref": "r", "outcome": "approved"}
	}
	cases := map[string]func(map[string]any){
		"oversized":  func(m map[string]any) { m["request_id"] = strings.Repeat("x", maxIDLen+1) },
		"opaque":     func(m map[string]any) { m["request_id"] = 7 },
		"vocabulary": func(m map[string]any) { m["outcome"] = "please approve" },
		"missing":    func(m map[string]any) { delete(m, "hitl_ref") },
	}
	for want, mutate := range cases {
		m := base()
		mutate(m)
		raw, _, bad := Sanitize(catalog.KindApprovalResolved, m)
		if bad != want || string(raw) != `{"sanitize_error":"`+want+`"}` {
			t.Fatalf("%s: bad=%q payload=%s", want, bad, raw)
		}
	}
	if _, _, bad := Sanitize("approval.nope", base()); bad != "kind" {
		t.Fatalf("unknown kind: %q", bad)
	}
	m := base()
	m["resolver"] = map[string]any{"kind": "operator", "id": "me", "name": "Display Name"}
	if raw, _, bad := Sanitize(catalog.KindApprovalResolved, m); bad == "" || strings.Contains(string(raw), "Display") {
		t.Fatalf("nested unknown key must fail closed, got %s", raw)
	}
}

func TestBuildStampIgnoresPayloadKeysAndFailsClosedEnvelope(t *testing.T) {
	db := openOutbox(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	row, err := Emitter{Host: "h1"}.Emit(context.Background(), tx, Approval{
		Kind: catalog.KindApprovalResolved, ItemID: "i1", Outcome: "bogus", At: time.Unix(5, 0),
		Resolver: Principal{Kind: "operator", ID: "me", URN: "msg://user/local/operator", Role: "operator"},
		Extra: map[string]any{
			"actor": map[string]any{"urn": "msg://user/evil"}, "scope": map[string]any{"host": "evil"},
			"corr": map[string]any{"correlation_id": "evil"}, "session_id": "evil",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var ev runtimeevents.Event
	if err = json.Unmarshal(row.EnvelopeJSON, &ev); err != nil {
		t.Fatal(err)
	}
	if string(ev.Payload) != `{"sanitize_error":"vocabulary"}` || ev.Class.Sanitized || ev.Class.Redactions != 4 {
		t.Fatalf("payload=%s class=%+v", ev.Payload, ev.Class)
	}
	if ev.Scope.Host != "h1" || ev.Corr.CorrelationID != "i1" || ev.Actor.URN != "msg://user/local/operator" || ev.SessionID != "" {
		t.Fatalf("stamp took a payload value: %+v %+v %+v", ev.Scope, ev.Corr, ev.Actor)
	}
	if !regexp.MustCompile(`^evt_[0-9a-f]{32}$`).MatchString(row.EventID) || ev.Sequence != 1 {
		t.Fatalf("id=%q seq=%d", row.EventID, ev.Sequence)
	}
	// the same row inserted again (a retry) does not duplicate
	if ok, insertErr := Insert(context.Background(), tx, row); insertErr != nil || ok {
		t.Fatalf("retry insert: ok=%v err=%v", ok, insertErr)
	}
	var n int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM event_outbox`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows = %d, %v", n, err)
	}
}

func TestVerifiedFollowsRecordedAssurance(t *testing.T) {
	for assurance, want := range map[string]string{
		"": "operator-session", "loopback-unverified": "operator-session",
		"operator-token": "token", "asserted": "claimed", "anything-else": "claimed",
	} {
		if got := Verified(assurance); got != want {
			t.Errorf("Verified(%q) = %q, want %q", assurance, got, want)
		}
	}
}
