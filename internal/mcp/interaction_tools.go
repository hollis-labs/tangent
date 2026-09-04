package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/interaction"
)

type interactionListKindsInput struct{}

type interactionResolveDefinitionInput struct {
	Kind    string `json:"kind"`
	Version string `json:"version,omitempty"`
}

// assertedActorInput contains only caller assertions. Direct MCP never accepts
// authority or assurance claims from the wire; the adapter pins both values.
type assertedActorInput struct {
	Scope        string `json:"scope"`
	PrincipalRef string `json:"principal_ref"`
}

type surfaceOpenInput struct {
	SurfaceID      string             `json:"surface_id,omitempty"`
	Caller         assertedActorInput `json:"caller"`
	IdempotencyKey string             `json:"idempotency_key"`
	OwnerScope     string             `json:"owner_scope"`
	Metadata       map[string]any     `json:"metadata,omitempty"`
	Policy         map[string]any     `json:"policy,omitempty"`
}

type surfaceGetInput struct {
	SurfaceID      string `json:"surface_id"`
	RequesterScope string `json:"requester_scope"`
}

type surfaceCloseInput struct {
	SurfaceID        string             `json:"surface_id"`
	ExpectedRevision int64              `json:"expected_revision"`
	Requester        assertedActorInput `json:"requester"`
	Reason           string             `json:"reason,omitempty"`
	PolicyRef        string             `json:"policy_ref"`
	Metadata         map[string]any     `json:"metadata,omitempty"`
}

type interactionSubmitInput struct {
	InteractionID  string                    `json:"interaction_id,omitempty"`
	SurfaceID      string                    `json:"surface_id"`
	Caller         assertedActorInput        `json:"caller"`
	IdempotencyKey string                    `json:"idempotency_key"`
	Definition     interaction.DefinitionRef `json:"definition"`
	Request        map[string]any            `json:"request"`
	ExternalRefs   map[string]any            `json:"external_refs,omitempty"`
	Policy         map[string]any            `json:"policy,omitempty"`
}

type interactionGetInput struct {
	InteractionID        string         `json:"interaction_id"`
	RequesterScope       string         `json:"requester_scope"`
	TransportCorrelation map[string]any `json:"transport_correlation,omitempty"`
}

type interactionAwaitInput struct {
	InteractionID        string         `json:"interaction_id"`
	RequesterScope       string         `json:"requester_scope"`
	MaximumWaitMillis    int64          `json:"maximum_wait_ms"`
	TransportCorrelation map[string]any `json:"transport_correlation,omitempty"`
}

type interactionCancelInput struct {
	InteractionID    string                    `json:"interaction_id"`
	ExpectedRevision int64                     `json:"expected_revision"`
	Requester        assertedActorInput        `json:"requester"`
	Cause            interaction.TerminalCause `json:"cause"`
	Reason           string                    `json:"reason,omitempty"`
}

type interactionSupersedeInput struct {
	InteractionID            string             `json:"interaction_id"`
	ExpectedRevision         int64              `json:"expected_revision"`
	ReplacementInteractionID string             `json:"replacement_interaction_id"`
	Requester                assertedActorInput `json:"requester"`
	Reason                   string             `json:"reason,omitempty"`
}

func (s *Server) registerInteractionTools() error {
	if err := addInteractionTool(s, "tangent.interaction_list_kinds", "List immutable interaction definitions available to the generic asynchronous service.", s.handleInteractionListKinds); err != nil {
		return err
	}
	if err := addInteractionTool(s, "tangent.interaction_resolve_definition", "Resolve a kind and optional version to the exact immutable definition binding Tangent will persist.", s.handleInteractionResolveDefinition); err != nil {
		return err
	}
	if err := addInteractionTool(s, "tangent.surface_open", "Open and activate a durable surface, returning its stable handle immediately. Retries are scoped by caller and idempotency key.", s.handleSurfaceOpen); err != nil {
		return err
	}
	if err := addInteractionTool(s, "tangent.surface_get", "Hydrate one durable surface and all of its canonical interaction records by handle.", s.handleSurfaceGet); err != nil {
		return err
	}
	if err := addInteractionTool(s, "tangent.surface_close", "Close a durable surface and atomically disposition all outstanding interactions under the named surface policy.", s.handleSurfaceClose); err != nil {
		return err
	}
	if err := addInteractionTool(s, "tangent.interaction_submit", "Submit a validated immutable interaction and return its stable handle immediately.", s.handleInteractionSubmit); err != nil {
		return err
	}
	if err := addInteractionTool(s, "tangent.interaction_get", "Get current or terminal interaction state by durable handle and record terminal retrieval separately from delivery.", s.handleInteractionGet); err != nil {
		return err
	}
	if err := s.addInteractionAwaitTool(); err != nil {
		return err
	}
	if err := addInteractionTool(s, "tangent.interaction_cancel", "Withdraw or cancel a caller-owned interaction using its durable revision.", s.handleInteractionCancel); err != nil {
		return err
	}
	if err := addInteractionTool(s, "tangent.interaction_supersede", "Supersede a caller-owned interaction with another durable interaction on the same surface.", s.handleInteractionSupersede); err != nil {
		return err
	}
	if err := addInteractionTool(s, "tangent.interaction_acknowledge", "Acknowledge an immutable terminal outcome exactly once. Idempotent, and deliberately separate from retrieval and delivery: reading a result or receiving it over HTTP does not acknowledge it.", s.handleInteractionAcknowledge); err != nil {
		return err
	}
	return nil
}

func (s *Server) addInteractionAwaitTool() error {
	schema, err := jsonschema.For[interactionAwaitInput](nil)
	if err != nil {
		return fmt.Errorf("build tangent.interaction_await input schema: %w", err)
	}
	waitSchema := schema.Properties["maximum_wait_ms"]
	if waitSchema == nil {
		return errors.New("build tangent.interaction_await input schema: maximum_wait_ms is missing")
	}
	waitSchema.Minimum = jsonschema.Ptr(1.0)
	waitSchema.Maximum = jsonschema.Ptr(50_000.0)
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.interaction_await",
		Description: "Wait up to 50 seconds for a durable interaction terminal outcome; timeout never changes interaction lifecycle.",
		InputSchema: schema,
	}, s.handleInteractionAwait)
	return nil
}

func addInteractionTool[Input any](
	server *Server,
	name string,
	description string,
	handler mcpsdk.ToolHandlerFor[Input, any],
) error {
	schema, err := jsonschema.For[Input](nil)
	if err != nil {
		return fmt.Errorf("build %s input schema: %w", name, err)
	}
	mcpsdk.AddTool(server.mcp, &mcpsdk.Tool{Name: name, Description: description, InputSchema: schema}, handler)
	return nil
}

func (s *Server) handleInteractionListKinds(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	_ interactionListKindsInput,
) (*mcpsdk.CallToolResult, any, error) {
	return s.interactionResult(s.interactions.ListInteractionKinds(ctx))
}

func (s *Server) handleInteractionResolveDefinition(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	input interactionResolveDefinitionInput,
) (*mcpsdk.CallToolResult, any, error) {
	return s.interactionResult(s.interactions.ResolveInteractionDefinition(ctx, interaction.DefinitionRef{
		Kind: input.Kind, Version: input.Version,
	}))
}

func (s *Server) handleSurfaceOpen(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	input surfaceOpenInput,
) (*mcpsdk.CallToolResult, any, error) {
	return s.interactionResult(s.interactions.OpenSurface(ctx, interaction.OpenSurfaceInput{
		ID: input.SurfaceID, Caller: directMCPActor(input.Caller), IdempotencyKey: input.IdempotencyKey,
		OwnerScope: input.OwnerScope, Metadata: rawJSON(input.Metadata), Policy: rawJSON(input.Policy),
	}))
}

func (s *Server) handleSurfaceGet(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	input surfaceGetInput,
) (*mcpsdk.CallToolResult, any, error) {
	return s.interactionResult(s.interactions.GetSurface(ctx, interaction.GetSurfaceInput{
		SurfaceID: input.SurfaceID, RequesterScope: input.RequesterScope,
	}))
}

func (s *Server) handleSurfaceClose(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	input surfaceCloseInput,
) (*mcpsdk.CallToolResult, any, error) {
	return s.interactionResult(s.interactions.CloseSurface(ctx, interaction.CloseSurfaceInput{
		SurfaceID: input.SurfaceID, ExpectedRevision: input.ExpectedRevision,
		Requester: directMCPActor(input.Requester), Reason: input.Reason, PolicyRef: input.PolicyRef,
		Metadata: rawJSON(input.Metadata),
	}))
}

func (s *Server) handleInteractionSubmit(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	input interactionSubmitInput,
) (*mcpsdk.CallToolResult, any, error) {
	return s.interactionResult(s.interactions.SubmitInteraction(ctx, interaction.SubmitInteractionInput{
		ID: input.InteractionID, SurfaceID: input.SurfaceID, Caller: directMCPActor(input.Caller),
		IdempotencyKey: input.IdempotencyKey, Definition: input.Definition,
		Request: rawJSON(input.Request), ExternalRefs: rawJSON(input.ExternalRefs), Policy: rawJSON(input.Policy),
	}))
}

func (s *Server) handleInteractionGet(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	input interactionGetInput,
) (*mcpsdk.CallToolResult, any, error) {
	return s.interactionResult(s.interactions.GetInteraction(ctx, interaction.GetInteractionInput{
		InteractionID: input.InteractionID, RequesterScope: input.RequesterScope,
		TransportCorrelation: rawJSON(input.TransportCorrelation),
	}))
}

func (s *Server) handleInteractionAwait(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	input interactionAwaitInput,
) (*mcpsdk.CallToolResult, any, error) {
	return s.interactionResult(s.interactions.AwaitResolution(ctx, interaction.AwaitResolutionInput{
		InteractionID: input.InteractionID, RequesterScope: input.RequesterScope,
		MaximumWaitMillis:    input.MaximumWaitMillis,
		TransportCorrelation: rawJSON(input.TransportCorrelation),
	}))
}

func (s *Server) handleInteractionCancel(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	input interactionCancelInput,
) (*mcpsdk.CallToolResult, any, error) {
	if input.Cause != interaction.TerminalCauseCallerWithdrawn &&
		input.Cause != interaction.TerminalCauseCallerCanceled {
		return s.interactionError(interaction.ErrUnauthorized)
	}
	return s.interactionResult(s.interactions.CancelInteraction(ctx, interaction.CancelInteractionInput{
		InteractionID: input.InteractionID, ExpectedRevision: input.ExpectedRevision,
		Requester: directMCPActor(input.Requester), Cause: input.Cause, Reason: input.Reason,
	}))
}

func (s *Server) handleInteractionSupersede(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	input interactionSupersedeInput,
) (*mcpsdk.CallToolResult, any, error) {
	return s.interactionResult(s.interactions.SupersedeInteraction(ctx, interaction.SupersedeInteractionInput{
		InteractionID: input.InteractionID, ExpectedRevision: input.ExpectedRevision,
		ReplacementInteractionID: input.ReplacementInteractionID,
		Requester:                directMCPActor(input.Requester), Reason: input.Reason,
	}))
}

func (s *Server) interactionResult(value any, err error) (*mcpsdk.CallToolResult, any, error) {
	if err != nil {
		return s.interactionError(err)
	}
	return nil, value, nil
}

func (s *Server) interactionError(err error) (*mcpsdk.CallToolResult, any, error) {
	code := "interaction_error"
	// A definition this host cannot serve is a distinguishable state, not one
	// opaque failure (ADR 0003 §8 C7): a caller's next move differs between
	// "this host is too old for that definition", "its capability request was
	// refused", and "an operator turned it off". These codes are additive —
	// before the versioned registry every shipped definition was available, so
	// no caller can have been relying on the collapsed code.
	var definitionState *interaction.DefinitionStateError
	if errors.As(err, &definitionState) {
		return toolErrorResult("definition_"+definitionState.State, definitionState.Error()), nil, nil
	}
	switch {
	case errors.Is(err, interaction.ErrIdempotencyConflict):
		code = "idempotency_conflict"
	case errors.Is(err, interaction.ErrRevisionConflict):
		code = "stale_revision"
	case errors.Is(err, interaction.ErrNotFound):
		code = "not_found"
	case errors.Is(err, interaction.ErrDefinitionNotFound):
		code = "definition_not_found"
	case errors.Is(err, interaction.ErrDefinitionUnavailable):
		code = "definition_unavailable"
	case errors.Is(err, interaction.ErrDefinitionValidation):
		code = "validation_failed"
	case errors.Is(err, interaction.ErrUnauthorized):
		code = "unauthorized"
	case errors.Is(err, interaction.ErrWaitTimeout):
		code = "await_timeout"
	case errors.Is(err, interaction.ErrTerminal):
		code = "terminal"
	case errors.Is(err, interaction.ErrNotRespondable):
		code = "not_respondable"
	case errors.Is(err, interaction.ErrInvalidRecord):
		code = "invalid_request"
	case errors.Is(err, context.Canceled):
		code = "wait_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		code = "await_timeout"
	}
	return toolErrorResult(code, err.Error()), nil, nil
}

func rawJSON(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return raw
}

func directMCPActor(assertion assertedActorInput) interaction.ActorBinding {
	return interaction.ActorBinding{
		Scope: assertion.Scope, PrincipalRef: assertion.PrincipalRef,
		Authority: "direct-mcp", Assurance: "asserted",
	}
}
