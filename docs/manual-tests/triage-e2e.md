# Triage e2e — manual test recipe

End-to-end smoke for `tangent.triage` driven by a real Claude Code
session. Verifies the multi-envelope room acceptance gate: a real LLM client calls the
MCP tool, the human responds in the browser, and the LLM receives a
structured response payload.

For a fully-automated mock-driven equivalent (no LLM involved), use
`scripts/triage-mock-call.mjs` — it spawns `./tangent` as a subprocess
and drives a real HTTP MCP call + real WebSocket round-trip from
outside the binary. CI does not run this script today; the canonical
in-process gate is `internal/server/integration_test.go`
(`TestIntegration_TriageRoundTrip`), which uses the SDK's in-memory
transports. The mock script is the human-runnable parity check for
the same loop. The recipe below is the real-LLM verification.

## Prerequisites

- A local clone of the repo.
- `make build` produced `./tangent` in the repo root.
- Claude Code installed (`claude` CLI on `$PATH`).
- A modern browser pointing at `localhost`.
- Port `7842` free (set `TANGENT_HTTP_PORT=...` to relocate; the WS,
  SPA, and MCP all share the port).

## 1. Boot Tangent

```bash
cd tangent  # your clone of the repo
./tangent
```

Expected log lines (timestamps elided; key=value attributes match the
slog text handler Tangent ships with):

```
level=INFO msg="loaded envelope types" count=<go-envelopes core catalog size>
level=INFO msg="registered tangent envelope extensions" plugin=tangent count=<total registered kinds, core + Tangent>
level=INFO msg="MCP server ready" http_url=http://127.0.0.1:7842/mcp sse_url=http://127.0.0.1:7842/sse
level=INFO msg="WebSocket bridge ready" ws_url=ws://127.0.0.1:7842/ws
level=INFO msg="tangent ready" url=http://127.0.0.1:7842/
level=INFO msg="tangent listening" addr=127.0.0.1:7842 dev_frontend_url=""
```

Note: the MCP triage tool logs `triage room created room=<roomID>
envelope=<id>` the moment a call arrives. It logs the room **id**, not a URL —
ADR 0002 §8 keeps assembled locators out of log lines. The tool *response*
carries the room URL back to the caller; from the log alone, construct
`http://127.0.0.1:7842/r/<roomID>`.

## 2. Wire Tangent into Claude Code

Tangent serves MCP over HTTP at `http://127.0.0.1:7842/mcp` (Streamable
HTTP) or `http://127.0.0.1:7842/sse` (legacy SSE for older clients).

Add it to Claude Code's MCP catalog. The exact `claude mcp add` syntax
depends on your `claude` version; both shapes below worked at the time
of writing:

```bash
# Modern (Streamable HTTP)
claude mcp add --transport http tangent http://127.0.0.1:7842/mcp

# Legacy (SSE — fall back to this if the modern call rejects)
claude mcp add --transport sse tangent http://127.0.0.1:7842/sse
```

Confirm the registration with `claude mcp list` — `tangent` should
appear with the URL above. If your CLI version differs, edit
`~/.config/claude/claude_config.json` (path may vary) to add an entry
manually under `mcpServers`.

## 3. Fire a triage call from Claude Code

Open a fresh Claude Code session in any working directory:

```bash
claude
```

Send something like:

> Use the tangent.triage tool to triage these items: `["read the new
> draft", "respond to the staging-env email", "delete the cw-3 branch"]`.

Claude will assemble a `tangent.triage` envelope and call the tool. The
Tangent log prints the room URL (see step 1).

## 4. Open the browser and triage

Paste the `http://127.0.0.1:7842/r/<roomID>` URL from the Tangent log
into a browser tab.

> **The browser step is load-bearing, not a convenience.** Since
> [ADR 0004](../adr/0004-caller-participant-and-room-access-authority.md) the
> `/ws` upgrade requires a participant session, immediately and with no grace
> period: knowing the room UUID grants nothing. Loading the page is what mints
> that session — an `HttpOnly` cookie the tab then presents on the upgrade — so
> a raw WebSocket client dialing `ws://127.0.0.1:7842/ws?roomID=...` with no
> cookie receives a 403 rather than an envelope. If you are scripting this
> step, use the cookie recipe in
> [`../mcp-integration.md`](../mcp-integration.md#room-access-and-caller-scope).

The page renders:

- A `<Triage>` card with the prompt and three items.
- Three buttons per item — Accept, Backlog, Delete — and a
  Submit / Cancel pair in the footer.

Click one decision per item. Submit becomes enabled when all items have
a decision; click it.

## 5. Verify the response in Claude Code

Claude receives the response as a `data`-kind envelope response carrying
the per-item decisions:

```json
{
  "v": 1,
  "envelopeId": "<the id Claude generated>",
  "kind": "data",
  "status": "submitted",
  "payload": {
    "decisions": [
      {"itemId": "item-0", "action": "accept"},
      {"itemId": "item-1", "action": "backlog"},
      {"itemId": "item-2", "action": "delete"}
    ]
  },
  "completedAt": "2026-...Z"
}
```

Claude should narrate the triage decisions back. End-to-end: under 30s,
no errors in either log.

## Troubleshooting

- **"Tangent didn't print a room URL"** — confirm the MCP call actually
  reached Tangent. Hit `curl -fsS -X POST http://127.0.0.1:7842/mcp -H
  'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'`
  to ensure the MCP surface is up.
- **"`claude mcp add` is rejecting `--transport http`"** — fall back to
  `sse` (Tangent serves both).
- **"Browser shows 'No component registered for ...'"** — the envelope
  type field on the wire isn't `tangent.triage`. Re-check Claude's call
  shape; the MCP tool's input schema pins the type.
- **"Port 7842 is in use"** — set `TANGENT_HTTP_PORT=7900` (or any free
  port) and rerun. Update the `claude mcp add` URL to match.
- **"WS connects but never receives an envelope"** — confirm the room
  ID in the URL matches what Tangent printed. A call carrying no
  `meta.roomID` gets a fresh room; a call naming an existing room reuses it
  (`room reused` in the log). A room with no active envelope shows
  "waiting for envelope..." until one is advanced into it — that is the room
  idling, not a hang.
- **"The WebSocket upgrade returns 403"** — the client presented no
  participant session. A browser gets one automatically by loading the
  room page; a scripted client has to mint and present the cookie, per
  [`../mcp-integration.md`](../mcp-integration.md#room-access-and-caller-scope).
  This is deliberate: the room UUID is a locator, and it is printed in
  logs and pasted into transcripts precisely because it is not a
  credential.
- **"The tab strip's close button says it is not authorized"** — the
  room belongs to a different caller partition. `session_close` is
  partition-enforcing; listing and reading rooms are not.
- **"Submit is disabled"** — by design until every item has a decision.
  Pick one of Accept / Backlog / Delete for each row.
- **"My LLM hallucinated a custom envelope shape"** — `tangent.triage`'s
  data shape is permissive (strings or objects). The component
  normalizes both. If decisions arrive with synthesized `item-N` ids,
  it's because the agent provided unstructured items — that's fine.

## Mock-driven equivalent

`scripts/triage-mock-call.mjs` — Node script that POSTs a `tangent.triage`
MCP call and replies through the WS bridge. No LLM needed. Use this for
CI smoke and for when you want to verify the wire shape without
involving Claude:

```bash
./tangent &
node scripts/triage-mock-call.mjs
# expects exit 0 on round-trip success
```

The Go integration test
(`internal/server/integration_test.go::TestIntegration_TriageRoundTrip`)
exercises the same path in-process with the official MCP SDK in-memory
transport. The mock script adds the Node-from-the-outside parity check
for the same loop. The triage tool still preserves its original public
contract even though it now routes through the session substrate — and, since
the foundation phase, through the canonical durable substrate — internally.
