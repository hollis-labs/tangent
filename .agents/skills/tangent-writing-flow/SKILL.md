---
name: tangent-writing-flow
description: Use when launching Tangent's multi-phase writing flow via `tangent_session_*`, `tangent_interview_question`, `tangent_synthesis_notes`, `tangent_block_draft`, `tangent_prose_revision`, and `tangent_output_render`. Provides a tight phase order, one copy/paste example, and pointers to deeper docs.
---

# Tangent Writing Flow

Use this skill when you need the full writing workflow, not a single bundled workflow.

## Phase order

1. `tangent_session_create`
2. `tangent_session_phase_advance` to `interview`
3. `tangent_interview_question`
4. advance to `synthesis`
5. `tangent_synthesis_notes`
6. advance to `drafting`
7. `tangent_block_draft`
8. advance to `revision`
9. `tangent_prose_revision`
10. advance to `output`
11. `tangent_output_render`
12. `tangent_session_get`

Reuse the same `roomID` across the whole flow.

## Copy/paste example

```text
Create one Tangent writing-flow room with `tangent_session_create`, reuse that same roomID through interview, synthesis, drafting, revision, and output, and use the exact sample payloads from `docs/manual-tests/writing-flow-e2e.md`. Wait for my submit/cancel response at each browser step.
```

## Completion and recovery

Prefer async so the call returns as soon as the request is durable:

```json
{ "completion": { "mode": "async", "reply_to": "msg://agent/agent-mux/agt_YOURS" } }
```

`reply_to` is **required**: your own Tether URN (`tether_whoami` returns it). When
the operator answers, or the item ends without an answer, Tangent mails the
outcome there. It arrives on your next turn, or at once if you are idle, so
send the request with `mode: "async"` and carry on; you do not need to poll or
await. The mail's `tangent.response` holds the operator's answer. The
polling tools below remain as recovery.


It returns a successful receipt with `"status": "pending"`, a durable handle,
and the room URL to hand a human. Without `mode: "async"` the call keeps the inline
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

- Full flow: `docs/manual-tests/writing-flow-e2e.md`
- Phase-specific docs:
  `docs/manual-tests/interview-question-e2e.md`,
  `docs/manual-tests/synthesis-notes-e2e.md`,
  `docs/manual-tests/block-draft-e2e.md`,
  `docs/manual-tests/prose-revision-e2e.md`
- Smoke runbook: `docs/manual-tests/workflow-smoke-tests.md`
- Integration guide: `docs/mcp-integration.md`
- Session tools: `internal/mcp/session_tools.go`
- Phase schemas:
  `internal/mcp/interview_question_schema.go`,
  `internal/mcp/synthesis_notes_schema.go`,
  `internal/mcp/block_draft_schema.go`,
  `internal/mcp/prose_revision_schema.go`,
  `internal/mcp/output_render_schema.go`
- Completion contract: `docs/room-workflow-completion.md`
