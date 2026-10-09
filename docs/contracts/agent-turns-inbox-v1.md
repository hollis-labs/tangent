# Tangent Agent Turns FIFO Inbox contract v1.2

**Status:** Implemented source contract; plugin installation and live delivery are separate work.

**Payload versions:** `1.0`, `1.1` and `1.2`. **Definition:** `tangent.agent-turn`, version `1.2`.

**Decision basis:** [ADR 0012](../adr/0012-messaging-consumer-and-stage-pipeline.md),
[ADR 0003](../adr/0003-definition-and-package-ownership.md), and
[ADR 0005](../adr/0005-product-boundary-and-portfolio-composition.md).

## 1. Ownership and admission

Tangent owns durable presentation, operator resolutions, and FIFO ordering.
Application adapters own their source subscription, stage execution, publication
ledger, and actual reply delivery. Tangent core has no Tether client or routing
adapter. The owner-scoped messaging MVP summarizes every admitted publication;
there is no attention filter, autonomous action, or automatic reply.

`question`, `approval`, and `failure` retain the producer's classification rather
than proving human authority or a diagnosis. Routed `final` maps to `terminal`:
a completed **turn output**, not session termination or task success. An ordinary
publication is an informational `checkpoint`, with no invented runtime session
or turn. Source attribution is data, not authentication or a grant.

Invalid classification, missing routed identity, mismatched sender/session,
invalid UTF-8, and oversized content are admission refusals. The caller preserves
its pending obligation and stops advancing its cursor; refusal does not authorize
truncation, silent skip, or a claim of complete ingestion.

## 2. Independent identities

| Identity | Meaning |
|---|---|
| `source_message.message_id` | Actual source publication, distinct from its optional producer `output_id`. |
| `source_message.sequence` | Source channel sequence; not Tangent's queue sequence. |
| `session_id`, `turn_id` | Actual originating runtime IDs for routed items. Never derived from output ID or text. |
| `item_id` / `interaction_id` | Tangent's durable interaction UUID. |
| `queue_sequence` / `surface_sequence` | Transactional arrival ordering in Tangent's surface. |
| `resolution_id` | Tangent's immutable operator resolution, distinct from an external reply receipt. |
| `idempotency_key` | Caller-owned deduplication identity. A channel consumer keys by endpoint/channel/publication ID. |

A suitable publication key is `tether-publication:v1:<SHA-256 of JSON [endpoint,channel,message.id]>`.
Two outputs in the same turn must not collapse into one item. Persist the exact
prepared request before enqueue. Existing idempotency conflict behavior can return
the original item; it is not evidence that a changed request replaced it.

## 3. Request and public API

The public `pkg/plugin.AgentTurnRequest` describes the payload.
`plugin.TurnsEnqueueInputSchema()` is the schema advertised by
`tangent.turns_enqueue`; HTTP `POST /api/turns/enqueue` uses the same service.
JSON Schema validates the shape; the service additionally checks encoded size,
summary agreement, unique stage IDs, timeout/code agreement, and routed identity
before any persistence. Unknown fields in the new typed objects are refused.

| Field | Bound and meaning |
|---|---|
| `contract_version` | `1.0` or `1.1`. The current public constant is `1.1`. |
| `turn_id`, `session_id` | Nonempty, at most 256 characters each; required except for an explicit `1.1` publication origin. |
| `idempotency_key` | Nonempty, at most 256 characters. |
| `kind` | `question`, `approval`, `checkpoint`, `failure`, or `terminal`. |
| `source` | Required `agent_id` (1–256 characters); optional `application_id` (1–128) and `agent_label` (up to 256). |
| `title` | 1–160 characters. |
| `content` | Original UTF-8 text, 1–65,536 characters. Preserved unchanged, including whitespace. |
| `summary` | Optional plain text, at most 600 characters; must equal each supplied summary annotation. |
| `options` | Optional bounded objects `{label,value,description?,recommended?}`, not strings. Publication-origin items cannot supply options. |
| `correlations` | Existing opaque application correlations; not authority. |
| `expires_at` | Optional RFC 3339 timestamp. |

Version `1.0` requests retain their original required runtime IDs and cannot
supply `annotations`, `stage_trace`, or `source_message`. Their stored version and
content remain readable; the renderer does not fabricate new metadata. Version
`1.1` without `source_message` also requires runtime IDs, preserving ordinary
existing callers while allowing them to adopt typed stages.

### Annotations and traces

`annotations` is an optional array of at most 16 `plugin.TurnAnnotation` objects:

```json
{
  "schema_version": 1,
  "stage_id": "summarize",
  "stage_version": "1",
  "kind": "summary",
  "summary": {"text": "Plain-text summary of the original."}
}
```

`stage_trace` is an optional array of at most 16 `plugin.TurnStageTrace` objects:

```json
{
  "stage_id": "summarize",
  "stage_version": "1",
  "outcome": "timed_out",
  "duration_ms": 15000,
  "failure_code": "stage_timeout"
}
```

Stage IDs and versions are nonempty and at most 128 characters. Each array has
unique stage IDs. Summary text is nonempty and at most 600 characters. Outcomes
are `passed`, `failed`, or `timed_out`. A successful stage has no failure code;
a failed stage requires one of `stage_error`, `stage_timeout`, `stage_panic`,
`stage_refused`, `invalid_output`, or `annotation_limit` (all within 128 characters).
`timed_out` agrees only with `stage_timeout`. These are operational codes, not raw
provider diagnostics or model reasoning. Duration is a nonnegative integer within
JavaScript's exact range (`0..9007199254740991`).

**The combined encoded metadata ceiling is 8 KiB.** The host uses Go's canonical
`encoding/json` encoding of the object containing the supplied `annotations` and
`stage_trace` fields, including keys, delimiters, and escapes. Individual Unicode
character limits do not replace this encoded-byte check. The original content and
source projection retain their separate field limits. Future annotation kinds
require an explicit schema change, not an arbitrary payload map.

### Source publication

`source_message` is an optional `plugin.TurnSourceMessage` in version `1.1`:
`schema_version: 1`, `origin: routed|publication`, nonempty `endpoint_ref`,
`channel`, `message_id`, positive `sequence`, `sender_urn`, and optional `output_id`
and typed `attribution`. Endpoint and sender are limited to 512 characters;
channel/message/output IDs to 256; sequence to JavaScript's exact integer range.
The sender must be a `msg://` URN. Attribution retains optional logical agent,
project/workstream, launch ID/display name, runtime, stop reason, classification,
and extraction confidence (`exact|heuristic|none|unknown`), without inferring authority.
Attribution strings are bounded to 256 characters. `source.agent_id` must agree
with a nonempty `logical_agent_id`, otherwise with the supplied sender URN.
This consistency check does not authenticate that attribution.

- `routed` requires actual runtime IDs and attribution kind. Sender must equal
  `msg://session/local/<session_id>`. Classification must match the item kind,
  with `final` mapped to `terminal`.
- `publication` requires `checkpoint` and cannot supply response options. Genuine
  caller-supplied runtime IDs are optional attribution; absent IDs stay absent.
  It always has `replyable: false` in the service view, even with supplied IDs.
  Backend reply attempts are refused without altering the item; explicit
  dismissal remains supported.

The item view exposes the persisted payload version, typed metadata, and derived
`replyable`. Both inbox surfaces show an escaped plain-text summary above a
separately expandable, exact original, visible failure markers, compact trace,
and attributed source. No stage output creates an acting button. Legacy bodies
without annotations remain readable in their usual presentation.

## 4. Operator replies and actual delivery

An explicit operator reply saves an immutable resolution in Tangent. A saved
resolution is not runtime delivery. `tangent.turn_await` returns unacknowledged
session replies with `wait_status: replies|timeout`; the wait is 30 seconds by
default, bounded to 50 seconds, and zero means a single read. Repeated reads do
not acknowledge or consume them. `GET /api/turns/sessions/{session_id}/replies`
reads history, including acknowledged replies. Dismissal creates no reply.

The optional response `interrupt` is a strict boolean; absence means false.
`plugin.AgentTurnResponse` exposes that response shape. The flag is saved with
its immutable resolution and returned by `tangent.turn_await`. A later inbox
refresh cannot change it. This is explicit operator intent, not a grant or proof
of human identity; a consumer must check the actual target's routing and interrupt
capabilities before calling its dedicated reply endpoint.

An application adapter handles the reply with its own actual source identity and
publication mapping. The separately installed messaging plugin owns the private
reply preparation, receipt and delivery ledger. The host binary has no Tether
client and does not infer delivery from a saved resolution.

For routed messaging items, the renderer reads the fixed plugin GET route
`/api/plugins/messaging/delivery?item_id=<item>`. It declares the host's
participant `view` capability and returns bounded metadata, never message/reply
text, idempotency keys or upstream diagnostic bodies. Missing/unknown capability
remains unavailable. The renderer never sends or acknowledges while polling.
A confirmed terminal failure offers a deliberate POST to
`/api/plugins/messaging/retry` under the participant `draft` capability, containing
`item_id`, the expected attempt `version`, a fresh user `action_id`, and the
explicit boolean `interrupt`. The private adapter must atomically preserve the
failed predecessor and prepare the new attempt before external dispatch. An
ambiguous HTTP result reuses that exact action/key/flag; a new key requires a new
explicit action. Accepted queued replies do not expose a retry button. No
re-resolution of Tangent's immutable answer or effectful GET is involved.
This single-owner MVP does not claim Tether per-message reply authorization or a
verified human principal; those remain before-1.0 work.
Tether queue acceptance, binding handoff, runtime delivery, and Tangent's
acknowledgement are distinct. An adapter calls `tangent.turn_ack` (or the HTTP ack
route) only after actual delivery, with the item ID and optional resolution ID.
A wrong resolution or unanswered item is refused; repeat acknowledgement is
idempotent. An undeliverable reply stays preserved and unacknowledged. There is
no shipped core callback or session-disappearance notification contract here.

## 5. Ordering, compatibility, and deployment limits

FIFO sequence is assigned transactionally and preserved across reads, filters,
operator dismissal, and restart. Client-side filters do not mutate admission or
queue order. Dismissal cancels an item explicitly; it does not reply to its source.
This change installs no new retention policy or automatic body purge.

The response schema changed, so the shipped definition and package are versioned
`1.2` (revision 1), with the contract lock and generated projections updated under
ADR 0003. **Existing pending interactions remain pinned to their old binding.**
The host's current-binding policy can make them unavailable for subsequent
operations after this additive bump; old item reads are retained, but old
requests/bindings are not normalized, migrated, rewritten, or silently reopened.
See [the current limitation](../architecture.md#an-additive-version-bump-takes-pending-interactions-out-of-service).
Plan operator handling before installing this host version; a compatibility
policy change is separate work.

A source merge is not host/plugin installation, routing activation, provider
availability, or real reply delivery. Consumers use an actual compatible released
host/public package through the normal release path; the accepted single-owner
identity, disclosure, and retention limits remain tracked before 1.0.
