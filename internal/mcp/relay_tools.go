package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/channel"
	"github.com/hollis-labs/tangent/internal/relay"
)

// relay_tools.go is the cooperative MCP inbox (CW-20260906-0066): seven
// tools over internal/channel and internal/relay, implementing ADR 0006 §3
// and the CW-20260907-0016 spike's observed contract.
//
// The operator's own send/read path is deliberately not here. These seven
// tools are the agent-facing MCP surface only; a human operator has no MCP
// client. CW-20260906-0017's channel pane is where the operator side lives,
// over a REST API calling the same two Stores directly — this file's gap is
// that boundary, not an omission.
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
	_ *mcpsdk.CallToolRequest,
	input relayOpenChannelInput,
) (*mcpsdk.CallToolResult, any, error) {
	ch, operator, err := s.channels.OpenChannel(ctx, channel.CreateChannelParams{
		Title: input.Title, OwnerScope: standaloneLocalScope, ProjectRef: input.ProjectRef,
	})
	if err != nil {
		return relayErrorResult(err)
	}
	return nil, relayOpenChannelOutput{
		ContractVersion: relayContractVersion, ChannelID: ch.ID,
		OperatorParticipantID: operator.ID, CreatedAt: ch.CreatedAt,
	}, nil
}

// standaloneLocalScope is the same advisory, non-enforced scope every
// direct-loopback caller gets elsewhere in this host (ADR 0004 §3.4); relay
// channels carry it for the same reason surfaces do, not as an access
// boundary.
const standaloneLocalScope = "standalone-local:anonymous"

// ── tangent.relay_attach ────────────────────────────────────────────────

type relayRuntimeInput struct {
	Authority           string   `json:"authority"`
	EndpointRef         string   `json:"endpoint_ref,omitempty"`
	AdapterCapabilities []string `json:"adapter_capabilities,omitempty"`
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
	_ *mcpsdk.CallToolRequest,
	input relayAttachInput,
) (*mcpsdk.CallToolResult, any, error) {
	if input.ChannelID == "" {
		return relayErrorResult(fmt.Errorf("%w: channel_id is required", channel.ErrInvalidRecord))
	}
	if input.Runtime.Authority == "" {
		return relayErrorResult(fmt.Errorf("%w: runtime.authority is required", channel.ErrInvalidRecord))
	}
	if _, err := s.channels.GetChannel(ctx, input.ChannelID); err != nil {
		return relayErrorResult(err)
	}
	participant, err := s.resolveAgentParticipant(ctx, input.Source)
	if err != nil {
		return relayErrorResult(err)
	}
	_, err = s.channels.AddParticipant(ctx, input.ChannelID, participant.ID)
	if err != nil {
		return relayErrorResult(err)
	}
	capabilities, err := jsonRawArray(input.Runtime.AdapterCapabilities)
	if err != nil {
		return relayErrorResult(fmt.Errorf("%w: adapter_capabilities: %w", channel.ErrInvalidRecord, err))
	}
	binding, err := s.channels.Rebind(ctx, input.ChannelID, participant.ID, channel.RebindParams{
		RuntimeAuthority: input.Runtime.Authority, RuntimeEndpointRef: input.Runtime.EndpointRef,
		AdapterCapabilities: capabilities,
	})
	if err != nil {
		return relayErrorResult(err)
	}
	return nil, relayAttachOutput{
		ContractVersion: relayContractVersion, ParticipantID: participant.ID, ChannelID: input.ChannelID,
		Generation: binding.Generation, RuntimeAuthority: binding.RuntimeAuthority,
		RuntimeEndpointRef: binding.RuntimeEndpointRef, AttachedAt: binding.BoundAt,
	}, nil
}

func (s *Server) resolveAgentParticipant(ctx context.Context, source relaySourceInput) (channel.Participant, error) {
	if source.ApplicationID == "" || source.AgentID == "" {
		return channel.Participant{}, fmt.Errorf("%w: source.application_id and source.agent_id are both required", channel.ErrInvalidRecord)
	}
	return s.channels.UpsertParticipant(ctx, channel.UpsertParticipantParams{
		Kind: channel.ParticipantAgent, ExternalAuthority: source.ApplicationID, ExternalRef: source.AgentID,
	})
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
	_ *mcpsdk.CallToolRequest,
	input relayDetachInput,
) (*mcpsdk.CallToolResult, any, error) {
	if input.ChannelID == "" {
		return relayErrorResult(fmt.Errorf("%w: channel_id is required", channel.ErrInvalidRecord))
	}
	participant, err := s.resolveAgentParticipant(ctx, input.Source)
	if err != nil {
		return relayErrorResult(err)
	}
	detachedAt := time.Now().UTC()
	if err := s.channels.RemoveParticipant(ctx, input.ChannelID, participant.ID); err != nil {
		return relayErrorResult(err)
	}
	return nil, relayDetachOutput{
		ContractVersion: relayContractVersion, ParticipantID: participant.ID,
		ChannelID: input.ChannelID, DetachedAt: detachedAt,
	}, nil
}

// ── tangent.relay_send ──────────────────────────────────────────────────

type relaySendInput struct {
	ChannelID         string            `json:"channel_id"`
	IdempotencyKey    string            `json:"idempotency_key"`
	Source            relaySourceInput  `json:"source"`
	Recipient         *relaySourceInput `json:"recipient,omitempty"`
	SubjectID         string            `json:"subject_id,omitempty"`
	ReplyToExchangeID string            `json:"reply_to_exchange_id,omitempty"`
	Body              string            `json:"body"`
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
	_ *mcpsdk.CallToolRequest,
	input relaySendInput,
) (*mcpsdk.CallToolResult, any, error) {
	if input.ChannelID == "" || input.IdempotencyKey == "" || input.Body == "" {
		return relayErrorResult(fmt.Errorf("%w: channel_id, idempotency_key, and body are all required", channel.ErrInvalidRecord))
	}
	sender, err := s.resolveAgentParticipant(ctx, input.Source)
	if err != nil {
		return relayErrorResult(err)
	}
	var recipient channel.Participant
	if input.Recipient != nil {
		recipient, err = s.resolveAgentParticipant(ctx, *input.Recipient)
		if err != nil {
			return relayErrorResult(err)
		}
	} else {
		recipient, err = s.resolveDefaultRecipient(ctx, input.ChannelID)
		if err != nil {
			return relayErrorResult(err)
		}
	}

	exchange, err := s.relay.AcceptExchange(ctx, relay.AcceptExchangeParams{
		IdempotencyKey: input.IdempotencyKey, ChannelID: input.ChannelID,
		SubjectID: input.SubjectID, SenderParticipantID: sender.ID, RecipientParticipantID: recipient.ID,
		ReplyToExchangeID: input.ReplyToExchangeID, Body: input.Body,
	})
	if err != nil {
		return relayErrorResult(err)
	}
	// An operator recipient has no runtime binding at all — "current" would
	// be a vacuous true (both sides empty) that reads as "yes, live and
	// routable" for a destination that was never routed via a binding in
	// the first place. Only ask the question for an agent recipient.
	var current bool
	if recipient.Kind == channel.ParticipantAgent {
		current, err = s.relay.RecipientBindingCurrent(ctx, exchange.ID)
		if err != nil {
			return relayErrorResult(err)
		}
	}
	presence, err := s.relay.Presence(ctx, recipient.ID)
	if err != nil {
		return relayErrorResult(err)
	}
	return nil, relaySendOutput{
		ContractVersion: relayContractVersion, ExchangeID: exchange.ID, ChannelID: exchange.ChannelID,
		Sequence: exchange.Sequence, Sender: relayRefFor(sender), Recipient: relayRefFor(recipient),
		RecipientBindingCurrent: current, SubjectID: exchange.SubjectID, ReplyToExchangeID: exchange.ReplyToExchangeID,
		CreatedAt: exchange.CreatedAt, RecipientPresence: relayPresenceFor(presence),
	}, nil
}

// ambiguousRecipientError is CW-20260906-0066's review requirement: refuse
// rather than guess when a channel has zero or several live operator
// participants, naming the candidates rather than silently misrouting.
type ambiguousRecipientError struct {
	ChannelID    string
	CandidateIDs []string
}

func (e *ambiguousRecipientError) Error() string {
	if len(e.CandidateIDs) == 0 {
		return fmt.Sprintf("channel %s has no live operator participant to default to; recipient is required", e.ChannelID)
	}
	return fmt.Sprintf("channel %s has %d live operator participants (%s); recipient is required",
		e.ChannelID, len(e.CandidateIDs), strings.Join(e.CandidateIDs, ", "))
}

func (s *Server) resolveDefaultRecipient(ctx context.Context, channelID string) (channel.Participant, error) {
	members, err := s.channels.ListChannelParticipants(ctx, channelID)
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
		return channel.Participant{}, &ambiguousRecipientError{ChannelID: channelID, CandidateIDs: ids}
	}
	return operators[0], nil
}

// ── tangent.relay_receive ───────────────────────────────────────────────

type relayReceiveInput struct {
	ChannelID string           `json:"channel_id"`
	Source    relaySourceInput `json:"source"`
	Cursor    int64            `json:"cursor,omitempty"`
	WaitMs    int64            `json:"wait_ms,omitempty"`
	Limit     int64            `json:"limit,omitempty"`
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
	_ *mcpsdk.CallToolRequest,
	input relayReceiveInput,
) (*mcpsdk.CallToolResult, any, error) {
	if input.ChannelID == "" {
		return relayErrorResult(fmt.Errorf("%w: channel_id is required", channel.ErrInvalidRecord))
	}
	participant, err := s.resolveAgentParticipant(ctx, input.Source)
	if err != nil {
		return relayErrorResult(err)
	}
	limit := int(input.Limit)
	result, err := s.relay.Receive(ctx, relay.ReceiveParams{
		ChannelID: input.ChannelID, ParticipantID: participant.ID,
		Cursor: input.Cursor, Wait: time.Duration(input.WaitMs) * time.Millisecond, Limit: limit,
	})
	if err != nil {
		return relayErrorResult(err)
	}
	status := "ok"
	if result.TimedOut {
		status = "timeout"
	}
	items := make([]relayExchangeView, 0, len(result.Items))
	for _, exchange := range result.Items {
		sender, senderErr := s.channels.GetParticipant(ctx, exchange.SenderParticipantID)
		if senderErr != nil {
			return relayErrorResult(senderErr)
		}
		items = append(items, relayExchangeView{
			ExchangeID: exchange.ID, Sequence: exchange.Sequence, Sender: relayRefFor(sender),
			SubjectID: exchange.SubjectID, ReplyToExchangeID: exchange.ReplyToExchangeID,
			Body: exchange.Body, CreatedAt: exchange.CreatedAt,
		})
	}
	return nil, relayReceiveOutput{
		ContractVersion: relayContractVersion, Status: status, Items: items,
		NextCursor: result.NextCursor, Presence: relayPresenceFor(result.Presence),
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
	_ *mcpsdk.CallToolRequest,
	input relayAckInput,
) (*mcpsdk.CallToolResult, any, error) {
	if input.ExchangeID == "" {
		return relayErrorResult(fmt.Errorf("%w: exchange_id is required", channel.ErrInvalidRecord))
	}
	participant, err := s.resolveAgentParticipant(ctx, input.Source)
	if err != nil {
		return relayErrorResult(err)
	}
	exchange, err := s.relay.GetExchange(ctx, input.ExchangeID)
	if err != nil {
		return relayErrorResult(err)
	}
	if exchange.RecipientParticipantID != participant.ID {
		return relayErrorResult(fmt.Errorf("%w: %s is not the recipient of %s", relay.ErrUnauthorized, participant.ID, exchange.ID))
	}
	existing, err := s.relay.GetRead(ctx, input.ExchangeID, participant.ID)
	alreadyAcked := err == nil
	if err != nil && !errors.Is(err, relay.ErrNotFound) {
		return relayErrorResult(err)
	}
	receipt, err := s.relay.RecordRead(ctx, input.ExchangeID, participant.ID)
	if err != nil {
		return relayErrorResult(err)
	}
	if alreadyAcked {
		receipt = existing
	}
	return nil, relayAckOutput{
		ContractVersion: relayContractVersion, ExchangeID: receipt.ExchangeID,
		ParticipantID: receipt.ParticipantID, AckedAt: receipt.AckedAt, AlreadyAcked: alreadyAcked,
	}, nil
}

// ── tangent.relay_capabilities ──────────────────────────────────────────

type relayCapabilitiesInput struct {
	ChannelID string            `json:"channel_id,omitempty"`
	Source    *relaySourceInput `json:"source,omitempty"`
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
	_ *mcpsdk.CallToolRequest,
	input relayCapabilitiesInput,
) (*mcpsdk.CallToolResult, any, error) {
	// The pairing rule lives here, in code, rather than as a schema
	// conditional: CW-20260907-0016 found mux's discovery schema drops
	// allOf/if/then branches, so a field gated behind one reaches the model
	// untyped. Both fields stay independently optional in the schema.
	if input.ChannelID != "" {
		if input.Source == nil {
			return relayErrorResult(fmt.Errorf("%w: source is required when channel_id is given", channel.ErrInvalidRecord))
		}
		if _, err := s.channels.GetChannel(ctx, input.ChannelID); err != nil {
			return relayErrorResult(err)
		}
		if _, err := s.resolveAgentParticipant(ctx, *input.Source); err != nil {
			return relayErrorResult(err)
		}
	}
	return nil, relayCapabilitiesOutput{
		ContractVersion: relayContractVersion, Adapter: "cli-relay",
		Capabilities: relayCapabilitiesSet{
			Receive: true, Ack: true, BoundedWait: true, Presence: true,
			Wake: false, EventReplay: false, StructuredEnvelopes: false,
		},
		WaitMsMax: int64(relay.MaximumReceiveWait / time.Millisecond),
	}, nil
}

// ── errors ──────────────────────────────────────────────────────────────

func relayErrorResult(err error) (*mcpsdk.CallToolResult, any, error) {
	code := "relay_error"
	var ambiguous *ambiguousRecipientError
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
	return toolErrorResult(code, err.Error()), nil, nil
}

func jsonRawArray(values []string) ([]byte, error) {
	if len(values) == 0 {
		return []byte("[]"), nil
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}
