package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	gmcpserver "github.com/hollis-labs/go-mcp/server"

	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/turns"
)

// turnAwaitInput is tangent.turn_await's arguments. WaitMillis is a pointer
// because absent and zero mean different things: absent takes the default wait,
// zero looks once and returns.
type turnAwaitInput struct {
	SessionID  string `json:"session_id" jsonschema:"The agent session whose answered turns to return."`
	WaitMillis *int64 `json:"wait_ms,omitempty" jsonschema:"How long to wait for a reply, in milliseconds, from 0 to 50000. Omit for 30000. Zero looks once and returns."`
}

// turnAckInput is tangent.turn_ack's arguments.
type turnAckInput struct {
	ItemID  string `json:"item_id" jsonschema:"The item_id of the turn whose reply was delivered."`
	ReplyID string `json:"reply_id,omitempty" jsonschema:"The reply's resolution_id, to assert which reply was delivered. Refused if it is not this turn's reply."`
}

func (s *Server) registerTurnsTools() error {
	enqueueSchema, err := buildTurnsEnqueueSchema()
	if err != nil {
		return fmt.Errorf("build tangent.turns_enqueue input schema: %w", err)
	}
	awaitSchema, err := buildTurnAwaitSchema()
	if err != nil {
		return fmt.Errorf("build tangent.turn_await input schema: %w", err)
	}
	ackSchema, err := jsonschema.For[turnAckInput](nil)
	if err != nil {
		return fmt.Errorf("build tangent.turn_ack input schema: %w", err)
	}

	addTool(s, gmcpserver.Tool{
		Name: "tangent.turns_enqueue",
		Description: "Enqueue one agent turn — a question, approval, checkpoint, failure or terminal result — " +
			"in the operator's /turns inbox, and return its durable item handle, including its FIFO queue_sequence. " +
			"Repeating an idempotency_key returns the original item rather than creating a second.",
		InputSchema: enqueueSchema,
	}, s.handleTurnsEnqueue)
	addTool(s, gmcpserver.Tool{
		Name: "tangent.turn_await",
		Description: "Wait up to 50 seconds for the operator to answer a turn in a session, and return the replies " +
			"not yet acknowledged, oldest answer first. A timeout returns an empty list and changes nothing. " +
			"Call tangent.turn_ack for each reply you have handled: an unacknowledged reply is returned again.",
		InputSchema: awaitSchema,
	}, s.handleTurnAwait)
	addTool(s, gmcpserver.Tool{
		Name: "tangent.turn_ack",
		Description: "Acknowledge that the operator's reply to one turn reached its agent, so tangent.turn_await " +
			"stops returning it. Idempotent. Refused for a turn nobody has answered.",
		InputSchema: ackSchema,
	}, s.handleTurnAck)
	return nil
}

// buildTurnsEnqueueSchema uses the packaged request schema as the tool's input,
// so the tool accepts exactly what POST /api/turns/enqueue does and the two
// cannot drift.
func buildTurnsEnqueueSchema() (*jsonschema.Schema, error) {
	var schema jsonschema.Schema
	if err := json.Unmarshal(extensions.AgentTurnContractSchema(), &schema); err != nil {
		return nil, err
	}
	if schema.Type != "object" {
		return nil, fmt.Errorf("agent-turn request schema has non-object root %q", schema.Type)
	}
	return &schema, nil
}

func buildTurnAwaitSchema() (*jsonschema.Schema, error) {
	schema, err := jsonschema.For[turnAwaitInput](nil)
	if err != nil {
		return nil, err
	}
	wait := schema.Properties["wait_ms"]
	if wait == nil {
		return nil, errors.New("wait_ms is missing")
	}
	// A pointer field reflects as nullable; the wire form is an integer or
	// absent, never null.
	wait.Type, wait.Types = "integer", nil
	wait.Minimum = jsonschema.Ptr(0.0)
	wait.Maximum = jsonschema.Ptr(float64(turns.MaximumAwaitWait.Milliseconds()))
	return schema, nil
}

func (s *Server) handleTurnsEnqueue(
	ctx context.Context,
	input map[string]any,
) (any, error) {
	source, ok := objectField(input, "source")
	if !ok {
		return nil, s.turnsError(fmt.Errorf("%w: source is required", turns.ErrInvalidRequest))
	}
	applicationID, _ := source["application_id"].(string)
	agentID, _ := source["agent_id"].(string)
	return s.turnsResult(s.turns.Enqueue(ctx, turns.EnqueueInput{
		Request: rawJSON(input), Caller: directHITLActor(applicationID, agentID),
	}))
}

func (s *Server) handleTurnAwait(
	ctx context.Context,
	input turnAwaitInput,
) (any, error) {
	var wait *time.Duration
	if input.WaitMillis != nil {
		duration := time.Duration(*input.WaitMillis) * time.Millisecond
		wait = &duration
	}
	return s.turnsResult(s.turns.Await(ctx, turns.AwaitInput{SessionID: input.SessionID, Wait: wait}))
}

func (s *Server) handleTurnAck(
	ctx context.Context,
	input turnAckInput,
) (any, error) {
	if err := s.turns.Ack(ctx, turns.AckInput{ItemID: input.ItemID, ReplyID: input.ReplyID}); err != nil {
		return nil, s.turnsError(err)
	}
	// The same body POST /api/turns/items/{id}/ack answers with.
	return map[string]any{
		"contract_version": turns.ContractVersion,
		"status":           "acknowledged",
		"item_id":          input.ItemID,
	}, nil
}

func (s *Server) turnsResult(value any, err error) (any, error) {
	if err != nil {
		return nil, s.turnsError(err)
	}
	return value, nil
}

func (s *Server) turnsError(err error) error {
	body := map[string]any{
		"contract_version": turns.ContractVersion,
		"code":             "turns_error",
		"message":          err.Error(),
	}
	switch {
	case errors.Is(err, interaction.ErrIdempotencyConflict):
		body["code"] = "idempotency_conflict"
	case errors.Is(err, turns.ErrNotFound), errors.Is(err, interaction.ErrNotFound):
		body["code"] = "not_found"
	case errors.Is(err, interaction.ErrUnauthorized):
		body["code"] = "unauthorized"
	case errors.Is(err, interaction.ErrDefinitionValidation), errors.Is(err, turns.ErrInvalidRequest):
		body["code"] = "validation_failed"
	case errors.Is(err, context.Canceled):
		body["code"] = "wait_canceled"
	}
	return &rawToolError{message: err.Error(), body: body}
}
