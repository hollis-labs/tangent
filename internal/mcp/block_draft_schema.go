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
        "context": {"type": "string", "description": "Orienting prose shown above the workflow. Renders as markdown."},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "data": {
          "type": "object",
          "properties": {
            "block_id": {"type": "string", "minLength": 1},
            "mode": {"type": "string", "enum": ["section", "paragraph"]},
            "label": {"type": "string"},
            "content": {"type": "string", "minLength": 1, "description": "The proposed block. Renders as markdown, as does the accepted draft it is appended to."},
            "rationale": {"type": "string", "description": "Why this block, for the reviewer. Renders as markdown."},
            "outline_hint": {"type": "string", "description": "Where this block sits in the outline. Renders as markdown."}
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
