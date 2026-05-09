package mcp

const designIterationEnvelopeType = "tangent.design-iteration"

var designIterationInputSchemaJSON = []byte(`{
  "type": "object",
  "title": "tangent.design-iteration input",
  "description": "A design-iteration envelope to dispatch through Tangent.",
  "properties": {
    "envelope": {
      "type": "object",
      "description": "The design-iteration envelope to dispatch. Wire-shape mirrors go-envelopes Envelope.",
      "properties": {
        "v": {"type": "integer", "minimum": 1, "description": "Envelope protocol version."},
        "id": {"type": "string", "minLength": 1, "description": "Client-supplied envelope id (echoed in the Response)."},
        "type": {"type": "string", "const": "tangent.design-iteration", "description": "Must be 'tangent.design-iteration' for this tool."},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "data": {
          "type": "object",
          "required": ["variant_id", "html", "prompts"],
          "properties": {
            "caption": {"type": "string"},
            "variant_id": {"type": "string", "minLength": 1},
            "html": {"type": "string", "minLength": 1},
            "prompts": {
              "type": "array",
              "minItems": 1,
              "items": {
                "type": "object",
                "required": ["id", "kind"],
                "properties": {
                  "id": {"type": "string", "minLength": 1},
                  "kind": {"type": "string", "enum": ["click-region", "button", "text-input"]},
                  "label": {"type": "string"},
                  "selector": {"type": "string"},
                  "placeholder": {"type": "string"}
                },
                "allOf": [
                  {
                    "if": {
                      "properties": { "kind": { "const": "click-region" } },
                      "required": ["kind"]
                    },
                    "then": {
                      "required": ["selector", "label"]
                    }
                  },
                  {
                    "if": {
                      "properties": { "kind": { "const": "button" } },
                      "required": ["kind"]
                    },
                    "then": {
                      "required": ["label"]
                    }
                  },
                  {
                    "if": {
                      "properties": { "kind": { "const": "text-input" } },
                      "required": ["kind"]
                    },
                    "then": {
                      "required": ["label"]
                    }
                  }
                ],
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
