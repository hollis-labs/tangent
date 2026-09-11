// Package hitl implements Tangent's durable, operator-owned HITL inbox over
// the workflow-neutral interaction substrate. It owns the v1 HITL contract
// semantics and projections, but no transport or browser lifecycle.
package hitl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	jsonschemav6 "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/interaction"
)

const (
	// ContractVersion is the payload contract carried in every v1 message.
	// DefinitionVersion pins the interaction's definition. They were one
	// constant until ADR 0009 bumped the manifests and separated them; see
	// extensions.HITLItemContractVersion for why conflating them was a defect
	// rather than a convenience.
	ContractVersion   = extensions.HITLItemContractVersion
	DefinitionVersion = extensions.HITLItemDefinitionVersion
	DefaultSurfaceID  = "surface_hitl_default"
	InboxURL          = "/hitl"
	defaultWait       = 30 * time.Second
	maximumWait       = 50 * time.Second
	maximumRequest    = 512 * 1024
)

var (
	ErrInvalidRequest   = errors.New("hitl: invalid request")
	ErrTerminalConflict = errors.New("hitl: another terminal outcome won")
)

var OperatorParticipant = interaction.ActorBinding{
	Scope:        "operator:local",
	PrincipalRef: "local-operator",
	Authority:    "tangent-loopback",
	Assurance:    "loopback-unverified",
}

type Service struct {
	interactions   *interaction.Service
	requestSchema  *jsonschemav6.Schema
	now            func() time.Time
	beforeSubmit   func()
	afterInboxLoad func()
}

func NewService(interactions *interaction.Service) (*Service, error) {
	if interactions == nil {
		return nil, fmt.Errorf("%w: interaction service is required", ErrInvalidRequest)
	}
	var schemaDocument any
	if err := json.Unmarshal(extensions.HITLItemContractSchema(), &schemaDocument); err != nil {
		return nil, fmt.Errorf("%w: decode embedded request schema: %w", ErrInvalidRequest, err)
	}
	compiler := jsonschemav6.NewCompiler()
	const schemaURI = "memory://tangent/hitl-item-v1.schema.json"
	if err := compiler.AddResource(schemaURI, schemaDocument); err != nil {
		return nil, fmt.Errorf("%w: load embedded request schema: %w", ErrInvalidRequest, err)
	}
	requestSchema, err := compiler.Compile(schemaURI)
	if err != nil {
		return nil, fmt.Errorf("%w: compile embedded request schema: %w", ErrInvalidRequest, err)
	}
	return &Service{
		interactions:  interactions,
		requestSchema: requestSchema,
		now:           func() time.Time { return time.Now().UTC() },
	}, nil
}

type EnqueueInput struct {
	Request json.RawMessage
	Caller  interaction.ActorBinding
}

type ItemHandle struct {
	ContractVersion string                       `json:"contract_version"`
	SurfaceID       string                       `json:"surface_id"`
	ItemID          string                       `json:"item_id"`
	State           interaction.InteractionState `json:"state"`
	Revision        int64                        `json:"revision"`
	QueueSequence   int64                        `json:"queue_sequence"`
	QueuePosition   *int64                       `json:"queue_position"`
	InboxURL        string                       `json:"inbox_url"`
	ItemURL         string                       `json:"item_url"`
}

type ParticipantCapture struct {
	PrincipalRef string `json:"principal_ref"`
	Authority    string `json:"authority"`
	Assurance    string `json:"assurance"`
}

type ResolutionView struct {
	ResolutionID                string             `json:"resolution_id"`
	Response                    json.RawMessage    `json:"response"`
	Participant                 ParticipantCapture `json:"participant"`
	ResolvedAt                  time.Time          `json:"resolved_at"`
	InteractionRevision         int64              `json:"interaction_revision"`
	PresentedProjectionRevision int64              `json:"presented_projection_revision"`
}

type TerminalOutcome struct {
	ContractVersion     string                       `json:"contract_version"`
	State               interaction.InteractionState `json:"state"`
	ItemID              string                       `json:"item_id"`
	InteractionRevision int64                        `json:"interaction_revision"`
	Resolution          *ResolutionView              `json:"resolution,omitempty"`
	Cause               interaction.TerminalCause    `json:"cause,omitempty"`
	Reason              string                       `json:"reason,omitempty"`
	PolicyRef           string                       `json:"policy_ref,omitempty"`
	ErrorCode           string                       `json:"error_code,omitempty"`
	Message             string                       `json:"message,omitempty"`
	ReplacementItemID   string                       `json:"replacement_item_id,omitempty"`
	TerminatedAt        *time.Time                   `json:"terminated_at,omitempty"`
}

type ItemView struct {
	ContractVersion string                       `json:"contract_version"`
	SurfaceID       string                       `json:"surface_id"`
	ItemID          string                       `json:"item_id"`
	State           interaction.InteractionState `json:"state"`
	Revision        int64                        `json:"revision"`
	QueueSequence   int64                        `json:"queue_sequence"`
	QueuePosition   *int64                       `json:"queue_position"`
	RequestSnapshot json.RawMessage              `json:"request_snapshot"`
	EnqueuedAt      time.Time                    `json:"enqueued_at"`
	UpdatedAt       time.Time                    `json:"updated_at"`
	TerminalOutcome *TerminalOutcome             `json:"terminal_outcome,omitempty"`
}

// OperatorItemView is the browser-facing projection for the local operator.
// PresentedProjectionRevision deliberately lives outside ItemView: ItemView is
// the strict caller contract, while this field is a presentation-channel CAS
// token that must survive a browser refresh.
type OperatorItemView struct {
	ItemView
	PresentedProjectionRevision *int64 `json:"presented_projection_revision,omitempty"`
}

// OperatorInbox is a durable, non-mutating projection of the one local HITL
// surface. Pending is always FIFO by queue_sequence. History is a separate,
// newest-first projection and never changes the pending ordering authority.
type OperatorInbox struct {
	ContractVersion string             `json:"contract_version"`
	SurfaceID       string             `json:"surface_id"`
	Revision        string             `json:"revision"`
	SyncedAt        time.Time          `json:"synced_at"`
	Pending         []OperatorItemView `json:"pending"`
	History         []OperatorItemView `json:"history"`
}

type RetrievalResult struct {
	ContractVersion string    `json:"contract_version"`
	Mode            string    `json:"mode"`
	WaitStatus      string    `json:"wait_status"`
	RetrievedAt     time.Time `json:"retrieved_at"`
	Item            ItemView  `json:"item"`
}

type IdempotencyConflictError struct {
	IdempotencyKey string `json:"idempotency_key"`
	ExistingItemID string `json:"existing_item_id"`
}

func (e *IdempotencyConflictError) Error() string {
	return fmt.Sprintf("%v: key %q already belongs to item %q", interaction.ErrIdempotencyConflict, e.IdempotencyKey, e.ExistingItemID)
}

func (e *IdempotencyConflictError) Unwrap() error { return interaction.ErrIdempotencyConflict }

type StaleRevisionError struct {
	Operation        string
	ItemID           string
	RevisionKind     string
	ExpectedRevision int64
	ActualRevision   int64
	CurrentState     interaction.InteractionState
	TerminalOutcome  *TerminalOutcome
}

func (e *StaleRevisionError) Error() string {
	return fmt.Sprintf("%v: %s %s revision for item %q: expected %d, actual %d", interaction.ErrRevisionConflict, e.Operation, e.RevisionKind, e.ItemID, e.ExpectedRevision, e.ActualRevision)
}

func (e *StaleRevisionError) Unwrap() error { return interaction.ErrRevisionConflict }

type TerminalConflictError struct {
	Outcome TerminalOutcome
}

func (e *TerminalConflictError) Error() string {
	return fmt.Sprintf("%v: item %q is already %s", ErrTerminalConflict, e.Outcome.ItemID, e.Outcome.State)
}

func (e *TerminalConflictError) Unwrap() error { return ErrTerminalConflict }

func (s *Service) Enqueue(ctx context.Context, input EnqueueInput) (ItemHandle, error) {
	request, fields, err := normalizeRequest(input.Request)
	if err != nil {
		return ItemHandle{}, err
	}
	if callerErr := validateCaller(input.Caller); callerErr != nil {
		return ItemHandle{}, callerErr
	}
	if schemaErr := s.validateRequest(request); schemaErr != nil {
		return ItemHandle{}, schemaErr
	}
	if fields.ExpiresAt != "" {
		if _, parseErr := time.Parse(time.RFC3339, fields.ExpiresAt); parseErr != nil {
			return ItemHandle{}, fmt.Errorf("%w: expires_at must be an absolute RFC 3339 timestamp", ErrInvalidRequest)
		}
	}

	existing, found, err := s.interactions.FindInteractionByIdempotency(
		ctx, input.Caller, fields.IdempotencyKey, reservedSurfaceCapability,
	)
	if err != nil {
		return ItemHandle{}, err
	}
	if found {
		existingDigest, digestErr := requestDigest(existing.RequestSnapshot)
		if digestErr != nil {
			return ItemHandle{}, digestErr
		}
		if !isHITLRecord(existing) || existingDigest != fields.Digest {
			return ItemHandle{}, &IdempotencyConflictError{
				IdempotencyKey: fields.IdempotencyKey,
				ExistingItemID: existing.ID,
			}
		}
		return s.itemHandle(ctx, existing)
	}
	if surfaceErr := s.ensureDefaultSurface(ctx); surfaceErr != nil {
		return ItemHandle{}, surfaceErr
	}
	if s.beforeSubmit != nil {
		s.beforeSubmit()
	}

	policyFields := map[string]any{"request_digest": fields.Digest}
	if fields.ExpiresAt != "" {
		policyFields["expires_at"] = fields.ExpiresAt
		policyFields["policy_ref"] = "request.expires_at"
	}
	policy, _ := json.Marshal(policyFields)
	handle, err := s.interactions.SubmitInteraction(ctx, interaction.SubmitInteractionInput{
		SurfaceID:      DefaultSurfaceID,
		Caller:         input.Caller,
		IdempotencyKey: fields.IdempotencyKey,
		Definition: interaction.DefinitionRef{
			Kind:    extensions.HITLItemEnvelopeType,
			Version: DefinitionVersion,
		},
		Request: request, Policy: policy, Capability: reservedSurfaceCapability,
	})
	if errors.Is(err, interaction.ErrIdempotencyConflict) {
		existing, found, lookupErr := s.interactions.FindInteractionByIdempotency(
			ctx, input.Caller, fields.IdempotencyKey, reservedSurfaceCapability,
		)
		if lookupErr != nil {
			return ItemHandle{}, lookupErr
		}
		if found {
			return ItemHandle{}, &IdempotencyConflictError{
				IdempotencyKey: fields.IdempotencyKey,
				ExistingItemID: existing.ID,
			}
		}
	}
	if err != nil {
		return ItemHandle{}, err
	}
	current, loadErr := s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
		InteractionID: handle.InteractionID, RequesterScope: input.Caller.Scope,
		Capability: reservedSurfaceCapability,
	})
	if loadErr != nil {
		return ItemHandle{}, loadErr
	}
	currentDigest, digestErr := requestDigest(current.Interaction.RequestSnapshot)
	if digestErr != nil {
		return ItemHandle{}, digestErr
	}
	if !isHITLRecord(current.Interaction) || currentDigest != fields.Digest {
		return ItemHandle{}, &IdempotencyConflictError{
			IdempotencyKey: fields.IdempotencyKey,
			ExistingItemID: current.Interaction.ID,
		}
	}
	return s.itemHandle(ctx, current.Interaction)
}

func (s *Service) validateRequest(raw json.RawMessage) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%w: decode canonical request: %w", ErrInvalidRequest, err)
	}
	if err := s.requestSchema.Validate(value); err != nil {
		return fmt.Errorf("%w: request does not match tangent.hitl-item@1.0: %w", interaction.ErrDefinitionValidation, err)
	}
	return nil
}

func (s *Service) ensureDefaultSurface(ctx context.Context) error {
	_, err := s.interactions.OpenSurface(ctx, interaction.OpenSurfaceInput{
		ID:             DefaultSurfaceID,
		Caller:         OperatorParticipant,
		IdempotencyKey: "hitl-default-surface-v1",
		OwnerScope:     OperatorParticipant.Scope,
		Metadata:       json.RawMessage(`{"kind":"hitl_inbox","contract_version":"1.0"}`),
		Policy:         json.RawMessage(`{"ordering":"fifo","visibility":"operator"}`),
		Capability:     reservedSurfaceCapability,
	})
	return err
}

type GetInput struct {
	ItemID               string
	Caller               interaction.ActorBinding
	TransportCorrelation json.RawMessage
}

func (s *Service) Get(ctx context.Context, input GetInput) (RetrievalResult, error) {
	if err := validateCaller(input.Caller); err != nil {
		return RetrievalResult{}, err
	}
	if _, err := s.inspectHITL(ctx, input.ItemID, input.Caller.Scope); err != nil {
		return RetrievalResult{}, err
	}
	outcome, err := s.interactions.GetInteraction(ctx, interaction.GetInteractionInput{
		InteractionID: input.ItemID, RequesterScope: input.Caller.Scope,
		TransportCorrelation: input.TransportCorrelation, Capability: reservedSurfaceCapability,
	})
	if err != nil {
		return RetrievalResult{}, err
	}
	item, err := s.itemView(ctx, outcome)
	if err != nil {
		return RetrievalResult{}, err
	}
	return RetrievalResult{
		ContractVersion: ContractVersion, Mode: "get", WaitStatus: "not_waited",
		RetrievedAt: retrievedAt(outcome, s.now()), Item: item,
	}, nil
}

type AwaitInput struct {
	ItemID               string
	Caller               interaction.ActorBinding
	Wait                 *time.Duration
	TransportCorrelation json.RawMessage
}

func (s *Service) Await(ctx context.Context, input AwaitInput) (RetrievalResult, error) {
	if err := validateCaller(input.Caller); err != nil {
		return RetrievalResult{}, err
	}
	if _, err := s.inspectHITL(ctx, input.ItemID, input.Caller.Scope); err != nil {
		return RetrievalResult{}, err
	}
	wait := defaultWait
	if input.Wait != nil {
		wait = *input.Wait
	}
	if wait < 0 || wait > maximumWait {
		return RetrievalResult{}, fmt.Errorf("%w: wait must be between 0 and 50000 milliseconds", ErrInvalidRequest)
	}
	if wait == 0 {
		outcome, err := s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
			InteractionID: input.ItemID, RequesterScope: input.Caller.Scope, Capability: reservedSurfaceCapability,
		})
		if err != nil {
			return RetrievalResult{}, err
		}
		if isTerminal(outcome.Interaction.State) {
			outcome, err = s.interactions.GetInteraction(ctx, interaction.GetInteractionInput{
				InteractionID: input.ItemID, RequesterScope: input.Caller.Scope,
				TransportCorrelation: input.TransportCorrelation, Capability: reservedSurfaceCapability,
			})
			if err != nil {
				return RetrievalResult{}, err
			}
			return s.awaitResult(ctx, outcome, "terminal")
		}
		return s.awaitResult(ctx, outcome, "timeout")
	}
	outcome, err := s.interactions.AwaitResolution(ctx, interaction.AwaitResolutionInput{
		InteractionID: input.ItemID, RequesterScope: input.Caller.Scope,
		MaximumWait: wait, TransportCorrelation: input.TransportCorrelation, Capability: reservedSurfaceCapability,
	})
	if errors.Is(err, interaction.ErrWaitTimeout) {
		outcome, err = s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
			InteractionID: input.ItemID, RequesterScope: input.Caller.Scope, Capability: reservedSurfaceCapability,
		})
		if err != nil {
			return RetrievalResult{}, err
		}
		if isTerminal(outcome.Interaction.State) {
			outcome, err = s.interactions.GetInteraction(ctx, interaction.GetInteractionInput{
				InteractionID: input.ItemID, RequesterScope: input.Caller.Scope,
				TransportCorrelation: input.TransportCorrelation, Capability: reservedSurfaceCapability,
			})
			if err != nil {
				return RetrievalResult{}, err
			}
			return s.awaitResult(ctx, outcome, "terminal")
		}
		return s.awaitResult(ctx, outcome, "timeout")
	}
	if err != nil {
		return RetrievalResult{}, err
	}
	return s.awaitResult(ctx, outcome, "terminal")
}

func (s *Service) awaitResult(ctx context.Context, outcome interaction.TerminalOutcome, status string) (RetrievalResult, error) {
	item, err := s.itemView(ctx, outcome)
	if err != nil {
		return RetrievalResult{}, err
	}
	return RetrievalResult{
		ContractVersion: ContractVersion, Mode: "await", WaitStatus: status,
		RetrievedAt: retrievedAt(outcome, s.now()), Item: item,
	}, nil
}

type WithdrawInput struct {
	ItemID           string
	Caller           interaction.ActorBinding
	ExpectedRevision *int64
	Reason           string
}

func (s *Service) Withdraw(ctx context.Context, input WithdrawInput) (TerminalOutcome, error) {
	if err := validateCaller(input.Caller); err != nil {
		return TerminalOutcome{}, err
	}
	if input.ItemID == "" {
		return TerminalOutcome{}, fmt.Errorf("%w: item_id is required", ErrInvalidRequest)
	}
	reason := strings.TrimSpace(input.Reason)
	if input.Reason != "" && reason == "" {
		return TerminalOutcome{}, fmt.Errorf("%w: reason must not be blank", ErrInvalidRequest)
	}
	current, err := s.inspectHITL(ctx, input.ItemID, input.Caller.Scope)
	if err != nil {
		return TerminalOutcome{}, err
	}
	if isTerminal(current.Interaction.State) {
		projected, projectionErr := terminalOutcome(current)
		if projectionErr != nil {
			return TerminalOutcome{}, projectionErr
		}
		if current.Interaction.State == interaction.InteractionStateCanceled &&
			current.Interaction.TerminalCause == interaction.TerminalCauseCallerWithdrawn {
			return *projected, nil
		}
		return TerminalOutcome{}, &TerminalConflictError{Outcome: *projected}
	}
	expected := current.Interaction.Revision
	if input.ExpectedRevision != nil {
		expected = *input.ExpectedRevision
		if expected < 1 {
			return TerminalOutcome{}, fmt.Errorf("%w: expected_revision must be positive", ErrInvalidRequest)
		}
		if expected != current.Interaction.Revision {
			return TerminalOutcome{}, staleError("withdraw", input.ItemID, expected, current)
		}
	}
	result, err := s.interactions.CancelInteraction(ctx, interaction.CancelInteractionInput{
		InteractionID: input.ItemID, ExpectedRevision: expected, Requester: input.Caller,
		Cause: interaction.TerminalCauseCallerWithdrawn, Reason: reason, Capability: reservedSurfaceCapability,
	})
	if err == nil {
		projected, projectionErr := terminalOutcome(interaction.TerminalOutcome{
			Interaction: result.Interaction, Notifications: result.Notifications,
		})
		if projectionErr != nil {
			return TerminalOutcome{}, projectionErr
		}
		return *projected, nil
	}
	if !errors.Is(err, interaction.ErrRevisionConflict) && !errors.Is(err, interaction.ErrTerminal) {
		return TerminalOutcome{}, err
	}
	latest, loadErr := s.inspectHITL(ctx, input.ItemID, input.Caller.Scope)
	if loadErr != nil {
		return TerminalOutcome{}, loadErr
	}
	if latest.Interaction.State == interaction.InteractionStateCanceled &&
		latest.Interaction.TerminalCause == interaction.TerminalCauseCallerWithdrawn {
		projected, projectionErr := terminalOutcome(latest)
		if projectionErr != nil {
			return TerminalOutcome{}, projectionErr
		}
		return *projected, nil
	}
	if isTerminal(latest.Interaction.State) {
		projected, projectionErr := terminalOutcome(latest)
		if projectionErr != nil {
			return TerminalOutcome{}, projectionErr
		}
		return TerminalOutcome{}, &TerminalConflictError{Outcome: *projected}
	}
	return TerminalOutcome{}, staleError("withdraw", input.ItemID, expected, latest)
}

type PresentInput struct {
	ItemID                      string
	ExpectedRevision            int64
	PresentedProjectionRevision int64
	ConnectionID                string
}

// Inbox returns the complete operator projection without recording a caller
// retrieval or changing any interaction state. A never-used inbox is a useful
// empty result; the default surface is still created atomically by Enqueue.
func (s *Service) Inbox(ctx context.Context) (OperatorInbox, error) {
	result := OperatorInbox{
		ContractVersion: ContractVersion,
		SurfaceID:       DefaultSurfaceID,
		Revision:        "empty",
		SyncedAt:        s.now(),
		Pending:         make([]OperatorItemView, 0),
		History:         make([]OperatorItemView, 0),
	}
	snapshot, err := s.interactions.GetSurfaceInteractions(ctx, interaction.GetSurfaceInput{
		SurfaceID:      DefaultSurfaceID,
		RequesterScope: OperatorParticipant.Scope,
		Capability:     reservedSurfaceCapability,
	})
	if errors.Is(err, interaction.ErrNotFound) {
		return result, nil
	}
	if err != nil {
		return OperatorInbox{}, err
	}
	if s.afterInboxLoad != nil {
		s.afterInboxLoad()
	}

	resolutions := make(map[string]*interaction.ResolutionRecord, len(snapshot.Resolutions))
	for index := range snapshot.Resolutions {
		resolution := &snapshot.Resolutions[index]
		resolutions[resolution.InteractionID] = resolution
	}

	digest := sha256.New()
	_, _ = fmt.Fprintf(digest, "%s:%d", snapshot.Surface.ID, snapshot.Surface.Revision)
	pendingPosition := int64(0)
	for _, record := range snapshot.Interactions {
		if !isHITLRecord(record) {
			continue
		}
		_, _ = fmt.Fprintf(digest, "|%s:%d:%s:%d", record.ID, record.SurfaceSequence, record.State, record.Revision)
		outcome := interaction.TerminalOutcome{Interaction: record, Resolution: resolutions[record.ID]}
		var position *int64
		if !isTerminal(record.State) {
			pendingPosition++
			projectedPosition := pendingPosition
			position = &projectedPosition
		}
		item, itemErr := itemViewAtPosition(outcome, position)
		if itemErr != nil {
			return OperatorInbox{}, itemErr
		}
		operatorItem := OperatorItemView{
			ItemView:                    item,
			PresentedProjectionRevision: record.PresentedProjectionRevision,
		}
		if isTerminal(record.State) {
			result.History = append(result.History, operatorItem)
		} else {
			result.Pending = append(result.Pending, operatorItem)
		}
	}
	sort.SliceStable(result.History, func(left, right int) bool {
		leftAt := result.History[left].UpdatedAt
		rightAt := result.History[right].UpdatedAt
		if leftAt.Equal(rightAt) {
			return result.History[left].QueueSequence > result.History[right].QueueSequence
		}
		return leftAt.After(rightAt)
	})
	result.Revision = hex.EncodeToString(digest.Sum(nil))
	return result, nil
}

// InspectOperatorItem returns one browser projection without recording a
// caller retrieval. It intentionally does not acknowledge presentation.
func (s *Service) InspectOperatorItem(ctx context.Context, itemID string) (OperatorItemView, error) {
	outcome, err := s.inspectHITL(ctx, itemID, OperatorParticipant.Scope)
	if err != nil {
		return OperatorItemView{}, err
	}
	item, err := s.itemView(ctx, outcome)
	if err != nil {
		return OperatorItemView{}, err
	}
	return OperatorItemView{
		ItemView:                    item,
		PresentedProjectionRevision: outcome.Interaction.PresentedProjectionRevision,
	}, nil
}

func (s *Service) Present(ctx context.Context, input PresentInput) (ItemHandle, error) {
	if input.ExpectedRevision < 1 || input.PresentedProjectionRevision < 1 {
		return ItemHandle{}, fmt.Errorf("%w: expected revisions must be positive", ErrInvalidRequest)
	}
	current, err := s.inspectHITL(ctx, input.ItemID, OperatorParticipant.Scope)
	if err != nil {
		return ItemHandle{}, err
	}
	if input.ExpectedRevision != current.Interaction.Revision {
		return ItemHandle{}, staleError("present", input.ItemID, input.ExpectedRevision, current)
	}
	if current.Interaction.State != interaction.InteractionStateStaged {
		return ItemHandle{}, interaction.ErrNotRespondable
	}
	handle, err := s.interactions.AcknowledgePresentation(ctx, interaction.PresentInteractionInput{
		InteractionID: input.ItemID, ExpectedRevision: input.ExpectedRevision,
		PresentedProjectionRevision: input.PresentedProjectionRevision,
		Participant:                 OperatorParticipant, ConnectionID: input.ConnectionID,
		Capability: reservedSurfaceCapability,
	})
	if errors.Is(err, interaction.ErrRevisionConflict) || errors.Is(err, interaction.ErrTerminal) {
		latest, loadErr := s.inspectHITL(ctx, input.ItemID, OperatorParticipant.Scope)
		if loadErr != nil {
			return ItemHandle{}, loadErr
		}
		return ItemHandle{}, staleError("present", input.ItemID, input.ExpectedRevision, latest)
	}
	if err != nil {
		return ItemHandle{}, err
	}
	current, err = s.inspectHITL(ctx, handle.InteractionID, OperatorParticipant.Scope)
	if err != nil {
		return ItemHandle{}, err
	}
	return s.itemHandle(ctx, current.Interaction)
}

type ResolveInput struct {
	ItemID                      string
	ExpectedRevision            int64
	PresentedProjectionRevision int64
	Response                    json.RawMessage
}

func (s *Service) Resolve(ctx context.Context, input ResolveInput) (TerminalOutcome, error) {
	if input.ExpectedRevision < 1 || input.PresentedProjectionRevision < 1 {
		return TerminalOutcome{}, fmt.Errorf("%w: expected revisions must be positive", ErrInvalidRequest)
	}
	current, err := s.inspectHITL(ctx, input.ItemID, OperatorParticipant.Scope)
	if err != nil {
		return TerminalOutcome{}, err
	}
	if input.ExpectedRevision != current.Interaction.Revision {
		return TerminalOutcome{}, staleError("resolve", input.ItemID, input.ExpectedRevision, current)
	}
	if current.Interaction.State != interaction.InteractionStatePresented &&
		current.Interaction.State != interaction.InteractionStateInProgress {
		return TerminalOutcome{}, interaction.ErrNotRespondable
	}
	if current.Interaction.PresentedProjectionRevision == nil {
		return TerminalOutcome{}, fmt.Errorf("%w: presented item has no projection revision", interaction.ErrInvalidRecord)
	}
	if input.PresentedProjectionRevision != *current.Interaction.PresentedProjectionRevision {
		actual := *current.Interaction.PresentedProjectionRevision
		return TerminalOutcome{}, staleErrorWithActual("resolve", input.ItemID, "presented_projection", input.PresentedProjectionRevision, actual, current)
	}
	response, err := normalizeAndValidateResponse(current.Interaction.RequestSnapshot, input.Response)
	if err != nil {
		return TerminalOutcome{}, err
	}
	result, err := s.interactions.ResolveInteraction(ctx, interaction.ResolveInteractionInput{
		InteractionID: input.ItemID, ExpectedInteractionRevision: input.ExpectedRevision,
		PresentedProjectionRevision: input.PresentedProjectionRevision,
		Participant:                 OperatorParticipant, ResponseKind: "data", ResponsePayload: response,
		Capability: reservedSurfaceCapability,
	})
	if errors.Is(err, interaction.ErrRevisionConflict) || errors.Is(err, interaction.ErrTerminal) {
		latest, loadErr := s.inspectHITL(ctx, input.ItemID, OperatorParticipant.Scope)
		if loadErr != nil {
			return TerminalOutcome{}, loadErr
		}
		return TerminalOutcome{}, staleError("resolve", input.ItemID, input.ExpectedRevision, latest)
	}
	if err != nil {
		return TerminalOutcome{}, err
	}
	projected, err := terminalOutcome(interaction.TerminalOutcome{
		Interaction: result.Interaction, Resolution: &result.Resolution,
		ResolutionDeliveries: result.Deliveries,
	})
	if err != nil {
		return TerminalOutcome{}, err
	}
	return *projected, nil
}

func (s *Service) itemHandle(ctx context.Context, record interaction.InteractionRecord) (ItemHandle, error) {
	if !isHITLRecord(record) {
		return ItemHandle{}, interaction.ErrNotFound
	}
	position, err := s.interactions.QueuePosition(ctx, record)
	if err != nil {
		return ItemHandle{}, err
	}
	return ItemHandle{
		ContractVersion: ContractVersion, SurfaceID: record.SurfaceID, ItemID: record.ID,
		State: record.State, Revision: record.Revision, QueueSequence: record.SurfaceSequence,
		QueuePosition: position, InboxURL: InboxURL,
		ItemURL: InboxURL + "/items/" + url.PathEscape(record.ID),
	}, nil
}

func (s *Service) itemView(ctx context.Context, outcome interaction.TerminalOutcome) (ItemView, error) {
	if !isHITLRecord(outcome.Interaction) {
		return ItemView{}, interaction.ErrNotFound
	}
	position, err := s.interactions.QueuePosition(ctx, outcome.Interaction)
	if err != nil {
		return ItemView{}, err
	}
	return itemViewAtPosition(outcome, position)
}

func itemViewAtPosition(outcome interaction.TerminalOutcome, position *int64) (ItemView, error) {
	view := ItemView{
		ContractVersion: ContractVersion, SurfaceID: outcome.Interaction.SurfaceID,
		ItemID: outcome.Interaction.ID, State: outcome.Interaction.State,
		Revision: outcome.Interaction.Revision, QueueSequence: outcome.Interaction.SurfaceSequence,
		QueuePosition: position, RequestSnapshot: outcome.Interaction.RequestSnapshot,
		EnqueuedAt: outcome.Interaction.CreatedAt, UpdatedAt: outcome.Interaction.UpdatedAt,
	}
	if isTerminal(outcome.Interaction.State) {
		terminal, err := terminalOutcome(outcome)
		if err != nil {
			return ItemView{}, err
		}
		view.TerminalOutcome = terminal
	}
	return view, nil
}

func (s *Service) inspectHITL(
	ctx context.Context,
	itemID string,
	requesterScope string,
) (interaction.TerminalOutcome, error) {
	if itemID == "" || requesterScope == "" {
		return interaction.TerminalOutcome{}, fmt.Errorf("%w: item_id and requester scope are required", ErrInvalidRequest)
	}
	outcome, err := s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
		InteractionID:  itemID,
		RequesterScope: requesterScope,
		Capability:     reservedSurfaceCapability,
	})
	if err != nil {
		return interaction.TerminalOutcome{}, err
	}
	if !isHITLRecord(outcome.Interaction) {
		// Do not disclose or mutate generic interaction handles through the
		// specialized HITL service, even when the caller owns both records.
		return interaction.TerminalOutcome{}, interaction.ErrNotFound
	}
	return outcome, nil
}

func isHITLRecord(record interaction.InteractionRecord) bool {
	return record.SurfaceID == DefaultSurfaceID &&
		record.Definition.Kind == extensions.HITLItemEnvelopeType &&
		record.Definition.Version == DefinitionVersion
}

func terminalOutcome(outcome interaction.TerminalOutcome) (*TerminalOutcome, error) {
	record := outcome.Interaction
	if !isTerminal(record.State) || record.TerminalAt == nil {
		return nil, fmt.Errorf("%w: item %q has no terminal outcome", ErrInvalidRequest, record.ID)
	}
	projected := &TerminalOutcome{
		ContractVersion: ContractVersion, State: record.State, ItemID: record.ID,
		InteractionRevision: record.Revision,
	}
	switch record.State {
	case interaction.InteractionStateResolved:
		if outcome.Resolution == nil {
			return nil, fmt.Errorf("%w: resolved item %q is missing its resolution", ErrInvalidRequest, record.ID)
		}
		projected.Resolution = &ResolutionView{
			ResolutionID: outcome.Resolution.ID, Response: outcome.Resolution.ResponsePayload,
			Participant: ParticipantCapture{
				PrincipalRef: outcome.Resolution.ParticipantRef,
				Authority:    outcome.Resolution.ParticipantAuthority,
				Assurance:    outcome.Resolution.ParticipantAssurance,
			},
			ResolvedAt: outcome.Resolution.RecordedAt, InteractionRevision: record.Revision,
			PresentedProjectionRevision: outcome.Resolution.PresentedProjectionRevision,
		}
	case interaction.InteractionStateCanceled:
		projected.Cause = record.TerminalCause
		projected.Reason = record.TerminalReason
		projected.TerminatedAt = record.TerminalAt
	case interaction.InteractionStateExpired:
		projected.PolicyRef = record.TerminalPolicyRef
		projected.TerminatedAt = record.TerminalAt
	case interaction.InteractionStateFailed:
		projected.ErrorCode = record.TerminalErrorCode
		projected.Message = record.TerminalReason
		projected.TerminatedAt = record.TerminalAt
	case interaction.InteractionStateSuperseded:
		projected.ReplacementItemID = record.ReplacementInteractionID
		projected.TerminatedAt = record.TerminalAt
	}
	return projected, nil
}

func staleError(operation, itemID string, expected int64, current interaction.TerminalOutcome) error {
	return staleErrorWithActual(operation, itemID, "interaction", expected, current.Interaction.Revision, current)
}

func staleErrorWithActual(operation, itemID, kind string, expected, actual int64, current interaction.TerminalOutcome) error {
	err := &StaleRevisionError{
		Operation: operation, ItemID: itemID, RevisionKind: kind,
		ExpectedRevision: expected, ActualRevision: actual, CurrentState: current.Interaction.State,
	}
	if isTerminal(current.Interaction.State) {
		if outcome, projectionErr := terminalOutcome(current); projectionErr == nil {
			err.TerminalOutcome = outcome
		}
	}
	return err
}

func retrievedAt(outcome interaction.TerminalOutcome, fallback time.Time) time.Time {
	if outcome.Retrieval != nil {
		return outcome.Retrieval.RetrievedAt
	}
	return fallback
}

func isTerminal(state interaction.InteractionState) bool {
	switch state {
	case interaction.InteractionStateResolved, interaction.InteractionStateCanceled,
		interaction.InteractionStateExpired, interaction.InteractionStateFailed,
		interaction.InteractionStateSuperseded:
		return true
	default:
		return false
	}
}

type requestFields struct {
	IdempotencyKey string
	ExpiresAt      string
	Digest         string
}

func normalizeRequest(raw json.RawMessage) (json.RawMessage, requestFields, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, requestFields{}, fmt.Errorf("%w: request is required", ErrInvalidRequest)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, requestFields{}, fmt.Errorf("%w: decode JSON: %w", ErrInvalidRequest, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, requestFields{}, fmt.Errorf("%w: request must contain one JSON value", ErrInvalidRequest)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, requestFields{}, fmt.Errorf("%w: request must be an object", ErrInvalidRequest)
	}
	if err := normalizeRequestFields(object); err != nil {
		return nil, requestFields{}, err
	}
	normalized, err := json.Marshal(object)
	if err != nil {
		return nil, requestFields{}, fmt.Errorf("%w: canonicalize JSON: %w", ErrInvalidRequest, err)
	}
	if len(normalized) > maximumRequest {
		return nil, requestFields{}, fmt.Errorf("%w: serialized request exceeds 512 KiB", ErrInvalidRequest)
	}
	field := func(name string) string {
		value, _ := object[name].(string)
		return value
	}
	for _, name := range []string{"contract_version", "kind", "idempotency_key", "title", "summary", "request"} {
		if field(name) == "" {
			return nil, requestFields{}, fmt.Errorf("%w: %s must not be blank", ErrInvalidRequest, name)
		}
	}
	source, ok := object["source"].(map[string]any)
	if !ok {
		return nil, requestFields{}, fmt.Errorf("%w: source is required", ErrInvalidRequest)
	}
	for _, name := range []string{"application_id", "agent_id"} {
		if value, _ := source[name].(string); value == "" {
			return nil, requestFields{}, fmt.Errorf("%w: source.%s must not be blank", ErrInvalidRequest, name)
		}
	}
	digest, err := requestDigest(normalized)
	if err != nil {
		return nil, requestFields{}, err
	}
	return normalized, requestFields{
		IdempotencyKey: field("idempotency_key"), ExpiresAt: field("expires_at"), Digest: digest,
	}, nil
}

var trimmedRequestFields = map[string]bool{
	"title": true, "summary": true, "request": true, "recommendation": true,
	"application_label": true, "agent_label": true, "label": true,
	"approve": true, "deny": true, "approve_with_note": true, "deny_with_note": true,
	"acknowledge": true, "acknowledge_with_note": true, "reply": true,
	"description": true, "base_label": true, "head_label": true,
}

var exactRequestFields = map[string]bool{
	"contract_version": true, "kind": true, "idempotency_key": true,
	"application_id": true, "agent_id": true, "authority": true, "id": true,
	"revision": true, "surface_id": true, "interaction_id": true,
	"artifact_id": true, "digest": true, "media_type": true, "logical_kind": true,
	"retrieval_capability_id": true, "expires_at": true, "retention_policy": true,
	"safe_preview_artifact_id": true, "type": true, "format": true,
	"sensitivity": true, "language": true,
}

func normalizeRequestFields(object map[string]any) error {
	return walkRequestFields(object, "")
}

func walkRequestFields(value any, path string) error {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return fmt.Errorf("%w: %s must not be blank", ErrInvalidRequest, path)
		}
		return nil
	case []any:
		for i := range typed {
			if err := walkRequestFields(typed[i], fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for key, child := range typed {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			text, isString := child.(string)
			if isString && trimmedRequestFields[key] {
				text = strings.TrimSpace(text)
				typed[key] = text
				child = text
			}
			if isString && exactRequestFields[key] && text != strings.TrimSpace(text) {
				return fmt.Errorf("%w: %s must not contain surrounding whitespace", ErrInvalidRequest, childPath)
			}
			if err := walkRequestFields(child, childPath); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}

func requestDigest(raw json.RawMessage) (string, error) {
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return "", fmt.Errorf("%w: decode request digest input: %w", ErrInvalidRequest, err)
	}
	delete(object, "idempotency_key")
	canonical, err := json.Marshal(object)
	if err != nil {
		return "", fmt.Errorf("%w: canonicalize request digest input: %w", ErrInvalidRequest, err)
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func normalizeAndValidateResponse(request, response json.RawMessage) (json.RawMessage, error) {
	var requestObject map[string]any
	if err := json.Unmarshal(request, &requestObject); err != nil {
		return nil, fmt.Errorf("%w: decode persisted request: %w", ErrInvalidRequest, err)
	}
	var responseValue any
	decoder := json.NewDecoder(bytes.NewReader(response))
	decoder.UseNumber()
	if err := decoder.Decode(&responseValue); err != nil {
		return nil, fmt.Errorf("%w: decode response: %w", ErrInvalidRequest, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: response must contain one JSON value", ErrInvalidRequest)
	}
	object, ok := responseValue.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: response must be an object", ErrInvalidRequest)
	}
	for _, field := range []string{"note", "reply"} {
		if text, exists := object[field].(string); exists {
			object[field] = strings.TrimSpace(text)
		}
	}
	if len(object) < 2 {
		return nil, fmt.Errorf("%w: response kind and decision are required", ErrInvalidRequest)
	}
	requestKind, _ := requestObject["kind"].(string)
	responseKind, _ := object["kind"].(string)
	decision, _ := object["decision"].(string)
	if responseKind != requestKind {
		return nil, fmt.Errorf("%w: response kind %q does not match request kind %q", ErrInvalidRequest, responseKind, requestKind)
	}
	var allowed map[string]bool
	switch requestKind {
	case "approval":
		allowed = map[string]bool{"kind": true, "decision": true, "note": true}
		if decision != "approved" && decision != "denied" {
			return nil, fmt.Errorf("%w: approval decision must be approved or denied", ErrInvalidRequest)
		}
	case "attention":
		allowed = map[string]bool{"kind": true, "decision": true, "note": true, "reply": true}
		if decision != "acknowledged" {
			return nil, fmt.Errorf("%w: attention decision must be acknowledged", ErrInvalidRequest)
		}
	default:
		return nil, fmt.Errorf("%w: unknown persisted request kind %q", ErrInvalidRequest, requestKind)
	}
	for key := range object {
		if !allowed[key] {
			return nil, fmt.Errorf("%w: response field %q is not allowed", ErrInvalidRequest, key)
		}
	}
	for _, key := range []string{"note", "reply"} {
		if value, exists := object[key]; exists {
			text, ok := value.(string)
			if !ok || text == "" {
				return nil, fmt.Errorf("%w: response %s must not be blank", ErrInvalidRequest, key)
			}
			limit := 4000
			if key == "reply" {
				limit = 12000
			}
			if len([]rune(text)) > limit {
				return nil, fmt.Errorf("%w: response %s exceeds %d characters", ErrInvalidRequest, key, limit)
			}
		}
	}
	normalized, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("%w: canonicalize response: %w", ErrInvalidRequest, err)
	}
	return normalized, nil
}

func validateCaller(caller interaction.ActorBinding) error {
	if caller.Scope == "" || caller.PrincipalRef == "" || caller.Authority == "" || caller.Assurance == "" {
		return fmt.Errorf("%w: complete caller binding is required", ErrInvalidRequest)
	}
	return nil
}
