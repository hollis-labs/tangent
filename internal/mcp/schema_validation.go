package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// go-mcp (CW-20260917-0018) registers tools through the official SDK's raw,
// untyped (*mcpsdk.Server).AddTool — not the schema-validating generic
// mcpsdk.AddTool[In, Out] this package used before. That method's own doc
// string says validation is the caller's responsibility: "Unmarshaling the
// arguments and validating them against the input schema are the caller's
// responsibility." go-mcp does not fill that gap; nothing in its server
// package resolves or validates a registered Tool.InputSchema.
//
// Several of this host's own schemas declare `additionalProperties: false`
// at the root specifically to reject malformed input before a handler runs
// (see gateway_metadata.go, which exists because that strictness once
// rejected legitimate gateway-injected fields too). This file restores that
// enforcement in the one place it belongs: a receiving middleware, run after
// gatewayMetadataMiddleware strips transport metadata, so validation sees
// exactly the arguments a handler would.
//
// It reproduces the exact validate → apply-defaults → validate sequence and
// error text ("validating \"arguments\": ...") the SDK's own typed AddTool
// generic used to produce, via the same public jsonschema-go API that
// generic uses internally (Schema.Resolve, Resolved.ApplyDefaults,
// Resolved.Validate) — see jsonschema-go's own validate.go doc comment on
// ApplyDefaults for the recommended order.

// resolveToolSchemas resolves every registered tool's InputSchema once, after
// registration completes (registerTools installs host tools; plugin tools
// are installed by the same path, so this covers both without either having
// to opt in). A tool whose InputSchema is not a *jsonschema.Schema — none
// today — is served without input validation rather than failing the boot,
// since go-mcp's own Tool.InputSchema field is declared `any` precisely to
// allow other schema representations.
func (s *Server) resolveToolSchemas() error {
	definitions := s.mcp.ToolDefinitions()
	resolved := make(map[string]*jsonschema.Resolved, len(definitions))
	for _, def := range definitions {
		schema, ok := def.InputSchema.(*jsonschema.Schema)
		if !ok || schema == nil {
			continue
		}
		r, err := schema.Resolve(nil)
		if err != nil {
			return fmt.Errorf("resolve input schema for %s: %w", def.Name, err)
		}
		resolved[def.Name] = r
	}
	s.resolvedSchemas = resolved
	return nil
}

// inputSchemaValidationMiddleware validates a tools/call's arguments against
// the tool's registered InputSchema before the handler runs. A tool with no
// resolved schema (none, once resolveToolSchemas has run) passes through
// unchanged.
func (s *Server) inputSchemaValidationMiddleware(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
	return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
		if method != "tools/call" {
			return next(ctx, method, req)
		}
		call, ok := req.(*mcpsdk.CallToolRequest)
		if !ok || call.Params == nil {
			return next(ctx, method, req)
		}
		resolved := s.resolvedSchemas[call.Params.Name]
		if resolved == nil {
			return next(ctx, method, req)
		}

		args := map[string]any{}
		if len(call.Params.Arguments) > 0 {
			if err := json.Unmarshal(call.Params.Arguments, &args); err != nil {
				return schemaValidationError(err)
			}
		}
		// Recommended order per jsonschema-go: resolve (already done),
		// ApplyDefaults, then Validate.
		if err := resolved.ApplyDefaults(&args); err != nil {
			return schemaValidationError(err)
		}
		if err := resolved.Validate(args); err != nil {
			return schemaValidationError(err)
		}
		encoded, err := json.Marshal(args)
		if err != nil {
			return schemaValidationError(err)
		}
		call.Params.Arguments = encoded
		return next(ctx, method, req)
	}
}

// schemaValidationError reports a schema failure the same way the SDK's own
// typed AddTool generic used to: a normal (non-protocol-level) tool result
// with IsError set and the SDK's own "validating \"arguments\": ..." message,
// so a client or test that already matches on that text keeps working.
func schemaValidationError(err error) (mcpsdk.Result, error) {
	var result mcpsdk.CallToolResult
	result.SetError(fmt.Errorf("validating \"arguments\": %w", err))
	return &result, nil
}
