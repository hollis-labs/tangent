package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/envelope"
)

// errorCodeNotWired is the v0.1 sentinel for "MCP layer is up, dispatcher
// has no handler for this envelope type yet." Surfaced by tangent.triage
// in PR 3; PR 4 replaces the path with a real WebSocket-bridge dispatch.
const errorCodeNotWired = "NOT_WIRED"

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
	Envelope envelopes.Envelope `json:"envelope"`
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
	_ *mcpsdk.CallToolRequest,
	_ struct{},
) (*mcpsdk.CallToolResult, listWorkflowsResult, error) {
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

	res := listWorkflowsResult{Workflows: workflows}
	textPayload, err := json.Marshal(res)
	if err != nil {
		return nil, res, fmt.Errorf("marshal list_workflows result: %w", err)
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(textPayload)}},
	}, res, nil
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
	_ *mcpsdk.CallToolRequest,
	args triageInput,
) (*mcpsdk.CallToolResult, any, error) {
	// Defensive: SDK input validation passes the schema, but the typed
	// struct has no way to enforce e.g. "type must equal triage" beyond
	// what the schema enforces. Belt-and-suspenders here keeps the
	// invariant explicit on the server side.
	if args.Envelope.Type != triageEnvelopeType {
		return toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf("tangent.triage rejects envelope type %q; want %q", args.Envelope.Type, triageEnvelopeType),
		), nil, nil
	}

	resp, err := s.dispatcher.Dispatch(ctx, &args.Envelope)
	if err != nil {
		return triageErrorResult(err), nil, nil
	}

	// Happy path (PR 4+): surface the Response as JSON text content. In
	// v0.1 this branch is dead because the dispatcher always returns
	// ErrNoHandler for `triage`; PR 4 brings it to life.
	body, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("marshal triage response: %v", marshalErr)), nil, nil
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(body)}},
	}, resp, nil
}

// triageErrorResult maps a dispatcher error into a structured tool
// result. The mapping is intentionally conservative — every dispatcher
// error becomes a tool-level isError=true response carrying an error
// envelope payload — so MCP clients can treat the surface uniformly.
//
// Mapping:
//   - ErrNoHandler                -> NOT_WIRED (v0.1 sentinel; PR 4 replaces)
//   - ErrUnknownType (envelope)   -> NOT_WIRED for `triage` specifically,
//     since `triage` is not in go-envelopes v0.1.0 core (the kind is
//     scheduled for v0.3 upstreaming;
//     followups.tangent.v01.triage_kind_in_go_envelopes). Treating
//     "registry doesn't know triage yet" as NOT_WIRED keeps the v0.1
//     contract uniform — clients see the same sentinel for both
//     "kind missing from registry" and "kind present, no handler"
//     until PR 4 makes both paths trivially reachable.
//   - ErrSchemaValidation         -> validation-failed
//   - everything else             -> host-error (with the verbatim message)
func triageErrorResult(err error) *mcpsdk.CallToolResult {
	switch {
	case errors.Is(err, envelope.ErrNoHandler):
		return toolErrorResult(errorCodeNotWired, fmt.Sprintf("no handler registered for triage envelope (PR 4 will wire this): %v", err))
	case errors.Is(err, envelopes.ErrUnknownType):
		// v0.1: collapse to NOT_WIRED; see godoc above.
		return toolErrorResult(errorCodeNotWired, fmt.Sprintf("triage envelope type not yet registered (PR 4 will wire this; v0.3 upstreams triage to go-envelopes core): %v", err))
	case errors.Is(err, envelopes.ErrSchemaValidation):
		return toolErrorResult(envelopes.ErrorCodeValidationFailed, err.Error())
	default:
		return toolErrorResult(envelopes.ErrorCodeHostError, err.Error())
	}
}

// toolErrorResult builds a CallToolResult flagged as an error, embedding
// a JSON envelope-style error body in a single text content block.
//
// We deliberately echo the canonical envelope error-code vocabulary
// (validation-failed, unsupported-type, host-error, NOT_WIRED) so
// clients that already speak the envelope protocol can branch on the
// `code` field without learning a second taxonomy.
//
// We do NOT use CallToolResult.SetError here: SetError is destructive
// (it overwrites Content with the error.Error() string), which would
// drop our structured JSON body. Instead we set IsError=true directly
// and keep the JSON payload as the lone TextContent block.
func toolErrorResult(code, message string) *mcpsdk.CallToolResult {
	body := map[string]any{
		"v":    envelopes.ProtocolVersion,
		"kind": string(envelopes.ResponseKindError),
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		// json.Marshal of a fixed-shape map cannot realistically fail;
		// fall back to a plain text marker so callers still see SOMETHING
		// rather than a truncated frame.
		raw = []byte(fmt.Sprintf(`{"kind":"error","error":{"code":%q,"message":%q}}`, code, message))
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(raw)}},
		IsError: true,
	}
}
