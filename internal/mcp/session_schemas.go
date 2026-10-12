package mcp

var sessionCreateInputSchemaJSON = []byte(`{
  "type": "object",
  "title": "tangent_session_create input",
  "properties": {
    "title": {"type": "string"},
    "meta": {"type": "object"}
  }
}`)

var sessionAdvanceInputSchemaJSON = []byte(`{
  "type": "object",
  "title": "tangent_session_advance input",
  "properties": {
    "roomID": {"type": "string", "minLength": 1},
    "envelope": {
      "type": "object",
      "properties": {
        "v": {"type": "integer", "minimum": 1},
        "id": {"type": "string", "minLength": 1},
        "type": {"type": "string", "minLength": 1},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "data": {"type": "object"},
        "trace": {"type": "object"},
        "meta": {"type": "object"}
      },
      "required": ["v", "id", "type", "data"]
    }
  },
  "required": ["roomID", "envelope"]
}`)

var sessionGetInputSchemaJSON = []byte(`{
  "type": "object",
  "title": "tangent_session_get input",
  "properties": {
    "roomID": {"type": "string", "minLength": 1}
  },
  "required": ["roomID"]
}`)

var sessionAdvancePhaseInputSchemaJSON = []byte(`{
  "type": "object",
  "title": "tangent_session_phase_advance input",
  "properties": {
    "roomID": {"type": "string", "minLength": 1},
    "to_phase": {"type": "string", "minLength": 1},
    "reason": {"type": "string"}
  },
  "required": ["roomID", "to_phase"]
}`)

var sessionSetPhaseOutputInputSchemaJSON = []byte(`{
  "type": "object",
  "title": "tangent_session_phase_output_set input",
  "properties": {
    "roomID": {"type": "string", "minLength": 1},
    "phase": {"type": "string", "minLength": 1},
    "key": {"type": "string", "minLength": 1},
    "value": {}
  },
  "required": ["roomID", "phase", "key", "value"]
}`)

var sessionCloseInputSchemaJSON = []byte(`{
  "type": "object",
  "title": "tangent_session_close input",
  "properties": {
    "roomID": {"type": "string", "minLength": 1},
    "status": {"type": "string"}
  },
  "required": ["roomID"]
}`)

var sessionListInputSchemaJSON = []byte(`{
  "type": "object",
  "title": "tangent_session_list input",
  "properties": {
    "active_only": {"type": "boolean"}
  }
}`)
