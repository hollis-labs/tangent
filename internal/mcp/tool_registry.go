package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	gmcpserver "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
)

// This file is the one place a tool name is claimed on this server, and the
// one place a tool's required MCP annotation hints are assigned.
//
// # Why it exists
//
// The MCP SDK's tool registry is a map keyed by name. A second AddTool on a
// name that is already registered replaces the first handler and says nothing —
// no error, no log line, no panic. That is the worst shape a collision can
// take: an agent calling `tangent_session_create` and reaching something else
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
	"tangent_triage":             {Idempotent: true},
	"tangent_feedback":           {Idempotent: true},
	"tangent_form_collect":       {Idempotent: true},
	"tangent_design_iteration":   {Idempotent: true},
	"tangent_interview_question": {Idempotent: true},
	"tangent_block_draft":        {Idempotent: true},
	"tangent_prose_revision":     {Idempotent: true},
	"tangent_output_render":      {Idempotent: true},
	"tangent_whiteboard":         {Idempotent: true},
	"tangent_app_board":          {Idempotent: true},
	"tangent_dashboard":          {Idempotent: true},
	"tangent_file_picker":        {Idempotent: true},
	"tangent_progress_panel":     {Idempotent: true},
	"tangent_wizard":             {Idempotent: true},
	"tangent_diff_review":        {Idempotent: true},
	"tangent_spreadsheet_review": {Idempotent: true},
	"tangent_approval_queue":     {Idempotent: true},
	"tangent_synthesis_notes":    {Idempotent: true},

	// Session/room lifecycle.
	"tangent_session_create":           {},
	"tangent_session_advance":          {Idempotent: true},
	"tangent_session_get":              {ReadOnly: true},
	"tangent_session_phase_advance":    {},
	"tangent_session_phase_output_set": {},
	"tangent_session_close":            {Destructive: true, Idempotent: true},
	"tangent_session_list":             {ReadOnly: true},

	"tangent_view_get":        {ReadOnly: true},
	"tangent_ui_navigate":     {},
	"tangent_ui_modal_open":   {},
	"tangent_ui_modal_close":  {},
	"tangent_ui_drawer_open":  {},
	"tangent_ui_drawer_close": {},
	"tangent_ui_item_focus":   {},

	// Catalog and registry diagnostics.
	"tangent_workflow_list":              {ReadOnly: true},
	"tangent_definition_list":            {ReadOnly: true},
	"tangent_definition_get":             {ReadOnly: true},
	"tangent_definition_diagnostics_get": {ReadOnly: true},

	// Operability.
	"tangent_health_get":      {ReadOnly: true},
	"tangent_telemetry_query": {ReadOnly: true},
	"tangent_retention_get":   {ReadOnly: true},

	// Pure caller inbox reads: no retrieval, presentation, delivery or ACK writes.
	"tangent_inbox_list":   {ReadOnly: true, Idempotent: true},
	"tangent_inbox_search": {ReadOnly: true, Idempotent: true},
	"tangent_inbox_get":    {ReadOnly: true, Idempotent: true},

	// Generic durable interaction surface.
	"tangent_interaction_kind_list":          {ReadOnly: true},
	"tangent_interaction_definition_resolve": {ReadOnly: true},
	"tangent_surface_open":                   {Idempotent: true},
	"tangent_surface_get":                    {ReadOnly: true},
	"tangent_surface_close":                  {Destructive: true},
	"tangent_interaction_submit":             {Idempotent: true},
	"tangent_interaction_get":                {ReadOnly: true},
	"tangent_interaction_await":              {ReadOnly: true},
	"tangent_interaction_cancel":             {Destructive: true},
	"tangent_interaction_supersede":          {Destructive: true},
	"tangent_interaction_ack":                {Idempotent: true},

	// Durable HITL inbox.
	"tangent_hitl_enqueue":  {Idempotent: true},
	"tangent_hitl_get":      {ReadOnly: true},
	"tangent_hitl_await":    {ReadOnly: true},
	"tangent_hitl_withdraw": {Destructive: true},

	// Docs inbox.
	"tangent_docs_enqueue": {Idempotent: true},

	// Artifact inbox (ADR 0015). create is idempotent by its idempotency_key;
	// update adds a revision each call; message appends; get and await only read.
	"tangent_artifact_create":   {Idempotent: true},
	"tangent_artifact_update":   {},
	"tangent_artifact_get":      {ReadOnly: true},
	"tangent_artifact_message":  {},
	"tangent_artifact_await":    {ReadOnly: true},
	"tangent_artifact_withdraw": {Destructive: true},

	// Agent turns inbox. turns_enqueue is idempotent by its idempotency_key,
	// turn_await only reads, and turn_ack converges: acknowledging twice leaves
	// the same state as acknowledging once.
	"tangent_turns_enqueue": {Idempotent: true},
	"tangent_turns_await":   {ReadOnly: true},
	"tangent_turns_ack":     {Idempotent: true},

	// Cooperative relay inbox.
	"tangent_relay_channel_open":     {},
	"tangent_relay_attach":           {},
	"tangent_relay_detach":           {Destructive: true},
	"tangent_relay_send":             {Idempotent: true},
	"tangent_relay_receive":          {ReadOnly: true},
	"tangent_relay_ack":              {Idempotent: true},
	"tangent_relay_capabilities_get": {ReadOnly: true},
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
	server.registerLegacyAlias(tool)
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
func (s *Server) isToolNameClaimed(name string) bool {
	_, alias := s.legacyAliases[name]
	return s.claimedTools[name] || alias
}

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
