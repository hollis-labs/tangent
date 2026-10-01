package hitl

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	jsonschemav6 "github.com/santhosh-tekuri/jsonschema/v6"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/interaction"
)

func TestEnqueueAllocatesOneGlobalFIFOAndScopesIdempotency(t *testing.T) {
	service, interactions, database, _ := newTestService(t, "fifo.db")
	const total = 24

	handles := make(chan ItemHandle, total)
	errorsCh := make(chan error, total)
	var wait sync.WaitGroup
	for index := range total {
		wait.Add(1)
		go func() {
			defer wait.Done()
			application := fmt.Sprintf("application-%02d", index%4)
			handle, err := service.Enqueue(context.Background(), EnqueueInput{
				Request: hitlRequest(application, fmt.Sprintf("agent-%02d", index), fmt.Sprintf("key-%02d", index), fmt.Sprintf("Item %02d", index)),
				Caller:  directTestCaller(application, fmt.Sprintf("agent-%02d", index)),
			})
			if err != nil {
				errorsCh <- err
				return
			}
			handles <- handle
		}()
	}
	wait.Wait()
	close(handles)
	close(errorsCh)
	for err := range errorsCh {
		t.Errorf("Enqueue: %v", err)
	}

	sequences := make([]int, 0, total)
	ids := make(map[string]bool, total)
	for handle := range handles {
		if handle.SurfaceID != DefaultSurfaceID || handle.ItemID == "" || handle.State != interaction.InteractionStateStaged ||
			handle.Revision != 3 || handle.QueuePosition == nil || handle.InboxURL != "/inbox" || handle.ItemURL == "" {
			t.Errorf("invalid enqueue handle: %#v", handle)
		}
		if ids[handle.ItemID] {
			t.Errorf("duplicate item ID %q", handle.ItemID)
		}
		ids[handle.ItemID] = true
		sequences = append(sequences, int(handle.QueueSequence))
	}
	if len(sequences) != total {
		t.Fatalf("received %d handles, want %d", len(sequences), total)
	}
	sort.Ints(sequences)
	for index, sequence := range sequences {
		if sequence != index+1 {
			t.Fatalf("FIFO sequences = %v", sequences)
		}
	}
	var stored int
	if err := database.QueryRow(`SELECT COUNT(*) FROM interactions WHERE surface_id = ?`, DefaultSurfaceID).Scan(&stored); err != nil {
		t.Fatalf("count interactions: %v", err)
	}
	if stored != total {
		t.Fatalf("stored interactions = %d, want %d", stored, total)
	}

	// Identical retry, including semantically ignorable surrounding space,
	// returns the original current handle and never allocates another sequence.
	original, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: hitlRequest("shared", "agent-a", "collision", "  Same item  "),
		Caller:  directTestCaller("shared", "agent-a"),
	})
	if err != nil {
		t.Fatalf("first scoped enqueue: %v", err)
	}
	retry, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: hitlRequest("shared", "agent-a", "collision", "Same item"),
		Caller:  directTestCaller("shared", "agent-b"),
	})
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if retry.ItemID != original.ItemID || retry.QueueSequence != original.QueueSequence {
		t.Fatalf("retry = %#v, want original %#v", retry, original)
	}
	_, err = service.Enqueue(context.Background(), EnqueueInput{
		Request: hitlRequest("shared", "agent-a", "collision", "Different item"),
		Caller:  directTestCaller("shared", "agent-a"),
	})
	var conflict *IdempotencyConflictError
	if !errors.As(err, &conflict) || conflict.ExistingItemID != original.ItemID {
		t.Fatalf("changed retry error = %#v, want idempotency conflict for %s", err, original.ItemID)
	}

	otherScope, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: hitlRequest("other", "agent-a", "collision", "Different scope"),
		Caller:  directTestCaller("other", "agent-a"),
	})
	if err != nil || otherScope.ItemID == original.ItemID {
		t.Fatalf("same key in another scope = %#v, %v", otherScope, err)
	}

	// Terminating the first item removes it only from the projected position;
	// its immutable sequence and durable row remain.
	withdrawn, err := service.Withdraw(context.Background(), WithdrawInput{
		ItemID: original.ItemID, Caller: directTestCaller("shared", "agent-a"),
	})
	if err != nil || withdrawn.Cause != interaction.TerminalCauseCallerWithdrawn {
		t.Fatalf("Withdraw = %#v, %v", withdrawn, err)
	}
	assertMatchesContract(t, extensions.HITLTerminalOutcomeDefinition, withdrawn)
	terminalRetry, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: hitlRequest("shared", "agent-a", "collision", "Same item"),
		Caller:  directTestCaller("shared", "agent-a"),
	})
	if err != nil || terminalRetry.ItemID != original.ItemID || terminalRetry.QueuePosition != nil ||
		terminalRetry.State != interaction.InteractionStateCanceled {
		t.Fatalf("terminal retry = %#v, %v", terminalRetry, err)
	}

	_ = interactions
}

func TestOperatorInboxProjectsFIFOHistoryAndDurablePresentationToken(t *testing.T) {
	service, _, _, _ := newTestService(t, "operator-inbox.db")

	empty, err := service.Inbox(context.Background())
	if err != nil {
		t.Fatalf("empty Inbox: %v", err)
	}
	if empty.Revision != "empty" || len(empty.Pending) != 0 || len(empty.History) != 0 {
		t.Fatalf("empty Inbox = %#v", empty)
	}

	first, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: hitlRequest("codex", "worker-1", "operator-first", "First"),
		Caller:  directTestCaller("codex", "worker-1"),
	})
	if err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	second, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: hitlRequest("nanite", "worker-2", "operator-second", "Second"),
		Caller:  directTestCaller("nanite", "worker-2"),
	})
	if err != nil {
		t.Fatalf("enqueue second: %v", err)
	}

	inbox, err := service.Inbox(context.Background())
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if inbox.Revision == "empty" || len(inbox.Pending) != 2 ||
		inbox.Pending[0].ItemID != first.ItemID || inbox.Pending[1].ItemID != second.ItemID {
		t.Fatalf("FIFO Inbox = %#v", inbox)
	}
	if inbox.Pending[0].Revision != first.Revision || inbox.Pending[0].PresentedProjectionRevision != nil {
		t.Fatalf("inspection mutated staged item = %#v", inbox.Pending[0])
	}

	presented, err := service.Present(context.Background(), PresentInput{
		ItemID: first.ItemID, ExpectedRevision: first.Revision,
		PresentedProjectionRevision: first.Revision, ConnectionID: "browser-tab-a",
	})
	if err != nil {
		t.Fatalf("Present: %v", err)
	}
	persisted, err := service.InspectOperatorItem(context.Background(), first.ItemID)
	if err != nil {
		t.Fatalf("InspectOperatorItem: %v", err)
	}
	if persisted.Revision != presented.Revision || persisted.PresentedProjectionRevision == nil ||
		*persisted.PresentedProjectionRevision != first.Revision {
		t.Fatalf("persisted presentation = %#v", persisted)
	}

	resolved, err := service.Resolve(context.Background(), ResolveInput{
		ItemID: first.ItemID, ExpectedRevision: persisted.Revision,
		PresentedProjectionRevision: *persisted.PresentedProjectionRevision,
		Response:                    json.RawMessage(`{"kind":"approval","decision":"denied"}`),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.State != interaction.InteractionStateResolved {
		t.Fatalf("Resolve = %#v", resolved)
	}

	after, err := service.Inbox(context.Background())
	if err != nil {
		t.Fatalf("Inbox after resolve: %v", err)
	}
	if len(after.Pending) != 1 || after.Pending[0].ItemID != second.ItemID ||
		len(after.History) != 1 || after.History[0].ItemID != first.ItemID ||
		after.History[0].TerminalOutcome == nil {
		t.Fatalf("Inbox after resolve = %#v", after)
	}
	if after.Revision == inbox.Revision {
		t.Fatalf("inbox revision did not change after terminal commit: %q", after.Revision)
	}

	_, err = service.Present(context.Background(), PresentInput{
		ItemID: first.ItemID, ExpectedRevision: first.Revision,
		PresentedProjectionRevision: first.Revision, ConnectionID: "stale-tab",
	})
	var stale *StaleRevisionError
	if !errors.As(err, &stale) || stale.TerminalOutcome == nil ||
		stale.TerminalOutcome.State != interaction.InteractionStateResolved {
		t.Fatalf("stale Present error = %#v", err)
	}
}

func TestOperatorInboxKeepsPositionsWithinOneAtomicSnapshot(t *testing.T) {
	service, _, _, _ := newTestService(t, "operator-snapshot.db")
	first, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: hitlRequest("codex", "worker-1", "snapshot-first", "First"),
		Caller:  directTestCaller("codex", "worker-1"),
	})
	if err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	second, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: hitlRequest("nanite", "worker-2", "snapshot-second", "Second"),
		Caller:  directTestCaller("nanite", "worker-2"),
	})
	if err != nil {
		t.Fatalf("enqueue second: %v", err)
	}
	presented, err := service.Present(context.Background(), PresentInput{
		ItemID: first.ItemID, ExpectedRevision: first.Revision,
		PresentedProjectionRevision: first.Revision, ConnectionID: "snapshot-tab",
	})
	if err != nil {
		t.Fatalf("present first: %v", err)
	}

	loaded := make(chan struct{})
	resume := make(chan struct{})
	service.afterInboxLoad = func() {
		close(loaded)
		<-resume
	}
	type inboxResult struct {
		inbox OperatorInbox
		err   error
	}
	result := make(chan inboxResult, 1)
	go func() {
		inbox, inboxErr := service.Inbox(context.Background())
		result <- inboxResult{inbox: inbox, err: inboxErr}
	}()
	<-loaded
	_, err = service.Resolve(context.Background(), ResolveInput{
		ItemID: first.ItemID, ExpectedRevision: presented.Revision,
		PresentedProjectionRevision: first.Revision,
		Response:                    json.RawMessage(`{"kind":"approval","decision":"approved"}`),
	})
	if err != nil {
		t.Fatalf("resolve concurrent with projection: %v", err)
	}
	close(resume)
	projected := <-result
	if projected.err != nil {
		t.Fatalf("Inbox: %v", projected.err)
	}
	if len(projected.inbox.Pending) != 2 || projected.inbox.Pending[0].ItemID != first.ItemID ||
		projected.inbox.Pending[1].ItemID != second.ItemID || projected.inbox.Pending[0].QueuePosition == nil ||
		*projected.inbox.Pending[0].QueuePosition != 1 || projected.inbox.Pending[1].QueuePosition == nil ||
		*projected.inbox.Pending[1].QueuePosition != 2 {
		t.Fatalf("mixed-snapshot inbox = %#v", projected.inbox)
	}
}

func TestOperatorInboxResumesPresentedItemAfterRestart(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "operator-restart.db")
	service, _, database, envelopeService := newTestServiceAtPath(t, databasePath)
	caller := directTestCaller("codex", "restart-worker")
	handle, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: hitlRequest("codex", "restart-worker", "operator-restart", "Resume after restart"),
		Caller:  caller,
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	presented, err := service.Present(context.Background(), PresentInput{
		ItemID: handle.ItemID, ExpectedRevision: handle.Revision,
		PresentedProjectionRevision: handle.Revision, ConnectionID: "browser-before-restart",
	})
	if err != nil {
		t.Fatalf("Present: %v", err)
	}
	if closeErr := database.Close(); closeErr != nil {
		t.Fatalf("close database: %v", closeErr)
	}

	restartedDB, err := tangentdb.Open(databasePath)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	t.Cleanup(func() { _ = restartedDB.Close() })
	if migrationErr := tangentdb.RunMigrations(restartedDB); migrationErr != nil {
		t.Fatalf("RunMigrations: %v", migrationErr)
	}
	restartedInteractions, err := interaction.NewService(
		interaction.NewStore(restartedDB),
		interaction.NewEnvelopeDefinitionCatalog(envelopeService, "hitl-test-host"),
		interaction.WithAwaitPollInterval(time.Millisecond),
		interaction.WithSurfaceAccessPolicy(SurfaceAccessPolicy{}),
	)
	if err != nil {
		t.Fatalf("interaction.NewService: %v", err)
	}
	if _, recoveryErr := restartedInteractions.RecoverAfterRestart(context.Background()); recoveryErr != nil {
		t.Fatalf("RecoverAfterRestart: %v", recoveryErr)
	}
	restarted, err := NewService(restartedInteractions)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	item, err := restarted.InspectOperatorItem(context.Background(), handle.ItemID)
	if err != nil {
		t.Fatalf("InspectOperatorItem after restart: %v", err)
	}
	if item.State != interaction.InteractionStatePresented || item.Revision != presented.Revision ||
		item.PresentedProjectionRevision == nil || *item.PresentedProjectionRevision != handle.Revision {
		t.Fatalf("restarted item = %#v", item)
	}
	resolved, err := restarted.Resolve(context.Background(), ResolveInput{
		ItemID: handle.ItemID, ExpectedRevision: item.Revision,
		PresentedProjectionRevision: *item.PresentedProjectionRevision,
		Response:                    json.RawMessage(`{"kind":"approval","decision":"approved"}`),
	})
	if err != nil || resolved.State != interaction.InteractionStateResolved {
		t.Fatalf("Resolve after restart = %#v, %v", resolved, err)
	}
}

func TestAttentionSharesFIFOAndReturnsDurableAcknowledgement(t *testing.T) {
	service, interactions, database, _ := newTestService(t, "attention-fifo.db")
	caller := directTestCaller("codex", "attention-worker")
	first, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: hitlRequest("codex", "attention-worker", "mixed-first", "First approval"), Caller: caller,
	})
	if err != nil {
		t.Fatalf("enqueue first approval: %v", err)
	}
	attention, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: attentionRequest("codex", "attention-worker", "mixed-attention", "Review warning"), Caller: caller,
	})
	if err != nil {
		t.Fatalf("enqueue attention: %v", err)
	}
	third, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: hitlRequest("codex", "attention-worker", "mixed-third", "Third approval"), Caller: caller,
	})
	if err != nil {
		t.Fatalf("enqueue third approval: %v", err)
	}
	if first.QueueSequence != 1 || attention.QueueSequence != 2 || third.QueueSequence != 3 {
		t.Fatalf("mixed FIFO sequences = %d, %d, %d", first.QueueSequence, attention.QueueSequence, third.QueueSequence)
	}

	inbox, err := service.Inbox(context.Background())
	if err != nil {
		t.Fatalf("Inbox mixed kinds: %v", err)
	}
	if len(inbox.Pending) != 3 || inbox.Pending[0].ItemID != first.ItemID ||
		inbox.Pending[1].ItemID != attention.ItemID || inbox.Pending[2].ItemID != third.ItemID {
		t.Fatalf("mixed FIFO inbox = %#v", inbox.Pending)
	}
	var attentionSnapshot map[string]any
	if decodeErr := json.Unmarshal(inbox.Pending[1].RequestSnapshot, &attentionSnapshot); decodeErr != nil {
		t.Fatalf("decode attention snapshot: %v", decodeErr)
	}
	labels := attentionSnapshot["action_labels"].(map[string]any)
	if labels["reply"] != "Respond with status" {
		t.Fatalf("normalized attention reply label = %#v", labels["reply"])
	}

	presented, err := service.Present(context.Background(), PresentInput{
		ItemID: attention.ItemID, ExpectedRevision: attention.Revision,
		PresentedProjectionRevision: attention.Revision, ConnectionID: "attention-tab",
	})
	if err != nil {
		t.Fatalf("Present attention: %v", err)
	}
	resolved, err := service.Resolve(context.Background(), ResolveInput{
		ItemID: attention.ItemID, ExpectedRevision: presented.Revision,
		PresentedProjectionRevision: attention.Revision,
		Response: json.RawMessage(
			`{"kind":"attention","decision":"acknowledged","note":"  logged for shift  ","reply":"  Worker restarted safely.  "}`,
		),
	})
	if err != nil {
		t.Fatalf("Resolve attention: %v", err)
	}
	const wantResponse = `{"decision":"acknowledged","kind":"attention","note":"logged for shift","reply":"Worker restarted safely."}`
	if resolved.Resolution == nil || string(resolved.Resolution.Response) != wantResponse {
		t.Fatalf("attention resolution = %#v", resolved.Resolution)
	}
	assertMatchesContract(t, extensions.HITLTerminalOutcomeDefinition, resolved)

	after, err := service.Inbox(context.Background())
	if err != nil {
		t.Fatalf("Inbox after attention acknowledgement: %v", err)
	}
	if len(after.Pending) != 2 || after.Pending[0].ItemID != first.ItemID ||
		after.Pending[1].ItemID != third.ItemID || len(after.History) != 1 ||
		after.History[0].ItemID != attention.ItemID {
		t.Fatalf("mixed inbox after attention acknowledgement = %#v", after)
	}
	if *after.Pending[0].QueuePosition != 1 || *after.Pending[1].QueuePosition != 2 ||
		after.Pending[0].QueueSequence != 1 || after.Pending[1].QueueSequence != 3 {
		t.Fatalf("post-filter positions/sequences = %#v", after.Pending)
	}

	got, err := service.Get(context.Background(), GetInput{ItemID: attention.ItemID, Caller: caller})
	if err != nil || got.Item.TerminalOutcome == nil || got.Item.TerminalOutcome.Resolution == nil ||
		string(got.Item.TerminalOutcome.Resolution.Response) != wantResponse {
		t.Fatalf("Get attention outcome = %#v, %v", got, err)
	}
	zero := time.Duration(0)
	awaited, err := service.Await(context.Background(), AwaitInput{
		ItemID: attention.ItemID, Caller: caller, Wait: &zero,
	})
	if err != nil || awaited.WaitStatus != "terminal" || awaited.Item.TerminalOutcome == nil ||
		awaited.Item.TerminalOutcome.Resolution == nil ||
		string(awaited.Item.TerminalOutcome.Resolution.Response) != wantResponse {
		t.Fatalf("Await attention outcome = %#v, %v", awaited, err)
	}
	var resolutionCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM resolutions WHERE interaction_id = ?`, attention.ItemID).Scan(&resolutionCount); err != nil || resolutionCount != 1 {
		t.Fatalf("attention resolution rows = %d, err=%v", resolutionCount, err)
	}
	_ = interactions
}

func TestAttentionConcurrentAcknowledgementHasOneImmutableWinner(t *testing.T) {
	service, _, database, _ := newTestService(t, "attention-race.db")
	caller := directTestCaller("codex", "attention-race-worker")
	handle, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: attentionRequest("codex", "attention-race-worker", "attention-race", "Race acknowledgement"),
		Caller:  caller,
	})
	if err != nil {
		t.Fatalf("Enqueue attention: %v", err)
	}
	presented, err := service.Present(context.Background(), PresentInput{
		ItemID: handle.ItemID, ExpectedRevision: handle.Revision,
		PresentedProjectionRevision: handle.Revision, ConnectionID: "shared-view",
	})
	if err != nil {
		t.Fatalf("Present attention: %v", err)
	}

	type result struct {
		outcome TerminalOutcome
		err     error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	responses := []json.RawMessage{
		json.RawMessage(`{"kind":"attention","decision":"acknowledged","note":"first tab"}`),
		json.RawMessage(`{"kind":"attention","decision":"acknowledged","reply":"second tab"}`),
	}
	for _, response := range responses {
		go func() {
			<-start
			outcome, resolveErr := service.Resolve(context.Background(), ResolveInput{
				ItemID: handle.ItemID, ExpectedRevision: presented.Revision,
				PresentedProjectionRevision: handle.Revision, Response: response,
			})
			results <- result{outcome: outcome, err: resolveErr}
		}()
	}
	close(start)

	var winnerResponse string
	var loserResponse string
	successes := 0
	for range 2 {
		current := <-results
		if current.err == nil {
			successes++
			winnerResponse = string(current.outcome.Resolution.Response)
			continue
		}
		var stale *StaleRevisionError
		if !errors.As(current.err, &stale) || stale.TerminalOutcome == nil ||
			stale.TerminalOutcome.Resolution == nil {
			t.Errorf("losing acknowledgement error = %#v", current.err)
			continue
		}
		loserResponse = string(stale.TerminalOutcome.Resolution.Response)
		if loserResponse == "" {
			t.Errorf("stale outcome omitted immutable winner response: %#v", stale.TerminalOutcome)
		}
	}
	if successes != 1 {
		t.Fatalf("attention acknowledgement successes = %d, want 1", successes)
	}
	if loserResponse != winnerResponse {
		t.Fatalf("stale acknowledgement response = %s, want winner %s", loserResponse, winnerResponse)
	}
	final, err := service.InspectOperatorItem(context.Background(), handle.ItemID)
	if err != nil || final.TerminalOutcome == nil || final.TerminalOutcome.Resolution == nil ||
		string(final.TerminalOutcome.Resolution.Response) != winnerResponse {
		t.Fatalf("immutable attention winner = %#v, %v; want %s", final, err, winnerResponse)
	}
	var resolutionCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM resolutions WHERE interaction_id = ?`, handle.ItemID).Scan(&resolutionCount); err != nil || resolutionCount != 1 {
		t.Fatalf("attention race resolution rows = %d, err=%v", resolutionCount, err)
	}
}

func TestAttentionPendingStateAndAcknowledgementSurviveRestart(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "attention-restart.db")
	service, _, database, envelopeService := newTestServiceAtPath(t, databasePath)
	caller := directTestCaller("codex", "attention-restart-worker")
	handle, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: attentionRequest("codex", "attention-restart-worker", "attention-restart", "Restart warning"),
		Caller:  caller,
	})
	if err != nil {
		t.Fatalf("Enqueue attention: %v", err)
	}
	presented, err := service.Present(context.Background(), PresentInput{
		ItemID: handle.ItemID, ExpectedRevision: handle.Revision,
		PresentedProjectionRevision: handle.Revision, ConnectionID: "before-restart",
	})
	if err != nil {
		t.Fatalf("Present attention: %v", err)
	}
	if closeErr := database.Close(); closeErr != nil {
		t.Fatalf("close database: %v", closeErr)
	}

	restartedDB, err := tangentdb.Open(databasePath)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	t.Cleanup(func() { _ = restartedDB.Close() })
	if migrationErr := tangentdb.RunMigrations(restartedDB); migrationErr != nil {
		t.Fatalf("RunMigrations: %v", migrationErr)
	}
	restartedInteractions, err := interaction.NewService(
		interaction.NewStore(restartedDB),
		interaction.NewEnvelopeDefinitionCatalog(envelopeService, "hitl-test-host"),
		interaction.WithAwaitPollInterval(time.Millisecond),
		interaction.WithSurfaceAccessPolicy(SurfaceAccessPolicy{}),
	)
	if err != nil {
		t.Fatalf("interaction.NewService: %v", err)
	}
	if _, recoveryErr := restartedInteractions.RecoverAfterRestart(context.Background()); recoveryErr != nil {
		t.Fatalf("RecoverAfterRestart: %v", recoveryErr)
	}
	restarted, err := NewService(restartedInteractions)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	inbox, err := restarted.Inbox(context.Background())
	if err != nil || len(inbox.Pending) != 1 || inbox.Pending[0].ItemID != handle.ItemID ||
		inbox.Pending[0].State != interaction.InteractionStatePresented ||
		inbox.Pending[0].Revision != presented.Revision || inbox.Pending[0].PresentedProjectionRevision == nil ||
		*inbox.Pending[0].PresentedProjectionRevision != handle.Revision {
		t.Fatalf("restarted attention inbox = %#v, %v", inbox, err)
	}
	resolved, err := restarted.Resolve(context.Background(), ResolveInput{
		ItemID: handle.ItemID, ExpectedRevision: inbox.Pending[0].Revision,
		PresentedProjectionRevision: *inbox.Pending[0].PresentedProjectionRevision,
		Response: json.RawMessage(
			`{"kind":"attention","decision":"acknowledged","reply":"Recovered after restart."}`,
		),
	})
	if err != nil || resolved.Resolution == nil ||
		string(resolved.Resolution.Response) != `{"decision":"acknowledged","kind":"attention","reply":"Recovered after restart."}` {
		t.Fatalf("Resolve attention after restart = %#v, %v", resolved, err)
	}
	zero := time.Duration(0)
	retrieved, err := restarted.Await(context.Background(), AwaitInput{
		ItemID: handle.ItemID, Caller: caller, Wait: &zero,
	})
	if err != nil || retrieved.WaitStatus != "terminal" || retrieved.Item.TerminalOutcome == nil ||
		retrieved.Item.TerminalOutcome.Resolution == nil ||
		string(retrieved.Item.TerminalOutcome.Resolution.Response) != string(resolved.Resolution.Response) {
		t.Fatalf("restarted attention retrieval = %#v, %v", retrieved, err)
	}
}

func TestEnqueueRejectsConcurrentGenericIdempotencyWinner(t *testing.T) {
	service, interactions, database, _ := newTestService(t, "enqueue-race.db")
	caller := directTestCaller("race-owner", "agent")
	request := hitlRequest("race-owner", "agent", "shared-race-key", "Same immutable request")

	if _, err := interactions.OpenSurface(context.Background(), interaction.OpenSurfaceInput{
		ID: "generic-surface", Caller: caller, OwnerScope: caller.Scope,
		IdempotencyKey: "open-generic-surface",
	}); err != nil {
		t.Fatalf("OpenSurface: %v", err)
	}
	var winner interaction.InteractionHandle
	var winnerErr error
	service.beforeSubmit = func() {
		// Run the competing submit in another goroutine and join it before the
		// HITL submit continues. The hook fixes the interleaving at the exact
		// precheck/create boundary without relying on timing.
		done := make(chan struct{})
		go func() {
			defer close(done)
			winner, winnerErr = interactions.SubmitInteraction(context.Background(), interaction.SubmitInteractionInput{
				SurfaceID: "generic-surface", Caller: caller,
				IdempotencyKey: "shared-race-key",
				Definition: interaction.DefinitionRef{
					Kind: extensions.HITLItemEnvelopeType, Version: DefinitionVersion,
				},
				Request: request,
			})
		}()
		<-done
	}
	_, err := service.Enqueue(context.Background(), EnqueueInput{Request: request, Caller: caller})
	if winnerErr != nil {
		t.Fatalf("competing generic submit: %v", winnerErr)
	}
	var conflict *IdempotencyConflictError
	if !errors.As(err, &conflict) || conflict.ExistingItemID != winner.InteractionID {
		t.Fatalf("Enqueue race error = %#v, want conflict for %q", err, winner.InteractionID)
	}
	var hitlRows int
	if countErr := database.QueryRow(
		`SELECT COUNT(*) FROM interactions WHERE surface_id = ?`, DefaultSurfaceID,
	).Scan(&hitlRows); countErr != nil || hitlRows != 0 {
		t.Fatalf("HITL rows after generic winner = %d, err=%v", hitlRows, countErr)
	}
}

func TestAwaitResolutionSurvivesDisconnectAndRestartWithoutAcknowledgingDelivery(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "restart.db")
	service, interactions, database, envelopeService := newTestServiceAtPath(t, databasePath)
	caller := directTestCaller("codex", "worker-7")
	handle, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: hitlRequest("codex", "worker-7", "restart-resolution", "Approve restart"),
		Caller:  caller,
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	assertMatchesContract(t, extensions.HITLItemHandleDefinition, handle)

	waitCtx, cancelWait := context.WithCancel(context.Background())
	waitResult := make(chan error, 1)
	waitDuration := 5 * time.Second
	go func() {
		_, awaitErr := service.Await(waitCtx, AwaitInput{ItemID: handle.ItemID, Caller: caller, Wait: &waitDuration})
		waitResult <- awaitErr
	}()
	cancelWait()
	if awaitErr := <-waitResult; !errors.Is(awaitErr, context.Canceled) {
		t.Fatalf("canceled await error = %v", awaitErr)
	}
	stillStaged, err := interactions.InspectInteraction(context.Background(), interaction.GetInteractionInput{
		InteractionID: handle.ItemID, RequesterScope: caller.Scope, Capability: reservedSurfaceCapability,
	})
	if err != nil || stillStaged.Interaction.State != interaction.InteractionStateStaged {
		t.Fatalf("await cancellation changed lifecycle: %#v, %v", stillStaged, err)
	}

	presented, err := service.Present(context.Background(), PresentInput{
		ItemID: handle.ItemID, ExpectedRevision: handle.Revision,
		PresentedProjectionRevision: 11, ConnectionID: "browser-before-disconnect",
	})
	if err != nil {
		t.Fatalf("Present: %v", err)
	}
	resolved, err := service.Resolve(context.Background(), ResolveInput{
		ItemID: handle.ItemID, ExpectedRevision: presented.Revision,
		PresentedProjectionRevision: 11,
		Response:                    json.RawMessage(`{"kind":"approval","decision":"approved","note":"  reviewed  "}`),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.State != interaction.InteractionStateResolved || resolved.Resolution == nil ||
		string(resolved.Resolution.Response) != `{"decision":"approved","kind":"approval","note":"reviewed"}` {
		t.Fatalf("Resolve outcome = %#v", resolved)
	}
	stored, err := interactions.InspectInteraction(context.Background(), interaction.GetInteractionInput{
		InteractionID: handle.ItemID, RequesterScope: caller.Scope, Capability: reservedSurfaceCapability,
	})
	if err != nil || len(stored.ResolutionDeliveries) != 1 ||
		stored.ResolutionDeliveries[0].State != interaction.DeliveryStateQueued {
		t.Fatalf("resolution delivery before restart = %#v, %v", stored.ResolutionDeliveries, err)
	}

	if closeErr := database.Close(); closeErr != nil {
		t.Fatalf("close pre-restart database: %v", closeErr)
	}
	restartedDB, err := tangentdb.Open(databasePath)
	if err != nil {
		t.Fatalf("reopen DB: %v", err)
	}
	t.Cleanup(func() { _ = restartedDB.Close() })
	if migrationErr := tangentdb.RunMigrations(restartedDB); migrationErr != nil {
		t.Fatalf("RunMigrations after restart: %v", migrationErr)
	}
	restartedInteractions, err := interaction.NewService(
		interaction.NewStore(restartedDB),
		interaction.NewEnvelopeDefinitionCatalog(envelopeService, "hitl-test-host"),
		interaction.WithAwaitPollInterval(time.Millisecond),
		interaction.WithSurfaceAccessPolicy(SurfaceAccessPolicy{}),
	)
	if err != nil {
		t.Fatalf("interaction.NewService after restart: %v", err)
	}
	if _, recoveryErr := restartedInteractions.RecoverAfterRestart(context.Background()); recoveryErr != nil {
		t.Fatalf("RecoverAfterRestart: %v", recoveryErr)
	}
	restarted, err := NewService(restartedInteractions)
	if err != nil {
		t.Fatalf("NewService after restart: %v", err)
	}
	poll := time.Duration(0)
	retrieved, err := restarted.Await(context.Background(), AwaitInput{
		ItemID: handle.ItemID, Caller: caller, Wait: &poll,
	})
	if err != nil {
		t.Fatalf("Await after restart: %v", err)
	}
	if retrieved.WaitStatus != "terminal" || retrieved.Item.TerminalOutcome == nil ||
		retrieved.Item.TerminalOutcome.Resolution == nil ||
		string(retrieved.Item.TerminalOutcome.Resolution.Response) != `{"decision":"approved","kind":"approval","note":"reviewed"}` {
		t.Fatalf("restarted await = %#v", retrieved)
	}
	assertMatchesContract(t, extensions.HITLRetrievalResultDefinition, retrieved)
	afterRetrieval, err := restartedInteractions.InspectInteraction(context.Background(), interaction.GetInteractionInput{
		InteractionID: handle.ItemID, RequesterScope: caller.Scope, Capability: reservedSurfaceCapability,
	})
	if err != nil || len(afterRetrieval.ResolutionDeliveries) != 1 ||
		afterRetrieval.ResolutionDeliveries[0].State != interaction.DeliveryStateQueued ||
		afterRetrieval.Retrieval != nil {
		t.Fatalf("retrieval conflated delivery/inspection: %#v, %v", afterRetrieval, err)
	}
}

func TestWithdrawResolveRaceAndDistinctTerminalCauses(t *testing.T) {
	service, interactions, _, _ := newTestService(t, "terminal-race.db")
	caller := directTestCaller("race", "agent")
	handle, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: hitlRequest("race", "agent", "race-1", "Resolve or withdraw"), Caller: caller,
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	presented, err := service.Present(context.Background(), PresentInput{
		ItemID: handle.ItemID, ExpectedRevision: handle.Revision, PresentedProjectionRevision: 2,
	})
	if err != nil {
		t.Fatalf("Present: %v", err)
	}
	expected := presented.Revision
	type result struct {
		state interaction.InteractionState
		err   error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	go func() {
		<-start
		outcome, resolveErr := service.Resolve(context.Background(), ResolveInput{
			ItemID: handle.ItemID, ExpectedRevision: expected, PresentedProjectionRevision: 2,
			Response: json.RawMessage(`{"kind":"approval","decision":"denied"}`),
		})
		results <- result{state: outcome.State, err: resolveErr}
	}()
	go func() {
		<-start
		outcome, withdrawErr := service.Withdraw(context.Background(), WithdrawInput{
			ItemID: handle.ItemID, Caller: caller, ExpectedRevision: &expected,
		})
		results <- result{state: outcome.State, err: withdrawErr}
	}()
	close(start)
	successes := 0
	for range 2 {
		result := <-results
		if result.err == nil {
			successes++
			if result.state != interaction.InteractionStateResolved && result.state != interaction.InteractionStateCanceled {
				t.Errorf("winning state = %s", result.state)
			}
		} else if !errors.Is(result.err, interaction.ErrRevisionConflict) && !errors.Is(result.err, ErrTerminalConflict) {
			t.Errorf("losing error = %v", result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("terminal race successes = %d, want 1", successes)
	}
	final, err := interactions.InspectInteraction(context.Background(), interaction.GetInteractionInput{
		InteractionID: handle.ItemID, RequesterScope: caller.Scope, Capability: reservedSurfaceCapability,
	})
	if err != nil || (final.Interaction.State != interaction.InteractionStateResolved &&
		final.Interaction.TerminalCause != interaction.TerminalCauseCallerWithdrawn) {
		t.Fatalf("terminal race final = %#v, %v", final.Interaction, err)
	}

	participantHandle, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: hitlRequest("race", "agent", "participant-cancel", "Participant cancel"), Caller: caller,
	})
	if err != nil {
		t.Fatalf("participant item enqueue: %v", err)
	}
	participantPresented, err := service.Present(context.Background(), PresentInput{
		ItemID: participantHandle.ItemID, ExpectedRevision: participantHandle.Revision,
		PresentedProjectionRevision: 1,
	})
	if err != nil {
		t.Fatalf("participant item present: %v", err)
	}
	if _, cancelErr := interactions.CancelInteraction(context.Background(), interaction.CancelInteractionInput{
		InteractionID: participantHandle.ItemID, ExpectedRevision: participantPresented.Revision,
		Requester: OperatorParticipant, Cause: interaction.TerminalCauseParticipantCanceled,
		Capability: reservedSurfaceCapability,
	}); cancelErr != nil {
		t.Fatalf("participant cancellation: %v", cancelErr)
	}
	_, err = service.Withdraw(context.Background(), WithdrawInput{
		ItemID: participantHandle.ItemID, Caller: caller,
	})
	var terminalConflict *TerminalConflictError
	if !errors.As(err, &terminalConflict) || terminalConflict.Outcome.Cause != interaction.TerminalCauseParticipantCanceled {
		t.Fatalf("withdraw after participant cancellation = %#v", err)
	}
}

func TestRequestAndResponseSemanticValidation(t *testing.T) {
	service, interactions, database, _ := newTestService(t, "validation.db")
	caller := directTestCaller("validation", "agent")
	_, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: hitlRequest("validation", "agent", "blank", "   "), Caller: caller,
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("blank title error = %v", err)
	}
	var surfaces int
	if queryErr := database.QueryRow(`SELECT COUNT(*) FROM surfaces`).Scan(&surfaces); queryErr != nil || surfaces != 0 {
		t.Fatalf("invalid first enqueue mutated durable surfaces: count=%d err=%v", surfaces, queryErr)
	}

	request := map[string]any{}
	if decodeErr := json.Unmarshal(hitlRequest("validation", "agent", "response", "  Preserve evidence  "), &request); decodeErr != nil {
		t.Fatalf("decode request fixture: %v", decodeErr)
	}
	evidenceContent := "  context line\n+ exact addition \n"
	request["evidence"] = []any{map[string]any{
		"type": "diff", "label": "  Exact diff  ", "format": "unified", "content": evidenceContent,
	}}
	rawRequest, _ := json.Marshal(request)
	handle, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: rawRequest, Caller: caller,
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	stored, err := interactions.InspectInteraction(context.Background(), interaction.GetInteractionInput{
		InteractionID: handle.ItemID, RequesterScope: caller.Scope, Capability: reservedSurfaceCapability,
	})
	if err != nil {
		t.Fatalf("InspectInteraction: %v", err)
	}
	var snapshot map[string]any
	if decodeErr := json.Unmarshal(stored.Interaction.RequestSnapshot, &snapshot); decodeErr != nil {
		t.Fatalf("decode persisted request: %v", decodeErr)
	}
	if snapshot["title"] != "Preserve evidence" {
		t.Fatalf("normalized title = %#v", snapshot["title"])
	}
	evidence := snapshot["evidence"].([]any)[0].(map[string]any)
	if evidence["content"] != evidenceContent || evidence["label"] != "Exact diff" {
		t.Fatalf("persisted evidence = %#v", evidence)
	}

	requestWithOtherKey := cloneJSONMap(t, snapshot)
	requestWithOtherKey["idempotency_key"] = "another-key"
	rawOtherKey, _ := json.Marshal(requestWithOtherKey)
	digestA, err := requestDigest(stored.Interaction.RequestSnapshot)
	if err != nil {
		t.Fatalf("requestDigest stored: %v", err)
	}
	digestB, err := requestDigest(rawOtherKey)
	if err != nil || digestA != digestB {
		t.Fatalf("idempotency key affected request digest: %q vs %q, err=%v", digestA, digestB, err)
	}

	invalidRetry := cloneJSONMap(t, request)
	invalidRetry["kind"] = " approval "
	rawInvalidRetry, _ := json.Marshal(invalidRetry)
	_, err = service.Enqueue(context.Background(), EnqueueInput{Request: rawInvalidRetry, Caller: caller})
	if !errors.Is(err, ErrInvalidRequest) || errors.Is(err, interaction.ErrIdempotencyConflict) {
		t.Fatalf("invalid changed retry precedence error = %v", err)
	}
	var interactionsCount int
	if queryErr := database.QueryRow(`SELECT COUNT(*) FROM interactions`).Scan(&interactionsCount); queryErr != nil || interactionsCount != 1 {
		t.Fatalf("invalid retry mutated interactions: count=%d err=%v", interactionsCount, queryErr)
	}

	presented, err := service.Present(context.Background(), PresentInput{
		ItemID: handle.ItemID, ExpectedRevision: handle.Revision, PresentedProjectionRevision: 1,
	})
	if err != nil {
		t.Fatalf("Present: %v", err)
	}
	_, err = service.Resolve(context.Background(), ResolveInput{
		ItemID: handle.ItemID, ExpectedRevision: presented.Revision, PresentedProjectionRevision: 1,
		Response: json.RawMessage(`{"kind":"attention","decision":"acknowledged"}`),
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("kind-mismatched response error = %v", err)
	}
	_, err = service.Resolve(context.Background(), ResolveInput{
		ItemID: handle.ItemID, ExpectedRevision: presented.Revision, PresentedProjectionRevision: 1,
		Response: json.RawMessage(`{"kind":"approval","decision":" approved "}`),
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("padded decision error = %v", err)
	}
}

func TestAttentionResponseSemanticValidation(t *testing.T) {
	service, _, _, _ := newTestService(t, "attention-validation.db")
	caller := directTestCaller("validation", "attention-agent")
	handle, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: attentionRequest("validation", "attention-agent", "attention-response", "Validate acknowledgement"),
		Caller:  caller,
	})
	if err != nil {
		t.Fatalf("Enqueue attention: %v", err)
	}
	presented, err := service.Present(context.Background(), PresentInput{
		ItemID: handle.ItemID, ExpectedRevision: handle.Revision,
		PresentedProjectionRevision: handle.Revision,
	})
	if err != nil {
		t.Fatalf("Present attention: %v", err)
	}

	invalid := []struct {
		name     string
		response json.RawMessage
	}{
		{name: "wrong kind", response: json.RawMessage(`{"kind":"approval","decision":"approved"}`)},
		{name: "wrong decision", response: json.RawMessage(`{"kind":"attention","decision":"approved"}`)},
		{name: "blank note", response: json.RawMessage(`{"kind":"attention","decision":"acknowledged","note":"   "}`)},
		{name: "blank reply", response: json.RawMessage(`{"kind":"attention","decision":"acknowledged","reply":"\n\t"}`)},
		{name: "unknown field", response: json.RawMessage(`{"kind":"attention","decision":"acknowledged","result":"done"}`)},
		{
			name: "oversized reply",
			response: func() json.RawMessage {
				raw, marshalErr := json.Marshal(map[string]any{
					"kind": "attention", "decision": "acknowledged", "reply": strings.Repeat("x", 12001),
				})
				if marshalErr != nil {
					panic(marshalErr)
				}
				return raw
			}(),
		},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			_, resolveErr := service.Resolve(context.Background(), ResolveInput{
				ItemID: handle.ItemID, ExpectedRevision: presented.Revision,
				PresentedProjectionRevision: handle.Revision, Response: tt.response,
			})
			if !errors.Is(resolveErr, ErrInvalidRequest) {
				t.Fatalf("Resolve error = %v, want ErrInvalidRequest", resolveErr)
			}
		})
	}
	current, err := service.InspectOperatorItem(context.Background(), handle.ItemID)
	if err != nil || current.State != interaction.InteractionStatePresented || current.Revision != presented.Revision {
		t.Fatalf("invalid attention responses mutated item = %#v, %v", current, err)
	}
}

func TestHITLOperationsRejectStagedResolveAndNonHITLHandles(t *testing.T) {
	service, interactions, database, _ := newTestService(t, "membership.db")
	caller := directTestCaller("membership", "agent")
	staged, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: hitlRequest("membership", "agent", "staged", "Not presented"), Caller: caller,
	})
	if err != nil {
		t.Fatalf("Enqueue staged item: %v", err)
	}
	_, err = service.Resolve(context.Background(), ResolveInput{
		ItemID: staged.ItemID, ExpectedRevision: staged.Revision,
		PresentedProjectionRevision: 1,
		Response:                    json.RawMessage(`{"kind":"approval","decision":"approved"}`),
	})
	if !errors.Is(err, interaction.ErrNotRespondable) || errors.Is(err, interaction.ErrRevisionConflict) {
		t.Fatalf("staged Resolve error = %v", err)
	}

	if _, err = interactions.OpenSurface(context.Background(), interaction.OpenSurfaceInput{
		ID: "generic-owned", Caller: caller, OwnerScope: caller.Scope,
		IdempotencyKey: "generic-owned-open",
	}); err != nil {
		t.Fatalf("OpenSurface generic-owned: %v", err)
	}
	generic, err := interactions.SubmitInteraction(context.Background(), interaction.SubmitInteractionInput{
		SurfaceID: "generic-owned", Caller: caller, IdempotencyKey: "generic-item",
		Definition: interaction.DefinitionRef{Kind: extensions.HITLItemEnvelopeType, Version: DefinitionVersion},
		Request:    hitlRequest("membership", "agent", "generic-item", "Generic record"),
	})
	if err != nil {
		t.Fatalf("SubmitInteraction generic-owned: %v", err)
	}
	if _, err = interactions.CancelInteraction(context.Background(), interaction.CancelInteractionInput{
		InteractionID: generic.InteractionID, ExpectedRevision: generic.Revision,
		Requester: caller, Cause: interaction.TerminalCauseCallerCanceled,
	}); err != nil {
		t.Fatalf("CancelInteraction generic-owned: %v", err)
	}

	assertNotFound := func(operation string, err error) {
		t.Helper()
		if !errors.Is(err, interaction.ErrNotFound) {
			t.Fatalf("%s error = %v, want ErrNotFound", operation, err)
		}
	}
	_, err = service.Get(context.Background(), GetInput{ItemID: generic.InteractionID, Caller: caller})
	assertNotFound("Get cross-surface", err)
	noWait := time.Duration(0)
	_, err = service.Await(context.Background(), AwaitInput{
		ItemID: generic.InteractionID, Caller: caller, Wait: &noWait,
	})
	assertNotFound("Await cross-surface", err)
	var retrievals int
	if countErr := database.QueryRow(
		`SELECT COUNT(*) FROM terminal_outcome_retrievals WHERE interaction_id = ?`, generic.InteractionID,
	).Scan(&retrievals); countErr != nil || retrievals != 0 {
		t.Fatalf("cross-surface HITL read recorded retrievals = %d, err=%v", retrievals, countErr)
	}

	binding, err := interactions.ResolveInteractionDefinition(context.Background(), interaction.DefinitionRef{
		Kind: extensions.HITLItemEnvelopeType, Version: DefinitionVersion,
	})
	if err != nil {
		t.Fatalf("ResolveInteractionDefinition: %v", err)
	}
	binding.Kind = "example.not-hitl"
	wrongDefinition, err := interaction.NewStore(database).CreateInteraction(context.Background(), interaction.CreateInteractionParams{
		SurfaceID: DefaultSurfaceID, CallerScope: caller.Scope,
		CallerPrincipalRef: caller.PrincipalRef, CallerAuthority: caller.Authority,
		CallerAssurance: caller.Assurance, IdempotencyKey: "wrong-definition",
		Definition:      binding,
		RequestSnapshot: hitlRequest("membership", "agent", "wrong-definition", "Wrong definition"),
		ActorRef:        caller.PrincipalRef, Authority: caller.Authority,
	})
	if err != nil {
		t.Fatalf("CreateInteraction wrong definition: %v", err)
	}
	wrongID := wrongDefinition.Interaction.ID
	_, err = service.Withdraw(context.Background(), WithdrawInput{ItemID: wrongID, Caller: caller})
	assertNotFound("Withdraw wrong definition", err)
	_, err = service.Present(context.Background(), PresentInput{
		ItemID: wrongID, ExpectedRevision: wrongDefinition.Interaction.Revision,
		PresentedProjectionRevision: 1,
	})
	assertNotFound("Present wrong definition", err)
	_, err = service.Resolve(context.Background(), ResolveInput{
		ItemID: wrongID, ExpectedRevision: wrongDefinition.Interaction.Revision,
		PresentedProjectionRevision: 1,
		Response:                    json.RawMessage(`{"kind":"approval","decision":"approved"}`),
	})
	assertNotFound("Resolve wrong definition", err)
}

func newTestService(t *testing.T, filename string) (*Service, *interaction.Service, *sql.DB, *envelope.Service) {
	t.Helper()
	return newTestServiceAtPath(t, filepath.Join(t.TempDir(), filename))
}

func newTestServiceAtPath(t *testing.T, databasePath string) (*Service, *interaction.Service, *sql.DB, *envelope.Service) {
	t.Helper()
	database, err := tangentdb.Open(databasePath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrationErr := tangentdb.RunMigrations(database); migrationErr != nil {
		t.Fatalf("db.RunMigrations: %v", migrationErr)
	}
	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if registrationErr := extensions.RegisterHITLItem(envelopeService); registrationErr != nil {
		t.Fatalf("RegisterHITLItem: %v", registrationErr)
	}
	interactions, err := interaction.NewService(
		interaction.NewStore(database),
		interaction.NewEnvelopeDefinitionCatalog(envelopeService, "hitl-test-host"),
		interaction.WithAwaitPollInterval(time.Millisecond),
		interaction.WithSurfaceAccessPolicy(SurfaceAccessPolicy{}),
	)
	if err != nil {
		t.Fatalf("interaction.NewService: %v", err)
	}
	service, err := NewService(interactions)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service, interactions, database, envelopeService
}

func hitlRequest(applicationID, agentID, key, title string) json.RawMessage {
	request, err := json.Marshal(map[string]any{
		"contract_version": ContractVersion,
		"kind":             "approval",
		"idempotency_key":  key,
		"title":            title,
		"summary":          "Test durable HITL semantics",
		"request":          "Approve or deny this request",
		"source": map[string]any{
			"application_id": applicationID,
			"agent_id":       agentID,
		},
	})
	if err != nil {
		panic(err)
	}
	return request
}

func attentionRequest(applicationID, agentID, key, title string) json.RawMessage {
	request, err := json.Marshal(map[string]any{
		"contract_version": ContractVersion,
		"kind":             "attention",
		"idempotency_key":  key,
		"title":            title,
		"summary":          "Test durable attention semantics",
		"request":          "Acknowledge this persistent attention item",
		"source": map[string]any{
			"application_id": applicationID,
			"agent_id":       agentID,
		},
		"action_labels": map[string]any{
			"acknowledge":           "Mark seen",
			"acknowledge_with_note": "Log context",
			"reply":                 "  Respond with status  ",
		},
	})
	if err != nil {
		panic(err)
	}
	return request
}

func directTestCaller(applicationID, agentID string) interaction.ActorBinding {
	return interaction.ActorBinding{
		Scope: "direct-loopback:" + applicationID, PrincipalRef: agentID,
		Authority: "direct-loopback", Assurance: "asserted",
	}
}

func cloneJSONMap(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal clone input: %v", err)
	}
	var clone map[string]any
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatalf("unmarshal clone input: %v", err)
	}
	return clone
}

func assertMatchesContract(t *testing.T, definition string, value any) {
	t.Helper()
	rawSchema, err := extensions.HITLContractDefinitionSchema(definition)
	if err != nil {
		t.Fatalf("HITLContractDefinitionSchema(%s): %v", definition, err)
	}
	var document any
	if decodeErr := json.Unmarshal(rawSchema, &document); decodeErr != nil {
		t.Fatalf("decode %s schema: %v", definition, decodeErr)
	}
	compiler := jsonschemav6.NewCompiler()
	const schemaURI = "memory://tangent/hitl-test.schema.json"
	if addErr := compiler.AddResource(schemaURI, document); addErr != nil {
		t.Fatalf("add %s schema: %v", definition, addErr)
	}
	compiled, err := compiler.Compile(schemaURI)
	if err != nil {
		t.Fatalf("compile %s schema: %v", definition, err)
	}
	rawValue, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s value: %v", definition, err)
	}
	var decoded any
	if err := json.Unmarshal(rawValue, &decoded); err != nil {
		t.Fatalf("decode %s value: %v", definition, err)
	}
	if err := compiled.Validate(decoded); err != nil {
		t.Fatalf("%s value does not match contract: %v\n%s", definition, err, rawValue)
	}
}
