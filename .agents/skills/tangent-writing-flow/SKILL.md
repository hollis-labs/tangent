---
name: tangent-writing-flow
description: Use when launching Tangent's multi-phase writing flow via `tangent.session_*`, `tangent.interview_question`, `tangent.synthesis_notes`, `tangent.block_draft`, `tangent.prose_revision`, and `tangent.output_render`. Provides a tight phase order, one copy/paste example, and pointers to deeper docs.
---

# Tangent Writing Flow

Use this skill when you need the full writing workflow, not a single bundled workflow.

## Phase order

1. `tangent.session_create`
2. `tangent.session_advance_phase` to `interview`
3. `tangent.interview_question`
4. advance to `synthesis`
5. `tangent.synthesis_notes`
6. advance to `drafting`
7. `tangent.block_draft`
8. advance to `revision`
9. `tangent.prose_revision`
10. advance to `output`
11. `tangent.output_render`
12. `tangent.session_get`

Reuse the same `roomID` across the whole flow.

## Copy/paste example

```text
Create one Tangent writing-flow room with `tangent.session_create`, reuse that same roomID through interview, synthesis, drafting, revision, and output, and use the exact sample payloads from `docs/manual-tests/writing-flow-e2e.md`. Wait for my submit/cancel response at each browser step.
```

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
