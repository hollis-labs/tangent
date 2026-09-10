package mcp

const appBoardEnvelopeType = "tangent.app-board"

// appBoardInputSchemaJSON advertises the tool's input. It is intentionally
// looser than the kind's request.schema.json — the manifest schema is what
// validates, and duplicating every nested shape here would be a second source
// of truth that drifts. What this document does is name the fields so a caller
// reading the tool list knows what to send.
var appBoardInputSchemaJSON = []byte(`{
  "title": "tangent.app-board input",
  "description": "An app-board envelope to dispatch through Tangent. Filters are a view over the cards you supply; Tangent never re-runs them as a query.",
  "type": "object",
  "properties": {
    "envelope": {
      "type": "object",
      "properties": {
        "v": {"type": "integer", "const": 1},
        "id": {"type": "string", "minLength": 1},
        "type": {"type": "string", "const": "tangent.app-board"},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "trace": {"type": "object"},
        "meta": {"type": "object"},
        "data": {
          "type": "object",
          "properties": {
            "board_id": {"type": "string", "minLength": 1},
            "title": {"type": "string"},
            "source": {"type": "object", "description": "Which application supplied the cards: {app, label}. A label for the reader; Tangent calls nothing."},
            "columns": {"type": "array", "description": "[{id, label, card_ids}]. A card_id naming no supplied card is ignored by the renderer."},
            "cards": {"type": "array", "description": "[{id, title, subtitle, badges, body, fields}]. body and detail sections render as markdown."},
            "filters": {"type": "array", "description": "[{id, label, kind, field, options, selected}]. A VIEW over the cards above — the host cannot reach a record you did not send."},
            "detail": {"type": "object", "description": "Optional pane inside this envelope, not a second one: {card_id, open, raised_by, sections, actions}."},
            "updated_at": {"type": "string"}
          },
          "required": ["board_id", "cards"]
        }
      },
      "required": ["v", "id", "type", "data"]
    }
  },
  "required": ["envelope"]
}`)
