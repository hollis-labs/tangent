package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

// presentAs creates an interaction and presents it under the given
// participant ref and assurance.
func presentAs(t *testing.T, store *Store, key, ref, assurance string) InteractionRecord {
	t.Helper()
	ctx := context.Background()
	surface := createTestSurface(t, store, "surface-"+key)
	created, err := store.CreateInteraction(ctx, testInteractionParams(surface.ID, "caller-a", key, key))
	if err != nil {
		t.Fatalf("CreateInteraction: %v", err)
	}
	presented, err := store.AdvanceInteraction(ctx, AdvanceInteractionParams{
		InteractionID: created.Interaction.ID, ExpectedRevision: created.Interaction.Revision,
		To: InteractionStatePresented, PresentedProjectionRevision: 1,
		ParticipantScope: "operator:local", ParticipantRef: ref,
		ParticipantAuthority: "tangent-loopback", ParticipantAssurance: assurance,
		ConnectionID: "connection-1", Authority: "tangent-loopback",
	})
	if err != nil {
		t.Fatalf("AdvanceInteraction presented: %v", err)
	}
	return presented
}

func resolveAs(record InteractionRecord, ref, assurance string) ResolveInteractionParams {
	params := resolutionParams(record, nil)
	params.ParticipantRef = ref
	params.ParticipantAuthority = "tangent-loopback"
	params.ParticipantAssurance = assurance
	return params
}

func countEvents(t *testing.T, store *Store, interactionID, eventType string) (int, string) {
	t.Helper()
	var (
		count    int
		metadata string
	)
	if err := store.db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(metadata), '') FROM interaction_events
WHERE interaction_id = ? AND event_type = ?`, interactionID, eventType).Scan(&count, &metadata); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return count, metadata
}

func TestOperatorTokenSupersedesLoopbackUnverifiedPresenter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := openTestStore(t)
	for _, presenterAssurance := range []string{"loopback-unverified", "asserted"} {
		presented := presentAs(t, store, "takeover-"+presenterAssurance, "local-operator", presenterAssurance)
		result, err := store.ResolveInteraction(ctx, resolveAs(presented, "local-operator", "operator-token"))
		if err != nil {
			t.Fatalf("operator-token over %s: %v", presenterAssurance, err)
		}
		if result.Resolution.ParticipantAssurance != "operator-token" ||
			result.Interaction.ParticipantAssurance != "operator-token" ||
			result.Interaction.State != InteractionStateResolved {
			t.Fatalf("takeover recorded resolution %q interaction %q state %q",
				result.Resolution.ParticipantAssurance, result.Interaction.ParticipantAssurance, result.Interaction.State)
		}
		count, metadata := countEvents(t, store, presented.ID, EventPresenterSuperseded)
		if count != 1 {
			t.Fatalf("presenter_superseded events = %d, want 1", count)
		}
		var journal map[string]string
		if err := json.Unmarshal([]byte(metadata), &journal); err != nil {
			t.Fatalf("decode journal: %v", err)
		}
		want := map[string]string{
			"from_participant_ref": "local-operator", "from_participant_authority": "tangent-loopback",
			"from_participant_assurance": presenterAssurance,
			"to_participant_ref":         "local-operator", "to_participant_authority": "tangent-loopback",
			"to_participant_assurance": "operator-token",
		}
		if fmt.Sprint(journal) != fmt.Sprint(want) {
			t.Fatalf("journal = %v, want %v", journal, want)
		}
		// Terminal-safe and idempotent: a second attempt finds a terminal
		// item and changes nothing.
		if _, err := store.ResolveInteraction(ctx, resolveAs(presented, "local-operator", "operator-token")); !errors.Is(err, ErrTerminal) {
			t.Fatalf("second resolve = %v, want ErrTerminal", err)
		}
		if count, _ := countEvents(t, store, presented.ID, EventPresenterSuperseded); count != 1 {
			t.Fatalf("a refused retry journaled another takeover: %d", count)
		}
	}
}

func TestLowerOrEqualAssuranceCannotSupersedeAPresenter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := openTestStore(t)

	cases := []struct {
		name                         string
		presenterRef, presenterLevel string
		resolverRef, resolverLevel   string
	}{
		{"unverified over operator", "local-operator", "operator-token", "local-operator", "loopback-unverified"},
		{"equal assurance, other identity", "local-operator", "operator-token", "other-operator", "operator-token"},
		{"equal unverified, other identity", "local-operator", "loopback-unverified", "other-operator", "loopback-unverified"},
		{"unknown presenter assurance", "local-operator", "in-process", "local-operator", "operator-token"},
	}
	for i, tc := range cases {
		presented := presentAs(t, store, fmt.Sprintf("refused-%d", i), tc.presenterRef, tc.presenterLevel)
		if _, err := store.ResolveInteraction(ctx, resolveAs(presented, tc.resolverRef, tc.resolverLevel)); !errors.Is(err, ErrNotRespondable) {
			t.Fatalf("%s: store resolve = %v, want ErrNotRespondable", tc.name, err)
		}
		if CanSupersedePresenter(presented, ActorBinding{
			Scope: "operator:local", PrincipalRef: tc.resolverRef, Authority: "tangent-loopback", Assurance: tc.resolverLevel,
		}) {
			t.Fatalf("%s: CanSupersedePresenter = true", tc.name)
		}
		after, err := store.GetInteraction(ctx, presented.ID)
		if err != nil {
			t.Fatal(err)
		}
		if after.State != InteractionStatePresented || after.Revision != presented.Revision ||
			after.ParticipantAssurance != tc.presenterLevel {
			t.Fatalf("%s: refused takeover mutated the item: %#v", tc.name, after)
		}
		if count, _ := countEvents(t, store, presented.ID, EventPresenterSuperseded); count != 0 {
			t.Fatalf("%s: refused takeover was journaled", tc.name)
		}
	}

	// A different participant scope never takes over, whatever its assurance.
	presented := presentAs(t, store, "other-scope", "local-operator", "loopback-unverified")
	foreign := resolveAs(presented, "local-operator", "operator-token")
	foreign.ParticipantScope = "gateway:elsewhere"
	if _, err := store.ResolveInteraction(ctx, foreign); !errors.Is(err, ErrNotRespondable) {
		t.Fatalf("cross-scope takeover = %v, want ErrNotRespondable", err)
	}
}

func TestSameIdentityResolutionIsUnchangedByTakeover(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := openTestStore(t)
	for _, level := range []string{"loopback-unverified", "operator-token"} {
		presented := presentAs(t, store, "same-"+level, "local-operator", level)
		result, err := store.ResolveInteraction(ctx, resolveAs(presented, "local-operator", level))
		if err != nil {
			t.Fatalf("same-identity resolve at %s: %v", level, err)
		}
		if result.Resolution.ParticipantAssurance != level {
			t.Fatalf("same-identity resolution assurance = %q, want %q", result.Resolution.ParticipantAssurance, level)
		}
		if count, _ := countEvents(t, store, presented.ID, EventPresenterSuperseded); count != 0 {
			t.Fatal("a same-identity resolution journaled a takeover")
		}
	}
}
