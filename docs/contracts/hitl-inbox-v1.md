# Tangent HITL Inbox contract v1.0

**Status:** Implemented

**Contract version:** `1.0`

**Definition kind:** `tangent.hitl-item`

**Decision basis:** [ADR 0001](../adr/0001-lifecycle-boundaries.md)

**Machine-readable contract:**
[`packages/tangent.hitl/hitl-item/request.schema.json`](../../internal/envelope/extensions/packages/tangent.hitl/hitl-item/request.schema.json)

This contract defines one item in Tangent's persistent operator-owned `/inbox`
inbox. It covers approval and persistent-attention requests, participant
resolutions, non-resolution terminal outcomes, stable handles, and retrieval
projections. The four MCP operations that expose it are implemented separately;
the schema and semantics here are their source of truth.

`tangent.approval-queue` is not this contract. It remains the existing bounded
batch workflow with one explicit submit boundary and its current wire shape.

## Canonical mappings

| HITL term | Canonical ADR term | Rule |
|---|---|---|
| inbox | Surface | The default inbox is one operator-scoped durable Surface. |
| `item_id` | `interaction_id` | The values are identical. `item_id` is ergonomic HITL vocabulary, not a second identity. |
| `queue_sequence` | `surface_sequence` | The values are identical. The generic interaction store allocates it transactionally; HITL must not create another sequence authority. |
| item `state` | Interaction state | Uses the exact ADR spellings, including `in_progress`. |
| participant result | Resolution | A valid approval, denial, or acknowledgement creates one immutable ResolutionRecord. |
| get/await result | retrieval projection | Reading a terminal outcome is not participant resolution, delivery acknowledgement, or downstream acceptance. |

The request definition and every command/result carry `contract_version: "1.0"`.
The definition binding pins `tangent.hitl-item` version `1.0` and the schema
digest. A missing or unsupported version is a validation error; an interaction
is never reinterpreted against a later schema.

### Adapter and code-generation targets

The schema bundle's named `$defs` are stable generation entry points:

| Operation | Input definition | Successful result definition |
|---|---|---|
| `tangent.hitl_enqueue` | `HITLItemRequestV1` | `HITLItemHandleV1` |
| `tangent.hitl_get` | `HITLGetCommandV1` | `HITLRetrievalResultV1` |
| `tangent.hitl_await` | `HITLAwaitCommandV1` | `HITLRetrievalResultV1` |
| `tangent.hitl_withdraw` | `HITLWithdrawCommandV1` | `HITLTerminalOutcomeV1` |
| participant resolve command | `HITLResolutionCommandV1` | `HITLTerminalOutcomeV1` |

MCP adapters extract the relevant object definition so every tool input schema
has an explicit object root. TypeScript generation emits the named definitions
and discriminated unions from their `enum` and `oneOf` constraints. Typed
conflicts use `HITLStaleRevisionErrorV1` or
`HITLIdempotencyConflictErrorV1` as applicable.

These entry points are now declared in the package's own
[`manifest.yaml`](../../internal/envelope/extensions/packages/tangent.hitl/hitl-item/manifest.yaml)
under `named_definitions`, so the table above has a machine-readable
counterpart the registry serves through `tangent.definition_get`. The
`response_schema` and `error_schema` the manifest references are projections of
this same bundle — `HITLResponseV1`, and a `oneOf` of the two typed conflicts —
committed as files and drift-tested against it.

`ui/src/lib/hitl-api.ts` remains hand-written. It carries a
`@definition-source` stamp over this bundle's `$defs` plus a test asserting the
match, which is the floor
[ADR 0003 §4.7](../adr/0003-definition-and-package-ownership.md) accepts when
full generation is out of scope. A stamp mismatch means the file must be
reviewed against the new bundle before the stamp is updated.

## Item request

`tangent.hitl_enqueue` accepts the root `HITLItemRequestV1` schema. Required
fields are:

| Field | Meaning |
|---|---|
| `contract_version` | Exactly `1.0`. |
| `kind` | `approval` or `attention`. |
| `idempotency_key` | Stable caller-chosen key, unique within immutable `caller_scope`. |
| `title` | Short operator-facing label, 1–160 characters after trimming. |
| `summary` | Concise context, 1–600 characters after trimming. |
| `request` | The action or acknowledgement requested, 1–4,000 characters after trimming. |
| `source` | Required caller assertion containing `application_id` and `agent_id`; optional display labels do not establish identity. |

Optional fields are `recommendation`, approval-only `impact`, kind-specific
`action_labels`, `correlations`, `details_markdown`, `evidence`, and an absolute
RFC 3339 `expires_at`. An omitted expiry means the item does not expire by
default. The application validator rejects required strings that become empty
after trimming and rejects a serialized request larger than 512 KiB; those two
aggregate rules supplement JSON Schema's structural and per-field bounds.

Minimal approval request:

```json
{
  "contract_version": "1.0",
  "kind": "approval",
  "idempotency_key": "torque:CW-20260904-0012:r17",
  "title": "Approve durable HITL service merge",
  "summary": "Restart and concurrency tests pass.",
  "request": "Approve or deny merging the durable HITL service change.",
  "source": {
    "application_id": "codex",
    "agent_id": "msg://agent/codex/worker-7"
  }
}
```

Minimal attention request:

```json
{
  "contract_version": "1.0",
  "kind": "attention",
  "idempotency_key": "nanite:session-01K4:turn-28:attention-1",
  "title": "Migration completed with one warning",
  "summary": "One legacy row retained uncertain provenance.",
  "request": "Acknowledge the warning, optionally leaving a note or reply.",
  "source": {
    "application_id": "nanite",
    "agent_id": "release-agent",
    "agent_label": "Release agent"
  }
}
```

The schema contains fuller approval and attention examples, including custom
labels, correlations, expiry, and every evidence family.

### Approval-only presentation fields

`impact.approve` and `impact.deny` describe what the caller may choose to do
after receiving the result. They do not authorize Tangent to perform that work.
Approval labels may customize `approve`, `approve_with_note`, `deny`, and
`deny_with_note`. These are presentation strings only:

- Approve and Approve with note both produce `decision: "approved"`.
- Deny and Deny with note both produce `decision: "denied"`.
- A with-note affordance requires a non-empty `note`; it never creates another
  outcome enum.

Attention labels may customize `acknowledge`, `acknowledge_with_note`, and
`reply`. An attention request cannot contain approval `impact` or approval
labels.

### Source assurance and correlations

Every value inside request `source` is an immutable caller assertion. In the
first direct-loopback MCP slice Tangent stores it with asserted assurance; it
does not call an agent label, MCP session, process, browser tab, or item URL
authenticated identity. The operator participant binding is the separate
`local-operator` binding with `loopback-unverified` assurance from ADR 0001.
For new direct-MCP enqueues, the adapter establishes a stable caller scope from
`source.application_id` inside the host-assigned `standalone-local` authority;
it does not use `agent_id` or a transport-session ID as the idempotency scope.
The request shape is unchanged — only the derived spelling moved, from
`direct-loopback:<app>` to `standalone-local:<app>`, and the old spelling still
reads as the same caller through a fixed alias with no data rewrite
([ADR 0004](../adr/0004-caller-participant-and-room-access-authority.md) §3.5).

> **`standalone-local` partitions are advisory, not a security boundary.** The
> partition is the caller's own declared `application_id` and nothing verifies
> it, so any local caller can assert any partition. Isolation is enforced only
> *across* authorities, where the prefix is host-assigned. A partition prevents
> one agent's retry from withdrawing another agent's item; it does not prevent
> one agent from claiming to be another.

A trusted Tether or future authenticated adapter may attach a separately
verified caller binding and transport receipt. It must not rewrite the original
source assertion or silently upgrade its assurance.

Post-enqueue Get, Await, and Withdraw commands carry a `caller` assertion with
a required stable `application_id` and optional `principal_ref`. On direct
loopback, the adapter resolves the durable caller scope from that application
ID under the host-assigned `standalone-local` authority, with
`loopback-unverified` assurance. With a verified gateway, the
out-of-band authenticated binding is authoritative: the payload assertion is
correlation only, must agree with the binding, and can never downgrade or
replace it. The resolved scope must match the interaction's immutable
`caller_scope`; an item ID, agent label, MCP session, or transport request never
grants access. Terminal Get/Await audit rows persist that resolved scope as
`requester_scope`.

`correlations.project`, `.task`, and `.session` are typed opaque references with
an owning `authority`, stable `id`, and optional `revision`/`label`. Additional
correlations cover agent turns, workflow runs, steps, conversations, and
business objects. A correlation grants no permission and never gives Tangent
authority to mutate the referenced object.

## Evidence

An item may contain at most 24 evidence blocks. Each block has a required
discriminator and label:

| `type` | Required content | Bound and interpretation |
|---|---|---|
| `markdown` | `content` | Inline Markdown, at most 64 KiB; render through the host sanitizer. |
| `text` | `content` | Inline plain/preformatted text, at most 64 KiB. |
| `diff` | `content` | Unified diff text, at most 128 KiB; optional base/head labels. |
| `tangent_reference` | `surface_id` | Stable Tangent surface reference with optional interaction and revision. |
| `artifact_ref` | `authority`, `artifact_id`, plus immutable `revision` or `digest` | Metadata for authority-mediated retrieval; optional media, sensitivity, size, capability, expiry, retention, and safe-preview metadata. |

The 512 KiB aggregate request bound still applies. An `artifact_ref` deliberately
has no `path` field. In v1, `artifact_id` and `safe_preview_artifact_id` are
opaque authority-local tokens matching `[A-Za-z0-9][A-Za-z0-9._@-]*`; path
separators and URI syntax are rejected. A raw local path, a `file:` URL, a
label, or a caller's ambient filesystem access is never artifact identity or
retrieval authority. The named authority resolves the reference and Tangent
exposes only a policy-mediated rendering/retrieval path.

## Enqueue, FIFO, and idempotency

On successful enqueue, Tangent atomically stores the immutable request and
definition binding, advances it to `staged`, commits its presentation
obligation, and allocates a unique monotonically increasing
`surface_sequence`. The HITL response exposes that value as `queue_sequence`.

`queue_position` is a current projection among nonterminal inbox items ordered
by `queue_sequence`; it may change when earlier items terminate. It is never an
identity or ordering authority. Inspection, focus, reconnect, priority hints,
and resolution do not rewrite `queue_sequence`. Resolved and otherwise
terminal items leave the default pending projection but remain available in
history.

The uniqueness boundary is `(caller_scope, idempotency_key)`:

- the same key and same canonical request digest returns the original handle,
  sequence, and current state;
- the same key with different immutable request content returns
  `idempotency_conflict` naming the existing `item_id` and changes nothing;
- keys from different caller scopes do not collide;
- transport, process, or MCP-session identity never changes this boundary.

For that comparison, Tangent first performs schema and semantic validation,
trims fields whose contract says non-blank, and seals the resulting request as
canonical JSON with object keys sorted and array order preserved. The request
digest covers every immutable request field except `idempotency_key` itself.
Presentation state, queue position, transport metadata, and later definition
materialization are not part of the digest.

The `HITLItemHandleV1` definition specifies the immediate response. It returns
`surface_id`, `item_id`, canonical `state`/`revision`, durable
`queue_sequence`, projected `queue_position`, `/inbox`, and the item deep link.
A first enqueue returns `staged` with an integer queue position. A duplicate
enqueue returns the original item's current state; if it is already terminal,
the handle carries that terminal state and `queue_position: null`. Handles do
not embed terminal payloads, so the caller uses Get or Await to retrieve the
immutable outcome.

## Participant resolution

The presentation channel submits `HITLResolutionCommandV1` with `item_id`, the
expected interaction revision, the exact presented projection revision, and a
kind-matched response:

```json
{
  "contract_version": "1.0",
  "item_id": "interaction_01K4APPROVAL",
  "expected_revision": 4,
  "presented_projection_revision": 3,
  "response": {
    "kind": "approval",
    "decision": "approved",
    "note": "Concurrency evidence reviewed."
  }
}
```

Approval responses are exactly `approved` or `denied`, plus an optional
non-empty note. Attention responses are exactly `acknowledged`, plus optional
non-empty `note` and/or `reply`. The service validates the response kind against
the immutable request kind. If `note` or `reply` is present, the service trims
it and rejects an empty result before attempting the terminal compare-and-set.

Only `presented` or `in_progress` items are respondable. Resolution validates
the expected interaction and presented-projection revisions and the participant
binding. It then compare-and-sets the item to `resolved` and atomically writes
the immutable ResolutionRecord and initial queued delivery obligation. A
different or repeated terminal command can never overwrite or reopen it.

If either supplied revision is stale, the command returns
`HITLStaleRevisionErrorV1`. Its `revision_kind` identifies `interaction` or
`presented_projection`; it carries expected/actual revisions, current state,
and, when safe, the immutable terminal outcome. The client resynchronizes; it
must not blind retry against a newer projection. A response lost after commit
is recovered through Get/Await rather than reapplying the action.

The stale-error schema is itself discriminated: Resolve may report either
interaction or presented-projection staleness, while Withdraw can report only
interaction staleness because it never accepts a projection revision.

## Withdrawal, expiry, and other terminal outcomes

`tangent.hitl_withdraw` accepts `HITLWithdrawCommandV1`, including its required
`caller` assertion. The adapter applies the direct-loopback or verified-gateway
rule above, and the resolved caller must equal the interaction's immutable
`caller_scope`; an optional expected revision enables stricter stale detection.
A successful withdrawal atomically transitions the nonterminal item to:

```json
{
  "contract_version": "1.0",
  "state": "canceled",
  "item_id": "interaction_01K4WITHDRAWN",
  "interaction_revision": 3,
  "cause": "caller_withdrawn",
  "reason": "The proposed deploy was superseded.",
  "terminated_at": "2026-09-04T03:31:00Z"
}
```

Repeating withdrawal after `caller_withdrawn` returns the existing immutable
outcome. If resolution, expiry, participant cancellation, or another terminal
disposition won the compare-and-set, withdrawal returns the current terminal
outcome as a conflict and does not replace it.

At or after `expires_at`, Tangent's policy evaluator may compare-and-set a
nonterminal item to `expired` and queue its terminal notification in the same
transaction. An MCP deadline, Await timeout, browser disconnect, WebSocket
loss, or process shutdown does not expire, withdraw, or cancel the item.

The canonical terminal union is `resolved`, `canceled`, `expired`, `failed`, or
`superseded`. Cancellation retains its typed cause
(`caller_withdrawn`, `caller_canceled`, `participant_canceled`,
`administrator_canceled`, or `surface_policy`). All terminal outcomes are
immutable and apply to one item only; there is no all-items submit boundary.

## Get and Await retrieval

`tangent.hitl_get` accepts `HITLGetCommandV1` and immediately returns
`HITLRetrievalResultV1`. `tangent.hitl_await` accepts `HITLAwaitCommandV1`; its
optional `wait_ms` must be 0–50,000 ms and defaults to 30,000 ms. Values outside
that range are rejected rather than clamped. The ceiling preserves response
headroom below Tangent's 60-second HTTP write timeout. Await returns when the
item is terminal or the wait elapses:

| `mode` | `wait_status` | Meaning |
|---|---|---|
| `get` | `not_waited` | Immediate durable projection. |
| `await` | `terminal` | The item was already terminal or became terminal while waiting. |
| `await` | `timeout` | The wait ended; `item` is the latest durable nonterminal projection. |

`HITLRetrievalResultV1` is a discriminated union of exactly those three rows:
Get cannot report an Await status, `await/terminal` requires a terminal item,
and `await/timeout` requires a nonterminal item. Both commands require the
`caller` assertion and the same resolved-scope access check as Withdraw.

The embedded `HITLItemViewV1` includes the immutable request snapshot, canonical
state/revision, sequence/position, timestamps, and the terminal outcome when
terminal. `queue_position` is `null` for terminal history.

Get and Await never mutate interaction state. Returning a terminal outcome
records a `TerminalOutcomeRetrievalRecord`; returning a pending projection or
Await timeout does not. Ending an Await request cancels only that waiter.
Retrieval is not a participant decision, delivery acknowledgement, or proof
that the caller applied a business effect.

## Downstream ownership

`approved`, `denied`, and `acknowledged` record what the operator submitted and
what Tangent durably captured. After retrieving that result, the caller decides
whether and how to perform its own transition. Tangent never merges code,
changes a Torque task, advances a Hadron workflow, deploys, publishes, sends a
business message, or reports those effects as completed.

If a downstream owner later supplies a typed receipt, Tangent may retain its
opaque `DownstreamOutcomeRef` separately from the ResolutionRecord. That receipt
cannot alter the human result.

## Versioning and compatibility

> **Forward-looking language in this contract is non-committed direction.**
> Sentences about a later slice, a verified gateway binding, a typed
> downstream receipt, or an authenticated adapter describe shapes the v1
> contract is designed to accommodate — not behaviour this build has. The
> shipped authority model is
> [ADR 0004](../adr/0004-caller-participant-and-room-access-authority.md),
> under which every direct loopback caller is `standalone-local:<app>` and
> its partition is advisory.

- Additive optional fields require a compatible minor contract/definition
  version and an exact persisted DefinitionBinding.
- Removing, renaming, changing meaning, widening authority, or adding a terminal
  enum requires a new major contract version.
- Unknown fields are rejected in v1 so misspellings do not silently become
  untrusted operational metadata.
- Existing `tangent.session_*`, workflow adapters, and
  `tangent.approval-queue` retain their current request/response shapes and
  behavior. They are not migrated to this contract by implication.
