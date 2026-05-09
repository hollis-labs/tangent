package mcp

const outputRenderEnvelopeType = "tangent.output-render"

var outputRenderInputSchemaJSON = []byte(`{
  "type": "object",
  "title": "tangent.output_render input",
  "description": "An output-render envelope to dispatch through Tangent.",
  "properties": {
    "envelope": {
      "type": "object",
      "description": "The output-render envelope to dispatch. Wire-shape mirrors go-envelopes Envelope.",
      "properties": {
        "v": {"type": "integer", "minimum": 1},
        "id": {"type": "string", "minLength": 1},
        "type": {"type": "string", "const": "tangent.output-render"},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "data": {
          "type": "object",
          "properties": {
            "title": {"type": "string"},
            "markdown": {"type": "string", "minLength": 1},
            "filename": {"type": "string", "minLength": 1},
            "format": {"type": "string", "enum": ["markdown"]},
            "summary": {"type": "string"}
          },
          "required": ["markdown"],
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
