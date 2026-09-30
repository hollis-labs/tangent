# Multi-agent e2e

Two independent callers holding two live rooms concurrently, plus one
persistent multi-envelope room that survives a browser close/reopen.

**A room is not an agent session.** It is a compatibility projection of a
durable surface (ADR 0001). This recipe drives each room from a different
agent session because that is the easy way to produce concurrent callers — not
because the two are the same thing. Nothing binds a room to one agent, one
caller, or one tab, and a single room accepts several simultaneous connections
with one resolver lease among them.

## 1. Boot Tangent

```bash
cd tangent  # your clone of the repo
./tangent
```

## 2. Register Tangent in two agent terminals

In terminal A:

```bash
claude mcp add --transport http tangent http://127.0.0.1:7842/mcp
```

In terminal B:

```bash
claude mcp add --transport http tangent http://127.0.0.1:7842/mcp
```

Fallback for older clients: use `--transport sse` with `/sse`.

## 3. Open two concurrent rooms

- In session A, ask Claude to run `tangent.triage`.
- In session B, ask Claude to run `tangent.feedback`.
- Open `http://127.0.0.1:7842/` in the browser. The tab strip should
  show both active rooms.
- Click each tab and resolve the envelopes independently.

Expected:

- Each caller gets the correct response for the room it is driving.
- The tab strip keeps both rooms visible while they are active. The strip
  reads `GET /api/rooms`, which requires a participant session and is scoped
  to the caller authority, so a browser with no session sees nothing.
- No cross-talk between rooms.

## 4. Persistent multi-envelope room check

Use one caller to drive a single room through multiple envelopes,
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
