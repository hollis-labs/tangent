// Package artifacts implements Tangent's artifact inbox (ADR 0015,
// CW-20261024-0061): HTML mocks, screenshots, diagrams and documents an agent
// shows the operator, which wait in the inbox, can be revised in place by the
// agent, and carry a message thread and an optional sign-off.
//
// An artifact is an interaction on a reserved surface whose request snapshot is
// immutable identity. Revisions and messages are the mutable half and live in
// Store. Read state reuses the docs read receipts, and is set only by an
// explicit operator action, never by viewing.
package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	jsonschemav6 "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/hollis-labs/tangent/internal/docs"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/interaction"
)

const (
	ContractVersion   = extensions.ArtifactItemContractVersion
	DefinitionVersion = extensions.ArtifactItemDefinitionVersion
	DefaultSurfaceID  = "surface_artifacts_default"
	InboxPathPrefix   = "/inbox/items/"
)

// OperatorParticipant is the binding browser commands act as.
var OperatorParticipant = interaction.ActorBinding{
	Scope:        "operator:local",
	PrincipalRef: "local-operator",
	Authority:    "tangent-loopback",
	Assurance:    "loopback-unverified",
}

type Source struct {
	AgentID       string `json:"agent_id"`
	ApplicationID string `json:"application_id,omitempty"`
	AgentLabel    string `json:"agent_label,omitempty"`
}

// Request is the immutable identity stored as the interaction's request
// snapshot. Content is deliberately absent: it is a revision.
type Request struct {
	ContractVersion string         `json:"contract_version"`
	IdempotencyKey  string         `json:"idempotency_key"`
	Source          Source         `json:"source"`
	Title           string         `json:"title"`
	Summary         string         `json:"summary,omitempty"`
	Type            string         `json:"type"`
	RequiresSignoff bool           `json:"requires_signoff,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
	Correlations    map[string]any `json:"correlations,omitempty"`
}

type Handle struct {
	ContractVersion string                       `json:"contract_version"`
	SurfaceID       string                       `json:"surface_id"`
	ItemID          string                       `json:"item_id"`
	State           interaction.InteractionState `json:"state"`
	Revision        int64                        `json:"revision"`
	QueueSequence   int64                        `json:"queue_sequence"`
	CreatedAt       time.Time                    `json:"created_at"`
	ArtifactRev     int                          `json:"artifact_revision"`
	InboxPath       string                       `json:"inbox_path"`
}

type Response struct {
	Action   string `json:"action"`
	Note     string `json:"note,omitempty"`
	Revision int    `json:"revision,omitempty"`
}

type Resolution struct {
	ResolutionID string    `json:"resolution_id"`
	Action       string    `json:"action"`
	Note         string    `json:"note,omitempty"`
	Revision     int       `json:"revision,omitempty"`
	ResolvedAt   time.Time `json:"resolved_at"`
	ResolvedBy   string    `json:"resolved_by"`
}

// View is the projection of one artifact for both the operator and the agent.
type View struct {
	ContractVersion string                       `json:"contract_version"`
	ItemID          string                       `json:"item_id"`
	AgentID         string                       `json:"agent_id"`
	AgentLabel      string                       `json:"agent_label,omitempty"`
	ApplicationID   string                       `json:"application_id,omitempty"`
	Title           string                       `json:"title"`
	Summary         string                       `json:"summary,omitempty"`
	Type            string                       `json:"type"`
	RequiresSignoff bool                         `json:"requires_signoff"`
	Tags            []string                     `json:"tags,omitempty"`
	Correlations    map[string]any               `json:"correlations,omitempty"`
	State           interaction.InteractionState `json:"state"`
	QueueSequence   int64                        `json:"queue_sequence"`
	Revision        int64                        `json:"revision"`
	CreatedAt       time.Time                    `json:"created_at"`
	UpdatedAt       time.Time                    `json:"updated_at"`
	ReadAt          *time.Time                   `json:"read_at,omitempty"`
	Resolution      *Resolution                  `json:"resolution,omitempty"`
	Revisions       []Revision                   `json:"revisions"`
	LatestRevision  int                          `json:"latest_revision"`
	Messages        []Message                    `json:"messages"`
	InboxPath       string                       `json:"inbox_path"`
}

type Inbox struct {
	ContractVersion string    `json:"contract_version"`
	SurfaceID       string    `json:"surface_id"`
	Revision        string    `json:"revision"`
	SyncedAt        time.Time `json:"synced_at"`
	Pending         []View    `json:"pending"`
	History         []View    `json:"history"`
	TotalPending    int       `json:"total_pending"`
	TotalTerminal   int       `json:"total_terminal"`
}

// ContentFields are the wire fields that carry one revision's content.
type ContentFields struct {
	Content       string
	ContentBase64 string
	Filename      string
	MediaType     string
	Note          string
}

type CreateInput struct {
	// Request is the full wire object: identity fields plus content fields.
	Request json.RawMessage
	Caller  interaction.ActorBinding
}

type UpdateInput struct {
	ItemID  string
	Caller  interaction.ActorBinding
	Content ContentFields
}

type MessageInput struct {
	ItemID string
	Caller interaction.ActorBinding
	Body   string
}

type GetInput struct {
	ItemID string
	Caller interaction.ActorBinding
	// AfterSeq limits Messages to those after this sequence; 0 returns all.
	AfterSeq int64
}

type AwaitInput struct {
	ItemID   string
	Caller   interaction.ActorBinding
	AfterSeq int64
	Wait     time.Duration
}

type WithdrawInput struct {
	ItemID string
	Caller interaction.ActorBinding
	Reason string
}

// AwaitResult is what Await returns: the current view plus why it returned.
type AwaitResult struct {
	View     View   `json:"item"`
	Reason   string `json:"reason"`
	TimedOut bool   `json:"timed_out"`
}

type SignoffInput struct {
	ItemID           string
	ExpectedRevision int64
	Note             string
	Participant      interaction.ActorBinding
}

type ArchiveInput struct {
	ItemID           string
	ExpectedRevision int64
	Reason           string
}

type OperatorMessageInput struct {
	ItemID string
	Body   string
	// Kind is KindMessage or KindChangesRequested.
	Kind string
}

const (
	defaultAwait = 30 * time.Second
	maxAwait     = 120 * time.Second
	awaitPoll    = 500 * time.Millisecond
)

type Service struct {
	interactions  *interaction.Service
	store         *Store
	reads         *docs.ReadStore
	requestSchema *jsonschemav6.Schema
	now           func() time.Time
}

func NewService(interactions *interaction.Service, store *Store, reads *docs.ReadStore) (*Service, error) {
	if interactions == nil || store == nil || reads == nil {
		return nil, fmt.Errorf("%w: interaction service, store and read store are required", ErrInvalidRequest)
	}
	var schemaDocument any
	if err := json.Unmarshal(extensions.ArtifactItemContractSchema(), &schemaDocument); err != nil {
		return nil, fmt.Errorf("%w: decode embedded artifact schema: %w", ErrInvalidRequest, err)
	}
	compiler := jsonschemav6.NewCompiler()
	const schemaURI = "memory://tangent/artifact-item-v1.schema.json"
	if err := compiler.AddResource(schemaURI, schemaDocument); err != nil {
		return nil, fmt.Errorf("%w: load embedded artifact schema: %w", ErrInvalidRequest, err)
	}
	requestSchema, err := compiler.Compile(schemaURI)
	if err != nil {
		return nil, fmt.Errorf("%w: compile embedded artifact schema: %w", ErrInvalidRequest, err)
	}
	return &Service{
		interactions:  interactions,
		store:         store,
		reads:         reads,
		requestSchema: requestSchema,
		now:           func() time.Time { return time.Now().UTC() },
	}, nil
}

// contentFieldNames are removed from the wire object before it is validated
// against the identity schema and stored as the request snapshot.
var contentFieldNames = []string{"content", "content_base64", "filename", "media_type", "note"}

// splitRequest separates the wire object into the identity JSON and the
// content fields.
func splitRequest(raw json.RawMessage) (json.RawMessage, ContentFields, string, error) {
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, ContentFields{}, "", fmt.Errorf("%w: invalid JSON: %w", ErrInvalidRequest, err)
	}
	var fields ContentFields
	fields.Content, _ = object["content"].(string)
	fields.ContentBase64, _ = object["content_base64"].(string)
	fields.Filename, _ = object["filename"].(string)
	fields.MediaType, _ = object["media_type"].(string)
	fields.Note, _ = object["note"].(string)
	artifactType, _ := object["type"].(string)
	// Agents should not have to know the contract version to send an artifact.
	if _, present := object["contract_version"]; !present {
		object["contract_version"] = ContractVersion
	}
	for _, name := range contentFieldNames {
		delete(object, name)
	}
	identity, err := json.Marshal(object)
	if err != nil {
		return nil, ContentFields{}, "", fmt.Errorf("%w: marshal identity: %w", ErrInvalidRequest, err)
	}
	return identity, fields, artifactType, nil
}

func (s *Service) Create(ctx context.Context, input CreateInput) (Handle, error) {
	if len(input.Request) == 0 {
		return Handle{}, fmt.Errorf("%w: request body is empty", ErrInvalidRequest)
	}
	identity, fields, artifactType, err := splitRequest(input.Request)
	if err != nil {
		return Handle{}, err
	}
	var schemaValue any
	if err = json.Unmarshal(identity, &schemaValue); err != nil {
		return Handle{}, fmt.Errorf("%w: invalid JSON: %w", ErrInvalidRequest, err)
	}
	if err = s.requestSchema.Validate(schemaValue); err != nil {
		return Handle{}, fmt.Errorf("%w: artifact validation failed: %w", ErrInvalidRequest, err)
	}
	file, err := BuildFile(artifactType, fields.Content, fields.ContentBase64, fields.Filename, fields.MediaType, fields.Note)
	if err != nil {
		return Handle{}, err
	}
	var req Request
	if err = json.Unmarshal(identity, &req); err != nil {
		return Handle{}, fmt.Errorf("%w: unmarshal request: %w", ErrInvalidRequest, err)
	}
	if err = s.ensureDefaultSurface(ctx); err != nil {
		return Handle{}, err
	}
	policy, _ := json.Marshal(map[string]any{"agent_id": req.Source.AgentID})
	submit, err := s.interactions.SubmitInteraction(ctx, interaction.SubmitInteractionInput{
		SurfaceID:      DefaultSurfaceID,
		Caller:         input.Caller,
		IdempotencyKey: req.IdempotencyKey,
		Definition: interaction.DefinitionRef{
			Kind:    extensions.ArtifactItemEnvelopeType,
			Version: DefinitionVersion,
		},
		Request:    identity,
		Policy:     policy,
		Capability: ReservedSurfaceCapability,
	})
	if errors.Is(err, interaction.ErrIdempotencyConflict) {
		existing, found, lookupErr := s.interactions.FindInteractionByIdempotency(
			ctx, input.Caller, req.IdempotencyKey, ReservedSurfaceCapability,
		)
		if lookupErr != nil {
			return Handle{}, lookupErr
		}
		if found && sameRequest(existing.RequestSnapshot, identity) {
			return s.handle(ctx, existing)
		}
	}
	if err != nil {
		return Handle{}, err
	}
	current, err := s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
		InteractionID:  submit.InteractionID,
		RequesterScope: input.Caller.Scope,
		Capability:     ReservedSurfaceCapability,
	})
	if err != nil {
		return Handle{}, err
	}
	if _, _, err = s.store.AddFirstRevision(ctx, submit.InteractionID, file); err != nil {
		// No content means no artifact: close the interaction rather than leave
		// an empty item in the operator's inbox.
		_, _ = s.interactions.CancelInteraction(ctx, interaction.CancelInteractionInput{
			InteractionID:    submit.InteractionID,
			ExpectedRevision: current.Interaction.Revision,
			Requester:        input.Caller,
			Cause:            interaction.TerminalCauseParticipantCanceled,
			Reason:           "artifact content could not be stored",
			Capability:       ReservedSurfaceCapability,
		})
		return Handle{}, err
	}
	// Present immediately: a staged item is invisible in the operator's inbox
	// and nothing else would ever move it forward (same as docs).
	if current.Interaction.State == interaction.InteractionStateStaged {
		_, presentErr := s.interactions.AcknowledgePresentation(ctx, interaction.PresentInteractionInput{
			InteractionID:               current.Interaction.ID,
			ExpectedRevision:            current.Interaction.Revision,
			PresentedProjectionRevision: 1,
			Participant:                 OperatorParticipant,
			ConnectionID:                "artifacts-auto-present",
			Capability:                  ReservedSurfaceCapability,
		})
		if presentErr == nil {
			if refreshed, inspectErr := s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
				InteractionID:  submit.InteractionID,
				RequesterScope: input.Caller.Scope,
				Capability:     ReservedSurfaceCapability,
			}); inspectErr == nil {
				current = refreshed
			}
		}
	}
	return s.handle(ctx, current.Interaction)
}

func (s *Service) handle(ctx context.Context, rec interaction.InteractionRecord) (Handle, error) {
	revisions, err := s.store.Revisions(ctx, rec.ID)
	if err != nil {
		return Handle{}, err
	}
	latest := 0
	if n := len(revisions); n > 0 {
		latest = revisions[n-1].Revision
	}
	return Handle{
		ContractVersion: ContractVersion,
		SurfaceID:       rec.SurfaceID,
		ItemID:          rec.ID,
		State:           rec.State,
		Revision:        rec.Revision,
		QueueSequence:   rec.SurfaceSequence,
		CreatedAt:       rec.CreatedAt,
		ArtifactRev:     latest,
		InboxPath:       InboxPathPrefix + rec.ID,
	}, nil
}

// inspectAs loads an artifact the caller may see and returns its record.
func (s *Service) inspectAs(ctx context.Context, itemID, requesterScope string) (interaction.TerminalOutcome, error) {
	if itemID == "" {
		return interaction.TerminalOutcome{}, fmt.Errorf("%w: item_id is required", ErrInvalidRequest)
	}
	rec, err := s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
		InteractionID:  itemID,
		RequesterScope: requesterScope,
		Capability:     ReservedSurfaceCapability,
	})
	if err != nil {
		return interaction.TerminalOutcome{}, err
	}
	if rec.Interaction.Definition.Kind != extensions.ArtifactItemEnvelopeType {
		return interaction.TerminalOutcome{}, ErrNotFound
	}
	return rec, nil
}

// Update adds a revision to an artifact the caller created.
func (s *Service) Update(ctx context.Context, input UpdateInput) (Handle, error) {
	rec, err := s.inspectAs(ctx, input.ItemID, input.Caller.Scope)
	if err != nil {
		return Handle{}, err
	}
	if isTerminal(rec.Interaction.State) {
		return Handle{}, ErrTerminalConflict
	}
	var req Request
	if err = json.Unmarshal(rec.Interaction.RequestSnapshot, &req); err != nil {
		return Handle{}, err
	}
	c := input.Content
	file, err := BuildFile(req.Type, c.Content, c.ContentBase64, c.Filename, c.MediaType, c.Note)
	if err != nil {
		return Handle{}, err
	}
	if _, err = s.store.AddRevision(ctx, input.ItemID, file); err != nil {
		return Handle{}, err
	}
	return s.handle(ctx, rec.Interaction)
}

// AgentMessage appends an agent message to the thread.
func (s *Service) AgentMessage(ctx context.Context, input MessageInput) (Message, error) {
	rec, err := s.inspectAs(ctx, input.ItemID, input.Caller.Scope)
	if err != nil {
		return Message{}, err
	}
	if isTerminal(rec.Interaction.State) {
		return Message{}, ErrTerminalConflict
	}
	body, err := checkBody(input.Body)
	if err != nil {
		return Message{}, err
	}
	return s.store.AddMessage(ctx, input.ItemID, AuthorAgent, KindMessage, body, 0)
}

func checkBody(body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", fmt.Errorf("%w: message body is required", ErrInvalidRequest)
	}
	if len(body) > MaxMessageBody {
		return "", fmt.Errorf("%w: message exceeds %d characters", ErrInvalidRequest, MaxMessageBody)
	}
	return body, nil
}

// Get returns the artifact as its creator sees it.
func (s *Service) Get(ctx context.Context, input GetInput) (View, error) {
	rec, err := s.inspectAs(ctx, input.ItemID, input.Caller.Scope)
	if err != nil {
		return View{}, err
	}
	return s.viewFor(ctx, rec, input.AfterSeq)
}

// Await blocks until the operator sends a message after AfterSeq, the artifact
// reaches a terminal state, or Wait elapses. A timeout is a successful result.
func (s *Service) Await(ctx context.Context, input AwaitInput) (AwaitResult, error) {
	wait := input.Wait
	if wait <= 0 {
		wait = defaultAwait
	}
	if wait > maxAwait {
		wait = maxAwait
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	ticker := time.NewTicker(awaitPoll)
	defer ticker.Stop()
	for {
		view, err := s.Get(ctx, GetInput{ItemID: input.ItemID, Caller: input.Caller, AfterSeq: input.AfterSeq})
		if err != nil {
			return AwaitResult{}, err
		}
		for _, m := range view.Messages {
			if m.Author == AuthorOperator {
				return AwaitResult{View: view, Reason: "operator_message"}, nil
			}
		}
		if isTerminal(view.State) {
			return AwaitResult{View: view, Reason: "terminal"}, nil
		}
		select {
		case <-ctx.Done():
			return AwaitResult{}, ctx.Err()
		case <-deadline.C:
			return AwaitResult{View: view, Reason: "timeout", TimedOut: true}, nil
		case <-ticker.C:
		}
	}
}

// Withdraw closes an artifact the caller no longer wants shown.
func (s *Service) Withdraw(ctx context.Context, input WithdrawInput) (View, error) {
	rec, err := s.inspectAs(ctx, input.ItemID, input.Caller.Scope)
	if err != nil {
		return View{}, err
	}
	if isTerminal(rec.Interaction.State) {
		return View{}, ErrTerminalConflict
	}
	reason := input.Reason
	if reason == "" {
		reason = "withdrawn by agent"
	}
	if _, err = s.interactions.CancelInteraction(ctx, interaction.CancelInteractionInput{
		InteractionID:    input.ItemID,
		ExpectedRevision: rec.Interaction.Revision,
		Requester:        input.Caller,
		Cause:            interaction.TerminalCauseCallerWithdrawn,
		Reason:           reason,
		Capability:       ReservedSurfaceCapability,
	}); err != nil {
		return View{}, err
	}
	return s.Get(ctx, GetInput{ItemID: input.ItemID, Caller: input.Caller})
}

func (s *Service) viewFor(ctx context.Context, rec interaction.TerminalOutcome, afterSeq int64) (View, error) {
	revisions, err := s.store.Revisions(ctx, rec.Interaction.ID)
	if err != nil {
		return View{}, err
	}
	messages, err := s.store.Messages(ctx, rec.Interaction.ID, afterSeq)
	if err != nil {
		return View{}, err
	}
	readAt, err := s.reads.ReadAtBulk(ctx, []string{rec.Interaction.ID})
	if err != nil {
		return View{}, err
	}
	return buildView(rec.Interaction, rec.Resolution, readAt, revisions, messages)
}

// Inspect returns one artifact for the operator.
func (s *Service) Inspect(ctx context.Context, itemID string) (View, error) {
	rec, err := s.inspectAs(ctx, itemID, OperatorParticipant.Scope)
	if err != nil {
		return View{}, err
	}
	return s.viewFor(ctx, rec, 0)
}

func (s *Service) Inbox(ctx context.Context) (Inbox, error) {
	result := Inbox{
		ContractVersion: ContractVersion,
		SurfaceID:       DefaultSurfaceID,
		Revision:        "empty",
		SyncedAt:        s.now(),
		Pending:         make([]View, 0),
		History:         make([]View, 0),
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
		return Inbox{}, err
	}
	resolutions := make(map[string]*interaction.ResolutionRecord, len(snapshot.Resolutions))
	for i := range snapshot.Resolutions {
		res := &snapshot.Resolutions[i]
		resolutions[res.InteractionID] = res
	}
	ids := make([]string, 0, len(snapshot.Interactions))
	for _, record := range snapshot.Interactions {
		if record.Definition.Kind == extensions.ArtifactItemEnvelopeType {
			ids = append(ids, record.ID)
		}
	}
	readAt, err := s.reads.ReadAtBulk(ctx, ids)
	if err != nil {
		return Inbox{}, err
	}
	revisions, err := s.store.RevisionsBulk(ctx, ids)
	if err != nil {
		return Inbox{}, err
	}
	messages, err := s.store.MessagesBulk(ctx, ids, 0)
	if err != nil {
		return Inbox{}, err
	}
	hidden, err := s.interactions.HiddenInboxItems(ctx)
	if err != nil {
		return Inbox{}, err
	}
	for _, record := range snapshot.Interactions {
		if record.Definition.Kind != extensions.ArtifactItemEnvelopeType || hidden[record.ID] {
			continue
		}
		view, viewErr := buildView(record, resolutions[record.ID], readAt, revisions[record.ID], messages[record.ID])
		if viewErr != nil {
			continue
		}
		if isTerminal(record.State) {
			result.History = append(result.History, view)
		} else {
			result.Pending = append(result.Pending, view)
		}
	}
	sort.SliceStable(result.Pending, func(i, j int) bool {
		return result.Pending[i].QueueSequence < result.Pending[j].QueueSequence
	})
	sort.SliceStable(result.History, func(i, j int) bool {
		return result.History[i].UpdatedAt.After(result.History[j].UpdatedAt)
	})
	result.TotalPending = len(result.Pending)
	result.TotalTerminal = len(result.History)
	result.Revision, err = s.Revision(ctx)
	if err != nil {
		return Inbox{}, err
	}
	return result, nil
}

// Revision is the change digest the event stream compares: the surface digest
// plus read state, the latest revision of each artifact and its message count.
func (s *Service) Revision(ctx context.Context) (string, error) {
	digestInput, err := s.interactions.SurfaceDigest(ctx, interaction.GetSurfaceInput{
		SurfaceID:      DefaultSurfaceID,
		RequesterScope: OperatorParticipant.Scope,
		Capability:     ReservedSurfaceCapability,
	}, extensions.ArtifactItemEnvelopeType)
	if errors.Is(err, interaction.ErrNotFound) {
		return "empty", nil
	}
	if err != nil {
		return "", err
	}
	ids := make([]string, 0, len(digestInput.Rows))
	for _, row := range digestInput.Rows {
		ids = append(ids, row.ID)
	}
	readAt, err := s.reads.ReadAtBulk(ctx, ids)
	if err != nil {
		return "", err
	}
	progress, err := s.store.ProgressAll(ctx)
	if err != nil {
		return "", err
	}
	hidden, err := s.interactions.HiddenInboxItems(ctx)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	_, _ = fmt.Fprintf(digest, "%s:%d", digestInput.SurfaceID, digestInput.Revision)
	for _, row := range digestInput.Rows {
		if hidden[row.ID] {
			continue
		}
		_, isRead := readAt[row.ID]
		p := progress[row.ID]
		_, _ = fmt.Fprintf(digest, "|%s:%d:%s:%d:%v:%d:%d", row.ID, row.SurfaceSequence, row.State, row.Revision, isRead, p.LatestRevision, p.Messages)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// MarkRead is the only thing that sets read state; viewing never does.
func (s *Service) MarkRead(ctx context.Context, itemID string) (View, error) {
	if _, err := s.Inspect(ctx, itemID); err != nil {
		return View{}, err
	}
	if err := s.reads.MarkRead(ctx, itemID); err != nil {
		return View{}, err
	}
	return s.Inspect(ctx, itemID)
}

// OperatorMessage records a message from the operator. Request changes is a
// message of its own kind and leaves the artifact open.
func (s *Service) OperatorMessage(ctx context.Context, input OperatorMessageInput) (View, error) {
	view, err := s.Inspect(ctx, input.ItemID)
	if err != nil {
		return View{}, err
	}
	if isTerminal(view.State) {
		return View{}, ErrTerminalConflict
	}
	kind := input.Kind
	if kind == "" {
		kind = KindMessage
	}
	if kind != KindMessage && kind != KindChangesRequested {
		return View{}, fmt.Errorf("%w: unsupported message kind %q", ErrInvalidRequest, kind)
	}
	body, err := checkBody(input.Body)
	if err != nil {
		return View{}, err
	}
	if _, err = s.store.AddMessage(ctx, input.ItemID, AuthorOperator, kind, body, view.LatestRevision); err != nil {
		return View{}, err
	}
	return s.Inspect(ctx, input.ItemID)
}

// Signoff approves the artifact: it records an approved message and resolves
// the interaction with the operator's typed response.
func (s *Service) Signoff(ctx context.Context, input SignoffInput) (View, error) {
	if input.ExpectedRevision < 1 {
		return View{}, fmt.Errorf("%w: expected_revision must be positive", ErrInvalidRequest)
	}
	current, err := s.inspectAs(ctx, input.ItemID, OperatorParticipant.Scope)
	if err != nil {
		return View{}, err
	}
	if current.Interaction.Revision != input.ExpectedRevision {
		return View{}, fmt.Errorf("%w: expected %d, got %d", ErrStaleRevision, input.ExpectedRevision, current.Interaction.Revision)
	}
	if isTerminal(current.Interaction.State) {
		return View{}, ErrTerminalConflict
	}
	revisions, err := s.store.Revisions(ctx, input.ItemID)
	if err != nil {
		return View{}, err
	}
	latest := 0
	if n := len(revisions); n > 0 {
		latest = revisions[n-1].Revision
	}
	payload, err := json.Marshal(Response{Action: "approved", Note: input.Note, Revision: latest})
	if err != nil {
		return View{}, fmt.Errorf("%w: marshal response: %w", ErrInvalidRequest, err)
	}
	projRev := int64(1)
	if current.Interaction.PresentedProjectionRevision != nil {
		projRev = *current.Interaction.PresentedProjectionRevision
	}
	if _, err = s.interactions.ResolveInteraction(ctx, interaction.ResolveInteractionInput{
		InteractionID:               input.ItemID,
		ExpectedInteractionRevision: input.ExpectedRevision,
		PresentedProjectionRevision: projRev,
		Participant:                 participantOrDefault(input.Participant),
		ResponseKind:                "data",
		ResponsePayload:             payload,
		Capability:                  ReservedSurfaceCapability,
	}); err != nil {
		return View{}, err
	}
	body := strings.TrimSpace(input.Note)
	if body == "" {
		body = "Approved."
	}
	if _, msgErr := s.store.AddMessage(ctx, input.ItemID, AuthorOperator, KindApproved, body, latest); msgErr != nil {
		return View{}, msgErr
	}
	return s.Inspect(ctx, input.ItemID)
}

func participantOrDefault(binding interaction.ActorBinding) interaction.ActorBinding {
	if binding == (interaction.ActorBinding{}) {
		return OperatorParticipant
	}
	return binding
}

// Archive closes a pending artifact and retains it in operator history.
func (s *Service) Archive(ctx context.Context, input ArchiveInput) (View, error) {
	current, err := s.inspectAs(ctx, input.ItemID, OperatorParticipant.Scope)
	if err != nil {
		return View{}, err
	}
	if isTerminal(current.Interaction.State) {
		return View{}, ErrTerminalConflict
	}
	reason := input.Reason
	if reason == "" {
		reason = "archived by operator"
	}
	exp := input.ExpectedRevision
	if exp < 1 {
		exp = current.Interaction.Revision
	}
	if _, err = s.interactions.CancelInteraction(ctx, interaction.CancelInteractionInput{
		InteractionID:    input.ItemID,
		ExpectedRevision: exp,
		Requester:        OperatorParticipant,
		Cause:            interaction.TerminalCauseParticipantCanceled,
		Reason:           reason,
		Capability:       ReservedSurfaceCapability,
	}); err != nil {
		return View{}, err
	}
	return s.Inspect(ctx, input.ItemID)
}

// Delete hides an artifact from operator lists while preserving the immutable
// interaction record. Revision files stay on disk until retention removes them.
func (s *Service) Delete(ctx context.Context, input ArchiveInput) (View, error) {
	current, err := s.Inspect(ctx, input.ItemID)
	if err != nil {
		return View{}, err
	}
	if input.ExpectedRevision != current.Revision {
		return View{}, ErrStaleRevision
	}
	if !isTerminal(current.State) {
		input.Reason = "deleted by operator"
		current, err = s.Archive(ctx, input)
		if err != nil {
			return View{}, err
		}
	}
	if err = s.interactions.HideBrowserInboxItem(ctx, input.ItemID); err != nil {
		return View{}, err
	}
	return current, nil
}

// OpenRevision returns one revision's metadata and file for the content route.
func (s *Service) OpenRevision(ctx context.Context, itemID string, revision int) (Revision, *os.File, error) {
	if _, err := s.inspectAs(ctx, itemID, OperatorParticipant.Scope); err != nil {
		return Revision{}, nil, err
	}
	return s.store.OpenRevision(ctx, itemID, revision)
}

func (s *Service) ensureDefaultSurface(ctx context.Context) error {
	_, err := s.interactions.OpenSurface(ctx, interaction.OpenSurfaceInput{
		ID:             DefaultSurfaceID,
		Caller:         OperatorParticipant,
		IdempotencyKey: "artifacts-default-surface-v1",
		OwnerScope:     OperatorParticipant.Scope,
		Metadata:       json.RawMessage(`{"purpose":"artifact_inbox"}`),
		Capability:     ReservedSurfaceCapability,
	})
	return err
}

func buildView(rec interaction.InteractionRecord, res *interaction.ResolutionRecord, readAt map[string]time.Time, revisions []Revision, messages []Message) (View, error) {
	var req Request
	if err := json.Unmarshal(rec.RequestSnapshot, &req); err != nil {
		return View{}, err
	}
	var resolution *Resolution
	if res != nil {
		var resp Response
		_ = json.Unmarshal(res.ResponsePayload, &resp)
		resolution = &Resolution{
			ResolutionID: res.ID,
			Action:       resp.Action,
			Note:         resp.Note,
			Revision:     resp.Revision,
			ResolvedAt:   res.RecordedAt,
			ResolvedBy:   res.ParticipantRef,
		}
	}
	var read *time.Time
	if t, ok := readAt[rec.ID]; ok {
		read = &t
	}
	if revisions == nil {
		revisions = []Revision{}
	}
	if messages == nil {
		messages = []Message{}
	}
	latest := 0
	if n := len(revisions); n > 0 {
		latest = revisions[n-1].Revision
	}
	return View{
		ContractVersion: ContractVersion,
		ItemID:          rec.ID,
		AgentID:         req.Source.AgentID,
		AgentLabel:      req.Source.AgentLabel,
		ApplicationID:   req.Source.ApplicationID,
		Title:           req.Title,
		Summary:         req.Summary,
		Type:            req.Type,
		RequiresSignoff: req.RequiresSignoff,
		Tags:            req.Tags,
		Correlations:    req.Correlations,
		State:           rec.State,
		QueueSequence:   rec.SurfaceSequence,
		Revision:        rec.Revision,
		CreatedAt:       rec.CreatedAt,
		UpdatedAt:       rec.UpdatedAt,
		ReadAt:          read,
		Resolution:      resolution,
		Revisions:       revisions,
		LatestRevision:  latest,
		Messages:        messages,
		InboxPath:       InboxPathPrefix + rec.ID,
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

// sameRequest reports whether two request bodies are semantically equal JSON.
func sameRequest(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	ac, aerr := json.Marshal(av)
	bc, berr := json.Marshal(bv)
	return aerr == nil && berr == nil && string(ac) == string(bc)
}
