package mcp

const diffReviewEnvelopeType = "tangent.diff-review"

var diffReviewInputSchemaJSON = []byte(`{
  "title": "tangent.diff-review input",
  "description": "A diff-review envelope to dispatch through Tangent.",
  "type": "object",
  "properties": {
    "envelope": {
      "type": "object",
      "properties": {
        "v": {"type": "integer", "const": 1},
        "id": {"type": "string", "minLength": 1},
        "type": {"type": "string", "const": "tangent.diff-review"},
        "typeVersion": {"type": "string"},
        "title": {"type": "string"},
        "context": {"type": "string"},
        "presentation": {"type": "string", "enum": ["inline", "modal", "drawer", "sidecar", "fullscreen"]},
        "trace": {"type": "object"},
        "meta": {"type": "object"},
        "data": {
          "type": "object",
          "properties": {
            "review_id": {"type": "string", "minLength": 1},
            "files": {
              "type": "array",
              "items": {
                "type": "object",
                "description": "{id, path, summary, before, after, hunks}. Each file summary renders as markdown. before, after and every hunk stay literal \u2014 a diff is evidence the reader compares character by character.",
                "properties": {
                  "id": {"type": "string", "minLength": 1}
                },
                "required": ["id"]
              }
            },
            "current_file": {"type": "string"},
            "filter_state": {"type": "object"},
            "before_ref": {"type": "object"},
            "after_ref": {"type": "object"}
          },
          "required": ["review_id", "files"]
        }
      },
      "required": ["v", "id", "type", "data"]
    }
  },
  "required": ["envelope"]
}`)
