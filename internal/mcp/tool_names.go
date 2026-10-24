package mcp

import (
	"context"
	"regexp"
	"sort"

	gmcpserver "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// This file owns the MCP tool naming scheme and the deprecated aliases that
// let callers of the old names keep working for a deprecation window.
//
// # The scheme
//
// Every tool this host serves is named tangent_<noun>_<verb>, matching
// ToolNamePattern. Dots and hyphens are not allowed: OpenAI-style
// function-calling clients accept only [A-Za-z0-9_-], and the tether MCP
// gateway never normalizes an upstream name, so a dotted name is uncallable
// from those clients. The noun is the thing acted on (session, interaction,
// surface, hitl, docs, turns, relay, inbox, definition, ...); the verb comes
// from a small fixed vocabulary: get, list, search, query, enqueue, submit,
// await, ack, cancel, withdraw, supersede, resolve, open, close, send,
// receive, attach, detach, advance, set. The interactive workflow surfaces
// (tangent_app_board, tangent_approval_queue, ...) are nouns on their own:
// calling one is the act of opening that surface.
//
// # Why aliases are hidden
//
// Each tool also answers to its old tangent.<name> spelling. An old name
// violates ToolNamePattern, so listing it would put a name strict clients
// reject into tools/list. The alias is therefore registered and callable, but
// withheld from tools/list unless the operator opts in with
// TANGENT_MCP_LEGACY_TOOL_NAMES=1 (WithLegacyToolNamesListed). An alias routes
// to the exact same handler and carries the same annotations; its description
// starts with a DEPRECATED notice. See docs/mcp-tool-names.md.

// ToolNamePattern is the charset every non-alias tool name must match.
var ToolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// legacyToolNames maps each current tool name to the deprecated name it
// replaced. Every tool this host registers itself has exactly one entry; the
// tests enforce both directions.
var legacyToolNames = map[string]string{
	"tangent_app_board":                      "tangent.app-board",
	"tangent_approval_queue":                 "tangent.approval-queue",
	"tangent_block_draft":                    "tangent.block_draft",
	"tangent_dashboard":                      "tangent.dashboard",
	"tangent_definition_diagnostics_get":     "tangent.definition_registry_diagnostics",
	"tangent_definition_get":                 "tangent.definition_get",
	"tangent_definition_list":                "tangent.definition_registry_list",
	"tangent_design_iteration":               "tangent.design-iteration",
	"tangent_diff_review":                    "tangent.diff-review",
	"tangent_docs_enqueue":                   "tangent.docs_enqueue",
	"tangent_feedback":                       "tangent.feedback",
	"tangent_file_picker":                    "tangent.file-picker",
	"tangent_form_collect":                   "tangent.form-collect",
	"tangent_health_get":                     "tangent.health_report",
	"tangent_hitl_await":                     "tangent.hitl_await",
	"tangent_hitl_enqueue":                   "tangent.hitl_enqueue",
	"tangent_hitl_get":                       "tangent.hitl_get",
	"tangent_hitl_withdraw":                  "tangent.hitl_withdraw",
	"tangent_inbox_get":                      "tangent.inbox_get",
	"tangent_inbox_list":                     "tangent.inbox_list",
	"tangent_inbox_search":                   "tangent.inbox_search",
	"tangent_interaction_ack":                "tangent.interaction_acknowledge",
	"tangent_interaction_await":              "tangent.interaction_await",
	"tangent_interaction_cancel":             "tangent.interaction_cancel",
	"tangent_interaction_definition_resolve": "tangent.interaction_resolve_definition",
	"tangent_interaction_get":                "tangent.interaction_get",
	"tangent_interaction_kind_list":          "tangent.interaction_list_kinds",
	"tangent_interaction_submit":             "tangent.interaction_submit",
	"tangent_interaction_supersede":          "tangent.interaction_supersede",
	"tangent_interview_question":             "tangent.interview_question",
	"tangent_output_render":                  "tangent.output_render",
	"tangent_progress_panel":                 "tangent.progress-panel",
	"tangent_prose_revision":                 "tangent.prose_revision",
	"tangent_relay_ack":                      "tangent.relay_ack",
	"tangent_relay_attach":                   "tangent.relay_attach",
	"tangent_relay_capabilities_get":         "tangent.relay_capabilities",
	"tangent_relay_channel_open":             "tangent.relay_open_channel",
	"tangent_relay_detach":                   "tangent.relay_detach",
	"tangent_relay_receive":                  "tangent.relay_receive",
	"tangent_relay_send":                     "tangent.relay_send",
	"tangent_retention_get":                  "tangent.retention_status",
	"tangent_session_advance":                "tangent.session_advance",
	"tangent_session_close":                  "tangent.session_close",
	"tangent_session_create":                 "tangent.session_create",
	"tangent_session_get":                    "tangent.session_get",
	"tangent_session_list":                   "tangent.session_list",
	"tangent_session_phase_advance":          "tangent.session_advance_phase",
	"tangent_session_phase_output_set":       "tangent.session_set_phase_output",
	"tangent_spreadsheet_review":             "tangent.spreadsheet-review",
	"tangent_surface_close":                  "tangent.surface_close",
	"tangent_surface_get":                    "tangent.surface_get",
	"tangent_surface_open":                   "tangent.surface_open",
	"tangent_synthesis_notes":                "tangent.synthesis_notes",
	"tangent_telemetry_query":                "tangent.telemetry_query",
	"tangent_triage":                         "tangent.triage",
	"tangent_turns_ack":                      "tangent.turn_ack",
	"tangent_turns_await":                    "tangent.turn_await",
	"tangent_turns_enqueue":                  "tangent.turns_enqueue",
	"tangent_ui_drawer_close":                "tangent.ui_close_drawer",
	"tangent_ui_drawer_open":                 "tangent.ui_open_drawer",
	"tangent_ui_item_focus":                  "tangent.ui_focus_item",
	"tangent_ui_modal_close":                 "tangent.ui_close_modal",
	"tangent_ui_modal_open":                  "tangent.ui_open_modal",
	"tangent_ui_navigate":                    "tangent.ui_navigate",
	"tangent_view_get":                       "tangent.view_get",
	"tangent_wizard":                         "tangent.wizard",
	"tangent_workflow_list":                  "tangent.list_workflows",
}

// LegacyToolName returns the deprecated alias of a current tool name.
func LegacyToolName(current string) (string, bool) {
	legacy, ok := legacyToolNames[current]
	return legacy, ok
}

// LegacyToolNames returns every deprecated alias this host answers to,
// sorted. It never overlaps ToolNames.
func (s *Server) LegacyToolNames() []string {
	s.pluginMu.RLock()
	defer s.pluginMu.RUnlock()
	names := make([]string, 0, len(s.legacyAliases))
	for name := range s.legacyAliases {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// WithLegacyToolNamesListed makes tools/list include the deprecated aliases.
// Off by default: see the file comment.
func WithLegacyToolNamesListed() Option {
	return func(s *Server) error {
		s.listLegacyTools = true
		return nil
	}
}

// registerLegacyAlias registers the deprecated spelling of an already
// registered tool, routed to the same handler. A tool with no entry in
// legacyToolNames gets none.
func (s *Server) registerLegacyAlias(tool gmcpserver.Tool) {
	legacy, ok := legacyToolNames[tool.Name]
	if !ok {
		return
	}
	if s.legacyAliases == nil {
		s.legacyAliases = map[string]string{}
	}
	s.legacyAliases[legacy] = tool.Name
	tool.Description = "DEPRECATED alias of " + tool.Name +
		"; this name does not match [a-z0-9_] and will be removed. " + tool.Description
	tool.Name = legacy
	s.mcp.RegisterTool(tool)
}

// hideLegacyAliasesMiddleware drops deprecated aliases from tools/list results
// unless the server was built WithLegacyToolNamesListed. tools/call is not
// touched, so an alias keeps working either way.
func (s *Server) hideLegacyAliasesMiddleware(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
	return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
		res, err := next(ctx, method, req)
		if err != nil || method != "tools/list" || s.listLegacyTools {
			return res, err
		}
		list, ok := res.(*mcpsdk.ListToolsResult)
		if !ok {
			return res, err
		}
		kept := make([]*mcpsdk.Tool, 0, len(list.Tools))
		for _, tool := range list.Tools {
			if tool == nil {
				continue
			}
			if _, alias := s.legacyAliases[tool.Name]; !alias {
				kept = append(kept, tool)
			}
		}
		list.Tools = kept
		return list, nil
	}
}
