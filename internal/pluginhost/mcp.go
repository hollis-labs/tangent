package pluginhost

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/hollis-labs/tangent/pkg/plugin"
)

// This file is the second registration surface this host honors: an MCP tool
// contributed by a plugin (ADR 0007 §4, CW-20260910-0029).
//
// # Why the SDK's own interface is not the registration
//
// `subprocess.MCPHandler` is the *dispatch* half — "the host dispatches
// mcp/call_tool requests here" — and its doc says registration is declarative,
// read from a subprocess plugin's `plugin.yaml`. This host spawns no
// subprocess and reads no yaml, so there is no declaration to read. The base
// `plugin.Host` contract carries no tool-registration method either; the SDK
// says so in as many words, and names extending the contract in the host's own
// package as the intended path.
//
// So RegisterMCPTool is Tangent's extension, and it takes the handler
// explicitly rather than type-asserting the plugin. That is not ceremony: the
// scoped host supplied to Load binds the registration to its actual owner;
// direct registrations on Host itself remain host-owned.
//
// # What a plugin cannot do here
//
// It cannot shadow. A tool name already claimed — by a host tool or by another
// plugin — is refused by name. The MCP SDK's registry is a map keyed by name
// and a second AddTool on the same name silently replaces the first, which is
// the exact failure this refusal exists to prevent: an agent calling
// `tangent.session_create` and reaching a plugin instead would be
// indistinguishable from it working.
//
// It cannot leave the namespace either. Every tool this build serves is
// `tangent.<name>`, and internal/smoke/docs_test.go's documentation gate finds
// tools by exactly that spelling. A plugin tool that could not be spelled that
// way would be a shipped tool the gate cannot see, so the shape is required
// here rather than discovered later as a hole in the gate.
//
// # The documentation gate covers plugin tools. Deliberately.
//
// CW-20260910-0029 asked for that answer to be decided rather than defaulted.
// It is yes, and it needs no code: the gate derives its reference surface by
// asking the *shipped binary* for tools/list, and a plugin tool is in that
// answer like any other. A plugin that contributes a tool and documents it
// nowhere fails `make smoke`. That is the intended outcome — "who ships it" was
// never the question the gate asks, "can an operator find it" is.

// ToolNamespace is plugin.ToolNamespace: the prefix every tool this build
// serves carries, which the documentation gate matches tool mentions by.
const ToolNamespace = plugin.ToolNamespace

// toolName is the shape a tool name must have after the namespace prefix. It
// is deliberately the same alphabet internal/smoke/docs_test.go's toolMention
// regexp accepts, because a name that regexp cannot match is a name the
// documentation gate cannot police.
var toolName = regexp.MustCompile(`^tangent\.[a-z][a-z0-9_-]*$`)

// Errors this host returns from the MCP surface.
var (
	// ErrInvalidTool reports a structurally unusable tool registration.
	ErrInvalidTool = errors.New("pluginhost: invalid MCP tool registration")
	// ErrToolNameClaimed reports a tool name a plugin has already claimed on
	// this host. A collision with a *host* tool is caught by internal/mcp,
	// which is the only place that knows the host surface.
	ErrToolNameClaimed = errors.New("pluginhost: MCP tool name already claimed by another plugin")
)

// MCPTool is one agent-callable tool a plugin contributes. See plugin.MCPTool.
type MCPTool = plugin.MCPTool

// RegisterMCPTool records one plugin-contributed MCP tool.
//
// It only records. The tool reaches the MCP surface when internal/mcp is
// constructed with MCPTools() — plugins load before the MCP server exists,
// because a plugin-contributed envelope kind has to be in the registry the MCP
// server reads at construction. Registration and installation are therefore two
// steps, and this is the first one.
func (h *Host) RegisterMCPTool(tool MCPTool) error { return h.registerMCPTool(tool, nil) }
func (h *Host) registerMCPTool(tool MCPTool, owner *registrationOwner) error {
	if tool.Name == "" {
		return fmt.Errorf("%w: tool has no name", ErrInvalidTool)
	}
	if !toolName.MatchString(tool.Name) {
		return fmt.Errorf(
			"%w: %q is not a %s<name> tool name (lower-case letters, digits, `_` and `-` after the prefix); "+
				"a name outside the namespace is a shipped tool the documentation gate cannot check",
			ErrInvalidTool, tool.Name, ToolNamespace)
	}
	if tool.Handler == nil {
		return fmt.Errorf(
			"%w: %s supplies no subprocess.MCPHandler, so nothing would service a call to it",
			ErrInvalidTool, tool.Name)
	}
	if len(tool.InputSchema) == 0 {
		return fmt.Errorf(
			"%w: %s supplies no input schema; MCP requires one and an absent schema means every "+
				"argument reaches the plugin unvalidated",
			ErrInvalidTool, tool.Name)
	}
	var shape struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(tool.InputSchema, &shape); err != nil {
		return fmt.Errorf("%w: %s input schema is not JSON: %w", ErrInvalidTool, tool.Name, err)
	}
	if shape.Type != "object" {
		return fmt.Errorf(
			`%w: %s input schema has type %q; MCP requires "object"`,
			ErrInvalidTool, tool.Name, shape.Type)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if owner != nil && !h.currentOwnerLocked(owner) {
		return ErrPluginNotLoaded
	}
	if _, claimed := h.tools[tool.Name]; claimed {
		return fmt.Errorf("%w: %s", ErrToolNameClaimed, tool.Name)
	}
	// Wrapped here rather than at the dispatch site, so the handler is bounded
	// and panic-safe before anything can call it and internal/mcp does not have
	// to remember to do it. See isolation.go for what the guard promises — and
	// for the one thing it does not, which is that the plugin stopped.
	tool.Handler = guardedMCPHandler{
		name: tool.Name, release: h.release, budget: h.budget, inner: tool.Handler, owner: owner,
	}
	if h.toolRegistry != nil {
		if err := h.toolRegistry.AddPluginTool(tool); err != nil {
			return err
		}
	}
	h.tools[tool.Name] = tool
	if owner != nil {
		h.toolOwners[tool.Name] = owner
	}
	h.logger.Info("pluginhost: mcp tool contributed", "tool", tool.Name)
	return nil
}

// MCPTools returns every plugin-contributed tool, sorted by name.
//
// Sorted rather than in registration order because this is what the MCP server
// installs: two builds with the same plugins should produce the same surface
// regardless of load order, so a diff of tools/list means something changed.
func (h *Host) MCPTools() []MCPTool {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]MCPTool, 0, len(h.tools))
	for _, tool := range h.tools {
		out = append(out, tool)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
