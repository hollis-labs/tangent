package mcp

import (
	"context"
	"errors"
	"fmt"

	envelopes "github.com/hollis-labs/go-envelopes"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/room"
)

// errorCodeNotWired is the legacy sentinel surfaced by PR 3 when no
// handler was registered for triage. PR 4 wires a real handler so the
// dispatcher path no longer collapses to NOT_WIRED in production; we
// keep the constant for tests that still pin against it (the pure
// dispatcher-only wiring path: env+dispatcher with no triage handler
// installed).
const errorCodeNotWired = "NOT_WIRED"

// errorCodeRoomDisconnected remains the compatibility code for an explicit
// Room/Manager close while work is pending. WebSocket loss alone no longer
// closes a Room or terminalizes its interaction.
const errorCodeRoomDisconnected = "ROOM_DISCONNECTED"

// workflowEntry is the per-workflow record returned from
// tangent.list_workflows. Mirrors envelopes.TypeSpec but flattens
// schema/UI metadata that MCP clients don't need.
type workflowEntry struct {
	Type         string   `json:"type"`
	Version      string   `json:"version,omitempty"`
	Description  string   `json:"description,omitempty"`
	ResponseKind string   `json:"response_kind"`
	Capabilities []string `json:"capabilities"`
}

// listWorkflowsResult is the structured payload returned by
// tangent.list_workflows.
type listWorkflowsResult struct {
	Workflows []workflowEntry `json:"workflows"`
}

// triageInput is the SDK-typed input shape for tangent.triage. Mirrors
// the JSON Schema declared in triage_schema.go. The envelope field is
// decoded into the canonical envelopes.Envelope on the way to validation.
type triageInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

// handleListWorkflows enumerates the envelope types for which a handler
// is currently registered on the dispatcher. This is the
// "what-can-Tangent-do" tool that MCP clients call before constructing
// envelopes.
//
// In PR 3 the dispatcher has no registered handlers, so the workflows
// array is empty — that's the contract: tools/list returns the tool, the
// tool returns "{workflows: []}", and clients learn that PR 3 binaries
// are envelope-aware but not yet workflow-bearing.
//
// PR 4+ register handlers (`triage` first); their entries appear here
// without code changes.
func (s *Server) handleListWorkflows(
	_ context.Context,
	_ struct{},
) (any, error) {
	all := s.envSvc.All()
	workflows := make([]workflowEntry, 0, len(all))
	for _, spec := range all {
		// Filter to types that have a registered handler. Until PR 4
		// registers triage, this list is empty by design; clients can
		// still see the tool exists, which is the v0.1 acceptance.
		if !s.dispatcher.Has(spec.Name) {
			continue
		}
		workflows = append(workflows, workflowEntry{
			Type:         spec.Name,
			Version:      spec.Version,
			Description:  spec.Description,
			ResponseKind: string(spec.ResponseKind),
			// Capabilities are a v0.4+ concept (per the brief). v0.1
			// returns an empty slice so the JSON shape is stable for
			// clients that already key off the field.
			Capabilities: []string{},
		})
	}

	return listWorkflowsResult{Workflows: workflows}, nil
}

// handleTriage validates the inbound triage envelope through Tangent's
// envelope service and dispatches it. In PR 3 the actual error path is
// envelope.ErrUnknownType: triage is not in go-envelopes v0.1.0 core, so
// Service.Validate (called by Dispatcher.Dispatch before any handler
// lookup) rejects the envelope. We collapse both ErrUnknownType and the
// later ErrNoHandler case into a single NOT_WIRED tool result so MCP
// clients see a stable contract — "wire shape accepted, workflow not
// online yet" — regardless of which layer reports it. PR 4 registers
// `triage` via the plugin extension API and the same path becomes the
// real dispatch.
func (s *Server) handleTriage(
	ctx context.Context,
	args triageInput,
) (any, error) {
	// Defensive: SDK input validation passes the schema, but the typed
	// struct has no way to enforce e.g. "type must equal triage" beyond
	// what the schema enforces. Belt-and-suspenders here keeps the
	// invariant explicit on the server side.
	if args.Envelope.Type != triageEnvelopeType {
		return nil, toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf("tangent.triage rejects envelope type %q; want %q", args.Envelope.Type, triageEnvelopeType),
		)
	}

	roomID, err := s.resolveWorkflowRoom(ctx, "triage", &args.Envelope)
	if err != nil {
		return nil, err
	}

	return s.advanceRoomEnvelope(ctx, roomID, &args.Envelope, nil, args.Completion)
}

// triageErrorResult maps a dispatcher error into a structured tool
// result. Every dispatcher error becomes a tool-level isError=true
// response carrying an error envelope payload — clients see one
// surface regardless of whether the failure was schema, routing, or
// transport.
//
// Mapping:
//   - ErrNoHandler                -> NOT_WIRED (legacy; only reachable in
//     test setups that omit handler registration. Production wires the
//     handler in main; the path here keeps the v0.1 PR 3 contract test
//     passing without forcing test-only branches in the handler.)
//   - ErrUnknownType (envelope)   -> NOT_WIRED for `triage`. Once the
//     plugin extension API registers triage at boot this is unreachable
//     too, but it remains a defensive belt for misconfigured boots.
//   - room.ErrRoomDisconnected /
//     room.ErrRoomClosed          -> ROOM_DISCONNECTED
//   - context.DeadlineExceeded    -> envelopes.ErrorCodeTimeout
//   - context.Canceled            -> user-canceled
//   - ErrSchemaValidation         -> validation-failed
//   - everything else             -> host-error (with the verbatim message)
func triageErrorResult(err error) error {
	switch {
	case errors.Is(err, envelope.ErrNoHandler):
		return toolErrorResult(errorCodeNotWired, fmt.Sprintf("no handler registered for triage envelope: %v", err))
	case errors.Is(err, envelopes.ErrUnknownType):
		return toolErrorResult(errorCodeNotWired, fmt.Sprintf("triage envelope type not registered (boot did not load the triage extension): %v", err))
	case errors.Is(err, room.ErrRoomDisconnected) || errors.Is(err, room.ErrRoomClosed):
		return toolErrorResult(errorCodeRoomDisconnected, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return toolErrorResult(envelopes.ErrorCodeTimeout, err.Error())
	case errors.Is(err, context.Canceled):
		return toolErrorResult(envelopes.ErrorCodeUserCanceled, err.Error())
	case errors.Is(err, envelopes.ErrSchemaValidation):
		return toolErrorResult(envelopes.ErrorCodeValidationFailed, err.Error())
	default:
		return toolErrorResult(envelopes.ErrorCodeHostError, err.Error())
	}
}
