package mcp

import (
	"context"
	"encoding/json"
	"sort"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/telemetry"
)

// Gateway transport metadata at the MCP tool boundary (CW-20260907-0022).
//
// Tether's `mux` proxy forwards a tool call with the caller's W3C trace
// context written INTO the tool arguments as underscore-prefixed keys
// (`_traceparent`, and `_tracestate` when the trace carries vendor state; see
// github.com/hollis-labs/go-otel/propagation.InjectMCP). MCP puts request
// metadata in `_meta`, not in arguments, so that placement is a gateway defect
// with its own Tether task. Until it moves, every strict tool here — the v1
// HITL contract sets `additionalProperties: false` at its root — refused the
// whole call with:
//
//	validating "arguments": validating root: unexpected additional properties ["_traceparent"]
//
// which meant a Claude Code session on the default mux configuration could not
// call a single HITL tool.
//
// The fix is a receiving middleware ahead of the SDK's input-schema validation
// (that validation runs inside the typed tool handler the SDK wraps around
// ours, so nothing in a Tangent handler runs first). It removes exactly the
// accepted keys from the arguments, hands the parsed traceparent to telemetry
// as the upstream trace context, and leaves everything else untouched. The
// schemas stay strict: `_traceparent` is transport metadata, not a caller
// assertion, and an unknown underscore key is still an error.

// acceptedGatewayMetadata is the whole set of argument keys Tangent treats as
// gateway transport metadata. It is deliberately tiny and defined once.
//
//   - `_traceparent`: the W3C trace context Tether's proxy injects on every
//     forwarded call. Parsed and recorded as the upstream trace.
//   - `_tracestate`: injected only when the upstream trace carries vendor
//     state (same injector, conditional). Stripped and not interpreted:
//     Tangent has no vendor-state consumer and must not store free text.
//
// Adding a key here is a deliberate act that needs the injector's source as
// evidence, the way both entries carry it.
var acceptedGatewayMetadata = map[string]bool{
	"_traceparent": true,
	"_tracestate":  true,
}

// GatewayMetadataKeys returns the accepted key set, sorted, for documentation
// and tests that assert the set has not grown by accident.
func GatewayMetadataKeys() []string {
	keys := make([]string, 0, len(acceptedGatewayMetadata))
	for key := range acceptedGatewayMetadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// gatewayMetadataMiddleware strips accepted gateway metadata from tools/call
// arguments before the SDK validates them, and attaches the parsed upstream
// trace context to the request context so every observation made while
// serving the call carries it (see internal/telemetry/upstream.go).
//
// Every other method, a call whose arguments are not a JSON object, and a call
// carrying none of the accepted keys pass through byte-for-byte unchanged.
func gatewayMetadataMiddleware(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
	return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
		if method != "tools/call" {
			return next(ctx, method, req)
		}
		call, ok := req.(*mcpsdk.CallToolRequest)
		if !ok || call.Params == nil || len(call.Params.Arguments) == 0 {
			return next(ctx, method, req)
		}
		arguments, upstream, changed := stripGatewayMetadata(call.Params.Arguments)
		if !changed {
			return next(ctx, method, req)
		}
		call.Params.Arguments = arguments
		if upstream.Valid() {
			ctx = telemetry.ContextWithUpstreamTrace(ctx, upstream)
		}
		return next(ctx, method, req)
	}
}

// stripGatewayMetadata removes the accepted keys from a JSON object and
// returns the remaining arguments, the parsed traceparent (zero when absent or
// malformed), and whether anything was removed. Arguments that are not a JSON
// object are returned as they were: the SDK's own validation is the right
// place for that error to surface.
//
// The re-encoded object is what the schema validates and what the tool
// handler receives, so a caller's own fields are preserved exactly (each value
// is carried as raw JSON, never decoded and re-encoded).
func stripGatewayMetadata(raw json.RawMessage) (json.RawMessage, telemetry.UpstreamTrace, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return raw, telemetry.UpstreamTrace{}, false
	}
	var upstream telemetry.UpstreamTrace
	changed := false
	for key := range acceptedGatewayMetadata {
		value, present := fields[key]
		if !present {
			continue
		}
		changed = true
		delete(fields, key)
		if key != "_traceparent" {
			continue
		}
		var header string
		if json.Unmarshal(value, &header) != nil {
			// A non-string traceparent is the gateway's bug, not the
			// caller's. Strip it so the call still validates, and record no
			// upstream link rather than a fabricated one.
			continue
		}
		if parsed, ok := telemetry.ParseTraceparent(header); ok {
			upstream = parsed
		}
	}
	if !changed {
		return raw, telemetry.UpstreamTrace{}, false
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return raw, telemetry.UpstreamTrace{}, false
	}
	return encoded, upstream, true
}
