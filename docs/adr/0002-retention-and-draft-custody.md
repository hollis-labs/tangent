# ADR 0002: Retention and Draft Custody Policy

**Status:** Accepted

**Date:** 2026-09-04

**Approved:** 2026-09-04 by Chrispian, during the Tangent foundation orchestration session

**Task:** `CW-20260825-0059`

**Baseline reviewed:** `0a45caa`

**Source:** [`../interactive-collaboration-direction.md`](../interactive-collaboration-direction.md)

**Composes with:** [`0001-lifecycle-boundaries.md`](0001-lifecycle-boundaries.md),
[`0003-definition-and-package-ownership.md`](0003-definition-and-package-ownership.md),
[`0004-caller-participant-and-room-access-authority.md`](0004-caller-participant-and-room-access-authority.md)

## Context

ADR 0001 separated the Surface, Interaction, Connection, and Delivery
lifecycles and made the durable records immutable. It deliberately delegated
"retention durations" and "redaction" to a later decision, and it recorded in
its own consequences that "persisting connection audit and draft revisions adds
retention/redaction obligations". This is that decision.

At `0a45caa` the actual behavior is:

- Every caller request payload is stored in full and forever. Legacy rooms
  keep it in `envelopes.request_payload`; canonical interactions keep it in
  the immutable `interactions.request_snapshot`; the async open path keeps a
  **second permanent copy** in `surface_open_requests.request_snapshot`.
- Every participant response is stored in full and forever, in
  `envelopes.response_payload` and in the immutable
  `resolutions.response_payload`.
- `rooms.phase_outputs` is one JSON map that mixes caller-seeded rows,
  participant drafts, presentation state, notes, whiteboard scenes, and
  business-shaped data under fifteen phase IDs.
- Nine browser `localStorage` key families hold participant drafts —
  including free text, decisions with comments, form answers, and full tldraw
  scene snapshots — outside the server's deletion, backup, and audit
  boundary. One `sessionStorage` key holds a diagnostic connection label.
- `internal/interaction` has a `SaveDraft` operation, a `DraftRevision` record
  with `sensitivity` and `expires_at` columns, and a
  `draft_revision_tombstones` table. **None of them has a caller.** No
  transport reaches `SaveDraft`, and nothing anywhere writes a tombstone. The
  only draft custody that actually runs today is the browser's.
- There is **no deletion path at all** for canonical records. Nine
  `*_immutable_delete` triggers abort `DELETE`, and SQLite fires them for
  foreign-key cascade actions too. Deleting a surface fails on
  `surface_events`; deleting an interaction fails on `interaction_events`;
  `surface_open_requests` is locked twice, by an `ON DELETE RESTRICT`
  reference and by its own trigger. Meanwhile the legacy `rooms`/`envelopes`
  pair, which holds the same payloads with none of the audit value, deletes
  cleanly by cascade. Deletability today is exactly backwards.

The result is a single-user local host that accumulates source code,
unpublished writing, personal data, deployment plans, and operator notes
indefinitely, in two places, with no way to remove any of it and no statement
about what a backup contains.

This ADR defines effective custody classes, the precedence that computes an
effective policy from four sources, the mechanism by which content is removed
from an immutable record, and the migration behavior for rooms that already
exist. It does not define authentication, does not add a capability system,
and does not authorize any change to the shipped workflow response shapes or
the durable `/hitl` contract.

## Decision

### 1. Custody is a pair, not a single scale

The direction document lists five custody classes. Two of them answer
different questions, and collapsing them produces a lattice that cannot be
ordered. An effective custody declaration is therefore a pair:

```text
custody = { retention: <class>, content_mode: inline | external-reference }
```

| Retention class | Meaning |
|---|---|
| `ephemeral` | The content never reaches durable storage. Not in SQLite, not in browser storage, not in a backup, not in a log. It exists in server memory for the life of the request and in browser memory for the life of the view. |
| `interaction` | The content is retained while the interaction is non-terminal, plus a bounded recovery window after it reaches a terminal state. At the end of that window the content is redacted in place and the record skeleton survives. |
| `surface` | The content is retained with its surface until the surface reaches `closed` or `expired`, plus a bounded window, and is then removed with the surface. |
| `durable-record` | The content is retained indefinitely as part of an immutable record. It is removed only by an explicit, audited erasure operation initiated by the local user. |

| Content mode | Meaning |
|---|---|
| `inline` | Tangent stores the content itself. |
| `external-reference` | Tangent stores typed identifiers, digests, media type, size, authority, and expiry — never the content. Content is resolved at read time through a host-registered capability and is never persisted or cached. |

Single-token shorthand is accepted so callers keep one field:
`"ephemeral"`, `"interaction"`, `"surface"`, and `"durable-record"` mean that
retention with `content_mode: inline`; `"external-reference"` means
`{retention: interaction, content_mode: external-reference}`.

The pair is the canonical form everywhere custody is expressed, including the
`retention_class` field of an interaction definition manifest
([ADR 0003 §2.6](0003-definition-and-package-ownership.md)). A manifest that
authors the single token `external-reference` is read as the pair above; it
does not declare a fifth position on the retention scale.

### 2. Custody applies to field classes, not to rows

Custody never removes a record. It removes content from within a record whose
identity and lifecycle remain intact. This is the composition point with ADR
0001: that ADR makes records immutable, and this ADR only ever replaces a
payload column with a typed tombstone.

Every persisted column belongs to exactly one field class:

| Field class | Contents | Custody eligibility |
|---|---|---|
| **Identity and lifecycle** | IDs, states, revisions, sequences, timestamps, terminal causes, idempotency keys, definition bindings, integrity digests. | **Always `durable-record`. Exempt from the precedence clamp and never redacted.** Removing them breaks idempotency, compare-and-set, and the audit chain. |
| **Caller payload** | The request the calling application submitted. | Subject to the declared custody. |
| **Participant draft** | Work in progress that has not become a terminal response. | Subject to the declared custody. Additionally governed by the browser rule in section 5. |
| **Participant resolution** | The immutable terminal response. | Subject to the declared custody, with a `durable-record` floor by default. |
| **Presentation and operational state** | Phase outputs, surface metadata, connection labels, projection state. | Subject to the declared custody; defaults to the surface's class. |
| **Delivery evidence** | Destination bindings, adapter receipts, transport correlation. | `durable-record` for the typed fields; the freeform text carried inside them is treated as caller payload. |
| **Audit metadata** | `*_events.metadata`. | `durable-record`, and restricted to typed keys. Freeform participant or caller text may never be written into an event's metadata. |
| **External reference** | Typed refs, digests, authorities, artifact IDs. | `durable-record`. These are what survives redaction. |
| **Adapter freeform text** | `terminal_reason`, `error_message`, `closed_reason`. | Treated as **caller payload**, not as metadata, because their content is supplied by a caller or an external adapter and is unbounded. |

A redacted payload column becomes:

```json
{"redacted": true, "policy_ref": "<policy>", "at": "<rfc3339>",
 "content_digest": "<sha-256 of the removed bytes>", "actor_ref": "<actor>"}
```

The digest is retained so a later question — "was this the payload that
produced that resolution?" — is still answerable after the content is gone.

### 3. Precedence: strictest wins, clamped by the host, pinned at submission

Four sources contribute to an effective policy.

| Source | Authority | Expressed as |
|---|---|---|
| **Host policy** | The local user's configuration of this Tangent installation. Declares a **floor** (the minimum retention the host requires) and a **ceiling** (the maximum retention the host permits). | Host config. |
| **Definition default** | The definition publisher, for its kind. | The definition manifest, pinned into `definition_bindings`. |
| **Caller request** | The calling application, per interaction. | `policy.custody` in the submission. |
| **User override** | The person at the browser, at present time. | A surface or interaction operation. |

The effective retention is:

```text
effective_retention =
    clamp( min(definition_default, caller_request, user_override),
           host_floor, host_ceiling )
```

where `ephemeral < interaction < surface < durable-record` orders retention
from least to most durable. Any party may **tighten** — ask for less
retention — and the host floor is what stops a caller or definition from
tightening away an audit obligation the host requires. Only the host may
loosen, and only by raising its own ceiling.

The effective content mode is:

```text
content_mode = external-reference if ANY source requests it, else inline
```

Nobody can turn `external-reference` off. A definition or plugin can never
silently weaken host or user policy in either dimension.

The effective policy is computed **once**, at the `submitted -> validated`
transition, and written into the immutable `interactions.policy` as a
`custody` object alongside the existing `request_digest`, `expires_at`, and
`policy_ref` fields. It is never recomputed. A later host configuration
change, a later invitation, and a later definition version do not
retroactively alter an interaction's custody. Changing custody after the fact
requires an explicit, audited retention operation (section 6), not a silent
re-evaluation. This is the same pinning rule ADR 0001 §3 applies to the
`DefinitionBinding`, applied to policy.

The surface carries its own custody in `surfaces.policy`, computed the same
way at surface creation, and it acts as the ceiling for every interaction
opened on it.

### 4. Default custody, and what each class obliges

**A new interaction that declares nothing takes `{retention: interaction,
content_mode: inline}`.** Its caller payload is redacted 30 days after the
interaction reaches a terminal state. This is the change that fixes "rooms
retain full payloads by default". It applies to interactions submitted after
this decision lands; every existing room and legacy row keeps `surface`
retention under section 12 and is unaffected.

Window lengths, all host-configurable, with these values as the defaults:

| Window | Default | Measured from |
|---|---|---|
| `interaction` recovery window | 30 days | The interaction's terminal transition. |
| `surface` window | 90 days | The surface reaching `closed` or `expired`. |
| Browser draft TTL | 14 days | Last write to the key. |
| `ephemeral` | 0 | Nothing is written, so nothing is measured. |

| | `ephemeral` | `interaction` | `surface` | `durable-record` | `external-reference` |
|---|---|---|---|---|---|
| **Retention** | Server memory only, for the request. | Until terminal, plus the 30-day recovery window. | Until the surface is `closed`/`expired`, plus the 90-day surface window. | Indefinite. | Refs and digests follow the paired retention class; content is never retained. |
| **Deletion** | Nothing to delete. | Automatic redaction at window expiry; user-initiated erasure at any time. | Removed with the surface; user-initiated erasure at any time. | Only by explicit, audited user-initiated erasure. Never automatic. | The ref is removed with its record. Tangent does not and cannot delete the referenced source. |
| **Backup** | Never present. | Included while retained. A backup taken before a redaction still contains the content; the retention operation records that and cannot fix it. | Same as `interaction`. | Included. The backup is the recovery guarantee. | Refs and digests are included; content never was. |
| **Redaction** | N/A. | Content column replaced by the typed tombstone; identity, lifecycle, and digests survive. | Same. | Same, but only under an explicit operation, never on a timer. | Nothing to redact. The ref is already the redacted form. |
| **Client cache** | **Forbidden.** No `localStorage`, no `sessionStorage`, no IndexedDB. | Allowed, with the 14-day TTL, unless the sensitivity rule in section 5 forbids it. | Allowed, with the same TTL and rule. | Allowed, with the same TTL and rule. Client cache is never the record of truth. | Refs may be cached; resolved content may not. |
| **Telemetry** | Identity and lifecycle only. | Identity and lifecycle only. | Identity and lifecycle only. | Identity and lifecycle only. | Identity, lifecycle, authority, and digest. |

No class permits payload content in telemetry. That is a floor, not a per-class
choice — see section 8.

**Resolution payloads are redactable, by explicit user-initiated erasure only.**
They are never redacted on a timer, and erasure always preserves
`response_kind`, `resolutions.integrity_digest`, `resolutions.participant_ref`,
and every timestamp. The alternative — resolutions are permanently unerasable —
would make Tangent a permanent record of every approval the user ever clicked
with no way out. The `durable-record` floor on a resolution means "no automatic
removal", not "no removal".

**Custody is declared per interaction**, not per field. A definition manifest
may additionally mark individual request and response fields as
**reference-only**, which is what gives the `external-reference` refusal in
section 9 something to check against; that field marking lives in the manifest
([ADR 0003 §2.2](0003-definition-and-package-ownership.md), `request_schema` /
`response_schema`, and §2.6 `inline_payload_limit_bytes`). Full per-field
custody is deferred until a definition actually needs it.

### 5. Sensitive interactions can prohibit browser persistence

Every projection sent to a presentation client carries the effective custody
descriptor:

```json
{"custody": {"retention": "interaction", "content_mode": "inline",
             "browser_persistence": "allowed" | "session_only" | "forbidden",
             "cache_ttl_seconds": 1209600}}
```

`browser_persistence` derives, and is not independently declarable:

- `ephemeral` retention, or `sensitivity` at or above the host-configured
  threshold, yields `forbidden`;
- an interaction whose surface is `session_only` yields `session_only`;
- otherwise `allowed`.

A definition manifest's `client_persistence_prohibited` and `draft_custody`
fields ([ADR 0003 §2.6](0003-definition-and-package-ownership.md)) are inputs
to that derivation on the tightening side only. A manifest may force
`forbidden`; a manifest declaring `browser-local` custody never overrides a
host or user policy that forbids it. This is the same one-directional rule as
section 3: any source may tighten, only the host may loosen.

Three enforcement points are required, because a client-side flag alone is not
enforcement:

1. **One gate, not nine.** The nine draft-storage modules in `ui/src/lib/`
   are replaced by a single custody-aware gate that takes the effective policy
   and refuses to write. No renderer calls `localStorage` directly.
2. **The projection withholds what it forbids.** When persistence is
   forbidden, the server-side projection omits prefill content that a client
   could otherwise re-derive and store. The renderer cannot cache what it was
   not sent.
3. **Tightening deletes what already exists.** On receiving a `forbidden` or
   `session_only` projection for a key that is already present, the client
   deletes that key before rendering. Policy that tightens after drafts were
   written must reach the drafts that were written.

**This is a data-at-rest boundary, not a security boundary.** Tangent is a
loopback single-user host; the browser is not modeled as an adversary. The
claim being made is that Tangent does not leave sensitive content lying in
browser storage outside its own deletion boundary — not that a determined
local process is prevented from reading it. ADR 0004 makes the same honesty
claim about `standalone-local` partitions.

Two defects in the current keys are fixed at the same time:

- The nine families use two incompatible shapes — five as
  `tangent:<name>-draft:v1:…` and four as `tangent.<name>.draft.v1:…`. Any
  "purge local Tangent state" operation must sweep both, and one written
  against a single prefix will silently miss four workflows. All nine
  normalize to `tangent:<workflow>:draft:v2:<surfaceID>:<instanceID>`.
- `file-picker` and `wizard` interpolate their IDs without
  `encodeURIComponent`, unlike the other seven. Normalization encodes every
  segment.

**v1 keys are migrated, then deleted.** A one-time client sweep on first load
after the upgrade rewrites the five colon-shaped and four dot-shaped families
into the single v2 shape, deletes the v1 keys once rewritten, deletes keys
whose surface no longer exists server-side, and deletes keys older than the
TTL. Deleting without migrating would lose in-progress drafts for anyone
mid-workflow across the upgrade; migrating without deleting would leave the
old keys behind forever, outside any subsequent purge that only knows the v2
shape.

### 6. Deletion and redaction need a mechanism, because the schema forbids both

This was verified against the schema at `0a45caa`, not inferred:

| Attempted operation | Result today |
|---|---|
| `DELETE FROM surfaces` (expecting cascade) | Aborted: `surface events are immutable`. SQLite fires `BEFORE DELETE` triggers for foreign-key cascade actions. |
| `DELETE FROM interactions` | Aborted: `interaction events are immutable`. |
| `DELETE FROM surface_open_requests` | Aborted twice: `ON DELETE RESTRICT` on `surfaces`, and its own immutable-delete trigger. |
| `UPDATE resolutions SET response_payload = …` | Aborted: `resolutions are immutable`. |
| `UPDATE draft_revisions SET payload = …` | Aborted: `draft revisions are immutable`. |
| `DELETE FROM rooms` (legacy) | **Succeeds**, cascading to `envelopes`. |

Nine `*_immutable_delete` triggers guard `definition_bindings`,
`delivery_attempts`, `delivery_events`, `draft_revisions`,
`interaction_events`, `resolutions`, `surface_events`,
`surface_open_requests`, and `terminal_outcome_retrievals`.

Weakening those triggers to permit deletion would remove the guarantee
everywhere, all the time, to enable an operation that runs rarely. Instead:

**All retention operations run through one narrowly scoped custody
maintenance path in `internal/db`, and nowhere else.** That path is accepted
as the single mechanism, with these obligations, each of which is a
requirement of the acceptance rather than a suggestion. It:

1. refuses to run while the HTTP server is accepting requests;
2. opens one transaction, drops only the specific triggers it must, performs
   the redaction or cascade delete, recreates the triggers, and commits — so
   the window in which the guard is absent is never observable to a serving
   connection;
3. writes an append-only `retention_operations` row recording the operation
   kind, the target IDs, the policy reference, the actor, the timestamp, the
   count and digests of the removed content, and the identifiers of any known
   backups that still contain it;
4. writes a `draft_revision_tombstones` row for each removed draft revision,
   finally giving that table its writer;
5. is the only code permitted to write a redaction tombstone into a payload
   column.

It is the only way to have both real immutability during normal operation and
a real deletion capability. It requires its own tests, because a bug there can
leave the database without its immutability guards.

Deletion granularity is **surface** and **interaction**. Per-caller-scope
deletion — "forget everything from application X" — is offered only where the
caller scope is a real boundary, and is **explicitly unavailable for the
`standalone-local` compatibility scope**, which all legacy callers share and
which therefore cannot separate them. This limitation is documented rather
than papered over. It remains a limitation after ADR 0004 makes
`standalone-local:<partition>` a host-assigned spelling: because any local
caller can assert any partition, a partition filter on a retention operation
is a convenience for the local user, never a guarantee that content from one
application has been isolated from another. Fixing it properly requires the
authenticated caller identity that ADR 0001 and ADR 0004 both defer.

`surface_open_requests` gets a schema change in the same migration: the
`ON DELETE RESTRICT` becomes `ON DELETE CASCADE`, in this work and not
deferred, because a request row that outlives its surface is a permanent
duplicate copy of the caller payload with no remaining referent. Until that
migration lands, no surface created through the async open path is deletable
at all, and its `request_snapshot` is a second copy no retention operation can
reach.

### 7. Backup semantics

`internal/db` opens SQLite with `journal_mode = WAL` and
`SetMaxOpenConns(1)`. Three consequences are contractual:

- **A file copy of `tangent.db` is not a backup.** Backups use `VACUUM INTO`
  or the online backup API against the live handle. Copying the file without
  its `-wal` and `-shm` companions produces a silently stale database.
- **A backup is a point-in-time copy that redaction cannot reach.** Removing
  content from the live database does not remove it from a backup taken
  earlier. The retention operation records which backups are known to be
  affected; it does not claim to have cleaned them.
- **Backups are opt-in.** A single-user local host does not make silent copies
  of the user's data to a path the user did not name. An explicit command
  using `VACUUM INTO` writes to a user-named path, and covers the real need.

The backup unit is the whole database. Per-custody-class backup exclusion is
not offered, because `ephemeral` content is never written and every other
class is retained while it is retained.

### 8. Telemetry and log redaction defaults

The current `loggingMiddleware` emits `method`, `path`, and `duration`. That
path includes `/r/{roomID}` and `/api/hitl/items/{itemID}`, and
`internal/mcp/session_tools.go` additionally logs the full room URL. That is
the entire current telemetry surface, and it already leaks identifiers into
stderr.

The default, which payload-safe telemetry work implements:

**Permitted:** Tangent IDs, lifecycle states, revisions, transitions, counts,
sizes, durations, definition kind and version, digests, typed error codes,
caller scope, authority, and assurance labels.

**Forbidden, in every log, metric, span, event name, and error string:**
request payloads, response payloads, draft payloads, phase output contents,
`terminal_reason`, `error_message`, `closed_reason`, notes and comment fields,
participant free text, capability material, and any URL carrying a capability
parameter or fragment.

Freeform adapter text is classified as caller payload precisely so that a
telemetry implementation reading this table does not mistake
`delivery_attempts.error_message` for safe operational metadata.

**Whole room and item URLs are not logged; the identifier alone is.** ADR 0004
§5 establishes that a room URL is a locator and not authority, so logging one
is not an authorization leak. This rule is narrower and stands on a different
footing: a whole URL is an unbounded string that will carry a query or
fragment as soon as scoped launch capabilities exist, and there is no reason
to pre-commit to that leak. Where the two ADRs meet — `logWorkflowRoomCreated`
(`internal/mcp/session_tools.go:459`) and `loggingMiddleware`
(`internal/server/server.go:305`) — **this rule governs the log format** and
ADR 0004 §6.2 governs what may never be added to it. Neither call site is
removed; `logWorkflowRoomCreated` logs the room id instead of the assembled
URL, and the log stays useful.

A definition manifest's `telemetry.redact_fields`
([ADR 0003 §2.8](0003-definition-and-package-ownership.md)) is an additional
publisher-authored tightening on top of this floor. It can never widen what is
permitted here.

### 9. External-reference mode

`external-reference` is not new; it is already implemented for HITL evidence
and is generalized here. `ArtifactRefEvidence` in `internal/hitl/evidence.go`
already carries exactly the right shape — `authority`, `artifact_id`,
`revision`, `digest`, `media_type`, `logical_kind`, `size_bytes`,
`sensitivity`, `retrieval_capability_id`, `expires_at`, `retention_policy`,
`safe_preview_artifact_id` — and `PreviewArtifactEvidence` resolves content
through a host-registered adapter and persists nothing.

The rules that make it a custody class rather than a convention:

1. **Refusal, not silent copying.** When the effective content mode is
   `external-reference` and a submission carries inline content in a
   position the definition marks as reference-only, the submission **fails**
   with a typed error. It is never accepted and stored anyway. This is the
   whole point of the class: an interaction declared as reference-only must
   not be able to become a copy by accident.
2. **Resolution is read-time and host-mediated.** Content is fetched only
   through a host-registered capability adapter, only for the exact
   `authority` plus `retrieval_capability_id` pair named in the immutable
   stored reference. As the existing code already states, an authority,
   artifact ID, path, URI, or action ID appearing in a payload is never
   permission to read or execute anything.
3. **Resolved content is never persisted and never cached.** Not to SQLite,
   not to browser storage, not to a backup. A preview is `ephemeral` by
   construction regardless of the interaction's retention class.
4. **The digest is the durable fact.** After the source is gone, Tangent can
   still say which artifact, at which revision, with which digest, was placed
   in front of the participant. It cannot reproduce the content, and it does
   not claim to.
5. **Expiry belongs to the reference.** `expires_at` on the reference is
   enforced at resolution time, as it already is. An expired reference yields
   a typed expired status, not a fallback to a stored copy.

### 10. Inventory: server-side persistence at `0a45caa`

Sixteen tables and one compatibility view. Thirty-four columns carry caller
payload, participant content, policy, identity references, or adapter-supplied
freeform text.

| Table | Column | Field class | Written by | Custody at rest today | Accepted default |
|---|---|---|---|---|---|
| `rooms` | `meta` | Caller payload | `room.Manager.Create` | Forever | `surface` |
| `rooms` | `phase_outputs` | Mixed: presentation, draft, caller payload, business data | 15 phase writers in `internal/room/` | Forever | `surface` |
| `rooms` | `closed_reason` | Adapter freeform | `session_close` | Forever | `surface` |
| `rooms` | `title` | Presentation | **Never written by any code path** | — | `surface` |
| `envelopes` | `request_payload` | Caller payload | `persistPendingEnvelope` | Forever | `surface` |
| `envelopes` | `response_payload` | Participant resolution | `persistEnvelopeFinalState` | Forever | `surface` |
| `envelopes` | `error_message` | Adapter freeform | `persistEnvelopeFinalState` | Forever | `surface` |
| `surfaces` | `metadata` | Presentation | `interaction.Store`, migration 0003 | Forever | `surface` |
| `surfaces` | `policy` | Policy | `interaction.Store` | Forever | `durable-record`, never redacted; holds the custody declaration |
| `surface_events` | `metadata` | Audit | `interaction.Store` (immutable) | Forever | `durable-record`, typed keys only |
| `interactions` | `request_snapshot` | Caller payload | `SubmitInteraction` (immutable) | Forever | **`interaction`** — redacted 30 days after terminal, per section 4 |
| `interactions` | `external_refs` | External reference | `SubmitInteraction` (immutable) | Forever | `durable-record` |
| `interactions` | `policy` | Policy | `SubmitInteraction` (immutable) | Forever | `durable-record`; holds the pinned custody |
| `interactions` | `terminal_reason` | Adapter freeform | Terminal transitions | Forever | Caller payload class |
| `interactions` | `caller_principal_ref` | Identity reference | `SubmitInteraction` | Forever | `durable-record` |
| `interactions` | `participant_ref` | Identity reference | Presentation, resolution | Forever | `durable-record` |
| `definition_bindings` | (all) | Identity and lifecycle | `SubmitInteraction` (immutable) | Forever | `durable-record`, never redacted |
| `interaction_events` | `metadata` | Audit | `interaction.Store` (immutable) | Forever | `durable-record`, typed keys only |
| `draft_revisions` | `payload` | Participant draft | `SaveDraftRevision` — **no transport caller exists** | Forever, if ever written | `interaction` |
| `draft_revisions` | `sensitivity` | Policy | `SaveDraftRevision` | Forever | `durable-record`; input to the browser rule |
| `draft_revision_tombstones` | (all) | Audit | **No writer exists** | — | `durable-record`; this ADR supplies the writer |
| `resolutions` | `response_payload` | Participant resolution | `ResolveInteraction` (immutable) | Forever | `durable-record`, erasable only by explicit user-initiated operation |
| `resolutions` | `integrity_digest` | Identity and lifecycle | `ResolveInteraction` | Forever | `durable-record`, never redacted; survives erasure |
| `resolutions` | `participant_ref` | Identity reference | `ResolveInteraction` | Forever | `durable-record`; survives erasure |
| `resolution_deliveries` | `destination_binding` | Delivery evidence | `ResolveInteraction` (identity-immutable) | Forever | `durable-record`; freeform contents treated as payload |
| `resolution_deliveries` | `policy` | Policy | `ResolveInteraction` | Forever | `durable-record` |
| `resolution_deliveries` | `receipt` | Delivery evidence | Delivery worker | Forever | `durable-record` |
| `resolution_deliveries` | `terminal_reason` | Adapter freeform | Delivery worker | Forever | Caller payload class |
| `terminal_notifications` | `destination_binding` | Delivery evidence | Terminal transitions | Forever | `durable-record` |
| `terminal_notifications` | `policy` | Policy | Terminal transitions | Forever | `durable-record` |
| `terminal_notifications` | `receipt` | Delivery evidence | Delivery worker | Forever | `durable-record` |
| `terminal_notifications` | `terminal_reason` | Adapter freeform | Delivery worker | Forever | Caller payload class |
| `terminal_outcome_retrievals` | `transport_correlation` | Delivery evidence | `AwaitResolution`/`Get` (immutable) | Forever | `durable-record` |
| `delivery_attempts` | `receipt` | Delivery evidence | Seal-once | Forever | `durable-record` |
| `delivery_attempts` | `error_message` | Adapter freeform | Seal-once | Forever | Caller payload class |
| `delivery_events` | `metadata` | Audit | `interaction.Store` (immutable) | Forever | `durable-record`, typed keys only |
| `surface_open_requests` | `request_snapshot` | Caller payload | `async_store` (immutable, undeletable) | Forever, unreachable by any deletion | Follows its interaction's custody, once the `ON DELETE CASCADE` fix in section 6 lands |
| `legacy_room_history_v12` | (view) | Projection over `rooms` + `envelopes` | Migration 0003 | Follows its base tables | Follows its base tables |

Phase IDs writing into `rooms.phase_outputs`: `approval-queue`, `dashboard`,
`diff-review`, `drafting`, `file_picker`, `form-collect`, `output`,
`progress_panel`, `revision`, `spreadsheet-review`, `synthesis`, `whiteboard`,
`wizard`. Two of them already store references rather than bytes —
`whiteboard` keeps `assets` and `export_refs`, `form-collect` keeps
`attachment_refs` — which is the `external-reference` pattern arrived at by
convention. Four store unbounded participant text under a `notes` key
(`approval-queue`, `form-collect`, `spreadsheet-review`, `whiteboard`), and
`whiteboard` stores a full `scene_snapshot`, plus a per-revision history of
snapshots.

### 11. Inventory: browser persistence at `0a45caa`

Nine `localStorage` key families and one `sessionStorage` key. No IndexedDB,
no cookies, no Cache API, no Zustand persist middleware. (ADR 0004 introduces
the first cookie: an HttpOnly participant-session cookie, which is capability
material, is never readable by page script, and is not a custody-bearing
draft store.)

| Key shape | Owning workflow | Contents | Cleared by | Accepted default |
|---|---|---|---|---|
| `tangent:wizard-draft:v1:<roomID>:<wizardID>` | Wizard | `currentStepID`, `progress[]`, `branchSelections[]`, `envelopeId`, `baseSeedKey` | Submit, seed mismatch | `interaction`, TTL |
| `tangent:whiteboard-draft:v1:<roomID>:<boardID>` | Whiteboard | `notes` free text, **full tldraw `scene` snapshot**, `baseRevisionId` | Submit, seed mismatch | `interaction`, TTL. Largest and most sensitive of the nine. |
| `tangent:form-collect-draft:v1:<roomID>:<formID>` | FormCollect | `answers{}` free-form, `notes`, `savedDrafts[]`, `templates[]`, `attachmentRefs[]` | Submit, seed mismatch | `interaction`, TTL |
| `tangent:approval-queue-draft:v1:<roomID>:<queueID>` | ApprovalQueue | `decisions[]` with per-item `comment` and `defer_reason`, `notes`, `exportRefs[]` | Submit, seed mismatch | `interaction`, TTL |
| `tangent:spreadsheet-review-draft:v1:<roomID>:<tableID>` | SpreadsheetReview | `queryState`, `selectedRowIDs[]`, `notes`, `savedViews[]`, `exportRefs[]` | Submit, seed mismatch | `interaction`, TTL |
| `tangent.dashboard.draft.v1:<roomID>:<dashboardID>` | Dashboard | `activeLayoutID`, `layout[]`, `savedLayouts[]` | Submit | `surface`, TTL. Presentation state, not participant content. |
| `tangent.diff-review.draft.v1:<roomID>:<reviewID>` | DiffReview | `decisions[]` with free-text `comment`, `comments{}`, `currentFile`, `filterState`, `exportRefs[]` | Submit, seed mismatch | `interaction`, TTL |
| `tangent.file-picker.draft.v1:<roomID>:<pickerID>` | FilePicker | `activeRootID`, `currentDir`, `search`, `selectedKeys[]`, `previewKey` — **filesystem paths** | Submit | `interaction`, TTL. Un-encoded key segments. |
| `tangent.progress-panel.view.v1:<roomID>:<panelID>` | ProgressPanel | `activeTab`, filters, `status`, `note`, `checkpointLabel` | Submit | `surface`, TTL |
| `tangent:hitl:connection-id` (`sessionStorage`) | HITL inbox | Diagnostic connection label `hitl-browser-<uuid>` | Tab close | Operational; no custody obligation. Already tolerates storage being disabled. Remains a diagnostic label under ADR 0004 §6.6. |

Every module already swallows storage failures, so `browser_persistence:
forbidden` degrades to in-memory behavior that the code paths already handle.

### 12. Migration and default behavior for existing rooms

**Existing legacy rows keep today's behavior.** Every existing `rooms` and
`envelopes` row, and every `legacy:*` interaction, resolution, and definition
binding minted by migration 0003, receives effective custody
`{retention: surface, content_mode: inline}` with
`custody_assurance: "legacy-unknown"`.

This is deliberate on three counts. It matches the behavior those rows were
actually created under. It does not reclassify them as `durable-record`, which
would over-claim a retention obligation the user never agreed to. And it does
not reclassify them as `interaction`, which would start silently deleting data
that exists today. ADR 0001 §11 already requires migration to preserve
uncertainty rather than invent clean history; the same rule governs custody.

**Nothing is retroactively redacted or deleted.** The migration writes policy,
not tombstones. Existing content is removed only when the user runs an
explicit retention operation.

**Existing surfaces inherit `surface`.** A surface created before the custody
field existed has no declaration; it takes `surface` retention and `inline`
content mode, which is its current behavior.

**The `/hitl` default surface keeps its contract.** `/hitl` items take
`durable-record` for identity, lifecycle, and resolution content, because the
inbox is an operator audit record and the durable `/hitl` contract is a
compatibility boundary. Evidence entries of type `artifact_ref` are forced to
`external-reference`, which they already are in practice. Evidence previews
are `ephemeral`, which they already are — `PreviewArtifactEvidence` persists
nothing.

**Existing browser keys are swept by the client, not the server.** The server
cannot reach them. The one-time migrate-then-delete sweep in section 5 runs on
first load.

**Existing backups are out of reach.** A backup taken before this ADR lands
contains everything, under the old behavior, and no retention operation
changes that. This is recorded, not solved.

## Consequences

### Positive

- A single-user local host can finally remove content it should not keep,
  through one audited path, without weakening the immutability guarantees ADR
  0001 established.
- Redaction preserves the audit chain: IDs, revisions, states, timestamps,
  participant bindings, and integrity digests survive, so "what happened"
  remains answerable after "what it said" is gone.
- Sensitive interactions get a real browser-persistence prohibition with three
  enforcement points, rather than nine independent storage modules each
  deciding for themselves.
- `external-reference` becomes a refusal rather than a convention, so a
  reference-only interaction cannot quietly become a copy.
- Downstream work gets concrete defaults: backup uses `VACUUM INTO` and is
  opt-in; deletion is surface- and interaction-scoped through a maintenance
  path; telemetry has an explicit forbidden list that correctly classifies
  freeform adapter text as payload.
- The `surface_open_requests` duplicate-copy defect and the two-shape
  `localStorage` key inconsistency are found and fixed rather than inherited.

### Negative and migration risks

- The maintenance path drops and recreates triggers inside a transaction. That
  is a genuinely dangerous operation and needs its own tests, a refusal to run
  while serving, and an audit row for every invocation. A bug there can leave
  the database without its immutability guards.
- Deletion does not reach backups, exported artifacts, delivered payloads
  already handed to a caller, or content the participant copied elsewhere.
  Tangent must state that plainly rather than imply a right to erasure it
  cannot deliver.
- Defaulting new interactions to `interaction` retention is a behavior change
  for new callers: a payload that would have survived indefinitely is redacted
  30 days after terminal unless the caller, definition, or host asks for more.
- Two custody vocabularies coexist during migration: legacy rows carrying
  `custody_assurance: "legacy-unknown"` and new interactions carrying a real
  declaration. Operators reading the store will see both.
- Replacing nine draft-storage modules with one gate touches nine shipped
  renderers and their tests. The workflow response shapes do not change, but
  the client draft-recovery behavior does.
- A `durable-record` floor on `/hitl` resolutions means a caller cannot
  request `ephemeral` handling of an approval decision. That is intentional,
  and it is a real constraint on callers who wanted one.
- `interactions.request_snapshot` is immutable by trigger, so even the
  accepted default of redacting caller payload after a window requires the
  maintenance path. Custody enforcement is never a plain `UPDATE`.
- Retention windows introduce a background timer that mutates the store. It
  must be idempotent, restart-safe, and must never race a resolution — an
  interaction that is non-terminal is never eligible.
- Per-caller-scope deletion is unavailable for `standalone-local` and stays
  unavailable after ADR 0004, because that ADR's partitions are advisory.

## Alternatives considered

### One flat five-value custody enum

Rejected. `external-reference` answers "what is stored", the other four answer
"for how long". Forced onto one scale, `external-reference` has no defensible
position: it is stricter than `durable-record` about content and weaker than
`ephemeral` about duration. The pair keeps the caller-facing token count at
one while making the lattice orderable.

### Relax the immutable-delete triggers to permit cascade deletes

Rejected. That removes the guarantee continuously to enable an operation that
runs rarely. The triggers are the thing that makes ADR 0001's immutability
claim true at the storage layer rather than by convention. A narrow, audited,
offline maintenance path is a smaller hole than a permanently weaker schema.
Rejecting the maintenance path outright was also considered and rejected: it
would make canonical records permanently undeletable and force resolution
payloads to be permanently unerasable.

### Delete records instead of redacting content

Rejected. Deleting an interaction destroys the idempotency key, so a caller
retrying an old request would create a duplicate rather than receive the
original. It also destroys the audit answer to "was this ever approved". The
skeleton is cheap and load-bearing; the payload is what is expensive and
sensitive.

### Let the caller declare custody freely, with no host floor

Rejected. A caller could then request `ephemeral` handling of an approval
decision and leave the operator with no record of what they approved. The host
is the party with the retention obligation and must be able to set a floor.

### Let the host silently re-evaluate custody when configuration changes

Rejected. An interaction's custody is pinned at submission for the same reason
its definition binding is: the participant and the caller both acted under a
policy, and changing it afterwards rewrites what they agreed to. A change
after the fact is an explicit, audited retention operation.

### Encrypt browser drafts instead of forbidding them

Rejected for this ADR. On a loopback single-user host the key would have to
live in the same browser as the ciphertext, so it adds ceremony without moving
the data-at-rest boundary. Forbidding persistence for sensitive interactions
is the honest control.

### Keep browser drafts as the only draft custody and never implement `SaveDraft`

Rejected. It is the current state, and it is why this ADR exists: the drafts
that matter most are exactly the ones that should not sit outside the server's
deletion and audit boundary. `SaveDraft`, its `sensitivity` column, and
`draft_revision_tombstones` already exist in the schema and need callers.

### Opt-out automatic backup

Rejected. A local single-user host should not write silent copies of the
user's data to a path the user did not name. The recovery need is met by an
explicit `VACUUM INTO` command.

### Defer the `surface_open_requests` cascade fix

Rejected. Until it lands, no surface created through the async open path can
be deleted at all, and its `request_snapshot` is a permanent second copy of
the caller payload that no retention operation can reach. It is a small
migration and it blocks the entire deletion story.

## Review questions

Chrispian accepted this ADR on 2026-09-04. Dependent tasks may treat the
following material choices as locked:

1. A new interaction that declares nothing takes `interaction` retention, with
   its caller payload redacted 30 days after the terminal transition. Existing
   rooms and legacy rows stay at `surface` and are not reclassified.
2. Resolution payloads are redactable, but only by an explicit user-initiated
   erasure, never on a timer, and always preserving `response_kind`,
   `integrity_digest`, the participant binding, and timestamps.
3. The scoped custody maintenance path in `internal/db` may drop and recreate
   the immutability triggers inside one transaction. It refuses to run while
   the server is serving and always writes a `retention_operations` audit row.
   It is the only code permitted to write a redaction tombstone.
4. Window defaults are 30 days `interaction`, 90 days `surface`, 14 days
   browser draft TTL, and zero for `ephemeral`; all are host-configurable.
5. Backup is opt-in through an explicit command using `VACUUM INTO` to a
   user-named path. Tangent makes no silent copies.
6. Custody is declared per interaction. A definition manifest may additionally
   mark individual request and response fields as reference-only, which is
   what the `external-reference` refusal checks against.
7. Per-caller-scope deletion is accepted as unavailable for `standalone-local`
   and is documented as such. After ADR 0004, a partition filter remains a
   convenience for the local user and never a guarantee of isolation.
8. The nine `localStorage` key families migrate to one v2 shape on first load
   after the upgrade, and the v1 keys are then deleted.
9. `surface_open_requests` moves from `ON DELETE RESTRICT` to
   `ON DELETE CASCADE` in this work, not later.

The review disposition was: accept this decision with the defaults above
folded in.

## References

- [`0001-lifecycle-boundaries.md`](0001-lifecycle-boundaries.md) — §3 pinned
  policy and definition binding, §5–§8 lifecycle authority, §9 drafts and
  downstream outcomes, §11 migration preserves uncertainty
- [`0003-definition-and-package-ownership.md`](0003-definition-and-package-ownership.md)
  — §2.6 `retention_class`, `draft_custody`, `client_persistence_prohibited`;
  §2.8 `telemetry.redact_fields`
- [`0004-caller-participant-and-room-access-authority.md`](0004-caller-participant-and-room-access-authority.md)
  — §3 `standalone-local` partitions are advisory, §5 room URLs are locators,
  §6 capability material never leaves the session store
- [`../interactive-collaboration-direction.md`](../interactive-collaboration-direction.md)
  — "Sensitivity and retention", "Browser local storage has implicit custody",
  "SQLite maintenance and backup are not product contracts", open questions 10
  and 16
- [`../contracts/hitl-inbox-v1.md`](../contracts/hitl-inbox-v1.md)
- [`../../internal/db/db.go`](../../internal/db/db.go) — WAL,
  `foreign_keys=ON`, `MaxOpenConns(1)`, `~/.tangent/tangent.db`
- `internal/db/migrations/0001_init.up.sql` …
  `0005_delivery_attempt_lifecycle.up.sql`
- [`../../internal/interaction/records.go`](../../internal/interaction/records.go)
  — `DraftRevision.Sensitivity`, `DraftRevision.ExpiresAt`
- [`../../internal/interaction/service.go`](../../internal/interaction/service.go)
  — `SaveDraftInput` (no transport caller)
- [`../../internal/interaction/reference.go`](../../internal/interaction/reference.go)
  — `SurfaceReferenceSnapshot`, which already omits drafts, deliveries, and
  audit journals
- [`../../internal/hitl/evidence.go`](../../internal/hitl/evidence.go) —
  `ArtifactRefEvidence`, `ArtifactPreviewAdapter`, non-persisting previews
- [`../../internal/room/phase_state.go`](../../internal/room/phase_state.go)
  and the fifteen phase writers in `internal/room/`
- [`../../internal/server/server.go`](../../internal/server/server.go) —
  `loggingMiddleware`
- [`../../internal/mcp/session_tools.go`](../../internal/mcp/session_tools.go)
  — `logWorkflowRoomCreated`
- `ui/src/lib/*-draft-storage.ts`,
  [`../../ui/src/lib/progress-panel-storage.ts`](../../ui/src/lib/progress-panel-storage.ts),
  [`../../ui/src/lib/hitl-api.ts`](../../ui/src/lib/hitl-api.ts)
- Torque tasks `CW-20260825-0059`, `CW-20260825-0072`, `CW-20260825-0075`,
  `CW-20260825-0077`, `CW-20260825-0078`
