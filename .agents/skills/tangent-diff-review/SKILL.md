---
name: tangent-diff-review
description: Use when launching Tangent's `tangent.diff-review` workflow. Provides a minimal copy/paste example and pointers to the deeper manual recipe, integration guide, and schema.
---

# Tangent Diff Review

Use this skill when you need to launch `tangent.diff-review`.

## Copy/paste example

Use this exact payload shape:

```json
{
  "review_id": "review-manual-1",
  "files": [
    {
      "id": "file-1",
      "path": "pkg/app.go",
      "summary": "Tighten validation",
      "hunks": [
        {
          "id": "hunk-1",
          "header": "@@ -1,3 +1,4 @@",
          "before": "return nil",
          "after": "return validate()"
        }
      ]
    }
  ],
  "before_ref": {
    "artifact_id": "artifact-before",
    "name": "before.patch"
  },
  "after_ref": {
    "artifact_id": "artifact-after",
    "name": "after.patch"
  }
}
```

## Notes

- Seed `files` explicitly.
- Every file needs a stable `id`.
- Every hunk should have a stable `id` if you want per-hunk review.

## Pointers

- Manual recipe: `docs/manual-tests/diff-review-e2e.md`
- Smoke runbook: `docs/manual-tests/workflow-smoke-tests.md`
- Integration guide: `docs/mcp-integration.md`
- Tool schema: `internal/mcp/diff_review_schema.go`
- Handler: `internal/mcp/diff_review_handler.go`
