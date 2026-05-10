package mcp

const progressPanelEnvelopeType = "tangent.progress-panel"

var progressPanelInputSchemaJSON = []byte(`{
  "title": "tangent.progress-panel input",
  "description": "A progress-panel envelope to dispatch through Tangent.",
  "type": "object",
  "properties": {
    "envelope": {
      "type": "object",
      "properties": {
        "v": {"type": "integer", "const": 1},
        "id": {"type": "string", "minLength": 1},
        "type": {"type": "string", "const": "tangent.progress-panel"},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "trace": {"type": "object"},
        "meta": {"type": "object"},
        "data": {
          "type": "object",
          "properties": {
            "panel_id": {"type": "string", "minLength": 1},
            "items": {"type": "array"},
            "updates": {"type": "array"},
            "summary": {"type": "object"}
          },
          "required": ["panel_id", "items"]
        }
      },
      "required": ["v", "id", "type", "data"]
    }
  },
  "required": ["envelope"]
}`)
