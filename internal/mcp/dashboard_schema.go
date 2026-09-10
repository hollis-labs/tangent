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
        "context": {"type": "string", "description": "Orienting prose shown above the workflow. Renders as markdown."},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "trace": {"type": "object"},
        "meta": {"type": "object"},
        "data": {
          "type": "object",
          "properties": {
            "dashboard_id": {"type": "string", "minLength": 1},
            "title": {"type": "string"},
            "tiles": {"type": "array", "description": "[{tile_id, kind, title, value, unit, status, subtitle, summary, workflow, room_id}]. Each tile summary renders as markdown."},
            "layout": {"type": "array"},
            "saved_layouts": {"type": "array"},
            "active_layout_id": {"type": "string"},
            "query_state": {"type": "object"},
            "summary": {"type": "object", "description": "{status, headline, detail, tile_count, active_room_count, ...}. detail renders as markdown; headline is a single-line heading and does not."},
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
