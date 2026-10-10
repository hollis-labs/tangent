package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	gmcpserver "github.com/hollis-labs/libs/plugin-mcp/go-mcp/server"
)

// This file is the one place a tool name is claimed on this server, and the
// one place a tool's required MCP annotation hints are assigned.
//
// # Why it exists
//
// The MCP SDK's tool registry is a map keyed by name. A second AddTool on a
// name that is already registered replaces the first handler and says nothing —
// no error, no log line, no panic. That is the worst shape a collision can
// take: an agent calling `tangent.session_create` and reaching something else
// entirely would look exactly like it working.
//
// Nothing about the host surface needed this while every tool was written in
// this package by hand. CW-20260910-0029 changed that: a plugin contributes a
// tool name from outside, and "refuse the collision, name it, never silently
// shadow" is the posture internal/pluginhost already takes toward every other
// thing a plugin can claim.
//
// So every registration goes through addTool, and the claimed set is what the
// server actually serves rather than a list maintained beside it. A host tool
// colliding with a host tool is a build defect and is reported the same way,
// because the check that only watches the newcomers is the check that misses
// the rename.
//
// # Why claiming does not return an error
//
// addTool has twenty-odd call sites and none of them could do anything useful
// with an error except return it, so a collision is recorded and reported once,
// by registerTools, before New hands back a server. The failure still fails the
// boot; it just does not cost every call site a branch it would never take.
//
// # Why annotations live in one table
//
// go-mcp (CW-20260917-0018) makes the four MCP tool-annotation hints —
// readOnlyHint, destructiveHint, idempotentHint, openWorldHint — a required
// part of every registration, rather than optional or inferred from a tool's
// name. Reviewing that property scattered across fifty call sites is how one
// gets missed; toolAnnotationTable is the single, reviewable statement of it.
// A tool with no entry panics at registration rather than silently defaulting
// to "safe" — the same posture go-mcp itself takes toward an unset hint.

// toolHints is this package's own copy of the four required MCP annotation
// fields, independent of go-mcp's own gmcpserver.Tool so the annotation table
// below can be built without repeating every other Tool field.
type toolHints struct {
	ReadOnly    bool
	Destructive bool
	Idempotent  bool
	OpenWorld   bool
}

// toolAnnotationTable declares the required annotation hints for every tool
// this host serves, by wire name.
//
// The policy behind the values, so a new entry can be judged by the same
// rule rather than guessed:
//   - ReadOnly is true only for a tool that makes no durable write. This is
//     the safety-relevant flag — an agent framework may auto-approve a
//     read-only tool — so it is exact rather than approximate.
//   - Destructive is true only for a tool that terminalizes, closes,
//     withdraws, cancels, or supersedes existing state. A tool that only
//     creates or advances state is not destructive, even though it mutates.
//   - Idempotent is true for a tool that is explicitly safe to retry with the
//     same arguments — carries its own idempotency key, or is a room
//     workflow tool, which advanceRoomEnvelope keys on envelope id and
//     refuses a differing retry as IDEMPOTENCY_CONFLICT rather than
//     executing it twice.
//   - OpenWorld is false for every tool this host serves. Nothing here
//     searches an open-ended external domain; every tool operates on
//     Tangent's own closed state.
var toolAnnotationTable = map[string]toolHints{
	// Named room workflows: create-or-advance a room and wait for a human.
	// Idempotent by construction — see advanceRoomEnvelope's envelope-id
	// identity and IDEMPOTENCY_CONFLICT handling.
	"tangent.triage":             {Idempotent: true},
	"tangent.feedback":           {Idempotent: true},
	"tangent.form-collect":       {Idempotent: true},
	"tangent.design-iteration":   {Idempotent: true},
	"tangent.interview_question": {Idempotent: true},
	"tangent.block_draft":        {Idempotent: true},
	"tangent.prose_revision":     {Idempotent: true},
	"tangent.output_render":      {Idempotent: true},
	"tangent.whiteboard":         {Idempotent: true},
	"tangent.app-board":          {Idempotent: true},
	"tangent.dashboard":          {Idempotent: true},
	"tangent.file-picker":        {Idempotent: true},
	"tangent.progress-panel":     {Idempotent: true},
	"tangent.wizard":             {Idempotent: true},
	"tangent.diff-review":        {Idempotent: true},
	"tangent.spreadsheet-review": {Idempotent: true},
	"tangent.approval-queue":     {Idempotent: true},
	"tangent.synthesis_notes":    {Idempotent: true},

	// Session/room lifecycle.
	"tangent.session_create":           {},
	"tangent.session_advance":          {Idempotent: true},
	"tangent.session_get":              {ReadOnly: true},
	"tangent.session_advance_phase":    {},
	"tangent.session_set_phase_output": {},
	"tangent.session_close":            {Destructive: true, Idempotent: true},
	"tangent.session_list":             {ReadOnly: true},

	// Catalog and registry diagnostics.
	"tangent.list_workflows":                  {ReadOnly: true},
	"tangent.definition_registry_list":        {ReadOnly: true},
	"tangent.definition_get":                  {ReadOnly: true},
	"tangent.definition_registry_diagnostics": {ReadOnly: true},

	// Operability.
	"tangent.health_report":    {ReadOnly: true},
	"tangent.telemetry_query":  {ReadOnly: true},
	"tangent.retention_status": {ReadOnly: true},

	// Pure caller inbox reads: no retrieval, presentation, delivery or ACK writes.
	"tangent.inbox_list":   {ReadOnly: true, Idempotent: true},
	"tangent.inbox_search": {ReadOnly: true, Idempotent: true},
	"tangent.inbox_get":    {ReadOnly: true, Idempotent: true},

	// Generic durable interaction surface.
	"tangent.interaction_list_kinds":         {ReadOnly: true},
	"tangent.interaction_resolve_definition": {ReadOnly: true},
	"tangent.surface_open":                   {Idempotent: true},
	"tangent.surface_get":                    {ReadOnly: true},
	"tangent.surface_close":                  {Destructive: true},
	"tangent.interaction_submit":             {Idempotent: true},
	"tangent.interaction_get":                {ReadOnly: true},
	"tangent.interaction_await":              {ReadOnly: true},
	"tangent.interaction_cancel":             {Destructive: true},
	"tangent.interaction_supersede":          {Destructive: true},
	"tangent.interaction_acknowledge":        {Idempotent: true},

	// Durable HITL inbox.
	"tangent.hitl_enqueue":  {Idempotent: true},
	"tangent.hitl_get":      {ReadOnly: true},
	"tangent.hitl_await":    {ReadOnly: true},
	"tangent.hitl_withdraw": {Destructive: true},

	// Docs inbox.
	"tangent.docs_enqueue": {Idempotent: true},

	// Agent turns inbox. turns_enqueue is idempotent by its idempotency_key,
	// turn_await only reads, and turn_ack converges: acknowledging twice leaves
	// the same state as acknowledging once.
	"tangent.turns_enqueue": {Idempotent: true},
	"tangent.turn_await":    {ReadOnly: true},
	"tangent.turn_ack":      {Idempotent: true},

	// Cooperative relay inbox.
	"tangent.relay_open_channel": {},
	"tangent.relay_attach":       {},
	"tangent.relay_detach":       {Destructive: true},
	"tangent.relay_send":         {Idempotent: true},
	"tangent.relay_receive":      {ReadOnly: true},
	"tangent.relay_ack":          {Idempotent: true},
	"tangent.relay_capabilities": {ReadOnly: true},
}

// annotationsFor returns the declared hints for a tool, panicking if none are
// declared. A missing entry is a build defect caught at boot — the same
// posture toolConflictError already takes toward a name claimed twice.
func annotationsFor(name string) toolHints {
	hints, ok := toolAnnotationTable[name]
	if !ok {
		panic(fmt.Sprintf("mcp: tool %s has no entry in toolAnnotationTable — every tool needs a "+
			"reviewed readOnly/destructive/idempotent/openWorld judgment before it can be registered", name))
	}
	return hints
}

// addTool registers one typed tool and claims its name.
//
// In is the tool's input shape — a hand-written struct, or map[string]any for
// a tool that validates its own shape dynamically. handler receives the
// decoded value directly; go-mcp's own JSON-Schema validation (installed as
// inputSchemaValidationMiddleware) has already run by the time a handler sees
// its arguments, so a decode failure here is the rare defensive case rather
// than the primary validation path.
func addTool[In any](
	server *Server,
	tool gmcpserver.Tool,
	handler func(context.Context, In) (any, error),
) {
	server.claimToolName(tool.Name)
	hints := annotationsFor(tool.Name)
	tool.ReadOnlyHint = hints.ReadOnly
	tool.DestructiveHint = hints.Destructive
	tool.IdempotentHint = hints.Idempotent
	tool.OpenWorldHint = hints.OpenWorld
	tool.Handler = func(ctx context.Context, raw map[string]any) (any, error) {
		args, err := decodeToolArgs[In](raw)
		if err != nil {
			return nil, err
		}
		return handler(ctx, args)
	}
	server.mcp.RegisterTool(tool)
}

// addInteractionTool registers a tool whose input schema is derived by
// reflection from Input, rather than hand-rolled — the shape every generic
// durable-interaction tool and a handful of others share.
func addInteractionTool[Input any](
	server *Server,
	name string,
	description string,
	handler func(context.Context, Input) (any, error),
) error {
	schema, err := jsonschema.For[Input](nil)
	if err != nil {
		return fmt.Errorf("build %s input schema: %w", name, err)
	}
	addTool(server, gmcpserver.Tool{Name: name, Description: description, InputSchema: schema}, handler)
	return nil
}

// decodeToolArgs decodes a tool call's raw JSON-object arguments into In via
// a JSON round-trip, so a handler declared against a typed struct (or
// map[string]any) receives exactly what the old SDK-typed AddTool generic
// used to hand it automatically.
func decodeToolArgs[In any](raw map[string]any) (In, error) {
	var args In
	if len(raw) == 0 {
		return args, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return args, fmt.Errorf("encode tool arguments: %w", err)
	}
	if err := json.Unmarshal(encoded, &args); err != nil {
		return args, fmt.Errorf("decode tool arguments: %w", err)
	}
	return args, nil
}

// claimToolName records a tool name. A repeat claim is recorded as a conflict
// rather than refused inline; see the file comment.
func (s *Server) claimToolName(name string) {
	if s.claimedTools == nil {
		s.claimedTools = map[string]bool{}
	}
	if s.claimedTools[name] {
		s.toolConflicts = append(s.toolConflicts, name)
		return
	}
	s.claimedTools[name] = true
}

// isToolNameClaimed reports whether a name is already on the surface. It is
// what lets plugin registration refuse a collision *before* making it, with a
// message about the plugin rather than about the surface.
func (s *Server) isToolNameClaimed(name string) bool { return s.claimedTools[name] }

// ToolNames returns every tool name this server serves, sorted.
//
// It is how a check can compare the surface against something else — the
// plugin host's contributed set, a document, another build — without anyone
// writing the surface down. internal/smoke uses it for exactly that.
func (s *Server) ToolNames() []string {
	s.pluginMu.RLock()
	defer s.pluginMu.RUnlock()
	names := make([]string, 0, len(s.claimedTools))
	for name := range s.claimedTools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// toolConflictError reports every name claimed twice during registration, or
// nil when the surface is clean.
func (s *Server) toolConflictError() error {
	if len(s.toolConflicts) == 0 {
		return nil
	}
	return fmt.Errorf(
		"tool name registered more than once: %s — the MCP SDK's registry would have kept only the "+
			"last handler for each, silently",
		strings.Join(s.toolConflicts, ", "))
}
