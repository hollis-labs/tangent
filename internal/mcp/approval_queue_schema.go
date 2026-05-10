package mcp

const approvalQueueEnvelopeType = "tangent.approval-queue"

var approvalQueueInputSchemaJSON = []byte(`{
  "title": "tangent.approval-queue input",
  "description": "An approval-queue envelope to dispatch through Tangent.",
  "type": "object",
  "properties": {
    "envelope": {
      "type": "object",
      "description": "The approval-queue envelope to dispatch. Wire-shape mirrors go-envelopes Envelope.",
      "properties": {
        "v": {"type": "integer", "const": 1},
        "id": {"type": "string", "minLength": 1},
        "type": {"type": "string", "const": "tangent.approval-queue"},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "trace": {"type": "object"},
        "meta": {"type": "object"},
        "data": {
          "type": "object",
          "properties": {
            "queue_id": {"type": "string", "minLength": 1},
            "title": {"type": "string"},
            "intent": {"type": "string"},
            "items": {"type": "array", "items": {"type": "object"}},
            "current_index": {"type": "integer", "minimum": 0},
            "notes": {"type": "string"}
          },
          "required": ["queue_id", "items"]
        }
      },
      "required": ["v", "id", "type", "data"]
    }
  },
  "required": ["envelope"]
}`)
