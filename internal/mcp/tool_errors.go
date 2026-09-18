package mcp

import (
	envelopes "github.com/hollis-labs/go-envelopes"
)

// This file is the tool-error contract every handler in this package returns
// through.
//
// go-mcp's ToolHandler is `func(ctx, args map[string]any) (any, error)`: a
// non-nil error is reported as a tool-execution error (CallToolResult with
// IsError=true), and go-mcp gives special treatment to two shapes —
// *budget.ToolError, and anything implementing budget.StructuredError — so
// the reported content is the caller's own structured body rather than a
// bare err.Error() string.
//
// Tangent's tool surface predates go-mcp and already has its own wire
// contracts for an error body: the envelope-style {v, kind, error:{code,
// message}} shape toolErrorResult builds, and the richer per-surface shapes
// hitlError/docsError build (contract_version, code, message, plus
// surface-specific fields). Neither matches budget.ToolError's fields, and
// changing either wire shape as a side effect of a library swap is exactly
// the kind of break this migration must not make. rawToolError is the
// adapter: it implements budget.StructuredError by handing back whatever
// body a caller already built, byte-for-byte.

// rawToolError reports a tool-execution error with a caller-supplied
// structured body, so a handler can keep building the exact JSON shape its
// callers already depend on instead of adopting go-mcp's own ToolError
// fields.
type rawToolError struct {
	message string
	body    any
}

// Error implements the error interface.
func (e *rawToolError) Error() string { return e.message }

// ToolErrorContent implements budget.StructuredError.
func (e *rawToolError) ToolErrorContent() any { return e.body }

// toolErrorResult builds the canonical envelope-style tool error: {v, kind:
// "error", error: {code, message}}. It is Tangent's original error
// vocabulary (validation-failed, unsupported-type, host-error, NOT_WIRED,
// and every surface-specific code), unchanged since before this package
// existed — clients branch on `code` without learning a second taxonomy.
func toolErrorResult(code, message string) error {
	return &rawToolError{
		message: code + ": " + message,
		body: map[string]any{
			"v":    envelopes.ProtocolVersion,
			"kind": string(envelopes.ResponseKindError),
			"error": map[string]any{
				"code":    code,
				"message": message,
			},
		},
	}
}
