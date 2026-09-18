package mcp

import (
	"context"
	"errors"
	"time"

	"github.com/hollis-labs/tangent/internal/channel"
	"github.com/hollis-labs/tangent/internal/relay"
)

// relay_tools.go is the cooperative MCP inbox (CW-20260906-0066): seven
// tools over relay.Provider, implementing ADR 0006 §3 and the
// CW-20260907-0016 spike's observed contract. This file owns the wire
// boundary only — JSON shapes, contract versioning, and translating
// relay.Provider's Go-level params/results to and from them. Participant
// resolution, default-recipient resolution, and every relay_* business rule
// live in relay.Provider (CW-20260906-0072): this file's handlers are thin
// on purpose.
//
// The operator's own send/read path is deliberately not here. These seven
// tools are the agent-facing MCP surface only; a human operator has no MCP
// client. CW-20260906-0017's channel pane is where the operator side lives,
// over a REST API calling relay.Provider (or the stores directly) — this
// file's gap is that boundary, not an omission.
//
// contractVersion is repeated on every input/output the way tangent.hitl_*
// repeats "1.0": "versioned contracts" per this task's acceptance item 1.
const relayContractVersion = "1.0"

// relaySourceInput is the caller's self-asserted identity, reusing
// tangent.hitl_*'s exact vocabulary (source.application_id / agent_id)
// rather than inventing a second shape. Both fields are required, unlike
// HITL's: an agent participant's whole point is resolving to the same
// stable identity across attach/send/receive/ack calls
// (channel.Store.UpsertParticipant's idempotent-by-external-ref lookup),
// which only works when agent_id is never empty.
type relaySourceInput struct {
	ApplicationID string `json:"application_id"`
	AgentID       string `json:"agent_id"`
}

func (s relaySourceInput) toRelaySource() relay.Source {
	return relay.Source{ApplicationID: s.ApplicationID, AgentID: s.AgentID}
}

// relayParticipantRef echoes a resolved participant back in a result, so a
// caller can see who Tangent actually addressed — including a default
// recipient it did not name itself.
type relayParticipantRef struct {
	ParticipantID string `json:"participant_id"`
	ApplicationID string `json:"application_id,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
}

func relayRefFor(p channel.Participant) relayParticipantRef {
	return relayParticipantRef{ParticipantID: p.ID, ApplicationID: p.ExternalAuthority, AgentID: p.ExternalRef}
}

func (s *Server) registerRelayTools() error {
	if err := addInteractionTool(s, "tangent.relay_open_channel",
		"Create a new channel and add the canonical operator participant as its first member. "+
			"Channel creation is deliberately its own tool, separate from attach: an agent that mistypes "+
			"or omits a channel id at attach must get a loud error, never a silently created channel that "+
			"leaves two sessions in separate rooms with no signal that anything went wrong.",
		s.handleRelayOpenChannel); err != nil {
		return err
	}
	if err := addInteractionTool(s, "tangent.relay_attach",
		"Attach an agent participant to an existing channel and bind its current runtime session as the "+
			"live destination, advancing the binding generation. channel_id must already exist (see "+
			"tangent.relay_open_channel); this call never creates one.",
		s.handleRelayAttach); err != nil {
		return err
	}
	if err := addInteractionTool(s, "tangent.relay_detach",
		"End an agent participant's membership in a channel and supersede its current runtime binding in "+
			"the same transaction, so no destination is ever trusted for a participant who has left. Covers "+
			"only a clean exit; a session that simply exits without calling this is read through presence, "+
			"not membership.",
		s.handleRelayDetach); err != nil {
		return err
	}
	if err := addInteractionTool(s, "tangent.relay_send",
		"Accept one message into the relay journal, atomically queuing its delivery work. recipient may be "+
			"omitted only when the channel has exactly one live operator participant to default to; "+
			"otherwise it is required and this refuses rather than guessing.",
		s.handleRelaySend); err != nil {
		return err
	}
	if err := addInteractionTool(s, "tangent.relay_receive",
		"Receive new messages addressed to this participant in this channel since cursor, optionally "+
			"waiting up to 50000 ms for one to arrive. A timeout returns status \"ok\" is not implied: it "+
			"returns status \"timeout\" with the same cursor, never an error, and cancels no outstanding work.",
		s.handleRelayReceive); err != nil {
		return err
	}
	if err := addInteractionTool(s, "tangent.relay_ack",
		"Acknowledge that this participant consumed one exchange. Separate from receive: retrieving a "+
			"message is not the same fact as having read it. Refuses if the caller is not the exchange's "+
			"recipient.",
		s.handleRelayAck); err != nil {
		return err
	}
	if err := addInteractionTool(s, "tangent.relay_capabilities",
		"Report what this relay adapter actually supports, with unsupported operations named explicitly "+
			"rather than simulated. source is required only when channel_id is given (validated here, not "+
			"encoded as a schema conditional, because a conditional on a field a model fills is exactly "+
			"what CW-20260907-0016 found gateways drop).",
		s.handleRelayCapabilities); err != nil {
		return err
	}
	return nil
}

// ── tangent.relay_open_channel ──────────────────────────────────────────

type relayOpenChannelInput struct {
	Title      string `json:"title,omitempty"`
	ProjectRef string `json:"project_ref,omitempty"`
}

type relayOpenChannelOutput struct {
	ContractVersion       string    `json:"contract_version"`
	ChannelID             string    `json:"channel_id"`
	OperatorParticipantID string    `json:"operator_participant_id"`
	CreatedAt             time.Time `json:"created_at"`
}

func (s *Server) handleRelayOpenChannel(
	ctx context.Context,
	input relayOpenChannelInput,
) (any, error) {
	result, err := s.relayProvider.OpenChannel(ctx, relay.OpenChannelParams{
		Title: input.Title, ProjectRef: input.ProjectRef,
	})
	if err != nil {
		return nil, relayErrorResult(err)
	}
	return relayOpenChannelOutput{
		ContractVersion: relayContractVersion, ChannelID: result.Channel.ID,
		OperatorParticipantID: result.Operator.ID, CreatedAt: result.Channel.CreatedAt,
	}, nil
}

// ── tangent.relay_attach ────────────────────────────────────────────────

// relayRuntimeInput carries no adapter_capabilities field. A []string is
// unconditionally schema'd as `"type": ["null", "array"]` by jsonschema-go —
// required or optional makes no difference, since a nil slice is a real Go
// zero value distinct from an empty one — so there is no flat way to keep
// an optional list here the way relay_send's recipient fields were
// flattened. Nothing in this task reads adapter_capabilities back once
// stored, so it is simpler and equally correct to not accept it at attach
// time at all; the DB column defaults to '[]' with no caller input. A
// future task that needs a caller to declare capabilities should carry
// them one string at a time or find a shape jsonschema-go renders as a
// plain type, not resurrect this field.
type relayRuntimeInput struct {
	Authority   string `json:"authority"`
	EndpointRef string `json:"endpoint_ref,omitempty"`
}

type relayAttachInput struct {
	ChannelID string            `json:"channel_id"`
	Source    relaySourceInput  `json:"source"`
	Runtime   relayRuntimeInput `json:"runtime"`
}

type relayAttachOutput struct {
	ContractVersion    string    `json:"contract_version"`
	ParticipantID      string    `json:"participant_id"`
	ChannelID          string    `json:"channel_id"`
	Generation         int64     `json:"generation"`
	RuntimeAuthority   string    `json:"runtime_authority"`
	RuntimeEndpointRef string    `json:"runtime_endpoint_ref,omitempty"`
	AttachedAt         time.Time `json:"attached_at"`
}

func (s *Server) handleRelayAttach(
	ctx context.Context,
	input relayAttachInput,
) (any, error) {
	result, err := s.relayProvider.Attach(ctx, relay.AttachParams{
		ChannelID: input.ChannelID, Source: input.Source.toRelaySource(),
		RuntimeAuthority: input.Runtime.Authority, RuntimeEndpointRef: input.Runtime.EndpointRef,
	})
	if err != nil {
		return nil, relayErrorResult(err)
	}
	return relayAttachOutput{
		ContractVersion: relayContractVersion, ParticipantID: result.Participant.ID, ChannelID: result.ChannelID,
		Generation: result.Binding.Generation, RuntimeAuthority: result.Binding.RuntimeAuthority,
		RuntimeEndpointRef: result.Binding.RuntimeEndpointRef, AttachedAt: result.Binding.BoundAt,
	}, nil
}

// ── tangent.relay_detach ────────────────────────────────────────────────

type relayDetachInput struct {
	ChannelID string           `json:"channel_id"`
	Source    relaySourceInput `json:"source"`
}

type relayDetachOutput struct {
	ContractVersion string    `json:"contract_version"`
	ParticipantID   string    `json:"participant_id"`
	ChannelID       string    `json:"channel_id"`
	DetachedAt      time.Time `json:"detached_at"`
}

func (s *Server) handleRelayDetach(
	ctx context.Context,
	input relayDetachInput,
) (any, error) {
	result, err := s.relayProvider.Detach(ctx, relay.DetachParams{
		ChannelID: input.ChannelID, Source: input.Source.toRelaySource(),
	})
	if err != nil {
		return nil, relayErrorResult(err)
	}
	return relayDetachOutput{
		ContractVersion: relayContractVersion, ParticipantID: result.Participant.ID,
		ChannelID: result.ChannelID, DetachedAt: result.DetachedAt,
	}, nil
}

// ── tangent.relay_send ──────────────────────────────────────────────────

type relaySendInput struct {
	ChannelID      string           `json:"channel_id"`
	IdempotencyKey string           `json:"idempotency_key"`
	Source         relaySourceInput `json:"source"`
	// RecipientApplicationID and RecipientAgentID are flat, not a nested
	// object, per director review of PR #34: a pointer-to-struct optional
	// field serializes as `"type": ["null", "object"]`, the exact union
	// shape the CW-20260907-0016 spike measured a model sending as a JSON
	// string in 3 of 3 sessions. Both must be given together, or both
	// omitted to default to the channel's one live operator; the pairing
	// is enforced in the handler, never as a schema conditional.
	RecipientApplicationID string `json:"recipient_application_id,omitempty" jsonschema:"Application id of an explicit recipient. Give this and recipient_agent_id together, or omit both to default to the channel's one live operator participant."`
	RecipientAgentID       string `json:"recipient_agent_id,omitempty" jsonschema:"Agent id of an explicit recipient. Give this and recipient_application_id together, or omit both to default to the channel's one live operator participant."`
	SubjectID              string `json:"subject_id,omitempty"`
	ReplyToExchangeID      string `json:"reply_to_exchange_id,omitempty"`
	Body                   string `json:"body" jsonschema:"The message text. Renders as markdown in the operator's channel pane."`
}

type relaySendOutput struct {
	ContractVersion         string              `json:"contract_version"`
	ExchangeID              string              `json:"exchange_id"`
	ChannelID               string              `json:"channel_id"`
	Sequence                int64               `json:"sequence"`
	Sender                  relayParticipantRef `json:"sender"`
	Recipient               relayParticipantRef `json:"recipient"`
	RecipientBindingCurrent bool                `json:"recipient_binding_current"`
	SubjectID               string              `json:"subject_id,omitempty"`
	ReplyToExchangeID       string              `json:"reply_to_exchange_id,omitempty"`
	CreatedAt               time.Time           `json:"created_at"`
	RecipientPresence       relayPresenceOutput `json:"recipient_presence"`
}

func (s *Server) handleRelaySend(
	ctx context.Context,
	input relaySendInput,
) (any, error) {
	result, err := s.relayProvider.Send(ctx, relay.SendParams{
		ChannelID: input.ChannelID, IdempotencyKey: input.IdempotencyKey, Source: input.Source.toRelaySource(),
		RecipientApplicationID: input.RecipientApplicationID, RecipientAgentID: input.RecipientAgentID,
		SubjectID: input.SubjectID, ReplyToExchangeID: input.ReplyToExchangeID, Body: input.Body,
	})
	if err != nil {
		return nil, relayErrorResult(err)
	}
	return relaySendOutput{
		ContractVersion: relayContractVersion, ExchangeID: result.Exchange.ID, ChannelID: result.Exchange.ChannelID,
		Sequence: result.Exchange.Sequence, Sender: relayRefFor(result.Sender), Recipient: relayRefFor(result.Recipient),
		RecipientBindingCurrent: result.RecipientBindingCurrent, SubjectID: result.Exchange.SubjectID,
		ReplyToExchangeID: result.Exchange.ReplyToExchangeID, CreatedAt: result.Exchange.CreatedAt,
		RecipientPresence: relayPresenceFor(result.Presence),
	}, nil
}

// ── tangent.relay_receive ───────────────────────────────────────────────

type relayReceiveInput struct {
	ChannelID string           `json:"channel_id"`
	Source    relaySourceInput `json:"source"`
	Cursor    int64            `json:"cursor,omitempty"`
	WaitMs    int64            `json:"wait_ms,omitempty"`
	Limit     int64            `json:"limit,omitempty"`
	// UnackedOnly excludes anything this participant already acked. A
	// relaunched process holds no cursor in memory, so cursor 0 with
	// unacked_only true is the durable-check-in shape: "everything that
	// still needs handling", regardless of how much acked history exists.
	// The corollary: anything received but never acked via
	// tangent.relay_ack keeps reappearing on every subsequent check-in,
	// forever — ack what you actually handle, including a message that
	// needs no reply, or it is re-delivered for the life of the channel.
	UnackedOnly bool `json:"unacked_only,omitempty" jsonschema:"Exclude anything already acknowledged via tangent.relay_ack, regardless of cursor. With cursor 0 this is the durable check-in shape for a freshly launched session: everything still unhandled. Anything received but not acked is re-delivered on every future call, forever, including a message you decided needed no reply — ack it anyway, or it never stops coming back."`
}

type relayExchangeView struct {
	ExchangeID        string              `json:"exchange_id"`
	Sequence          int64               `json:"sequence"`
	Sender            relayParticipantRef `json:"sender"`
	SubjectID         string              `json:"subject_id,omitempty"`
	ReplyToExchangeID string              `json:"reply_to_exchange_id,omitempty"`
	Body              string              `json:"body"`
	CreatedAt         time.Time           `json:"created_at"`
}

type relayPresenceOutput struct {
	Open       bool       `json:"open"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

func relayPresenceFor(p relay.Presence) relayPresenceOutput {
	return relayPresenceOutput{Open: p.Open, LastSeenAt: p.LastSeenAt}
}

type relayReceiveOutput struct {
	ContractVersion string              `json:"contract_version"`
	Status          string              `json:"status"`
	Items           []relayExchangeView `json:"items"`
	NextCursor      int64               `json:"next_cursor"`
	Presence        relayPresenceOutput `json:"presence"`
}

func (s *Server) handleRelayReceive(
	ctx context.Context,
	input relayReceiveInput,
) (any, error) {
	page, err := s.relayProvider.Receive(ctx, relay.InboxQuery{
		ChannelID: input.ChannelID, Source: input.Source.toRelaySource(),
		Cursor: input.Cursor, Wait: time.Duration(input.WaitMs) * time.Millisecond, Limit: int(input.Limit),
		UnackedOnly: input.UnackedOnly,
	})
	if err != nil {
		return nil, relayErrorResult(err)
	}
	status := "ok"
	if page.TimedOut {
		status = "timeout"
	}
	items := make([]relayExchangeView, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, relayExchangeView{
			ExchangeID: item.Exchange.ID, Sequence: item.Exchange.Sequence, Sender: relayRefFor(item.Sender),
			SubjectID: item.Exchange.SubjectID, ReplyToExchangeID: item.Exchange.ReplyToExchangeID,
			Body: item.Exchange.Body, CreatedAt: item.Exchange.CreatedAt,
		})
	}
	return relayReceiveOutput{
		ContractVersion: relayContractVersion, Status: status, Items: items,
		NextCursor: page.NextCursor, Presence: relayPresenceFor(page.Presence),
	}, nil
}

// ── tangent.relay_ack ───────────────────────────────────────────────────

type relayAckInput struct {
	Source     relaySourceInput `json:"source"`
	ExchangeID string           `json:"exchange_id"`
}

type relayAckOutput struct {
	ContractVersion string    `json:"contract_version"`
	ExchangeID      string    `json:"exchange_id"`
	ParticipantID   string    `json:"participant_id"`
	AckedAt         time.Time `json:"acked_at"`
	AlreadyAcked    bool      `json:"already_acked"`
}

func (s *Server) handleRelayAck(
	ctx context.Context,
	input relayAckInput,
) (any, error) {
	result, err := s.relayProvider.Ack(ctx, relay.AckParams{
		Source: input.Source.toRelaySource(), ExchangeID: input.ExchangeID,
	})
	if err != nil {
		return nil, relayErrorResult(err)
	}
	return relayAckOutput{
		ContractVersion: relayContractVersion, ExchangeID: result.Receipt.ExchangeID,
		ParticipantID: result.Receipt.ParticipantID, AckedAt: result.Receipt.AckedAt, AlreadyAcked: result.AlreadyAcked,
	}, nil
}

// ── tangent.relay_capabilities ──────────────────────────────────────────

type relayCapabilitiesInput struct {
	ChannelID string `json:"channel_id,omitempty"`
	// ApplicationID and AgentID are flat, not a nested object, for the same
	// reason relay_send's recipient fields are: a pointer-to-struct
	// optional field serializes as `"type": ["null", "object"]`, the union
	// shape CW-20260907-0016 measured a model sending as a JSON string.
	// Both must be given together, and both are required when channel_id
	// is given — the pairing is enforced in the handler, never as a schema
	// conditional.
	ApplicationID string `json:"application_id,omitempty" jsonschema:"Required together with agent_id when channel_id is given, to scope the answer to that channel's current binding; omit both for the adapter's generic capabilities."`
	AgentID       string `json:"agent_id,omitempty" jsonschema:"Required together with application_id when channel_id is given; omit both for the adapter's generic capabilities."`
}

type relayCapabilitiesSet struct {
	Receive             bool `json:"receive"`
	Ack                 bool `json:"ack"`
	BoundedWait         bool `json:"bounded_wait"`
	Presence            bool `json:"presence"`
	Wake                bool `json:"wake"`
	EventReplay         bool `json:"event_replay"`
	StructuredEnvelopes bool `json:"structured_envelopes"`
}

type relayCapabilitiesOutput struct {
	ContractVersion string               `json:"contract_version"`
	Adapter         string               `json:"adapter"`
	Capabilities    relayCapabilitiesSet `json:"capabilities"`
	WaitMsMax       int64                `json:"wait_ms_max"`
}

func (s *Server) handleRelayCapabilities(
	ctx context.Context,
	input relayCapabilitiesInput,
) (any, error) {
	result, err := s.relayProvider.Capabilities(ctx, relay.CapabilitiesParams{
		ChannelID: input.ChannelID, ApplicationID: input.ApplicationID, AgentID: input.AgentID,
	})
	if err != nil {
		return nil, relayErrorResult(err)
	}
	return relayCapabilitiesOutput{
		ContractVersion: relayContractVersion, Adapter: result.Adapter,
		Capabilities: relayCapabilitiesSet{
			Receive: result.Capabilities.Receive, Ack: result.Capabilities.Ack, BoundedWait: result.Capabilities.BoundedWait,
			Presence: result.Capabilities.Presence, Wake: result.Capabilities.Wake, EventReplay: result.Capabilities.EventReplay,
			StructuredEnvelopes: result.Capabilities.StructuredEnvelopes,
		},
		WaitMsMax: result.WaitMsMax,
	}, nil
}

// ── errors ──────────────────────────────────────────────────────────────

func relayErrorResult(err error) error {
	code := "relay_error"
	var ambiguous *relay.AmbiguousRecipientError
	switch {
	case errors.As(err, &ambiguous):
		code = "ambiguous_recipient"
	case errors.Is(err, channel.ErrNotFound), errors.Is(err, relay.ErrNotFound):
		code = "not_found"
	case errors.Is(err, channel.ErrInvalidRecord), errors.Is(err, relay.ErrInvalidRecord):
		code = "validation_failed"
	case errors.Is(err, relay.ErrUnauthorized):
		code = "unauthorized"
	case errors.Is(err, relay.ErrNotMember):
		code = "not_a_member"
	case errors.Is(err, relay.ErrIdempotencyConflict):
		code = "idempotency_conflict"
	}
	return toolErrorResult(code, err.Error())
}
