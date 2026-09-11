package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// The draft sequence is the one revision a client has to PREDICT rather than
// echo, so the refusal has to tell it what to predict next. Before
// CW-20260910-0134 it did not, and a browser that lost its count could only
// guess — which a page reload is enough to cause.

// presentedForDraft creates and presents an interaction ready to take drafts.
func presentedForDraft(t *testing.T, store *Store, name string) InteractionRecord {
	t.Helper()
	ctx := context.Background()
	surface := createTestSurface(t, store, name)
	created, err := store.CreateInteraction(ctx,
		testInteractionParams(surface.ID, "caller-a", name+"-key", "draft"))
	if err != nil {
		t.Fatalf("CreateInteraction: %v", err)
	}
	presented, err := store.AdvanceInteraction(ctx, AdvanceInteractionParams{
		InteractionID:               created.Interaction.ID,
		ExpectedRevision:            created.Interaction.Revision,
		To:                          InteractionStatePresented,
		PresentedProjectionRevision: 9,
		ParticipantScope:            "operator:local",
		ParticipantRef:              "local-operator",
		ParticipantAuthority:        "local",
		ParticipantAssurance:        "loopback-unverified",
		ConnectionID:                "connection-1",
		Authority:                   "loopback-unverified",
	})
	if err != nil {
		t.Fatalf("AdvanceInteraction presented: %v", err)
	}
	return presented
}

// saveDraftAt writes one draft at the given sequence revision, always against
// the interaction's CURRENT revision — because a draft bumps the interaction
// too, so a fixture that pinned it would be testing the wrong conflict.
func saveDraftAt(t *testing.T, store *Store, id string, revision int64) error {
	t.Helper()
	ctx := context.Background()
	current, err := store.GetInteraction(ctx, id)
	if err != nil {
		t.Fatalf("GetInteraction: %v", err)
	}
	_, err = store.SaveDraftRevision(ctx, DraftRevision{
		InteractionID:        id,
		Revision:             revision,
		InteractionRevision:  current.Revision,
		ParticipantScope:     "operator:local",
		ParticipantRef:       "local-operator",
		ParticipantAuthority: "local",
		ParticipantAssurance: "loopback-unverified",
		DefinitionVersion:    current.Definition.Version,
		Payload:              json.RawMessage(`{"note":"checked"}`),
	})
	return err
}

// TestStaleDraftReportsTheRevisionItWanted is the fix at its source: the store
// has just computed MAX(revision)+1 to make the comparison, so carrying it out
// costs nothing and is the only place the answer exists.
func TestStaleDraftReportsTheRevisionItWanted(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	presented := presentedForDraft(t, store, "surface-draft-conflict")

	if err := saveDraftAt(t, store, presented.ID, 1); err != nil {
		t.Fatalf("first draft: %v", err)
	}

	// A client that lost its count starts over at 1 — a page reload is enough.
	err := saveDraftAt(t, store, presented.ID, 1)
	if err == nil {
		t.Fatal("a repeated draft revision was accepted")
	}

	// Every existing caller keeps working: this narrows a refusal rather than
	// introducing a new one.
	if !errors.Is(err, ErrRevisionConflict) {
		t.Errorf("err = %v, want it to wrap ErrRevisionConflict", err)
	}

	var conflict *DraftRevisionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v (%T), want a *DraftRevisionConflictError", err, err)
	}
	if conflict.Sent != 1 {
		t.Errorf("Sent = %d, want the revision the client used", conflict.Sent)
	}
	if conflict.Expected != 2 {
		t.Errorf("Expected = %d, want 2 — the revision that would have been accepted",
			conflict.Expected)
	}

	// The reported number is the one that actually works. That is what makes
	// the recovery a correction rather than another guess.
	if err := saveDraftAt(t, store, presented.ID, conflict.Expected); err != nil {
		t.Fatalf("draft at the reported revision %d: %v", conflict.Expected, err)
	}
}

// TestAReloadedClientRecoversInOneStep is the reported symptom, held directly.
//
// Five drafts land, the client forgets its count, and the number it is handed
// gets it back in ONE step rather than after five more refusals.
func TestAReloadedClientRecoversInOneStep(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	presented := presentedForDraft(t, store, "surface-draft-reload")

	for revision := int64(1); revision <= 5; revision++ {
		if err := saveDraftAt(t, store, presented.ID, revision); err != nil {
			t.Fatalf("draft %d: %v", revision, err)
		}
	}

	err := saveDraftAt(t, store, presented.ID, 1)
	var conflict *DraftRevisionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want a draft-sequence conflict", err)
	}
	if conflict.Expected != 6 {
		t.Fatalf("Expected = %d, want 6", conflict.Expected)
	}
	if err := saveDraftAt(t, store, presented.ID, conflict.Expected); err != nil {
		t.Fatalf("recovery draft: %v", err)
	}
}

// TestAnInteractionRevisionConflictIsNotADraftSequenceConflict.
//
// Both surface as ErrRevisionConflict and they are not the same problem. The
// interaction revision is one the client ECHOES from the presentation, so
// nothing needs carrying back — and reporting a draft revision for it would
// send a client to resynchronize a sequence that was never wrong.
func TestAnInteractionRevisionConflictIsNotADraftSequenceConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := openTestStore(t)
	presented := presentedForDraft(t, store, "surface-draft-interaction-rev")

	_, err := store.SaveDraftRevision(ctx, DraftRevision{
		InteractionID:        presented.ID,
		Revision:             1,
		InteractionRevision:  presented.Revision + 99,
		ParticipantScope:     "operator:local",
		ParticipantRef:       "local-operator",
		ParticipantAuthority: "local",
		ParticipantAssurance: "loopback-unverified",
		DefinitionVersion:    presented.Definition.Version,
		Payload:              json.RawMessage(`{"note":"checked"}`),
	})
	if err == nil {
		t.Fatal("a draft against a stale interaction revision was accepted")
	}
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("err = %v, want a revision conflict", err)
	}
	var conflict *DraftRevisionConflictError
	if errors.As(err, &conflict) {
		t.Errorf("err = %v, want NO draft-sequence conflict: the draft counter was correct", err)
	}
}
