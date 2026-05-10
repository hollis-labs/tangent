package mcp

const dashboardEnvelopeType = "tangent.dashboard"

var dashboardInputSchemaJSON = []byte(`{
  "title": "tangent.dashboard input",
  "description": "A dashboard envelope to dispatch through Tangent.",
  "type": "object",
  "properties": {
    "envelope": {
      "type": "object",
      "properties": {
        "v": {"type": "integer", "const": 1},
        "id": {"type": "string", "minLength": 1},
        "type": {"type": "string", "const": "tangent.dashboard"},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "trace": {"type": "object"},
        "meta": {"type": "object"},
        "data": {
          "type": "object",
          "properties": {
            "dashboard_id": {"type": "string", "minLength": 1},
            "title": {"type": "string"},
            "tiles": {"type": "array"},
            "layout": {"type": "array"},
            "saved_layouts": {"type": "array"},
            "active_layout_id": {"type": "string"},
            "query_state": {"type": "object"},
            "summary": {"type": "object"},
            "updated_at": {"type": "string"}
          },
          "required": ["dashboard_id", "tiles"]
        }
      },
      "required": ["v", "id", "type", "data"]
    }
  },
  "required": ["envelope"]
}`)
