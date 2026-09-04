---
name: tangent-hitl-inbox
description: Use when an agent needs to enqueue one durable operator approval or persistent-attention request in Tangent's `/hitl` inbox, retain an asynchronous handle, later get/await/withdraw it, or choose between the HITL inbox and the separate approval-queue batch workflow.
---

# Tangent HITL Inbox

Use the durable inbox when one operator-owned approval or attention item must
outlive the originating request, connection, or Tangent process.

## Default flow

1. Choose `approval` for an approve/deny decision or `attention` for durable
   acknowledgement, optional note, or reply.
2. Build a strict v1 request from
   [`references/request-shapes.md`](./references/request-shapes.md). Give the
   operator a concrete title, concise summary, explicit request, recommendation
   when useful, both approval impacts, source assertion, correlations, and only
   relevant evidence. Omit unknown optional facts; never invent correlation or
   artifact identity to make a template look complete.
3. Call `tangent.hitl_enqueue`. Return control as soon as its durable handle
   arrives. Retain `surface_id`, `item_id`, `item_url`, `inbox_url`,
   `queue_sequence`, current `state`/`revision`, `source.application_id`, and
   the caller-chosen `idempotency_key`. Treat `queue_position` as a changing
   projection. Copy handle values from the actual enqueue result; never predict
   an ID, revision, sequence, position, or state before that result exists. Do
   not keep the original tool call open for the human decision.
4. Continue independent work. Later call `tangent.hitl_get`, or use a bounded
   `tangent.hitl_await` when waiting is useful. An await timeout is a successful
   pending projection and changes no lifecycle state.
5. Call `tangent.hitl_withdraw` only when the originating application no longer
   needs a still-pending item. Reuse the same `caller.application_id` used as
   `source.application_id` at enqueue.
6. Treat `approved`, `denied`, and `acknowledged` as captured operator outcomes.
   The caller—not Tangent—owns any merge, deployment, task transition, publish,
   or other downstream effect.

## Boundaries

- Request `source`, labels, and project/task/session correlations are immutable
  caller assertions, not authenticated identity or authorization. A trusted
  gateway binding is separate and must not rewrite them.
- `/hitl` is one persistent FIFO ledger across agents. Selecting, filtering,
  reconnecting, or inspecting evidence never reorders or resolves it.
- Tangent deliberately emits no toast or OS notification. Tell the operator to
  open the returned `item_url` or `/hitl`; never promise ambient notification.
- Evidence supports bounded `markdown`, `text`, unified `diff`, read-only
  `tangent_reference`, and authority-owned `artifact_ref` metadata. Preview
  requires an explicitly supported authority/capability adapter. Never turn a
  path, `file:` URI, label, or ambient agent access into artifact authority.
- Use `tangent.approval-queue` instead when the operator needs the existing
  room-backed batch review with one submit boundary. Do not translate that
  workflow into HITL items implicitly.

## Operator smoke

Open `/hitl` empty; concurrently enqueue approval and attention items from two
application IDs and confirm both remain pending; inspect all five evidence
families; resolve one item; reconnect the browser; restart Tangent on the same
database; retrieve the resolved outcome from a new MCP request; then withdraw
the remaining item. Confirm FIFO stability, durable deep links, no room
creation, no toast/new window/OS notification, and no downstream business
action. Use the full assertions in
[`docs/manual-tests/hitl-inbox-e2e.md`](../../../docs/manual-tests/hitl-inbox-e2e.md).

## Sources of truth

- Payload template and direct/Tether examples:
  [`references/request-shapes.md`](./references/request-shapes.md)
- Contract semantics:
  [`docs/contracts/hitl-inbox-v1.md`](../../../docs/contracts/hitl-inbox-v1.md)
- Machine-readable schema:
  [`internal/envelope/extensions/hitl_item_schema.json`](../../../internal/envelope/extensions/hitl_item_schema.json)
- MCP wiring and Tether caveat:
  [`docs/mcp-integration.md`](../../../docs/mcp-integration.md)
