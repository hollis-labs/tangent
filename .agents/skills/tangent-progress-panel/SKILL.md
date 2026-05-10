---
name: tangent-progress-panel
description: Use when launching Tangent's `tangent.progress-panel` workflow. Provides a minimal copy/paste example and pointers to the deeper manual recipe, integration guide, and schema.
---

# Tangent Progress Panel

Use this skill when you need to launch `tangent.progress-panel`.

## Copy/paste example

Use this exact payload shape:

```json
{
  "panel_id": "panel-e2e",
  "items": [
    {
      "item_id": "scan",
      "label": "Scan repo",
      "status": "running"
    },
    {
      "item_id": "summary",
      "label": "Write summary",
      "status": "queued"
    }
  ],
  "summary": {
    "current_status": "running",
    "headline": "1 running"
  }
}
```

## Notes

- Seed `items` explicitly or the UI has nothing actionable.
- `updates`, `checkpoints`, and `summary` are optional but useful.

## Pointers

- Manual recipe: `docs/manual-tests/progress-panel-e2e.md`
- Smoke runbook: `docs/manual-tests/workflow-smoke-tests.md`
- Integration guide: `docs/mcp-integration.md`
- Tool schema: `internal/mcp/progress_panel_schema.go`
- Handler: `internal/mcp/progress_panel_handler.go`
