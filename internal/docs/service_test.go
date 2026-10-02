package docs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/interaction"
)

func newTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "docs-test.db")
	database, err := tangentdb.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err = tangentdb.RunMigrations(database); err != nil {
		t.Fatalf("db.RunMigrations: %v", err)
	}
	envSvc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if err = extensions.RegisterDocItem(envSvc); err != nil {
		t.Fatalf("RegisterDocItem: %v", err)
	}
	interactions, err := interaction.NewService(
		interaction.NewStore(database),
		interaction.NewEnvelopeDefinitionCatalog(envSvc, "docs-test-host"),
		interaction.WithSurfaceAccessPolicy(SurfaceAccessPolicy{}),
	)
	if err != nil {
		t.Fatalf("interaction.NewService: %v", err)
	}
	svc, err := NewService(interactions, NewReadStore(database))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, database
}

func docRequestJSON(idempotencyKey, title, content string, requiresAck bool) json.RawMessage {
	data, _ := json.Marshal(map[string]any{
		"contract_version": "1.0",
		"idempotency_key":  idempotencyKey,
		"source": map[string]any{
			"agent_id":    "fragments-engine",
			"agent_label": "Fragments Engine",
		},
		"title":            title,
		"summary":          "A document for review",
		"content_markdown": content,
		"requires_ack":     requiresAck,
		"tags":             []string{"report"},
	})
	return data
}

func testCaller(appID string) interaction.ActorBinding {
	return interaction.ActorBinding{
		Scope:        "standalone-local:" + appID,
		PrincipalRef: appID,
		Authority:    "standalone-local",
		Assurance:    "loopback-unverified",
	}
}

func TestEnqueue_FIFOArrivalOrder(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	const count = 5
	var handles []DocHandle
	for i := 1; i <= count; i++ {
		h, err := svc.Enqueue(ctx, EnqueueInput{
			Request: docRequestJSON(fmt.Sprintf("fe:%d", i), fmt.Sprintf("Doc %d", i), "Body", false),
			Caller:  testCaller("fragments-engine"),
		})
		if err != nil {
			t.Fatalf("Enqueue doc %d: %v", i, err)
		}
		handles = append(handles, h)
	}

	for i := 0; i < count; i++ {
		if want := int64(i + 1); handles[i].QueueSequence != want {
			t.Errorf("doc %d QueueSequence = %d, want %d", i, handles[i].QueueSequence, want)
		}
	}

	inbox, err := svc.Inbox(ctx)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if inbox.TotalPending != count {
		t.Fatalf("TotalPending = %d, want %d", inbox.TotalPending, count)
	}
	for i := 0; i < count; i++ {
		if want := int64(i + 1); inbox.Pending[i].QueueSequence != want {
			t.Errorf("inbox pending[%d] QueueSequence = %d, want %d", i, inbox.Pending[i].QueueSequence, want)
		}
		if inbox.Pending[i].ReadAt != nil {
			t.Errorf("inbox pending[%d] ReadAt = %v, want nil (unread by default)", i, inbox.Pending[i].ReadAt)
		}
	}
}

func TestEnqueue_Idempotency(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	req := docRequestJSON("fe:dup", "Duplicate delivery", "Body", false)
	h1, err := svc.Enqueue(ctx, EnqueueInput{Request: req, Caller: testCaller("fragments-engine")})
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	h2, err := svc.Enqueue(ctx, EnqueueInput{Request: req, Caller: testCaller("fragments-engine")})
	if err != nil {
		t.Fatalf("second enqueue: %v", err)
	}
	if h1.ItemID != h2.ItemID {
		t.Errorf("idempotent replay item ID mismatch: %q vs %q", h1.ItemID, h2.ItemID)
	}
}

// TestViewingNeverMarksRead is the requirement this package exists to
// enforce: InspectDoc (what every read/list path calls) must never itself
// cause a document to become read.
func TestViewingNeverMarksRead(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	h, err := svc.Enqueue(ctx, EnqueueInput{
		Request: docRequestJSON("fe:view", "Read me", "Body", false),
		Caller:  testCaller("fragments-engine"),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	for i := 0; i < 3; i++ {
		peek, peekErr := svc.InspectDoc(ctx, h.ItemID)
		if peekErr != nil {
			t.Fatalf("InspectDoc (view %d): %v", i, peekErr)
		}
		if peek.ReadAt != nil {
			t.Fatalf("view %d: ReadAt = %v, want nil — viewing must not mark read", i, peek.ReadAt)
		}
	}

	inbox, err := svc.Inbox(ctx)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if inbox.Pending[0].ReadAt != nil {
		t.Errorf("Inbox listing marked the doc read; it must stay unread until MarkRead is called")
	}

	view, err := svc.MarkRead(ctx, h.ItemID)
	if err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if view.ReadAt == nil {
		t.Fatal("after MarkRead, ReadAt is still nil")
	}

	// Idempotent: marking an already-read doc read again keeps the original timestamp semantics working.
	view2, err := svc.MarkRead(ctx, h.ItemID)
	if err != nil {
		t.Fatalf("second MarkRead: %v", err)
	}
	if view2.ReadAt == nil {
		t.Fatal("after second MarkRead, ReadAt is nil")
	}
}

func TestAcknowledge_ResolvesAndMovesToHistory(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	h, err := svc.Enqueue(ctx, EnqueueInput{
		Request: docRequestJSON("fe:ack", "Needs a nod", "Please confirm", true),
		Caller:  testCaller("fragments-engine"),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	item, err := svc.Acknowledge(ctx, AcknowledgeInput{ItemID: h.ItemID, ExpectedRevision: h.Revision, Note: "Looks good"})
	if err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if item.State != interaction.InteractionStateResolved {
		t.Errorf("State = %q, want resolved", item.State)
	}
	if item.Resolution == nil || item.Resolution.Action != "acknowledged" {
		t.Fatalf("Resolution = %+v, want action=acknowledged", item.Resolution)
	}

	inbox, err := svc.Inbox(ctx)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if inbox.TotalPending != 0 || inbox.TotalTerminal != 1 {
		t.Errorf("TotalPending=%d TotalTerminal=%d, want 0/1", inbox.TotalPending, inbox.TotalTerminal)
	}

	// An acknowledged (terminal) doc cannot be archived further.
	if _, err := svc.Archive(ctx, ArchiveInput{ItemID: h.ItemID, ExpectedRevision: item.Revision}); err == nil {
		t.Error("Archive after Acknowledge succeeded, want ErrTerminalConflict")
	}
}

func TestArchive_HidesFromPendingWithoutRequiringAck(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	h, err := svc.Enqueue(ctx, EnqueueInput{
		Request: docRequestJSON("fe:archive", "Never needs a decision", "FYI only", false),
		Caller:  testCaller("fragments-engine"),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	item, err := svc.Archive(ctx, ArchiveInput{ItemID: h.ItemID, ExpectedRevision: h.Revision, Reason: "done reading"})
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if item.State != interaction.InteractionStateCanceled {
		t.Errorf("State = %q, want canceled", item.State)
	}

	inbox, err := svc.Inbox(ctx)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if inbox.TotalPending != 0 {
		t.Errorf("TotalPending = %d, want 0", inbox.TotalPending)
	}
	if inbox.TotalTerminal != 1 {
		t.Errorf("TotalTerminal = %d, want 1", inbox.TotalTerminal)
	}
}

func TestDeleteHidesPendingAndAcknowledgedDocsWithoutLosingEvidence(t *testing.T) {
	for _, acknowledged := range []bool{false, true} {
		t.Run(fmt.Sprint(acknowledged), func(t *testing.T) {
			svc, _ := newTestService(t)
			ctx := context.Background()
			handle, err := svc.Enqueue(ctx, EnqueueInput{Request: docRequestJSON("delete-test", "Delete test", "Original document", true), Caller: testCaller("test")})
			if err != nil {
				t.Fatal(err)
			}
			current, err := svc.InspectDoc(ctx, handle.ItemID)
			if err != nil {
				t.Fatal(err)
			}
			if acknowledged {
				current, err = svc.Acknowledge(ctx, AcknowledgeInput{ItemID: handle.ItemID, ExpectedRevision: current.Revision, Note: "Keep this response"})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err = svc.Delete(ctx, ArchiveInput{ItemID: handle.ItemID, ExpectedRevision: current.Revision + 1}); !errors.Is(err, ErrStaleRevision) {
				t.Fatalf("stale delete = %v", err)
			}
			deleted, err := svc.Delete(ctx, ArchiveInput{ItemID: handle.ItemID, ExpectedRevision: current.Revision})
			if err != nil {
				t.Fatal(err)
			}
			inbox, err := svc.Inbox(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if inbox.TotalPending != 0 || inbox.TotalTerminal != 0 {
				t.Fatal("deleted document still listed")
			}
			entries, err := svc.interactions.BrowserInbox(ctx)
			if err != nil || len(entries) != 0 {
				t.Fatalf("unified inbox = %v, %v", entries, err)
			}
			evidence, err := svc.InspectDoc(ctx, handle.ItemID)
			if err != nil || evidence.ContentMarkdown != "Original document" {
				t.Fatalf("lost evidence: %v", err)
			}
			if acknowledged && (evidence.Resolution == nil || evidence.Resolution.Note != "Keep this response") {
				t.Fatal("lost confirmed response")
			}
			if !acknowledged && evidence.State != interaction.InteractionStateCanceled {
				t.Fatal("deleted active doc was not closed")
			}
			if _, err = svc.Delete(ctx, ArchiveInput{ItemID: handle.ItemID, ExpectedRevision: deleted.Revision}); err != nil {
				t.Fatalf("retry deletion: %v", err)
			}
		})
	}
}
