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

## Completion and recovery

Prefer async so the call returns as soon as the request is durable:

```json
{ "completion": { "mode": "async" } }
```

It returns a successful receipt with `"status": "pending"`, a durable handle,
and the room URL to hand a human. Omitting `completion` keeps the inline
default: wait up to 45 seconds, return the normal response if the operator
answers in time, and otherwise return that same pending receipt — never an
error, and never a cancellation.

Recover a result three equivalent ways: `tangent.interaction_get`,
`tangent.interaction_await`, or by retrying this call with the identical
envelope id and payload. All three return the same immutable result. A retry
with a *changed* payload is an `IDEMPOTENCY_CONFLICT`; use a new envelope id.

Acknowledge with `tangent.interaction_acknowledge` when your side has committed
to the result. Reading a result does not acknowledge it.

Full contract: `docs/room-workflow-completion.md`

## Pointers

- Manual recipe: `docs/manual-tests/diff-review-e2e.md`
- Smoke runbook: `docs/manual-tests/workflow-smoke-tests.md`
- Integration guide: `docs/mcp-integration.md`
- Tool schema: `internal/mcp/diff_review_schema.go`
- Handler: `internal/mcp/diff_review_handler.go`
- Completion contract: `docs/room-workflow-completion.md`
