# ADR 0004: Caller, Participant, and Room-Access Authority

**Status:** Accepted

**Date:** 2026-09-04

**Approved:** 2026-09-04 by Chrispian, during the Tangent foundation orchestration session

**Task:** `CW-20260825-0061`

**Baseline reviewed:** `0a45caa`

**Source:** `docs/interactive-collaboration-direction.md`, retired 2026-09-04
under `CW-20260825-0067`. Its durable content is
[`0005-product-boundary-and-portfolio-composition.md`](0005-product-boundary-and-portfolio-composition.md); the rest was
superseded by this ADR and its siblings, converted to Torque tasks, or
captured to Tesseract knowledge. The file remains readable in git history
between `f457ad1` and `a332e76`.
— "Identity and authorization model" and "Room invitations and browser sessions"

**Builds on:** [`0001-lifecycle-boundaries.md`](0001-lifecycle-boundaries.md)

**Composes with:** [`0002-retention-and-draft-custody.md`](0002-retention-and-draft-custody.md)
(telemetry floor, session-table retention),
[`0003-definition-and-package-ownership.md`](0003-definition-and-package-ownership.md)
(the separate host-mediated capability namespace)

**Implemented by:** `CW-20260825-0075`. Composes with `CW-20260825-0077`
(host-mediated capabilities). Constrains `CW-20260825-0078` (payload-safe
telemetry).

## Context

ADR 0001 separated Surface, Interaction, Connection, and Delivery lifecycles and
named the identity fields each record carries. It deliberately did not define
who may perform an operation, and it recorded the gap explicitly: "the
loopback-only first slice still lacks authenticated caller and participant
identity". That gap is now the load-bearing one. The durable records have
`caller_scope`, `owner_scope`, `participant_scope`, and a `Capability`
argument, but nothing in the shipped process establishes any of them from
evidence.

At `0a45caa` the concrete situation is:

**Room identity is the only room authority.** `room.Manager.Create` issues a
`uuid.NewString()` (`internal/room/room.go:643`) and the `Room` struct carries
no owner, caller, or participant field at all. `/r/{roomID}` is pre-validated
against the live manager and otherwise falls through to the SPA
(`internal/server/server.go:289`). `/ws` accepts an upgrade for any existing
`roomID` query parameter with an origin allowlist and nothing else
(`internal/ws/handler.go:88`, `:101`). `Room.HandleResponseFrom` accepts a
terminal response from whatever socket is currently attached, guarded only by a
connection generation and a presentation revision
(`internal/room/room.go:388`, `:491`). Knowing a room UUID is therefore
sufficient to view the interaction, answer it as the participant, and cancel it.
The UUID is a locator that behaves as a bearer credential.

**Caller scope exists but is self-asserted.** The generic tools accept
`caller.scope` and `requester_scope` verbatim from the wire and the adapter pins
only the authority/assurance labels: `directMCPActor` returns
`{Scope: <wire>, Authority: "direct-mcp", Assurance: "asserted"}`
(`internal/mcp/interaction_tools.go`), and `directHITLActor` composes
`"direct-loopback:" + application_id` (`internal/mcp/hitl_tools.go`). Every
authorization check in `internal/interaction/service.go` — `authorizeInteraction`
(`:786`), `CancelInteraction` (`:581`), `SupersedeInteraction` (`:632`),
`CloseSurface` (`:705`) — is a string comparison against that self-asserted
value. `OpenSurface` accepts a wire-supplied `owner_scope` that is never checked
against the caller (`:173`–`:189`), so a caller may open a surface owned by any
scope it can spell. `SubmitInteraction` checks only `SurfaceAccessPolicy`
(`:220`), so a caller may submit onto another caller's surface. The isolation is
real against accident and empty against intent.

**The seventeen workflow tools and seven `session_*` tools have no scoping at
all.** `handleSessionList` returns every room (`internal/mcp/session_tools.go:244`),
`handleSessionClose` closes any room by id (`:221`), `advanceRoomEnvelope`
pushes an envelope into any room by id (`:262`), and the workflow tools reuse an
arbitrary caller-supplied room through `env.Meta["roomID"]`
(`internal/mcp/triage_handler.go:131`). There is no caller field on the code
path at all.

**The one real enforcement is `SurfaceAccessPolicy`.**
`internal/hitl/surface_policy.go` reserves `surface_hitl_default` from the
generic tools using two process-internal capability constants that are never
accepted from the wire. `PrivilegedActorPolicy` and `DeliveryWorkerPolicy` exist
as deny-by-default interfaces (`internal/interaction/service.go:33`–`:53`) and are
not wired in `cmd/tangent/main.go`, so no administrator or delivery-worker
authority exists in the shipped process.

**The SPA is itself an unauthenticated MCP caller.** `ui/src/routes/Room.tsx:233`
POSTs `tangent.session_get` to `/mcp`, and `ui/src/components/TabStrip.tsx:58`,
`:182` POST `tangent.session_close` and `tangent.session_list`. `/mcp` and `/sse`
carry no origin, same-site, or CSRF middleware — only `/api/hitl/*` does, via
`hitlSameOrigin` (`internal/server/hitl.go:208`). The browser is therefore both
participant and caller today, through a route that any origin can reach.

**There is no security header anywhere.** No `Referrer-Policy`, no CSP header,
no `X-Content-Type-Options`. The only CSP is inside the design-iteration iframe
`srcdoc`. `loggingMiddleware` logs `r.URL.Path` for every request
(`internal/server/server.go:305`) and `logWorkflowRoomCreated` logs the full room
URL (`internal/mcp/session_tools.go:459`). Both are safe only if the URL is not
authority — which is exactly what this ADR must guarantee rather than assume.

**Already decided upstream.** `CW-20260904-0068` establishes that direct
loopback calls without an authenticated adapter binding persist an explicit
`standalone-local` / unverified caller scope until this decision's
implementation supplies scoped authorization. Migration `0003` already backfills
every legacy room and envelope at `standalone-local`
(`internal/db/migrations/0003_durable_interactions.up.sql:485`, `:521`). This ADR
defines what that scope becomes.

This ADR defines principals, capabilities, the access mechanism, and the
cross-caller rules. It does not define an authentication protocol, a credential
store, a user directory, remote access, or multi-tenancy. Tangent remains a
loopback, single-user interaction host.

## Decision

### 1. Principals

Six principals are canonical. Each has one identity source and one thing it may
never be inferred from. Identifiers are opaque; a principal is established by
the host, never by a tool argument.

| Principal | Definition | Identity and where it is established | Never inferred from |
|---|---|---|---|
| **Caller application** | The software product or adapter that submits work: an MCP client, a gateway adapter, a script, the Tangent SPA's own browser API. It owns the purpose of an interaction and the downstream consequence. | `caller_scope`, assigned by the receiving adapter from the connection's admission facts plus the caller's declared application id. Persisted on `InteractionRecord.CallerScope` and `SurfaceRecord.OwnerScope`. | An MCP session id, a transport connection, a process id, a tool name, or a display name. |
| **Caller agent or user** | The agent turn or human on whose behalf the caller application acts. Attribution only in v0.x. | `caller_principal_ref` plus `caller_authority` / `caller_assurance`. Supplied by the caller as an assertion on the loopback path; supplied as a verified binding by a trusted adapter when one is composed. | A `go-envelopes` `Trace.agentId`, a chat handle, or any value that has not been separately verified. |
| **Participant human** | The person whose input Tangent presents and captures. The only principal that may resolve an interaction. | A **participant session** (§4) held server-side and named by an HttpOnly cookie. Projected into records as `participant_scope` / `participant_ref` / `participant_authority` / `participant_assurance`. | Possession of a room URL, an item URL, a WebSocket, a browser tab, or a `connection_id` argument. |
| **Connection** | One browser or native client attachment to a surface projection. An audit and routing fact. | Server-assigned `connection_id`, derived from the participant session that opened it. | Anything. A connection holds no capability and grants none. A client-supplied connection id is a diagnostic label only. |
| **Plugin publisher** | The owner of an interaction definition, its schema, its renderer, and the interpretation of its responses. | `DefinitionBinding.Publisher`, resolved at submit time from `spec.PluginID`, defaulting to `hollis-labs/go-envelopes` (`internal/interaction/catalog.go:215`). Pinned immutably per interaction. | A request argument. Publisher identity is definition provenance, not request authority. |
| **Host administrator** | The person or process operating this Tangent instance. Holds `administer`. | `PrivilegedActorPolicy`, established in-process at construction. Deny-by-default in the shipped binary and unwired today. | A caller scope string, a tool argument, or a participant session. |

Two of these — connection and plugin publisher — are explicitly **not**
capability holders. They are named here so `CW-20260825-0075` does not discover
them later and grant them something. ADR 0003 §5 states the publisher side of
that boundary from the definition-ownership direction: shipping a package never
confers host authority.

### 2. Capabilities

Seven capabilities, and `submit` is accepted as the seventh. The source
document names five (view, draft, resolve, cancel, administer); the task record
adds `close`; this ADR adds `submit`, because creating work on an existing
surface is a distinct power from reading it and today it is the unguarded one.
Capabilities are always evaluated against a named object — a surface, or an
interaction on a surface. There is no global grant except `administer`.

These are **object-access capabilities**: who may perform an operation on a
surface or an interaction. They are a different namespace from the
**host-mediated effect capabilities** a renderer declares in
[ADR 0003 §2.5](0003-definition-and-package-ownership.md) (`clipboard.write`,
`export.download`, `file.read_scoped`, …). The two never substitute for one
another, and `CW-20260825-0077` consumes both without merging them.

*(Amended 2026-09-04, C1.)* Non-substitution alone is necessary and not
sufficient: it is satisfied by two systems that ignore each other, which is the
failure this rule was written to prevent, approached from the other side. The
positive rule is that **an effect is admitted only when its object-access
precondition is also satisfied**, evaluated through `authz.Authorize` like every
other refusal in the process. A renderer that may not `view` an interaction may
not read a file on its behalf, however complete its effect grant.
`effect.ObjectPrecondition` is that mapping — it names which question must be
answered first, and it is a mapping and never an equivalence between the two
powers.

| Capability | Grants | Shipped operations it gates |
|---|---|---|
| `view` | Read a surface or interaction projection, its definition binding, its state, and its terminal outcome. Reading a terminal outcome writes a `TerminalOutcomeRetrievalRecord`. | `tangent.inbox_list`, `tangent.inbox_search`, `tangent.inbox_get` (pure caller/surface-owner reads; no retrieval write), `surface_get`, `interaction_get`, `interaction_await`, `session_get`, `session_list`, `hitl_get`, `hitl_await`, `GET /api/hitl`, `GET /api/hitl/items/{id}`, `GET /api/hitl/events`, evidence reference and preview reads, the WebSocket projection stream. |
| `submit` | Create an interaction on a surface, mutate caller-owned surface workflow state, and open a surface under a given owner scope. | `interaction_submit`, `session_advance`, `session_advance_phase`, `session_set_phase_output`, `surface_open`, `hitl_enqueue`, the 17 workflow tools. |
| `draft` | Write an immutable `DraftRevision` and acknowledge a presented projection revision. Never terminal. | `SaveDraft`, `POST /api/hitl/items/{id}/present`, the SPA's per-workflow draft persistence when it becomes server-custodied. |
| `resolve` | Submit a terminal response to a presented interaction at its exact pinned revisions. Sealed as an immutable `ResolutionRecord`. | `POST /api/hitl/items/{id}/resolve`, the WebSocket `response` frame. |
| `cancel` | Terminate a nonterminal interaction with an authorized typed cause, or supersede one with a replacement. The cause determines which principal may exercise it (§7). | `interaction_cancel`, `interaction_supersede`, `hitl_withdraw`, the WebSocket `cancel` frame. |
| `close` | Terminate a surface and atomically disposition every outstanding interaction on it under the named surface policy. | `surface_close`, `session_close`. |
| `administer` | Every capability above on every object in the process, plus `ExpireInteraction`, plus participant-session revocation, plus unfiltered listing. | `ExpireInteraction` (already host-policy-only), and any future operator tooling. Not exposed on the MCP surface. |

`administer` is not implied by owning an object. Owning a surface implies
`view`, `submit`, and `close` on it; it does not imply `resolve`. A caller can
never resolve its own interaction, because resolution is the participant's
authority and its immutability is the product's whole value.

### 3. Caller scope grammar, and what `standalone-local` becomes

A caller scope is `<authority>:<partition>`.

- The **authority** is assigned by the receiving adapter from admission facts
  and comes from a closed set. It is never caller-supplied.
- The **partition** is the caller's declared application id, normalized. It is
  caller-supplied.

Two authorities exist in v0.x:

| Authority | Assigned when | Assurance | Isolation strength |
|---|---|---|---|
| `standalone-local` | A direct loopback MCP, SSE, or same-origin browser-API call with no verified adapter binding. | `loopback-unverified` | Partitions are **advisory**. Any local caller can claim any partition, so a partition prevents accident, not intent. |
| `gateway:<binding_id>` | A trusted in-process adapter established a verified caller binding (Tether, Nanite, or a Cerberus-mediated host). | Whatever the adapter recorded, at minimum `adapter-verified` | Enforced. |

> **`standalone-local` partitions are advisory, not a security boundary.**
> Any local caller can assert any partition, because the partition is the
> caller's own declared application id and nothing verifies it. Partitions are
> enforced **only across authorities**, where the authority prefix is
> host-assigned. A `standalone-local:a` caller is prevented from colliding with
> `standalone-local:b` by accident; it is not prevented from claiming to be
> `standalone-local:b`. No downstream product, adapter, docstring, or tool
> description may present a `standalone-local` partition as isolation.

Rules `CW-20260825-0075` implements:

1. `standalone-local` stops being a legacy fallback string and becomes the real,
   host-assigned authority for every unauthenticated loopback caller. A caller
   that supplies no application id is `standalone-local:anonymous`.
2. A wire-supplied `caller.scope`, `requester_scope`, or `owner_scope` is no
   longer the authorization value. The adapter derives the scope and, where the
   caller supplied a different string, stores it as an attribution label in
   `external_refs` and ignores it for authorization. The tool arguments remain
   accepted so the shipped schemas do not break.
3. **Cross-authority isolation is always enforced.** A `standalone-local:*`
   caller can never read, submit onto, cancel, or close a `gateway:*`-owned
   object, and the reverse. Because the authority prefix is host-assigned, this
   boundary is real.
4. **Within `standalone-local`, partition isolation is advisory and must be
   documented as such** — in `docs/architecture.md`, in `docs/mcp-integration.md`,
   and in the tool descriptions. This documentation obligation is part of the
   decision, not an implementation nicety. Nothing downstream may build a trust
   assumption on a `standalone-local` partition. It exists so a second agent's
   retry does not cancel the first agent's item, not so two agents can keep
   secrets from each other on the same machine. ADR 0002 §6 carries the same
   limitation into retention: a per-partition deletion filter is a convenience
   for the local user, never a guarantee that one application's content has
   been isolated from another's.
5. Existing persisted spellings are read through a fixed alias, with no data
   rewrite: `direct-loopback:<app>` reads as `standalone-local:<app>`; bare
   `standalone-local` reads as `standalone-local:anonymous`. New writes use the
   canonical form. The `hitl_*` tools keep accepting `source.application_id` and
   `caller.application_id` unchanged; only the scope the host derives from them
   changes spelling.

`standalone-local` becomes, in one sentence: **a genuine, host-assigned,
single-authority caller scope whose internal partitions are attribution and
accident-avoidance, not a security boundary.**

### 4. Access mechanism: an authenticated browser participant session

**Selected: a server-held participant session, minted on loopback admission and
carried in an HttpOnly cookie. Rejected: a scoped expiring invitation carried in
the room URL.**

The mechanism:

1. Any same-origin GET of an HTML document from a loopback `Host` with no valid
   participant session cookie mints one. The server inserts a durable
   participant-session row and sets a cookie naming it. The row holds a
   SHA-256 of the cookie value, creation and last-seen timestamps, an absolute
   expiry, the bound participant principal ref, its assurance, and the granted
   capability set. It holds no headers, no payloads, and not the cookie value
   itself.
2. The cookie is `HttpOnly`, `SameSite=Lax`, `Path=/`, with no `Domain`
   attribute and no `Secure` flag (loopback HTTP cannot use `Secure`, which also
   forecloses the `__Host-` prefix — an accepted limitation of a loopback tool).
   `SameSite=Lax` rather than `Strict` so a link opened from a terminal or
   another application still carries the session on top-level navigation.
3. The minted session is the `local-operator` participant that
   `internal/hitl/service.go:40` hardcodes today, with authority
   `tangent-loopback` and assurance `loopback-unverified`. The package-level
   `OperatorParticipant` var becomes the *default template* the session store
   instantiates, not the identity itself.
4. Every state-changing browser request — the `/ws` upgrade, `/api/hitl/*`
   writes, and the browser room API that replaces the SPA's direct `/mcp` use —
   requires a valid session with the relevant capability on the named object.
   The static SPA bundle does not.
5. Cross-site protection stays where it already works: the existing
   `hitlSameOrigin` middleware (loopback `Host` + `Sec-Fetch-Site` +
   `Origin` match) is extended to cover `/mcp`, `/sse`, `/ws`, and the new
   browser room API. It already permits header-less non-browser clients, so MCP
   clients are unaffected and drive-by browser POSTs are not.
6. The session id rotates whenever assurance changes — when a composed identity
   authority binds a verified principal to the session, the old id is revoked
   and a new one issued in the same transaction.

**Session lifetime and storage.** Sessions live in SQLite and survive a
`tangent` restart, storing only the SHA-256 of the cookie value. There is **no
idle expiry** — a local single-user tool must not log its user out mid-decision
— with an absolute maximum lifetime of **30 days**, rotation on assurance
change, and revocation through an explicit CLI flag rather than a UI control.
The session table is subject to ADR 0002's retention obligations like any other
durable record; its hashed cookie value is capability material and is governed
by §6.

**Why this and not an invitation.** The invitation form fails three of this
decision's own constraints at once. It puts capability material in a URL, which
is the one place §6 forbids it — and Tangent already logs room URLs
(`internal/mcp/session_tools.go:459`), prints them into agent transcripts as tool
output, and serves pages with no `Referrer-Policy`. It is anti-ergonomic for the
standalone case: a single local user would have to obtain a fresh token per
room, and would find a stale link dead every time they returned to their desk
after the TTL, which is precisely when a human-in-the-loop tool is being used.
And it does not actually raise assurance on loopback — an invitation minted by
an unauthenticated local process and consumed by an unauthenticated local
browser proves nothing that loopback admission did not already prove; it only
adds a secret to leak. A URL fragment exchanged once for a session, as the
source document suggests, is a strictly weaker version of what this section
already does directly, because loopback admission is the same evidence without
the intermediate secret.

The session mechanism gives the property that matters: **sharing, logging, or
pasting a room URL no longer shares access.** Authority moves from a string that
travels to a cookie that does not.

### 5. Room URLs are locators

`/r/{roomID}`, the `/hitl` inbox URL, and `/hitl/items/{itemID}` deep links name
an object. They are not authorization evidence and are not secrets. Concretely:

- They may appear in tool responses, agent transcripts, the SPA's address bar,
  and browser history without transferring any authority.
- Opening one without a session mints a session and grants that session the
  default participant capability set — which is what a single-user local tool
  should do — but the *session*, not the URL, is thereafter the authority. A
  second browser on the same machine gets its own session and its own audit
  trail rather than inheriting the first one's.
- `/ws?roomID=` stops being sufficient to attach. **The upgrade requires a valid
  participant session immediately, with no grace period**: `view` on that
  surface to attach, and `resolve` before a `response` frame is accepted. This
  is the single most important behavior change in this ADR — it is what stops a
  room UUID from being an answer credential — and a grace period would be that
  same bug with a deadline.
- Unknown ids keep returning 404 (`roomFallbackHandler`,
  `internal/server/server.go:289`). Known-but-unauthorized returns **403 within
  the same authority and 404 across authorities**, so a foreign authority cannot
  probe for existence. Existence leakage to the local user is not a threat this
  product defends against, and a 403 is far easier to debug.

Because the URL is not authority, `logWorkflowRoomCreated` and
`loggingMiddleware` are not authorization leaks and neither call site is
removed. **Their log format is nonetheless governed by
[ADR 0002 §8](0002-retention-and-draft-custody.md)**, which is a narrower rule
resting on different reasoning: a whole URL is an unbounded string that will
carry a query or fragment as soon as scoped launch capabilities exist, so the
identifier is logged and the assembled URL is not. This ADR's §6.2 says what
may never be added to those log lines; ADR 0002 §8 says what shape the existing
line takes. Neither weakens the other.

### 6. Capability material never leaves the session store

*(Amended 2026-09-04, C3 — the original text of this paragraph read "The only
capability material in the system is the participant session id" and enumerated
capability material as a closed set. That was true at `0a45caa` and stopped
being true the moment scoped grants existed. It is corrected here rather than
footnoted, because the uncorrected sentence makes the model unimplementable.)*

This section governs **two** classes of material, and the distinction between
them is load-bearing in both directions.

**Capability material — never leaves the session store.** The participant
session id is the only member. Possessing it *is* authority: it identifies the
session whose capability set `authz.Authorize` reads. Rules 1 through 7 below
are about this class and are hard constraints, inherited by
`CW-20260825-0078`.

**Scoped locators — may travel, because they grant nothing alone.** A room URL
(§5) and an `effect.Handle` id (ADR 0003 §2.5, B3) are both in this class. A
handle is short-lived grant *material* in the sense
[ADR 0002 §5](0002-retention-and-draft-custody.md) cares about — it is scoped,
expiring, and use-counted — and it is **not a credential**: it is re-authorized
on every use against the session and the pinned binding, and possessing one
without them grants nothing. That is precisely why a handle id may appear in a
renderer payload where a session id may not. Without this distinction the honest
reading of §6 forbids a handle id in a renderer payload, which makes the whole
host-mediated capability model unimplementable; and the dishonest reading treats
the handle as a bearer token, which is worse. A scoped locator is therefore
exempt from rule 1's placement list and subject to its own scoping rules, not to
this section's secrecy rules.

`CW-20260825-0078` inherits the following as hard constraints on **capability
material**.

1. The session id appears in exactly two places: the `Set-Cookie` / `Cookie`
   header, and a hash column in the session table. It must never appear in a
   URL path, query string, or fragment; a WebSocket query parameter; a tool
   argument or tool response; an envelope; `room.meta`; `SurfaceRecord.Metadata`;
   `InteractionRecord.ExternalRefs` or `Policy`; a `ConnectionRecord`; a
   `DeliveryAttempt` receipt; or an slog attribute.
2. `loggingMiddleware` logs method, path, and duration. It must never be
   extended to log `r.URL.RawQuery`, `Cookie`, `Authorization`, or request
   bodies. This is a maintenance rule, not an observation.
3. Every HTML response and every JSON API response sets
   `Referrer-Policy: no-referrer`, applied **process-wide** alongside
   `X-Content-Type-Options: nosniff` in the same middleware that already wraps
   the mux. Locators are not authority, but `design-iteration` renders untrusted
   agent-authored HTML and evidence payloads may carry links; leaking a locator
   to a third party is needless. Authenticated JSON also sets
   `Cache-Control: no-store`, which `writeHITLJSON` already does
   (`internal/server/hitl.go:269`).
4. `document.title` stays the static `Tangent`. It must not be set from a room
   id, an item id, or any session value. (The SPA sets no title today, which is
   already correct; `TabStrip.shortRoomID` truncating a locator for display is
   fine and stays.)
5. Unauthorized errors carry a fixed message. `interactionError` and `hitlError`
   currently surface `err.Error()`, and `ErrUnauthorized`'s text is a constant —
   that must stay true. An authorization failure must never name the required
   capability, the owning scope, the session, or the participant.
6. The client-supplied `connection_id` on `POST /api/hitl/items/{id}/present`
   and the `sessionStorage` value from `getHITLConnectionID()`
   (`ui/src/lib/hitl-api.ts:226`) remain diagnostic labels, as their own comments
   already state. The canonical `connection_id` becomes server-assigned from the
   session; the client value is recorded beside it as a label or dropped.
7. Browser-local draft storage (`ui/src/lib/*-draft-storage.ts`) holds
   participant work, not capability material, and is unaffected **by this ADR**.
   It stays non-authoritative per ADR 0001 §9, and its custody, key shape, and
   TTL are governed by [ADR 0002 §5](0002-retention-and-draft-custody.md),
   which replaces the nine modules with one custody-aware gate.

### 7. Principal × capability matrix

Default grants in the shipped `standalone-local` configuration. "Own" means an
object whose `owner_scope` or `caller_scope` matches the principal's resolved
scope. Composition (§10) can raise assurance; it does not change this shape.

| Principal | `view` | `submit` | `draft` | `resolve` | `cancel` | `close` | `administer` |
|---|---|---|---|---|---|---|---|
| **Caller application** | Own surfaces and interactions; operator-scoped surfaces it enqueued onto | Own surfaces; the operator inbox surface | — | — | Own interactions, causes `caller_withdrawn` and `caller_canceled` only | Own surfaces | — |
| **Caller agent or user** | — (attribution only; inherits nothing of its own) | — | — | — | — | — | — |
| **Participant human** | Any surface its session is bound to | — | Interactions presented to it | Interactions presented to it, at pinned revisions | Presented interactions, cause `participant_canceled` only | — | — |
| **Connection** | — | — | — | — | — | — | — |
| **Plugin publisher** | — | — | — | — | — | — | — |
| **Host administrator** | All | All | All | — | All causes incl. `administrator_canceled`, plus `ExpireInteraction` | All | Yes |

A freshly minted loopback participant session receives exactly the
**Participant human** row: `view`, `draft`, `resolve`, and participant-cause
`cancel`. It does not receive `close`, which stays a caller and administrator
power.

Three cells are deliberately empty and must stay empty:

- A caller can never `resolve`. Resolution is the participant's immutable act.
- A participant can never `close` a surface by default. Closing dispositions
  other callers' outstanding work; the shipped `/hitl` UI has no close control
  and should not grow one without a host grant.
- Nobody `administer`s in the shipped binary. `PrivilegedActorPolicy` stays
  deny-by-default until `CW-20260825-0077` supplies a real one.
  `CW-20260825-0075` must not invent an administrator credential.

`TerminalCauseSurfacePolicy` remains unreachable from any single-interaction
operation, as it is today (`internal/interaction/service.go:585`); it belongs
only to `CloseSurface`'s aggregate transaction.

### 8. Per-tool caller scoping for the shipped MCP surface

Thirty-nine tools ship at `0a45caa`: 25 registered in `internal/mcp/server.go`
(one discovery tool, seventeen workflow tools, seven `session_*`), ten
`surface_*` / `interaction_*`, and four `hitl_*`. "Scoped today" answers whether
the operation is bounded by any caller identity at all in the shipped process.

| Tool | Scoped today | Target capability | Target object |
|---|---|---|---|
| `tangent.list_workflows` | N/A — no object | none | Definition catalog; publisher attribution only |
| The 17 workflow tools: `triage`, `feedback`, `form-collect`, `design-iteration`, `interview_question`, `block_draft`, `prose_revision`, `output_render`, `whiteboard`, `dashboard`, `file-picker`, `progress-panel`, `wizard`, `diff-review`, `spreadsheet-review`, `approval-queue`, `synthesis_notes` | **No.** Any caller may reuse any room via `env.Meta["roomID"]` and receive that room's human response | `submit` | Named surface when reused; a new caller-owned surface otherwise |
| `tangent.session_create` | **No** | `submit` | New surface, `owner_scope` = resolved caller scope |
| `tangent.session_advance` | **No.** Any caller may push into any room | `submit` | Named surface |
| `tangent.session_get` | **No.** Returns full envelope history and phase outputs for any room | `view` | Named surface |
| `tangent.session_advance_phase` | **No** | `submit` | Named surface |
| `tangent.session_set_phase_output` | **No** | `submit` | Named surface |
| `tangent.session_close` | **No.** Any caller may close any room | `close` | Named surface |
| `tangent.session_list` | **No.** Returns every room in the process | `view`, filtered (§9) | Surface set |
| `tangent.surface_open` | Partially — records `caller.scope`, but accepts an unchecked wire `owner_scope` | `submit`; `owner_scope` host-derived | New or named surface |
| `tangent.surface_get` | Asserted string only | `view` | Named surface |
| `tangent.surface_close` | Asserted string only (`Requester.Scope == OwnerScope`) | `close` | Named surface |
| `tangent.interaction_submit` | **No cross-surface check.** Only `SurfaceAccessPolicy` gates the target surface | `submit` | Named surface |
| `tangent.interaction_get` | Asserted `requester_scope` | `view` | Named interaction |
| `tangent.interaction_await` | Asserted `requester_scope` | `view` | Named interaction |
| `tangent.interaction_cancel` | Asserted string; adapter already restricts causes to the two caller causes | `cancel` | Named interaction |
| `tangent.interaction_supersede` | Asserted string; already requires same caller scope and same surface for the replacement | `cancel` + `submit` | Both interactions |
| `tangent.interaction_list_kinds` | N/A — no object | none | Definition catalog |
| `tangent.interaction_resolve_definition` | N/A — no object | none | Definition catalog |
| `tangent.hitl_enqueue` | Records `direct-loopback:<application_id>`; enqueue itself is open by design | `submit` on the operator surface, granted to every admitted caller | `surface_hitl_default` |
| `tangent.hitl_get` | Asserted `caller.application_id` | `view`, own items only | Named item |
| `tangent.hitl_await` | Asserted `caller.application_id` | `view`, own items only | Named item |
| `tangent.hitl_withdraw` | Asserted `caller.application_id` | `cancel`, own items only, cause `caller_withdrawn` | Named item |

Non-MCP surfaces, for completeness: `/healthz` stays open, as do the readiness
and capability probes added beside it (`/readyz`, `/healthz/capability`,
`/healthz/capability/{kind}`) — they are operator probes that must be reachable
by `curl` with no session, and they carry no payload, participant text, path,
or session material to protect; `/mcp`, `/sse`, and
`/ws` gain the same-origin guard; the five `/api/hitl/*` routes gain the
participant-session requirement on top of the origin guard they already have;
`/r/{roomID}` and `/` keep serving the SPA bundle without a capability check.

### 9. Cross-caller list, get, and close

| Operation | Same partition | Cross-partition, same `standalone-local` authority | Cross-authority |
|---|---|---|---|
| **List** — `session_list` | Allowed | **Allowed, unchanged.** Legacy `session_*` tools resolve to the whole `standalone-local` authority so today's "show me all my rooms" behavior is preserved. `active_only` keeps filtering surface lifecycle only. | Denied; foreign-authority surfaces are omitted, never counted, never errored on |
| **List** — canonical `surface_*` | Allowed | Denied by default. Opt in per call with an explicit `include_authority_scope` argument, which is honored only within `standalone-local` and recorded on the retrieval audit | Denied |
| **Get** — `session_get` | Allowed | **Allowed, unchanged** (compatibility boundary) | Denied, as `not_found` |
| **Get** — `surface_get`, `interaction_get`, `interaction_await` | Allowed | Allowed only when the requester's scope matches `owner_scope` or the requester opened the surface — the existing `SurfaceWasOpenedByScope` rule (`internal/interaction/async_store.go:145`), retained | Denied, as `not_found` |
| **Get** — `hitl_get`, `hitl_await` | Allowed | Denied. The inbox is shared; the items are not. Unchanged from today | Denied, as `not_found` |
| **Close** — `session_close`, `surface_close` | Allowed | **Denied**, including for legacy `session_close`. Closing dispositions other partitions' pending interactions, so advisory isolation is enforced here even though it is advisory for reads | Denied, as `not_found` |

Closing a surface that carries interactions owned by other partitions
dispositions them with cause `surface_policy` and queues each a
`TerminalNotificationRecord`, per ADR 0001 §5. The closing principal receives
counts and handles, never those interactions' request or response payloads.
`CloseSurface` records the closing actor's resolved scope and principal ref in
the `surface.closed` event.

**`session_close` becomes partition-enforcing.** This is the one place this
decision narrows a shipped behavior, and it is a deliberate exception to ADR
0001 §10's treatment of `session_*` as compatibility vocabulary. It is the
operation that destroys another caller's pending human work. `session_list` and
`session_get` stay authority-wide; only the destructive operation is narrowed.
The SPA's tab-strip close button (`ui/src/components/TabStrip.tsx:58`) is
exactly the caller that must stop reaching it through an unauthenticated `/mcp`
POST — which §11 makes part of the same task rather than a follow-up.

### 10. Composition with Nanite, Tether, and Cerberus

Composition attaches at exactly two points, both established by a trusted
in-process adapter and never by a wire argument — mirroring the existing
`Capability` field, whose `json:"-"` tag and accompanying comment already state
that it "must never be accepted from a wire request"
(`internal/interaction/reference.go:16`).

1. **Verified caller binding.** An adapter that has authenticated its upstream
   raises the caller authority from `standalone-local` to `gateway:<binding_id>`
   and records the assurance it can actually attest. Tangent stores the opaque
   external refs and never calls back into the composing product to authorize a
   request.
2. **Verified participant binding.** An identity authority may bind an opaque
   `principal_ref` and a higher assurance to an existing participant session,
   rotating the session id in the same transaction. The session's capability set
   may be widened or narrowed by that authority.

Neither is required. With neither present, every shipped tool works with no new
required argument, the authority is `standalone-local`, the assurance is
`loopback-unverified`, and the participant is the local operator. Standalone use
is the default configuration, not a degraded one.

`CW-20260825-0077` **wires the `PrivilegedActorPolicy` seam**; it does not grant
the capability. *(Amended 2026-09-04, C2 — the original wording, "supplies the
real `PrivilegedActorPolicy` when a Cerberus Workspace authority is present",
reads as licence to ship an administrator. It is not, and `0077` did not.)*
Two things were conflated and are now separate:

1. **Wiring the seam — done.** `cmd/tangent` constructs a policy over
   `effect.Standalone()`, which denies both questions, so the composition point
   has a call site and a test. A composition point that had only ever run with
   the deny answer had not been tested at all; one that has never granted
   anything is still correct.
2. **Granting the capability — not done, and not grantable.** §12 puts
   credential custody out of scope and §7 says nothing administers in the
   shipped binary. Until the credential-custody decision exists there is nothing
   to grant an administrator *with*.

`CW-20260825-0075` leaves the deny-by-default policy in place and does not
invent an administrator credential. `0077` composes by providing the
host-policy *seam* this ADR reserves; it does not change the principals or the
capability set.

### 11. Scope of `CW-20260825-0075`, and the recipes it breaks

`CW-20260825-0075` implements §§1–10 **and** migrates the SPA off its direct
`/mcp` calls, in the same task. Concretely it adds a small `/api/rooms` surface
mirroring the shape of `/api/hitl` — participant-session authenticated, origin
guarded, `Cache-Control: no-store` — to replace `ui/src/routes/Room.tsx:233`'s
`session_get` POST and `ui/src/components/TabStrip.tsx:58`, `:182`'s
`session_close` and `session_list` POSTs, and it extends `hitlSameOrigin` to
`/mcp`, `/sse`, and `/ws`. Without that migration, either the tab-strip close
button breaks under §9's enforcement or `session_close` stays unenforced, and
the browser remains an unauthenticated caller on a route with no origin guard.

**Requiring a participant session on the `/ws` upgrade breaks the hand-rolled
`websocat` recipes in [`../mcp-integration.md`](../mcp-integration.md) and
[`../manual-tests/triage-e2e.md`](../manual-tests/triage-e2e.md).** Those
recipes connect to `/ws?roomID=…` with no cookie, which is precisely the
capability this ADR removes. Both documents are updated in the same change, as
part of `CW-20260825-0075` and not as a follow-up: a manual test that no longer
works is a broken contract with whoever runs it next. Any other manual-test
recipe that attaches to `/ws` without a cookie is updated in that same change.

### 12. What this ADR does not decide

Session table schema, cookie name, capability storage encoding, error-code
strings, and the `/api/rooms` route shapes are implementation choices for
`CW-20260825-0075`. Remote access, multi-user access, credential custody, a
user directory, and any authentication protocol remain out of scope and would
each need their own decision.

## Consequences

### Positive

- A room UUID stops being an answer credential. Pasting, logging, or screen-
  sharing a room URL no longer transfers the ability to resolve someone's
  pending interaction.
- `standalone-local` becomes a truthful, host-assigned scope rather than a
  string a caller can spell, and the honest strength of its internal partitions
  is written down instead of assumed.
- `CW-20260825-0075` can build a permission check directly from §7 and §8
  without re-deciding anything: resolve the principal, resolve the object,
  look up the cell.
- The two existing enforcement mechanisms — `SurfaceAccessPolicy` and
  `hitlSameOrigin` — are reused rather than replaced, so the change is additive
  in the places that already work.
- `CW-20260825-0078` inherits an explicit, enumerable list of what must never
  reach telemetry, and the list has exactly one item in it.
- Composition raises assurance without becoming required, so the standalone
  binary keeps its current ergonomics: open a URL, answer the question.

### Negative and migration risks

- Loopback admission is not authentication. A malicious local process can still
  mint a participant session by making a same-origin request. This ADR moves
  authority off the URL; it does not defend against a hostile process running as
  the same user, and it must not be described as if it does.
- Partition isolation within `standalone-local` is advisory. If that nuance is
  lost in a docstring, a downstream product will build a trust assumption on it.
  The documentation obligation in §3.4 is load-bearing.
- The SPA must stop calling `/mcp` directly. That is real work inside
  `CW-20260825-0075`, per §11, not a follow-up.
- Requiring a session on the `/ws` upgrade breaks hand-rolled `websocat`-style
  smoke recipes and any manual test that connects without a cookie.
  `docs/mcp-integration.md` and `docs/manual-tests/triage-e2e.md` are updated in
  the same change.
- A cookie on loopback HTTP cannot be `Secure` and therefore cannot use the
  `__Host-` prefix. This is inherent to the deployment shape.
- A durable session table adds a retention and revocation obligation to a
  product that previously had no user state at all. Its custody follows ADR
  0002.
- 403-within-authority and 404-across-authority is a two-shape error contract
  that adapters must not collapse.
- `session_close` narrows a shipped behavior. A caller that has been closing
  other partitions' rooms will start receiving a denial.

## Alternatives considered

### A scoped, expiring invitation carried in the room URL

Rejected. It places capability material in the one channel §6 forbids, into a
system that already logs room URLs and returns them as tool output with no
`Referrer-Policy` set. It is anti-ergonomic for a single local user, who would
face a dead link every time they returned to a room after the TTL. And on
loopback it raises no assurance over admission — it adds a secret without adding
evidence.

### A fragment-exchanged one-time handoff (`#tok=` traded for a session)

Rejected as a strictly weaker form of §4. It reaches the same destination — a
server-held session — through an extra secret that transits the URL bar, browser
history, and any agent transcript that echoes the link. On loopback the exchange
proves nothing the direct admission did not.

### Keep the room UUID as the credential and rely on its entropy

Rejected. `uuid.NewString()` has adequate entropy, but the value is deliberately
published: it is returned by `session_create`, printed in `logWorkflowRoomCreated`,
rendered in the SPA's `<h1>`, and pasted into agent transcripts by design. A
credential that the product's own ergonomics require it to broadcast is not a
credential.

### Enforce `standalone-local` partitions as a real security boundary

Rejected as dishonest. Any local caller can assert any partition, so enforcing
them would produce an authorization model whose guarantees do not survive the
first adversary. Advisory-within-authority plus enforced-across-authority states
the true strength.

### Make the caller scope the participant scope on loopback (one principal)

Rejected. It would let a caller resolve its own interaction, which destroys the
one fact Tangent is authoritative for. The caller/participant split must hold
even when both are the same human at the same machine.

### Add a shared secret or `--auth-token` flag to the binary

Rejected for v0.x. It changes standalone ergonomics from "run it and open the
URL" to "manage a secret", it puts capability material into shell history and
MCP client configuration files, and it defends against a threat (a second user
on the machine) that the single-user constraint already excludes.

### Defer participant identity until an identity authority is composed

Rejected. That is the status quo, and the status quo is that a room UUID
resolves interactions. The default loopback participant session is what makes
composition optional instead of required.

### A grace period before `/ws` requires a session

Rejected. Requiring a session on the upgrade is the change that stops a room
UUID from being an answer credential; a grace period is that bug with a
deadline, and it would leave the manual-test recipes silently working until the
day they stopped.

### Ship `session_close` enforcement without migrating the SPA

Rejected. It would break the tab-strip close button, and it would leave the
browser an unauthenticated caller on an unguarded route. The migration belongs
in the same task.

### In-memory participant sessions

Rejected. Losing every session on restart is a worse ergonomic than the
retention obligation of a session table, and it contradicts the durable
direction ADR 0001 set. SQLite storing only a SHA-256 of the cookie value is
the accepted form.

### Uniform 404 for every unauthorized access

Rejected. It leaks nothing at all, but existence leakage to the local user is
not a threat this product defends against, and a 403 within the authority is far
easier to debug.

## Review questions

Chrispian accepted this ADR on 2026-09-04. Dependent tasks may treat the
following material choices as locked:

1. `submit` is accepted as the seventh capability, alongside `view`, `draft`,
   `resolve`, `cancel`, `close`, and `administer`.
2. `session_close` becomes partition-enforcing. `session_list` and
   `session_get` stay authority-wide.
3. `CW-20260825-0075` also migrates the SPA off direct `/mcp` calls, via a
   small `/api/rooms` surface mirroring `/api/hitl`, plus extending
   `hitlSameOrigin` to `/mcp`, `/sse`, and `/ws`.
4. The `/ws` upgrade requires a participant session **immediately**, with no
   grace period. This breaks the hand-rolled `websocat` recipes in
   `docs/mcp-integration.md` and `docs/manual-tests/triage-e2e.md`; both are
   updated in the same change as part of `CW-20260825-0075`.
5. Participant sessions have no idle expiry, an absolute 30-day maximum,
   rotation on assurance change, and CLI-flag revocation.
6. Sessions persist in SQLite, storing only a SHA-256 of the cookie value.
7. Canonical scope spelling is `standalone-local:<app>`, with a read-time alias
   for `direct-loopback:<app>` and bare `standalone-local`, no data rewrite, and
   unchanged `hitl_*` request shapes.
8. Unauthorized access returns 403 within an authority and 404 across
   authorities.
9. A freshly minted loopback participant session gets `view`, `draft`,
   `resolve`, and participant-cause `cancel`. `close` and `administer` are
   withheld.
10. `CW-20260825-0075` leaves `PrivilegedActorPolicy` deny-by-default;
    `CW-20260825-0077` **wires the seam over `effect.Standalone()`, which denies
    both questions**. *(Amended 2026-09-04, C2.)* It does not supply an
    administrator: granting the capability needs the credential-custody decision
    §12 defers.
11. `Referrer-Policy: no-referrer` and `X-Content-Type-Options: nosniff` are
    applied process-wide in the middleware that already wraps the mux.
12. **`standalone-local` partitions are advisory, not a security boundary.**
    Any local caller can assert any partition; partitions are enforced only
    across authorities. This must survive into tool descriptions,
    `docs/architecture.md`, and `docs/mcp-integration.md` so that no downstream
    product trusts a partition as isolation.

The review disposition was: accept this decision with full enforcement and the
SPA migration in scope.

## Amendments required by CW-20260825-0077

**Status: Accepted.** Approved 2026-09-04 by Chrispian during the Tangent
foundation orchestration session, under `CW-20260825-0067`. Each amendment
below has been applied in place to the section it names; this block records
the reasoning.

`CW-20260825-0077` reconciled this ADR's
object-access capabilities with
[ADR 0003 §2.5](0003-definition-and-package-ownership.md)'s host-mediated
effect capabilities, and wired `PrivilegedActorPolicy`. Three things this ADR
says did not survive contact. The full model is
[`../host-mediated-capabilities.md`](../host-mediated-capabilities.md).

### C1 — "the two never substitute for one another" is necessary and not sufficient

§2 and ADR 0003 §2.5 both say the namespaces never substitute for one another,
and both stop there. Non-substitutability alone is satisfied by two systems
that ignore each other, which is the failure mode the reciprocal note warned
about from the other direction.

**Amend §2** to state the positive rule: an effect is admitted only when its
*object-access precondition* is also satisfied, evaluated through
`authz.Authorize` like every other refusal in the process. A renderer that may
not `view` an interaction may not read a file on its behalf, however complete
its effect grant. `effect.ObjectPrecondition` is the mapping, and it is a
mapping and not an equivalence — it says which question must be answered first,
never that the two capabilities are the same power.

### C2 — §Q10's "supplies a real one" admits a reading this task refused

§10 and §Q10 say `CW-20260825-0077` "supplies the real `PrivilegedActorPolicy`
when a Cerberus Workspace authority is present", which reads as licence to ship
an administrator. It should not, and this task did not: §12 puts credential
custody out of scope, and §7 says nothing administers in the shipped binary.

**Amend §Q10** to distinguish the two things it currently conflates: *wiring
the seam* (done — `cmd/tangent` constructs a policy over
`effect.Standalone()`, which denies both questions, so the composition point
has a call site and a test) from *granting the capability* (not done, and not
grantable without the credential-custody decision §12 defers). A composition
point that only ever ran with the deny answer had not been tested; one that has
never granted anything is still correct.

### C3 — §6 enumerates capability material as a closed set, and it is not

§6.1 says "the only capability material in the system is the participant
session id" and lists the two places it may appear. That was true at `0a45caa`
and stopped being true the moment scoped grants existed. An effect handle is
not a credential — possessing one grants nothing without the session and the
pinned binding — but it is short-lived grant *material* in the sense
[ADR 0002 §5](0002-retention-and-draft-custody.md) cares about, and §6 gives no
rule for it.

**Amend §6** to distinguish capability material (never leaves the session
store) from scoped locators (may travel, because they are re-authorized on
every use and grant nothing alone). Without that distinction the honest reading
of §6 forbids a handle id in a renderer payload, which would make the whole
model unimplementable — and the dishonest reading treats the handle as a bearer
token, which is worse.

## References

- [`0001-lifecycle-boundaries.md`](0001-lifecycle-boundaries.md) — §3 identity
  and idempotency, §5–§7 lifecycle authority tables, §9 drafts are not
  authoritative, §10 compatibility vocabulary
- [`0002-retention-and-draft-custody.md`](0002-retention-and-draft-custody.md)
  — §5 the browser draft gate, §6 deletion granularity and the
  `standalone-local` limitation, §8 the telemetry floor that governs log format
- [`0003-definition-and-package-ownership.md`](0003-definition-and-package-ownership.md)
  — §2.5 the separate host-mediated capability namespace, §5 publisher identity
  is provenance and not authority
- [`0005-product-boundary-and-portfolio-composition.md`](0005-product-boundary-and-portfolio-composition.md) — §3
  responsibility boundaries (participant identity) and §3.1 the secret
  boundary, promoted from the retired direction document. Its "Room invitations
  and browser sessions" paragraph stated the pre-C3 locator-versus-capability
  framing and was deliberately **not** promoted; §5 and §6 above are the
  authority.
- [`../architecture.md`](../architecture.md) — "Limits": localhost only, no
  remote access, no auth, no capability gating
- [`../mcp-integration.md`](../mcp-integration.md) — "Agent labels, MCP
  sessions, browser connections, and item URLs are not authority"; the
  `websocat` recipe updated by `CW-20260825-0075`
- [`../manual-tests/triage-e2e.md`](../manual-tests/triage-e2e.md) — the
  `websocat` recipe updated by `CW-20260825-0075`
- [`../../internal/interaction/service.go`](../../internal/interaction/service.go)
  — `authorizeInteraction`, `PrivilegedActorPolicy`, `DeliveryWorkerPolicy`,
  `SurfaceAccessPolicy`, `TerminalCauseSurfacePolicy`
- [`../../internal/interaction/reference.go`](../../internal/interaction/reference.go)
  — the `Capability string` field that must never be accepted from the wire
- [`../../internal/interaction/async_store.go`](../../internal/interaction/async_store.go)
  — `SurfaceWasOpenedByScope`
- [`../../internal/hitl/surface_policy.go`](../../internal/hitl/surface_policy.go),
  [`../../internal/hitl/service.go`](../../internal/hitl/service.go) —
  `OperatorParticipant`, reserved-surface capabilities
- [`../../internal/mcp/interaction_tools.go`](../../internal/mcp/interaction_tools.go)
  (`directMCPActor`),
  [`../../internal/mcp/hitl_tools.go`](../../internal/mcp/hitl_tools.go)
  (`directHITLActor`),
  [`../../internal/mcp/session_tools.go`](../../internal/mcp/session_tools.go)
- [`../../internal/room/room.go`](../../internal/room/room.go),
  [`../../internal/ws/handler.go`](../../internal/ws/handler.go),
  [`../../internal/server/server.go`](../../internal/server/server.go),
  [`../../internal/server/hitl.go`](../../internal/server/hitl.go)
  (`hitlSameOrigin`, `writeHITLJSON`)
- `internal/db/migrations/0003_durable_interactions.up.sql` —
  `standalone-local` legacy backfill at lines 485 and 521
- [`../../ui/src/routes/Room.tsx`](../../ui/src/routes/Room.tsx),
  [`../../ui/src/components/TabStrip.tsx`](../../ui/src/components/TabStrip.tsx),
  [`../../ui/src/lib/ws-client.ts`](../../ui/src/lib/ws-client.ts),
  [`../../ui/src/lib/hitl-api.ts`](../../ui/src/lib/hitl-api.ts)
- Torque tasks `CW-20260825-0061` (this decision), `CW-20260904-0068`
  (caller-scope precursor), `CW-20260825-0075` (implementation),
  `CW-20260825-0077` (host-mediated capabilities), `CW-20260825-0078`
  (payload-safe telemetry)

### Inbox read tools (CW-20261003-0034)

The pure MCP inbox tools use the existing caller-application `view` decision
with `PartitionScoped` access: a record's caller or its owning surface may read
it; a foreign caller authority is always hidden. Reserved HITL/docs/turns
surfaces admit this pure read workflow separately from their unchanged mutation
capabilities. No wire field can supply that host policy or a participant grant.

| Reader / condition | List and search | Get / cursor |
|---|---|---|
| Caller or owning surface, same authority, host read policy permits | Matching metadata only; no global counts | Retained request and confirmed response only; cursor rechecks access |
| Same-authority nonsubscriber | Omitted, including matches and next-page evidence | Fixed `unauthorized`; invalid cursor if the anchor is not visible |
| Foreign authority | Omitted, including matches and next-page evidence | Fixed `not_found`; invalid cursor without existence details |
| Host read policy refuses | Omitted | Fixed `unauthorized`; revoked cursor is invalid |
| Hidden or purged item | Omitted | `not_found`; anchor cursor is invalid |
| Retained redaction tombstone | Metadata may remain; removed content and tombstone text never match | Explicit tombstone and redacted flags; no recovery from other stores |

Unsubmitted drafts, participant bindings, policy, external refs, delivery
metadata and audit journals are outside this projection. Reads make no durable
retrieval/ACK or lifecycle writes. This does not change participant browser
access, authority-wide legacy session reads, or the advisory nature of local
caller partitions.
