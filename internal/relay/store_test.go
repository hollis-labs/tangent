package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/channel"
	tangentdb "github.com/hollis-labs/tangent/internal/db"
)

// testHarness bundles the two stores a caller of this package always needs
// together, plus the raw handle for direct verification.
type testHarness struct {
	db       *sql.DB
	channels *channel.Store
	relay    *Store
}

func openTestHarness(t *testing.T) *testHarness {
	t.Helper()
	return openTestHarnessAt(t, filepath.Join(t.TempDir(), "relay.db"))
}

func openTestHarnessAt(t *testing.T, path string) *testHarness {
	t.Helper()
	database, err := tangentdb.Open(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	err = tangentdb.RunMigrations(database)
	if err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	channels, err := channel.NewStore(database)
	if err != nil {
		t.Fatalf("channel.NewStore: %v", err)
	}
	relayStore, err := NewStore(database, channels)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return &testHarness{db: database, channels: channels, relay: relayStore}
}

// seedChannel creates a channel with an operator and an agent participant,
// both bound as members, and returns their ids.
func (h *testHarness) seedChannel(t *testing.T) (channelID, operatorID, agentID string) {
	t.Helper()
	ctx := context.Background()

	ch, err := h.channels.CreateChannel(ctx, channel.CreateChannelParams{OwnerScope: "standalone-local:anonymous"})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	operator, err := h.channels.UpsertParticipant(ctx, channel.UpsertParticipantParams{Kind: channel.ParticipantOperator})
	if err != nil {
		t.Fatalf("UpsertParticipant (operator): %v", err)
	}
	agent, err := h.channels.UpsertParticipant(ctx, channel.UpsertParticipantParams{
		Kind: channel.ParticipantAgent, ExternalAuthority: "claude-code", ExternalRef: "tangent-14",
	})
	if err != nil {
		t.Fatalf("UpsertParticipant (agent): %v", err)
	}
	if _, err := h.channels.AddParticipant(ctx, ch.ID, operator.ID); err != nil {
		t.Fatalf("AddParticipant (operator): %v", err)
	}
	if _, err := h.channels.AddParticipant(ctx, ch.ID, agent.ID); err != nil {
		t.Fatalf("AddParticipant (agent): %v", err)
	}
	return ch.ID, operator.ID, agent.ID
}

func TestAcceptExchangeInsertsExchangeAndOutboxAtomically(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	exchange, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "idem-1", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID,
		Body: "run the smoke test",
	})
	if err != nil {
		t.Fatalf("AcceptExchange: %v", err)
	}
	if exchange.ID == "" || exchange.Sequence != 1 {
		t.Fatalf("AcceptExchange = %+v, want an id and sequence 1", exchange)
	}
	// No live agent binding exists yet: this is a legitimate queued state,
	// not an error.
	if exchange.RecipientBindingID != "" {
		t.Fatalf("RecipientBindingID = %q, want empty (no live binding at accept time)", exchange.RecipientBindingID)
	}

	item, err := h.relay.GetOutboxItem(ctx, exchange.ID)
	if err != nil {
		t.Fatalf("GetOutboxItem: %v", err)
	}
	if item.Status != OutboxPending || item.Attempts != 0 {
		t.Fatalf("outbox item = %+v, want pending with zero attempts, inserted atomically with the exchange", item)
	}
}

func TestAcceptExchangeIsIdempotent(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	params := AcceptExchangeParams{
		IdempotencyKey: "idem-dup", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "reply once",
	}
	first, err := h.relay.AcceptExchange(ctx, params)
	if err != nil {
		t.Fatalf("AcceptExchange (first): %v", err)
	}
	second, err := h.relay.AcceptExchange(ctx, params)
	if err != nil {
		t.Fatalf("AcceptExchange (replay): %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("replaying the same idempotency key minted a second exchange: %s != %s", first.ID, second.ID)
	}

	var exchangeCount int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM exchanges WHERE idempotency_key = 'idem-dup'`).Scan(&exchangeCount); err != nil {
		t.Fatalf("count exchanges: %v", err)
	}
	if exchangeCount != 1 {
		t.Fatalf("exchanges with idem-dup = %d, want 1", exchangeCount)
	}

	// The same key with a different body is a conflict, not a silent
	// resolve to the earlier row.
	conflicting := params
	conflicting.Body = "a different message under the same key"
	if _, err := h.relay.AcceptExchange(ctx, conflicting); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("AcceptExchange with a reused key and a different body = %v, want ErrIdempotencyConflict", err)
	}
}

// TestAcceptExchangeReplayResolvesAfterTheOriginalIsRedacted is
// CW-20260907-0043's required case: AcceptExchange's idempotency-conflict
// check compares bodies, and a redacted body is a tombstone JSON string that
// can never equal a real retry body. Without the fix, every legitimate
// replay of an already-redacted exchange would misfire as
// ErrIdempotencyConflict. The key match alone is trusted instead once the
// original is redacted — there is no longer a real body left to compare
// against — and the caller must still be able to tell the content is gone:
// Exchange.BodyRedacted() is that signal, not a silently-returned tombstone.
func TestAcceptExchangeReplayResolvesAfterTheOriginalIsRedacted(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	params := AcceptExchangeParams{
		IdempotencyKey: "idem-redacted-replay", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID,
		Body: "the original message",
	}
	original, err := h.relay.AcceptExchange(ctx, params)
	if err != nil {
		t.Fatalf("AcceptExchange (original): %v", err)
	}
	if original.BodyRedacted() {
		t.Fatal("a freshly accepted exchange reports its body as already redacted")
	}

	if _, redactErr := tangentdb.RedactExchange(ctx, h.db, tangentdb.RetentionRequest{
		ExchangeID: original.ID, ActorRef: "operator:test", Authority: tangentdb.AuthorityLocalUser,
	}); redactErr != nil {
		t.Fatalf("RedactExchange: %v", redactErr)
	}

	replayed, err := h.relay.AcceptExchange(ctx, params)
	if err != nil {
		t.Fatalf("AcceptExchange (replay after redaction) = %v, want it to resolve to the existing exchange", err)
	}
	if replayed.ID != original.ID {
		t.Fatalf("replay after redaction minted a second exchange: %s != %s", replayed.ID, original.ID)
	}
	if !replayed.BodyRedacted() {
		t.Fatal("the replay result does not signal that its content was already erased")
	}

	var exchangeCount int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM exchanges WHERE idempotency_key = 'idem-redacted-replay'`).
		Scan(&exchangeCount); err != nil {
		t.Fatalf("count exchanges: %v", err)
	}
	if exchangeCount != 1 {
		t.Fatalf("exchanges with idem-redacted-replay = %d, want 1", exchangeCount)
	}
}

func TestAcceptExchangeRefusesANonMember(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, _ := h.seedChannel(t)

	stranger, err := h.channels.UpsertParticipant(ctx, channel.UpsertParticipantParams{
		Kind: channel.ParticipantAgent, ExternalAuthority: "claude-code", ExternalRef: "not-in-this-channel",
	})
	if err != nil {
		t.Fatalf("UpsertParticipant: %v", err)
	}
	_, err = h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "idem-stranger", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: stranger.ID, Body: "hi",
	})
	if !errors.Is(err, ErrNotMember) {
		t.Fatalf("AcceptExchange to a non-member = %v, want ErrNotMember", err)
	}
}

func TestExchangeFreezesTheLiveBindingAtAcceptTime(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	binding, err := h.channels.Rebind(ctx, channelID, agentID, channel.RebindParams{
		RuntimeAuthority: "claude-code-cli", RuntimeEndpointRef: "session-alpha",
	})
	if err != nil {
		t.Fatalf("Rebind: %v", err)
	}

	exchange, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "idem-bound", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "hello session-alpha",
	})
	if err != nil {
		t.Fatalf("AcceptExchange: %v", err)
	}
	if exchange.RecipientBindingID != binding.ID {
		t.Fatalf("RecipientBindingID = %q, want the live binding %q frozen at accept time", exchange.RecipientBindingID, binding.ID)
	}

	current, err := h.relay.RecipientBindingCurrent(ctx, exchange.ID)
	if err != nil {
		t.Fatalf("RecipientBindingCurrent: %v", err)
	}
	if !current {
		t.Fatal("RecipientBindingCurrent = false immediately after accept, want true")
	}
}

func TestRecipientBindingCurrentDetectsAStaleBinding(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	if _, err := h.channels.Rebind(ctx, channelID, agentID, channel.RebindParams{
		RuntimeAuthority: "claude-code-cli", RuntimeEndpointRef: "session-alpha",
	}); err != nil {
		t.Fatalf("Rebind (alpha): %v", err)
	}
	exchange, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "idem-stale", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "sent to session-alpha",
	})
	if err != nil {
		t.Fatalf("AcceptExchange: %v", err)
	}

	// The recipient rebinds — a new session attached — after the exchange
	// was accepted. The exchange's frozen snapshot now names a superseded
	// generation: exactly the signal a delivery worker needs before it
	// routes to a destination this exchange was never addressed to.
	_, err = h.channels.Rebind(ctx, channelID, agentID, channel.RebindParams{
		RuntimeAuthority: "claude-code-cli", RuntimeEndpointRef: "session-beta",
	})
	if err != nil {
		t.Fatalf("Rebind (beta): %v", err)
	}

	current, err := h.relay.RecipientBindingCurrent(ctx, exchange.ID)
	if err != nil {
		t.Fatalf("RecipientBindingCurrent: %v", err)
	}
	if current {
		t.Fatal("RecipientBindingCurrent = true after a rebind superseded the frozen binding, want false")
	}

	// A fresh exchange accepted after the rebind correctly freezes the new
	// generation and reads as current.
	fresh, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "idem-fresh", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "sent to session-beta",
	})
	if err != nil {
		t.Fatalf("AcceptExchange (fresh): %v", err)
	}
	freshCurrent, err := h.relay.RecipientBindingCurrent(ctx, fresh.ID)
	if err != nil {
		t.Fatalf("RecipientBindingCurrent (fresh): %v", err)
	}
	if !freshCurrent {
		t.Fatal("a freshly accepted exchange after the rebind reads as stale, want current")
	}
}

func TestSequenceIsTheReplayCursor(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	var accepted []Exchange
	for i, key := range []string{"idem-a", "idem-b", "idem-c"} {
		exchange, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
			IdempotencyKey: key, ChannelID: channelID,
			SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: key,
		})
		if err != nil {
			t.Fatalf("AcceptExchange %d: %v", i, err)
		}
		if exchange.Sequence != int64(i+1) {
			t.Fatalf("exchange %d sequence = %d, want %d", i, exchange.Sequence, i+1)
		}
		accepted = append(accepted, exchange)
	}

	all, err := h.relay.ListForDestination(ctx, channelID, agentID, 0, 50, false)
	if err != nil {
		t.Fatalf("ListForDestination(cursor=0): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListForDestination(cursor=0) returned %d, want 3", len(all))
	}

	resumed, err := h.relay.ListForDestination(ctx, channelID, agentID, accepted[0].Sequence, 50, false)
	if err != nil {
		t.Fatalf("ListForDestination(cursor=%d): %v", accepted[0].Sequence, err)
	}
	if len(resumed) != 2 || resumed[0].ID != accepted[1].ID || resumed[1].ID != accepted[2].ID {
		t.Fatalf("ListForDestination resumed from cursor = %+v, want exchanges b and c in order", resumed)
	}
}

// TestUnackedOnlyExcludesAlreadyAckedExchangesRegardlessOfCursor is
// CW-20260906-0072's fix for a relaunched, cursor-less session: cursor=0
// alone re-reads a destination's entire history on every check-in, with no
// way to tell "new to me" from "I acked this yesterday" — a non-issue under
// continuous standby (the cursor lived in memory for the process's whole
// life) but the central ergonomic and cost fact of a durable check-in
// model, where a fresh process with no cursor is the normal case.
// unacked_only turns cursor=0 into "give me exactly what still needs
// handling", scaling with backlog rather than with total history.
func TestUnackedOnlyExcludesAlreadyAckedExchangesRegardlessOfCursor(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	var accepted []Exchange
	for _, key := range []string{"idem-a", "idem-b", "idem-c"} {
		exchange, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
			IdempotencyKey: key, ChannelID: channelID,
			SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: key,
		})
		if err != nil {
			t.Fatalf("AcceptExchange %s: %v", key, err)
		}
		accepted = append(accepted, exchange)
	}
	// Ack the middle one out of order, the way a real recipient might
	// process an out-of-order backlog.
	if _, err := h.relay.RecordRead(ctx, accepted[1].ID, agentID); err != nil {
		t.Fatalf("RecordRead: %v", err)
	}

	// A relaunched session with no cursor: cursor=0, unacked_only=true.
	unacked, err := h.relay.ListForDestination(ctx, channelID, agentID, 0, 50, true)
	if err != nil {
		t.Fatalf("ListForDestination(unacked_only=true): %v", err)
	}
	if len(unacked) != 2 || unacked[0].ID != accepted[0].ID || unacked[1].ID != accepted[2].ID {
		t.Fatalf("ListForDestination(unacked_only=true) = %+v, want exchanges a and c, not the acked one", unacked)
	}

	// The same cursor=0 without the filter still returns all three — the
	// filter is additive, not a replacement for the existing contract.
	everything, err := h.relay.ListForDestination(ctx, channelID, agentID, 0, 50, false)
	if err != nil {
		t.Fatalf("ListForDestination(unacked_only=false): %v", err)
	}
	if len(everything) != 3 {
		t.Fatalf("ListForDestination(unacked_only=false) = %+v, want all 3", everything)
	}

	// Ack the rest; a subsequent unacked_only call finds nothing left.
	_, err = h.relay.RecordRead(ctx, accepted[0].ID, agentID)
	if err != nil {
		t.Fatalf("RecordRead: %v", err)
	}
	_, err = h.relay.RecordRead(ctx, accepted[2].ID, agentID)
	if err != nil {
		t.Fatalf("RecordRead: %v", err)
	}
	drained, err := h.relay.ListForDestination(ctx, channelID, agentID, 0, 50, true)
	if err != nil {
		t.Fatalf("ListForDestination(unacked_only=true, drained): %v", err)
	}
	if len(drained) != 0 {
		t.Fatalf("ListForDestination(unacked_only=true) after acking everything = %+v, want none", drained)
	}
}

func TestReceiveWithUnackedOnlySkipsAlreadyAckedItems(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	first, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "idem-first", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "first",
	})
	if err != nil {
		t.Fatalf("AcceptExchange (first): %v", err)
	}
	_, err = h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "idem-second", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "second",
	})
	if err != nil {
		t.Fatalf("AcceptExchange (second): %v", err)
	}
	_, err = h.relay.RecordRead(ctx, first.ID, agentID)
	if err != nil {
		t.Fatalf("RecordRead: %v", err)
	}

	// A relaunched session's check-in: cursor 0 (it has no memory of one),
	// unacked_only true.
	result, err := h.relay.Receive(ctx, ReceiveParams{
		ChannelID: channelID, ParticipantID: agentID, Cursor: 0, UnackedOnly: true,
	})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Body != "second" {
		t.Fatalf("Receive(unacked_only=true) = %+v, want only the unacked \"second\" exchange", result.Items)
	}
}

func TestClaimNextForDeliveryReclaimsAnExpiredLease(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	exchange, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "idem-crash", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "will crash mid-delivery",
	})
	if err != nil {
		t.Fatalf("AcceptExchange: %v", err)
	}

	first, err := h.relay.ClaimNextForDelivery(ctx, "worker-a", time.Hour, 10)
	if err != nil {
		t.Fatalf("ClaimNextForDelivery (worker-a): %v", err)
	}
	if len(first) != 1 || first[0].ExchangeID != exchange.ID || first[0].Status != OutboxLeased {
		t.Fatalf("first claim = %+v, want exactly one leased item", first)
	}

	// worker-a crashes: it never calls RecordDeliveryOutcome. A second
	// worker claiming immediately gets nothing — the lease has not expired.
	second, err := h.relay.ClaimNextForDelivery(ctx, "worker-b", time.Hour, 10)
	if err != nil {
		t.Fatalf("ClaimNextForDelivery (worker-b, live lease): %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("worker-b claimed %d items while worker-a's lease is still live, want 0", len(second))
	}

	// Backdate the lease directly, simulating the passage of time past its
	// expiry rather than sleeping in the test.
	_, err = h.db.ExecContext(ctx,
		`UPDATE exchange_outbox SET lease_expires_at = ? WHERE exchange_id = ?`,
		time.Now().UTC().Add(-time.Minute), exchange.ID,
	)
	if err != nil {
		t.Fatalf("backdate lease: %v", err)
	}

	reclaimed, err := h.relay.ClaimNextForDelivery(ctx, "worker-c", time.Hour, 10)
	if err != nil {
		t.Fatalf("ClaimNextForDelivery (worker-c, expired lease): %v", err)
	}
	if len(reclaimed) != 1 || reclaimed[0].ExchangeID != exchange.ID || reclaimed[0].LeasedBy != "worker-c" {
		t.Fatalf("reclaimed = %+v, want worker-c to reclaim the crashed worker's item", reclaimed)
	}
	if reclaimed[0].Attempts != 2 {
		t.Fatalf("attempts after reclaim = %d, want 2 (one per claim)", reclaimed[0].Attempts)
	}
}

func TestClaimNextForDeliveryNeverDoubleClaimsTheSameItem(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	exchange, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "idem-race", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "one item, two workers",
	})
	if err != nil {
		t.Fatalf("AcceptExchange: %v", err)
	}

	const workers = 8
	results := make(chan []OutboxItem, workers)
	for i := range workers {
		go func(i int) {
			claimed, claimErr := h.relay.ClaimNextForDelivery(ctx, fmt.Sprintf("worker-%d", i), time.Hour, 10)
			if claimErr != nil {
				t.Errorf("worker %d: ClaimNextForDelivery: %v", i, claimErr)
			}
			results <- claimed
		}(i)
	}
	totalClaimed := 0
	for range workers {
		totalClaimed += len(<-results)
	}
	if totalClaimed != 1 {
		t.Fatalf("total items claimed across %d concurrent workers = %d, want exactly 1", workers, totalClaimed)
	}

	item, err := h.relay.GetOutboxItem(ctx, exchange.ID)
	if err != nil {
		t.Fatalf("GetOutboxItem: %v", err)
	}
	if item.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1 — a double claim would have incremented it twice", item.Attempts)
	}
}

func TestRecordDeliveryOutcomeUncertainReturnsToPendingAutomatically(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	exchange, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "idem-uncertain", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "timeout on the way out",
	})
	if err != nil {
		t.Fatalf("AcceptExchange: %v", err)
	}
	_, err = h.relay.ClaimNextForDelivery(ctx, "worker-a", time.Hour, 10)
	if err != nil {
		t.Fatalf("ClaimNextForDelivery: %v", err)
	}

	receipt, err := h.relay.RecordDeliveryOutcome(ctx, exchange.ID, RecordDeliveryOutcomeParams{
		Outcome: DeliveryUncertain, ErrorMessage: "read timeout, no ack from the transport",
	})
	if err != nil {
		t.Fatalf("RecordDeliveryOutcome: %v", err)
	}
	if receipt.Outcome != DeliveryUncertain || receipt.AttemptNumber != 1 {
		t.Fatalf("receipt = %+v, want uncertain attempt 1", receipt)
	}

	item, err := h.relay.GetOutboxItem(ctx, exchange.ID)
	if err != nil {
		t.Fatalf("GetOutboxItem: %v", err)
	}
	if item.Status != OutboxPending {
		t.Fatalf("outbox status after an uncertain outcome = %q, want pending (auto-retryable)", item.Status)
	}

	// It really is retryable: a subsequent claim picks it back up.
	reclaimed, err := h.relay.ClaimNextForDelivery(ctx, "worker-b", time.Hour, 10)
	if err != nil {
		t.Fatalf("ClaimNextForDelivery (retry): %v", err)
	}
	if len(reclaimed) != 1 || reclaimed[0].ExchangeID != exchange.ID {
		t.Fatalf("retry claim = %+v, want the uncertain item back", reclaimed)
	}
}

func TestRequeueNeverRetargetsAFailedDelivery(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	exchange, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "idem-failed", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "the destination refused it",
	})
	if err != nil {
		t.Fatalf("AcceptExchange: %v", err)
	}
	_, err = h.relay.ClaimNextForDelivery(ctx, "worker-a", time.Hour, 10)
	if err != nil {
		t.Fatalf("ClaimNextForDelivery: %v", err)
	}
	_, err = h.relay.RecordDeliveryOutcome(ctx, exchange.ID, RecordDeliveryOutcomeParams{
		Outcome: DeliveryFailed, ErrorCode: "unauthorized", ErrorMessage: "destination refused",
	})
	if err != nil {
		t.Fatalf("RecordDeliveryOutcome: %v", err)
	}

	item, err := h.relay.GetOutboxItem(ctx, exchange.ID)
	if err != nil {
		t.Fatalf("GetOutboxItem: %v", err)
	}
	if item.Status != OutboxFailed {
		t.Fatalf("status after a definite failure = %q, want failed (terminal until an explicit requeue)", item.Status)
	}

	// A failed item is not silently retried by ClaimNextForDelivery.
	claimed, err := h.relay.ClaimNextForDelivery(ctx, "worker-b", time.Hour, 10)
	if err != nil {
		t.Fatalf("ClaimNextForDelivery: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("ClaimNextForDelivery claimed a failed item without an explicit Requeue: %+v", claimed)
	}

	requeued, err := h.relay.Requeue(ctx, exchange.ID)
	if err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	if requeued.Status != OutboxPending {
		t.Fatalf("status after Requeue = %q, want pending", requeued.Status)
	}

	// The retry never retargeted: same sender, recipient, and body as the
	// original — Requeue has no parameter that could have changed them,
	// and the exchange row itself is immutable.
	afterRequeue, err := h.relay.GetExchange(ctx, exchange.ID)
	if err != nil {
		t.Fatalf("GetExchange: %v", err)
	}
	if afterRequeue.SenderParticipantID != exchange.SenderParticipantID ||
		afterRequeue.RecipientParticipantID != exchange.RecipientParticipantID ||
		afterRequeue.Body != exchange.Body {
		t.Fatalf("exchange changed shape across a requeue: before=%+v after=%+v", exchange, afterRequeue)
	}

	reclaimed, err := h.relay.ClaimNextForDelivery(ctx, "worker-c", time.Hour, 10)
	if err != nil {
		t.Fatalf("ClaimNextForDelivery (after requeue): %v", err)
	}
	if len(reclaimed) != 1 || reclaimed[0].ExchangeID != exchange.ID {
		t.Fatalf("claim after requeue = %+v, want the requeued item", reclaimed)
	}
}

func TestRecordReadIsIdempotentAndDistinctFromReceive(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	exchange, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "idem-read", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "did you see this",
	})
	if err != nil {
		t.Fatalf("AcceptExchange: %v", err)
	}

	// Listing (receiving) the exchange does not itself create a read fact.
	_, err = h.relay.ListForDestination(ctx, channelID, agentID, 0, 50, false)
	if err != nil {
		t.Fatalf("ListForDestination: %v", err)
	}
	_, err = h.relay.GetRead(ctx, exchange.ID, agentID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetRead before any ack = %v, want ErrNotFound: retrieval is not acknowledgement", err)
	}

	first, err := h.relay.RecordRead(ctx, exchange.ID, agentID)
	if err != nil {
		t.Fatalf("RecordRead: %v", err)
	}
	second, err := h.relay.RecordRead(ctx, exchange.ID, agentID)
	if err != nil {
		t.Fatalf("RecordRead (again): %v", err)
	}
	if !first.AckedAt.Equal(second.AckedAt) {
		t.Fatalf("acking twice changed the recorded time: %v != %v", first.AckedAt, second.AckedAt)
	}

	var rowCount int
	if err := h.db.QueryRow(
		`SELECT COUNT(*) FROM exchange_reads WHERE exchange_id = ? AND participant_id = ?`, exchange.ID, agentID,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count read rows: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("exchange_reads rows for one ack repeated twice = %d, want 1", rowCount)
	}
}

func TestOutboxSurvivesAProcessRestart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "restart.db")
	h := openTestHarnessAt(t, path)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	exchange, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "idem-restart", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "still here after restart",
	})
	if err != nil {
		t.Fatalf("AcceptExchange: %v", err)
	}
	_, err = h.relay.ClaimNextForDelivery(ctx, "worker-before-restart", time.Millisecond, 10)
	if err != nil {
		t.Fatalf("ClaimNextForDelivery: %v", err)
	}
	// Simulate the crash boundary the lease exists to survive: the lease is
	// already expired by the time the process comes back.
	time.Sleep(5 * time.Millisecond)

	err = h.db.Close()
	if err != nil {
		t.Fatalf("close database (simulating restart): %v", err)
	}

	// A fresh Store over the same file, the way a restarted process boots.
	restarted := openTestHarnessAt(t, path)

	loaded, err := restarted.relay.GetExchange(ctx, exchange.ID)
	if err != nil {
		t.Fatalf("GetExchange after restart: %v", err)
	}
	if loaded.Body != exchange.Body {
		t.Fatalf("exchange body after restart = %q, want %q", loaded.Body, exchange.Body)
	}

	claimed, err := restarted.relay.ClaimNextForDelivery(ctx, "worker-after-restart", time.Hour, 10)
	if err != nil {
		t.Fatalf("ClaimNextForDelivery after restart: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ExchangeID != exchange.ID {
		t.Fatalf("claim after restart = %+v, want the pre-restart item, nothing lost or stuck", claimed)
	}
}

// TestListForDestinationNeverTouchesPresenceOrCursor is the test tangent-14
// asked for by name in the CW-20260906-0066 design review: acceptance item
// 2's "UI reads do not consume agent inbox messages" holds trivially today
// because nothing about ListForDestination consumes anything — which is
// exactly why that property needs to be pinned rather than left to hold by
// accident. If presence-touching is ever moved down into the store to save
// a call, this goes red.
func TestListForDestinationNeverTouchesPresenceOrCursor(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	exchange, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "idem-ui-read", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "a UI would show this too",
	})
	if err != nil {
		t.Fatalf("AcceptExchange: %v", err)
	}

	before, err := h.relay.Presence(ctx, agentID)
	if err != nil {
		t.Fatalf("Presence (before): %v", err)
	}
	if before.Open || before.LastSeenAt != nil {
		t.Fatalf("presence before any UI-shaped read = %+v, want closed and never seen", before)
	}

	// A UI-shaped read: the same method a future channel-pane REST endpoint
	// would call to show history, several times over, as a UI polling for
	// display would.
	for i := range 3 {
		items, listErr := h.relay.ListForDestination(ctx, channelID, agentID, 0, 50, false)
		if listErr != nil {
			t.Fatalf("ListForDestination (%d): %v", i, listErr)
		}
		if len(items) != 1 || items[0].ID != exchange.ID {
			t.Fatalf("ListForDestination (%d) = %+v, want the one exchange, unconsumed", i, items)
		}
	}

	afterReads, err := h.relay.Presence(ctx, agentID)
	if err != nil {
		t.Fatalf("Presence (after UI reads): %v", err)
	}
	if afterReads.Open || afterReads.LastSeenAt != nil {
		t.Fatalf("presence after UI-shaped reads = %+v, want still closed and never seen — a UI read must not touch presence", afterReads)
	}
	_, err = h.relay.GetRead(ctx, exchange.ID, agentID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetRead after UI-shaped reads = %v, want ErrNotFound — a UI read must not consume", err)
	}

	// Receive, by contrast, does touch presence — proving the distinction is
	// real, not that Presence itself is inert.
	_, err = h.relay.Receive(ctx, ReceiveParams{ChannelID: channelID, ParticipantID: agentID})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	afterReceive, err := h.relay.Presence(ctx, agentID)
	if err != nil {
		t.Fatalf("Presence (after Receive): %v", err)
	}
	if afterReceive.LastSeenAt == nil {
		t.Fatal("presence after an actual Receive call still shows never seen")
	}
}

func TestReceiveReturnsNewItemsAndAdvancesTheCursor(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	exchange, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "idem-receive", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "hello agent",
	})
	if err != nil {
		t.Fatalf("AcceptExchange: %v", err)
	}

	result, err := h.relay.Receive(ctx, ReceiveParams{ChannelID: channelID, ParticipantID: agentID})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if result.TimedOut {
		t.Fatal("Receive with an item already waiting reported a timeout")
	}
	if len(result.Items) != 1 || result.Items[0].ID != exchange.ID {
		t.Fatalf("Receive.Items = %+v, want the one exchange", result.Items)
	}
	if result.NextCursor != exchange.Sequence {
		t.Fatalf("Receive.NextCursor = %d, want %d", result.NextCursor, exchange.Sequence)
	}
	if result.Presence.LastSeenAt == nil {
		t.Fatal("Presence.LastSeenAt is nil after a completed Receive")
	}

	// Resuming from the returned cursor sees nothing new: no replay gap and
	// no duplicate delivery of the same item.
	resumed, err := h.relay.Receive(ctx, ReceiveParams{ChannelID: channelID, ParticipantID: agentID, Cursor: result.NextCursor})
	if err != nil {
		t.Fatalf("Receive (resumed): %v", err)
	}
	if len(resumed.Items) != 0 || resumed.NextCursor != result.NextCursor {
		t.Fatalf("Receive (resumed) = %+v, want no items and the same cursor", resumed)
	}
}

func TestReceiveTimesOutWithoutErrorOrSideEffectOnTheOutbox(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, _, agentID := h.seedChannel(t)

	result, err := h.relay.Receive(ctx, ReceiveParams{
		ChannelID: channelID, ParticipantID: agentID, Wait: 150 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if !result.TimedOut {
		t.Fatal("Receive with nothing to deliver did not report a timeout")
	}
	if len(result.Items) != 0 || result.NextCursor != 0 {
		t.Fatalf("Receive (timeout) = %+v, want no items and the cursor unchanged", result)
	}
	// A timeout is not an error and cancels no outstanding delivery work —
	// there was none to cancel, and this is the check that stays true even
	// if a future change adds some.
}

func TestReceiveWakesBeforeItsWaitElapsesWhenAMessageArrives(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	go func() {
		time.Sleep(150 * time.Millisecond)
		if _, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
			IdempotencyKey: "idem-late-arrival", ChannelID: channelID,
			SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "sorry for the delay",
		}); err != nil {
			t.Errorf("AcceptExchange (background): %v", err)
		}
	}()

	started := time.Now()
	result, err := h.relay.Receive(ctx, ReceiveParams{
		ChannelID: channelID, ParticipantID: agentID, Wait: 5 * time.Second,
	})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if result.TimedOut || len(result.Items) != 1 {
		t.Fatalf("Receive = %+v, want the item that arrived mid-wait, not a timeout", result)
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("Receive took the full wait (%s) instead of waking up when the message arrived", elapsed)
	}
}

func TestReceiveRejectsAWaitOutsideZeroToMaximum(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, _, agentID := h.seedChannel(t)

	if _, err := h.relay.Receive(ctx, ReceiveParams{
		ChannelID: channelID, ParticipantID: agentID, Wait: MaximumReceiveWait + time.Second,
	}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("Receive with wait over the maximum = %v, want ErrInvalidRecord", err)
	}
	if _, err := h.relay.Receive(ctx, ReceiveParams{
		ChannelID: channelID, ParticipantID: agentID, Wait: -time.Second,
	}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("Receive with a negative wait = %v, want ErrInvalidRecord", err)
	}
}

// TestListForChannelReturnsBothDirectionsNewestFirst is CW-20260907-0017's
// whole-channel history read: an operator-sent and an agent-sent exchange
// in the same channel, both surfaced in one call, ordered newest first —
// the shape ListForDestination cannot produce, since its ordering
// (sequence) is monotonic per recipient only.
func TestListForChannelReturnsBothDirectionsNewestFirst(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	toAgent, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "op-to-agent", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "from the operator",
	})
	if err != nil {
		t.Fatalf("AcceptExchange (operator to agent): %v", err)
	}
	toOperator, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "agent-to-op", ChannelID: channelID,
		SenderParticipantID: agentID, RecipientParticipantID: operatorID, Body: "from the agent",
	})
	if err != nil {
		t.Fatalf("AcceptExchange (agent to operator): %v", err)
	}

	items, err := h.relay.ListForChannel(ctx, channelID, 0)
	if err != nil {
		t.Fatalf("ListForChannel: %v", err)
	}
	if len(items) != 2 || items[0].ID != toOperator.ID || items[1].ID != toAgent.ID {
		t.Fatalf("ListForChannel = %+v, want [toOperator, toAgent] newest first", items)
	}
}

// TestRedactionPreservesListOrderingAndTheReplayCursor is CW-20260907-0043's
// replay-cursor case: redacting an exchange must not disturb ListForChannel's
// (created_at, id) order or ListForDestination's sequence-based cursor —
// both read the row exactly as stored, and a tombstone is just a different
// string in the same body column.
func TestRedactionPreservesListOrderingAndTheReplayCursor(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	first, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "idem-first", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "message one",
	})
	if err != nil {
		t.Fatalf("AcceptExchange (first): %v", err)
	}
	second, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "idem-second", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "message two",
	})
	if err != nil {
		t.Fatalf("AcceptExchange (second): %v", err)
	}

	if _, redactErr := tangentdb.RedactExchange(ctx, h.db, tangentdb.RetentionRequest{
		ExchangeID: first.ID, ActorRef: "operator:test", Authority: tangentdb.AuthorityLocalUser,
	}); redactErr != nil {
		t.Fatalf("RedactExchange: %v", redactErr)
	}

	channelItems, err := h.relay.ListForChannel(ctx, channelID, 0)
	if err != nil {
		t.Fatalf("ListForChannel: %v", err)
	}
	if len(channelItems) != 2 || channelItems[0].ID != second.ID || channelItems[1].ID != first.ID {
		t.Fatalf("ListForChannel after redacting the older item = %+v, want [second, first] newest first",
			channelItems)
	}
	if channelItems[1].BodyRedacted() != true || channelItems[0].BodyRedacted() {
		t.Fatalf("BodyRedacted did not distinguish the redacted item from the untouched one: %+v", channelItems)
	}

	destinationItems, err := h.relay.ListForDestination(ctx, channelID, agentID, 0, 0, false)
	if err != nil {
		t.Fatalf("ListForDestination: %v", err)
	}
	if len(destinationItems) != 2 || destinationItems[0].Sequence != 1 || destinationItems[1].Sequence != 2 {
		t.Fatalf("ListForDestination after redacting sequence 1 = %+v, want sequence 1 then 2 — "+
			"the replay cursor must still resume from the same sequence numbers", destinationItems)
	}
	if !destinationItems[0].BodyRedacted() {
		t.Fatal("ListForDestination lost the redacted item's tombstone")
	}
}

// TestListForChannelNeverTouchesPresenceOrCursor pins the same pure-read
// guarantee ListForDestination carries (TestListForDestinationNeverTouchesPresenceOrCursor):
// a channel pane's whole-history view must never be confused with the
// agent's consuming, ack-driven receive path.
func TestListForChannelNeverTouchesPresenceOrCursor(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t)

	exchange, err := h.relay.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: "idem-pane-read", ChannelID: channelID,
		SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "a pane would show this too",
	})
	if err != nil {
		t.Fatalf("AcceptExchange: %v", err)
	}

	for i := range 3 {
		items, listErr := h.relay.ListForChannel(ctx, channelID, 0)
		if listErr != nil {
			t.Fatalf("ListForChannel (%d): %v", i, listErr)
		}
		if len(items) != 1 || items[0].ID != exchange.ID {
			t.Fatalf("ListForChannel (%d) = %+v, want the one exchange, unconsumed", i, items)
		}
	}

	presence, err := h.relay.Presence(ctx, agentID)
	if err != nil {
		t.Fatalf("Presence: %v", err)
	}
	if presence.Open || presence.LastSeenAt != nil {
		t.Fatalf("presence after ListForChannel reads = %+v, want still closed and never seen", presence)
	}
	if _, err := h.relay.GetRead(ctx, exchange.ID, agentID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetRead after ListForChannel reads = %v, want ErrNotFound — a pane read must not consume", err)
	}
}
