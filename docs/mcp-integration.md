# MCP integration

User-facing setup guide for wiring Tangent into an MCP-speaking agent.
For the architectural picture see [`architecture.md`](./architecture.md);
for the quick cross-workflow smoke pass see
[`manual-tests/workflow-smoke-tests.md`](./manual-tests/workflow-smoke-tests.md);
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
for the wizard flow see
[`manual-tests/wizard-e2e.md`](./manual-tests/wizard-e2e.md);
for raw curl probes see [`mcp-smoketest.md`](./mcp-smoketest.md).

## Install

The latest **git tag** is `v0.11.0`. Everything the foundation phase added —
resumable completion, multi-connection rooms, the definition registry, scoped
authorization, renderer trust classes, health, telemetry, and the database
operations — is **untagged**, so build from source to get it.

```bash
# Latest tagged release:
go install github.com/hollis-labs/tangent/cmd/tangent@v0.11.0

# Current behaviour (untagged):
git clone git@github.com:hollis-labs/tangent.git
cd tangent
make build         # produces ./tangent
```

Pin a tag when you want a stable surface; build from source when you need the
behaviour this document describes. Do not mix the two on one machine — see
"Managed runtime" below.

## Run

```bash
tangent
```

Default port is `7842`; override with `TANGENT_HTTP_PORT=7900` if it
collides. Expected startup logs:

```
level=INFO msg="loaded envelope types" count=<go-envelopes core catalog>
level=INFO msg="registered tangent envelope extensions" plugin=tangent count=<total registered kinds>
level=INFO msg="MCP server ready" http_url=http://127.0.0.1:7842/mcp sse_url=http://127.0.0.1:7842/sse
level=INFO msg="WebSocket bridge ready" ws_url=ws://127.0.0.1:7842/ws
level=INFO msg="tangent ready" url=http://127.0.0.1:7842/
level=INFO msg="tangent listening" addr=127.0.0.1:7842 dev_frontend_url=""
```

When an agent invokes a workflow tool, Tangent prints a room URL like
`http://127.0.0.1:7842/r/<roomID>` — open that in a browser to render
the envelope. (Tangent binds 127.0.0.1 IPv4-only; pasting `localhost`
also works on most systems but the canonical form matches the bind.)

## Managed runtime (single launch authority)

Tangent binds a fixed port and holds room state in `~/.tangent`, so it
must have exactly **one** launch authority on a given machine. Two
supervisors racing for `:7842` produce a bind failure and a split room
store, and the loser looks to agents like an MCP server that connects
and then dies.

Pick one of these and record it:

- **Unmanaged** — you start `tangent` yourself. Nothing else may start it.
- **Supervised** — a process manager (Cerberus, launchd, systemd,
  Docker, a shell supervisor) owns start/stop/restart. Then never run
  `tangent` by hand: ask the supervisor to restart it instead.

Whichever you pick, the *client* side is separate and may be plural:
several agents can connect to the one process at once. Adding a Claude
Code / Cursor / Codex entry does not create a second runtime — those
only dial an already-running server. Only add a *launch* entry once.

For this workspace the authority is recorded in
[`.agent-ops/project.yaml`](../.agent-ops/project.yaml) under `build.authority`
and `build.resource`; `deployment.type` says how the process is supervised.
Treat that file as the answer to "who starts Tangent here?" — and if a
skill, runbook, or agent prompt tells you to `cd` into the repo and run
`./tangent` while a supervisor owns it, that instruction is stale.

### Managed: Cerberus owns the process, Tether fronts the surface

This is how Tangent runs on the machine this repository is developed on, and
it is two separate things that fail separately.

**Cerberus owns the lifecycle.** The resource is `tangent-dev` (mode
`dev_session`, run from the workspace, port 7842), defined in
`~/.cerberus/projects/tangent.cerberus.yaml`:

```bash
cerberus resource status tangent-dev    # what the supervisor believes
cerberus resource deploy tangent-dev    # rebuild and restart — the way to restart
cerberus resource doctor  tangent-dev   # reads /readyz, not just supervisor state
```

Do not run `./tangent` by hand while this resource is running: you get two
launch authorities racing for `:7842` and a split room store. And remember that
`status: running` is supervisor bookkeeping, not a probe — reconcile it against
`/healthz` (see "Cold-start check" above).

**Tether fronts the tool surface.** The MCP upstream is declared in
`~/.tether/catalog/mcp-servers/tangent.yaml` (`transport: sse`, pointing at
`<base>/sse`, `enabled: true`); there is deliberately no Tether *launch-project*
entry, because Cerberus owns launching. An agent behind the gateway reaches
Tangent through `mux`:

```bash
mux mcp --proxy --servers torque,tesseract,cerberus   # default proxy: Tangent tools via mux_call
mux mcp --proxy --only tangent                        # native-flat: Tangent tools under their own names
```

Both modes work against the strict tool schemas. The default proxy's
`mux_call` writes the caller's W3C trace context into the arguments as
`_traceparent` (and `_tracestate`); Tangent strips exactly those keys at its
MCP boundary before schema validation and records the trace as an upstream
link on its telemetry (`CW-20260907-0022`). Any other unknown key is still
rejected.

Discovery is dynamic — the gateway lists whatever the upstream advertises — so
a gateway serving a cached list is the single most common way Tangent "loses"
tools. That is the `CATALOG_STALE` and `UPSTREAM_ABSENT` half of
[`mcp-smoketest.md`](./mcp-smoketest.md), and it is what the gated smoke run
checks:

```bash
TANGENT_SMOKE_ENV=1 make smoke
```

Four hops means four different fixes. Restart the process, restart the gateway,
refresh the catalog, or investigate one capability — the finding's leading mode
token says which.

### Direct local: no supervisor, no gateway

If none of the above is installed on your machine — which is the normal case
for anyone who just cloned the repo — Tangent is a plain binary and this is the
whole story:

```bash
make build && ./tangent          # serves http://localhost:7842/
claude mcp add --transport http tangent http://localhost:7842/mcp
```

Nothing else may start it. Override the port with `TANGENT_HTTP_PORT` if 7842
is taken, and point your agent's MCP URL at the same port. `make smoke` (with
no `TANGENT_SMOKE_ENV`) works here too: it boots its own copy on a reserved
port against a temp database and never touches your running instance.

The managed and direct paths are mutually exclusive per machine. Pick one,
record it, and do not let a `go install`ed binary and a workspace build both
claim the port.

### Cold-start check

After a reboot, a logout, or an MCP catalog refresh, confirm the
runtime is singular before debugging anything else:

```bash
curl -fsS http://127.0.0.1:7842/healthz          # -> {"status":"ok","probe":"liveness"}
lsof -nP -iTCP:7842 -sTCP:LISTEN                 # -> exactly one PID
curl -fsS http://127.0.0.1:7842/readyz | jq .    # -> "status":"ok" and five passing checks
```

If `lsof` shows more than one listener, or `healthz` answers but your
supervisor reports the resource stopped, you have two launch
authorities. Stop the unmanaged copy, not the supervised one.

`/healthz` proves only that the process is responding — it deliberately
touches no dependency, because the supervisor restarts on it and a probe
that failed on a slow query would restart a repairable process in a loop.
**A 200 from `/healthz` is not evidence that Tangent can serve.** `/readyz`
is: it checks the database, the schema version against the version the
running binary embeds, the definition registry, the renderer host, and the
delivery worker, and returns 503 with a per-check `operator_action` when any
of them fails. Per-kind answers are at `/healthz/capability/{kind}`, and the
same three reports are available over MCP as `tangent.health_report` for a
client that has no HTTP path to the host.

A common real finding here is a schema behind the binary: `/healthz` answers,
`/readyz` reports `migrations: fail`, and the fix is `tangent --migrate-only`
followed by a redeploy — not a restart loop.

**A supervisor's `status: running` is not a live probe.** It reports what the
supervisor believes it started, and a process that exited or was killed out
from under it can leave that belief in place while nothing is listening on the
port. Reconcile it against `/healthz` before trusting either — and note that a
`/healthz` answered by a *different* process on the same port is the same class
of mistake, which is why the launch-authority check above comes first.

Every non-passing check in a readiness or capability report carries a
`correlation` block: a `trace_id` and the tool that reads it. Passing it to
`tangent.telemetry_query` returns that check's or that kind's history —
when it started failing, how often, and what callers saw while it was failing —
from the durable `telemetry_events` table rather than from stderr, so the
answer survives a restart and a log rotation. The same tool takes an
`interaction_id` from a pending receipt and returns one invocation's whole
trail, and with `include_metrics` returns presentation and resolution latency,
reconnects, stale clients, delivery lag and retries, draft conflicts, renderer
failures, and capability denials. It carries no payload, participant text,
path, session, effect handle, or URL — by construction rather than by
filtering.

> **Fixed — legacy `/sse` sessions no longer go stale.** The server's 30s
> `ReadTimeout` used to tear down a quiet `GET /sse` stream and drop its MCP
> session, after which every later call answered `404 session not found` and a
> pooling gateway kept serving a dead session id. `longLivedMCPHandler` now
> clears the read deadline as well as the write one for `/mcp` and `/sse`, so
> an idle SSE subscription survives. `/mcp` (Streamable HTTP) is still the
> better choice for anything long-lived, and a `404 session not found` from an
> older deployment still means "reconnect", not "server down" — check that the
> serving process is the build on disk with `TANGENT_SMOKE_ENV=1 make smoke`
> before chasing it further.

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

## Durable HITL inbox

The HITL inbox is a persistent asynchronous operation surface, separate from
the room-backed `tangent.approval-queue` batch workflow. It exposes four MCP
tools:

- `tangent.hitl_enqueue` stores one approval or persistent-attention item and
  immediately returns its durable `item_id`, global FIFO `queue_sequence`,
  current `queue_position`, `/hitl`, and an item deep link.
- `tangent.hitl_get` retrieves the current item projection or its immutable
  terminal outcome.
- `tangent.hitl_await` waits 0–50,000 ms (30,000 ms by default). A timeout is a
  successful `await/timeout` projection and never changes item lifecycle.
- `tangent.hitl_withdraw` compare-and-sets one caller-owned item to
  `canceled/caller_withdrawn`; repeating the same withdrawal returns the
  original immutable outcome.

The caller scope is derived from `source.application_id` on enqueue and
`caller.application_id` thereafter, and the request shapes are unchanged; only
the scope Tangent derives from them changes spelling, from
`direct-loopback:<app>` to the canonical `standalone-local:<app>`. The old
spelling still reads as the same caller, with no data rewrite. **Those
partitions are advisory, not a security boundary** — see
[Room access and caller scope](#room-access-and-caller-scope). Agent labels,
MCP sessions, browser connections, and item URLs are not authority. Human
resolution, terminal retrieval, and downstream delivery are separate durable
facts; Tangent records the operator outcome but does not perform the caller's
business transition. The complete v1 request, evidence, response, and error shapes are
in [`contracts/hitl-inbox-v1.md`](./contracts/hitl-inbox-v1.md).
The repo-local
[`tangent-hitl-inbox` launcher](../.agents/skills/tangent-hitl-inbox/SKILL.md)
defaults to asynchronous enqueue and keeps its complete request template plus
direct Tangent and Tether native-flat copy/paste examples in one
[`request-shapes` reference](../.agents/skills/tangent-hitl-inbox/references/request-shapes.md).

The operator opens `http://127.0.0.1:7842/hitl` (or the returned item deep
link). Pending approvals and attention items remain in one durable FIFO order
while the operator inspects them; kind filters are projections of that ledger.
Each approval is committed immediately with Approve, Deny, or the corresponding
non-empty-note variant. Attention items remain pending until withdrawn or
committed as Acknowledged, optionally with a non-empty note or reply. An
acknowledgement records receipt only and never performs the caller's business
transition. There is no batch submit boundary, in-app toast, new-window
behavior, OS notification, or other notification side channel.
The page resynchronizes from SQLite after live revision hints, refresh, or a
reconnected browser, and reports a stale-tab conflict without replaying the
outcome.
The full operator exercise is in
[`manual-tests/hitl-inbox-e2e.md`](./manual-tests/hitl-inbox-e2e.md).

Tether discovers these names dynamically. The default proxy reaches them
through `mux_call`; native-flat (`mux mcp --proxy --only tangent`) exposes
them under their own names. `mux_call` injects `_traceparent` (and
`_tracestate`) into the arguments; Tangent accepts exactly those two keys as
gateway transport metadata, removes them before the strict v1 validation, and
otherwise keeps the schemas closed. Moving that injection into MCP `_meta` is
Tether's follow-up (`CW-20260907-0026`). The adapter retains the schema object
root, properties, required fields, and `$defs`, but omits top-level `allOf` and
`oneOf` while adapting schemas through its MCP SDK. Tangent's upstream schema
validation remains authoritative; callers must not treat the gateway's reduced
discovery schema as permission to send a shape the v1 contract rejects.
Native-flat forwarding does not itself authenticate request `source`; only a
separately verified gateway binding can establish authenticated caller
identity.

The shipped evidence discriminators are `markdown`, `text`, `diff`,
`tangent_reference`, and `artifact_ref`. Inline content is bounded and
sanitized. Tangent references are read-only durable projections. Artifact
metadata becomes previewable only through an explicitly registered
authority/capability adapter; a path, `file:` URI, or agent's ambient access is
never retrieval authority.

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

Verified working shape. Codex reads `~/.codex/config.toml`; declare the
server as a URL upstream (Streamable HTTP), with no `command` — Codex
dials the already-running process, it does not launch one:

```toml
[mcp_servers.Tangent]
url = "http://localhost:7842/mcp"
```

Per-tool approval is optional; without it Codex prompts on each call:

```toml
[mcp_servers.Tangent.tools."tangent.triage"]
approval_mode = "auto"
```

Repeat the block per tool you want pre-approved. `/sse` works as a
legacy fallback if your Codex build rejects Streamable HTTP.

## Verification (no agent required)

Confirm the MCP surface is up and advertises the current tool set:

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  | jq '.result.tools[].name'
```

Do not compare the answer against a number written in a document. This one has
been wrong repeatedly — it has moved from 25 to 46 across the tasks that grew
the surface — so the check that matters compares the deployment against the
build on disk rather than against prose:

```bash
make smoke                       # transports, parity, and one read-only tool call
TANGENT_SMOKE_ENV=1 make smoke   # …plus the live deployment, Cerberus, and the Tether gateway
```

`make smoke` prints the surface the shipped build advertises and its digest,
and the gated run reports any hop that disagrees as `CATALOG_STALE`, naming the
tools that are absent. See [`mcp-smoketest.md`](./mcp-smoketest.md) for the
four failure modes and the operator recipe.

The names group into room/workflow and session compatibility tools, generic
durable surface/interaction tools, the 4 HITL inbox operations
(`tangent.hitl_enqueue`, `_get`, `_await`, `_withdraw`), the definition-registry
diagnostics (`tangent.definition_registry_list`, `tangent.definition_get`,
`tangent.definition_registry_diagnostics`), and the operability probes
(`tangent.health_report`, `tangent.telemetry_query`,
`tangent.retention_status`). To list them from the artifact you are about to
deploy, boot it on a scratch database and port:

```bash
TANGENT_DB_PATH=$(mktemp -d)/t.db TANGENT_HTTP_PORT=17842 ./tangent &
curl -fsS -X POST http://localhost:17842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  | jq -r '.result.tools[].name' | sort
```

`tangent.list_workflows` returns the dispatcher-backed room workflow
definitions. It intentionally excludes the non-renderer `tangent.hitl-item`
interaction definition; clients discover the four HITL operations in the MCP
tool catalog and their named request/result/evidence definitions in the strict
schema bundle. Ask the build for both counts rather than trusting a number
here — `internal/smoke/docs_test.go` asserts that this document and the shipped
binary still agree.

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
  `closed` once only the persisted row remains. It says nothing about whether
  anyone is looking — see `connections`.
- `connections`: the room's Connection lifecycle, present while the room is
  live. `connections[]` lists each attached client (`connection_id`, `label`,
  `client_kind`, `role`, `attached_at`), and `resolver_lease` names the single
  connection whose submission may become terminal. Reported separately from
  interaction state on purpose: a room can be busy with nobody attached, and
  attached with nothing to answer. `tangent.session_list` carries the same fact
  compactly as `connection_count` and `resolver_lease`.
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

## Room access and caller scope

Implements [ADR 0004](adr/0004-caller-participant-and-room-access-authority.md).
Two things changed for integrators.

**A room URL is a locator, not a credential.** Tangent prints room URLs, returns
them as tool output, and expects them in agent transcripts. Knowing one grants
nothing. Authority lives in a browser participant session — an `HttpOnly`
cookie minted when a same-origin loopback browser opens any Tangent page —
which a pasted link does not carry.

**`/ws` requires that session, immediately and with no grace period.** A raw
WebSocket client (`websocat`, a hand-rolled script, a smoke-test harness) that
dials `ws://127.0.0.1:7842/ws?roomID=...` with no cookie now receives an
ordinary **403** instead of an upgrade. There is no flag to opt out, and the
upgrade is refused *before* the socket exists so the failure is a readable HTTP
status rather than a connection that closes silently.

To attach by hand, obtain a session first and present it:

```bash
# 1. Mint a session the way a browser does, and keep the cookie.
curl -fsS -c /tmp/tangent-cookies.txt \
  -H 'Accept: text/html' -H 'Sec-Fetch-Dest: document' -H 'Sec-Fetch-Site: none' \
  http://127.0.0.1:7842/ > /dev/null

# 2. Read the cookie value back out of the jar.
COOKIE="tangent_participant=$(awk '/tangent_participant/ {print $7}' /tmp/tangent-cookies.txt)"

# 3. Attach, presenting the cookie on the upgrade request.
websocat -H="Cookie: $COOKIE" \
  "ws://127.0.0.1:7842/ws?roomID=<roomID>&clientID=cli-1"
```

Opening the room URL in a browser does all of this for you, and remains the
supported path. The recipe above exists so a scripted smoke test can still
attach.

**Caller scope is `<authority>:<partition>`.** The authority is host-assigned
from admission facts and cannot be spelled by a caller; the partition is the
caller's declared application id. Every direct loopback caller is
`standalone-local:<app>`, or `standalone-local:anonymous` when it declares
nothing. Wire arguments named `caller.scope`, `requester_scope`, and
`owner_scope` are still accepted so shipped schemas do not break, but only
their partition half survives — the authority is reassigned by the host, and a
value naming a foreign authority becomes a partition of the local one.

> **`standalone-local` partitions are advisory, not a security boundary.**
> Any local caller can assert any partition, because the partition is the
> caller's own declared application id and nothing verifies it. Partitions are
> enforced **only across authorities**, where the prefix is host-assigned. They
> exist so a second agent's retry does not cancel the first agent's item, not
> so two agents can keep secrets from each other on the same machine. Do not
> build a trust assumption on one.

**What that changes for tools.** `tangent.session_list` and
`tangent.session_get` stay authority-wide — "show me all my rooms" is
unchanged. `tangent.session_close` and `tangent.surface_close` are now
partition-scoped, because closing dispositions another caller's pending human
work. The seventeen workflow tools and `tangent.session_advance` can no longer
push into a room another partition owns. Refusals are **403-shaped
(`ROOM_FORBIDDEN`) within an authority** and **404-shaped (`ROOM_NOT_FOUND`)
across authorities**; adapters must not collapse the two.

**Origin guard.** `/mcp`, `/sse`, and `/ws` now carry the same-origin guard
`/api/hitl/*` has always had. It permits header-less non-browser clients,
so MCP clients and the `curl` recipes in this document are unaffected; what it
refuses is a browser request that declares itself cross-site.

**Revoking sessions.** `tangent --revoke-participant-sessions` ends every
browser session and exits. Sessions have no idle expiry and an absolute 30-day
lifetime; every browser mints a fresh one on its next page load.

## Troubleshooting

- **`/ws` returns 403 / "the upgrade was refused".** The client presented no
  participant session. Open the room URL in a browser, or follow the cookie
  recipe in [Room access and caller scope](#room-access-and-caller-scope).
- **A tool returns `ROOM_FORBIDDEN` or `ROOM_NOT_FOUND` for a room you can
  see.** The room belongs to another caller partition (403-shaped) or another
  authority (404-shaped). `session_list` and `session_get` stay authority-wide;
  closing and advancing do not.
- **Port `7842` is in use.** Run `TANGENT_HTTP_PORT=7900 tangent` (or
  any free port) and update your agent's MCP URL to match.
- **Transport mismatch.** If the modern Streamable HTTP endpoint
  (`/mcp`) doesn't connect, try the SSE endpoint (`/sse`) — Tangent
  serves both. Don't mix them within one client config.
- **`claude mcp add` rejects `--transport http`.** Use
  `--transport sse` and the `/sse` URL. The two transports are
  equivalent for the current tool surface.
- **`go install` vs fresh-clone build.** `go install` is the simplest path to
  the latest tagged binary (`@v0.11.0`); build-from-source is required for the
  untagged foundation behaviour this document describes. The two are not
  API-compatible across releases — pin a tag until you have a reason not to,
  and never let both a `go install`ed binary and a workspace build supervise
  the same port.
- **Browser shows "No component registered for ..."** The envelope
  `type` on the wire is not one of Tangent's registered workflow kinds.
  The bundled tools pin the type for you; if you're calling the session
  substrate directly, make sure the envelope `type` matches a registered
  kind such as `tangent.triage`, `tangent.feedback`,
  `tangent.form-collect`, or
  `tangent.design-iteration`.
- **Room URL shows "waiting for envelope..."** A call carrying no
  `meta.roomID` gets a fresh room; a call naming an existing room reuses it. A
  room with no active envelope idles at that message until one is advanced into
  it — that is the room waiting, not a hang and not a lost call. If you were
  expecting an answer, the interaction is still durable: retrieve it with
  `tangent.interaction_get` or wait again with `tangent.interaction_await`.
- **A tool returned `"status":"pending"` instead of an answer.** That is a
  success, not a failure. The inline wait window elapsed with nobody at the
  browser, so the call returned a durable handle rather than erroring. Resolve
  the room, then retrieve the outcome — or re-issue the identical original
  invocation, which returns the same handle rather than creating a second
  interaction. See
  [`room-workflow-completion.md`](./room-workflow-completion.md).
- **Two Tangent processes point at the same DB.** SQLite WAL mode
  tolerates concurrent readers and writers, but sharing one
  `~/.tangent/tangent.db` between multiple long-lived Tangent processes
  is still a coordination choice. If you want isolation for testing, set
  `TANGENT_DB_PATH` per process.

For more, see the manual e2e recipe's troubleshooting section.
