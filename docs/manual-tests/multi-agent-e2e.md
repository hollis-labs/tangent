# Multi-agent e2e

The v0.2 acceptance gate: two independent agent sessions, two active
rooms, and one persistent multi-envelope room that survives a browser
close/reopen.

## 1. Boot Tangent

```bash
cd ~/Projects-apps/tangent
./tangent
```

## 2. Register Tangent in two agent terminals

In terminal A:

```bash
claude mcp add --transport http tangent http://localhost:7842/mcp
```

In terminal B:

```bash
claude mcp add --transport http tangent http://localhost:7842/mcp
```

Fallback for older clients: use `--transport sse` with `/sse`.

## 3. Open two concurrent rooms

- In session A, ask Claude to run `tangent.triage`.
- In session B, ask Claude to run `tangent.feedback`.
- Open `http://127.0.0.1:7842/` in the browser. The tab strip should
  show both active rooms.
- Click each tab and resolve the envelopes independently.

Expected:

- Each agent gets the correct response for its own room.
- The tab strip keeps both rooms visible while they are active.
- No cross-talk between sessions.

## 4. Persistent multi-envelope room check

Use one agent session to drive a single room through multiple envelopes,
for example:

1. first interview-style question
2. second interview-style question
3. synthesis / summary note

After the first response:

- close the browser tab
- reopen the same `/r/<roomID>` URL
- continue the flow

Expected:

- the room still exists
- the next envelope arrives on reconnect
- prior iterations remain visible where the workflow supports history

## 5. Optional curl smoke for the tab strip

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tangent.session_create","arguments":{"title":"room-A"}}}'

curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tangent.session_create","arguments":{"title":"room-B"}}}'

curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tangent.session_list","arguments":{"active_only":true}}}'
```

The final call should return both active rooms.

If you want a single-room non-LLM variant of the same persistence
substrate, see
[`multi-envelope-session-e2e.md`](./multi-envelope-session-e2e.md).
