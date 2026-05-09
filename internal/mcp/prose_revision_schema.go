package mcp

const proseRevisionEnvelopeType = "tangent.prose-revision"

var proseRevisionInputSchemaJSON = []byte(`{
  "type": "object",
  "title": "tangent.prose_revision input",
  "description": "A prose-revision envelope to dispatch through Tangent and persist explicit per-suggestion revision outcomes on the room.",
  "properties": {
    "envelope": {
      "type": "object",
      "description": "The prose-revision envelope to dispatch. Wire-shape mirrors go-envelopes Envelope.",
      "properties": {
        "v": {"type": "integer", "minimum": 1},
        "id": {"type": "string", "minLength": 1},
        "type": {"type": "string", "const": "tangent.prose-revision"},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "data": {
          "type": "object",
          "properties": {
            "lens": {"type": "string", "enum": ["review", "copy", "style"]},
            "revision_id": {"type": "string", "minLength": 1},
            "block_id": {"type": "string"},
            "label": {"type": "string"},
            "summary": {"type": "string"},
            "source_text": {"type": "string", "minLength": 1},
            "suggestions": {
              "type": "array",
              "minItems": 1,
              "items": {
                "type": "object",
                "properties": {
                  "id": {"type": "string", "minLength": 1},
                  "label": {"type": "string"},
                  "original_text": {"type": "string"},
                  "suggested_text": {"type": "string", "minLength": 1},
                  "reason": {"type": "string"}
                },
                "required": ["id", "suggested_text"],
                "additionalProperties": false
              }
            }
          },
          "required": ["lens", "source_text", "suggestions"],
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
