# Relay tool shapes

Seven tools, `contract_version: "1.0"` on every input and output. Every
schema is flat at the top level except `source` (every tool) and `runtime`
(`relay_attach` only) — both required objects, never optional, so they
never hit the `["null","object"]` union jsonschema-go renders for an
optional nested struct.

```json
{ "application_id": "claude-code", "agent_id": "tangent-15" }
```

is `source` on every call below. Pick stable values for the life of one
collaboration; identity resolves by this exact pair
(`channel.Store.UpsertParticipant`), not by anything Tangent verifies.

## tangent.relay_open_channel

Creates a new channel and its canonical operator participant. Never call
this to check whether a channel exists — it always creates one.

```json
{ "title": "phase-4 relay work", "project_ref": "CW-20260907-0014" }
```

Both fields optional. Result:

```json
{
  "contract_version": "1.0",
  "channel_id": "...",
  "operator_participant_id": "...",
  "created_at": "..."
}
```

## tangent.relay_attach

`channel_id` must already exist — this never creates one, unlike
`relay_open_channel`. Re-attach after every relaunch; `generation`
advances and the previous binding for this participant is superseded.

```json
{
  "channel_id": "REPLACE",
  "source": { "application_id": "claude-code", "agent_id": "tangent-15" },
  "runtime": { "authority": "claude-code-cli", "endpoint_ref": "session-xyz" }
}
```

`runtime.endpoint_ref` is optional; `runtime.authority` is required.
Result carries `participant_id`, `channel_id`, `generation`,
`runtime_authority`, `runtime_endpoint_ref`, `attached_at`.

## tangent.relay_detach

Best-effort clean exit. Supersedes the current binding in the same
transaction as ending membership — never assume this fires on a crash or a
killed process.

```json
{
  "channel_id": "REPLACE",
  "source": { "application_id": "claude-code", "agent_id": "tangent-15" }
}
```

## tangent.relay_send

`recipient_application_id` and `recipient_agent_id` are flat fields, not a
nested object — give both together, or omit both to default to the
channel's one live operator (refused as `ambiguous_recipient` if there are
zero or several).

```json
{
  "channel_id": "REPLACE",
  "idempotency_key": "claude-code:tangent-15:REPLACE_CHANNEL:phase-4-status-1",
  "source": { "application_id": "claude-code", "agent_id": "tangent-15" },
  "recipient_application_id": "claude-code",
  "recipient_agent_id": "tangent-14",
  "subject_id": "",
  "reply_to_exchange_id": "",
  "body": "unacked_only chosen for the cursor remedy; see architecture.md."
}
```

`subject_id` and `reply_to_exchange_id` are both optional; omit rather than
send empty strings if you have neither. Result carries `exchange_id`,
`channel_id`, `sequence`, resolved `sender`/`recipient` refs,
`recipient_binding_current` (only meaningful for an agent recipient — an
operator recipient has no runtime binding to be "current" about),
`recipient_presence` (see the presence caveat in `SKILL.md`), and echoed
`subject_id`/`reply_to_exchange_id` when given.

## tangent.relay_receive — the durable check-in call

```json
{
  "channel_id": "REPLACE",
  "source": { "application_id": "claude-code", "agent_id": "tangent-15" },
  "cursor": 0,
  "wait_ms": 0,
  "unacked_only": true
}
```

`cursor: 0` with `unacked_only: true` is the shape a freshly relaunched
session needs: everything still unhandled, regardless of how much acked
history exists, with no memory of a prior cursor required. Set `wait_ms`
above 0 (up to `wait_ms_max`) only if you want this one call to also wait
briefly for something new; a check-in loop otherwise calls this on its own
schedule with `wait_ms: 0`.

Result:

```json
{
  "contract_version": "1.0",
  "status": "ok",
  "items": [
    {
      "exchange_id": "...",
      "sequence": 1,
      "sender": { "participant_id": "...", "application_id": "...", "agent_id": "..." },
      "body": "...",
      "created_at": "..."
    }
  ],
  "next_cursor": 1,
  "presence": { "open": true, "last_seen_at": "..." }
}
```

`status` is `"ok"` or `"timeout"` — a timeout is never an error and cancels
no outstanding work. `items` is empty on a timeout. Ack every item you
handle (see `relay_ack` below) before your next check-in, or it reappears.

## tangent.relay_ack

```json
{
  "source": { "application_id": "claude-code", "agent_id": "tangent-15" },
  "exchange_id": "REPLACE"
}
```

Refused as `unauthorized` if the caller is not the exchange's recipient.
`already_acked` is `true` on a repeat ack of the same exchange — acking
twice is safe and idempotent, never an error.

## tangent.relay_capabilities

Neither field, or both together — never one alone:

```json
{}
```

```json
{
  "channel_id": "REPLACE",
  "application_id": "claude-code",
  "agent_id": "tangent-15"
}
```

Result:

```json
{
  "contract_version": "1.0",
  "adapter": "cli-relay",
  "capabilities": {
    "receive": true,
    "ack": true,
    "bounded_wait": true,
    "presence": true,
    "wake": false,
    "event_replay": false,
    "structured_envelopes": false
  },
  "wait_ms_max": 50000
}
```

`wake` and `event_replay` are `false` today: this provider is pull-only,
with nothing addressable for Tangent to push into between calls.

## Error codes

Every failed call returns `{"error": {"code": "...", "message": "..."}}`
via the tool's `IsError` result, never a transport-level error for a
business-rule refusal:

| code | meaning |
|---|---|
| `not_found` | the channel or exchange named does not exist |
| `validation_failed` | a required field is missing, or a paired field (recipient, capabilities identity) was given half |
| `ambiguous_recipient` | `relay_send` with no explicit recipient and zero or several live operators |
| `unauthorized` | `relay_ack` from a participant that is not the exchange's recipient |
| `not_a_member` | sender or recipient is not a live member of the channel |
| `idempotency_conflict` | the same sender reused an idempotency key for a logically different exchange |
| `relay_error` | anything else |
