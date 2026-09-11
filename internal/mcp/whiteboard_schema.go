package mcp

const whiteboardEnvelopeType = "tangent.whiteboard"

var whiteboardInputSchemaJSON = []byte(`{
  "type": "object",
  "title": "tangent.whiteboard input",
  "description": "A whiteboard envelope to dispatch through Tangent.",
  "properties": {
    "envelope": {
      "type": "object",
      "description": "The whiteboard envelope to dispatch. Wire-shape mirrors go-envelopes Envelope.",
      "properties": {
        "v": {"type": "integer", "minimum": 1},
        "id": {"type": "string", "minLength": 1},
        "type": {"type": "string", "const": "tangent.whiteboard"},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string", "description": "Orienting prose shown above the workflow. Renders as markdown."},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "data": {
          "type": "object",
          "properties": {
            "board_id": {"type": "string", "minLength": 1},
            "title": {"type": "string"},
            "intent": {"type": "string", "description": "What this board is for. Renders as markdown, as does each revision summary."},
            "scene": {"type": "object"},
            "assets": {
              "type": "array",
              "items": {
                "type": "object",
                "properties": {
                  "asset_id": {"type": "string"},
                  "artifact_id": {"type": "string"},
                  "name": {"type": "string"},
                  "mime_type": {"type": "string"},
                  "source": {"type": "string"},
                  "uri": {"type": "string"},
                  "kind": {"type": "string", "enum": ["reference_image"]},
                  "width": {"type": "integer", "minimum": 0},
                  "height": {"type": "integer", "minimum": 0}
                },
                "additionalProperties": false
              }
            },
            "export_refs": {
              "type": "array",
              "items": {
                "type": "object",
                "properties": {
                  "artifact_id": {"type": "string"},
                  "name": {"type": "string"},
                  "mime_type": {"type": "string"},
                  "kind": {"type": "string", "enum": ["png"]},
                  "uri": {"type": "string"},
                  "created_at": {"type": "string"},
                  "size_bytes": {"type": "integer", "minimum": 0},
                  "width": {"type": "integer", "minimum": 0},
                  "height": {"type": "integer", "minimum": 0}
                },
                "additionalProperties": false
              }
            },
            "notes": {"type": "string"},
            "tool_mode": {"type": "string", "enum": ["select", "draw", "text", "shape", "arrow", "note"]},
            "reference_images": {
              "type": "array",
              "items": {
                "type": "object",
                "properties": {
                  "asset_id": {"type": "string"},
                  "artifact_id": {"type": "string"},
                  "name": {"type": "string"},
                  "mime_type": {"type": "string"},
                  "source": {"type": "string"},
                  "uri": {"type": "string"},
                  "kind": {"type": "string", "enum": ["reference_image"]},
                  "width": {"type": "integer", "minimum": 0},
                  "height": {"type": "integer", "minimum": 0}
                },
                "additionalProperties": false
              }
            }
          },
          "required": ["board_id"],
          "additionalProperties": false
        },
        "trace": {"type": "object"},
        "meta": {"type": "object"}
      },
      "required": ["v", "id", "type", "data"]
    }
  },
  "required": ["envelope"]
}`)
