package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/docs"
)

// inboxOnlyDocs implements DocsService without the optional Revision method, so
// the handler must fall back to Inbox for change detection.
type inboxOnlyDocs struct {
	inboxCalls atomic.Int64
	revision   atomic.Value // string
}

func (f *inboxOnlyDocs) Inbox(context.Context) (docs.DocsInbox, error) {
	f.inboxCalls.Add(1)
	rev, _ := f.revision.Load().(string)
	return docs.DocsInbox{Revision: rev}, nil
}
func (f *inboxOnlyDocs) InspectDoc(context.Context, string) (docs.DocItemView, error) {
	return docs.DocItemView{}, nil
}
func (f *inboxOnlyDocs) MarkRead(context.Context, string) (docs.DocItemView, error) {
	return docs.DocItemView{}, nil
}
func (f *inboxOnlyDocs) Acknowledge(context.Context, docs.AcknowledgeInput) (docs.DocItemView, error) {
	return docs.DocItemView{}, nil
}
func (f *inboxOnlyDocs) Archive(context.Context, docs.ArchiveInput) (docs.DocItemView, error) {
	return docs.DocItemView{}, nil
}
func (f *inboxOnlyDocs) Delete(context.Context, docs.ArchiveInput) (docs.DocItemView, error) {
	return docs.DocItemView{}, nil
}

// revisionDocs adds the cheap Revision method; Inbox must never be needed.
type revisionDocs struct {
	inboxOnlyDocs
	revisionCalls atomic.Int64
}

func (f *revisionDocs) Revision(context.Context) (string, error) {
	f.revisionCalls.Add(1)
	rev, _ := f.revision.Load().(string)
	return rev, nil
}

func runDocsEvents(t *testing.T, svc DocsService, flip func()) string {
	t.Helper()
	handler := newDocsHTTPHandler(svc)
	handler.eventPoll = time.Millisecond
	handler.eventLifetime = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/docs/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { handler.events(rec, req); close(done) }()
	time.Sleep(60 * time.Millisecond)
	flip()
	time.Sleep(60 * time.Millisecond)
	cancel()
	<-done
	return rec.Body.String()
}

func TestDocsEventsUseCheapRevisionWhenAvailable(t *testing.T) {
	svc := &revisionDocs{}
	svc.revision.Store("r1")
	body := runDocsEvents(t, svc, func() { svc.revision.Store("r2") })
	if got := svc.inboxCalls.Load(); got != 0 {
		t.Fatalf("Inbox was called %d times; the event stream must only use Revision", got)
	}
	if svc.revisionCalls.Load() < 3 {
		t.Fatalf("Revision calls = %d, want it polled", svc.revisionCalls.Load())
	}
	if !strings.Contains(body, `"revision":"r1"`) || !strings.Contains(body, `"revision":"r2"`) {
		t.Fatalf("expected revision events r1 then r2, got %q", body)
	}
	if strings.Count(body, "event: revision") != 2 {
		t.Fatalf("expected exactly two revision events (initial + one change), got %q", body)
	}
}

func TestDocsEventsFallBackToInboxWithoutRevision(t *testing.T) {
	svc := &inboxOnlyDocs{}
	svc.revision.Store("a")
	body := runDocsEvents(t, svc, func() { svc.revision.Store("b") })
	if svc.inboxCalls.Load() < 3 {
		t.Fatalf("Inbox calls = %d, want fallback polling", svc.inboxCalls.Load())
	}
	if strings.Count(body, "event: revision") != 2 {
		t.Fatalf("expected two revision events, got %q", body)
	}
}
