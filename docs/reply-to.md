# reply_to: answers delivered to the agent that asked

**Status:** Implemented for room workflows and `tangent_interaction_submit` (2026-10-30). Artifacts, docs and HITL enqueue are next.

## What changed

Agents used to poll (`tangent_interaction_get`, `tangent_interaction_await`) or hold a
call open to learn what the operator answered. Every agent-facing submit now carries
`reply_to`, the agent's own Tether URN. When the operator answers, or the item ends
without an answer (canceled, expired, superseded), Tangent mails the outcome to that
URN. Tether delivers it on the agent's next turn, or at once if the agent is idle.

## The contract

- Room-backed tools (`tangent_form_collect`, `tangent_approval_queue`, `tangent_diff_review`,
  `tangent_design_iteration`, ... and `tangent_session_advance`): `completion.reply_to`.
- `tangent_interaction_submit`: top-level `reply_to`.
- Required when the server runs with `WithReplyToRequired` (production boot does). A call
  without a usable `msg://...` address is refused with `REPLY_TO_REQUIRED`.
- Tangent's own plugins use `msg://app/tangent/internal`, which names no agent and is skipped.
- Stored on the interaction's `external_refs.reply_to`.

## Delivery

A `tether_mail` delivery rides the existing delivery queue beside the caller-pull one:
resolution deliveries for answers, terminal notifications for everything else. The
`replymail` worker claims only `tether_mail`; every other claimer skips them. It posts to
Tether's notify endpoint:

```
POST {TANGENT_TETHER_URL}/messages/notify
Authorization: Bearer {TANGENT_TETHER_TOKEN}      (send-only service credential)
Idempotency-Key: tangent:resolution:<interaction id>:<revision>
{"from": TANGENT_TETHER_SENDER, "to": reply_to, "kind": "response", "payload": {...}}
```

Network errors, 429 and 5xx retry with backoff (30 s doubling to 4 h, 12 attempts). Other 4xx
is a terminal failure. Not being woken ("offline", "busy") is a success: Tether stores the mail.
Unconfigured, the worker does not start and deliveries stay queued until the credential is installed.

## The mail

`payload.tangent` carries `interaction_id`, `kind`, `state`, `room_id`, `envelope_id`, and for an
answer `response` (the operator's answer, up to 48 KB, else `response_truncated` and a pointer to
`tangent_interaction_get`) or for no answer `terminal_cause` and `terminal_reason`. Secret form
answers are already tombstones in the stored resolution and never leave in the mail.

## Not here

Archive and delete for interactions from the browser need an operator authority the host
denies by default (ADR 0004; `PrivilegedActorPolicy`). The Inbox shows a title and
copy-link/copy-id on workflow items; ending one from the browser is a separate decision.
