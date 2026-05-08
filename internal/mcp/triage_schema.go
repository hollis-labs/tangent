// Package mcp wires Tangent's HTTP-mounted MCP server. It registers two
// tools — tangent.list_workflows (catalog) and tangent.triage (envelope
// dispatch) — over the official modelcontextprotocol/go-sdk transports.
//
// PR 3 establishes the layer; PR 4 will register a real triage handler on
// the dispatcher that backs tangent.triage. Until then, the triage tool
// validates the inbound envelope and surfaces the dispatcher's
// ErrNoHandler as a NOT_WIRED tool error so MCP clients can integration-
// test the wire shape without waiting on the WebSocket bridge.
package mcp

// triageInputSchema is the v0.1 hand-rolled JSON Schema for the
// tangent.triage MCP tool's input.
//
// Why hand-rolled: triage is NOT in go-envelopes v0.1.0's core manifest
// (verified by the implementer brief — `triage` will land in core during
// the v0.3 visual-kinds work; tracked as
// followups.tangent.v01.triage_kind_in_go_envelopes). Until then, Tangent
// declares the tool surface here so MCP clients can call the tool today
// even though the registry itself does not know about the type. The
// envelope payload carried in `envelope.data` is intentionally permissive
// for v0.1 — the WebSocket bridge in PR 4 narrows it once the real
// handler exists.
//
// The shape mirrors the `Envelope` struct in go-envelopes/types.go: only
// the fields a client must populate to get a triage flow off the ground
// are required (v, id, type, data). Optional fields are listed but not
// required so future extensions don't break v0.1 callers.
const triageEnvelopeType = "triage"

// triageInputSchemaJSON is the marshaled JSON Schema document advertised
// to MCP clients via tools/list. The SDK ingests it into a
// jsonschema-go.Schema; we keep it as raw bytes because the source of
// truth is the go-envelopes manifest (when triage lands there in v0.3 we
// will swap this stub out for the registry-derived schema).
//
// The schema is a stripped-down twin of the canonical Envelope wire
// format; see protocol-spec-v1.md §6 for the full definition.
var triageInputSchemaJSON = []byte(`{
  "type": "object",
  "title": "tangent.triage input",
  "description": "A triage-kind envelope (v0.1 hand-rolled stub; will be replaced by the registry schema once 'triage' lands in go-envelopes core).",
  "properties": {
    "envelope": {
      "type": "object",
      "description": "The triage envelope to dispatch. Wire-shape mirrors go-envelopes Envelope.",
      "properties": {
        "v": {"type": "integer", "minimum": 1, "description": "Envelope protocol version."},
        "id": {"type": "string", "minLength": 1, "description": "Client-supplied envelope id (echoed in the Response)."},
        "type": {"type": "string", "const": "triage", "description": "Must be 'triage' for this tool."},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "data": {"type": "object", "description": "Triage-specific payload; v0.1 leaves this open."},
        "trace": {"type": "object"},
        "meta": {"type": "object"}
      },
      "required": ["v", "id", "type"]
    }
  },
  "required": ["envelope"]
}`)
