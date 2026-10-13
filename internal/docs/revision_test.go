package docs

import (
	"context"
	"testing"
)

// TestRevisionMatchesInbox pins that the cheap Revision (used by the event
// streams on every poll tick) is byte-for-byte the digest Inbox computes, across
// every kind of change that must wake a subscriber: empty surface, new doc,
// read receipt, acknowledgement, archive and delete (hidden).
func TestRevisionMatchesInbox(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	assertSame := func(step string) string {
		t.Helper()
		inbox, err := svc.Inbox(ctx)
		if err != nil {
			t.Fatalf("%s: Inbox: %v", step, err)
		}
		rev, err := svc.Revision(ctx)
		if err != nil {
			t.Fatalf("%s: Revision: %v", step, err)
		}
		if rev != inbox.Revision {
			t.Fatalf("%s: Revision = %q, Inbox.Revision = %q", step, rev, inbox.Revision)
		}
		return rev
	}

	prev := assertSame("empty")

	changed := func(step string) {
		t.Helper()
		next := assertSame(step)
		if next == prev {
			t.Fatalf("%s: revision did not change", step)
		}
		prev = next
	}

	a, err := svc.Enqueue(ctx, EnqueueInput{Request: docRequestJSON("rev:a", "A", "Body A", true), Caller: testCaller("fragments-engine")})
	if err != nil {
		t.Fatalf("Enqueue a: %v", err)
	}
	changed("after first doc")

	b, err := svc.Enqueue(ctx, EnqueueInput{Request: docRequestJSON("rev:b", "B", "Body B", false), Caller: testCaller("fragments-engine")})
	if err != nil {
		t.Fatalf("Enqueue b: %v", err)
	}
	changed("after second doc")

	if _, err = svc.MarkRead(ctx, b.ItemID); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	changed("after read receipt")

	if _, err = svc.Acknowledge(ctx, AcknowledgeInput{ItemID: a.ItemID, ExpectedRevision: a.Revision, Note: "ok"}); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	changed("after acknowledge")

	item, err := svc.InspectDoc(ctx, b.ItemID)
	if err != nil {
		t.Fatalf("InspectDoc: %v", err)
	}
	if _, err = svc.Archive(ctx, ArchiveInput{ItemID: b.ItemID, ExpectedRevision: item.Revision, Reason: "done"}); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	changed("after archive")

	archived, err := svc.InspectDoc(ctx, a.ItemID)
	if err != nil {
		t.Fatalf("InspectDoc a: %v", err)
	}
	if _, err = svc.Delete(ctx, ArchiveInput{ItemID: a.ItemID, ExpectedRevision: archived.Revision}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	changed("after delete (hidden)")

	// No change -> same revision on repeated polls.
	again := assertSame("steady state")
	if again != prev {
		t.Fatalf("steady state revision moved: %q vs %q", again, prev)
	}
}
