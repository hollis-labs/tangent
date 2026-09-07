package channel

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hollis-labs/tangent/internal/authz"
	tangentdb "github.com/hollis-labs/tangent/internal/db"
)

func openTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "channel.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrateErr := tangentdb.RunMigrations(database); migrateErr != nil {
		t.Fatalf("run migrations: %v", migrateErr)
	}
	store, err := NewStore(database)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, database
}

func TestCreateChannelRoundTrips(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()

	created, err := store.CreateChannel(ctx, CreateChannelParams{
		Title:      "tangent-14 and Chrispian",
		OwnerScope: "standalone-local:anonymous",
		ProjectRef: "PRJ-20260825-0002",
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if created.ID == "" {
		t.Fatal("CreateChannel returned an empty id")
	}

	loaded, err := store.GetChannel(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if loaded.ID != created.ID || loaded.Title != created.Title || loaded.OwnerScope != created.OwnerScope ||
		loaded.ProjectRef != created.ProjectRef || string(loaded.Metadata) != string(created.Metadata) ||
		!loaded.CreatedAt.Equal(created.CreatedAt) || !loaded.UpdatedAt.Equal(created.UpdatedAt) ||
		loaded.ArchivedAt != nil {
		t.Fatalf("GetChannel = %+v, want %+v", loaded, created)
	}
}

func TestOpenChannelAddsTheCanonicalOperatorAsAMember(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()

	ch, operator, err := store.OpenChannel(ctx, CreateChannelParams{OwnerScope: "standalone-local:anonymous"})
	if err != nil {
		t.Fatalf("OpenChannel: %v", err)
	}
	if operator.Kind != ParticipantOperator || operator.ExternalAuthority != OperatorExternalAuthority ||
		operator.ExternalRef != OperatorExternalRef {
		t.Fatalf("operator = %+v, want the canonical operator identity", operator)
	}
	members, err := store.ListChannelParticipants(ctx, ch.ID)
	if err != nil {
		t.Fatalf("ListChannelParticipants: %v", err)
	}
	if len(members) != 1 || members[0].ID != operator.ID {
		t.Fatalf("members of a freshly opened channel = %+v, want exactly the operator", members)
	}

	// The operator is the same stable participant across channels — opening
	// a second channel does not mint a second operator identity.
	secondChannel, secondOperator, err := store.OpenChannel(ctx, CreateChannelParams{OwnerScope: "standalone-local:anonymous"})
	if err != nil {
		t.Fatalf("OpenChannel (second): %v", err)
	}
	if secondOperator.ID != operator.ID {
		t.Fatalf("second channel's operator = %s, want the same operator %s", secondOperator.ID, operator.ID)
	}
	operatorChannels, err := store.ListParticipantChannels(ctx, operator.ID)
	if err != nil {
		t.Fatalf("ListParticipantChannels: %v", err)
	}
	if len(operatorChannels) != 2 {
		t.Fatalf("operator's channels = %+v, want both %s and %s", operatorChannels, ch.ID, secondChannel.ID)
	}
}

func TestChannelSupportsMultiplePendingSubjects(t *testing.T) {
	t.Parallel()
	store, database := openTestStore(t)
	ctx := context.Background()

	ch, err := store.CreateChannel(ctx, CreateChannelParams{OwnerScope: "standalone-local:anonymous"})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	surfaceID, interactionID := seedSurfaceAndInteraction(t, database, "surface-a", "interaction-a")

	first, err := store.AddSubject(ctx, CreateSubjectParams{
		ChannelID:     ch.ID,
		Type:          SubjectInteraction,
		InteractionID: interactionID,
		Title:         "first pending subject",
	})
	if err != nil {
		t.Fatalf("AddSubject (interaction): %v", err)
	}
	second, err := store.AddSubject(ctx, CreateSubjectParams{
		ChannelID: ch.ID,
		Type:      SubjectSurface,
		SurfaceID: surfaceID,
		Title:     "second pending subject",
	})
	if err != nil {
		t.Fatalf("AddSubject (surface): %v", err)
	}

	subjects, err := store.ListSubjects(ctx, ch.ID)
	if err != nil {
		t.Fatalf("ListSubjects: %v", err)
	}
	if len(subjects) != 2 {
		t.Fatalf("ListSubjects returned %d subjects, want 2 (both pending at once)", len(subjects))
	}
	for _, subject := range subjects {
		if subject.Status != SubjectPending {
			t.Fatalf("subject %s status = %q, want pending", subject.ID, subject.Status)
		}
	}

	// Resolving one leaves the other pending: there is no single-pending
	// invariant tying them together.
	resolved, err := store.ResolveSubject(ctx, first.ID)
	if err != nil {
		t.Fatalf("ResolveSubject: %v", err)
	}
	if resolved.Status != SubjectResolved {
		t.Fatalf("resolved subject status = %q, want resolved", resolved.Status)
	}
	still, err := store.GetSubject(ctx, second.ID)
	if err != nil {
		t.Fatalf("GetSubject: %v", err)
	}
	if still.Status != SubjectPending {
		t.Fatalf("second subject status = %q, want still pending after resolving the first", still.Status)
	}
}

func TestAddSubjectEnforcesCanonicalAssociationShape(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()

	ch, err := store.CreateChannel(ctx, CreateChannelParams{OwnerScope: "standalone-local:anonymous"})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	_, err = store.AddSubject(ctx, CreateSubjectParams{ChannelID: ch.ID, Type: SubjectInteraction})
	if err == nil {
		t.Fatal("AddSubject with type interaction and no interaction id should fail")
	}
	_, err = store.AddSubject(ctx, CreateSubjectParams{ChannelID: ch.ID, Type: SubjectSurface})
	if err == nil {
		t.Fatal("AddSubject with type surface and no surface id should fail")
	}

	freeform, err := store.AddSubject(ctx, CreateSubjectParams{ChannelID: ch.ID, Type: SubjectFreeform, Title: "no canonical backing"})
	if err != nil {
		t.Fatalf("AddSubject (freeform): %v", err)
	}
	if freeform.InteractionID != "" || freeform.SurfaceID != "" {
		t.Fatalf("freeform subject carries a canonical association: %+v", freeform)
	}
}

func TestSubjectSurvivesPurgeOfItsReferentAsATombstone(t *testing.T) {
	t.Parallel()
	store, database := openTestStore(t)
	ctx := context.Background()

	ch, err := store.CreateChannel(ctx, CreateChannelParams{OwnerScope: "standalone-local:anonymous"})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	surfaceID, interactionID := seedSurfaceAndInteraction(t, database, "surface-tombstone", "interaction-tombstone")
	subject, err := store.AddSubject(ctx, CreateSubjectParams{
		ChannelID: ch.ID, Type: SubjectInteraction, InteractionID: interactionID, Title: "will be purged",
	})
	if err != nil {
		t.Fatalf("AddSubject: %v", err)
	}
	if subject.ReferentPurged() {
		t.Fatal("a freshly created subject reports its referent as already purged")
	}

	// internal/db.PurgeSurface cascades a surface delete through interactions
	// (ON DELETE CASCADE) and lands on this subject's FK (ON DELETE SET
	// NULL). Deleting the surface directly exercises the same FK chain
	// without importing internal/db, which would be a layering inversion for
	// this leaf package.
	_, err = database.ExecContext(ctx, `DELETE FROM surfaces WHERE id = ?`, surfaceID)
	if err != nil {
		t.Fatalf("delete surface (simulating a purge): %v", err)
	}

	purged, err := store.GetSubject(ctx, subject.ID)
	if err != nil {
		t.Fatalf("GetSubject after purge: %v", err)
	}
	if !purged.ReferentPurged() {
		t.Fatal("subject does not report its referent as purged after the interaction was cascaded away")
	}
	if purged.InteractionID != "" {
		t.Fatalf("purged subject InteractionID = %q, want empty", purged.InteractionID)
	}
	if purged.Type != SubjectInteraction || purged.Title != "will be purged" {
		t.Fatalf("purge changed the subject's own identity: %+v", purged)
	}
}

func TestWorkerProvenanceIsOpaqueAndNeverBecomesAParticipant(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()

	ch, err := store.CreateChannel(ctx, CreateChannelParams{OwnerScope: "standalone-local:anonymous"})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	subject, err := store.AddSubject(ctx, CreateSubjectParams{
		ChannelID:        ch.ID,
		Type:             SubjectFreeform,
		WorkerProvenance: []byte(`[{"application_id":"worker-1","role":"implementer"}]`),
	})
	if err != nil {
		t.Fatalf("AddSubject: %v", err)
	}
	if string(subject.WorkerProvenance) != `[{"application_id":"worker-1","role":"implementer"}]` {
		t.Fatalf("worker provenance = %s, want the exact opaque payload back", subject.WorkerProvenance)
	}

	participants, err := store.ListChannelParticipants(ctx, ch.ID)
	if err != nil {
		t.Fatalf("ListChannelParticipants: %v", err)
	}
	if len(participants) != 0 {
		t.Fatalf("worker provenance produced %d channel participants, want 0 (nonauthoritative)", len(participants))
	}
}

func TestUpsertParticipantResolvesTheSameExternalIdentity(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()

	params := UpsertParticipantParams{
		Kind:              ParticipantAgent,
		ExternalAuthority: "claude-code",
		ExternalRef:       "tangent-14",
		Label:             "tangent-14",
	}
	first, err := store.UpsertParticipant(ctx, params)
	if err != nil {
		t.Fatalf("UpsertParticipant (first): %v", err)
	}
	params.Label = "tangent-14 (relabelled by a later launch)"
	second, err := store.UpsertParticipant(ctx, params)
	if err != nil {
		t.Fatalf("UpsertParticipant (second): %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("UpsertParticipant minted a second identity for the same external ref: %s != %s", first.ID, second.ID)
	}
}

// TestConcurrentUpsertParticipantSameIdentityResolvesToOneRow is the
// regression for a director review finding on CW-20260906-0064 (PR #32):
// UpsertParticipant's original lookup-then-insert had a gap in which two
// concurrent registrations of the same asserted identity — the 0071/0072
// scenario of two peers attaching at once — could both observe "not found"
// and both attempt to insert, so every loser errored on the unique
// constraint instead of resolving to the winner. The fix folds the lookup
// and the insert into one INSERT ... ON CONFLICT DO NOTHING statement.
func TestConcurrentUpsertParticipantSameIdentityResolvesToOneRow(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()

	const n = 8
	var wg sync.WaitGroup
	ids := make([]string, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			p, err := store.UpsertParticipant(ctx, UpsertParticipantParams{
				Kind:              ParticipantAgent,
				ExternalAuthority: "claude-code",
				ExternalRef:       "session-abc",
				Label:             "peer",
			})
			ids[i], errs[i] = p.ID, err
		}(i)
	}
	close(start)
	wg.Wait()

	distinct := map[string]bool{}
	for i := range n {
		if errs[i] != nil {
			t.Errorf("goroutine %d: UpsertParticipant returned an error instead of resolving to the existing participant: %v", i, errs[i])
			continue
		}
		distinct[ids[i]] = true
	}
	if len(distinct) > 1 {
		t.Fatalf("same asserted identity resolved to %d distinct participants, want exactly 1", len(distinct))
	}
}

func TestOneParticipantSpansMultipleChannels(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()

	participant, err := store.UpsertParticipant(ctx, UpsertParticipantParams{
		Kind: ParticipantAgent, ExternalAuthority: "claude-code", ExternalRef: "tangent-84",
	})
	if err != nil {
		t.Fatalf("UpsertParticipant: %v", err)
	}
	channelA, err := store.CreateChannel(ctx, CreateChannelParams{Title: "project A", OwnerScope: "standalone-local:anonymous"})
	if err != nil {
		t.Fatalf("CreateChannel A: %v", err)
	}
	channelB, err := store.CreateChannel(ctx, CreateChannelParams{Title: "project B", OwnerScope: "standalone-local:anonymous"})
	if err != nil {
		t.Fatalf("CreateChannel B: %v", err)
	}
	_, err = store.AddParticipant(ctx, channelA.ID, participant.ID)
	if err != nil {
		t.Fatalf("AddParticipant A: %v", err)
	}
	_, err = store.AddParticipant(ctx, channelB.ID, participant.ID)
	if err != nil {
		t.Fatalf("AddParticipant B: %v", err)
	}
	// Idempotent: adding the same binding again does not error or duplicate.
	_, err = store.AddParticipant(ctx, channelA.ID, participant.ID)
	if err != nil {
		t.Fatalf("AddParticipant A (again): %v", err)
	}

	channels, err := store.ListParticipantChannels(ctx, participant.ID)
	if err != nil {
		t.Fatalf("ListParticipantChannels: %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("ListParticipantChannels returned %d channels, want 2", len(channels))
	}

	membersOfA, err := store.ListChannelParticipants(ctx, channelA.ID)
	if err != nil {
		t.Fatalf("ListChannelParticipants: %v", err)
	}
	if len(membersOfA) != 1 || membersOfA[0].ID != participant.ID {
		t.Fatalf("ListChannelParticipants(A) = %+v, want exactly the one participant", membersOfA)
	}

	err = store.RemoveParticipant(ctx, channelA.ID, participant.ID)
	if err != nil {
		t.Fatalf("RemoveParticipant: %v", err)
	}
	after, err := store.ListParticipantChannels(ctx, participant.ID)
	if err != nil {
		t.Fatalf("ListParticipantChannels after remove: %v", err)
	}
	if len(after) != 1 || after[0].ID != channelB.ID {
		t.Fatalf("ListParticipantChannels after leaving A = %+v, want only B", after)
	}
}

// TestChannelPartitionsAreAdvisoryNotIsolationBoundaries pins acceptance
// item 3's least-tested clause: "advisory partitions remain advisory" (ADR
// 0006 §3; internal/authz.AdvisoryPartitionNotice makes the identical claim
// about caller partitions — "any local caller can assert any partition, and
// isolation is enforced only across authorities"). The property holds today
// only because nothing in this package filters a channel or membership query
// by owner_scope or project_ref; that is a claim that holds by absence, and
// this repo's own rule is that a claim like that needs a test that goes red
// the day someone adds such a filter, not prose that goes stale silently.
func TestChannelPartitionsAreAdvisoryNotIsolationBoundaries(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()
	t.Log(authz.AdvisoryPartitionNotice)

	participant, err := store.UpsertParticipant(ctx, UpsertParticipantParams{
		Kind: ParticipantAgent, ExternalAuthority: "claude-code", ExternalRef: "tangent-14",
	})
	if err != nil {
		t.Fatalf("UpsertParticipant: %v", err)
	}
	channelA, err := store.CreateChannel(ctx, CreateChannelParams{
		Title: "tangent", OwnerScope: "standalone-local:project-a", ProjectRef: "PRJ-a",
	})
	if err != nil {
		t.Fatalf("CreateChannel A: %v", err)
	}
	channelB, err := store.CreateChannel(ctx, CreateChannelParams{
		Title: "hadron", OwnerScope: "standalone-local:project-b", ProjectRef: "PRJ-b",
	})
	if err != nil {
		t.Fatalf("CreateChannel B: %v", err)
	}
	if channelA.OwnerScope == channelB.OwnerScope || channelA.ProjectRef == channelB.ProjectRef {
		t.Fatal("test setup is broken: the two channels must differ in both owner_scope and project_ref")
	}

	_, err = store.AddParticipant(ctx, channelA.ID, participant.ID)
	if err != nil {
		t.Fatalf("AddParticipant A: %v", err)
	}
	_, err = store.AddParticipant(ctx, channelB.ID, participant.ID)
	if err != nil {
		t.Fatalf("AddParticipant B: %v", err)
	}

	// One participant reference, bound to two channels under different
	// owner_scope and project_ref: nothing here refuses the second bind or
	// narrows the read because the partitions differ.
	channels, err := store.ListParticipantChannels(ctx, participant.ID)
	if err != nil {
		t.Fatalf("ListParticipantChannels across two owner_scope/project_ref partitions: %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("ListParticipantChannels returned %d channels across two partitions, want 2 — a partition became an isolation boundary", len(channels))
	}
	seen := map[string]bool{}
	for _, ch := range channels {
		seen[ch.ID] = true
	}
	if !seen[channelA.ID] || !seen[channelB.ID] {
		t.Fatalf("ListParticipantChannels = %+v, want both channelA (owner_scope %q) and channelB (owner_scope %q)",
			channels, channelA.OwnerScope, channelB.OwnerScope)
	}

	// Same property, other direction: reading channel A's members is not
	// narrowed by channel B's differing partition existing at all.
	membersOfA, err := store.ListChannelParticipants(ctx, channelA.ID)
	if err != nil {
		t.Fatalf("ListChannelParticipants: %v", err)
	}
	if len(membersOfA) != 1 || membersOfA[0].ID != participant.ID {
		t.Fatalf("ListChannelParticipants(A) = %+v, want exactly the one participant despite the partition difference", membersOfA)
	}
}

func TestRebindAdvancesGenerationExplicitlyAndSupersedesThePrevious(t *testing.T) {
	t.Parallel()
	store, database := openTestStore(t)
	ctx := context.Background()

	participant, err := store.UpsertParticipant(ctx, UpsertParticipantParams{
		Kind: ParticipantAgent, ExternalAuthority: "claude-code", ExternalRef: "tangent-14",
	})
	if err != nil {
		t.Fatalf("UpsertParticipant: %v", err)
	}
	ch, err := store.CreateChannel(ctx, CreateChannelParams{OwnerScope: "standalone-local:anonymous"})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	_, err = store.AddParticipant(ctx, ch.ID, participant.ID)
	if err != nil {
		t.Fatalf("AddParticipant: %v", err)
	}

	first, err := store.Rebind(ctx, ch.ID, participant.ID, RebindParams{
		RuntimeAuthority: "claude-code-cli", RuntimeEndpointRef: "session-alpha",
	})
	if err != nil {
		t.Fatalf("Rebind (first): %v", err)
	}
	if first.Generation != 1 || !first.Current() {
		t.Fatalf("first binding = %+v, want generation 1 and current", first)
	}

	second, err := store.Rebind(ctx, ch.ID, participant.ID, RebindParams{
		RuntimeAuthority: "claude-code-cli", RuntimeEndpointRef: "session-beta",
	})
	if err != nil {
		t.Fatalf("Rebind (second): %v", err)
	}
	if second.Generation != 2 || !second.Current() {
		t.Fatalf("second binding = %+v, want generation 2 and current", second)
	}

	current, err := store.CurrentBinding(ctx, ch.ID, participant.ID)
	if err != nil {
		t.Fatalf("CurrentBinding: %v", err)
	}
	if current.ID != second.ID || current.RuntimeEndpointRef != "session-beta" {
		t.Fatalf("CurrentBinding = %+v, want the second binding", current)
	}

	history, err := store.BindingHistory(ctx, ch.ID, participant.ID)
	if err != nil {
		t.Fatalf("BindingHistory: %v", err)
	}
	if len(history) != 2 || history[0].Generation != 1 || history[1].Generation != 2 {
		t.Fatalf("BindingHistory = %+v, want generations [1, 2]", history)
	}
	if history[0].Current() {
		t.Fatal("the superseded generation-1 binding still reads as current")
	}
	if history[0].SupersededAt == nil {
		t.Fatal("generation-1 binding has no superseded_at after rebind")
	}
	if history[0].RuntimeEndpointRef != "session-alpha" {
		t.Fatalf("generation-1 binding endpoint = %q, want the original session-alpha untouched", history[0].RuntimeEndpointRef)
	}

	// The schema enforces at most one current binding per (channel,
	// participant): a direct insert attempting a second uncontested current
	// row must fail, not just the Store's own transaction discipline.
	_, err = database.ExecContext(ctx, `
INSERT INTO channel_participant_bindings (id, channel_id, participant_id, generation, runtime_authority, bound_at)
VALUES ('rogue', ?, ?, 3, 'claude-code-cli', ?)`, ch.ID, participant.ID, first.BoundAt)
	if err == nil {
		t.Fatal("inserting a second current binding for the same (channel, participant) should violate the partial unique index")
	}
}

// TestRemoveParticipantSupersedesTheCurrentBinding is the CW-20260906-0066
// settlement of the decision CW-20260906-0064's review left open: leaving a
// channel supersedes the current binding in the same transaction, so
// CurrentBinding never resolves a destination for a non-member and a
// rejoin always requires an explicit Rebind.
func TestRemoveParticipantSupersedesTheCurrentBinding(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()

	participant, err := store.UpsertParticipant(ctx, UpsertParticipantParams{
		Kind: ParticipantAgent, ExternalAuthority: "claude-code", ExternalRef: "tangent-14",
	})
	if err != nil {
		t.Fatalf("UpsertParticipant: %v", err)
	}
	ch, err := store.CreateChannel(ctx, CreateChannelParams{OwnerScope: "standalone-local:anonymous"})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	_, err = store.AddParticipant(ctx, ch.ID, participant.ID)
	if err != nil {
		t.Fatalf("AddParticipant: %v", err)
	}
	bound, err := store.Rebind(ctx, ch.ID, participant.ID, RebindParams{
		RuntimeAuthority: "claude-code-cli", RuntimeEndpointRef: "session-alpha",
	})
	if err != nil {
		t.Fatalf("Rebind: %v", err)
	}

	err = store.RemoveParticipant(ctx, ch.ID, participant.ID)
	if err != nil {
		t.Fatalf("RemoveParticipant: %v", err)
	}

	_, err = store.CurrentBinding(ctx, ch.ID, participant.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("CurrentBinding after leaving = %v, want ErrNotFound", err)
	}
	history, err := store.BindingHistory(ctx, ch.ID, participant.ID)
	if err != nil {
		t.Fatalf("BindingHistory: %v", err)
	}
	if len(history) != 1 || history[0].ID != bound.ID || history[0].Current() || history[0].SupersededAt == nil {
		t.Fatalf("BindingHistory after leaving = %+v, want the one binding superseded", history)
	}

	// A rejoin does not resurrect the old binding: CurrentBinding stays
	// ErrNotFound until an explicit Rebind.
	_, err = store.AddParticipant(ctx, ch.ID, participant.ID)
	if err != nil {
		t.Fatalf("AddParticipant (rejoin): %v", err)
	}
	_, err = store.CurrentBinding(ctx, ch.ID, participant.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("CurrentBinding after rejoin without an explicit Rebind = %v, want ErrNotFound", err)
	}
}

func TestViewFocusIsIndependentPerView(t *testing.T) {
	t.Parallel()
	store, database := openTestStore(t)
	ctx := context.Background()

	ch, err := store.CreateChannel(ctx, CreateChannelParams{OwnerScope: "standalone-local:anonymous"})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	_, interactionID := seedSurfaceAndInteraction(t, database, "surface-view", "interaction-view")
	subjectA, err := store.AddSubject(ctx, CreateSubjectParams{ChannelID: ch.ID, Type: SubjectInteraction, InteractionID: interactionID, Title: "subject A"})
	if err != nil {
		t.Fatalf("AddSubject A: %v", err)
	}
	subjectB, err := store.AddSubject(ctx, CreateSubjectParams{ChannelID: ch.ID, Type: SubjectFreeform, Title: "subject B"})
	if err != nil {
		t.Fatalf("AddSubject B: %v", err)
	}

	_, err = store.SetViewFocus(ctx, SetViewFocusParams{ChannelID: ch.ID, ViewRef: "window-1", FocusedSubjectID: subjectA.ID})
	if err != nil {
		t.Fatalf("SetViewFocus window-1: %v", err)
	}
	_, err = store.SetViewFocus(ctx, SetViewFocusParams{ChannelID: ch.ID, ViewRef: "window-2", FocusedSubjectID: subjectB.ID})
	if err != nil {
		t.Fatalf("SetViewFocus window-2: %v", err)
	}

	focus1, err := store.GetViewFocus(ctx, ch.ID, "window-1")
	if err != nil {
		t.Fatalf("GetViewFocus window-1: %v", err)
	}
	focus2, err := store.GetViewFocus(ctx, ch.ID, "window-2")
	if err != nil {
		t.Fatalf("GetViewFocus window-2: %v", err)
	}
	if focus1.FocusedSubjectID != subjectA.ID {
		t.Fatalf("window-1 focus = %q, want subject A (%q)", focus1.FocusedSubjectID, subjectA.ID)
	}
	if focus2.FocusedSubjectID != subjectB.ID {
		t.Fatalf("window-2 focus = %q, want subject B (%q)", focus2.FocusedSubjectID, subjectB.ID)
	}

	// Moving window-1's focus to subject B must not move window-2's focus.
	_, err = store.SetViewFocus(ctx, SetViewFocusParams{ChannelID: ch.ID, ViewRef: "window-1", FocusedSubjectID: subjectB.ID})
	if err != nil {
		t.Fatalf("SetViewFocus window-1 (retarget): %v", err)
	}
	focus2Again, err := store.GetViewFocus(ctx, ch.ID, "window-2")
	if err != nil {
		t.Fatalf("GetViewFocus window-2 (again): %v", err)
	}
	if focus2Again.FocusedSubjectID != subjectB.ID {
		t.Fatalf("window-2 focus after retargeting window-1 = %q, want unchanged subject B (%q)", focus2Again.FocusedSubjectID, subjectB.ID)
	}
}

func TestArchiveChannelIsPresentationOnly(t *testing.T) {
	t.Parallel()
	store, database := openTestStore(t)
	ctx := context.Background()

	ch, err := store.CreateChannel(ctx, CreateChannelParams{OwnerScope: "standalone-local:anonymous"})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	_, interactionID := seedSurfaceAndInteraction(t, database, "surface-archive", "interaction-archive")
	subject, err := store.AddSubject(ctx, CreateSubjectParams{ChannelID: ch.ID, Type: SubjectInteraction, InteractionID: interactionID})
	if err != nil {
		t.Fatalf("AddSubject: %v", err)
	}

	err = store.ArchiveChannel(ctx, ch.ID)
	if err != nil {
		t.Fatalf("ArchiveChannel: %v", err)
	}
	archived, err := store.GetChannel(ctx, ch.ID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if archived.ArchivedAt == nil {
		t.Fatal("archived channel has no archived_at")
	}

	// Archiving retains obligations: the subject is still there, unresolved.
	still, err := store.GetSubject(ctx, subject.ID)
	if err != nil {
		t.Fatalf("GetSubject after archive: %v", err)
	}
	if still.Status != SubjectPending {
		t.Fatalf("subject status after archiving its channel = %q, want still pending", still.Status)
	}
}

// seedSurfaceAndInteraction inserts a minimal existing-substrate surface and
// interaction directly, the way internal/interaction's own store would, so
// channel_subjects can prove its canonical association against real rows
// without importing internal/interaction (which would be a layering
// inversion for a leaf storage package).
func seedSurfaceAndInteraction(t *testing.T, database *sql.DB, surfaceID, interactionID string) (string, string) {
	t.Helper()
	const now = "2026-09-07T00:00:00Z"
	if _, err := database.Exec(`
INSERT INTO surfaces (id, owner_scope, lifecycle_state, created_at, updated_at)
VALUES (?, 'standalone-local:anonymous', 'active', ?, ?)`, surfaceID, now, now); err != nil {
		t.Fatalf("seed surface: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO interactions (
  id, surface_id, caller_scope, caller_authority, caller_assurance,
  idempotency_key, surface_sequence, request_snapshot, lifecycle_state,
  created_at, updated_at
) VALUES (?, ?, 'standalone-local:anonymous', 'standalone-local', 'loopback-unverified', ?, 1, '{}', 'submitted', ?, ?)`,
		interactionID, surfaceID, "idem-"+interactionID, now, now); err != nil {
		t.Fatalf("seed interaction: %v", err)
	}
	return surfaceID, interactionID
}
