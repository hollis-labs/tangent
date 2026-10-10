package interaction

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
)

func inboxReadFixture(t *testing.T, store *Store, surface, scope, key, text string) InteractionRecord {
	t.Helper()
	result, err := store.CreateInteraction(context.Background(), testInteractionParams(surface, scope, key, text))
	if err != nil {
		t.Fatal(err)
	}
	return result.Interaction
}

func inboxReadResolve(t *testing.T, store *Store, record InteractionRecord) {
	t.Helper()
	presented, err := store.AdvanceInteraction(context.Background(), AdvanceInteractionParams{
		InteractionID: record.ID, ExpectedRevision: record.Revision, To: InteractionStatePresented, PresentedProjectionRevision: 9,
		ParticipantScope: "operator:local", ParticipantRef: "local-operator", ParticipantAuthority: "local", ParticipantAssurance: "loopback-unverified", ConnectionID: "synthetic", Authority: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, draftErr := store.SaveDraftRevision(context.Background(), DraftRevision{
		InteractionID: presented.ID, Revision: 1, InteractionRevision: presented.Revision,
		ParticipantScope: presented.ParticipantScope, ParticipantRef: presented.ParticipantRef,
		ParticipantAuthority: presented.ParticipantAuthority, ParticipantAssurance: presented.ParticipantAssurance,
		DefinitionVersion: presented.Definition.Version, Payload: json.RawMessage(`{"text":"private unsubmitted draft"}`),
	}); draftErr != nil {
		t.Fatal(draftErr)
	}
	presented, err = store.GetInteraction(context.Background(), presented.ID)
	if err != nil {
		t.Fatal(err)
	}
	params := resolutionParams(presented, nil)
	params.ResponsePayload = json.RawMessage(`{"answer":"Confirmed Café response"}`)
	if _, err = store.ResolveInteraction(context.Background(), params); err != nil {
		t.Fatal(err)
	}
}

func TestInboxReadSubscriberAuthorizationAndPureHistory(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	service := newTestService(t, store)
	surface := createTestSurface(t, store, "private-inbox")
	record := inboxReadFixture(t, store, surface.ID, "standalone-local:author", "one", "Private synthetic request")
	inboxReadResolve(t, store, record)
	for _, test := range []struct {
		scope string
		want  error
	}{
		{"standalone-local:author", nil}, {"direct-loopback:author", nil}, {"operator:local", nil},
		{"standalone-local:nonsubscriber", ErrUnauthorized}, {"gateway:other:author", ErrNotFound}, {"", ErrUnauthorized},
	} {
		t.Run(test.scope, func(t *testing.T) {
			item, err := service.GetInboxItem(ctx, test.scope, record.ID)
			if !errors.Is(err, test.want) {
				t.Fatalf("get: %v, want %v", err, test.want)
			}
			if test.want == nil && (len(item.ResponseHistory) != 1 || !strings.Contains(string(item.ResponseHistory[0].Payload), "Café")) {
				t.Fatal("confirmed response lost")
			}
			if test.want != nil && item.ItemID != "" {
				t.Fatal("refusal exposed item")
			}
		})
	}
	for _, table := range []string{"terminal_outcome_retrievals", "terminal_outcome_acknowledgements"} {
		var count int
		if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("read wrote %s", table)
		}
	}
	page, err := service.ReadInbox(ctx, InboxReadInput{RequesterScope: "standalone-local:nonsubscriber"})
	if err != nil || len(page.Items) != 0 || page.NextCursor != "" {
		t.Fatalf("nonsubscriber listing: %#v %v", page, err)
	}
}

func TestInboxReadPaginationFiltersAndCursorRefusal(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	service := newTestService(t, store)
	surface := createTestSurface(t, store, "pagination")
	instant := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return instant }
	first := inboxReadFixture(t, store, surface.ID, "standalone-local:a", "first", "first")
	_ = inboxReadFixture(t, store, surface.ID, "standalone-local:other", "hidden-other", "secret foreign match")
	_ = inboxReadFixture(t, store, surface.ID, "gateway:other:a", "foreign", "secret foreign match")
	foreign := inboxReadFixture(t, store, surface.ID, "gateway:other:a", "foreign-get", "foreign body")
	if _, err := service.GetInboxItem(ctx, "standalone-local:a", foreign.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign get: %v", err)
	}
	instant = instant.Add(time.Second)
	second := inboxReadFixture(t, store, surface.ID, "standalone-local:a", "second", "second")
	instant = instant.Add(time.Second)
	third := inboxReadFixture(t, store, surface.ID, "standalone-local:a", "third", "third")
	input := InboxReadInput{RequesterScope: "standalone-local:a", Limit: 1}
	for _, want := range []string{first.ID, second.ID, third.ID} {
		page, err := service.ReadInbox(ctx, input)
		if err != nil || len(page.Items) != 1 || page.Items[0].ItemID != want {
			t.Fatalf("page: %#v %v", page, err)
		}
		if len(page.Items[0].Request) != 0 || len(page.Items[0].ResponseHistory) != 0 {
			t.Fatal("list returned content")
		}
		if want != third.ID && page.NextCursor == "" {
			t.Fatal("lost next page")
		}
		if want == third.ID && page.NextCursor != "" {
			t.Fatal("foreign rows created another page")
		}
		input.Cursor = page.NextCursor
	}
	from := second.CreatedAt
	before := third.CreatedAt
	page, err := service.ReadInbox(ctx, InboxReadInput{RequesterScope: "standalone-local:a", CreatedFrom: &from, CreatedBefore: &before, Status: second.State, Kind: second.Definition.Kind})
	if err != nil || len(page.Items) != 1 || page.Items[0].ItemID != second.ID {
		t.Fatalf("time/status/kind: %#v %v", page, err)
	}
	initial, err := service.ReadInbox(ctx, InboxReadInput{RequesterScope: "standalone-local:a", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []InboxReadInput{
		{RequesterScope: "standalone-local:other", Cursor: initial.NextCursor},
		{RequesterScope: "standalone-local:a", Kind: "changed", Cursor: initial.NextCursor},
		{RequesterScope: "standalone-local:a", Cursor: "not-base64"},
		{RequesterScope: "standalone-local:a", Cursor: strings.Repeat("x", 2049)},
		{RequesterScope: "standalone-local:a", Limit: 101},
		{RequesterScope: "standalone-local:a", Limit: -1},
		{RequesterScope: "standalone-local:a", Status: "invented"},
		{RequesterScope: "standalone-local:a", Query: strings.Repeat("x", 1025)},
		{RequesterScope: "standalone-local:a", CreatedFrom: &before, CreatedBefore: &from},
	} {
		if _, err := service.ReadInbox(ctx, bad); !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("accepted invalid input: %#v %v", bad, err)
		}
	}
	forged, _ := json.Marshal(inboxReadCursor{Version: 1, AfterItem: "foreign-does-not-exist", Filter: inboxReadFilter(InboxReadInput{RequesterScope: "standalone-local:a"})})
	if _, err := service.ReadInbox(ctx, InboxReadInput{RequesterScope: "standalone-local:a", Cursor: base64.RawURLEncoding.EncodeToString(forged)}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("forged cursor: %v", err)
	}
}

func TestInboxReadSearchAndRetentionTombstones(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	service := newTestService(t, store)
	surface := createTestSurface(t, store, "retention-read")
	record := inboxReadFixture(t, store, surface.ID, "standalone-local:a", "request", "literal 100% _ SQL quote ' and \"escaped\" text")
	inboxReadResolve(t, store, record)
	for _, query := range []string{"100% _", "sql QUOTE '", "\"escaped\"", "CAFÉ"} {
		page, searchErr := service.ReadInbox(ctx, InboxReadInput{RequesterScope: "standalone-local:a", Query: query})
		if searchErr != nil || len(page.Items) != 1 || page.Items[0].ItemID != record.ID {
			t.Fatalf("query %q: %#v %v", query, page, searchErr)
		}
	}
	for _, query := range []string{"summary", "participant_ref", "primary", "private unsubmitted draft", "' OR 1=1 --"} {
		page, searchErr := service.ReadInbox(ctx, InboxReadInput{RequesterScope: "standalone-local:a", Query: query})
		if searchErr != nil || len(page.Items) != 0 {
			t.Fatalf("noncontent query %q: %#v %v", query, page, searchErr)
		}
	}
	if _, err := tangentdb.RedactInteraction(ctx, store.db, tangentdb.RetentionRequest{InteractionID: record.ID, ActorRef: "synthetic-owner", PolicyRef: "fixture-retention"}); err != nil {
		t.Fatal(err)
	}
	item, err := service.GetInboxItem(ctx, "standalone-local:a", record.ID)
	if err != nil || !item.RequestRedacted || len(item.ResponseHistory) != 1 || !item.ResponseHistory[0].Redacted {
		t.Fatalf("tombstones lost: %#v %v", item, err)
	}
	for _, query := range []string{"100%", "Café", "fixture-retention", "synthetic-owner"} {
		page, searchErr := service.ReadInbox(ctx, InboxReadInput{RequesterScope: "standalone-local:a", Query: query})
		if searchErr != nil || len(page.Items) != 0 {
			t.Fatalf("erased/tombstone query %q: %#v %v", query, page, searchErr)
		}
	}
	page, err := service.ReadInbox(ctx, InboxReadInput{RequesterScope: "standalone-local:a"})
	if err != nil || len(page.Items) != 1 || !page.Items[0].RequestRedacted {
		t.Fatal("redaction removed metadata identity")
	}
	if hideErr := service.HideBrowserInboxItem(ctx, record.ID); hideErr != nil {
		t.Fatal(hideErr)
	}
	if _, hiddenErr := service.GetInboxItem(ctx, "standalone-local:a", record.ID); !errors.Is(hiddenErr, ErrNotFound) {
		t.Fatalf("hidden get: %v", hiddenErr)
	}
	page, err = service.ReadInbox(ctx, InboxReadInput{RequesterScope: "standalone-local:a"})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("hidden item returned")
	}
}

type inboxReadPolicy struct{ deny bool }

func (*inboxReadPolicy) Authorize(string, string) bool {
	return false
}

func TestInboxReadCursorRevalidatesHostPolicy(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	policy := &inboxReadPolicy{}
	service := newTestService(t, store)
	service.surfaces = policy
	surface := createTestSurface(t, store, "policy-read")
	record := inboxReadFixture(t, store, surface.ID, "standalone-local:a", "one", "one")
	_ = inboxReadFixture(t, store, surface.ID, "standalone-local:a", "two", "two")
	page, err := service.ReadInbox(ctx, InboxReadInput{RequesterScope: "standalone-local:a", Limit: 1})
	if err != nil || page.NextCursor == "" {
		t.Fatalf("initial cursor: %v", err)
	}
	policy.deny = true
	if _, err := service.ReadInbox(ctx, InboxReadInput{RequesterScope: "standalone-local:a", Cursor: page.NextCursor}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("revoked cursor: %v", err)
	}
	if _, err := service.GetInboxItem(ctx, "standalone-local:a", record.ID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("policy denial: %v", err)
	}
}

func (policy *inboxReadPolicy) AuthorizeInboxRead(string) bool { return !policy.deny }

func TestInboxReadPurgedCursorDoesNotRecoverLegacyContent(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	service := newTestService(t, store)
	removedSurface := createTestSurface(t, store, "purged-inbox")
	retainedSurface := createTestSurface(t, store, "retained-inbox")
	removed := inboxReadFixture(t, store, removedSurface.ID, "standalone-local:a", "removed", "erased unique phrase")
	retained := inboxReadFixture(t, store, retainedSurface.ID, "standalone-local:a", "retained", "retained unique phrase")
	page, err := service.ReadInbox(ctx, InboxReadInput{RequesterScope: "standalone-local:a", Limit: 1})
	if err != nil || page.NextCursor == "" {
		t.Fatalf("initial page: %#v %v", page, err)
	}
	if _, purgeErr := tangentdb.PurgeSurface(ctx, store.db, tangentdb.RetentionRequest{SurfaceID: removedSurface.ID, ActorRef: "synthetic-owner", PolicyRef: "fixture-purge"}); purgeErr != nil {
		t.Fatal(purgeErr)
	}
	if _, getErr := service.GetInboxItem(ctx, "standalone-local:a", removed.ID); !errors.Is(getErr, ErrNotFound) {
		t.Fatalf("purged get: %v", getErr)
	}
	if _, cursorErr := service.ReadInbox(ctx, InboxReadInput{RequesterScope: "standalone-local:a", Cursor: page.NextCursor}); !errors.Is(cursorErr, ErrInvalidRecord) {
		t.Fatalf("purged cursor: %v", cursorErr)
	}
	remaining, readErr := service.ReadInbox(ctx, InboxReadInput{RequesterScope: "standalone-local:a"})
	if readErr != nil || len(remaining.Items) != 1 || remaining.Items[0].ItemID != retained.ID {
		t.Fatalf("remaining: %#v %v", remaining, readErr)
	}
	erased, searchErr := service.ReadInbox(ctx, InboxReadInput{RequesterScope: "standalone-local:a", Query: "erased unique phrase"})
	if searchErr != nil || len(erased.Items) != 0 {
		t.Fatal("purged content recovered")
	}
}
