package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// This file is how a plugin *drives* Tangent, as opposed to how it registers
// with it (CW-20260910-0031).
//
// # Why a tool caller and not a typed facade
//
// An app plugin's whole job is to open a surface and keep it fresh: create a
// room, present an envelope, read back what the participant staged, replace it
// with fresh content. Every one of those is already a tool an agent calls, and
// the settled composition rule (Tesseract `agents_drive_tangent_apps_are_called`)
// is that an agent drives Tangent. A plugin doing the same work should be the
// same kind of caller, not a second kind with a private door.
//
// So the host grants one method, and it grants no authority a local MCP caller
// does not already have: the calls go through the real tool surface, the real
// middleware, and the same host-assigned caller identity every direct MCP
// caller resolves to. Adding a typed method per need would be the shape ADR
// 0007's risk section warns about — a host surface that grows because the SDK
// or a consumer asked, one method at a time, until the boundary is a list.
//
// It is also deliberately NOT `GetService`. That surface stays unimplemented
// because it is untyped and unbounded: a plugin that could reach the database
// or the room manager through it would make every boundary in this repository
// advisory. A tool caller can do exactly what the tool surface can do, and the
// tool surface is a contract with tests.

// ErrToolCallerUnavailable reports a plugin trying to drive Tangent before the
// host has been given a caller — which in a real process means before the MCP
// server exists. It is not a state a plugin can recover from; it is a
// composition-root defect, and it says so.
var ErrToolCallerUnavailable = errors.New(
	"pluginhost: no tool caller is attached to this host")

// ErrToolCallerAttached reports a second AttachToolCaller. One host has one
// tool surface, and silently replacing it would leave earlier callers holding
// a handle to a surface nothing else uses.
var ErrToolCallerAttached = errors.New("pluginhost: a tool caller is already attached")

// ToolResult is one tool call's answer.
//
// Content is the tool's own JSON, verbatim. IsError distinguishes a tool that
// answered "no" from a transport failure, which comes back as an error — the
// same split the MCP contract makes, preserved rather than collapsed, because
// "Torque said this task does not exist" and "the call never arrived" need
// different handling by the plugin.
type ToolResult struct {
	Content json.RawMessage
	IsError bool
}

// Unmarshal decodes a successful result into value. An isError result is
// returned as an error carrying the tool's own body, so a plugin that forgets
// to check IsError cannot mistake a refusal for data.
func (r ToolResult) Unmarshal(value any) error {
	if r.IsError {
		return fmt.Errorf("tool reported an error: %s", string(r.Content))
	}
	if len(r.Content) == 0 {
		return errors.New("tool returned no content")
	}
	return json.Unmarshal(r.Content, value)
}

// ToolCaller invokes one of the host's own MCP tools in process.
type ToolCaller interface {
	CallTool(ctx context.Context, name string, arguments any) (ToolResult, error)
}

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
