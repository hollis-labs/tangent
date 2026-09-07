---
name: tangent-relay-inbox
description: Use when a Claude Code session needs to cooperate with another session (or the operator) through Tangent's relay — open or attach to a shared channel, send a message with a durable idempotency key, check in for new messages as a durable check-in rather than a continuous wait, and acknowledge what it has actually handled.
---

# Tangent Relay Inbox

Use the relay when this session's work depends on exchanging messages with
another Claude Code session or the human operator, and that exchange must
survive either side relaunching. This is the CLI relay provider
(CW-20260906-0072): a pull-only, request/response surface over seven
`tangent.relay_*` MCP tools. There is no push — nothing arrives unprompted —
so this skill's shape is "check in periodically," never "stay connected."

## Why durable check-in, not continuous standby

A session that holds a live `relay_receive` call open with a long `wait_ms`
for the whole time it is idle is the continuous-standby model. It was
measured and rejected on cost: at the bounded-wait ceiling, one standing
session costs roughly $350–450/week. The durable check-in model — attach
once, then poll on your own schedule with `unacked_only: true` — costs
one call per check-in and holds no state between calls. Prefer check-in
unless a specific task has already justified continuous standby to the
operator.

## Default flow

1. **Get a channel.** If you are starting the collaboration, call
   `tangent.relay_open_channel` once and record `channel_id`. If you are
   joining one another session or the operator already opened, get
   `channel_id` from them — never call `relay_open_channel` speculatively
   to "see if it exists"; it always creates a new channel.
2. **Attach.** Call `tangent.relay_attach` with your `source`
   (`application_id` + `agent_id` — pick stable values and reuse them for
   the life of this collaboration, since `channel.Store.UpsertParticipant`
   resolves identity by that pair) and `runtime` (`authority` plus an
   `endpoint_ref` you control, such as a session id). Attach again after
   every relaunch — generation advances, and the previous binding is
   automatically superseded, not left dangling.
3. **Send.** Call `tangent.relay_send` with a constructed `idempotency_key`
   (see below — never shell out to build one). Name an explicit recipient
   (`recipient_application_id` + `recipient_agent_id` together) unless the
   channel has exactly one live operator to default to; a default send to
   an ambiguous channel is refused, not guessed. Read
   `recipient_presence` in the result for a signal about the OTHER side —
   see the presence caveat below before you act on it.
4. **Check in.** Call `tangent.relay_receive` with `cursor: 0` and
   `unacked_only: true` on your own schedule (a fixed interval, a
   milestone in your own work, or a bounded `wait_ms` up to
   `wait_ms_max` if you want one call to also wait briefly). This is the
   full check-in: it needs no cursor held in memory, so it is correct
   immediately after a relaunch with zero prior state. A `status: "timeout"`
   is not an error and not "nothing exists" — it means nothing new arrived
   in this call's window; check in again later.
5. **Ack everything you handle.** Call `tangent.relay_ack` for every
   exchange you have finished with — including one you read and decided
   needs no reply. `unacked_only` means the server's only record of "this
   session is done with this message" is the ack. Anything received but
   never acked is returned again on every future check-in, forever. Do not
   ack something you have not actually processed just to silence it.
6. **Detach on a clean exit, but never rely on it firing.** Call
   `tangent.relay_detach` when you know you are ending the collaboration.
   Treat it as best-effort: a killed process, a crash, or a lost connection
   means detach never runs, and that is an expected, ordinary outcome, not
   a bug to route around. The channel's membership and binding history are
   the durable record; a session that vanished without detaching is simply
   read through presence (`last_seen_at`), never assumed to still be
   listening.

## Idempotency keys, without shelling out

`relay_send` requires `idempotency_key`, and the whole point of the field
is defeated if a retry mints a fresh one — that creates a second exchange
for the same logical message instead of returning the first. Build the key
from values you already hold; never from the wall clock, a random value, or
a shelled-out `date`/UUID call, all of which change on every retry and
guarantee the key does nothing.

- **Replying to a specific message:** use the exchange you are replying to
  as the anchor — it is already a stable, unique id you hold with no
  extra work: `"{application_id}:{agent_id}:{channel_id}:reply:{reply_to_exchange_id}"`.
- **A fresh outbound message with no reply anchor:** pick a short, stable
  slug that names the logical thing you are sending *before* you send it —
  a phase name, a milestone, a check-in ordinal you are already tracking —
  and reuse that exact slug if you retry the same send:
  `"{application_id}:{agent_id}:{channel_id}:{your-slug}"`. Choose a new
  slug only when the intent is genuinely a new message, not a retry of the
  same one.

Either way the key is pure string construction from data already in hand.
If you find yourself needing a timestamp to make a key "unique enough,"
that is a sign you are about to mint a new key on every retry rather than
reusing one — stop and use one of the two patterns above instead.

## The presence caveat

`relay_send`'s `recipient_presence` and `relay_receive`'s `presence` are
easy to misread. A session's own `relay_receive` call always reports
`open: true` on itself while it is inside that call — that is an artifact
of when the flag is set and cleared, not a claim that anyone is listening.
The only presence read worth acting on is a THIRD PARTY's view: what
`relay_send`'s `recipient_presence` says about the *other* participant.
Even then, `open` is in-memory and process-local — always `false`
immediately after a restart Tangent did not observe through — and
`last_seen_at` is the only fact that survives one. Do not use presence to
decide whether to keep retrying a send; use it only as a hint for how
soon a reply might arrive.

## Check-in loop shape

- Bound every wait: `wait_ms` is capped at `wait_ms_max`
  (`tangent.relay_capabilities`'s `wait_ms_max`, currently the same 50000 ms
  ceiling `tangent.hitl_await` uses) so no single call can block
  indefinitely.
- Treat a timeout and a genuine tool error as the same "no progress this
  attempt" signal for one shared retry counter or backoff — do not give
  errors their own unbounded retry loop just because they are not
  literally `status: "timeout"`. A channel that starts erroring on every
  check-in (deleted, detached, unauthorized) should stop the loop and
  surface the problem, not spin forever waiting for a timeout that will
  never come.
- Prefer this skill's own re-await loop over a Stop hook. If a Stop hook is
  used at all, cap its retry count and never make it a global default —
  the CW-20260907-0016 spike found an uncapped hook is how a session gets
  stuck re-triggering itself indefinitely.

## Diagnostics

There is no separate relay diagnostics tool. Call
`tangent.relay_capabilities` — with `channel_id` + `source` to scope the
answer to your current binding, or with neither for the adapter's generic
answer — and read `capabilities` and `wait_ms_max` directly; unsupported
operations are named `false` rather than simulated.

## Boundaries

- This skill assumes Tangent's MCP tools are already reachable the normal
  way for this session; relay needs no bespoke `--mcp-config` or separate
  wiring beyond however this session already reaches `tangent.*` tools.
- `source.application_id` and `source.agent_id` are self-asserted, not
  authenticated. Pick stable values and do not rely on them as an access
  boundary.
- Every relay tool input is flat at the top level by construction — no
  `allOf`/`if`/`then`/`oneOf`, no optional nested object outside `source`
  (and `runtime` on attach). Do not build a client that assumes a richer
  shape than what `tangent.relay_capabilities` and the tool schemas
  actually advertise.
- The operator's own send/read path has no MCP tool; it is a REST surface
  over the same two stores (CW-20260906-0017), not this skill's concern.

## Sources of truth

- Tool-by-tool request/response shapes and worked examples:
  [`references/relay-tool-shapes.md`](./references/relay-tool-shapes.md)
- Boundary vocabulary (Channel, Participant, Runtime binding, Exchange):
  [`docs/adr/0006-collaboration-surface-and-relay-boundary.md`](../../../docs/adr/0006-collaboration-surface-and-relay-boundary.md)
- Architecture, the durable check-in shape, and the dormant-outbox
  reasoning: [`docs/architecture.md`](../../../docs/architecture.md)
  ("Cooperative relay inbox")
- Source: [`internal/mcp/relay_tools.go`](../../../internal/mcp/relay_tools.go),
  [`internal/relay/provider.go`](../../../internal/relay/provider.go)
