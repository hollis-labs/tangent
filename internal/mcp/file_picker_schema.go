package mcp

const filePickerEnvelopeType = "tangent.file-picker"

var filePickerInputSchemaJSON = []byte(`{
  "title": "tangent.file-picker input",
  "description": "A file-picker envelope to dispatch through Tangent.",
  "type": "object",
  "properties": {
    "envelope": {
      "type": "object",
      "properties": {
        "v": {"type": "integer", "const": 1},
        "id": {"type": "string", "minLength": 1},
        "type": {"type": "string", "const": "tangent.file-picker"},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "trace": {"type": "object"},
        "meta": {"type": "object"},
        "data": {
          "type": "object",
          "properties": {
            "picker_id": {"type": "string", "minLength": 1},
            "browse_roots": {"type": "array"},
            "selected_refs": {"type": "array"},
            "query_state": {"type": "object"},
            "files": {"type": "array"}
          },
          "required": ["picker_id", "browse_roots"]
        }
      },
      "required": ["v", "id", "type", "data"]
    }
  },
  "required": ["envelope"]
}`)
