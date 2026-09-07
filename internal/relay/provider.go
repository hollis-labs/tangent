package relay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/tangent/internal/channel"
)

// Provider is the orchestration boundary CW-20260906-0072 introduces between
// an MCP-level (or any other) transport and this package's two stores
// (channel.Store and Store). It exists because relay_tools.go's seven
// handlers, before this task, resolved participants, validated inputs, and
// sequenced calls across both stores inline — logic with nothing MCP-shaped
// about it, sitting in the one file that also owns JSON schemas and
// contract-version wire framing. Provider pulls that logic out so it can be
// exercised, and eventually swapped, independent of the transport: a
// future push-capable transport (CW-20260907-0061) or a Nanite plugin host
// implements the same seven methods without relay_tools.go changing shape.
//
// CLIProvider is the only implementation today, and it is deliberately not
// an SDK registration — the personal-MVP plan's decision 2 is to build the
// relay in Tangent core first and shape storage/service ports for later
// extraction, not to stand up a plugin host before anything needs one.
type Provider interface {
	OpenChannel(ctx context.Context, params OpenChannelParams) (OpenChannelResult, error)
	Attach(ctx context.Context, params AttachParams) (AttachResult, error)
	Detach(ctx context.Context, params DetachParams) (DetachResult, error)
	Send(ctx context.Context, params SendParams) (SendResult, error)
	Receive(ctx context.Context, query InboxQuery) (InboxPage, error)
	Ack(ctx context.Context, params AckParams) (AckResult, error)
	Capabilities(ctx context.Context, params CapabilitiesParams) (CapabilitiesResult, error)
}

// Source is a caller's self-asserted identity: an external authority and a
// reference within it. It reuses tangent.hitl_*'s exact vocabulary at the
// transport layer (source.application_id / agent_id); here it is the same
// shape with the JSON tags stripped, since a Go caller of Provider has no
// wire format to satisfy.
type Source struct {
	ApplicationID string
	AgentID       string
}

// CLIProvider is the first-party relay Provider (CW-20260906-0072): the
// seven tangent.relay_* tools' orchestration, backed directly by a
// channel.Store and a Store. "CLI" names what attaches to it today — a
// Claude Code session's cooperative-loop skill — not a constraint on what
// could attach through the same seven methods later.
type CLIProvider struct {
	channels *channel.Store
	store    *Store
}

// NewCLIProvider wires a CLIProvider to the two stores every relay
// orchestration call needs. Both must be non-nil; this mirrors
// WithRelay's own validation one layer up, and a nil store here would
// panic on the first call rather than fail fast at construction.
func NewCLIProvider(channels *channel.Store, store *Store) *CLIProvider {
	return &CLIProvider{channels: channels, store: store}
}

var _ Provider = (*CLIProvider)(nil)

// standaloneLocalScope is the same advisory, non-enforced scope every
// direct-loopback caller gets elsewhere in this host (ADR 0004 §3.4); relay
// channels carry it for the same reason surfaces do, not as an access
// boundary.
const standaloneLocalScope = "standalone-local:anonymous"

// ── OpenChannel ─────────────────────────────────────────────────────────

type OpenChannelParams struct {
	Title      string
	ProjectRef string
}

type OpenChannelResult struct {
	Channel  channel.Channel
	Operator channel.Participant
}

func (p *CLIProvider) OpenChannel(ctx context.Context, params OpenChannelParams) (OpenChannelResult, error) {
	ch, operator, err := p.channels.OpenChannel(ctx, channel.CreateChannelParams{
		Title: params.Title, OwnerScope: standaloneLocalScope, ProjectRef: params.ProjectRef,
	})
	if err != nil {
		return OpenChannelResult{}, err
	}
	return OpenChannelResult{Channel: ch, Operator: operator}, nil
}

// ── Attach ──────────────────────────────────────────────────────────────

type AttachParams struct {
	ChannelID          string
	Source             Source
	RuntimeAuthority   string
	RuntimeEndpointRef string
}

type AttachResult struct {
	Participant channel.Participant
	ChannelID   string
	Binding     channel.RuntimeBinding
}

func (p *CLIProvider) Attach(ctx context.Context, params AttachParams) (AttachResult, error) {
	if params.ChannelID == "" {
		return AttachResult{}, fmt.Errorf("%w: channel_id is required", channel.ErrInvalidRecord)
	}
	if params.RuntimeAuthority == "" {
		return AttachResult{}, fmt.Errorf("%w: runtime.authority is required", channel.ErrInvalidRecord)
	}
	if _, err := p.channels.GetChannel(ctx, params.ChannelID); err != nil {
		return AttachResult{}, err
	}
	participant, err := p.resolveAgentParticipant(ctx, params.Source)
	if err != nil {
		return AttachResult{}, err
	}
	_, err = p.channels.AddParticipant(ctx, params.ChannelID, participant.ID)
	if err != nil {
		return AttachResult{}, err
	}
	binding, err := p.channels.Rebind(ctx, params.ChannelID, participant.ID, channel.RebindParams{
		RuntimeAuthority: params.RuntimeAuthority, RuntimeEndpointRef: params.RuntimeEndpointRef,
	})
	if err != nil {
		return AttachResult{}, err
	}
	return AttachResult{Participant: participant, ChannelID: params.ChannelID, Binding: binding}, nil
}

func (p *CLIProvider) resolveAgentParticipant(ctx context.Context, source Source) (channel.Participant, error) {
	if source.ApplicationID == "" || source.AgentID == "" {
		return channel.Participant{}, fmt.Errorf("%w: source.application_id and source.agent_id are both required", channel.ErrInvalidRecord)
	}
	return p.channels.UpsertParticipant(ctx, channel.UpsertParticipantParams{
		Kind: channel.ParticipantAgent, ExternalAuthority: source.ApplicationID, ExternalRef: source.AgentID,
	})
}

// ── Detach ──────────────────────────────────────────────────────────────

type DetachParams struct {
	ChannelID string
	Source    Source
}

type DetachResult struct {
	Participant channel.Participant
	ChannelID   string
	DetachedAt  time.Time
}

func (p *CLIProvider) Detach(ctx context.Context, params DetachParams) (DetachResult, error) {
	if params.ChannelID == "" {
		return DetachResult{}, fmt.Errorf("%w: channel_id is required", channel.ErrInvalidRecord)
	}
	participant, err := p.resolveAgentParticipant(ctx, params.Source)
	if err != nil {
		return DetachResult{}, err
	}
	detachedAt := time.Now().UTC()
	if err := p.channels.RemoveParticipant(ctx, params.ChannelID, participant.ID); err != nil {
		return DetachResult{}, err
	}
	return DetachResult{Participant: participant, ChannelID: params.ChannelID, DetachedAt: detachedAt}, nil
}

// ── Send ────────────────────────────────────────────────────────────────

type SendParams struct {
	ChannelID              string
	IdempotencyKey         string
	Source                 Source
	RecipientApplicationID string
	RecipientAgentID       string
	SubjectID              string
	ReplyToExchangeID      string
	Body                   string
}

type SendResult struct {
	Exchange                Exchange
	Sender                  channel.Participant
	Recipient               channel.Participant
	RecipientBindingCurrent bool
	Presence                Presence
}

func (p *CLIProvider) Send(ctx context.Context, params SendParams) (SendResult, error) {
	if params.ChannelID == "" || params.IdempotencyKey == "" || params.Body == "" {
		return SendResult{}, fmt.Errorf("%w: channel_id, idempotency_key, and body are all required", channel.ErrInvalidRecord)
	}
	sender, err := p.resolveAgentParticipant(ctx, params.Source)
	if err != nil {
		return SendResult{}, err
	}
	hasApplicationID := params.RecipientApplicationID != ""
	hasAgentID := params.RecipientAgentID != ""
	var recipient channel.Participant
	switch {
	case hasApplicationID != hasAgentID:
		return SendResult{}, fmt.Errorf(
			"%w: recipient_application_id and recipient_agent_id must be given together, or both omitted",
			channel.ErrInvalidRecord)
	case hasApplicationID && hasAgentID:
		recipient, err = p.resolveAgentParticipant(ctx, Source{
			ApplicationID: params.RecipientApplicationID, AgentID: params.RecipientAgentID,
		})
		if err != nil {
			return SendResult{}, err
		}
	default:
		recipient, err = p.resolveDefaultRecipient(ctx, params.ChannelID)
		if err != nil {
			return SendResult{}, err
		}
	}

	exchange, err := p.store.AcceptExchange(ctx, AcceptExchangeParams{
		IdempotencyKey: params.IdempotencyKey, ChannelID: params.ChannelID,
		SubjectID: params.SubjectID, SenderParticipantID: sender.ID, RecipientParticipantID: recipient.ID,
		ReplyToExchangeID: params.ReplyToExchangeID, Body: params.Body,
	})
	if err != nil {
		return SendResult{}, err
	}
	// An operator recipient has no runtime binding at all — "current" would
	// be a vacuous true (both sides empty) that reads as "yes, live and
	// routable" for a destination that was never routed via a binding in
	// the first place. Only ask the question for an agent recipient.
	var current bool
	if recipient.Kind == channel.ParticipantAgent {
		current, err = p.store.RecipientBindingCurrent(ctx, exchange.ID)
		if err != nil {
			return SendResult{}, err
		}
	}
	presence, err := p.store.Presence(ctx, recipient.ID)
	if err != nil {
		return SendResult{}, err
	}
	return SendResult{
		Exchange: exchange, Sender: sender, Recipient: recipient,
		RecipientBindingCurrent: current, Presence: presence,
	}, nil
}

// AmbiguousRecipientError is CW-20260906-0066's review requirement: refuse
// rather than guess when a channel has zero or several live operator
// participants, naming the candidates rather than silently misrouting.
type AmbiguousRecipientError struct {
	ChannelID    string
	CandidateIDs []string
}

func (e *AmbiguousRecipientError) Error() string {
	if len(e.CandidateIDs) == 0 {
		return fmt.Sprintf("channel %s has no live operator participant to default to; recipient is required", e.ChannelID)
	}
	return fmt.Sprintf("channel %s has %d live operator participants (%s); recipient is required",
		e.ChannelID, len(e.CandidateIDs), strings.Join(e.CandidateIDs, ", "))
}

func (p *CLIProvider) resolveDefaultRecipient(ctx context.Context, channelID string) (channel.Participant, error) {
	members, err := p.channels.ListChannelParticipants(ctx, channelID)
	if err != nil {
		return channel.Participant{}, err
	}
	var operators []channel.Participant
	for _, member := range members {
		if member.Kind == channel.ParticipantOperator {
			operators = append(operators, member)
		}
	}
	if len(operators) != 1 {
		ids := make([]string, len(operators))
		for i, operator := range operators {
			ids[i] = operator.ID
		}
		return channel.Participant{}, &AmbiguousRecipientError{ChannelID: channelID, CandidateIDs: ids}
	}
	return operators[0], nil
}

// ── Receive ─────────────────────────────────────────────────────────────

// InboxQuery is a Provider.Receive call's parameters. It is named apart
// from this package's existing ReceiveParams/ReceiveResult (Store.Receive's
// own signature) rather than reusing those names: a Provider caller
// supplies a Source it has not yet resolved to a participant, and gets back
// exchanges paired with their resolved sender, neither of which
// Store.Receive's shape carries.
type InboxQuery struct {
	ChannelID string
	Source    Source
	Cursor    int64
	Wait      time.Duration
	Limit     int
	// UnackedOnly excludes anything this participant already acked via
	// RecordRead, regardless of cursor — see Store.ListForDestination's doc
	// comment for the durable-check-in reasoning. The corollary a caller
	// must hold: anything received but never acked keeps reappearing on
	// every subsequent query, forever. That is intended — silently
	// dropping a message would be worse than re-showing it — but it means
	// a caller using UnackedOnly must ack everything it actually handles,
	// including a message that needs no reply, or that message is
	// re-delivered for the life of the channel.
	UnackedOnly bool
}

type InboxItem struct {
	Exchange Exchange
	Sender   channel.Participant
}

type InboxPage struct {
	Items      []InboxItem
	NextCursor int64
	TimedOut   bool
	Presence   Presence
}

func (p *CLIProvider) Receive(ctx context.Context, query InboxQuery) (InboxPage, error) {
	if query.ChannelID == "" {
		return InboxPage{}, fmt.Errorf("%w: channel_id is required", channel.ErrInvalidRecord)
	}
	participant, err := p.resolveAgentParticipant(ctx, query.Source)
	if err != nil {
		return InboxPage{}, err
	}
	result, err := p.store.Receive(ctx, ReceiveParams{
		ChannelID: query.ChannelID, ParticipantID: participant.ID,
		Cursor: query.Cursor, Wait: query.Wait, Limit: query.Limit, UnackedOnly: query.UnackedOnly,
	})
	if err != nil {
		return InboxPage{}, err
	}
	items := make([]InboxItem, 0, len(result.Items))
	for _, exchange := range result.Items {
		sender, senderErr := p.channels.GetParticipant(ctx, exchange.SenderParticipantID)
		if senderErr != nil {
			return InboxPage{}, senderErr
		}
		items = append(items, InboxItem{Exchange: exchange, Sender: sender})
	}
	return InboxPage{Items: items, NextCursor: result.NextCursor, TimedOut: result.TimedOut, Presence: result.Presence}, nil
}

// ── Ack ─────────────────────────────────────────────────────────────────

type AckParams struct {
	Source     Source
	ExchangeID string
}

type AckResult struct {
	Receipt      ReadReceipt
	AlreadyAcked bool
}

func (p *CLIProvider) Ack(ctx context.Context, params AckParams) (AckResult, error) {
	if params.ExchangeID == "" {
		return AckResult{}, fmt.Errorf("%w: exchange_id is required", channel.ErrInvalidRecord)
	}
	participant, err := p.resolveAgentParticipant(ctx, params.Source)
	if err != nil {
		return AckResult{}, err
	}
	exchange, err := p.store.GetExchange(ctx, params.ExchangeID)
	if err != nil {
		return AckResult{}, err
	}
	if exchange.RecipientParticipantID != participant.ID {
		return AckResult{}, fmt.Errorf("%w: %s is not the recipient of %s", ErrUnauthorized, participant.ID, exchange.ID)
	}
	existing, err := p.store.GetRead(ctx, params.ExchangeID, participant.ID)
	alreadyAcked := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return AckResult{}, err
	}
	receipt, err := p.store.RecordRead(ctx, params.ExchangeID, participant.ID)
	if err != nil {
		return AckResult{}, err
	}
	if alreadyAcked {
		receipt = existing
	}
	return AckResult{Receipt: receipt, AlreadyAcked: alreadyAcked}, nil
}

// ── Capabilities ────────────────────────────────────────────────────────

type CapabilitiesParams struct {
	ChannelID     string
	ApplicationID string
	AgentID       string
}

// Capabilities is what one Provider actually supports, with unsupported
// operations named explicitly rather than simulated — the same discipline
// tangent.relay_capabilities documents at the wire layer.
type Capabilities struct {
	Receive             bool
	Ack                 bool
	BoundedWait         bool
	Presence            bool
	Wake                bool
	EventReplay         bool
	StructuredEnvelopes bool
}

type CapabilitiesResult struct {
	Adapter      string
	Capabilities Capabilities
	WaitMsMax    int64
}

func (p *CLIProvider) Capabilities(ctx context.Context, params CapabilitiesParams) (CapabilitiesResult, error) {
	// The pairing rule lives here, in code, rather than as a schema
	// conditional: CW-20260907-0016 found mux's discovery schema drops
	// allOf/if/then branches, so a field gated behind one reaches the model
	// untyped. Both fields stay independently optional in the schema.
	hasApplicationID := params.ApplicationID != ""
	hasAgentID := params.AgentID != ""
	switch {
	case hasApplicationID != hasAgentID:
		return CapabilitiesResult{}, fmt.Errorf("%w: application_id and agent_id must be given together, or both omitted", channel.ErrInvalidRecord)
	case params.ChannelID != "" && !hasApplicationID:
		return CapabilitiesResult{}, fmt.Errorf("%w: application_id and agent_id are required when channel_id is given", channel.ErrInvalidRecord)
	case params.ChannelID != "":
		if _, err := p.channels.GetChannel(ctx, params.ChannelID); err != nil {
			return CapabilitiesResult{}, err
		}
		if _, err := p.resolveAgentParticipant(ctx, Source{ApplicationID: params.ApplicationID, AgentID: params.AgentID}); err != nil {
			return CapabilitiesResult{}, err
		}
	}
	return CapabilitiesResult{
		Adapter: "cli-relay",
		Capabilities: Capabilities{
			Receive: true, Ack: true, BoundedWait: true, Presence: true,
			Wake: false, EventReplay: false, StructuredEnvelopes: false,
		},
		WaitMsMax: int64(MaximumReceiveWait / time.Millisecond),
	}, nil
}
