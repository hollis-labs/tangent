// Package docs implements Tangent's third durable operator inbox
// (CW-20260917-0009): documents an agent sends the operator to read at their
// own pace, independent of the durable interaction substrate's own
// resolved/unresolved state. A document optionally asks for an
// acknowledgment; every document, ack-required or not, carries its own
// read/unread state, set only by an explicit operator action — never by the
// act of viewing.
package docs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	jsonschemav6 "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/interaction"
)

const (
	ContractVersion   = extensions.DocItemContractVersion
	DefinitionVersion = extensions.DocItemDefinitionVersion
	DefaultSurfaceID  = "surface_docs_default"
	InboxURL          = "/inbox"
)

var (
	ErrInvalidRequest   = errors.New("docs: invalid request")
	ErrTerminalConflict = errors.New("docs: another terminal outcome won")
	ErrNotFound         = errors.New("docs: doc not found")
	ErrStaleRevision    = errors.New("docs: stale revision conflict")
)

var OperatorParticipant = interaction.ActorBinding{
	Scope:        "operator:local",
	PrincipalRef: "local-operator",
	Authority:    "tangent-loopback",
	Assurance:    "loopback-unverified",
}

type DocSource struct {
	AgentID       string `json:"agent_id"`
	ApplicationID string `json:"application_id,omitempty"`
	AgentLabel    string `json:"agent_label,omitempty"`
}

type DocItemRequest struct {
	ContractVersion string         `json:"contract_version"`
	IdempotencyKey  string         `json:"idempotency_key"`
	Source          DocSource      `json:"source"`
	Title           string         `json:"title"`
	Summary         string         `json:"summary,omitempty"`
	ContentMarkdown string         `json:"content_markdown"`
	RequiresAck     bool           `json:"requires_ack,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
	Correlations    map[string]any `json:"correlations,omitempty"`
}

type DocHandle struct {
	ContractVersion string                       `json:"contract_version"`
	SurfaceID       string                       `json:"surface_id"`
	ItemID          string                       `json:"item_id"`
	State           interaction.InteractionState `json:"state"`
	Revision        int64                        `json:"revision"`
	QueueSequence   int64                        `json:"queue_sequence"`
	CreatedAt       time.Time                    `json:"created_at"`
}

type DocResponse struct {
	Action string `json:"action"`
	Note   string `json:"note,omitempty"`
}

type DocResolution struct {
	ResolutionID string    `json:"resolution_id"`
	Action       string    `json:"action"`
	Note         string    `json:"note,omitempty"`
	ResolvedAt   time.Time `json:"resolved_at"`
	ResolvedBy   string    `json:"resolved_by"`
}

type DocItemView struct {
	ContractVersion string                       `json:"contract_version"`
	ItemID          string                       `json:"item_id"`
	AgentID         string                       `json:"agent_id"`
	AgentLabel      string                       `json:"agent_label,omitempty"`
	ApplicationID   string                       `json:"application_id,omitempty"`
	Title           string                       `json:"title"`
	Summary         string                       `json:"summary,omitempty"`
	ContentMarkdown string                       `json:"content_markdown"`
	RequiresAck     bool                         `json:"requires_ack"`
	Tags            []string                     `json:"tags,omitempty"`
	Correlations    map[string]any               `json:"correlations,omitempty"`
	State           interaction.InteractionState `json:"state"`
	QueueSequence   int64                        `json:"queue_sequence"`
	Revision        int64                        `json:"revision"`
	CreatedAt       time.Time                    `json:"created_at"`
	UpdatedAt       time.Time                    `json:"updated_at"`
	ReadAt          *time.Time                   `json:"read_at,omitempty"`
	Resolution      *DocResolution               `json:"resolution,omitempty"`
}

type DocsInbox struct {
	ContractVersion string        `json:"contract_version"`
	SurfaceID       string        `json:"surface_id"`
	Revision        string        `json:"revision"`
	SyncedAt        time.Time     `json:"synced_at"`
	Pending         []DocItemView `json:"pending"`
	History         []DocItemView `json:"history"`
	TotalPending    int           `json:"total_pending"`
	TotalTerminal   int           `json:"total_terminal"`
}

type EnqueueInput struct {
	Request json.RawMessage
	Caller  interaction.ActorBinding
}

type AcknowledgeInput struct {
	ItemID           string
	ExpectedRevision int64
	Note             string
}

type ArchiveInput struct {
	ItemID           string
	ExpectedRevision int64
	Reason           string
}

type Service struct {
	interactions  *interaction.Service
	reads         *ReadStore
	requestSchema *jsonschemav6.Schema
	now           func() time.Time
}

func NewService(interactions *interaction.Service, reads *ReadStore) (*Service, error) {
	if interactions == nil {
		return nil, fmt.Errorf("%w: interaction service is required", ErrInvalidRequest)
	}
	if reads == nil {
		return nil, fmt.Errorf("%w: read store is required", ErrInvalidRequest)
	}
	var schemaDocument any
	if err := json.Unmarshal(extensions.DocItemContractSchema(), &schemaDocument); err != nil {
		return nil, fmt.Errorf("%w: decode embedded doc schema: %w", ErrInvalidRequest, err)
	}
	compiler := jsonschemav6.NewCompiler()
	const schemaURI = "memory://tangent/doc-item-v1.schema.json"
	if err := compiler.AddResource(schemaURI, schemaDocument); err != nil {
		return nil, fmt.Errorf("%w: load embedded doc schema: %w", ErrInvalidRequest, err)
	}
	requestSchema, err := compiler.Compile(schemaURI)
	if err != nil {
		return nil, fmt.Errorf("%w: compile embedded doc schema: %w", ErrInvalidRequest, err)
	}
	return &Service{
		interactions:  interactions,
		reads:         reads,
		requestSchema: requestSchema,
		now:           func() time.Time { return time.Now().UTC() },
	}, nil
}

func (s *Service) Enqueue(ctx context.Context, input EnqueueInput) (DocHandle, error) {
	if len(input.Request) == 0 {
		return DocHandle{}, fmt.Errorf("%w: request body is empty", ErrInvalidRequest)
	}
	var schemaValue any
	if err := json.Unmarshal(input.Request, &schemaValue); err != nil {
		return DocHandle{}, fmt.Errorf("%w: invalid JSON: %w", ErrInvalidRequest, err)
	}
	if err := s.requestSchema.Validate(schemaValue); err != nil {
		return DocHandle{}, fmt.Errorf("%w: doc validation failed: %w", ErrInvalidRequest, err)
	}

	var req DocItemRequest
	if err := json.Unmarshal(input.Request, &req); err != nil {
		return DocHandle{}, fmt.Errorf("%w: unmarshal request: %w", ErrInvalidRequest, err)
	}

	if err := s.ensureDefaultSurface(ctx); err != nil {
		return DocHandle{}, err
	}

	policyMap := map[string]any{"agent_id": req.Source.AgentID}
	policy, _ := json.Marshal(policyMap)

	submitHandle, err := s.interactions.SubmitInteraction(ctx, interaction.SubmitInteractionInput{
		SurfaceID:      DefaultSurfaceID,
		Caller:         input.Caller,
		IdempotencyKey: req.IdempotencyKey,
		Definition: interaction.DefinitionRef{
			Kind:    extensions.DocItemEnvelopeType,
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
			return DocHandle{}, lookupErr
		}
		if found {
			return itemHandle(existing), nil
		}
	}
	if err != nil {
		return DocHandle{}, err
	}

	// Immediately transition staged item to presented for the operator, same
	// as HITL and Turns — a doc sitting in "staged" is invisible to the
	// operator's inbox query in practice, and there is no separate action
	// that would ever move it forward on its own.
	current, loadErr := s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
		InteractionID:  submitHandle.InteractionID,
		RequesterScope: input.Caller.Scope,
		Capability:     ReservedSurfaceCapability,
	})
	if loadErr != nil {
		return DocHandle{}, loadErr
	}
	if current.Interaction.State == interaction.InteractionStateStaged {
		_, presentErr := s.interactions.AcknowledgePresentation(ctx, interaction.PresentInteractionInput{
			InteractionID:               current.Interaction.ID,
			ExpectedRevision:            current.Interaction.Revision,
			PresentedProjectionRevision: 1,
			Participant:                 OperatorParticipant,
			ConnectionID:                "docs-auto-present",
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

	return itemHandle(current.Interaction), nil
}

func (s *Service) Inbox(ctx context.Context) (DocsInbox, error) {
	result := DocsInbox{
		ContractVersion: ContractVersion,
		SurfaceID:       DefaultSurfaceID,
		Revision:        "empty",
		SyncedAt:        s.now(),
		Pending:         make([]DocItemView, 0),
		History:         make([]DocItemView, 0),
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
		return DocsInbox{}, err
	}

	resolutions := make(map[string]*interaction.ResolutionRecord, len(snapshot.Resolutions))
	for i := range snapshot.Resolutions {
		res := &snapshot.Resolutions[i]
		resolutions[res.InteractionID] = res
	}

	interactionIDs := make([]string, 0, len(snapshot.Interactions))
	for _, record := range snapshot.Interactions {
		if record.Definition.Kind == extensions.DocItemEnvelopeType {
			interactionIDs = append(interactionIDs, record.ID)
		}
	}
	readAt, err := s.reads.ReadAtBulk(ctx, interactionIDs)
	if err != nil {
		return DocsInbox{}, err
	}

	hidden, err := s.interactions.HiddenInboxItems(ctx)
	if err != nil {
		return DocsInbox{}, err
	}
	digest := sha256.New()
	_, _ = fmt.Fprintf(digest, "%s:%d", snapshot.Surface.ID, snapshot.Surface.Revision)

	for _, record := range snapshot.Interactions {
		if record.Definition.Kind != extensions.DocItemEnvelopeType || hidden[record.ID] {
			continue
		}
		_, isRead := readAt[record.ID]
		_, _ = fmt.Fprintf(digest, "|%s:%d:%s:%d:%v", record.ID, record.SurfaceSequence, record.State, record.Revision, isRead)

		view, viewErr := buildItemView(record, resolutions[record.ID], readAt)
		if viewErr != nil {
			continue
		}

		if isTerminal(record.State) {
			result.History = append(result.History, view)
		} else {
			result.Pending = append(result.Pending, view)
		}
	}

	// Strict arrival FIFO for the active queue, same as HITL and Turns.
	sort.SliceStable(result.Pending, func(i, j int) bool {
		return result.Pending[i].QueueSequence < result.Pending[j].QueueSequence
	})
	sort.SliceStable(result.History, func(i, j int) bool {
		return result.History[i].UpdatedAt.After(result.History[j].UpdatedAt)
	})

	result.TotalPending = len(result.Pending)
	result.TotalTerminal = len(result.History)
	result.Revision = hex.EncodeToString(digest.Sum(nil))
	return result, nil
}

func (s *Service) InspectDoc(ctx context.Context, itemID string) (DocItemView, error) {
	record, err := s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
		InteractionID:  itemID,
		RequesterScope: OperatorParticipant.Scope,
		Capability:     ReservedSurfaceCapability,
	})
	if err != nil {
		return DocItemView{}, err
	}
	if record.Interaction.Definition.Kind != extensions.DocItemEnvelopeType {
		return DocItemView{}, ErrNotFound
	}
	readAt, err := s.reads.ReadAtBulk(ctx, []string{itemID})
	if err != nil {
		return DocItemView{}, err
	}
	return buildItemView(record.Interaction, record.Resolution, readAt)
}

// MarkRead is the only thing that sets a doc's read state. Nothing about
// fetching or displaying a doc calls this — the operator's own explicit
// action does, matching the requirement that viewing a document never
// silently marks it read.
func (s *Service) MarkRead(ctx context.Context, itemID string) (DocItemView, error) {
	if itemID == "" {
		return DocItemView{}, fmt.Errorf("%w: item_id is required", ErrInvalidRequest)
	}
	if _, err := s.InspectDoc(ctx, itemID); err != nil {
		return DocItemView{}, err
	}
	if err := s.reads.MarkRead(ctx, itemID); err != nil {
		return DocItemView{}, err
	}
	return s.InspectDoc(ctx, itemID)
}

func (s *Service) Acknowledge(ctx context.Context, input AcknowledgeInput) (DocItemView, error) {
	if input.ItemID == "" {
		return DocItemView{}, fmt.Errorf("%w: item_id is required", ErrInvalidRequest)
	}
	if input.ExpectedRevision < 1 {
		return DocItemView{}, fmt.Errorf("%w: expected_revision must be positive", ErrInvalidRequest)
	}
	current, err := s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
		InteractionID:  input.ItemID,
		RequesterScope: OperatorParticipant.Scope,
		Capability:     ReservedSurfaceCapability,
	})
	if err != nil {
		return DocItemView{}, err
	}
	if current.Interaction.Definition.Kind != extensions.DocItemEnvelopeType {
		return DocItemView{}, ErrNotFound
	}
	if current.Interaction.Revision != input.ExpectedRevision {
		return DocItemView{}, fmt.Errorf("%w: expected %d, got %d", ErrStaleRevision, input.ExpectedRevision, current.Interaction.Revision)
	}
	if isTerminal(current.Interaction.State) {
		return DocItemView{}, ErrTerminalConflict
	}

	responsePayload, err := json.Marshal(DocResponse{Action: "acknowledged", Note: input.Note})
	if err != nil {
		return DocItemView{}, fmt.Errorf("%w: marshal response: %w", ErrInvalidRequest, err)
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
		return DocItemView{}, err
	}
	return s.InspectDoc(ctx, input.ItemID)
}

// Archive closes a pending document and retains it in operator history.
func (s *Service) Archive(ctx context.Context, input ArchiveInput) (DocItemView, error) {
	if input.ItemID == "" {
		return DocItemView{}, fmt.Errorf("%w: item_id is required", ErrInvalidRequest)
	}
	current, err := s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
		InteractionID:  input.ItemID,
		RequesterScope: OperatorParticipant.Scope,
		Capability:     ReservedSurfaceCapability,
	})
	if err != nil {
		return DocItemView{}, err
	}
	if current.Interaction.Definition.Kind != extensions.DocItemEnvelopeType {
		return DocItemView{}, ErrNotFound
	}
	if isTerminal(current.Interaction.State) {
		return DocItemView{}, ErrTerminalConflict
	}

	reason := input.Reason
	if reason == "" {
		reason = "archived by operator"
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
		return DocItemView{}, err
	}
	return s.InspectDoc(ctx, input.ItemID)
}

func (s *Service) ensureDefaultSurface(ctx context.Context) error {
	_, err := s.interactions.OpenSurface(ctx, interaction.OpenSurfaceInput{
		ID:             DefaultSurfaceID,
		Caller:         OperatorParticipant,
		IdempotencyKey: "docs-default-surface-v1",
		OwnerScope:     OperatorParticipant.Scope,
		Metadata:       json.RawMessage(`{"purpose":"docs_inbox"}`),
		Capability:     ReservedSurfaceCapability,
	})
	return err
}

func itemHandle(rec interaction.InteractionRecord) DocHandle {
	return DocHandle{
		ContractVersion: ContractVersion,
		SurfaceID:       rec.SurfaceID,
		ItemID:          rec.ID,
		State:           rec.State,
		Revision:        rec.Revision,
		QueueSequence:   rec.SurfaceSequence,
		CreatedAt:       rec.CreatedAt,
	}
}

func buildItemView(rec interaction.InteractionRecord, res *interaction.ResolutionRecord, readAt map[string]time.Time) (DocItemView, error) {
	var req DocItemRequest
	if err := json.Unmarshal(rec.RequestSnapshot, &req); err != nil {
		return DocItemView{}, err
	}

	var resolution *DocResolution
	if res != nil {
		var resp DocResponse
		_ = json.Unmarshal(res.ResponsePayload, &resp)
		resolution = &DocResolution{
			ResolutionID: res.ID,
			Action:       resp.Action,
			Note:         resp.Note,
			ResolvedAt:   res.RecordedAt,
			ResolvedBy:   res.ParticipantRef,
		}
	}

	var read *time.Time
	if t, ok := readAt[rec.ID]; ok {
		read = &t
	}

	return DocItemView{
		ContractVersion: ContractVersion,
		ItemID:          rec.ID,
		AgentID:         req.Source.AgentID,
		AgentLabel:      req.Source.AgentLabel,
		ApplicationID:   req.Source.ApplicationID,
		Title:           req.Title,
		Summary:         req.Summary,
		ContentMarkdown: req.ContentMarkdown,
		RequiresAck:     req.RequiresAck,
		Tags:            req.Tags,
		Correlations:    req.Correlations,
		State:           rec.State,
		QueueSequence:   rec.SurfaceSequence,
		Revision:        rec.Revision,
		CreatedAt:       rec.CreatedAt,
		UpdatedAt:       rec.UpdatedAt,
		ReadAt:          read,
		Resolution:      resolution,
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

// Delete removes a document from operator lists while preserving the immutable
// interaction and its outcome for caller retrieval and delivery.
func (s *Service) Delete(ctx context.Context, input ArchiveInput) (DocItemView, error) {
	current, err := s.InspectDoc(ctx, input.ItemID)
	if err != nil {
		return DocItemView{}, err
	}
	if input.ExpectedRevision != current.Revision {
		return DocItemView{}, ErrStaleRevision
	}
	if !isTerminal(current.State) {
		input.Reason = "deleted by operator"
		current, err = s.Archive(ctx, input)
		if err != nil {
			return DocItemView{}, err
		}
	}
	if err = s.interactions.HideBrowserInboxItem(ctx, input.ItemID); err != nil {
		return DocItemView{}, err
	}
	return current, nil
}
