package mcp

const wizardEnvelopeType = "tangent.wizard"

var wizardInputSchemaJSON = []byte(`{
  "type": "object",
  "title": "tangent.wizard input",
  "description": "A guided wizard envelope to dispatch through Tangent.",
  "properties": {
    "envelope": {
      "type": "object",
      "properties": {
        "v": {"type": "integer", "minimum": 1},
        "id": {"type": "string", "minLength": 1},
        "type": {"type": "string", "const": "tangent.wizard"},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "data": {
          "type": "object",
          "required": ["wizard_id", "steps"],
          "properties": {
            "wizard_id": {"type": "string", "minLength": 1},
            "title": {"type": "string"},
            "description": {"type": "string"},
            "current_step_id": {"type": "string"},
            "steps": {"type": "array"},
            "progress": {"type": "array"},
            "branch_selections": {"type": "array"},
            "summary": {"type": "object"},
            "updated_at": {"type": "string"}
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
