// Package channelpane is the operator-facing application service for the
// minimal channel pane (CW-20260907-0017): it composes channel.Store,
// relay.Store, and hitl.Service's already-public Inbox() into the
// presentation shape internal/server's browser API exposes at
// /api/channels. It is not part of the tangent.relay_* MCP surface — the
// operator is not an MCP client (ADR 0006 §3; CW-20260906-0066's package
// doc says the same for the same reason) — and it makes no change to
// internal/hitl's own contract: HITL correlation is read-only, parsing the
// same exported RequestSnapshot the /hitl inbox itself renders.
//
// Every read here is a pure read. ListChannels and GetChannel call only
// relay.Store.ListForDestination, relay.Store.ListForChannel,
// relay.Store.GetRead, and relay.Store.Presence — never
// relay.Store.Receive, which is the agent's own consuming, ack-driven
// position marker. Only MarkRead writes (relay.Store.RecordRead), and only
// when a caller explicitly asks for it, never as a side effect of a GET.
package channelpane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/hollis-labs/tangent/internal/channel"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/relay"
)

// unreadListLimit bounds how many unacked-by-operator exchanges
// ListChannels and MarkRead look at per channel in one call. The
// personal-MVP scale this task targets (one operator, a handful of
// channels) never approaches it; it exists so a runaway channel cannot
// make either call unbounded.
const unreadListLimit = 500

// historyLimit bounds GetChannel's whole-history read. CW-20260907-0017 is
// deliberately the minimal pane — no scrollback pagination is in scope —
// so this is generous rather than tuned; a channel with more history than
// this is a phase-5 problem (subject pane, search), not this task's.
const historyLimit = 200

// HITLInbox is the narrow slice of hitl.Service this package depends on —
// exactly Inbox, already public. Declared as an interface, rather than
// taking *hitl.Service directly, so this package's own tests can inject a
// fixture without a real durable-interaction store.
type HITLInbox interface {
	Inbox(ctx context.Context) (hitl.OperatorInbox, error)
}

// Service is CW-20260907-0017's application service.
type Service struct {
	channels *channel.Store
	relay    *relay.Store
	hitl     HITLInbox
}

// New constructs a Service. hitlInbox may be nil — HITL correlation is
// then silently empty, the same "optional dependency, honest empty
// answer" choice this host makes elsewhere (health, telemetry) rather
// than failing the whole pane over one unavailable projection.
func New(channels *channel.Store, relayStore *relay.Store, hitlInbox HITLInbox) (*Service, error) {
	if channels == nil {
		return nil, fmt.Errorf("channelpane: nil channel store")
	}
	if relayStore == nil {
		return nil, fmt.Errorf("channelpane: nil relay store")
	}
	return &Service{channels: channels, relay: relayStore, hitl: hitlInbox}, nil
}

// ChannelSummary is one row of the channel list.
type ChannelSummary struct {
	ChannelID          string     `json:"channel_id"`
	Title              string     `json:"title,omitempty"`
	UnreadCount        int        `json:"unread_count"`
	NeedsInputCount    int        `json:"needs_input_count"`
	LastMessageAt      *time.Time `json:"last_message_at,omitempty"`
	LastMessagePreview string     `json:"last_message_preview,omitempty"`
}

// MessageView is one exchange rendered for the addressed chat view.
//
// DeliveryState is set only for an operator-sent message (Direction ==
// "operator") — an agent post has no delivery state to report; the
// operator is reading it, not waiting on anyone. Exactly three values:
// "queued" (unacked, the agent's own receive is actively open right now —
// delivery is imminent, measured at ~58ms in CW-20260906-0071's live
// proof), "awaiting-peer" (unacked, the agent is idle or gone —
// last_seen_at, if any, is the only evidence of life; ARCHITECTURE.md §5:
// "Tangent does not pretend the agent is reading"), and
// "accepted-by-peer" (a row in exchange_reads: the agent acked it). A
// fourth, "failed", is deliberately never produced: under the pull model
// CW-20260906-0072 settled on, nothing ever writes a delivery receipt, so
// a message cannot fail in transit — it sits until the agent asks.
// Rendering a state the system cannot enter would be worse than omitting
// it.
//
// RecipientBindingCurrent is deliberately not exposed here. A reconnect
// and an explicit rebind are the same code path (channel.Store.Rebind),
// so a channel's entire history reads binding-not-current after any
// ordinary agent relaunch — that is provenance ("sent to an earlier
// session"), not a delivery outcome, and the safest way to guarantee this
// pane never paints it as an error is to not render it at all.
type MessageView struct {
	ExchangeID    string    `json:"exchange_id"`
	Direction     string    `json:"direction"` // "operator" | "agent"
	Body          string    `json:"body"`
	CreatedAt     time.Time `json:"created_at"`
	DeliveryState string    `json:"delivery_state,omitempty"`
}

// HITLItem is one HITL item raised by this channel's agent, shown inline
// with a link to its /hitl item.
type HITLItem struct {
	ItemID     string    `json:"item_id"`
	ItemURL    string    `json:"item_url"`
	State      string    `json:"state"`
	EnqueuedAt time.Time `json:"enqueued_at"`
}

// AgentPresence is a third party's view of the channel's agent
// participant — never the operator's own self-report, which this package
// never makes, since nothing here ever calls relay.Store.Receive.
type AgentPresence struct {
	Open       bool       `json:"open"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

// ChannelDetail is the full addressed chat view for one channel. Messages
// is newest first, matching internal/hitl's OperatorInbox.History
// convention; a caller rendering a chat reverses it.
type ChannelDetail struct {
	ChannelID string         `json:"channel_id"`
	Title     string         `json:"title,omitempty"`
	Messages  []MessageView  `json:"messages"`
	HITLItems []HITLItem     `json:"hitl_items"`
	Agent     *AgentPresence `json:"agent_presence,omitempty"`
}

// SendInput is one operator-composed message. RecipientApplicationID and
// RecipientAgentID must be given together, or both omitted to default to
// the channel's one live agent participant — the mirror image of
// relay.Provider's "one live operator" default for an agent sender.
type SendInput struct {
	Body                   string
	RecipientApplicationID string
	RecipientAgentID       string
}

// AmbiguousRecipientError refuses rather than guesses when a channel has
// zero or several live agent participants, naming the candidates rather
// than silently misrouting — the same discipline
// relay.AmbiguousRecipientError holds for an agent sender's default send.
type AmbiguousRecipientError struct {
	ChannelID    string
	CandidateIDs []string
}

func (e *AmbiguousRecipientError) Error() string {
	if len(e.CandidateIDs) == 0 {
		return fmt.Sprintf("channel %s has no live agent participant to default to; recipient is required", e.ChannelID)
	}
	joined := e.CandidateIDs[0]
	for _, id := range e.CandidateIDs[1:] {
		joined += ", " + id
	}
	return fmt.Sprintf("channel %s has %d live agent participants (%s); recipient is required",
		e.ChannelID, len(e.CandidateIDs), joined)
}

func (s *Service) resolveOperatorParticipant(ctx context.Context) (channel.Participant, error) {
	return s.channels.UpsertParticipant(ctx, channel.UpsertParticipantParams{
		Kind: channel.ParticipantOperator, ExternalAuthority: channel.OperatorExternalAuthority, ExternalRef: channel.OperatorExternalRef,
	})
}

func (s *Service) channelAgents(ctx context.Context, channelID string) ([]channel.Participant, error) {
	members, err := s.channels.ListChannelParticipants(ctx, channelID)
	if err != nil {
		return nil, err
	}
	var agents []channel.Participant
	for _, member := range members {
		if member.Kind == channel.ParticipantAgent {
			agents = append(agents, member)
		}
	}
	return agents, nil
}

func (s *Service) resolveDefaultAgentRecipient(ctx context.Context, channelID string) (channel.Participant, error) {
	agents, err := s.channelAgents(ctx, channelID)
	if err != nil {
		return channel.Participant{}, err
	}
	if len(agents) != 1 {
		ids := make([]string, len(agents))
		for i, agent := range agents {
			ids[i] = agent.ID
		}
		return channel.Participant{}, &AmbiguousRecipientError{ChannelID: channelID, CandidateIDs: ids}
	}
	return agents[0], nil
}

// hitlSourceOnly extracts just enough of an HITL request snapshot to
// correlate it to a relay participant. internal/hitl exposes no parsed
// Source field of its own — ItemView.RequestSnapshot is already public
// json.RawMessage; this reads it, it does not change it.
type hitlSourceOnly struct {
	Source struct {
		ApplicationID string `json:"application_id"`
		AgentID       string `json:"agent_id"`
	} `json:"source"`
}

func agentKey(applicationID, agentID string) string { return applicationID + "\x00" + agentID }

func (s *Service) pendingHITLByAgent(ctx context.Context) (map[string][]hitl.OperatorItemView, error) {
	if s.hitl == nil {
		return nil, nil
	}
	inbox, err := s.hitl.Inbox(ctx)
	if err != nil {
		return nil, err
	}
	byAgent := make(map[string][]hitl.OperatorItemView)
	for _, item := range inbox.Pending {
		var snapshot hitlSourceOnly
		if json.Unmarshal(item.RequestSnapshot, &snapshot) != nil {
			continue // malformed snapshot: not this package's contract to enforce
		}
		if snapshot.Source.ApplicationID == "" || snapshot.Source.AgentID == "" {
			continue
		}
		key := agentKey(snapshot.Source.ApplicationID, snapshot.Source.AgentID)
		byAgent[key] = append(byAgent[key], item)
	}
	return byAgent, nil
}

// ListChannels returns every channel the operator is a member of — which
// is every channel that exists, since OpenChannel always adds the
// operator as its first member — sorted newest-activity first.
func (s *Service) ListChannels(ctx context.Context) ([]ChannelSummary, error) {
	operator, err := s.resolveOperatorParticipant(ctx)
	if err != nil {
		return nil, err
	}
	channels, err := s.channels.ListParticipantChannels(ctx, operator.ID)
	if err != nil {
		return nil, err
	}
	pendingByAgent, err := s.pendingHITLByAgent(ctx)
	if err != nil {
		return nil, err
	}

	summaries := make([]ChannelSummary, 0, len(channels))
	for _, ch := range channels {
		unread, err := s.relay.ListForDestination(ctx, ch.ID, operator.ID, 0, unreadListLimit, true)
		if err != nil {
			return nil, err
		}
		agents, err := s.channelAgents(ctx, ch.ID)
		if err != nil {
			return nil, err
		}
		needsInput := 0
		for _, agent := range agents {
			needsInput += len(pendingByAgent[agentKey(agent.ExternalAuthority, agent.ExternalRef)])
		}
		recent, err := s.relay.ListForChannel(ctx, ch.ID, 1)
		if err != nil {
			return nil, err
		}
		summary := ChannelSummary{
			ChannelID: ch.ID, Title: ch.Title, UnreadCount: len(unread), NeedsInputCount: needsInput,
		}
		if len(recent) > 0 {
			at := recent[0].CreatedAt
			summary.LastMessageAt = &at
			summary.LastMessagePreview = recent[0].Body
		}
		summaries = append(summaries, summary)
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		ti, tj := summaries[i].LastMessageAt, summaries[j].LastMessageAt
		switch {
		case ti == nil:
			return false
		case tj == nil:
			return true
		default:
			return ti.After(*tj)
		}
	})
	return summaries, nil
}

// GetChannel returns one channel's full addressed chat view.
func (s *Service) GetChannel(ctx context.Context, channelID string) (ChannelDetail, error) {
	ch, err := s.channels.GetChannel(ctx, channelID)
	if err != nil {
		return ChannelDetail{}, err
	}
	operator, err := s.resolveOperatorParticipant(ctx)
	if err != nil {
		return ChannelDetail{}, err
	}
	exchanges, err := s.relay.ListForChannel(ctx, channelID, historyLimit)
	if err != nil {
		return ChannelDetail{}, err
	}
	agents, err := s.channelAgents(ctx, channelID)
	if err != nil {
		return ChannelDetail{}, err
	}

	messages := make([]MessageView, 0, len(exchanges))
	for _, exchange := range exchanges {
		view := MessageView{ExchangeID: exchange.ID, Body: exchange.Body, CreatedAt: exchange.CreatedAt}
		if exchange.SenderParticipantID == operator.ID {
			view.Direction = "operator"
			state, stateErr := s.deliveryState(ctx, exchange)
			if stateErr != nil {
				return ChannelDetail{}, stateErr
			}
			view.DeliveryState = state
		} else {
			view.Direction = "agent"
		}
		messages = append(messages, view)
	}

	var hitlItems []HITLItem
	if len(agents) > 0 {
		pendingByAgent, pendingErr := s.pendingHITLByAgent(ctx)
		if pendingErr != nil {
			return ChannelDetail{}, pendingErr
		}
		for _, agent := range agents {
			for _, item := range pendingByAgent[agentKey(agent.ExternalAuthority, agent.ExternalRef)] {
				hitlItems = append(hitlItems, HITLItem{
					ItemID: item.ItemID, ItemURL: hitl.InboxURL + "/items/" + url.PathEscape(item.ItemID),
					State: string(item.State), EnqueuedAt: item.EnqueuedAt,
				})
			}
		}
	}

	var agentPresence *AgentPresence
	if len(agents) > 0 {
		presence, presErr := s.relay.Presence(ctx, agents[0].ID)
		if presErr != nil {
			return ChannelDetail{}, presErr
		}
		agentPresence = &AgentPresence{Open: presence.Open, LastSeenAt: presence.LastSeenAt}
	}

	return ChannelDetail{
		ChannelID: ch.ID, Title: ch.Title, Messages: messages, HITLItems: hitlItems, Agent: agentPresence,
	}, nil
}

func (s *Service) deliveryState(ctx context.Context, exchange relay.Exchange) (string, error) {
	_, err := s.relay.GetRead(ctx, exchange.ID, exchange.RecipientParticipantID)
	switch {
	case err == nil:
		return "accepted-by-peer", nil
	case errors.Is(err, relay.ErrNotFound):
		// Fall through to the presence check.
	default:
		return "", err
	}
	presence, err := s.relay.Presence(ctx, exchange.RecipientParticipantID)
	if err != nil {
		return "", err
	}
	if presence.Open {
		return "queued", nil
	}
	return "awaiting-peer", nil
}

// SendMessage accepts one operator-composed message into the relay
// journal, over channel.Store and relay.Store directly — never the
// tangent.relay_* MCP surface, which is agent-facing only (ADR 0006 §3;
// CW-20260906-0066's package doc). The idempotency key is minted here,
// server-side, on every call: a human clicking Send has no retry loop to
// de-duplicate against, unlike an agent's skill-driven send.
func (s *Service) SendMessage(ctx context.Context, channelID string, input SendInput) (MessageView, error) {
	if input.Body == "" {
		return MessageView{}, fmt.Errorf("%w: body is required", channel.ErrInvalidRecord)
	}
	operator, err := s.resolveOperatorParticipant(ctx)
	if err != nil {
		return MessageView{}, err
	}
	hasApp := input.RecipientApplicationID != ""
	hasAgent := input.RecipientAgentID != ""
	var recipient channel.Participant
	switch {
	case hasApp != hasAgent:
		return MessageView{}, fmt.Errorf(
			"%w: recipient_application_id and recipient_agent_id must be given together, or both omitted", channel.ErrInvalidRecord)
	case hasApp && hasAgent:
		recipient, err = s.channels.UpsertParticipant(ctx, channel.UpsertParticipantParams{
			Kind: channel.ParticipantAgent, ExternalAuthority: input.RecipientApplicationID, ExternalRef: input.RecipientAgentID,
		})
		if err != nil {
			return MessageView{}, err
		}
	default:
		recipient, err = s.resolveDefaultAgentRecipient(ctx, channelID)
		if err != nil {
			return MessageView{}, err
		}
	}

	exchange, err := s.relay.AcceptExchange(ctx, relay.AcceptExchangeParams{
		IdempotencyKey: "operator-send:" + uuid.NewString(), ChannelID: channelID,
		SenderParticipantID: operator.ID, RecipientParticipantID: recipient.ID, Body: input.Body,
	})
	if err != nil {
		return MessageView{}, err
	}
	state, err := s.deliveryState(ctx, exchange)
	if err != nil {
		return MessageView{}, err
	}
	return MessageView{
		ExchangeID: exchange.ID, Direction: "operator", Body: exchange.Body,
		CreatedAt: exchange.CreatedAt, DeliveryState: state,
	}, nil
}

// MarkRead acknowledges every exchange currently unread by the operator in
// one channel — a caller-explicit action, never a side effect of
// ListChannels or GetChannel. It returns the count marked.
func (s *Service) MarkRead(ctx context.Context, channelID string) (int, error) {
	operator, err := s.resolveOperatorParticipant(ctx)
	if err != nil {
		return 0, err
	}
	unread, err := s.relay.ListForDestination(ctx, channelID, operator.ID, 0, unreadListLimit, true)
	if err != nil {
		return 0, err
	}
	for _, exchange := range unread {
		if _, err := s.relay.RecordRead(ctx, exchange.ID, operator.ID); err != nil {
			return 0, err
		}
	}
	return len(unread), nil
}

// Revision is a cheap, opaque "has anything the channel list cares about
// changed" fingerprint, matching internal/hitl's own event contract: it is
// never treated as state itself, only as a hint to refetch ListChannels.
// It is a digest of exactly the fields the list view renders (unread
// count, needs-input count, last message time) — a presence transition
// between "queued" and "awaiting-peer" inside an already-open channel's
// detail view is not reflected here, by design; that view refreshes on
// its own short interval rather than waiting on this hint.
func (s *Service) Revision(ctx context.Context) (string, error) {
	summaries, err := s.ListChannels(ctx)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	for _, summary := range summaries {
		fmt.Fprintf(digest, "%s|%d|%d|%v\n", summary.ChannelID, summary.UnreadCount, summary.NeedsInputCount, summary.LastMessageAt)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
