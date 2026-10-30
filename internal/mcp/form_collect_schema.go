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
        "context": {"type": "string", "description": "Orienting prose shown above the workflow. Renders as markdown."},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "data": {
          "type": "object",
          "required": ["form_id", "schema"],
          "properties": {
            "form_id": {"type": "string", "minLength": 1},
            "intent": {"type": "string", "description": "What this form is for. Renders as markdown."},
            "schema": {"type": "object", "anyOf": [{"required": ["fields"]}, {"required": ["sections"]}], "properties": {"fields": {"type": "array", "items": {"type": "object", "required": ["id", "type", "label"], "properties": {"id": {"type": "string", "minLength": 1}, "type": {"type": "string"}, "label": {"type": "string"}}}}, "sections": {"type": "array", "items": {"type": "object", "required": ["id", "fields"], "properties": {"id": {"type": "string", "minLength": 1}, "title": {"type": "string"}, "fields": {"type": "array", "items": {"type": "object", "required": ["id", "type", "label"], "properties": {"id": {"type": "string", "minLength": 1}, "type": {"type": "string"}, "label": {"type": "string"}}}}}}}}, "description": "{fields, sections}. Fields are keyed by \"id\" (not \"name\"); each needs id, type and label. A section is {id, title, fields: [...]} (id and fields required) and holds its own fields: do not point a flat field at a section with a \"section\" key. Each section description renders as markdown. Field help text does not: it is composed with Tangent\u2019s own \"Required.\"/\"Optional.\" sentence. A field with \"secret\": true renders masked and is never stored: the answer is replaced by a redaction tombstone before anything is written, and the value reaches the creating caller once, in the first read of the resolved outcome. A secret field must be type text, textarea or password (type password is always secret) and must not be prefilled. If the value can no longer be delivered (already read, expired, or Tangent restarted) the answer holds the tombstone and the response carries meta.secret_unavailable: re-request it."},
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
