package channelpane

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/channel"
	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/relay"
)

type testHarness struct {
	db       *sql.DB
	channels *channel.Store
	relay    *relay.Store
}

func openTestHarness(t *testing.T) *testHarness {
	t.Helper()
	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "channelpane.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	err = tangentdb.RunMigrations(database)
	if err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	channels, err := channel.NewStore(database)
	if err != nil {
		t.Fatalf("channel.NewStore: %v", err)
	}
	relayStore, err := relay.NewStore(database, channels)
	if err != nil {
		t.Fatalf("relay.NewStore: %v", err)
	}
	return &testHarness{db: database, channels: channels, relay: relayStore}
}

// seedChannel opens a channel and attaches one agent participant, returning
// the channel id, the (global, stable) operator participant id, and the
// agent's id.
func (h *testHarness) seedChannel(t *testing.T, applicationID, agentRef string) (channelID, operatorID, agentID string) {
	t.Helper()
	ctx := context.Background()
	ch, operator, err := h.channels.OpenChannel(ctx, channel.CreateChannelParams{OwnerScope: "standalone-local:anonymous"})
	if err != nil {
		t.Fatalf("OpenChannel: %v", err)
	}
	agent, err := h.channels.UpsertParticipant(ctx, channel.UpsertParticipantParams{
		Kind: channel.ParticipantAgent, ExternalAuthority: applicationID, ExternalRef: agentRef,
	})
	if err != nil {
		t.Fatalf("UpsertParticipant (agent): %v", err)
	}
	if _, err := h.channels.AddParticipant(ctx, ch.ID, agent.ID); err != nil {
		t.Fatalf("AddParticipant (agent): %v", err)
	}
	return ch.ID, operator.ID, agent.ID
}

type fakeHITLInbox struct {
	inbox hitl.OperatorInbox
	err   error
}

func (f *fakeHITLInbox) Inbox(context.Context) (hitl.OperatorInbox, error) {
	return f.inbox, f.err
}

func pendingHITLItem(t *testing.T, itemID, applicationID, agentID string) hitl.OperatorItemView {
	t.Helper()
	snapshot := []byte(`{"source":{"application_id":"` + applicationID + `","agent_id":"` + agentID + `"}}`)
	return hitl.OperatorItemView{
		ItemView: hitl.ItemView{
			ItemID: itemID, State: interaction.InteractionStatePresented,
			RequestSnapshot: snapshot, EnqueuedAt: time.Now().UTC(),
		},
	}
}

func TestListChannelsComputesUnreadNeedsInputAndPreview(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t, "claude-code", "agent-a")

	if _, err := h.relay.AcceptExchange(ctx, relay.AcceptExchangeParams{
		IdempotencyKey: "a1", ChannelID: channelID, SenderParticipantID: agentID, RecipientParticipantID: operatorID, Body: "first",
	}); err != nil {
		t.Fatalf("AcceptExchange 1: %v", err)
	}
	if _, err := h.relay.AcceptExchange(ctx, relay.AcceptExchangeParams{
		IdempotencyKey: "a2", ChannelID: channelID, SenderParticipantID: agentID, RecipientParticipantID: operatorID, Body: "second, most recent",
	}); err != nil {
		t.Fatalf("AcceptExchange 2: %v", err)
	}

	fakeHITL := &fakeHITLInbox{inbox: hitl.OperatorInbox{
		Pending: []hitl.OperatorItemView{pendingHITLItem(t, "item-1", "claude-code", "agent-a")},
	}}
	svc, err := New(h.channels, h.relay, fakeHITL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	summaries, err := svc.ListChannels(ctx)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("ListChannels = %+v, want exactly one channel", summaries)
	}
	got := summaries[0]
	if got.ChannelID != channelID || got.UnreadCount != 2 || got.NeedsInputCount != 1 || got.LastMessagePreview != "second, most recent" {
		t.Fatalf("ListChannels[0] = %+v, want unread=2 needs_input=1 preview=%q", got, "second, most recent")
	}
}

func TestGetChannelReportsAwaitingPeerThenAcceptedByPeer(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t, "claude-code", "agent-a")

	exchange, err := h.relay.AcceptExchange(ctx, relay.AcceptExchangeParams{
		IdempotencyKey: "op1", ChannelID: channelID, SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "from operator",
	})
	if err != nil {
		t.Fatalf("AcceptExchange: %v", err)
	}

	svc, err := New(h.channels, h.relay, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	detail, err := svc.GetChannel(ctx, channelID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if len(detail.Messages) != 1 || detail.Messages[0].DeliveryState != "awaiting-peer" {
		t.Fatalf("GetChannel before ack = %+v, want one message with delivery_state awaiting-peer (agent idle, unacked)", detail.Messages)
	}

	_, err = h.relay.RecordRead(ctx, exchange.ID, agentID)
	if err != nil {
		t.Fatalf("RecordRead: %v", err)
	}
	detail, err = svc.GetChannel(ctx, channelID)
	if err != nil {
		t.Fatalf("GetChannel (after ack): %v", err)
	}
	if detail.Messages[0].DeliveryState != "accepted-by-peer" {
		t.Fatalf("GetChannel after ack = %+v, want delivery_state accepted-by-peer", detail.Messages[0])
	}
}

// TestGetChannelReportsQueuedWhilePeerIsActivelyReceiving pins the
// corrected mapping: "queued" means the peer's own receive is open right
// now (delivery imminent), not "presence closed" — the inverse of an
// earlier, corrected reading of this task's design notes.
func TestGetChannelReportsQueuedWhilePeerIsActivelyReceiving(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t, "claude-code", "agent-a")

	svc, err := New(h.channels, h.relay, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	receiveDone := make(chan struct{})
	go func() {
		defer close(receiveDone)
		_, _ = h.relay.Receive(ctx, relay.ReceiveParams{ChannelID: channelID, ParticipantID: agentID, Wait: 300 * time.Millisecond})
	}()
	// Give the goroutine's Receive call time to open before sending — this
	// is the same "confirmed still open" style CW-20260906-0071's live proof
	// used, adapted to a unit test's shorter, deterministic window.
	deadline := time.Now().Add(2 * time.Second)
	for {
		presence, presErr := h.relay.Presence(ctx, agentID)
		if presErr != nil {
			t.Fatalf("Presence (waiting for open): %v", presErr)
		}
		if presence.Open {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent's Receive never opened within the deadline")
		}
		time.Sleep(2 * time.Millisecond)
	}

	_, err = h.relay.AcceptExchange(ctx, relay.AcceptExchangeParams{
		IdempotencyKey: "op1", ChannelID: channelID, SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "from operator",
	})
	if err != nil {
		t.Fatalf("AcceptExchange: %v", err)
	}

	detail, err := svc.GetChannel(ctx, channelID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if len(detail.Messages) != 1 || detail.Messages[0].DeliveryState != "queued" {
		t.Fatalf("GetChannel while the agent's receive is open = %+v, want delivery_state queued", detail.Messages)
	}
	<-receiveDone
}

func TestSendMessageDefaultsToTheOneLiveAgentAndRefusesAmbiguity(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, _, _ := h.seedChannel(t, "claude-code", "agent-a")

	svc, err := New(h.channels, h.relay, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	sent, err := svc.SendMessage(ctx, channelID, SendInput{Body: "hello"})
	if err != nil {
		t.Fatalf("SendMessage (one agent, default recipient): %v", err)
	}
	if sent.Direction != "operator" || sent.Body != "hello" {
		t.Fatalf("SendMessage = %+v", sent)
	}

	secondAgent, err := h.channels.UpsertParticipant(ctx, channel.UpsertParticipantParams{
		Kind: channel.ParticipantAgent, ExternalAuthority: "claude-code", ExternalRef: "agent-b",
	})
	if err != nil {
		t.Fatalf("UpsertParticipant (second agent): %v", err)
	}
	_, err = h.channels.AddParticipant(ctx, channelID, secondAgent.ID)
	if err != nil {
		t.Fatalf("AddParticipant (second agent): %v", err)
	}

	_, err = svc.SendMessage(ctx, channelID, SendInput{Body: "hello again"})
	var ambiguous *AmbiguousRecipientError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("SendMessage with two live agents and no explicit recipient = %v, want AmbiguousRecipientError", err)
	}

	_, err = svc.SendMessage(ctx, channelID, SendInput{Body: "to B specifically", RecipientApplicationID: "claude-code", RecipientAgentID: "agent-b"})
	if err != nil {
		t.Fatalf("SendMessage with an explicit recipient: %v", err)
	}
}

func TestMarkReadAcksEveryUnreadMessageExactlyOnce(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t, "claude-code", "agent-a")

	for _, key := range []string{"m1", "m2"} {
		if _, err := h.relay.AcceptExchange(ctx, relay.AcceptExchangeParams{
			IdempotencyKey: key, ChannelID: channelID, SenderParticipantID: agentID, RecipientParticipantID: operatorID, Body: key,
		}); err != nil {
			t.Fatalf("AcceptExchange %s: %v", key, err)
		}
	}

	svc, err := New(h.channels, h.relay, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	count, err := svc.MarkRead(ctx, channelID)
	if err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if count != 2 {
		t.Fatalf("MarkRead = %d, want 2", count)
	}

	again, err := svc.MarkRead(ctx, channelID)
	if err != nil {
		t.Fatalf("MarkRead (again): %v", err)
	}
	if again != 0 {
		t.Fatalf("MarkRead (again) = %d, want 0 — already acked", again)
	}
}

// TestChannelPaneReadsNeverTouchPresenceOrConsume is a channelpane-level
// pin of the same guarantee internal/relay's own tests hold at the store
// layer (TestListForDestinationNeverTouchesPresenceOrCursor,
// TestListForChannelNeverTouchesPresenceOrCursor): a future edit to this
// package must not be able to introduce a consuming read (relay.Receive)
// without a test noticing here, one layer up from the store's own guard.
func TestChannelPaneReadsNeverTouchPresenceOrConsume(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, operatorID, agentID := h.seedChannel(t, "claude-code", "agent-a")
	exchange, err := h.relay.AcceptExchange(ctx, relay.AcceptExchangeParams{
		IdempotencyKey: "m1", ChannelID: channelID, SenderParticipantID: operatorID, RecipientParticipantID: agentID, Body: "hi",
	})
	if err != nil {
		t.Fatalf("AcceptExchange: %v", err)
	}

	svc, err := New(h.channels, h.relay, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 3; i++ {
		_, err = svc.ListChannels(ctx)
		if err != nil {
			t.Fatalf("ListChannels (%d): %v", i, err)
		}
		_, err = svc.GetChannel(ctx, channelID)
		if err != nil {
			t.Fatalf("GetChannel (%d): %v", i, err)
		}
	}

	presence, err := h.relay.Presence(ctx, agentID)
	if err != nil {
		t.Fatalf("Presence: %v", err)
	}
	if presence.Open || presence.LastSeenAt != nil {
		t.Fatalf("presence after repeated pane reads = %+v, want still closed and never seen", presence)
	}
	if _, err := h.relay.GetRead(ctx, exchange.ID, agentID); !errors.Is(err, relay.ErrNotFound) {
		t.Fatalf("GetRead after repeated pane reads = %v, want ErrNotFound — a pane read must not consume", err)
	}
}
