# Workflow smoke tests

Quick operator runbook for confirming each Tangent workflow still opens,
accepts one minimal interaction, and returns a structured response.

Use this when you want broad confidence across the shipped workflow
surface without running every deep e2e recipe.

## Preflight

1. Start Tangent locally:

```bash
make build
./tangent
```

2. Confirm the server starts cleanly and exposes MCP on
   `http://127.0.0.1:7842/mcp`.
3. Check the three health probes before blaming a workflow for anything:

```bash
curl -fsS http://127.0.0.1:7842/healthz              # liveness — touches nothing
curl -fsS http://127.0.0.1:7842/readyz | jq .        # readiness — must be "ok"
curl -fsS http://127.0.0.1:7842/healthz/capability   # which kinds can be served
```

   A 200 from `/healthz` is **not** evidence Tangent can serve. `/readyz` is.
   `/healthz/capability/{kind}` answers for one kind.

4. Confirm the process is serving the build you think it is:

```bash
make smoke                       # transports, parity, one read-only tool call
TANGENT_SMOKE_ENV=1 make smoke   # …plus the live deployment, Cerberus, and Tether
```

   A `CATALOG_STALE` finding here means the deployment is behind the artifact on
   disk, and every workflow below will then smoke against the wrong surface.
   See [`../mcp-smoketest.md`](../mcp-smoketest.md).

5. Connect one MCP-speaking agent to Tangent.
6. Keep a browser ready. Tangent logs the room **id**; the tool response
   carries the room URL, or construct `http://127.0.0.1:7842/r/<roomID>`.

The durable HITL inbox is the exception: open `/hitl` directly and retain the
handle returned by `tangent.hitl_enqueue`; it does not create a room.

## Operator rules

- Use the linked e2e doc's sample payload literally for each workflow.
  Do not rely on a loose natural-language prompt when the workflow needs
  seeded data.
- Run bundled workflows in fresh rooms unless the linked doc explicitly
  says to reuse one room across multiple phases.
- Browser tabs are replaceable attachments. Navigating away or closing a tab
  leaves pending work unresolved; reopening the room URL resumes it. A caller
  whose inline wait elapses receives a **successful pending receipt**, not an
  error — resolving the room afterwards still produces the outcome. Use the
  workflow's explicit Cancel control when cancellation is intended.
- Several tabs may watch one room at once. The connection strip above the
  envelope shows how many clients are attached and which one holds the
  **resolver lease** — only that tab's submit becomes the answer. A second tab
  opens as an observer; press **Take over** there before answering. Refreshing
  a tab is not a second tab: it replaces its own socket and keeps the lease.
  Submitting from an observer is refused with a visible message and leaves the
  envelope on screen, which is the expected behavior, not a bug.

## Validation affordances to check in every workflow

Two different things can stop a submission, and they read differently on
purpose. See [`room-validation-affordances.md`](../room-validation-affordances.md)
for the conventions behind them.

- **Blocked** — you have not finished. Amber text sits *beside* the terminal
  button and names what remains in the workflow's own words, with a
  **Go to <control>** button that scrolls to and focuses the control. There is
  never a dead terminal button with nothing next to it.
- **Refused** — you submitted and the server declined. Red text in the
  connection strip, starting `Not submitted:`, with the envelope restored so
  you can answer again.

While walking each workflow below, confirm:

1. A control that becomes required after a choice (a defer reason after Defer,
   a revision note after Revise, an inline edit after Inline edit) grows a
   visible `required` badge at that moment — not a placeholder, not a
   relabelled string.
2. The terminal button, when inert, has an adjacent explanation naming the
   specific item / file / step / field that is outstanding — not just a count.
3. **Go to <control>** actually moves you: it switches queue item, wizard step,
   or file, scrolls the control into view, and leaves the caret in it.
4. An optional note is labelled as optional and does not read as a mandate; a
   formal reason says it is the recorded reason.
5. No error is rendered in green.

Workflows with no terminal gate by design — `tangent.synthesis-notes` and
`tangent.output-render` (read-only acknowledgements), `tangent.dashboard` and
`tangent.spreadsheet-review` (submitting an unchanged view is a legitimate
outcome) — have no blocked state to check. That is correct, not a gap.

## Payload-sensitive workflows

These workflows are easy to launch incorrectly if the agent improvises
the envelope shape:

- `tangent.diff-review` needs seeded `files`, and each file needs a
  stable `id`. Hunks also need stable `id` values if you want per-hunk
  review.
- `tangent.file-picker` needs seeded `browse_roots` plus `files`.
  Without `files`, the UI can open with no selectable candidates.
- `tangent.progress-panel` needs seeded `items`. `updates`,
  `checkpoints`, and `summary` are optional but recommended for a useful
  smoke pass.
- `tangent.dashboard` needs seeded `tiles`. `layout` and
  `saved_layouts` are strongly recommended or the workflow has little to
  exercise.
- `tangent.wizard` needs real `steps` objects plus a `current_step_id`
  that matches one of those `step_id` values. If a step exposes
  branches, every `target_step_id` must also point at a real step.

## Shared pass criteria

Every workflow smoke test should confirm these basics:

- Tangent logs a room URL and the browser renders the expected workflow.
- The UI accepts one realistic minimal interaction without obvious errors.
- Submitting or completing the workflow returns a structured response to
  the agent.
- For room-backed workflows, reopening the same room shows the accepted
  canonical state rather than a blank screen.
- No room-backed call returns an MCP error because a human took too long. A
  wait that runs past 45 seconds returns a successful `"status":"pending"`
  receipt with a durable handle, and the same call retried with the identical
  payload later returns the operator's answer. See
  [`room-workflow-completion-e2e.md`](./room-workflow-completion-e2e.md).

## Bundled workflows

Use the linked e2e doc for the exact sample payload. For smoke coverage,
you only need the minimal interaction listed here, but still launch the
workflow with the full seeded example from the linked doc.

| Workflow | Launch reference | Minimal interaction | Pass if |
| --- | --- | --- | --- |
| `tangent.triage` | [`triage-e2e.md`](./triage-e2e.md) | Fill the required triage fields and submit once. | Agent receives a `data` response and the room resolves cleanly. |
| `tangent.feedback` | [`feedback-e2e.md`](./feedback-e2e.md) | Answer the feedback prompt set and submit. | Agent receives the structured questionnaire response with your answers. |
| `tangent.design-iteration` | [`design-iteration-e2e.md`](./design-iteration-e2e.md) | Review one iteration, choose one action, and submit. | Agent receives the chosen action and the selected iteration state. |
| `tangent.whiteboard` | [`whiteboard-e2e.md`](./whiteboard-e2e.md) | Add or move one object on the canvas, then submit. | Agent receives a whiteboard response and reopening the room shows the saved scene. |
| `tangent.spreadsheet-review` | [`spreadsheet-review-e2e.md`](./spreadsheet-review-e2e.md) | Filter or select one row, add one note or action, then submit. | Agent receives the selected row state and reopening shows the accepted review state. |
| `tangent.form-collect` | [`form-collect-e2e.md`](./form-collect-e2e.md) | Fill the required fields and submit once. | Agent receives the structured form payload and the room can reopen with accepted values. |
| `tangent.approval-queue` | [`approval-queue-e2e.md`](./approval-queue-e2e.md) | Make one decision on one queued item and submit. | Agent receives the decision payload and reopening shows the accepted queue state. |
| `tangent.diff-review` | [`diff-review-e2e.md`](./diff-review-e2e.md) | Review one file or hunk, record one decision, and submit. | Agent receives the diff-review payload and reopening shows the accepted decision set. |
| `tangent.file-picker` | [`file-picker-e2e.md`](./file-picker-e2e.md) | Browse or filter once, select one file, and submit. | Agent receives the selected artifact refs and reopening preserves the accepted picker state. |
| `tangent.progress-panel` | [`progress-panel-e2e.md`](./progress-panel-e2e.md) | Add one update, checkpoint, or log entry, then submit. | Agent receives the appended progress state and reopening shows the accepted timeline. |
| `tangent.dashboard` | [`dashboard-e2e.md`](./dashboard-e2e.md) | Open the dashboard, trigger one refresh or update action, then submit. | Agent receives the updated dashboard payload and reopening shows the accepted layout/state. |
| `tangent.wizard` | [`wizard-e2e.md`](./wizard-e2e.md) | Fill the first step, choose any branch if present, save once, then complete the final step. | Agent sees partial progress first, then a submitted completion response; reopening shows accepted wizard progress. |

This table lists the bundled room workflows. It is a runbook, not the registry:
ask `tangent.list_workflows` for what the build actually dispatches, and
`make smoke` for the whole tool surface. `internal/smoke/docs_test.go` fails if
this table and the shipped build disagree.

## Beyond the workflow table

Two recipes are not workflow smoke and are easy to skip. Do not skip them.

- [`room-workflow-completion-e2e.md`](./room-workflow-completion-e2e.md) —
  resumable completion: the pending receipt, reconnect, restart, and recovery.
  This is the contract every workflow above now depends on.
- [`renderer-sandbox-e2e.md`](./renderer-sandbox-e2e.md) — CSP and renderer
  sandboxing. **There is no browser in CI**, so this manual recipe is the only
  place any of it is actually observed being enforced. A green `make test` is
  evidence the policy is *emitted*, never that a browser honoured it.

## Durable HITL inbox

Use the repo-local
[`tangent-hitl-inbox` skill](../../.agents/skills/tangent-hitl-inbox/SKILL.md)
and [`hitl-inbox-e2e.md`](./hitl-inbox-e2e.md). Enqueue returns immediately;
retain its handle, then resolve in `/hitl` and retrieve the immutable outcome
through a later Get or Await call.

| Surface | Minimal interaction | Pass if |
| --- | --- | --- |
| `tangent.hitl_*` + `/hitl` | Enqueue approval and attention items from different applications, inspect evidence, resolve one, restart, retrieve it by handle, and withdraw the other. | FIFO and deep links survive reconnect/restart; all five evidence families remain readable; no room, toast, new window, OS notification, or downstream business transition is created. |

## Writing flow

Run this as one room across the canonical writing phases. Reuse the same
`roomID` for every phase. Use [`writing-flow-e2e.md`](./writing-flow-e2e.md)
for the exact sample calls and ignore any phase-specific docs that begin
with their own fresh-room setup.

| Phase tool | Launch reference | Minimal interaction | Pass if |
| --- | --- | --- | --- |
| `tangent.session_create` | [`writing-flow-e2e.md`](./writing-flow-e2e.md) | Create one room for the writing flow. | Tangent returns a room ID and the room URL opens. |
| `tangent.interview_question` | [`interview-question-e2e.md`](./interview-question-e2e.md) | Answer one long-form prompt and submit. | Agent receives the answer payload and the room advances cleanly. |
| `tangent.synthesis_notes` | [`synthesis-notes-e2e.md`](./synthesis-notes-e2e.md) | Review the synthesis handoff and click `Continue`. | Agent receives the continue response and the room remains usable. |
| `tangent.block_draft` | [`block-draft-e2e.md`](./block-draft-e2e.md) | Accept one draft block or make one inline edit, then submit. | Agent receives the accepted block response and the room stores the accepted draft block. |
| `tangent.prose_revision` | [`prose-revision-e2e.md`](./prose-revision-e2e.md) | Accept or reject one suggestion and submit. | Agent receives the per-suggestion decision payload. |
| `tangent.output_render` | [`writing-flow-e2e.md`](./writing-flow-e2e.md) | Verify the final output renders, then click `Done`. | Agent receives the final output response and the room retains final output metadata. |

## Close-out

After finishing the smoke pass:

1. Run one `tangent.session_get` on any room-backed workflow you
   exercised heavily.
2. Confirm the persisted projection for that workflow is populated.
3. Close any rooms you created if you do not want them left in the local
   room list.

## When to use the deeper recipes

Use the full e2e docs instead of this smoke pass when you need to verify
workflow-specific behavior such as:

- local draft recovery
- reopen and revision history
- export artifacts
- branch navigation
- jump-back phase transitions
- summary or audit payload details
- completion, waiting, and recovery semantics
  ([`room-workflow-completion-e2e.md`](./room-workflow-completion-e2e.md))
