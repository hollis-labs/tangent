# MCP integration

User-facing setup guide for wiring Tangent into an MCP-speaking agent.
For the architectural picture see [`architecture.md`](./architecture.md);
for the full Claude Code e2e walkthrough see
[`manual-tests/triage-e2e.md`](./manual-tests/triage-e2e.md); for raw
curl probes see [`mcp-smoketest.md`](./mcp-smoketest.md).

## Install

```bash
go install github.com/hollis-labs/tangent/cmd/tangent@v0.2.0
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
level=INFO msg="registered tangent envelope extensions" plugin=tangent count=29
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
tools: `tangent.triage`, `tangent.feedback`, or
`tangent.design-iteration`. Tangent prints a room URL, the browser
resolves the workflow, and Claude receives the structured response back.
Full walkthroughs live in the manual recipes under
[`docs/manual-tests/`](./manual-tests/).

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

Confirm the MCP surface is up and advertises the v0.2 tools:

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  | jq '.result.tools[].name'
```

Expected:

```
"tangent.design-iteration"
"tangent.feedback"
"tangent.list_workflows"
"tangent.session_advance"
"tangent.session_advance_phase"
"tangent.session_close"
"tangent.session_create"
"tangent.session_get"
"tangent.session_list"
"tangent.session_set_phase_output"
"tangent.triage"
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
  equivalent for v0.1's tool surface.
- **`go install` vs fresh-clone build.** `go install` is the simplest
  path for a stable v0.2.0 binary; build-from-source is required if you
  want unreleased fixes from `main`. The two are not API-compatible
  across releases — pin via `@v0.2.0` until you have a reason not to.
- **Browser shows "No component registered for ..."** The envelope
  `type` on the wire is not one of Tangent's registered workflow kinds.
  The bundled tools pin the type for you; if you're calling the session
  substrate directly, make sure the envelope `type` matches a registered
  kind such as `tangent.triage`, `tangent.feedback`, or
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
