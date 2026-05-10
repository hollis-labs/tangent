# MCP integration

User-facing setup guide for wiring Tangent into an MCP-speaking agent.
For the architectural picture see [`architecture.md`](./architecture.md);
for the full Claude Code e2e walkthrough see
[`manual-tests/triage-e2e.md`](./manual-tests/triage-e2e.md), for the
writing flow see
[`manual-tests/writing-flow-e2e.md`](./manual-tests/writing-flow-e2e.md),
and for the whiteboard flow see
[`manual-tests/whiteboard-e2e.md`](./manual-tests/whiteboard-e2e.md); for the
spreadsheet-review flow see
[`manual-tests/spreadsheet-review-e2e.md`](./manual-tests/spreadsheet-review-e2e.md);
for the approval-queue flow see
[`manual-tests/approval-queue-e2e.md`](./manual-tests/approval-queue-e2e.md);
for the diff-review flow see
[`manual-tests/diff-review-e2e.md`](./manual-tests/diff-review-e2e.md);
for the file-picker flow see
[`manual-tests/file-picker-e2e.md`](./manual-tests/file-picker-e2e.md);
for the progress-panel flow see
[`manual-tests/progress-panel-e2e.md`](./manual-tests/progress-panel-e2e.md);
for the dashboard flow see
[`manual-tests/dashboard-e2e.md`](./manual-tests/dashboard-e2e.md);
for the generalized form flow see
[`manual-tests/form-collect-e2e.md`](./manual-tests/form-collect-e2e.md);
for raw curl probes see [`mcp-smoketest.md`](./mcp-smoketest.md).

## Install

```bash
go install github.com/hollis-labs/tangent/cmd/tangent@v0.11.0
```

Or build from source:

```bash
git clone git@github.com:hollis-labs/tangent.git
cd tangent
make build         # produces ./tangent
```

## Run

```bash
tangent
```

Default port is `7842`; override with `TANGENT_HTTP_PORT=7900` if it
collides. Expected startup logs:

```
level=INFO msg="loaded envelope types" count=26
level=INFO msg="registered tangent envelope extensions" plugin=tangent count=38
level=INFO msg="MCP server ready" http_url=http://127.0.0.1:7842/mcp sse_url=http://127.0.0.1:7842/sse
level=INFO msg="WebSocket bridge ready" ws_url=ws://127.0.0.1:7842/ws
level=INFO msg="tangent ready" url=http://127.0.0.1:7842/
level=INFO msg="tangent listening" addr=127.0.0.1:7842 dev_frontend_url=""
```

When an agent invokes a workflow tool, Tangent prints a room URL like
`http://127.0.0.1:7842/r/<roomID>` — open that in a browser to render
the envelope. (Tangent binds 127.0.0.1 IPv4-only; pasting `localhost`
also works on most systems but the canonical form matches the bind.)

## Claude Code

Verified against the `claude` CLI as of **2026-05-08**:

```bash
claude mcp add --transport http tangent http://localhost:7842/mcp
```

If your `claude` rejects `--transport http` (older versions), fall back
to SSE — Tangent serves both:

```bash
claude mcp add --transport sse tangent http://localhost:7842/sse
```

Confirm it registered:

```bash
claude mcp list
# tangent should appear with the URL above
```

Then in any Claude Code session, ask Claude to use one of the bundled
tools: `tangent.triage`, `tangent.feedback`, `tangent.form-collect`,
`tangent.design-iteration`, `tangent.whiteboard`,
`tangent.spreadsheet-review`, `tangent.approval-queue`,
`tangent.diff-review`, `tangent.file-picker`,
`tangent.progress-panel`, `tangent.dashboard`, or the writing
sequence via `tangent.session_*`, `tangent.interview_question`,
`tangent.synthesis_notes`, `tangent.block_draft`,
`tangent.prose_revision`, and `tangent.output_render`. Tangent prints a
room URL, the browser resolves the workflow, and Claude receives the
structured response back. Full walkthroughs live in the manual recipes
under [`docs/manual-tests/`](./manual-tests/).

## Cursor

Cursor reads MCP servers from a JSON config at `~/.cursor/mcp.json`.
Add an entry pointing at Tangent:

```json
{
  "mcpServers": {
    "tangent": {
      "url": "http://localhost:7842/mcp"
    }
  }
}
```

> **Note.** Cursor's MCP config schema has shifted across versions
> (some versions key on `command`/`args` for stdio servers, others on
> `url` for HTTP). The shape above matches Cursor's current docs for
> HTTP MCP servers; if your version differs, consult Cursor's MCP docs
> and report back what works so we can document it.

Restart Cursor after editing the file.

## Codex

Codex's MCP CLI shape was not verified for v0.1.0. Once the CLI is
known, the registration shape will look approximately like:

```bash
# placeholder — verify against Codex's current MCP docs
codex mcp add tangent http://localhost:7842/mcp
```

Until verified, follow Codex's upstream MCP setup guide and point it at
`http://localhost:7842/mcp` (Streamable HTTP) or
`http://localhost:7842/sse` (legacy SSE). If you confirm a working
shape, please contribute it back.

## Verification (no agent required)

Confirm the MCP surface is up and advertises the current tool set:

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  | jq '.result.tools[].name'
```

Expected:

```
"tangent.design-iteration"
"tangent.dashboard"
"tangent.diff-review"
"tangent.feedback"
"tangent.file-picker"
"tangent.progress-panel"
"tangent.form-collect"
"tangent.interview_question"
"tangent.list_workflows"
"tangent.block_draft"
"tangent.prose_revision"
"tangent.output_render"
"tangent.approval-queue"
"tangent.session_advance"
"tangent.session_advance_phase"
"tangent.session_close"
"tangent.session_create"
"tangent.session_get"
"tangent.session_list"
"tangent.session_set_phase_output"
"tangent.spreadsheet-review"
"tangent.synthesis_notes"
"tangent.triage"
"tangent.whiteboard"
```

One-shot probes for the new surfaces:

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tangent.session_create","arguments":{"title":"doc-smoke"}}}'

curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tangent.session_list","arguments":{"active_only":true}}}'

curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"tangent.session_advance_phase","arguments":{"roomID":"<room-id>","to_phase":"drafting","reason":"move into drafting"}}}'

curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"tangent.session_set_phase_output","arguments":{"roomID":"<room-id>","phase":"drafting","key":"outline","value":{"title":"V1"}}}}'
```

`tangent.session_get` now returns both room lifecycle status and the
workflow-neutral phase substrate:

- `status` / legacy `phase`: `active` while a room is live in memory,
  `closed` once only the persisted row remains.
- `current_phase`: the room's current workflow phase ID.
- `phases_visited`: append-only ordered phase history. Jumping back to a
  prior phase appends that phase again rather than rewriting history.
- `phase_outputs`: a map keyed by phase ID. Each value is a versioned
  blob shaped like `{"version":1,"data":{...}}`.
- `spreadsheet_review`: when a room has persisted spreadsheet-review
  state, a dedicated projection with `table_id`, canonical `columns`
  and `rows`, normalized `query_state`, `notes`, `updated_at`,
  room-backed `saved_views`, persisted `selected_row_ids`,
  normalized `selected_rows`, optional bulk `action_id`, and
  lightweight CSV `export_refs`.
- `diff_review`: when a room has persisted diff-review state, a
  dedicated projection with `review_id`, canonical `files`,
  `current_file`, `filter_state`, normalized `decisions`, freeform
  `comments`, durable `summary`, artifact-backed `before_ref` /
  `after_ref`, and lightweight summary `export_refs`.
- `file_picker`: when a room has persisted file-picker state, a
  dedicated projection with `picker_id`, canonical `browse_roots`,
  durable `selected_refs`, normalized `query_state`, append-only
  `selection_revisions`, accepted `submission_summary`, and stable
  artifact-ref `handoff` payloads for downstream workflows.
- `progress_panel`: when a room has persisted progress-panel state, a
  dedicated projection with `panel_id`, canonical `items`, append-only
  `updates`, derived `checkpoints`, and concise summary fields such as
  `current_status`, `last_checkpoint_label`, and `completion_result`.
- `dashboard`: when a room has persisted dashboard state, a dedicated
  projection with `dashboard_id`, canonical `tiles`, normalized
  `layout`, reusable `saved_layouts`, normalized `query_state`,
  accepted `snapshot_history`, and concise `export_state` metadata for
  downstream handoff.
- `approval_queue`: when a room has persisted approval-queue state, a
  dedicated projection with `queue_id`, canonical `items`,
  `current_index`, normalized `decisions`, queue `notes`, `updated_at`,
  append-only `audit_trail`, and lightweight audit `export_refs`.
- `whiteboard`: when a room has persisted board state, a dedicated
  projection with `board_id`, `scene_snapshot`, referenced `assets`,
  `export_refs`, `notes`, `updated_at`, and append-only
  `revision_history` metadata.
- `form_collect`: when a room has persisted generalized form state, a
  dedicated projection with `form_id`, canonical `schema`, normalized
  `answers`, room-backed `saved_drafts`, `templates`, lightweight
  `attachment_refs`, and a durable `submission_summary`.
- `final_output`: the persisted final markdown artifact once
  `tangent.output_render` runs.

For persisted spreadsheet review, Tangent treats agent-provided rows as
canonical table content and keeps `session_get` lightweight by
projecting normalized query state, named saved views, selected rows,
and export metadata directly off the room substrate rather than
synthesizing them from browser-local state.

For the shipped v0.4 whiteboard workflow, Tangent treats the full scene
snapshot as canonical room state but expects image/file inputs to be
referenced through lightweight asset metadata rather than inlined base64
payloads. `session_get` intentionally stays lightweight for agents: it
surfaces revision metadata, not every historical scene blob. The room UI
can still reopen or continue from older revisions because full revision
snapshots are retained on the room for the whiteboard browser itself.

The bundled writing workflow's canonical phase sequence is:

1. `interview`
2. `synthesis`
3. `drafting`
4. `revision`
5. `output`

Jump-backs are explicit. If a revision pass uncovers a drafting issue,
call `tangent.session_advance_phase` back to `drafting`, resolve the new
`tangent.block_draft` envelope, then advance forward again.

For deeper probes (calling a workflow, expected error frames) see
[`mcp-smoketest.md`](./mcp-smoketest.md) and
[`manual-tests/multi-envelope-session-e2e.md`](./manual-tests/multi-envelope-session-e2e.md).

## Troubleshooting

- **Port `7842` is in use.** Run `TANGENT_HTTP_PORT=7900 tangent` (or
  any free port) and update your agent's MCP URL to match.
- **Transport mismatch.** If the modern Streamable HTTP endpoint
  (`/mcp`) doesn't connect, try the SSE endpoint (`/sse`) — Tangent
  serves both. Don't mix them within one client config.
- **`claude mcp add` rejects `--transport http`.** Use
  `--transport sse` and the `/sse` URL. The two transports are
  equivalent for the current tool surface.
- **`go install` vs fresh-clone build.** `go install` is the simplest
  path for a stable v0.10.0 binary; build-from-source is required if you
  want unreleased fixes from `main`. The two are not API-compatible
  across releases — pin via `@v0.10.0` until you have a reason not to.
- **Browser shows "No component registered for ..."** The envelope
  `type` on the wire is not one of Tangent's registered workflow kinds.
  The bundled tools pin the type for you; if you're calling the session
  substrate directly, make sure the envelope `type` matches a registered
  kind such as `tangent.triage`, `tangent.feedback`,
  `tangent.form-collect`, or
  `tangent.design-iteration`.
- **Room URL hangs at "waiting for envelope..."** Each MCP call gets a
  fresh room unless you deliberately reuse one through
  `tangent.session_*`; stale URLs from a prior call will sit idle until
  a new envelope is advanced into that room.
- **Two Tangent processes point at the same DB.** SQLite WAL mode
  tolerates concurrent readers and writers, but sharing one
  `~/.tangent/tangent.db` between multiple long-lived Tangent processes
  is still a coordination choice. If you want isolation for testing, set
  `TANGENT_DB_PATH` per process.

For more, see the manual e2e recipe's troubleshooting section.
