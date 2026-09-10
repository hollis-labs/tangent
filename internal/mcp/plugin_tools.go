package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	envelopes "github.com/hollis-labs/go-envelopes"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/hollis-labs/plugin-sdk/subprocess"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

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
// # Why the untyped AddTool
//
// Every host tool goes through the SDK's generic AddTool, which binds a Go
// struct and gets schema validation for free. A plugin tool has no Go type
// here — its schema arrives as JSON — so it takes the untyped registration,
// and the responsibilities the generic was carrying become this file's:
// unmarshaling arguments, validating them against the declared schema, and
// shaping the result. That is the whole reason this file is longer than the
// registration it performs.
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
		resolved, err := schema.Resolve(nil)
		if err != nil {
			return fmt.Errorf("resolve plugin tool %s input schema: %w", tool.Name, err)
		}
		s.mcp.AddTool(&mcpsdk.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schema,
		}, pluginToolHandler(tool, resolved))
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

// pluginToolHandler adapts one plugin's SDK dispatch interface to the MCP
// SDK's untyped tool handler.
//
// It never returns a non-nil error. Every failure — bad arguments, a plugin
// error, a plugin panic — comes back as an isError tool result carrying the
// envelope error-code vocabulary the rest of this package already speaks, so a
// caller branches on `code` rather than on which layer broke.
func pluginToolHandler(
	tool pluginhost.MCPTool,
	resolved *jsonschema.Resolved,
) mcpsdk.ToolHandler {
	return func(
		ctx context.Context,
		request *mcpsdk.CallToolRequest,
	) (result *mcpsdk.CallToolResult, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				// Contained here rather than left to the SDK: a panic that
				// escapes this handler takes down the connection serving every
				// other tool, and the caller learns nothing about which plugin
				// did it.
				result = toolErrorResult(errorCodePluginPanicked,
					fmt.Sprintf("plugin tool %s panicked: %v", tool.Name, recovered))
				err = nil
			}
		}()

		arguments, argErr := pluginToolArguments(request)
		if argErr != nil {
			return toolErrorResult(envelopes.ErrorCodeValidationFailed,
				fmt.Sprintf("%s arguments are not a JSON object: %v", tool.Name, argErr)), nil
		}
		if validateErr := resolved.Validate(arguments); validateErr != nil {
			return toolErrorResult(envelopes.ErrorCodeValidationFailed,
				fmt.Sprintf("%s arguments do not match the tool's schema: %v", tool.Name, validateErr)), nil
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
			return toolErrorResult(errorCodePluginFailed,
				fmt.Sprintf("plugin tool %s: %v", tool.Name, callErr)), nil
		}
		if len(call.Envelopes) > 0 {
			// The SDK lets a plugin emit envelopes alongside a tool result.
			// This host has no path for that: an envelope reaches a
			// participant by being presented on a room through the interaction
			// substrate, which is what the room-backed tools do and what a
			// plugin tool can do by calling them. Accepting the field and
			// dropping it would be a plugin believing it displayed something.
			return toolErrorResult(errorCodePluginFailed,
				fmt.Sprintf("plugin tool %s returned %d envelope(s); this host does not emit envelopes "+
					"from a tool result — present them on a room instead",
					tool.Name, len(call.Envelopes))), nil
		}

		body := string(call.Content)
		if body == "" {
			body = "{}"
		}
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: body}},
			IsError: call.IsError,
		}, nil
	}
}

// pluginToolArguments decodes a call's arguments into the shape both the
// validator and the SDK's plugin contract want.
//
// Absent arguments decode to an empty object rather than nil, so a schema with
// no required properties accepts a call that passed none — which is what every
// other tool on this surface does.
func pluginToolArguments(request *mcpsdk.CallToolRequest) (map[string]any, error) {
	arguments := map[string]any{}
	if request == nil || request.Params == nil {
		return arguments, nil
	}
	raw := request.Params.Arguments
	if len(raw) == 0 || string(raw) == "null" {
		return arguments, nil
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, err
	}
	return arguments, nil
}
