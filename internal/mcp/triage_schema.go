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
// PR 4 registers the type via the go-envelopes plugin extension API
// (see internal/envelope/extensions/triage.go). The registered name is
// "tangent.triage" — go-envelopes rejects un-namespaced names as
// reserved for core types. The MCP schema below pins envelope.type to
// the same string so direct MCP calls and dispatcher validation agree.
//
// The shape mirrors the `Envelope` struct in go-envelopes/types.go: the
// fields a client must populate to get a triage flow off the ground are
// required (v, id, type, data). Optional fields are listed but not
// required so future extensions don't break v0.1 callers.
//
// When go-envelopes upstreams a `triage` kind into core (v0.3 plan) the
// wire name flips to the bare form; the schema constant and the
// extension manifest both update together.
const triageEnvelopeType = "tangent.triage"

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
        "type": {"type": "string", "const": "tangent.triage", "description": "Must be 'tangent.triage' for this tool (registered via the plugin extension API)."},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "data": {"type": "object", "description": "Triage-specific payload; v0.1 leaves this open. The prompt, and each item\u2019s own words \u2014 a string item, or an object item\u2019s title, label, summary or name \u2014 render as markdown. An object item\u2019s remaining fields are shown as a JSON block and stay literal."},
        "trace": {"type": "object"},
        "meta": {"type": "object"}
      },
      "required": ["v", "id", "type", "data"]
    }
  },
  "required": ["envelope"]
}`)
