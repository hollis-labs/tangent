package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	gmcpserver "github.com/hollis-labs/go-mcp/server"
	"github.com/hollis-labs/plugin-sdk/subprocess"

	"github.com/hollis-labs/tangent/internal/pluginhost"
)

// This file installs plugin-contributed MCP tools onto the Tangent tool
// surface (ADR 0007 §4, CW-20260910-0029).
//
// # A plugin tool is an ordinary tool
//
// It appears in tools/list on both transports, it carries a JSON Schema the
// server validates arguments against before the plugin sees them, and it is
// subject to the documentation gate like every other shipped tool. Nothing
// about the wire says a tool came from a plugin, and nothing should: an agent
// choosing a tool is not making a trust decision, the host already made it.
//
// # Why this file registers a raw handler rather than a typed one
//
// go-mcp's Tool.Handler is untyped — func(ctx, args map[string]any) (any,
// error) — for every tool, host and plugin alike (see tool_registry.go's
// addTool, which wraps a typed Go struct around the same untyped contract).
// A plugin tool has no Go struct to wrap in the first place — its schema
// arrives as JSON from across the plugin boundary — so it registers directly
// against go-mcp's own contract instead of going through addTool. Argument
// validation is not this file's job either way: it runs once, centrally, for
// every registered tool (schema_validation.go).
//
// # Three things a plugin cannot do to the surface
//
//   - **Shadow.** The name is claimed through tool_registry.go, after every
//     host tool. A collision fails New and names the tool.
//   - **Skip validation.** Arguments are checked against the plugin's own
//     declared schema before dispatch. A plugin that declares a schema and
//     then receives something else would be a plugin whose schema is
//     decorative.
//   - **Take the server down.** A handler that panics is contained and
//     reported as a tool error. A tool surface where one plugin's bug is every
//     caller's outage is not a tool surface worth having.

const (
	// errorCodePluginFailed is returned when a plugin handler reports an error
	// or returns a result this host cannot represent.
	errorCodePluginFailed = "PLUGIN_FAILED"
	// errorCodePluginPanicked is returned when a plugin handler panics. It is
	// deliberately distinct from PLUGIN_FAILED: an error is a plugin saying
	// no, and a panic is a plugin defect, and an operator reading a log should
	// not have to tell them apart by prose.
	errorCodePluginPanicked = "PLUGIN_PANICKED"
)

// registerPluginTools installs every plugin-contributed tool. It runs last in
// registerTools so the host surface has already claimed its names.
//
// Every plugin-contributed tool carries the same conservative annotation —
// mutating, destructive, non-idempotent, open-world — the "unaudited name
// defaults to the dangerous assumption" posture: the host has no way to know
// a plugin tool's actual read/write shape, and inferring "safe" from a name
// is the exact bug the go-mcp annotation contract exists to make impossible.
// A plugin that wants a more permissive hint has no way to declare one today;
// that is a narrower, deliberate gap, not an oversight.
func (s *Server) registerPluginTools() error {
	for _, tool := range s.pluginTools {
		if s.isToolNameClaimed(tool.Name) {
			// Refused before the claim rather than reported as a generic
			// conflict afterwards: this collision has a nameable cause and a
			// clear remedy, and the message should say so.
			return fmt.Errorf(
				"plugin tool %s collides with a tool this build already serves; rename the plugin's "+
					"tool — the MCP SDK's registry would otherwise have kept the plugin's handler and "+
					"silently shadowed the host's",
				tool.Name)
		}
		s.claimToolName(tool.Name)
		schema, err := pluginToolSchema(tool)
		if err != nil {
			return err
		}
		s.mcp.RegisterTool(gmcpserver.Tool{
			Name:            tool.Name,
			Description:     tool.Description,
			InputSchema:     schema,
			Handler:         pluginToolHandler(tool),
			DestructiveHint: true,
			OpenWorldHint:   true,
		})
	}
	return nil
}

// pluginToolSchema parses the plugin's declared input schema.
//
// pluginhost has already checked that it is JSON and that its type is
// "object" — this is the second parse, into the type the SDK wants, and it
// exists because the declaration crosses a package boundary as bytes so the
// same declaration will survive CW-20260910-0034's subprocess mode unchanged.
func pluginToolSchema(tool pluginhost.MCPTool) (*jsonschema.Schema, error) {
	var schema jsonschema.Schema
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		return nil, fmt.Errorf("unmarshal plugin tool %s input schema: %w", tool.Name, err)
	}
	return &schema, nil
}

// pluginToolHandler adapts one plugin's SDK dispatch interface to go-mcp's
// untyped tool handler.
//
// Argument validation against the plugin's own declared schema is no longer
// this function's job: inputSchemaValidationMiddleware (schema_validation.go)
// validates every registered tool's arguments — host and plugin alike —
// before any handler runs, from the same resolved schema this file supplies
// at registration. A bad-arguments failure therefore never reaches here.
func pluginToolHandler(
	tool pluginhost.MCPTool,
) gmcpserver.ToolHandler {
	return func(
		ctx context.Context,
		arguments map[string]any,
	) (result any, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				// Contained here rather than left to the SDK: a panic that
				// escapes this handler takes down the connection serving every
				// other tool, and the caller learns nothing about which plugin
				// did it.
				result = nil
				err = toolErrorResult(errorCodePluginPanicked,
					fmt.Sprintf("plugin tool %s panicked: %v", tool.Name, recovered))
			}
		}()

		if arguments == nil {
			// Absent arguments decode to an empty object rather than nil, so a
			// schema with no required properties accepts a call that passed
			// none — which is what every other tool on this surface does.
			arguments = map[string]any{}
		}

		// SessionID is deliberately empty. Tangent's MCP transports run
		// stateless (see Server.HTTPHandler), so there is no session identity
		// to pass — and a synthesized one would be a fact about nothing that a
		// plugin could nevertheless key state on.
		call, callErr := tool.Handler.MCPCallTool(ctx, subprocess.MCPCallRequest{
			ToolName:  tool.Name,
			Arguments: arguments,
		})
		if callErr != nil {
			return nil, toolErrorResult(errorCodePluginFailed,
				fmt.Sprintf("plugin tool %s: %v", tool.Name, callErr))
		}
		if len(call.Envelopes) > 0 {
			// The SDK lets a plugin emit envelopes alongside a tool result.
			// This host has no path for that: an envelope reaches a
			// participant by being presented on a room through the interaction
			// substrate, which is what the room-backed tools do and what a
			// plugin tool can do by calling them. Accepting the field and
			// dropping it would be a plugin believing it displayed something.
			return nil, toolErrorResult(errorCodePluginFailed,
				fmt.Sprintf("plugin tool %s returned %d envelope(s); this host does not emit envelopes "+
					"from a tool result — present them on a room instead",
					tool.Name, len(call.Envelopes)))
		}

		body := call.Content
		if len(body) == 0 {
			body = json.RawMessage("{}")
		}
		if call.IsError {
			return nil, &rawToolError{
				message: fmt.Sprintf("plugin tool %s reported an error", tool.Name),
				body:    body,
			}
		}
		return body, nil
	}
}
