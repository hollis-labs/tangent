---
name: tangent-whiteboard
description: Use when launching Tangent's `tangent.whiteboard` workflow. Provides a minimal copy/paste example and pointers to the deeper manual recipe, integration guide, and schema.
---

# Tangent Whiteboard

Use this skill when you need to launch `tangent.whiteboard`.

## Copy/paste example

```text
Use `tangent.whiteboard` to open a board called `board-1` so I can sketch a simple layout, then wait for my submit/cancel response.
```

## Notes

- Keep the browser tab on the same room until submit resolves.
- For reproducible runs, prefer the exact payload from the manual recipe.

## Pointers

- Manual recipe: `docs/manual-tests/whiteboard-e2e.md`
- Smoke runbook: `docs/manual-tests/workflow-smoke-tests.md`
- Integration guide: `docs/mcp-integration.md`
- Envelope extension: `internal/envelope/extensions/whiteboard.go`
- Data schema: `internal/envelope/extensions/whiteboard_schema.json`
- Handler: `internal/mcp/whiteboard_handler.go`
