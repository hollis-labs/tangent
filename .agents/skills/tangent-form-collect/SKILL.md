---
name: tangent-form-collect
description: Use when launching Tangent's `tangent.form-collect` workflow. Provides a minimal copy/paste example and pointers to the deeper manual recipe, integration guide, and schema.
---

# Tangent Form Collect

Use this skill when you need to launch `tangent.form-collect`.

## Copy/paste example

```text
Use `tangent.form-collect` with one required text field, one priority selector, one optional notify checkbox, and one submit action. Wait for my submit/cancel response.
```

## Notes

- `action_id` in the submitted payload is optional.
- If you need a literal schema payload, use the full manual recipe.

## Pointers

- Manual recipe: `docs/manual-tests/form-collect-e2e.md`
- Smoke runbook: `docs/manual-tests/workflow-smoke-tests.md`
- Integration guide: `docs/mcp-integration.md`
- Tool schema: `internal/mcp/form_collect_schema.go`
- Handler: `internal/mcp/form_collect_handler.go`
