package mcp

const synthesisNotesEnvelopeType = "tangent.synthesis-notes"

var synthesisNotesInputSchemaJSON = []byte(`{
  "type": "object",
  "title": "tangent.synthesis_notes input",
  "description": "A synthesis-notes envelope to persist private notes and send a phase-gated preview through Tangent.",
  "properties": {
    "envelope": {
      "type": "object",
      "description": "The synthesis-notes envelope to dispatch. Wire-shape mirrors go-envelopes Envelope.",
      "properties": {
        "v": {"type": "integer", "minimum": 1},
        "id": {"type": "string", "minLength": 1},
        "type": {"type": "string", "const": "tangent.synthesis-notes"},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "data": {
          "type": "object",
          "properties": {
            "private_notes": {"type": "string", "minLength": 1},
            "summary": {"type": "string"},
            "outline_state": {"type": "string", "enum": ["absent", "present", "skipped"]},
            "outline": {
              "type": "object",
              "properties": {
                "title": {"type": "string"},
                "items": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "properties": {
                      "label": {"type": "string"},
                      "description": {"type": "string"}
                    },
                    "additionalProperties": false
                  }
                }
              },
              "additionalProperties": false
            }
          },
          "required": ["private_notes"],
          "additionalProperties": false,
          "allOf": [
            {
              "if": {
                "properties": {
                  "outline_state": {"const": "present"}
                },
                "required": ["outline_state"]
              },
              "then": {
                "required": ["outline"]
              }
            }
          ]
        },
        "trace": {"type": "object"},
        "meta": {"type": "object"}
      },
      "required": ["v", "id", "type", "data"]
    }
  },
  "required": ["envelope"]
}`)
