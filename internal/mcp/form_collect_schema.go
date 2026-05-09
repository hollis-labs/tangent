package mcp

const formCollectEnvelopeType = "tangent.form-collect"

var formCollectInputSchemaJSON = []byte(`{
  "type": "object",
  "title": "tangent.form-collect input",
  "description": "A generalized form-collect envelope to dispatch through Tangent.",
  "properties": {
    "envelope": {
      "type": "object",
      "description": "The form-collect envelope to dispatch.",
      "properties": {
        "v": {"type": "integer", "minimum": 1},
        "id": {"type": "string", "minLength": 1},
        "type": {"type": "string", "const": "tangent.form-collect"},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "data": {
          "type": "object",
          "required": ["form_id", "schema"],
          "properties": {
            "form_id": {"type": "string", "minLength": 1},
            "intent": {"type": "string"},
            "schema": {"type": "object"},
            "answers": {"type": "object"},
            "notes": {"type": "string"},
            "actions": {"type": "array"},
            "saved_drafts": {"type": "array"},
            "templates": {"type": "array"},
            "attachment_refs": {"type": "array"},
            "submission_summary": {"type": "object"}
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
