# Room workflow completion, waiting, and recovery

**Status:** Implemented
**Applies to:** every named room-backed workflow tool and `tangent.session_advance`

## The problem this solves

A room workflow waits for a human. Humans take minutes, sometimes tens of
minutes. A single HTTP request does not.

Before this change, one MCP request blocked on a process-local channel until
the operator answered. If the transport died first, two things went wrong at
once:

1. The caller got nothing — an empty reply, no result, and no identity with
   which to go looking for one.
2. The expiring request context *terminalized the request*, so a timeout could
   overwrite work a human had already done.

The live case: approval-queue room `b2510de2-a7ea-4eae-a903-84d9b83410b2`
stayed open about twenty-two minutes. The operator submitted successfully and
SQLite durably recorded the result at `2026-09-04T12:57:17Z`. The caller's
request had already exceeded Tangent's 60-second write timeout; curl exited 52
("Empty reply from server") with a zero-byte response and a zero-byte stderr.
The human's answer survived. The caller's access to it did not.

## What a caller sees now

Every named room workflow accepts an optional `completion` object:

```json
{ "completion": { "mode": "async" } }
```

`mode` is `"wait"` (the default) or `"async"`.

### Fast path — unchanged

In `wait` mode, if the operator answers within **45 seconds**, the tool returns
the exact same response body it always did. Nothing about an integration
written against v0.12 changes for interactions a human answers quickly.

### The 45-second boundary

At 45 seconds the call returns a **successful pending receipt** — not an MCP
error, and not a cancellation:

```json
{
  "status": "pending",
  "handle": {
    "surface_id": "…",
    "interaction_id": "…",
    "room_id": "…",
    "envelope_id": "…",
    "url": "http://127.0.0.1:7842/r/…"
  },
  "resume": {
    "get_tool": "tangent.interaction_get",
    "await_tool": "tangent.interaction_await",
    "retry_original": true
  }
}
```

Branch on the top-level `status` field: `"pending"` means the human has not
answered yet. Anything else is a workflow response.

The window sits under the 60-second HTTP write timeout on purpose, so the
receipt is written while the response is still writable.

### Async mode — recommended for agents

`{"completion":{"mode":"async"}}` returns that same receipt immediately. Use it
whenever you are not going to sit and stare at the call: the durable handle is
created before anything can lose it, and the room URL is in the receipt so you
can hand a human a link straight away.

`wait` remains the default so third-party callers keep their existing
behavior.

## Recovering the result

Three routes, all equivalent. Each returns the **same immutable result**, byte
for byte, however many times you ask.

1. **`tangent.interaction_get`** — `{"interaction_id": "…", "requester_scope":
   "standalone-local"}`. Returns the current state, or the terminal outcome
   with its `resolution.response_payload`.
2. **`tangent.interaction_await`** — the same arguments plus
   `maximum_wait_ms` (up to 50000). Bounded; a timeout changes nothing.
3. **Retry the original invocation.** Call the same tool again with the same
   envelope id, type, and payload. This is the route that works when the first
   pending receipt itself was lost: you do not need to have kept the handle,
   only the request you made.

`requester_scope` resolves to `standalone-local:anonymous` for a direct
loopback MCP caller that declares no application id — `"standalone-local"`
still reads as exactly that, through a fixed alias with no data rewrite. The
argument is still accepted, but it is no longer the authorization value: the
authority half is assigned by the host from admission facts, and only the
partition half is caller-supplied
([ADR 0004](adr/0004-caller-participant-and-room-access-authority.md) §3).

The scope is recorded honestly: a loopback call carries no authenticated
identity, and Tangent does not pretend otherwise. **Partitions inside
`standalone-local` are advisory, not a security boundary** — any local caller
can assert any partition, and isolation is enforced only across authorities.
A retrieval is authorized against the interaction's own caller scope or its
surface's owner scope; cross-authority reads return `not_found` rather than
`unauthorized`, so a foreign authority cannot probe for existence.

## Identity, retries, and conflicts

A room workflow invocation is identified by **caller scope + workflow kind +
envelope id**. The room is deliberately not part of it.

- An **identical retry** — same identity, same payload — always returns the
  same interaction and, once terminal, the same immutable result. It never
  re-asks the human and never executes twice.
- A **conflicting retry** — same identity, different payload — is a hard error
  with code `IDEMPOTENCY_CONFLICT`. It cannot open a second interaction and it
  cannot execute anything. Use a new envelope id.

"Payload" means the envelope's `data`. Several workflows present the caller's
payload merged with persisted room state; identity is keyed on what the caller
sent, not on what the participant currently sees, so ordinary room progress
never turns a legitimate retry into a conflict.

## What can and cannot end an interaction

These stop **only the active waiter or transport**. The interaction stays
exactly as answerable as it was:

- the 45-second window elapsing,
- the MCP or HTTP request disconnecting,
- the operator's browser tab closing or refreshing,
- Tangent restarting.

These are the only things that produce a terminal outcome:

- the participant submitting a response,
- the participant cancelling (returns the v0.12 `ack` / `cancelled` shape),
- an authorized caller cancelling via `tangent.interaction_cancel`,
- an authorized caller closing the room via `tangent.session_close`, which
  dispositions everything outstanding under a named surface policy.

After a restart, room presentation and history are rebuilt from canonical
interaction records. The in-memory pending map and the v0.12 `rooms` /
`envelopes` tables are compatibility projections; they never decide an
outcome. A legacy row can read `pending` long after the canonical interaction
resolved — the `legacy_room_history_v12` view reports both, and the canonical
`interaction_state` is the one that is true.

## Acknowledgement

`tangent.interaction_acknowledge` records that a caller has taken
responsibility for a terminal outcome:

```json
{"interaction_id": "…", "requester_scope": "standalone-local"}
```

It is idempotent — repeating it returns the original acknowledgement with
`"created": false` and writes nothing.

Acknowledgement is deliberately separate from two things it is easy to confuse
it with:

- **Retrieval** proves a caller *read* an outcome. Reading it again, or ten
  more times, still does not acknowledge it.
- **Delivery** proves Tangent *handed* an outcome to a destination — recorded
  as a durable attempt with a receipt. A successful HTTP response is not the
  caller saying it accepted responsibility.

Acknowledge when your side has committed to the result. Nothing forces you to;
the outcome stays immutable and retrievable either way.

## Operator notes

- A room shows outstanding work again after a browser refresh, a reconnect, or
  a Tangent restart. Nothing you were mid-way through is lost by closing a tab.
- A submission Tangent cannot accept (a malformed or stale response) is
  rejected without ending the interaction. Submit again.
- Cancelling is explicit and terminal. It is one of the two ways a room
  workflow ends.

## Related

- `internal/roomflow` — the shared compatibility adapter.
- `internal/interaction` — the canonical durable substrate.
- `docs/interactive-collaboration-direction.md` — the architectural direction
  this implements.
