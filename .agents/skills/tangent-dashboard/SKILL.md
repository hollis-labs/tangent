---
name: tangent-dashboard
description: Use when launching Tangent's `tangent.dashboard` workflow. Provides a minimal copy/paste example and pointers to the deeper manual recipe, integration guide, and schema.
---

# Tangent Dashboard

Use this skill when you need to launch `tangent.dashboard`.

## Copy/paste example

Use this exact payload shape:

```json
{
  "dashboard_id": "ops-dashboard",
  "title": "Ops dashboard",
  "tiles": [
    {
      "tile_id": "tile-open",
      "kind": "room_count",
      "title": "Open rooms",
      "value": "4",
      "room_id": "room-123"
    },
    {
      "tile_id": "tile-review",
      "kind": "workflow_summary",
      "title": "Reviews waiting",
      "value": "2",
      "workflow": "tangent.diff-review",
      "artifact_ref": "artifact://review-002"
    }
  ],
  "layout": [
    { "tile_id": "tile-open", "x": 0, "y": 0, "w": 2, "h": 1 },
    { "tile_id": "tile-review", "x": 1, "y": 0, "w": 2, "h": 1 }
  ],
  "saved_layouts": [
    {
      "layout_id": "layout-default",
      "name": "Default",
      "is_default": true,
      "tiles": [
        { "tile_id": "tile-open", "x": 0, "y": 0, "w": 2, "h": 1 },
        { "tile_id": "tile-review", "x": 1, "y": 0, "w": 2, "h": 1 }
      ]
    }
  ]
}
```

## Notes

- Seed `tiles` explicitly.
- `layout` and `saved_layouts` are strongly recommended for a useful run.

## Completion and recovery

Prefer async so the call returns as soon as the request is durable:

```json
{ "completion": { "mode": "async" } }
```

It returns a successful receipt with `"status": "pending"`, a durable handle,
and the room URL to hand a human. Omitting `completion` keeps the v0.12
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

- Manual recipe: `docs/manual-tests/dashboard-e2e.md`
- Smoke runbook: `docs/manual-tests/workflow-smoke-tests.md`
- Integration guide: `docs/mcp-integration.md`
- Tool schema: `internal/mcp/dashboard_schema.go`
- Handler: `internal/mcp/dashboard_handler.go`
- Completion contract: `docs/room-workflow-completion.md`
