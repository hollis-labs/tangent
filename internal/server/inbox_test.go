package server

import (
	"context"
	"github.com/hollis-labs/tangent/internal/turns"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/interaction"
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
}
