---
name: tangent-wizard
description: Use when launching Tangent's `tangent.wizard` workflow. Provides a minimal valid copy/paste payload and pointers to the deeper manual recipe, integration guide, and schema.
---

# Tangent Wizard

Use this skill when you need to launch `tangent.wizard`.

## Copy/paste example

Use this exact payload shape:

```json
{
  "wizard_id": "wizard-1",
  "title": "Release wizard",
  "current_step_id": "step-scope",
  "steps": [
    {
      "step_id": "step-scope",
      "title": "Scope",
      "kind": "form",
      "fields": {
        "fields": [
          {
            "field_id": "scope",
            "label": "Scope",
            "kind": "textarea"
          }
        ]
      },
      "branches": [
        {
          "branch_id": "review",
          "label": "Review",
          "target_step_id": "step-review"
        }
      ]
    },
    {
      "step_id": "step-review",
      "title": "Review",
      "kind": "review"
    }
  ]
}
```

## Notes

- `current_step_id` must match a real `steps[].step_id`.
- Every branch `target_step_id` must point at a real step.
- Use the exact payload above unless you intentionally need a different flow.

## Pointers

- Manual recipe: `docs/manual-tests/wizard-e2e.md`
- Smoke runbook: `docs/manual-tests/workflow-smoke-tests.md`
- Integration guide: `docs/mcp-integration.md`
- Tool schema: `internal/mcp/wizard_schema.go`
- Handler: `internal/mcp/wizard_handler.go`
- State validator: `internal/room/wizard_state.go`
