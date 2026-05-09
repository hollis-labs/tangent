package mcp

const feedbackEnvelopeType = "tangent.feedback"

var feedbackInputSchemaJSON = []byte(`{
  "type": "object",
  "title": "tangent.feedback input",
  "description": "A feedback-kind envelope to dispatch through Tangent.",
  "properties": {
    "envelope": {
      "type": "object",
      "description": "The feedback envelope to dispatch. Wire-shape mirrors go-envelopes Envelope.",
      "properties": {
        "v": {"type": "integer", "minimum": 1, "description": "Envelope protocol version."},
        "id": {"type": "string", "minLength": 1, "description": "Client-supplied envelope id (echoed in the Response)."},
        "type": {"type": "string", "const": "tangent.feedback", "description": "Must be 'tangent.feedback' for this tool."},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "data": {
          "type": "object",
          "required": ["questions"],
          "properties": {
            "prompt": {"type": "string"},
            "layout": {"type": "string", "enum": ["inline", "walkthrough", "auto"]},
            "questions": {
              "type": "array",
              "minItems": 1,
              "items": {
                "type": "object",
                "required": ["id", "type", "label"],
                "properties": {
                  "id": {"type": "string", "minLength": 1},
                  "type": {
                    "type": "string",
                    "enum": ["radio", "checkbox", "select", "multiselect", "text", "textarea"]
                  },
                  "label": {"type": "string", "minLength": 1},
                  "help": {"type": "string"},
                  "required": {"type": "boolean"},
                  "options": {
                    "type": "array",
                    "items": {
                      "type": "object",
                      "required": ["value", "label"],
                      "properties": {
                        "value": {"type": "string"},
                        "label": {"type": "string"},
                        "help": {"type": "string"}
                      },
                      "additionalProperties": false
                    }
                  },
                  "default": {},
                  "suggestion": {
                    "type": "object",
                    "required": ["value"],
                    "properties": {
                      "value": {},
                      "rationale": {"type": "string"}
                    },
                    "additionalProperties": false
                  },
                  "allowNote": {"type": "boolean"},
                  "placeholder": {"type": "string"}
                },
                "additionalProperties": false
              }
            }
          },
          "additionalProperties": true
        },
        "trace": {"type": "object"},
        "meta": {"type": "object"}
      },
      "required": ["v", "id", "type", "data"]
    }
  },
  "required": ["envelope"]
}`)
