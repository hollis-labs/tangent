package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	gmcpserver "github.com/hollis-labs/libs/plugin-mcp/go-mcp/server"

	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
)

func (s *Server) registerHITLTools() error {
	tools := []struct {
		name        string
		description string
		definition  string
		output      string
		handler     func(context.Context, map[string]any) (any, error)
	}{
		{
			name: "tangent.hitl_enqueue", definition: extensions.HITLItemRequestDefinition,
			output:      extensions.HITLItemHandleDefinition,
			description: "Enqueue one durable approval or persistent-attention item and immediately return its stable handle and FIFO position.",
			handler:     s.handleHITLEnqueue,
		},
		{
			name: "tangent.hitl_get", definition: extensions.HITLGetCommandDefinition,
			output:      extensions.HITLRetrievalResultDefinition,
			description: "Get the current durable HITL item or its immutable terminal outcome by stable item handle.",
			handler:     s.handleHITLGet,
		},
		{
			name: "tangent.hitl_await", definition: extensions.HITLAwaitCommandDefinition,
			output:      extensions.HITLRetrievalResultDefinition,
			description: "Wait at most 50 seconds for a durable HITL item to become terminal; timeout changes no lifecycle state.",
			handler:     s.handleHITLAwait,
		},
		{
			name: "tangent.hitl_withdraw", definition: extensions.HITLWithdrawCommandDefinition,
			output:      extensions.HITLTerminalOutcomeDefinition,
			description: "Withdraw one caller-owned HITL item with an immutable caller_withdrawn terminal outcome.",
			handler:     s.handleHITLWithdraw,
		},
	}
	for _, tool := range tools {
		schema, err := buildHITLSchema(tool.definition)
		if err != nil {
			return fmt.Errorf("build %s input schema: %w", tool.name, err)
		}
		outputSchema, err := buildHITLOutputSchema(tool.output)
		if err != nil {
			return fmt.Errorf("build %s output schema: %w", tool.name, err)
		}
		addTool(s, gmcpserver.Tool{
			Name: tool.name, Description: tool.description,
			InputSchema: schema, OutputSchema: outputSchema,
		}, tool.handler)
	}
	return nil
}

func buildHITLOutputSchema(definition string) (*jsonschema.Schema, error) {
	raw, err := extensions.HITLContractDefinitionSchema(definition)
	if err != nil {
		return nil, err
	}
	var root map[string]json.RawMessage
	if decodeErr := json.Unmarshal(raw, &root); decodeErr != nil {
		return nil, decodeErr
	}
	// MCP requires outputSchema to have an explicit object root. The terminal
	// and retrieval definitions are discriminated oneOf unions of objects, so
	// this constraint is additive and retains the exact union below it.
	root["type"] = json.RawMessage(`"object"`)
	raw, err = json.Marshal(root)
	if err != nil {
		return nil, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	return &schema, nil
}

func buildHITLSchema(definition string) (*jsonschema.Schema, error) {
	raw, err := extensions.HITLContractDefinitionSchema(definition)
	if err != nil {
		return nil, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	if schema.Type != "object" {
		return nil, fmt.Errorf("definition %s has non-object root %q", definition, schema.Type)
	}
	return &schema, nil
}

func (s *Server) handleHITLEnqueue(
	ctx context.Context,
	input map[string]any,
) (any, error) {
	source, ok := objectField(input, "source")
	if !ok {
		return nil, s.hitlError(fmt.Errorf("%w: source is required", hitl.ErrInvalidRequest))
	}
	applicationID, _ := source["application_id"].(string)
	agentID, _ := source["agent_id"].(string)
	return s.hitlResult(s.hitl.Enqueue(ctx, hitl.EnqueueInput{
		Request: rawJSON(input), Caller: directHITLActor(applicationID, agentID),
	}))
}

func (s *Server) handleHITLGet(
	ctx context.Context,
	input map[string]any,
) (any, error) {
	caller, ok := objectField(input, "caller")
	if !ok {
		return nil, s.hitlError(fmt.Errorf("%w: caller is required", hitl.ErrInvalidRequest))
	}
	return s.hitlResult(s.hitl.Get(ctx, hitl.GetInput{
		ItemID: stringField(input, "item_id"), Caller: directHITLCallerAssertion(caller),
		TransportCorrelation: directHITLCorrelation("get"),
	}))
}

func (s *Server) handleHITLAwait(
	ctx context.Context,
	input map[string]any,
) (any, error) {
	caller, ok := objectField(input, "caller")
	if !ok {
		return nil, s.hitlError(fmt.Errorf("%w: caller is required", hitl.ErrInvalidRequest))
	}
	var wait *time.Duration
	if rawWait, exists := input["wait_ms"]; exists {
		milliseconds, ok := integerField(rawWait)
		if !ok {
			return nil, s.hitlError(fmt.Errorf("%w: wait_ms must be an integer", hitl.ErrInvalidRequest))
		}
		duration := time.Duration(milliseconds) * time.Millisecond
		wait = &duration
	}
	return s.hitlResult(s.hitl.Await(ctx, hitl.AwaitInput{
		ItemID: stringField(input, "item_id"), Caller: directHITLCallerAssertion(caller),
		Wait: wait, TransportCorrelation: directHITLCorrelation("await"),
	}))
}

func (s *Server) handleHITLWithdraw(
	ctx context.Context,
	input map[string]any,
) (any, error) {
	caller, ok := objectField(input, "caller")
	if !ok {
		return nil, s.hitlError(fmt.Errorf("%w: caller is required", hitl.ErrInvalidRequest))
	}
	var expected *int64
	if value, exists := input["expected_revision"]; exists {
		revision, ok := integerField(value)
		if !ok {
			return nil, s.hitlError(fmt.Errorf("%w: expected_revision must be an integer", hitl.ErrInvalidRequest))
		}
		expected = &revision
	}
	return s.hitlResult(s.hitl.Withdraw(ctx, hitl.WithdrawInput{
		ItemID: stringField(input, "item_id"), Caller: directHITLCallerAssertion(caller),
		ExpectedRevision: expected, Reason: stringField(input, "reason"),
	}))
}

func (s *Server) hitlResult(value any, err error) (any, error) {
	if err != nil {
		return nil, s.hitlError(err)
	}
	return value, nil
}

func (s *Server) hitlError(err error) error {
	body := map[string]any{
		"contract_version": hitl.ContractVersion,
		"code":             "hitl_error",
		"message":          err.Error(),
	}
	var idempotencyConflict *hitl.IdempotencyConflictError
	var stale *hitl.StaleRevisionError
	var terminalConflict *hitl.TerminalConflictError
	switch {
	case errors.As(err, &idempotencyConflict):
		body["code"] = "idempotency_conflict"
		body["idempotency_key"] = idempotencyConflict.IdempotencyKey
		body["existing_item_id"] = idempotencyConflict.ExistingItemID
	case errors.As(err, &stale):
		body["code"] = "stale_revision"
		body["operation"] = stale.Operation
		body["item_id"] = stale.ItemID
		body["revision_kind"] = stale.RevisionKind
		body["expected_revision"] = stale.ExpectedRevision
		body["actual_revision"] = stale.ActualRevision
		body["current_state"] = stale.CurrentState
		if stale.TerminalOutcome != nil {
			body["terminal_outcome"] = stale.TerminalOutcome
		}
	case errors.As(err, &terminalConflict):
		body["code"] = "terminal_conflict"
		body["terminal_outcome"] = terminalConflict.Outcome
	case errors.Is(err, interaction.ErrNotFound):
		body["code"] = "not_found"
	case errors.Is(err, interaction.ErrUnauthorized):
		body["code"] = "unauthorized"
	case errors.Is(err, interaction.ErrDefinitionValidation), errors.Is(err, hitl.ErrInvalidRequest):
		body["code"] = "validation_failed"
	case errors.Is(err, context.Canceled):
		body["code"] = "wait_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		body["code"] = "await_timeout"
	}
	return &rawToolError{message: err.Error(), body: body}
}

// directHITLActor derives the caller identity behind one hitl_* call.
//
// The request shape is unchanged: `source.application_id` and
// `caller.application_id` are still what a caller sends. Only the scope the
// host derives from them changes spelling — `direct-loopback:<app>` becomes
// the canonical `standalone-local:<app>`, and the old spelling still reads as
// the same caller through the fixed alias, with no data rewrite.
func directHITLActor(applicationID, principalRef string) interaction.ActorBinding {
	applicationID = strings.TrimSpace(applicationID)
	principalRef = strings.TrimSpace(principalRef)
	if principalRef == "" {
		principalRef = applicationID
	}
	return declaredCaller(applicationID, principalRef)
}

func directHITLCallerAssertion(caller map[string]any) interaction.ActorBinding {
	return directHITLActor(stringField(caller, "application_id"), stringField(caller, "principal_ref"))
}

func directHITLCorrelation(operation string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"adapter": "direct-mcp", "operation": operation})
	return raw
}

func objectField(object map[string]any, name string) (map[string]any, bool) {
	value, ok := object[name].(map[string]any)
	return value, ok
}

func stringField(object map[string]any, name string) string {
	value, _ := object[name].(string)
	return value
}

func integerField(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		integer := int64(typed)
		return integer, float64(integer) == typed
	case json.Number:
		integer, err := typed.Int64()
		return integer, err == nil
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	default:
		return 0, false
	}
}
