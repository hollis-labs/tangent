---
name: tangent-design-iteration
description: Use when launching Tangent's `tangent_design_iteration` workflow. Provides a minimal copy/paste example and pointers to the deeper manual recipe, MCP integration guide, and schema.
---

# Tangent Design Iteration

Use this skill when you need to launch `tangent_design_iteration`.

## Copy/paste example

```text
Use `tangent_design_iteration` to show me two simple hero-card concepts and let me choose one action for the current iteration. Wait for my submit/cancel response.
```

## What the operator sees

A `click-region` prompt makes that part of the preview selectable. A click only **selects** it: the
page shows "Selected: <label>" with **Send selection** and **Clear**, and the item stays pending until
the operator sends. Buttons and text prompts submit with their own Send. A region selector that matches
a large area (for example `.board` or `body`) is fine now, but keep regions meaningful, because the
selected region's `action_id` is what you get back.

## Design iteration or artifact?

This tool asks for **one choice** and resolves when the operator sends it. If you just want to show a
design, iterate on it over several revisions and let the operator compare and comment, send an
artifact instead: `tangent_artifact_create` with `type: html`, then `tangent_artifact_update` for each
revision (one item, a revision switcher, Request changes, optional Approve; it never resolves by
accident). See the `tangent-artifacts` skill.

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

Recover a result three equivalent ways: `tangent_interaction_get`,
`tangent_interaction_await`, or by retrying this call with the identical
envelope id and payload. All three return the same immutable result. A retry
with a *changed* payload is an `IDEMPOTENCY_CONFLICT`; use a new envelope id.

Acknowledge with `tangent_interaction_ack` when your side has committed
to the result. Reading a result does not acknowledge it.

Full contract: `docs/room-workflow-completion.md`

## Pointers

- Manual recipe: `docs/manual-tests/design-iteration-e2e.md`
- Smoke runbook: `docs/manual-tests/workflow-smoke-tests.md`
- Integration guide: `docs/mcp-integration.md`
- Tool schema: `internal/mcp/design_iteration_schema.go`
- Completion contract: `docs/room-workflow-completion.md`
