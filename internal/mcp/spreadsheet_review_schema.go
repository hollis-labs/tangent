package mcp

const spreadsheetReviewEnvelopeType = "tangent.spreadsheet-review"

var spreadsheetReviewInputSchemaJSON = []byte(`{
  "title": "tangent.spreadsheet-review input",
  "description": "A spreadsheet-review envelope to dispatch through Tangent.",
  "type": "object",
  "properties": {
    "envelope": {
      "type": "object",
      "description": "The spreadsheet-review envelope to dispatch. Wire-shape mirrors go-envelopes Envelope.",
      "properties": {
        "v": {"type": "integer", "const": 1},
        "id": {"type": "string", "minLength": 1},
        "type": {"type": "string", "const": "tangent.spreadsheet-review"},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "trace": {"type": "object"},
        "meta": {"type": "object"},
        "data": {
          "type": "object",
          "properties": {
            "table_id": {"type": "string", "minLength": 1},
            "title": {"type": "string"},
            "intent": {"type": "string"},
            "columns": {"type": "array", "items": {"type": "object"}},
            "rows": {"type": "array", "items": {"type": "object"}},
            "query_state": {"type": "object"},
            "notes": {"type": "string"},
            "row_actions": {"type": "array", "items": {"type": "object"}}
          },
          "required": ["table_id"]
        }
      },
      "required": ["v", "id", "type", "data"]
    }
  },
  "required": ["envelope"]
}`)
