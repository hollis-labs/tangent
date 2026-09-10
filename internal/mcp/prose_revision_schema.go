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
        "context": {"type": "string", "description": "Orienting prose shown above the workflow. Renders as markdown."},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "data": {
          "type": "object",
          "properties": {
            "lens": {"type": "string", "enum": ["review", "copy", "style"]},
            "revision_id": {"type": "string", "minLength": 1},
            "block_id": {"type": "string"},
            "label": {"type": "string"},
            "summary": {"type": "string", "description": "What this revision pass is about. Renders as markdown."},
            "source_text": {"type": "string", "minLength": 1, "description": "The text under revision. Displayed literally, NOT as markdown: the suggestions below quote exact substrings of it, and a reflowed source would destroy the comparison the reviewer is making."},
            "suggestions": {
              "type": "array",
              "minItems": 1,
              "items": {
                "type": "object",
                "properties": {
                  "id": {"type": "string", "minLength": 1},
                  "label": {"type": "string"},
                  "original_text": {"type": "string", "description": "The current wording. Displayed literally, NOT as markdown, for the same reason source_text is."},
                  "suggested_text": {"type": "string", "minLength": 1, "description": "The proposed wording. Displayed literally, NOT as markdown, so the reviewer compares exactly what would be written."},
                  "reason": {"type": "string", "description": "Why this change, for the reviewer. Renders as markdown."}
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
