package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/turns"
)

type fakeInbox struct{ entries []interaction.InboxEntry }

func (f *fakeInbox) BrowserInbox(context.Context) ([]interaction.InboxEntry, error) {
	return f.entries, nil
}

func getInbox(t *testing.T, h http.Handler, ifNoneMatch string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/inbox", nil)
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestInboxServesConditionalETag(t *testing.T) {
	svc := &fakeInbox{entries: []interaction.InboxEntry{{Sequence: 1}}}
	h := inboxHandler(svc)

	first := getInbox(t, h, "")
	if first.Code != http.StatusOK || first.Body.Len() == 0 {
		t.Fatalf("first read = %d, %d bytes", first.Code, first.Body.Len())
	}
	etag := first.Header().Get("ETag")
	if etag == "" || first.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("want ETag and no-store, got %q / %q", etag, first.Header().Get("Cache-Control"))
	}
	if first.Header().Get("Server-Timing") == "" {
		t.Fatal("missing Server-Timing")
	}

	for _, header := range []string{etag, "W/" + etag, `"other", ` + etag, "*"} {
		again := getInbox(t, h, header)
		if again.Code != http.StatusNotModified || again.Body.Len() != 0 {
			t.Fatalf("If-None-Match %q = %d with %d bytes, want 304 empty", header, again.Code, again.Body.Len())
		}
		if again.Header().Get("ETag") != etag {
			t.Fatalf("304 must repeat the ETag, got %q", again.Header().Get("ETag"))
		}
	}

	svc.entries = append(svc.entries, interaction.InboxEntry{Sequence: 2})
	changed := getInbox(t, h, etag)
	if changed.Code != http.StatusOK || changed.Header().Get("ETag") == etag {
		t.Fatalf("a changed list must return 200 with a new ETag, got %d %q", changed.Code, changed.Header().Get("ETag"))
	}
}

func TestTurnsInboxETagIgnoresReadTimestamp(t *testing.T) {
	read := func(at time.Time) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		inbox := turns.TurnsInbox{SyncedAt: at}
		err := writeConditionalJSON(rec, httptest.NewRequest(http.MethodGet, "/api/turns", nil), "turns", time.Now(), inbox, withoutSyncTime(inbox))
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	a := read(time.Unix(1000, 0))
	b := read(time.Unix(2000, 0))
	if a.Header().Get("ETag") != b.Header().Get("ETag") {
		t.Fatalf("an unchanged list must keep its ETag across reads: %q vs %q", a.Header().Get("ETag"), b.Header().Get("ETag"))
	}
	if a.Body.String() == b.Body.String() {
		t.Fatal("the body still reports the read time")
	}
	if !strings.HasPrefix(a.Header().Get("ETag"), "W/") {
		t.Fatalf("a tag over different bytes than the body must be weak, got %q", a.Header().Get("ETag"))
	}
}

func richEntry(id, title, deep string) interaction.InboxEntry {
	snapshot := `{"title":"` + title + `","body":"` + deep + `","source":{"agent_label":"Agent","secret":"x"}}`
	return interaction.InboxEntry{
		Sequence: 1,
		Interaction: interaction.InteractionRecord{
			ID:              id,
			RequestSnapshot: json.RawMessage(snapshot),
			ExternalRefs:    json.RawMessage(`{"a":1}`),
			Policy:          json.RawMessage(`{"b":2}`),
		},
		Resolution: &interaction.ResolutionRecord{ID: "res", InteractionID: id, ResponsePayload: json.RawMessage(`{"reply":"saved text"}`)},
	}
}

func TestInboxSummaryDropsHeavyFields(t *testing.T) {
	svc := &fakeInbox{entries: []interaction.InboxEntry{richEntry("i1", "Title", strings.Repeat("deep ", 5000))}}
	rec := httptest.NewRecorder()
	inboxHandler(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/inbox?view=summary", nil))
	var got struct {
		Items []struct {
			Interaction struct {
				RequestSnapshot map[string]any `json:"request_snapshot"`
				ExternalRefs    any            `json:"external_refs"`
			} `json:"interaction"`
			Resolution struct {
				ResponsePayload any `json:"response_payload"`
			} `json:"resolution"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	item := got.Items[0]
	if item.Interaction.RequestSnapshot["title"] != "Title" || item.Interaction.RequestSnapshot["body"] != nil {
		t.Fatalf("summary snapshot = %v", item.Interaction.RequestSnapshot)
	}
	if src := item.Interaction.RequestSnapshot["source"].(map[string]any); src["agent_label"] != "Agent" || src["secret"] != nil {
		t.Fatalf("summary source = %v", src)
	}
	if item.Interaction.ExternalRefs != nil || item.Resolution.ResponsePayload != nil {
		t.Fatal("external refs and resolution payload must be dropped")
	}
	if rec.Body.Len() > 1000 {
		t.Fatalf("summary should be small, got %d bytes", rec.Body.Len())
	}
	full := httptest.NewRecorder()
	inboxHandler(svc).ServeHTTP(full, httptest.NewRequest(http.MethodGet, "/api/inbox", nil))
	if full.Body.Len() < 20000 {
		t.Fatalf("the default read must stay full, got %d bytes", full.Body.Len())
	}
}

func TestInboxItemAndSearch(t *testing.T) {
	svc := &fakeInbox{entries: []interaction.InboxEntry{
		richEntry("i1", "One", "needle in the body"),
		richEntry("i2", "Two", "nothing"),
	}}
	mux := http.NewServeMux()
	mux.Handle("GET /api/inbox/items/{interactionID}", inboxItemHandler(svc))
	mux.Handle("GET /api/inbox/search", inboxSearchHandler(svc))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/inbox/items/i2", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"body":"nothing"`) {
		t.Fatalf("item = %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/inbox/items/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing item = %d", rec.Code)
	}
	for q, want := range map[string]string{"NEEDLE": `["i1"]`, "saved text": `["i1","i2"]`, "absent": `[]`, "": `[]`} {
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/inbox/search?q="+url.QueryEscape(q), nil))
		if !strings.Contains(rec.Body.String(), `"ids":`+want) {
			t.Fatalf("search %q = %s, want ids %s", q, rec.Body.String(), want)
		}
	}
}
