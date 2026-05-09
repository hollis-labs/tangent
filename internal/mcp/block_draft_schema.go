package mcp

const blockDraftEnvelopeType = "tangent.block-draft"

var blockDraftInputSchemaJSON = []byte(`{
  "type": "object",
  "title": "tangent.block_draft input",
  "description": "A block-draft envelope to dispatch through Tangent and persist accepted blocks on the room.",
  "properties": {
    "envelope": {
      "type": "object",
      "description": "The block-draft envelope to dispatch. Wire-shape mirrors go-envelopes Envelope.",
      "properties": {
        "v": {"type": "integer", "minimum": 1},
        "id": {"type": "string", "minLength": 1},
        "type": {"type": "string", "const": "tangent.block-draft"},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "data": {
          "type": "object",
          "properties": {
            "block_id": {"type": "string", "minLength": 1},
            "mode": {"type": "string", "enum": ["section", "paragraph"]},
            "label": {"type": "string"},
            "content": {"type": "string", "minLength": 1},
            "rationale": {"type": "string"},
            "outline_hint": {"type": "string"}
          },
          "required": ["block_id", "content"],
          "additionalProperties": false
        },
        "trace": {"type": "object"},
        "meta": {"type": "object"}
      },
      "required": ["v", "id", "type", "data"]
    }
  },
  "required": ["envelope"]
}`)
