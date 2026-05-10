---
name: tangent-spreadsheet-review
description: Use when launching Tangent's `tangent.spreadsheet-review` workflow. Provides a minimal copy/paste example and pointers to the deeper manual recipe, integration guide, and schema.
---

# Tangent Spreadsheet Review

Use this skill when you need to launch `tangent.spreadsheet-review`.

## Copy/paste example

```text
Use `tangent.spreadsheet-review` to open a table called `table-1` with two rows and one bulk action, then wait for my submit/cancel response.
```

## Notes

- Seed real `columns`, `rows`, and usually `row_actions`.
- For deterministic smoke runs, copy the exact payload from the manual recipe.

## Pointers

- Manual recipe: `docs/manual-tests/spreadsheet-review-e2e.md`
- Smoke runbook: `docs/manual-tests/workflow-smoke-tests.md`
- Integration guide: `docs/mcp-integration.md`
- Envelope extension: `internal/envelope/extensions/spreadsheet_review.go`
- Data schema: `internal/envelope/extensions/spreadsheet_review_schema.json`
- Handler: `internal/mcp/spreadsheet_review_handler.go`
