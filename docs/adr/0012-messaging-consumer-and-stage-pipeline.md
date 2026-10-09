# ADR 0012: Messaging Consumer, Stage Pipeline, and Reply Ownership

**Status:** Proposed engineering design under the authorized Wave A allocation.
**Date:** 2026-10-09
**Task:** `CW-20261002-0131`
**Scope decision:** portfolio tracker DEC-051 (decided); DEC-052 remains deferred.

## Context

The owner wants messages from Tether in one Tangent inbox, with a short summary
above the original and an explicit reply reaching the sender. DEC-051 scopes
the MVP to the owner's own agent-os setup: Tether and Tangent only. Every
message presented to this consumer is processed; the MVP filters nothing and
makes no attention decision. Nanite and Torque consumers, automatic replies,
JEV, filtering, batching, and the right-hand assistant are outside this slice.
Before-1.0 work is tracked in `CW-20261009-0051` rather than silently assumed.

This record fixes implementation choices for the consumer tasks. It changes no
running service, catalog, plugin installation, schema, or credential. Merging
the record is not evidence that the consumer or its proposed fields exist.

### Source observations

Read against Tangent `8e3e887c100fd10d97dbd49178c036a923a1b5bb`,
tangent-plugins `1bbb191b49d80eef6d0f629b907cc36530eb3225`, and Tether
`8fafa4aa6d764310ce457f366fca2f0c2b3db061`:

- [ADR 0005](0005-product-boundary-and-portfolio-composition.md) and
  [ADR 0010](0010-the-boundary-is-coupling-not-write-direction.md) put an
  application adapter in a plugin. Tangent core provides interactions and
  presentation, without taking the application's client or workflow policy.
- Tangent has strict SDK manifest v2, protocol-2 subprocess supervision, and
  [`pkg/plugin/hostclient`](../../pkg/plugin/hostclient/client.go). Installed
  plugins receive separate persistent `Init.DataDir` and disposable
  `Init.CacheDir`; the host sends empty config and grants. `GetService` and
  host-held plugin configuration remain unimplemented.
- The published [enqueue schema](../../pkg/plugin/turns_enqueue.schema.json)
  accepts contract `1.0`, an optional `summary` of at most 600 characters,
  and content of at most 65,536 characters. It rejects unknown top-level
  fields. There is no annotations or delivery-projection field. The prose
  [inbox contract](../contracts/agent-turns-inbox-v1.md) omits the existing
  summary field and overstates delivery plumbing.
- Tether's [consumer guide](https://github.com/hollis-labs/tether/blob/8fafa4aa6d764310ce457f366fca2f0c2b3db061/docs/consumer-guide-session-routing.md)
  defines committed channel history, explicit reconnect, purge tombstones,
  routable `final|question|approval|failure`, stable output identity, dedicated
  reply acceptance, and separately readable delivery outcomes. An event-bus
  excerpt or staged message is not a committed channel publication.
- The runner currently imports go-tether-client `v0.8.0`; `0048` owns adoption
  of `v0.10.0`. That client's channel, capabilities, and reply methods exist,
  including `ReplyDelivery`; neither subscription reconnect nor delivery
  polling is automatic. The old
  [`internal/turns/tetherbridge`](../../internal/turns/tetherbridge/bridge.go)
  still waits for the nonexistent `session.turn_waiting_input` event.
- Tether already mounts normalized `POST /ai/chat` (API reference and
  `internal/api/ai.go`, `internal/llm/types.go`). This is a request/response
  model gateway, not an agent-session launch. The runner instead supervises
  processes, enqueues extracted turns, and pumps operator replies into them.

These are source observations, not deployed capability or live-traffic proofs.
`0049` owns those proofs and the approved `owner-inbox` routing scope.

## Options considered

| Choice | Alternatives and tradeoffs | Selected |
|---|---|---|
| Consumer boundary | Wire Tether directly into Tangent core, or install an independent subprocess adapter. The first repeats the retired application coupling; the second adds packaging and restart state. | A new messaging plugin in tangent-plugins. |
| Pipeline location | Put stages in the plugin, in Tangent, or in a portable library. Plugin-only code is initially simpler but ties later stages to its transport and storage. | A separate `github.com/hollis-labs/libs/message-pipeline` Go module, with adapters and concrete storage in the plugin. No new repository or release is authorized here. |
| Summarizer runtime | Reuse a runner-supervised session, spawn a CLI per message, or call the existing Tether AI gateway. Reuse saves startup but mixes message context and needs a tool-free launch contract. Spawning adds process cost and credential/working-directory exposure. Neither runner path presently provides a summary-only result without its inbox machinery. | A stateless bounded `/ai/chat` call per message. It incurs provider latency/cost but starts no agent session and has no acting tool loop. |
| Delivery processing | Trust SSE live delivery or replay from durable history with a local ledger. SSE alone loses the crash boundary; the ledger costs a private database and migrations. | Persist preparation and results, then enqueue idempotently and advance the cursor after the sink receipt. |
| Reply writer | Inject a turn from the plugin, or submit one dedicated Tether reply. Two writers can deliver twice or choose the wrong successor. | Tether alone performs next-turn injection and binding handoff. |

## Decision

### 1. Plugin boundary, configuration, and lifecycle

The messaging plugin is a new independently packaged subprocess in
tangent-plugins. It imports Tangent's public plugin types and hostclient, the
released Tether client, and the stage library; Tangent core imports none of its
Tether adapter code. Manifest declarations grant no extra authority. It uses
the existing verified-bundle, Init, Load, health, revoke, and Unload lifecycle.
Subscription and processing contexts belong to the loaded incarnation and are
cancelled and joined on unload. A callback's expired call context cannot leave
an unowned background subscription.

Non-secret startup configuration names Tether's endpoint and caller identity,
Tangent's MCP endpoint, channel names, ordered stages, limits, and an instruction
document. The plugin reads its own environment/config file. It does not call
`GetConfig`, reach into host storage, or introduce a reverse-profile offer.
Caller identity is operator-supplied, not derived from agent labels or message
text. Credentials stay with the existing runtime credential mechanism; no key
is embedded in a bundle, trace, task, or this ADR.

For the MVP, Tangent is the inbox and plugin configuration holds stage settings.
This is the reversible DEC-052 assumption, not a decision that Tether can never
own user policy. Configuration is snapshotted and versioned for a processing
run. Rules/thresholds have no filtering effect in the MVP.

### 2. Portable stages and item presentation

The library defines immutable message input, accumulated annotation state, an
ordered stage interface accepting `context.Context`, and a result containing
annotations, a disposition, and declarative follow-ups. Input carries opaque
source/message identity, original text, and typed attribution; the library has
no Tangent/Tether imports, SQL ownership, UI, provider credentials, or effects.
Its store interfaces support stage-result and cursor persistence; the plugin
implements them. Stable stage IDs and explicit priority determine order, with
configuration order breaking equal priorities. This can later adapt to the
plugin-platform filter catalog without pretending it is a registered hook now.

Each stage declares its version, finite timeout, and fail mode. The MVP has only
`summarize`, uses `pass`, and defaults to fail-open. Error, timeout, panic,
refusal, or invalid model output adds a bounded failed-stage marker and continues
with the original. Cancellation during shutdown preserves pending work instead
of committing it as an ordinary stage failure. Failure diagnostics are closed
codes and metadata, never raw provider errors or message text.

An annotation is versioned data with `stage_id`, `stage_version`, `kind`, and
typed bounded payload. MVP kind `summary` carries plain text; a compact trace
carries outcome, duration, and a closed failure code. The original is never
rewritten by a stage. Tangent displays the summary above a separately expandable
original and marks failures visibly. A summary is not an approval, task result,
or authority to reply. Trace metadata may later carry decision rationale under
a new typed annotation version; it is not a dump of model reasoning.

`hold` means durable pending disposition, not deletion or cursor loss. `drop`
means an explicit future audited disposition with source identity retained.
Batching would group references while preserving each original identity and
reply target. None is enabled by this MVP. Follow-ups are declarations for a
future owner, not executable tool calls. Filtered/audit presentation (`0030`),
options (`0031`), and `decide()` (`0050`, JEV later) remain separate work.

### 3. Channel identity, kinds, and admission

Read committed channel history/SSE. Order by publication `Seq`, not timestamps,
turn counters, event-bus cursors, or message counts. Deduplicate a publication by
`(Tether endpoint identity, channel, message.id)` and persist `metadata.output_id`
when supplied as additional producer attribution. Do not derive a runtime turn
ID from the output ID, hash the body to deduplicate, or collapse multiple outputs
from the same runtime turn. The same message published in different configured
channels is a distinct channel item; channel selection must avoid unwanted
duplicate presentation.

For routed output, `session_id` is the canonical sender session ID, checked
against `from=msg://session/local/<id>`, `thread_id`, and any session metadata.
`turn_id` remains the supplied runtime `metadata.turn_id`.
`source.agent_id` uses nonempty `logical_agent_id`, otherwise the actual sender
URN, explicitly labelled as sender attribution rather than a logical identity.
Keep launch label, runtime, confidence, stop reason, output ID, and routing
correlations separately. A suitable bounded idempotency key is
`tether-publication:v1:<SHA-256 of the JSON tuple [endpoint,channel,message.id]>`.
Persist the exact mapping to Tangent's returned item ID. The old
`tether:<session_id>:<turn_id>` suggestion is insufficient for multiple outputs.

| Tether classification | Tangent item kind | Meaning retained |
|---|---|---|
| `question` | `question` | An ended turn has an unresolved question signal. |
| `approval` | `approval` | An ended turn has a refusal/approval signal; not permission granted. |
| `failure` | `failure` | Failed turn, without treating text as an exact diagnosis. |
| `final` | `terminal` | Completed **turn output**, not session termination or task success. |
| Ordinary non-routed publication | `checkpoint` | Informational publication; no runtime-turn claim and no automatic reply target. |

Tether event kind `terminal` is not routable. The consumer does not fabricate it
from an exit or subscribe to the event bus to fill a gap. `checkpoint` is not a
new producer classifier or a summary of progress inferred by the model.

Contract `1.1` must distinguish a routed output from an ordinary publication:
the latter can omit runtime `turn_id`/`session_id` and is nonreplyable. Do not mint
fake session/turn IDs to satisfy contract `1.0`. Fake-publication verification
must exercise both a genuine test-owned routed tuple and this nonreplyable case.
Missing or inconsistent required routed attribution, unknown classification,
invalid text, and content beyond Tangent's current size ceiling are explicit
admission refusals, not relevance filters. Preserve a pending obligation, expose
metadata-only health, and stop advancement at that sequence; do not truncate
the original, silently skip, or claim complete ingestion. `0070` must settle any
larger-body contract before rollout that needs it.

For a purged tombstone, record its publication identity/sequence and purge marker,
then advance without summarizing or recreating removed content. This is a source
retention outcome, not a consumer attention decision. Existing channel purge
can remove data before capture; this system cannot promise recovery of that body.

### 4. Durable state, replay, and reconnect

Use one private plugin-owned SQLite database inside the supplied `Init.DataDir`,
with owner-only directories/files. `CacheDir` and the read-only bundle are not
durable storage. This is local custody, not isolation from other processes
running as the same OS user.

Persist per-endpoint/channel cursors, admitted publication identity and original,
stage results keyed by message identity + stage ID + stage/version/config and
instruction digests, immutable prepared enqueue payload, sink item receipt, and
reply submission/delivery records. A replay reuses the saved stage outcome,
including a failure marker, and the exact enqueue payload. It does not spend
another model call or silently change the annotation under the same sink key.
An ambiguous provider result may have incurred cost; fail-open recording is not
a provider exactly-once guarantee.

Persist preparation before calling the external sink. After a successful
`turns_enqueue` receipt, commit the item mapping and cursor together locally.
A crash between sink commit and local commit retries that exact request/key.
Tangent currently returns an existing item even on an idempotency payload
conflict; therefore that behavior cannot prove a changed request was accepted.
Never regenerate a request during replay. A sink refusal retains the pending
obligation and cursor. Stage fail-open cannot make a refused sink look delivered.

Process channel sequences serially for the MVP; later parallel work must advance
only a contiguous settled prefix. Reconnect with bounded backoff and the last
committed cursor using history followed by `SubscribeChannel`. The client does
not reconnect itself. SSE hints/high-water marks are not processed messages.
Reading channels requires no mailbox claim/ack/consume; the plugin never issues
those operations against a channel. Health distinguishes Tether unavailable,
stage degraded, admission blocked, and sink blocked, without exposing bodies.

### 5. Bounded, tool-free summarization

`0072` implements an adapter to the existing normalized `/ai/chat` wire over the
configured Tether transport, without importing Tether internals. Each request
contains only one admitted original and its attribution. A plugin-owned default
instruction document lives in the verified bundle, with an optional operator
path selecting a replacement. Snapshot and digest it at startup. Later standing
directions can replace that document provider without changing the stage.

Request `operation=chat`, `mode=summarize`, no tools or attachments, no prior
conversation, and an output budget of at most 512 tokens. Use a configurable finite
deadline (MVP default 15 seconds) within the processing budget. Route/model hints
are hints, not guarantees; configured gateway budget/refusal remains effective.
The gateway owns provider credentials and routing. Verify availability during
rollout; no provider call is made by this ADR and no default provider is invented.

Place trusted instructions separately from the quoted, untrusted source text.
Accept only a strict schema-validated summary object containing nonempty plain
text up to the existing 600-character summary bound; reject extra fields, tool
use, refusal, malformed/truncated output, or a requested action. Cap the gateway
response body at 64 KiB before decoding. Never execute
a model-emitted command, follow-up, option, or reply. Unknown provider errors
become a failed-stage code. Do not automatically retry a timed-out billable call.
There is no claim that prompting prevents disclosure of secrets present in the
source; redaction/retention and stronger isolation remain `0051` obligations.

### 6. Tangent contract and rendering changes specified

`0134` implements, rather than this ADR editing, the next Agent Turns contract:

- Version `1.1`, retaining readable `1.0` items. Preserve `content`, existing
  `summary`, options, and Tangent item/queue identity. Add optional bounded
  typed `annotations` and `stage_trace`; the summary annotation and legacy
  `summary` mirror must agree. Old items without either render normally.
- Add optional `source_message` with schema version, origin
  `routed|publication`, endpoint reference, channel, message ID, sequence,
  sender URN, optional output ID, and typed attribution. Routed items require
  real session/turn bindings. Ordinary publication items omit them, disable
  runtime reply, and keep their actual sender. No invented turn or agent grant.
- Retain the original text byte-for-byte as UTF-8; traces and summaries cannot
  replace it. Bound annotations and traces to 16 entries each, stage IDs and
  versions to 128 characters each, summary text to 600 characters, closed failure
  codes to 128 characters, and the combined encoded annotation/trace fields to
  8 KiB. Durations are nonnegative integer milliseconds. The implementation must
  enforce the encoded-byte ceiling as well as JSON Schema character limits,
  within the host's actual transport budget; later payload kinds need an
  explicit typed schema, not an unbounded arbitrary map.
- Update the packaged request schema, public embedded schema/constants, typed
  service request/view, renderer, prose contract, and conformance fixtures as
  one change. A changed `contract_digest` requires a **definition version** bump
  and contract-lock update under ADR 0003, not just a revision bump. Preserve
  old item reads; document the existing pending-interaction blast radius rather
  than normalizing old persisted requests into a new identity.
- Correct contract §3 to describe consumer admission of routed messages without
  an attention filter, §2/§4 for publication identity and summary/annotations,
  and §5 for the reply protocol below. Do not represent proposed callback or
  session-disappearance plumbing as current implementation.

### 7. User-driven replies and truthful delivery projection

Only an explicit operator resolution produces a reply. The messaging plugin
reads durable resolutions through the public `turn_await` contract and maps the
Tangent item back to its saved **channel publication ID**. It submits exactly
one dedicated Tether `Reply` with a stable key derived from Tangent's resolution
ID and saved caller identity. It does not use compatibility `message_send`,
runner `SendTurn`, notification wake, or a private outbox that injects a turn.
The summarizer cannot invoke this path.

Persist the prepared reply and key before submission, then the returned reply ID
and acceptance state. Ambiguous HTTP acceptance retries the same key/body/flag;
a 202 or duplicate receipt means queued acceptance, not runtime delivery. Poll
`ReplyDelivery` with bounded backoff; save the actual state/reason/attempts and
original/target/delivered-to sessions. A handed-off target alone is not delivery;
`delivered/turn_failed` means Tether observed runtime activity, not task success.

`0133` owns a plugin participant-guarded GET route under
`/api/plugins/messaging/` exposing a bounded delivery projection by item ID.
The Tangent UI can render that generic projection without importing Tether's
client. Tangent's own resolution delivery state remains separate: call
`turn_ack` only after actual Tether `delivered`, never on mere queue acceptance.
Undeliverable replies remain preserved, unacknowledged, with their terminal
plugin projection; polling the same Tangent resolution must not resubmit them.
If generic persisted host projection is needed, specify a public typed surface
in `0133` rather than accessing the host database or claiming one already exists.

Default `interrupt=false`. Expose an interrupt choice only when the **sender
session's** capabilities support reply and interrupt. Capability success does
not prove a live signal was produced. Surface typed refusal/no-effect separately
from accepted interrupt timeout (which still queues a reply). An explicit retry
of an undeliverable logical reply is a new user action with a new key, not a
daemon retry; retain the old outcome. Unknown delivery states stay unknown.

Identity currently uses asserted/observe-mode information; reply-specific
authorization is not installed. This is the owner's accepted single-user MVP
limit, not verified human authority or an auto-reply grant. `0051`, Tether
`0116`, and the paused verified-identity work own closure before 1.0.

## Consequences and downstream work

This design adds a persistent adapter database and a contract migration. It
keeps Tangent domain-free and reply injection single-owned, but each new message
can incur an AI call and a slow stage delays later publication presentation.
Fail-open preserves visibility at the cost of sometimes presenting no summary.
Provider failures, source purge, an oversized original, or sink refusal prevent
blanket lossless/exactly-once claims. Private original custody and provider
exposure must be understood during the owner's MVP rollout.

| Task | Required implementation or corrected acceptance |
|---|---|
| `CW-20261002-0132` | New libs message-pipeline module; portable stage/store contracts, versioned keys, cancellation vs failure, ordered settlement and synthetic conformance. Concrete SQL remains adapter-owned. |
| `CW-20261002-0070` | New manifest-v2 subprocess plugin; DataDir ledger, committed channel replay, immutable enqueue retry, kind/identity mapping, ordinary-publication nonreplyability and explicit admission/sink refusal. |
| `CW-20261002-0072` | Stateless bounded Tether AI gateway adapter, replacing the older mandatory runner-supervised-session wording; no acting tools, schema validation, bundled instruction document, fail-open evidence. |
| `CW-20261002-0134` | Contract `1.1` and definition bump, optional annotation/trace/source projection, old-item reads, summary/original presentation and failure marker. |
| `CW-20261002-0133` | Dedicated reply/key/receipt persistence, separate actual-delivery projection, ack only after delivered, capability-gated interrupt and explicit retry. |
| `CW-20261002-0136` | Fake channel/gateway/sink/dispatcher cases first; replay/crash-boundary dedupe, ordinary/nonreplyable, purge, oversized/refused admission, stage timeout/refusal and actual delivery states. Live proofs only through the scoped routing task, with no fabricated runtime signals. |
| `CW-20261009-0048` | Client `v0.10.0` adoption; include `ReplyDelivery` as well as the listed channel/capability/Reply methods. No source-version update implied by this ADR. |
| `CW-20261009-0049` | Approved `owner-inbox` routing for the specified launch-manager/architect/planner actors only; verify deployed/per-session capabilities and future-launch vs existing-session pickup. This ADR performs no catalog edit. |
| `CW-20261009-0052` | Install actual released compatible host/plugin/library versions, verify tool-free gateway and private state/config, dogfood, record restart/recovery limits and obtain owner sign-off. Neither an ADR nor source smoke is rollout. |
| `CW-20261002-0135` | Retire the unwired tetherbridge only after verification and rollout; retain the generic turns service. Do not close `CW-20261008-0126` or `CW-20261001-0422` here. |

Wave B can implement the library and rendering contract after this ADR lands;
plugin/summarizer, reply, verification, and rollout follow their recorded task
dependencies. Later filtering, options, standing directions, and JEV fit typed
stages without activating them now. An existing JEV key is never copied into
configuration in git, a task, trace, or message.

## Open questions and deferred owner decisions

- DEC-052: durable home of user rules and the long-term inbox/chat surface. The
  plugin-config/Tangent choice is provisional and must stay replaceable.
- Before 1.0 (`0051`): verified identity and reply authorization, disclosure
  boundaries, original/annotation/reply retention, scale, policy/UI ownership.
  No authentication, redaction, retention, or sandbox guarantee is inferred here.
- Rollout (`0049`, `0052`): actual enabled actor routes, deployed capabilities,
  gateway/provider availability and budgets, compatible released versions, and
  unsupported runtime signal combinations. These need evidence, not new MVP
  policy choices in this ADR.
- A routed original exceeding the current enqueue limit requires an explicit
  bounded content/presentation contract before that traffic can be accepted.
  Keeping it pending is the interim behavior; truncation is not an answer.

No unresolved Chrispian-owned choice prevents this design slice from landing.
Deciding a deferred owner question or widening the audience requires separate
direction; consumer implementation is still governed by its own allocated task.
