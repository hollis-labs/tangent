package pluginhost

import (
	"errors"
	"fmt"

	"github.com/hollis-labs/tangent/pkg/plugin"
)

// The caller a plugin drives Tangent through, and its result, are defined in
// pkg/plugin, the public plugin surface; they are aliased here so the host and
// a plugin share one type. The reasoning (why a tool caller rather than a typed
// facade, and why not GetService) is on pkg/plugin's ToolCaller.

// ErrToolCallerUnavailable is plugin.ErrToolCallerUnavailable: the same error
// value, so errors.Is matches either spelling.
var ErrToolCallerUnavailable = plugin.ErrToolCallerUnavailable

// ErrToolCallerAttached reports a second AttachToolCaller. One host has one
// tool surface, and silently replacing it would leave earlier callers holding
// a handle to a surface nothing else uses.
var ErrToolCallerAttached = errors.New("pluginhost: a tool caller is already attached")

// ToolResult is one tool call's answer. See plugin.ToolResult.
type ToolResult = plugin.ToolResult

// ToolCaller invokes one of the host's own MCP tools. See plugin.ToolCaller.
type ToolCaller = plugin.ToolCaller

// AttachToolCaller gives the host the tool surface plugins drive.
//
// The composition root calls it after the MCP server is constructed, which is
// necessarily after plugins have loaded — a plugin-contributed envelope kind
// has to be in the registry the MCP server reads. A plugin therefore resolves
// the caller at dispatch time rather than holding one from Load.
func (h *Host) AttachToolCaller(caller ToolCaller) error {
	if caller == nil {
		return fmt.Errorf("pluginhost: tool caller is nil")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.toolCaller != nil {
		return ErrToolCallerAttached
	}
	h.toolCaller = caller
	return nil
}

// Tools returns the attached tool caller, or an error naming what is missing.
//
// It returns an error rather than a nil interface because a nil ToolCaller is
// a panic waiting at the first call, several frames away from the mistake.
func (h *Host) Tools() (ToolCaller, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.toolCaller == nil {
		return nil, ErrToolCallerUnavailable
	}
	return h.toolCaller, nil
}
