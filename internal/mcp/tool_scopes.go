package mcp

import (
	"context"
	"errors"
	"sync"

	gmcpserver "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/access"
)

// A tool is reachable only through a scope.
//
// Every tool carries the scope a caller must hold to call it, written in this
// file (hosts' tools) or at registration (plugins' tools), and the table is
// consulted on every tools/call before anything else about the call. A tool
// with no entry is not open: it is refused to every caller, which is also what
// makes a tool a plugin withdrew unreachable the moment it is withdrawn.
//
// No tool holds access.Resolve. A decision is a person's, made at a user
// session through the browser API; the tool surface has none, and
// TestNoToolNeedsTheDecisionScope keeps it that way.
var hostToolScopes = map[string]string{
	// Reads of what is there.
	"tangent_artifact_await":                 access.View,
	"tangent_artifact_get":                   access.View,
	"tangent_definition_diagnostics_get":     access.View,
	"tangent_definition_get":                 access.View,
	"tangent_definition_list":                access.View,
	"tangent_health_get":                     access.View,
	"tangent_hitl_await":                     access.View,
	"tangent_hitl_get":                       access.View,
	"tangent_inbox_get":                      access.View,
	"tangent_inbox_list":                     access.View,
	"tangent_inbox_search":                   access.View,
	"tangent_interaction_await":              access.View,
	"tangent_interaction_definition_resolve": access.View,
	"tangent_interaction_get":                access.View,
	"tangent_interaction_kind_list":          access.View,
	"tangent_relay_capabilities_get":         access.View,
	"tangent_relay_receive":                  access.View,
	"tangent_retention_get":                  access.View,
	"tangent_session_get":                    access.View,
	"tangent_session_list":                   access.View,
	"tangent_surface_get":                    access.View,
	"tangent_telemetry_query":                access.View,
	"tangent_turns_await":                    access.View,
	"tangent_view_get":                       access.View,
	"tangent_workflow_list":                  access.View,

	// Putting something in front of the operator: the work agents submit.
	"tangent_app_board":                access.Submit,
	"tangent_approval_queue":           access.Submit,
	"tangent_artifact_create":          access.Submit,
	"tangent_artifact_message":         access.Submit,
	"tangent_artifact_update":          access.Submit,
	"tangent_block_draft":              access.Submit,
	"tangent_dashboard":                access.Submit,
	"tangent_design_iteration":         access.Submit,
	"tangent_diff_review":              access.Submit,
	"tangent_docs_enqueue":             access.Submit,
	"tangent_feedback":                 access.Submit,
	"tangent_file_picker":              access.Submit,
	"tangent_form_collect":             access.Submit,
	"tangent_hitl_enqueue":             access.Submit,
	"tangent_interaction_submit":       access.Submit,
	"tangent_interaction_supersede":    access.Submit,
	"tangent_interview_question":       access.Submit,
	"tangent_output_render":            access.Submit,
	"tangent_progress_panel":           access.Submit,
	"tangent_prose_revision":           access.Submit,
	"tangent_relay_attach":             access.Submit,
	"tangent_relay_channel_open":       access.Submit,
	"tangent_relay_detach":             access.Submit,
	"tangent_relay_send":               access.Submit,
	"tangent_session_advance":          access.Submit,
	"tangent_session_create":           access.Submit,
	"tangent_session_phase_advance":    access.Submit,
	"tangent_session_phase_output_set": access.Submit,
	"tangent_spreadsheet_review":       access.Submit,
	"tangent_surface_open":             access.Submit,
	"tangent_synthesis_notes":          access.Submit,
	"tangent_triage":                   access.Submit,
	"tangent_turns_enqueue":            access.Submit,
	"tangent_ui_drawer_close":          access.Submit,
	"tangent_ui_drawer_open":           access.Submit,
	"tangent_ui_item_focus":            access.Submit,
	"tangent_ui_modal_close":           access.Submit,
	"tangent_ui_modal_open":            access.Submit,
	"tangent_ui_navigate":              access.Submit,
	"tangent_wizard":                   access.Submit,

	// Acknowledging that something was delivered. Not a decision.
	"tangent_interaction_ack": access.Deliver,
	"tangent_relay_ack":       access.Deliver,
	"tangent_turns_ack":       access.Deliver,

	// Taking back one's own request.
	"tangent_artifact_withdraw": access.Withdraw,
	"tangent_hitl_withdraw":     access.Withdraw,

	"tangent_interaction_cancel": access.Cancel,

	"tangent_session_close": access.Close,
	"tangent_surface_close": access.Close,
}

// toolScopes is the live table: the host's tools, plus whatever plugins have
// registered and not withdrawn.
type toolScopes struct {
	mu     sync.RWMutex
	scopes map[string]string
}

func newToolScopes() *toolScopes {
	t := &toolScopes{scopes: make(map[string]string, len(hostToolScopes))}
	for name, scope := range hostToolScopes {
		t.scopes[name] = scope
	}
	return t
}

func (t *toolScopes) lookup(name string) (string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	scope, ok := t.scopes[name]
	return scope, ok && scope != ""
}

func (t *toolScopes) set(name, scope string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scopes[name] = scope
}

func (t *toolScopes) remove(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.scopes, name)
}

// ToolScope is the scope a tool (or a deprecated alias of one) needs, or not ok
// when it has none (and is therefore unreachable).
func (s *Server) ToolScope(name string) (string, bool) {
	s.pluginMu.RLock()
	if canonical, alias := s.legacyAliases[name]; alias {
		name = canonical
	}
	s.pluginMu.RUnlock()
	return s.scopes.lookup(name)
}

// Authorizer decides whether the caller in ctx may call tool, which needs
// scope. The host supplies one that asks the auth guard. With none, every call
// is refused: a server nobody authorizes is not an open one.
type Authorizer func(ctx context.Context, tool, scope string) error

// defaultAuthorizer applies when a server is built with no WithAuthorizer. It
// is nil, which refuses every call; only the package's own tests replace it
// (export_test.go), so no production path can come to run without one.
var defaultAuthorizer Authorizer

// WithAuthorizer installs the host's authorizer. Passing nil is an explicit
// choice to refuse every call.
func WithAuthorizer(a Authorizer) Option {
	return func(server *Server) error {
		server.authorizer, server.authorizerSet = a, true
		return nil
	}
}

// WithServer makes this the SDK server the tools register on, so the host's
// transport serves them. Without it New builds its own, for tests and for
// transports that connect to it directly.
func WithServer(server *gmcpserver.Server) Option {
	return func(s *Server) error {
		if server == nil {
			return errors.New("mcp: server is nil")
		}
		s.external = server
		return nil
	}
}

// errToolRefused is what a refused call answers. It names nothing: not the
// scope, not whether the tool exists.
var errToolRefused = toolErrorResult("forbidden", "this credential may not call that tool")

// authorizeMiddleware refuses a tools/call whose tool has no scope, or whose
// caller the authorizer refuses. A deprecated alias is authorized as the tool
// it names.
func (s *Server) authorizeMiddleware(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
	return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
		if method != "tools/call" {
			return next(ctx, method, req)
		}
		call, ok := req.(*mcpsdk.CallToolRequest)
		if !ok || call.Params == nil {
			return nil, errToolRefused
		}
		name := call.Params.Name
		s.pluginMu.RLock()
		if canonical, alias := s.legacyAliases[name]; alias {
			name = canonical
		}
		s.pluginMu.RUnlock()
		scope, known := s.scopes.lookup(name)
		if !known || s.authorizer == nil || s.authorizer(ctx, name, scope) != nil {
			var result mcpsdk.CallToolResult
			result.SetError(errToolRefused)
			return &result, nil //nolint:nilerr // a refusal is a tool error result, not a protocol error
		}
		return next(ctx, method, req)
	}
}
