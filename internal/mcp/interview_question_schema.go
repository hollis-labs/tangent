package mcp

const interviewQuestionEnvelopeType = "tangent.interview-question"

var interviewQuestionInputSchemaJSON = []byte(`{
  "type": "object",
  "title": "tangent.interview_question input",
  "description": "An interview-question envelope to dispatch through Tangent.",
  "properties": {
    "envelope": {
      "type": "object",
      "description": "The interview-question envelope to dispatch. Wire-shape mirrors go-envelopes Envelope.",
      "properties": {
        "v": {"type": "integer", "minimum": 1},
        "id": {"type": "string", "minLength": 1},
        "type": {"type": "string", "const": "tangent.interview-question"},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "data": {
          "type": "object",
          "properties": {
            "prompt": {"type": "string", "minLength": 1},
            "prompt_markdown": {"type": "string", "minLength": 1},
            "helper_text": {"type": "string"},
            "choices": {
              "type": "array",
              "items": {
                "type": "object",
                "required": ["id", "label"],
                "properties": {
                  "id": {"type": "string", "minLength": 1},
                  "label": {"type": "string", "minLength": 1},
                  "description": {"type": "string"}
                },
                "additionalProperties": false
              }
            },
            "thread_id": {"type": "string", "minLength": 1},
            "topic_label": {"type": "string", "minLength": 1},
            "output_shape": {
              "type": "object",
              "required": ["label"],
              "properties": {
                "label": {"type": "string", "minLength": 1},
                "help": {"type": "string"},
                "placeholder": {"type": "string"}
              },
              "additionalProperties": false
            }
          },
          "additionalProperties": false,
          "allOf": [
            {
              "anyOf": [
                {"required": ["prompt"]},
                {"required": ["prompt_markdown"]}
              ]
            },
            {
              "anyOf": [
                {"required": ["thread_id"]},
                {"required": ["topic_label"]}
              ]
            }
          ]
        },
        "trace": {"type": "object"},
        "meta": {"type": "object"}
      },
      "required": ["v", "id", "type", "data"]
    }
  },
  "required": ["envelope"]
}`)
