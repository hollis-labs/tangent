package mcp

import (
	"fmt"
	"sort"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// This file is the one place a tool name is claimed on this server.
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

// addTool registers one typed tool and claims its name.
//
// It is a thin wrapper over mcpsdk.AddTool and must stay one: the SDK's generic
// does the schema binding, the argument unmarshaling and the output validation,
// and reimplementing any of that here would be a second contract to keep in
// step with the first.
func addTool[In, Out any](
	server *Server,
	tool *mcpsdk.Tool,
	handler mcpsdk.ToolHandlerFor[In, Out],
) {
	server.claimToolName(tool.Name)
	mcpsdk.AddTool(server.mcp, tool, handler)
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
