// Package turns implements Tangent's second durable FIFO operator inbox for live agent turns
// (CW-20260913-0019). It provides strict arrival order presentation for agent questions,
// approvals, checkpoints, failures, and completions, along with a durable reply routing
// path back to Tether and originating sessions.
package turns

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	jsonschemav6 "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/interaction"
)

const (
	ContractVersion   = extensions.AgentTurnContractVersion
	DefinitionVersion = extensions.AgentTurnDefinitionVersion
	DefaultSurfaceID  = "surface_turns_default"
	InboxURL          = "/turns"
)

const (
	// DefaultAwaitWait is how long Await waits when the caller names no limit.
	DefaultAwaitWait = 30 * time.Second
	// MaximumAwaitWait is the longest a single Await may hold a call open. It
	// matches HITL's ceiling: a caller that needs longer calls again, and
	// nothing is lost between calls because the reply is durable.
	MaximumAwaitWait = 50 * time.Second

	defaultAwaitPoll = 500 * time.Millisecond

	// AwaitStatusReplies means Await returned at least one undelivered reply.
	AwaitStatusReplies = "replies"
	// AwaitStatusTimeout means the wait elapsed with nothing to deliver.
	AwaitStatusTimeout = "timeout"
)

var (
	ErrInvalidRequest   = errors.New("turns: invalid request")
	ErrTerminalConflict = errors.New("turns: another terminal outcome won")
	ErrNotFound         = errors.New("turns: turn not found")
	ErrStaleRevision    = errors.New("turns: stale revision conflict")
)

var OperatorParticipant = interaction.ActorBinding{
	Scope:        "operator:local",
	PrincipalRef: "local-operator",
	Authority:    "tangent-loopback",
	Assurance:    "loopback-unverified",
}

type AgentTurnSource struct {
	AgentID       string `json:"agent_id"`
	ApplicationID string `json:"application_id,omitempty"`
	AgentLabel    string `json:"agent_label,omitempty"`
}

type AgentTurnOption struct {
	Label       string `json:"label"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

type AgentTurnRequest struct {
	ContractVersion string            `json:"contract_version"`
	TurnID          string            `json:"turn_id"`
	SessionID       string            `json:"session_id"`
	IdempotencyKey  string            `json:"idempotency_key"`
	Kind            string            `json:"kind"`
	Source          AgentTurnSource   `json:"source"`
	Title           string            `json:"title"`
	Summary         string            `json:"summary,omitempty"`
	Content         string            `json:"content"`
	Options         []AgentTurnOption `json:"options,omitempty"`
	Correlations    map[string]any    `json:"correlations,omitempty"`
	ExpiresAt       string            `json:"expires_at,omitempty"`
}

type TurnHandle struct {
	ContractVersion string                       `json:"contract_version"`
	SurfaceID       string                       `json:"surface_id"`
	ItemID          string                       `json:"item_id"`
	TurnID          string                       `json:"turn_id"`
	SessionID       string                       `json:"session_id"`
	AgentID         string                       `json:"agent_id"`
	Kind            string                       `json:"kind"`
	State           interaction.InteractionState `json:"state"`
	Revision        int64                        `json:"revision"`
	QueueSequence   int64                        `json:"queue_sequence"`
	QueuePosition   *int64                       `json:"queue_position,omitempty"`
	CreatedAt       time.Time                    `json:"created_at"`
}

type AgentTurnResponse struct {
	Action         string `json:"action"`
	ResponseText   string `json:"response_text,omitempty"`
	SelectedOption string `json:"selected_option,omitempty"`
	Note           string `json:"note,omitempty"`
}

type TurnResolution struct {
	ResolutionID   string    `json:"resolution_id"`
	Action         string    `json:"action"`
	ResponseText   string    `json:"response_text,omitempty"`
	SelectedOption string    `json:"selected_option,omitempty"`
	Note           string    `json:"note,omitempty"`
	ResolvedAt     time.Time `json:"resolved_at"`
	ResolvedBy     string    `json:"resolved_by"`
}

type TurnItemView struct {
	ContractVersion string                       `json:"contract_version"`
	ItemID          string                       `json:"item_id"`
	TurnID          string                       `json:"turn_id"`
	SessionID       string                       `json:"session_id"`
	AgentID         string                       `json:"agent_id"`
	AgentLabel      string                       `json:"agent_label,omitempty"`
	ApplicationID   string                       `json:"application_id,omitempty"`
	Kind            string                       `json:"kind"`
	Title           string                       `json:"title"`
	Summary         string                       `json:"summary,omitempty"`
	Content         string                       `json:"content"`
	Options         []AgentTurnOption            `json:"options,omitempty"`
	Correlations    map[string]any               `json:"correlations,omitempty"`
	State           interaction.InteractionState `json:"state"`
	QueueSequence   int64                        `json:"queue_sequence"`
	QueuePosition   *int64                       `json:"queue_position,omitempty"`
	Revision        int64                        `json:"revision"`
	CreatedAt       time.Time                    `json:"created_at"`
	UpdatedAt       time.Time                    `json:"updated_at"`
	ExpiresAt       *time.Time                   `json:"expires_at,omitempty"`
	Resolution      *TurnResolution              `json:"resolution,omitempty"`
	DeliveryState   interaction.DeliveryState    `json:"delivery_state"`
}

type TurnsInbox struct {
	ContractVersion string         `json:"contract_version"`
	SurfaceID       string         `json:"surface_id"`
	Revision        string         `json:"revision"`
	SyncedAt        time.Time      `json:"synced_at"`
	Pending         []TurnItemView `json:"pending"`
	History         []TurnItemView `json:"history"`
	TotalPending    int            `json:"total_pending"`
	TotalTerminal   int            `json:"total_terminal"`
}

type EnqueueInput struct {
	Request json.RawMessage
	Caller  interaction.ActorBinding
}

type ReplyInput struct {
	ItemID           string
	ExpectedRevision int64
	Action           string
	ResponseText     string
	SelectedOption   string
	Note             string
}

type DismissInput struct {
	ItemID           string
	ExpectedRevision int64
	Reason           string
}

type AckInput struct {
	ItemID  string
	ReplyID string
}

// AwaitInput asks for the operator replies a session has not yet acknowledged.
// A nil Wait takes DefaultAwaitWait; zero means look once and return.
type AwaitInput struct {
	SessionID string
	Wait      *time.Duration
}

// AwaitResult is what Await returns. Replies is never nil, so a timeout
// serializes as an empty list rather than null.
type AwaitResult struct {
	ContractVersion string         `json:"contract_version"`
	SessionID       string         `json:"session_id"`
	WaitStatus      string         `json:"wait_status"`
	Replies         []TurnItemView `json:"replies"`
}

type Service struct {
	interactions  *interaction.Service
	requestSchema *jsonschemav6.Schema
	now           func() time.Time
	awaitPoll     time.Duration
}

// Option configures a Service.
type Option func(*Service)

// WithAwaitPollInterval sets how often Await re-reads the inbox while it waits.
func WithAwaitPollInterval(interval time.Duration) Option {
	return func(s *Service) {
		if interval > 0 {
			s.awaitPoll = interval
		}
	}
}

func NewService(interactions *interaction.Service, opts ...Option) (*Service, error) {
	if interactions == nil {
		return nil, fmt.Errorf("%w: interaction service is required", ErrInvalidRequest)
	}
	var schemaDocument any
	if err := json.Unmarshal(extensions.AgentTurnContractSchema(), &schemaDocument); err != nil {
		return nil, fmt.Errorf("%w: decode embedded turn schema: %w", ErrInvalidRequest, err)
	}
	compiler := jsonschemav6.NewCompiler()
	const schemaURI = "memory://tangent/agent-turn-v1.schema.json"
	if err := compiler.AddResource(schemaURI, schemaDocument); err != nil {
		return nil, fmt.Errorf("%w: load embedded turn schema: %w", ErrInvalidRequest, err)
	}
	requestSchema, err := compiler.Compile(schemaURI)
	if err != nil {
		return nil, fmt.Errorf("%w: compile embedded turn schema: %w", ErrInvalidRequest, err)
	}
	svc := &Service{
		interactions:  interactions,
		requestSchema: requestSchema,
		now:           func() time.Time { return time.Now().UTC() },
		awaitPoll:     defaultAwaitPoll,
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc, nil
}

func (s *Service) Enqueue(ctx context.Context, input EnqueueInput) (TurnHandle, error) {
	if len(input.Request) == 0 {
		return TurnHandle{}, fmt.Errorf("%w: request body is empty", ErrInvalidRequest)
	}
	var schemaValue any
	if err := json.Unmarshal(input.Request, &schemaValue); err != nil {
		return TurnHandle{}, fmt.Errorf("%w: invalid JSON: %w", ErrInvalidRequest, err)
	}
	if err := s.requestSchema.Validate(schemaValue); err != nil {
		return TurnHandle{}, fmt.Errorf("%w: turn validation failed: %w", ErrInvalidRequest, err)
	}

	var req AgentTurnRequest
	if err := json.Unmarshal(input.Request, &req); err != nil {
		return TurnHandle{}, fmt.Errorf("%w: unmarshal request: %w", ErrInvalidRequest, err)
	}

	if err := s.ensureDefaultSurface(ctx); err != nil {
		return TurnHandle{}, err
	}

	// Submit interaction to durable surface
	policyMap := map[string]any{
		"session_id": req.SessionID,
		"turn_id":    req.TurnID,
		"agent_id":   req.Source.AgentID,
	}
	if req.ExpiresAt != "" {
		policyMap["expires_at"] = req.ExpiresAt
	}
	policy, _ := json.Marshal(policyMap)

	submitHandle, err := s.interactions.SubmitInteraction(ctx, interaction.SubmitInteractionInput{
		SurfaceID:      DefaultSurfaceID,
		Caller:         input.Caller,
		IdempotencyKey: req.IdempotencyKey,
		Definition: interaction.DefinitionRef{
			Kind:    extensions.AgentTurnEnvelopeType,
			Version: DefinitionVersion,
		},
		Request:    input.Request,
		Policy:     policy,
		Capability: ReservedSurfaceCapability,
	})
	if errors.Is(err, interaction.ErrIdempotencyConflict) {
		existing, found, lookupErr := s.interactions.FindInteractionByIdempotency(
			ctx, input.Caller, req.IdempotencyKey, ReservedSurfaceCapability,
		)
		if lookupErr != nil {
			return TurnHandle{}, lookupErr
		}
		if found {
			return s.itemHandle(existing, &req)
		}
	}
	if err != nil {
		return TurnHandle{}, err
	}

	// Immediately transition staged item to presented for the operator
	current, loadErr := s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
		InteractionID:  submitHandle.InteractionID,
		RequesterScope: input.Caller.Scope,
		Capability:     ReservedSurfaceCapability,
	})
	if loadErr != nil {
		return TurnHandle{}, loadErr
	}

	if current.Interaction.State == interaction.InteractionStateStaged {
		_, presentErr := s.interactions.AcknowledgePresentation(ctx, interaction.PresentInteractionInput{
			InteractionID:               current.Interaction.ID,
			ExpectedRevision:            current.Interaction.Revision,
			PresentedProjectionRevision: 1,
			Participant:                 OperatorParticipant,
			ConnectionID:                "turns-auto-present",
			Capability:                  ReservedSurfaceCapability,
		})
		if presentErr == nil {
			current, _ = s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
				InteractionID:  submitHandle.InteractionID,
				RequesterScope: input.Caller.Scope,
				Capability:     ReservedSurfaceCapability,
			})
		}
	}

	return s.itemHandle(current.Interaction, &req)
}

func (s *Service) Inbox(ctx context.Context) (TurnsInbox, error) {
	result := TurnsInbox{
		ContractVersion: ContractVersion,
		SurfaceID:       DefaultSurfaceID,
		Revision:        "empty",
		SyncedAt:        s.now(),
		Pending:         make([]TurnItemView, 0),
		History:         make([]TurnItemView, 0),
	}
	snapshot, err := s.interactions.GetSurface(ctx, interaction.GetSurfaceInput{
		SurfaceID:      DefaultSurfaceID,
		RequesterScope: OperatorParticipant.Scope,
		Capability:     ReservedSurfaceCapability,
	})
	if errors.Is(err, interaction.ErrNotFound) {
		return result, nil
	}
	if err != nil {
		return TurnsInbox{}, err
	}

	resolutions := make(map[string]*interaction.ResolutionRecord, len(snapshot.Resolutions))
	for i := range snapshot.Resolutions {
		res := &snapshot.Resolutions[i]
		resolutions[res.InteractionID] = res
	}

	deliveries := make(map[string]interaction.DeliveryState, len(snapshot.ResolutionDeliveries))
	for _, del := range snapshot.ResolutionDeliveries {
		deliveries[del.ResolutionID] = del.State
	}

	digest := sha256.New()
	_, _ = fmt.Fprintf(digest, "%s:%d", snapshot.Surface.ID, snapshot.Surface.Revision)

	pendingPosition := int64(0)
	for _, record := range snapshot.Interactions {
		if record.Definition.Kind != extensions.AgentTurnEnvelopeType {
			continue
		}
		_, _ = fmt.Fprintf(digest, "|%s:%d:%s:%d", record.ID, record.SurfaceSequence, record.State, record.Revision)

		var position *int64
		if !isTerminal(record.State) {
			pendingPosition++
			p := pendingPosition
			position = &p
		}

		res := resolutions[record.ID]
		delState := interaction.DeliveryStateQueued
		if res != nil {
			if ds, ok := deliveries[res.ID]; ok {
				delState = ds
			}
		}

		view, viewErr := buildItemView(record, res, delState, position)
		if viewErr != nil {
			continue
		}

		if isTerminal(record.State) {
			result.History = append(result.History, view)
		} else {
			result.Pending = append(result.Pending, view)
		}
	}

	// Sort Pending strictly by SurfaceSequence ASC (strict arrival FIFO)
	sort.SliceStable(result.Pending, func(i, j int) bool {
		return result.Pending[i].QueueSequence < result.Pending[j].QueueSequence
	})

	// Sort History by UpdatedAt DESC (most recent terminal first)
	sort.SliceStable(result.History, func(i, j int) bool {
		return result.History[i].UpdatedAt.After(result.History[j].UpdatedAt)
	})

	result.TotalPending = len(result.Pending)
	result.TotalTerminal = len(result.History)
	result.Revision = hex.EncodeToString(digest.Sum(nil))
	return result, nil
}

func (s *Service) InspectTurn(ctx context.Context, itemID string) (TurnItemView, error) {
	record, err := s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
		InteractionID:  itemID,
		RequesterScope: OperatorParticipant.Scope,
		Capability:     ReservedSurfaceCapability,
	})
	if err != nil {
		return TurnItemView{}, err
	}
	if record.Interaction.Definition.Kind != extensions.AgentTurnEnvelopeType {
		return TurnItemView{}, ErrNotFound
	}
	delState := interaction.DeliveryStateQueued
	if len(record.ResolutionDeliveries) > 0 {
		delState = record.ResolutionDeliveries[0].State
	}
	return buildItemView(record.Interaction, record.Resolution, delState, nil)
}

func (s *Service) Reply(ctx context.Context, input ReplyInput) (TurnItemView, error) {
	if input.ItemID == "" {
		return TurnItemView{}, fmt.Errorf("%w: item_id is required", ErrInvalidRequest)
	}
	if input.ExpectedRevision < 1 {
		return TurnItemView{}, fmt.Errorf("%w: expected_revision must be positive", ErrInvalidRequest)
	}
	action := strings.TrimSpace(input.Action)
	if action == "" {
		action = "respond"
	}

	current, err := s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
		InteractionID:  input.ItemID,
		RequesterScope: OperatorParticipant.Scope,
		Capability:     ReservedSurfaceCapability,
	})
	if err != nil {
		return TurnItemView{}, err
	}
	if current.Interaction.Definition.Kind != extensions.AgentTurnEnvelopeType {
		return TurnItemView{}, ErrNotFound
	}
	if current.Interaction.Revision != input.ExpectedRevision {
		return TurnItemView{}, fmt.Errorf("%w: expected %d, got %d", ErrStaleRevision, input.ExpectedRevision, current.Interaction.Revision)
	}
	if isTerminal(current.Interaction.State) {
		return TurnItemView{}, ErrTerminalConflict
	}

	responsePayload, err := json.Marshal(AgentTurnResponse{
		Action:         action,
		ResponseText:   input.ResponseText,
		SelectedOption: input.SelectedOption,
		Note:           input.Note,
	})
	if err != nil {
		return TurnItemView{}, fmt.Errorf("%w: marshal response: %w", ErrInvalidRequest, err)
	}

	projRev := int64(1)
	if current.Interaction.PresentedProjectionRevision != nil {
		projRev = *current.Interaction.PresentedProjectionRevision
	}

	_, err = s.interactions.ResolveInteraction(ctx, interaction.ResolveInteractionInput{
		InteractionID:               input.ItemID,
		ExpectedInteractionRevision: input.ExpectedRevision,
		PresentedProjectionRevision: projRev,
		Participant:                 OperatorParticipant,
		ResponseKind:                "data",
		ResponsePayload:             responsePayload,
		Capability:                  ReservedSurfaceCapability,
	})
	if err != nil {
		return TurnItemView{}, err
	}

	return s.InspectTurn(ctx, input.ItemID)
}

func (s *Service) Dismiss(ctx context.Context, input DismissInput) (TurnItemView, error) {
	if input.ItemID == "" {
		return TurnItemView{}, fmt.Errorf("%w: item_id is required", ErrInvalidRequest)
	}
	current, err := s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
		InteractionID:  input.ItemID,
		RequesterScope: OperatorParticipant.Scope,
		Capability:     ReservedSurfaceCapability,
	})
	if err != nil {
		return TurnItemView{}, err
	}
	if current.Interaction.Definition.Kind != extensions.AgentTurnEnvelopeType {
		return TurnItemView{}, ErrNotFound
	}
	if isTerminal(current.Interaction.State) {
		return TurnItemView{}, ErrTerminalConflict
	}

	reason := input.Reason
	if reason == "" {
		reason = "dismissed by operator"
	}

	exp := input.ExpectedRevision
	if exp < 1 {
		exp = current.Interaction.Revision
	}

	_, err = s.interactions.CancelInteraction(ctx, interaction.CancelInteractionInput{
		InteractionID:    input.ItemID,
		ExpectedRevision: exp,
		Requester:        OperatorParticipant,
		Cause:            interaction.TerminalCauseParticipantCanceled,
		Reason:           reason,
		Capability:       ReservedSurfaceCapability,
	})
	if err != nil {
		return TurnItemView{}, err
	}
	return s.InspectTurn(ctx, input.ItemID)
}

func (s *Service) Ack(ctx context.Context, input AckInput) error {
	if input.ItemID == "" {
		return fmt.Errorf("%w: item_id is required", ErrInvalidRequest)
	}
	// Verify interaction exists
	current, err := s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
		InteractionID:  input.ItemID,
		RequesterScope: OperatorParticipant.Scope,
		Capability:     ReservedSurfaceCapability,
	})
	if err != nil {
		return err
	}
	if current.Interaction.Definition.Kind != extensions.AgentTurnEnvelopeType {
		return ErrNotFound
	}
	// A turn nobody has answered has nothing to acknowledge. Saying so here,
	// rather than letting the store fail to find a resolution, keeps the caller
	// from reading "not found" as "the item does not exist".
	if current.Resolution == nil {
		return fmt.Errorf("%w: turn %s has no reply to acknowledge", ErrInvalidRequest, input.ItemID)
	}
	// reply_id is optional, but a caller that names one is asserting which
	// reply it delivered, and acknowledging a different one silently would
	// defeat the point of naming it.
	if input.ReplyID != "" && input.ReplyID != current.Resolution.ID {
		return fmt.Errorf("%w: reply_id does not match the reply recorded for turn %s", ErrInvalidRequest, input.ItemID)
	}
	// Record delivery outcome to acknowledged
	return s.interactions.RecordDeliveryOutcome(ctx, interaction.RecordDeliveryOutcomeInput{
		InteractionID:      input.ItemID,
		Outcome:            interaction.DeliveryStateAcknowledged,
		RuntimeAuthority:   "tether",
		RuntimeEndpointRef: "session-delivery",
		Capability:         ReservedSurfaceCapability,
	})
}

// SessionReplies returns all resolved turns for a given session ID (durable pull for Tether).
func (s *Service) SessionReplies(ctx context.Context, sessionID string) ([]TurnItemView, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("%w: session_id is required", ErrInvalidRequest)
	}
	inbox, err := s.Inbox(ctx)
	if err != nil {
		return nil, err
	}
	var matches []TurnItemView
	for _, item := range inbox.History {
		if item.SessionID == sessionID && item.Resolution != nil {
			matches = append(matches, item)
		}
	}
	return matches, nil
}

// Await returns the operator replies a session has not yet acknowledged,
// waiting up to input.Wait for one to arrive.
//
// It is the long-poll form of SessionReplies, and it differs in one way that
// matters: it leaves out replies already acknowledged. SessionReplies is a
// history read and returns every reply; an Await that did the same would return
// at once forever after the first reply, and a caller looping on it would spin.
// Withholding acknowledged replies is what makes await → handle → ack a loop
// that ends, and leaving unacknowledged ones in is what makes delivery
// at-least-once: a caller that dies between receiving and acking sees the reply
// again.
//
// A timeout is a normal result, not an error, and changes no lifecycle state.
func (s *Service) Await(ctx context.Context, input AwaitInput) (AwaitResult, error) {
	if input.SessionID == "" {
		return AwaitResult{}, fmt.Errorf("%w: session_id is required", ErrInvalidRequest)
	}
	wait := DefaultAwaitWait
	if input.Wait != nil {
		wait = *input.Wait
	}
	if wait < 0 || wait > MaximumAwaitWait {
		return AwaitResult{}, fmt.Errorf("%w: wait must be between 0 and %d milliseconds",
			ErrInvalidRequest, MaximumAwaitWait.Milliseconds())
	}

	var deadline <-chan time.Time
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		deadline = timer.C
	}
	ticker := time.NewTicker(s.awaitPoll)
	defer ticker.Stop()

	for {
		replies, err := s.undeliveredReplies(ctx, input.SessionID)
		if err != nil {
			return AwaitResult{}, err
		}
		if len(replies) > 0 || wait == 0 {
			return s.awaitResult(input.SessionID, replies), nil
		}
		select {
		case <-ctx.Done():
			return AwaitResult{}, ctx.Err()
		case <-deadline:
			// One last look, so a reply recorded in the final poll interval is
			// delivered rather than reported as a timeout.
			replies, err = s.undeliveredReplies(ctx, input.SessionID)
			if err != nil {
				return AwaitResult{}, err
			}
			return s.awaitResult(input.SessionID, replies), nil
		case <-ticker.C:
		}
	}
}

func (s *Service) awaitResult(sessionID string, replies []TurnItemView) AwaitResult {
	status := AwaitStatusTimeout
	if len(replies) > 0 {
		status = AwaitStatusReplies
	}
	if replies == nil {
		replies = make([]TurnItemView, 0)
	}
	return AwaitResult{
		ContractVersion: ContractVersion,
		SessionID:       sessionID,
		WaitStatus:      status,
		Replies:         replies,
	}
}

// undeliveredReplies lists a session's answered turns whose reply still needs
// delivering, in the order the operator answered them.
//
// terminal_failure is excluded along with acknowledged: it records that the
// runtime confirmed the session gone, and the reply is kept in history rather
// than offered to whoever asks next.
func (s *Service) undeliveredReplies(ctx context.Context, sessionID string) ([]TurnItemView, error) {
	inbox, err := s.Inbox(ctx)
	if err != nil {
		return nil, err
	}
	var out []TurnItemView
	for _, item := range inbox.History {
		if item.SessionID != sessionID || item.Resolution == nil {
			continue
		}
		switch item.DeliveryState {
		case interaction.DeliveryStateAcknowledged, interaction.DeliveryStateTerminalFailure:
			continue
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Resolution.ResolvedAt.Equal(out[j].Resolution.ResolvedAt) {
			return out[i].Resolution.ResolvedAt.Before(out[j].Resolution.ResolvedAt)
		}
		return out[i].QueueSequence < out[j].QueueSequence
	})
	return out, nil
}

func (s *Service) ensureDefaultSurface(ctx context.Context) error {
	_, err := s.interactions.OpenSurface(ctx, interaction.OpenSurfaceInput{
		ID:             DefaultSurfaceID,
		Caller:         OperatorParticipant,
		IdempotencyKey: "turns-default-surface-v1",
		OwnerScope:     OperatorParticipant.Scope,
		Metadata:       json.RawMessage(`{"purpose":"agent_turns_fifo_inbox"}`),
		Capability:     ReservedSurfaceCapability,
	})
	return err
}

func (s *Service) itemHandle(rec interaction.InteractionRecord, req *AgentTurnRequest) (TurnHandle, error) {
	turnID := ""
	sessionID := ""
	agentID := ""
	kind := ""
	if req != nil {
		turnID = req.TurnID
		sessionID = req.SessionID
		agentID = req.Source.AgentID
		kind = req.Kind
	} else {
		var r AgentTurnRequest
		if err := json.Unmarshal(rec.RequestSnapshot, &r); err == nil {
			turnID = r.TurnID
			sessionID = r.SessionID
			agentID = r.Source.AgentID
			kind = r.Kind
		}
	}
	return TurnHandle{
		ContractVersion: ContractVersion,
		SurfaceID:       rec.SurfaceID,
		ItemID:          rec.ID,
		TurnID:          turnID,
		SessionID:       sessionID,
		AgentID:         agentID,
		Kind:            kind,
		State:           rec.State,
		Revision:        rec.Revision,
		QueueSequence:   rec.SurfaceSequence,
		CreatedAt:       rec.CreatedAt,
	}, nil
}

func buildItemView(rec interaction.InteractionRecord, res *interaction.ResolutionRecord, deliveryState interaction.DeliveryState, position *int64) (TurnItemView, error) {
	var req AgentTurnRequest
	if err := json.Unmarshal(rec.RequestSnapshot, &req); err != nil {
		return TurnItemView{}, err
	}

	var expires *time.Time
	if req.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, req.ExpiresAt); err == nil {
			expires = &t
		}
	}

	var resolution *TurnResolution
	if res != nil {
		var resp AgentTurnResponse
		_ = json.Unmarshal(res.ResponsePayload, &resp)
		resolution = &TurnResolution{
			ResolutionID:   res.ID,
			Action:         resp.Action,
			ResponseText:   resp.ResponseText,
			SelectedOption: resp.SelectedOption,
			Note:           resp.Note,
			ResolvedAt:     res.RecordedAt,
			ResolvedBy:     res.ParticipantRef,
		}
	}

	return TurnItemView{
		ContractVersion: ContractVersion,
		ItemID:          rec.ID,
		TurnID:          req.TurnID,
		SessionID:       req.SessionID,
		AgentID:         req.Source.AgentID,
		AgentLabel:      req.Source.AgentLabel,
		ApplicationID:   req.Source.ApplicationID,
		Kind:            req.Kind,
		Title:           req.Title,
		Summary:         req.Summary,
		Content:         req.Content,
		Options:         req.Options,
		Correlations:    req.Correlations,
		State:           rec.State,
		QueueSequence:   rec.SurfaceSequence,
		QueuePosition:   position,
		Revision:        rec.Revision,
		CreatedAt:       rec.CreatedAt,
		UpdatedAt:       rec.UpdatedAt,
		ExpiresAt:       expires,
		Resolution:      resolution,
		DeliveryState:   deliveryState,
	}, nil
}

func isTerminal(state interaction.InteractionState) bool {
	switch state {
	case interaction.InteractionStateResolved,
		interaction.InteractionStateCanceled,
		interaction.InteractionStateExpired,
		interaction.InteractionStateFailed,
		interaction.InteractionStateSuperseded:
		return true
	default:
		return false
	}
}
