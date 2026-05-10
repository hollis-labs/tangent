---
name: tangent-approval-queue
description: Use when launching Tangent's `tangent.approval-queue` workflow. Provides a minimal copy/paste example and pointers to the deeper manual recipe, integration guide, and schema.
---

# Tangent Approval Queue

Use this skill when you need to launch `tangent.approval-queue`.

## Copy/paste example

```text
Use `tangent.approval-queue` with two review items and let me make one decision on one queued item. Wait for my submit/cancel response.
```

## Pointers

- Manual recipe: `docs/manual-tests/approval-queue-e2e.md`
- Smoke runbook: `docs/manual-tests/workflow-smoke-tests.md`
- Integration guide: `docs/mcp-integration.md`
- Tool schema: `internal/mcp/approval_queue_schema.go`
- Handler: `internal/mcp/approval_queue_handler.go`
