# ADR 0003: Interaction Definition Manifest and Package Ownership

**Status:** Accepted

**Date:** 2026-09-04

**Approved:** 2026-09-04 by Chrispian, during the Tangent foundation orchestration session

**Task:** `CW-20260825-0060`

**Baseline reviewed:** `0a45caa`

**Source:** [`../interactive-collaboration-direction.md`](../interactive-collaboration-direction.md)

**Depends on:** [`0001-lifecycle-boundaries.md`](0001-lifecycle-boundaries.md).
This ADR extends, and does not restate, the vocabulary and lifecycle
boundaries locked there.

**Composes with:** [`0002-retention-and-draft-custody.md`](0002-retention-and-draft-custody.md)
(custody and telemetry fields),
[`0004-caller-participant-and-room-access-authority.md`](0004-caller-participant-and-room-access-authority.md)
(the publisher principal, and the separate access-capability namespace)

**Blocks:** `CW-20260825-0065` (versioned registry), `CW-20260825-0073`
(renderer trust classes), `CW-20260825-0074` (bundled package boundary proof),
`CW-20260825-0077` (host-mediated capabilities).

## Context

ADR 0001 made the interaction the durable unit and required every interaction
to pin "the exact definition Tangent used". The HITL slice implemented that:
`internal/interaction/catalog.go` ships an `EnvelopeDefinitionCatalog` that
derives a deterministic, content-addressed `DefinitionBinding` from the exact
registration bytes, and `internal/interaction/records.go` persists it into the
immutable `definition_bindings` table (`0003_durable_interactions.up.sql`,
guarded by `definition_bindings_immutable_update` / `_delete` triggers).

That binding is the correct foundation. It is also incomplete in a way that
blocks four downstream tasks: it pins *identity and request validation* and
nothing else. Definition ownership is currently split across five mechanisms
that do not know about each other.

**1. Registration is one shape; rendering is three shapes.**

`cmd/tangent/main.go:118-190` registers 18 Tangent-owned kinds through
`envelope.Service.RegisterTypeFromManifest(name, manifest, requestSchema,
pluginID="tangent")`, on top of the 26 core definitions loaded by
`envelopes.LoadCore`. Each in-tree manifest fragment carries a `ui.component`
slug (`TriageView`, `WizardView`, …). Nothing consumes those slugs.
`ui/src/main.tsx:282-298` instead hand-registers 17 React adapters into
`ui/src/lib/envelope-registry.ts` by wire-name string literal, and
`tangent.hitl-item` — which carries no `ui:` block at all — renders through a
third path, a bespoke `/hitl` route (`ui/src/routes/HITLInbox.tsx`) over a
hand-written client (`ui/src/lib/hitl-api.ts`). A kind binds to a renderer by
three unrelated conventions, and the manifest's own answer is dead metadata.

**2. Generated TypeScript describes definitions Tangent does not render.**

`cmd/tangent-dump-types/main.go` calls `envelopes.LoadCore` and never registers
the Tangent extensions, so `ui/src/generated/envelope-types.ts` covers exactly
the 26 upstream core kinds and none of the 18 Tangent kinds. Its
`EnvelopeKindMap` points at `components/chat/envelopes/...` paths that exist in
Nanite, not in this repo. Meanwhile every shape the Tangent UI actually uses is
hand-typed inside `ui/src/components/envelopes/*.tsx` and `ui/src/lib/hitl-api.ts`.
`make check-envelopes` gates the generated file by whole-file byte equality,
which detects drift in the 26 kinds nobody renders and cannot detect drift in
the 18 kinds everybody renders. `docs/contracts/hitl-inbox-v1.md` names the
HITL `$defs` as "code-generation targets"; no generator consumes them.

**3. Response shapes are not part of any definition.**

`EnvelopeDefinitionCatalog.ValidateInteractionResponse` compares the response
*kind* string against the pinned value and checks `json.Valid`. Nothing more.
Actual response validation is hand-written Go, per kind, per handler
(`internal/mcp/form_collect_handler.go:185`, `diff_review_handler.go:193`,
`wizard_handler.go:167`, …). Of the 18 extension schemas, only
`hitl_item_schema.json` carries a `$defs` bundle (40 entries) describing
commands and results; the other 17 are request-only `data` schemas. A pinned
binding therefore cannot answer "what may come back", which is what a durable
resolution record needs in order to be re-validatable years later.

**4. Persistence, capability, and telemetry behaviour is undeclared.**

Nine browser-local draft modules exist under `ui/src/lib/*-draft-storage.ts`
(approval-queue, dashboard, diff-review, file-picker, form-collect,
progress-panel, spreadsheet-review, whiteboard, wizard). The durable
`Service.SaveDraft` / `DraftRevision` path has zero production callers. Draft
custody is real, per-kind, and invisible to the record. `tangent.design-iteration`
executes agent-authored HTML in a narrow `srcdoc` sandbox; that trust decision
lives in a renderer, not a manifest. `internal/mcp/tools.go:37` already exposes
`workflowEntry.Capabilities []string` and hard-codes it empty with a comment
deferring the concept. There is no telemetry subsystem at all today.

**5. The upstream registry has no version, digest, or trust concept.**

`go-envelopes` v0.1.0 — the pinned dependency — is a *catalog and wire model*,
nothing more, and it is important not to design against a registry it does not
have:

- `Registry.types` is `map[string]TypeSpec` keyed on **name alone**. Two
  versions of one kind cannot coexist; a second registration of the same name
  returns `ErrConflict`. There is no `Lookup(name, version)`.
- `TypeSpec.Version` is never parsed, compared, or keyed on. `LoadCore`
  hardcodes `"1.0.0"` for all 26 core types; `RegisterTypeFromManifest` copies
  the plugin fragment's string verbatim. The library documents version gating as
  the host's job.
- `Envelope.TypeVersion` exists on the wire and is read by nothing upstream.
  The resolution rule in ADR 0001 §3 is therefore entirely Tangent's to enforce.
- Raw manifest and schema **bytes are discarded** after compilation, which is
  exactly why `envelope.Service.LookupDefinitionMaterial` keeps a side table.
  Core kinds have no retained source, so `binding()` returns
  `ErrDefinitionUnavailable` and `ListInteractionKinds` skips them — asserted by
  `TestEnvelopeDefinitionCatalogFailsClosedWithoutExactMaterial`. Failing closed
  is right, and it means the generic path cannot bind an upstream generic kind
  at all today.
- `DataSchema.Location` is a resolved resource **identity** URI
  (`plugin://tangent/envelopes/<type>.schema.json` for extensions,
  `embedded://manifest/schemas/<type>` for core), not a content hash. It does
  not change when the schema body changes. Every digest in this stack is
  Tangent-computed.
- `TypeSpec.PayloadSchema` — a *response* schema slot — already exists and is
  populated by **no** manifest path; only a hand-built `TypeSpec` passed to
  `RegisterType` can set it. `ValidateResponse` also does not check the
  response against the spec's declared `ResponseKind`, which the library calls
  "intentionally loose".
- There is no digest, publisher, capability, trust, or packaging primitive
  upstream at all. `docs/extension-api.md` explicitly defers trust to "the
  surrounding plugin SDK". `libs/plugin-sdk` does not implement it either, and
  Tangent does not depend on that SDK. **Today the trust responsibility lands
  on Tangent by default, undeclared.**

Separately, the working tree of `go-envelopes` is at **v0.3.0**, three minor
releases ahead of the pinned `v0.1.0`. The Go API is unchanged, but the catalog
**shrinks by eight kinds** (`question-form`, `todo-list`, `plan-review`, the four
`message-*`, `chat-loop-budget-soft-warning`) and `session-task` renames its
status value `cancelled` to `canceled`. Any upgrade is a registry-contents
change, not an API change.

The consequence is that "who owns this definition" has no single answer, and a
reader deciding where a *new* interaction kind belongs has no test to apply.
This ADR defines one inspectable manifest, its identity, and the ownership
split, so that `CW-20260825-0065` has something concrete to materialize.

This ADR does not authorize a plugin sandbox, an importmap/shared-chunk loader,
signing infrastructure, dynamic package loading, or removal of any shipped
tool.

## Decision

### 1. One inspectable binding: the interaction definition manifest

A single immutable document — the **interaction definition manifest** — is the
authored source of truth for one `kind@version`. Everything Tangent needs to
resolve, validate, render, isolate, persist, observe, and audit an interaction
comes from that document or from host policy applied to it. Nothing comes from
a renderer's private convention.

The manifest is authored by the **definition publisher**. Tangent resolves and
materializes it, derives a content-addressed **definition binding** from it,
and pins that binding into the interaction record. The existing
`DefinitionBinding` struct and `definition_bindings` table are the pinned
projection of the manifest; they are extended, not replaced.

Materialization states from the direction document remain the vocabulary:

```text
registered -> resolved -> verified -> materialized -> available
                                      \-> incompatible
                                      \-> quarantined
                                      \-> unavailable
```

`registered` never implies `available`. A manifest whose renderer, capability
grant, or host-version range does not resolve is `incompatible` or
`unavailable`, and `SubmitInteraction` fails closed with a typed error rather
than presenting a degraded surface.

### 2. Manifest fields

Types are given in Go/JSON terms. "Produced by" names the authority that writes
the value; where Tangent computes it, the value is *derived*, never authored.

#### 2.1 Identity

| Field | Type | Produced by | Meaning |
|---|---|---|---|
| `manifest_version` | `string` (semver) | publisher | Version of the manifest *format*. Tangent rejects a major it does not implement. |
| `publisher` | `string` | publisher | Owning identity. `hollis-labs/go-envelopes` for core; the plugin/package id otherwise. Today derived in `catalog.go:binding()` from `spec.PluginID` with a core fallback. |
| `kind` | `string` | publisher | Namespaced wire name (`tangent.triage`). Un-namespaced names remain reserved for go-envelopes core. |
| `version` | `string` | publisher | Immutable definition version. Two manifests with the same `kind@version` and different content are a publisher error, detected by digest. |
| `revision` | `int64` | publisher | Monotonic non-semantic revision within a `version`, for correcting a description or a renderer asset without a version bump. Any change that alters `contract_digest` requires a `version` bump, not a `revision` bump. |
| `title` | `string` | publisher | Human label for registry listings. |
| `description` | `string` | publisher | Free text. |
| `source` | `enum` | Tangent | Registry provenance as reported by `TypeSpec.Source.String()` (`core`, plugin, …). Already persisted. |
| `package_id` | `string` | publisher | The package that ships this manifest (see §5). |
| `package_version` | `string` | publisher | Version of that package. |
| `ownership_class` | `enum` | publisher, checked by Tangent | `generic-catalog` \| `host-package` \| `application-package`. The declared answer to the decision tests in §7; Tangent rejects a manifest whose class contradicts its publisher namespace. |

#### 2.2 Contract — request, response, error

| Field | Type | Produced by | Meaning |
|---|---|---|---|
| `request_schema` | JSON Schema (inline or `$ref` into the package) | publisher | Validates the immutable request snapshot. Today's `material.RequestSchema`. May mark individual fields **reference-only**, which is what ADR 0002 §9's `external-reference` refusal checks against. |
| `request_schema_identity` | `string` (URI) | **Tangent, derived** | The compiled schema's resource URI, today `TypeSpec.DataSchema.Location`. It is a stable *identity*, not a content hash — it does not change when the schema body changes. Persisted already as `schema_identity`. |
| `request_schema_digest` | `string` (`sha256:…`) | **Tangent, derived** | Already computed in `catalog.go:binding()`. |
| `response_kind` | `enum` | publisher | `data` \| `ack` \| `ui` \| `async-ack` \| `error`, the upstream `ResponseKind` vocabulary. Already pinned. Tangent enforces it in `ValidateInteractionResponse`; upstream `ValidateResponse` does not. |
| `response_schema` | JSON Schema | publisher | **New. Mandatory for every new definition**, per §9. Validates the terminal response payload and closes the gap where `ValidateInteractionResponse` only compares a kind string. `TypeSpec.PayloadSchema` is the existing upstream slot for it, reachable via `RegisterType` — no upstream change is needed to start carrying it. May also mark fields reference-only. |
| `response_schema_digest` | `string` | **Tangent, derived** | |
| `error_schema` | JSON Schema, optional | publisher | Typed publisher errors (the HITL `HITLStaleRevisionErrorV1` / `HITLIdempotencyConflictErrorV1` pattern, generalized). |
| `named_definitions` | `map[string]string` | publisher | Stable `$defs` entry points and what each is for — the generalization of the HITL adapter/codegen target table. Consumed by MCP schema derivation and by Sigil. |
| `contract_digest` | `string` | **Tangent, derived** | `sha256` over canonically-serialized `{request_schema, response_schema, error_schema, response_kind, named_definitions}`. The value that must not change within a `version`. |

#### 2.3 Renderer

| Field | Type | Produced by | Meaning |
|---|---|---|---|
| `renderer.id` | `string` | publisher | Stable renderer identity, distinct from `kind` so one renderer can serve several kinds. |
| `renderer.class` | `enum` | publisher | `react-component` \| `declarative` \| `sandboxed-frame` \| `external-surface`. |
| `renderer.entry` | `string` | publisher | Module specifier / exported symbol for `react-component`; a Sigil page reference for `declarative`; a frame entry for `sandboxed-frame`; a handoff URL template for `external-surface`. Replaces today's unused `ui.component` slug. |
| `renderer.asset_digest` | `string`, optional | publisher, verified by Tangent | Digest of the renderer bundle when assets ship separately from the host binary. Empty for in-tree renderers built with the release. |
| `renderer.trust_class` | `enum` | publisher declares, **Tangent policy decides** | `core-trusted` \| `portfolio-trusted` \| `declarative` \| `sandboxed-code` \| `external-surface`, exactly the direction document's classes. A manifest *requests* a class; Tangent never grants more than the trust evidence in §2.7 supports. **This is the field `CW-20260825-0073` binds to.** |
| `renderer.fallback.renderer_id` | `string`, optional | publisher | Declared safe fallback. |
| `renderer.fallback.preserves_meaning` | `bool` | publisher | Whether the fallback still captures the interaction's required decision. Tangent uses the fallback **only** when this is true (§8). |
| `renderer.fallback.degradation` | `enum` | publisher | `none` \| `read-only` \| `raw-payload`. |
| `renderer.presentation` | `object`, optional | publisher | Accessibility and layout hints (min viewport, focus mode, print/export shape). Non-authoritative. |

#### 2.4 Versions and compatibility

| Field | Type | Produced by | Meaning |
|---|---|---|---|
| `compatible_host_versions` | semver range | publisher | Against Tangent's `mcp.HostVersion` (`v0.12.0` today), already persisted as `host_version`. |
| `compatible_protocol_versions` | semver range | publisher | Against the go-envelopes wire model version. |
| `compatible_sdk_versions` | semver range | publisher | Against the plugin SDK, for out-of-tree packages. |
| `supersedes` | `[]string` | publisher | Prior `kind@version` values this replaces, for registry listing only. Never used to reinterpret a pinned interaction. |
| `compatibility_class` | `enum` | publisher, checked by Tangent | `additive` \| `breaking` relative to the previous version of the same kind. Tangent rejects `additive` when `contract_digest` changed in a non-additive way. |

#### 2.5 Capabilities

These are **host-mediated effect capabilities** — what a renderer may cause the
host to do. They are a different namespace from the seven object-access
capabilities in [ADR 0004 §2](0004-caller-participant-and-room-access-authority.md)
(`view`, `submit`, `draft`, `resolve`, `cancel`, `close`, `administer`), which
govern who may perform an operation on a surface or interaction. The two never
substitute for one another: a granted `export.download` never implies `resolve`,
and a participant's `resolve` never implies `file.read_scoped`. Both are inputs
to `CW-20260825-0077`, which must keep them separately named.

| Field | Type | Produced by | Meaning |
|---|---|---|---|
| `required_capabilities` | `[]Capability` | publisher | Host-mediated effects the renderer cannot perform on its own authority. **This is the field `CW-20260825-0077` binds to.** |
| `Capability.id` | `string` | publisher | Namespaced grant name, e.g. `clipboard.write`, `export.download`, `file.read_scoped`, `network.fetch`, `process.exec`. |
| `Capability.scope` | `object` | publisher | Grant-shaped constraint (allowed roots, allowed origins, byte ceilings). A path string is not a capability. |
| `Capability.optional` | `bool` | publisher | When true, denial degrades the renderer rather than failing resolution. |
| `Capability.rationale` | `string` | publisher | Shown to the user at grant time. |
| `granted_capabilities` | `[]Capability` | **Tangent policy, at materialization** | The intersection of requested capabilities with host policy. Persisted in the binding so a resolution records what the renderer could actually do. Never authored. |

#### 2.6 Persistence and sensitivity

Every field in this table is an **input to** the custody precedence in
[ADR 0002 §3](0002-retention-and-draft-custody.md), on the tightening side
only. A manifest may ask for less retention or forbid client persistence; it
can never loosen host or user policy.

| Field | Type | Produced by | Meaning |
|---|---|---|---|
| `draft_custody` | `enum` | publisher | `disabled` \| `ephemeral` \| `browser-local` \| `tangent-custodied` \| `external`. The nine existing `*-draft-storage.ts` modules declare `browser-local` honestly; they are not silently upgraded. The value is a truthful description of where the publisher expects drafts to live, not a grant: effective browser persistence still derives from ADR 0002 §5. |
| `retention_class` | custody pair | publisher | The `{retention, content_mode}` pair defined in [ADR 0002 §1](0002-retention-and-draft-custody.md). The single-token shorthands `ephemeral`, `interaction`, `surface`, `durable-record`, and `external-reference` are accepted and expand to that pair. It is not a flat five-value scale. |
| `sensitivity_default` | `enum` | publisher | `normal` \| `sensitive` \| `restricted`. A caller may request stricter; a package can never weaken host policy. Feeds the `browser_persistence` derivation in ADR 0002 §5. |
| `inline_payload_limit_bytes` | `int64` | publisher, capped by Tangent | Above this the definition must use artifact references. |
| `client_persistence_prohibited` | `bool` | publisher | Hard prohibition on any browser-side caching, overriding `draft_custody`. Forces `browser_persistence: forbidden`. |

#### 2.7 Trust evidence

Named `trust.*` deliberately: `evidence` is already taken in the HITL contract
for participant-facing decision material (`internal/hitl/evidence.go`) and the
two concepts must not be confused.

| Field | Type | Produced by | Meaning |
|---|---|---|---|
| `trust.assurance` | `enum` | **Tangent, derived at materialization** | How this manifest was obtained and verified: `in-tree-build` \| `content-addressed-registry` \| `signed-package` \| `unverified`. Today's binding hard-codes `content-addressed-registry`; that value is preserved for the shipped kinds. |
| `trust.source_locator` | `string` | Tangent | Where the manifest came from (embedded FS path, package path, registry URL). |
| `trust.signature` | `string`, optional | publisher | Detached signature over `manifest_digest`. Not implemented in v0.x; the field exists so `signed-package` is expressible without a schema change. |
| `trust.signer_key_id` | `string`, optional | publisher | |
| `trust.verified_at` | `time.Time` | Tangent | When verification last succeeded. |
| `trust.quarantine_reason` | `string`, optional | Tangent | Set when materialization moved the definition to `quarantined`. |

`unverified` is a reserved enum value with **no v0.x producer**. Every shipped
path is `in-tree-build` or `content-addressed-registry`, and a manifest that
resolves to neither fails registration rather than materializing as
`unverified`. Reserving the value keeps `CW-20260825-0073` from needing a
format change; it must not become a permissive fallback.

#### 2.8 Telemetry

| Field | Type | Produced by | Meaning |
|---|---|---|---|
| `telemetry.emits` | `[]string` | publisher | Typed event names the renderer may emit. An event not declared here is dropped by the host. |
| `telemetry.redact_fields` | `[]string` | publisher | JSON pointers into request/response that must never appear in a log, event, or trace. Additive tightening on top of the forbidden list in [ADR 0002 §8](0002-retention-and-draft-custody.md); it can never widen what that floor permits. |
| `telemetry.opt_in` | `bool` | publisher | Whether emission requires explicit user opt-in. Defaults true. |

Tangent has no telemetry subsystem at `0a45caa`. These fields are declared now
so that adding one later is not a manifest-format break, and so that
`redact_fields` is authored alongside the schema that needs it rather than
retrofitted.

#### 2.9 Derived digests (never authored)

| Field | Type | Meaning |
|---|---|---|
| `manifest_digest` | `string` | `sha256` over the canonically-serialized manifest excluding all derived fields. |
| `contract_digest` | `string` | §2.2. The stability guarantee for a `version`. |
| `binding_digest` | `string` | The composite already computed by `catalog.go:binding()` — manifest bytes, request schema bytes, publisher, kind, version, source, plugin id, response kind, and `envelopeDefinitionValidatorRevision`. Extended to cover `contract_digest`, `renderer.*`, `required_capabilities`, `granted_capabilities`, and `trust.assurance`. |
| `validator_revision` | `string` | The existing `envelopeDefinitionValidatorRevision` constant. Any semantic change to validation must bump it so old records fail closed instead of being reinterpreted. **This mechanism is kept exactly as shipped.** |

### 3. Identity rules

- A definition is identified by `(publisher, kind, version)`. That triple is
  immutable: republishing different content under it is an error, detected
  because `contract_digest` changed.
- `binding_digest` is the *pin*. `ResolveInteractionDefinition` continues to
  refuse to serve a binding whose current registry content no longer matches
  (`currentPinnedSpec`), and `ValidateInteractionRequest` continues to validate
  against retained bytes rather than the live compiled schema — the behaviour
  asserted by `TestEnvelopeDefinitionCatalogValidatesAgainstPinnedContent`.
- **`revision` is kept as a distinct field.** `catalog.go` currently sets
  `Revision: spec.Version`, so the column exists and is meaningless. A distinct
  monotonic revision lets a description or renderer asset be corrected without
  a version bump. It may advance within a `version` only when
  `contract_digest`, `renderer.class`, `renderer.trust_class`, and
  `required_capabilities` are all unchanged. Otherwise it is a new `version`.
- Interactions pinned to an older `revision` are never re-pinned. The registry
  may list only the newest revision; the store keeps every pinned one.
- **The version index lives in Tangent, not upstream.**
  `internal/envelope.Service` holds `map[kind]map[version]DefinitionMaterial`
  beside the upstream registry — which it already half does through
  `LookupDefinitionMaterial` — and the upstream registry remains the
  single-current-version validator. This unblocks `CW-20260825-0065` without a
  `go-envelopes` release, and can be pushed upstream later if a second host
  needs it.
- **`RegisterTypeFromManifest` gains a response-schema-carrying sibling.**
  `RegisterDefinition(manifest, requestSchema, responseSchema, pluginID)`
  builds the `TypeSpec` directly, populating the already-present
  `TypeSpec.PayloadSchema`, retains all three byte slices as
  `DefinitionMaterial`, and folds the response schema into the binding digest.
  It is the smallest change that makes §2.2's `response_schema` real and it
  needs nothing from upstream.

### 4. Source digest propagation and stale-artifact detection

Generated artifacts must carry the digest of the source that produced them, so
drift is *detected* rather than inferred from a successful build.

1. **Stamp.** Every generated file carries a header line
   `// @definition-source sha256:<hex>` where the digest is a stable hash over
   the ordered set of `(kind, version, revision, manifest_digest)` for every
   manifest that contributed to it. Sigil-generated declarative renderers and
   Go/TypeScript bindings carry the same stamp.
2. **Check.** `make check-envelopes` compares that stamp against a freshly
   computed digest instead of comparing whole file bytes. Byte comparison
   couples the gate to generator formatting and, at `0a45caa`, watches the
   wrong 26 definitions. Digest comparison reports *which* kind drifted.
3. **Coverage.** The dump tool must load the same registry the server loads —
   `LoadCore` **plus** the extension registrations from `cmd/tangent/main.go` —
   so the generated types cover all 44 shipped definitions, not 26. Extracting
   the registration list into a single `extensions.RegisterAll(svc)` used by
   both binaries is the mechanical prerequisite. Its sequencing is fixed in §9.
4. **Runtime.** The manifest digests the running host resolved are exposed by
   `tangent.interaction_list_kinds` and `tangent.interaction_resolve_definition`.
   A client holding generated types can compare its stamp against the live
   digest and refuse to submit against a definition it was not generated for.
5. **Renderer.** The renderer registry keys on `kind@version` and asserts the
   `contract_digest` it was generated against. A renderer whose stamp does not
   match the resolved manifest is treated as unavailable and the §8 fallback
   rule applies. This is what replaces `ui/src/main.tsx`'s string-literal
   registration.
6. **Persistence.** No generated artifact is an authority. A stale stamp is a
   build/CI failure and a runtime unavailability, never a reason to reinterpret
   a pinned binding.
7. **`ui/src/lib/hitl-api.ts` is generated from the HITL `$defs` bundle** as
   part of the same generator work. It is the highest-risk hand-written
   surface: a frozen v1 wire contract with 40 `$defs` and no drift detection at
   all, and `docs/contracts/hitl-inbox-v1.md` already names those `$defs` as
   code-generation targets. **Condition:** if full generation proves too large
   for that task, the accepted minimum is a `@definition-source` digest stamp
   on the hand-written file plus a test asserting it matches the bundle.
   Leaving it with neither is not an accepted outcome.

### 5. Ownership assignment

| Layer | Owns | Must not own |
|---|---|---|
| **`go-envelopes`** | The portable wire model (`Envelope`, `Response`, the `ResponseKind` / `ResponseStatus` / error-code / `Presentation` vocabularies, `Trace`), `ProtocolVersion`, the manifest *format*, and the catalog of **generic** interaction kinds with their request and response schemas. To carry a manifest it must gain three things it does not have at v0.1.0: retained source bytes plus a content digest; a registry keyed on `(name, version)` rather than name alone; and a manifest path that populates the already-present `TypeSpec.PayloadSchema`. Until then a generic kind cannot be bound at all (`ErrDefinitionUnavailable`). | Any product's workflow phases, business status enums, renderer implementations, capability grants, trust decisions, or host policy. It is a catalog, not a dumping ground — and it explicitly does not enforce trust. |
| **Plugin SDK** | How a package is declared, discovered, registered, configured, granted capabilities, initialized, observed, and stopped: the package manifest envelope carrying one or more interaction definition manifests, package identity, host/protocol/SDK compatibility ranges, config schema (no secret values), and health/diagnostics. `libs/plugin-sdk` today is Go-only subprocess JSON-RPC with declarative `plugin.yaml` registration including a `registers.envelopes` section — but it **ships no manifest struct, schema, or parser** (the shape is host-owned), has no trust or permission gating, and Tangent does not depend on it. Owning the manifest envelope is a *destination* for that SDK, not a description of it. | Interaction semantics, schemas, or renderer content. The SDK is packaging, not meaning. |
| **Tangent** | Host authority: the definition registry and materialization cache; digest derivation and binding pinning; request/response validation against retained bytes; trust-class evaluation and capability granting; the renderer host, isolation, and fallback policy; surfaces, interactions, connections, drafts, resolutions, delivery, and audit; and the compatibility adapters for shipped kinds. Owns `granted_capabilities` and `trust.assurance` — never the publisher's declarations. | Publisher-owned schemas or renderer semantics; caller-owned business state; downstream acceptance. Registration does not transfer definition ownership. A bundled package being in the binary does not make its business semantics Tangent core. |
| **Sigil** | Build-time generation from manifests: TypeScript types, Go bindings, MCP tool input schemas from `named_definitions`, and declarative renderer scaffolds for `renderer.class: declarative`. Must stamp `@definition-source` into everything it emits. | Runtime rendering, registry authority, plugin lifecycle, or trust decisions. Sigil is a code generator; it is not in the request path. |
| **Application-owned packages** (Nanite, Torque, Hadron, Fragments Engine, third parties) | Their own interaction kinds, schemas, renderers, response interpretation, and the business meaning of a resolution. The purpose, external object, requested participant, expiry policy, and downstream consequence of each request. | Tangent's operational state, surface/interaction lifecycle, or the resolution capture fact. They receive typed evidence; they do not get host authority by shipping a package. |

The split is by **authority**, not by repository. A definition living in
Tangent's tree because it is bundled with the release is still publisher-owned
content that Tangent hosts.

Sigil generates declarative renderers at build time and Tangent ships the
runtime primitives those scaffolds compose. This keeps Sigil out of the request
path, which both the Sigil README and the direction document require, at the
cost of a Tangent↔Sigil version coupling that does not exist today.

Publisher identity is definition provenance, never request authority: the
plugin publisher is explicitly a non-capability-holding principal in
[ADR 0004 §1](0004-caller-participant-and-room-access-authority.md).

### 6. Where each shipped kind belongs

At `0a45caa` the production registry is 26 core definitions plus **18**
Tangent-registered extensions (`cmd/tangent/main.go:118-190`). Seventeen have
dispatcher handlers (`internal/mcp/triage_handler.go:186-275`) and React
adapters (`ui/src/main.tsx:282-298`); `tangent.hitl-item` has neither and
instead flows through the durable interaction service and the `/hitl` route.

| Kind | Version | Response kind | Owner | Rationale |
|---|---|---|---|---|
| `tangent.triage` | 0.1 | data | **`go-envelopes` generic catalog** | Accept/reject/annotate an opaque item. Names no product noun; already flagged in-tree for core upstreaming in go-envelopes v0.3. |
| `tangent.feedback` | 0.2 | data | **`go-envelopes` generic catalog** | A short structured questionnaire is domain-free. |
| `tangent.form-collect` | 0.6 | data | **`go-envelopes` generic catalog** | Schema-driven form; the schema is caller-supplied, so the kind carries no business vocabulary. |
| `tangent.interview-question` | 0.3 | data | **`go-envelopes` generic catalog** | One long-form question with quick-picks. |
| `tangent.output-render` | 0.3 | ack | **`go-envelopes` generic catalog** | Present markdown, acknowledge. Pure presentation. |
| `tangent.progress-panel` | 0.10 | data | **`go-envelopes` generic catalog** | Generic progress items and summary state. |
| `tangent.approval-queue` | 0.7 | data | **Tangent package** (`tangent.review`) | Bounded serialized review with evidence panes and audit-export metadata. Reusable, but its queue and audit semantics are host-shaped, and ADR 0001 explicitly forbids collapsing it into the HITL inbox. |
| `tangent.diff-review` | 0.8 | data | **Tangent package** (`tangent.review`) | Per-file/per-hunk decisions with durable artifact refs; needs scoped artifact capability. |
| `tangent.spreadsheet-review` | 0.5 | data | **Tangent package** (`tangent.review`) | Review-only tabular decision with CSV export metadata (`export.download` capability). |
| `tangent.file-picker` | 0.9 | data | **Tangent package** (`tangent.workspace`) | Requires `file.read_scoped` with allowed roots; capability-bearing by nature. |
| `tangent.design-iteration` | 0.2 | data | **Tangent package** (`tangent.canvas`) | Executes untrusted agent-authored HTML. Must declare `renderer.class: sandboxed-frame` and carry the existing `srcdoc` policy as manifest, not as renderer convention. |
| `tangent.whiteboard` | 0.4 | data | **Tangent package** (`tangent.canvas`) | Heavy third-party renderer (tldraw) with a persisted scene; asset-digest-bearing. |
| `tangent.dashboard` | 0.11 | data | **Tangent package** (`tangent.canvas`) | Tile/layout state with explicit refresh turns; presentation-navigation, host-shaped. |
| `tangent.wizard` | 0.12 | data | **Tangent package** (`tangent.compound`) | A bounded compound submission — permitted by the workflow boundary, but its step/branch state is presentation navigation Tangent hosts. It is the closest call in this table; see the condition below. |
| `tangent.block-draft` | 0.3 | data | **Application package** (writing publisher) | Accumulates accepted blocks on the room. Business semantics of "accepted block" belong to the writing application. |
| `tangent.prose-revision` | 0.3 | data | **Application package** (writing publisher) | Per-suggestion outcomes under a review/copy/style lens — an editorial domain vocabulary. |
| `tangent.synthesis-notes` | 0.3 | ack | **Application package** (writing publisher) | Private working notes with phase-gated preview; depends on `session_advance_phase` semantics that the workflow boundary assigns to the caller. |
| `tangent.hitl-item` | 1.0 | data | **Tangent core-adjacent package** (`tangent.hitl`) | The operator inbox is host infrastructure with a frozen v1 wire contract. Stays in Tangent, but as a package with a manifest like any other — it is the reference implementation of the boundary. |

Three conditions attach to this table.

- **The `go-envelopes` generic catalog column is a destination, not a plan.**
  The ownership label is adopted now so the decision tests in §7 have force. No
  upstream move is scheduled, and none may be scheduled, until two conditions
  both hold: `go-envelopes` retains source bytes for core kinds (so a generic
  kind can be bound at all), and a second host actually consumes one.
  `tangent.triage` is the natural first candidate because upstreaming is
  already flagged in-tree.
- **`tangent.wizard` is a Tangent package while its steps stay presentation
  navigation.** If a shipped wizard's steps carry business gates, it is a
  Hadron run wearing a costume and it moves to T4 as an application package.
- **The three writing kinds are labelled application-package now and stay
  bundled until a writing application exists to own them.** Calling them
  Tangent packages would be the "bundled therefore core" error the direction
  document warns about and would make Tangent responsible for editorial
  semantics.

`internal/hitl/` is already the working prototype of a Tangent package: it owns
one definition, reserves a named surface behind an application-internal
capability string (`internal/hitl/surface_policy.go`), validates against its
own schema bundle, ships its own MCP tools and its own route.
`CW-20260825-0074` proves the boundary against one of the six
`go-envelopes generic catalog` candidates, because those are the ones whose
migration also exercises the upstream boundary.

No kind changes repository, wire name, schema, or response shape as a result of
this ADR.

The manifest for a bundled kind lives as a standalone `manifest.yaml` plus its
schema files in the package directory, embedded with `//go:embed`, mirroring
how `hitl_item_schema.json` is already embedded. The inline `[]byte` YAML
strings in `internal/envelope/extensions/*.go` do not survive the field growth
in §2, and a file has a digest-able identity.

### 7. Decision tests

Apply in order. The first test that answers "yes" assigns ownership.

**T1 — Generic-catalog test (`go-envelopes`).** Would two unrelated publishers
submit this kind, with no shared business vocabulary between them? Is the
response interpretable without knowing the caller's domain? Does the schema
name no product noun, status enum, or policy? Would a reasonable second host
(Nanite inline, a CLI) render it? *All four yes* → `go-envelopes` generic
catalog.

**T2 — Host-primitive test (Tangent core).** Is this about the surface,
interaction, connection, resolution, or delivery *lifecycle* — rather than
about what is being decided? Would every kind break if it were removed? Does it
mediate an effect no renderer may perform on its own authority? *Yes* →
Tangent core, not a definition at all.

**T3 — Host-package test (Tangent package).** Is it reviewed and shipped with
the Tangent release, does it need a renderer inside Tangent's own React tree or
host-mediated capabilities, and are its semantics about *how a decision is
presented* rather than *what the decision means to a business*? *Yes* → a
Tangent-distributed package: its own manifest, package id, capability
declarations, and reserved surface if it needs one.

**T4 — Application-package test (publisher-owned).** Does the schema name a
product's business object, status vocabulary, phase, or policy? Would a domain
owner — not Tangent — decide whether a response was correct? Does its meaning
change when the calling application changes? *Yes* → application-owned package,
published by that application, hosted by Tangent.

**T5 — Workflow-state test.** Does it need state that survives *between*
interactions and is not presentation navigation? *Yes* → it is not an
interaction definition at all; it belongs to the caller or to Hadron. Room
`current_phase` and arbitrary phase outputs remain compatibility projections
and must not become the model for anything new.

**T6 — Trust test (applies to every answer above).** Does it execute code, load
third-party assets, render untrusted markup, or touch the filesystem, clipboard,
network, or a process? *Yes* → it must declare `renderer.trust_class` and
`required_capabilities`, and it cannot be `core-trusted` unless it ships and is
reviewed with the Tangent release. Ambient host authority is never inherited.

**Tie-break.** When T1 and T3 both plausibly apply, prefer T3 and revisit after
a second independent publisher actually adopts the kind. Promoting a package
kind into the generic catalog later is additive; demoting a generic kind out of
the shared catalog is a breaking change for every host.

### 8. Compatibility and safe fallback rules

**C1 — Pinned bindings are never reinterpreted.** Unchanged from ADR 0001 and
from the shipped implementation. A pinned interaction validates against its
retained bytes. A registry change that alters the current binding makes the
pinned definition `unavailable` for *new* submissions; it never rewrites an
existing record.

**C2 — Version semantics.** Additive optional fields → minor `version` bump,
`compatibility_class: additive`. Removing, renaming, changing the meaning of a
field, widening authority, adding a terminal enum value, changing
`response_kind`, raising `renderer.trust_class`, or adding a
`required_capabilities` entry → new major `version`. This generalizes the rules
already written in `docs/contracts/hitl-inbox-v1.md`.

**C3 — Shipped kinds register through compatibility package adapters.** All 18
extension kinds and `tangent.hitl-item` keep their exact wire names, versions,
request schemas, response payloads, MCP tool names, `roomID`, `/r/<roomID>`
routes, persisted history, and phase projections. The adapter emits a manifest
on their behalf; `RegisterTypeFromManifest` remains the registration path. **No
wire breakage, in either direction.**

**C4 — Legacy manifest defaults are honest and restrictive.** Where a shipped
kind has no authored value, the adapter fills in what the code actually does,
never something more permissive: `trust.assurance: content-addressed-registry`
(today's literal value) for extension kinds, `in-tree-build` for renderers built
with the release; `required_capabilities: []`; `draft_custody: browser-local`
for the nine kinds with a `*-draft-storage.ts` module and `disabled` for the
rest; `renderer.class: react-component` except `tangent.design-iteration`,
which is `sandboxed-frame`. A missing `response_schema` yields
`compatibility_response_schema: absent` and preserves today's kind-only check —
it does not silently start rejecting responses that production accepts.

`retention_class` is deliberately left **unauthored** for the shipped kinds, so
that ADR 0002 §4's host default (`interaction`, redacted 30 days after
terminal) governs new interactions of those kinds while every row that already
exists keeps `surface` under ADR 0002 §12. Authoring `surface` into a
compatibility manifest would re-loosen, through the `min()` in ADR 0002 §3,
exactly the default that ADR 0002 tightened — which is the opposite of what C4
is for.

**C5 — Safe fallback.** Tangent uses a fallback renderer **only** when the
manifest declares one and `renderer.fallback.preserves_meaning` is true. It
must never downgrade a structured decision to a free-text box or drop required
evidence. With no qualifying fallback, resolution fails closed with the existing
upstream error code `component-load-failed`, the interaction stays non-terminal,
and the caller is told the definition is unavailable — a renderer problem is not
a participant cancellation. The existing JSON debug fallback in
`EnvelopeRouter.tsx` remains a *development* affordance and is not a
`preserves_meaning` fallback.

**C6 — Frozen contracts.** `tangent.hitl-item` v1.0, the `/api/hitl` surface,
and the four `tangent.hitl_*` tools are frozen at their contract version.
`tangent.session_*`, the workflow-named tools, and `tangent.approval-queue`
keep their current shapes. Any removal requires a separate accepted ADR, per
ADR 0001 §11.7.

**C7 — Unavailable is a state, not an error to paper over.** `incompatible`,
`quarantined`, and `unavailable` are distinguishable in the registry projection
and in `tangent.interaction_resolve_definition`. Failing closed —
`ErrDefinitionUnavailable`, as already implemented — is the required behaviour.
The existing upstream error vocabulary is sufficient and is reused rather than
extended: `unsupported-type`, `unsupported-version`, `validation-failed`,
`capability-denied`, `component-load-failed`.

### 9. Sequencing

Two ordering decisions are part of this ADR, not left to implementation
scheduling.

**S1 — The dump-tool coverage fix lands as an isolated commit before
`CW-20260825-0065`.** `cmd/tangent-dump-types` is changed to load the same
registry the server loads, the extension registration list is extracted from
`cmd/tangent/main.go:118-190` into a single `extensions.RegisterAll(svc)` used
by both binaries, and `ui/src/generated/envelope-types.ts` is regenerated to
cover all 44 shipped definitions instead of 26.

That commit contains nothing else. The regenerated file is a large one-time
diff, and isolating it means the diff can be read as "the generator now sees
the right registry" rather than being buried inside the registry rewrite. It
lands first because until it does, the staleness gate points at the wrong 26
definitions and `CW-20260825-0065` would be built against a generator that
cannot see the kinds it is meant to version. `extensions.RegisterAll` is the
mechanism that stops the server and the dump tool drifting apart again.

**S2 — `response_schema` is mandatory for new definitions immediately;
the seventeen shipped kinds are backfilled one at a time.** They carry
`compatibility_response_schema: absent` under C4 in the meantime, and are
authored from the handlers' actual behaviour — not from what the handlers ought
to do — during the `CW-20260825-0074` package work. Making it mandatory for
shipped kinds immediately would block `CW-20260825-0065` behind seventeen
schema-writing tasks.

**S3 — Tangent stays on `go-envelopes` v0.1.0 through this ADR's
implementation.** The upgrade to v0.3.0 is a separate task, taken after the
digest-stamped generator lands, so the catalog change — eight kinds removed,
`session-task`'s `cancelled` renamed to `canceled` — surfaces as a named
per-kind drift report rather than inside a large generated diff. Wiring the
shipped `envelopestest.RunContract` into Tangent's suite first gives a cheap
upgrade tripwire. None of the eight removed kinds is used by Tangent today, so
the upgrade is low-risk; it should still not be bundled with a registry
rewrite.

### 10. Not decided here

Dynamic/out-of-process package loading, a JavaScript sandbox runtime,
importmaps or shared chunks, signing infrastructure and key management, the
capability *grant UI*, an external definition registry, and the concrete SQL for
the extended binding columns. The v0.x prohibition on a Nanite-style plugin
sandbox stands; nothing in this ADR authorizes one. `renderer.class:
sandboxed-code` is expressible in the manifest and unimplemented in v0.x.

## Consequences

### Positive

- One document answers every definition question, and the answer is pinned into
  the interaction record and inspectable through MCP.
- `CW-20260825-0065` has a concrete field list to materialize; `0073` binds to
  `renderer.trust_class`; `0077` binds to `required_capabilities` /
  `granted_capabilities`; `0074` has a decision test to prove against.
- Response shapes become part of the contract, so a durable resolution can be
  re-validated later against the exact definition that produced it.
- Digest-stamped artifacts make schema drift a reported failure naming the
  drifted kind, instead of a whole-file diff on the wrong 26 definitions.
- Renderer binding becomes one mechanism instead of three.
- Draft custody, sandbox policy, and export capability become declared and
  auditable rather than hard-coded in nine UI modules and one iframe.
- The manifest's custody, sensitivity, and telemetry fields give ADR 0002 a
  publisher-authored input on the tightening side, so a definition that knows
  its payload is sensitive can say so at the point where the schema is written.

### Negative and costs

- Every shipped kind needs a `response_schema` authored where only hand-written
  Go validation exists today — seventeen schemas, and they must be written from
  the handlers' actual behaviour, not from what the handlers ought to do.
- The dump tool must load the full registry, which couples `cmd/tangent-dump-types`
  to the extension registration list; the generated TypeScript grows from 26
  to 44 kinds and the committed file changes substantially in one commit. S1
  isolates that commit so the diff is readable.
- The manifest is large. A publisher authoring a simple kind must still make
  explicit choices about custody, trust, and capabilities. That is the point,
  but it raises the floor.
- `go-envelopes` must retain source bytes for core definitions before any
  generic kind can be bound; until then §6's `generic-catalog` column is a
  destination, not a shipping plan.
- Two capability namespaces now exist — host-mediated effects here, object
  access in ADR 0004. `CW-20260825-0077` consumes both and must not merge them.

### Risks

- Six of the eighteen shipped kinds are labelled for upstream. Moving a kind out
  of `tangent.*` namespacing is a wire-name change and therefore a breaking
  change for any caller. The safe path is dual registration under C3 with the
  `tangent.*` name retained indefinitely — which means the "generic catalog"
  destination may never fully arrive, and that is acceptable.
- Declaring `browser-local` draft custody for nine kinds makes an existing
  weakness legible. Reviewers may read the ADR as introducing it.
- The manifest presumes a registry that can hold two versions of one kind.
  `go-envelopes` v0.1.0 cannot (name-only key, `ErrConflict` on duplicate),
  which is why §3 puts the version index in Tangent. Designing as though
  upstream already supported it is the most likely way this ADR produces an
  unimplementable task.
- Six core kinds ship with `DataSchema == nil` at v0.1.0 (`todo-list`,
  `plan-review`, and the four `message-*`), and five schema files in the
  upstream manifest directory are never registered at all. "Registered" and
  "has a validatable request schema" are already different states upstream.

## Alternatives considered

### Make `response_schema` mandatory for the shipped kinds immediately

Rejected. It would block `CW-20260825-0065` behind seventeen schema-writing
tasks, each of which must be authored from handler behaviour rather than
intent. C4's `compatibility_response_schema: absent` preserves today's
kind-only check without pretending a schema exists.

### Delete the `revision` column and let `version` carry everything

Rejected. `catalog.go` currently sets `Revision: spec.Version`, so the column
is present and meaningless. A distinct monotonic revision lets a description or
a renderer asset be corrected without version churn, and §3 forbids using it to
change any contract-bearing field.

### Change the upstream registry key to `(name, version)` first

Rejected as the first move. It makes `CW-20260825-0065` wait on a
`go-envelopes` release and a Tangent dependency bump. The version index lives
in Tangent, where `LookupDefinitionMaterial` already keeps a side table, and
can be pushed upstream later if a second host needs it.

### Let `trust.assurance: unverified` be a permissive fallback

Rejected. The value is reserved so `signed-package` and future classes are
expressible without a format change, but no v0.x path produces it: a manifest
that resolves to neither `in-tree-build` nor `content-addressed-registry` fails
registration.

### Keep manifests as inline `[]byte` YAML beside the Go registration

Rejected. The inline strings in `internal/envelope/extensions/*.go` do not
survive the field growth in §2, and a file has a digest-able identity that an
inline literal does not.

### Ship the dump-tool coverage fix inside `CW-20260825-0065`

Rejected. The regenerated `envelope-types.ts` is a large one-time diff; folded
into the registry rewrite it becomes unreviewable, and until it lands the
staleness gate watches the wrong definitions. S1 makes it an isolated
prerequisite commit.

### Upgrade to `go-envelopes` v0.3.0 as part of this work

Rejected. The upgrade removes eight catalog kinds and renames a status value.
Bundled with a registry rewrite those changes are invisible; taken after the
digest-stamped generator lands they surface as a named per-kind drift report.

### Have Tangent ship its own declarative-renderer generator

Rejected. Sigil generates at build time and Tangent ships the runtime
primitives those scaffolds compose, which is what keeps Sigil out of the
request path. The cost is a Tangent↔Sigil version coupling that does not exist
today.

## Review questions

Chrispian accepted this ADR on 2026-09-04. Dependent tasks may treat the
following material choices as locked:

1. `response_schema` is mandatory for every new definition. The seventeen
   shipped kinds carry `compatibility_response_schema: absent` under C4 and are
   backfilled one at a time during `CW-20260825-0074`.
2. `revision` is kept as a field distinct from `version`, constrained by §3.
3. The six `go-envelopes` generic-catalog assignments are a destination only.
   No upstream move is scheduled until go-envelopes retains source bytes for
   core kinds **and** a second host consumes one.
4. **The dump-tool coverage fix lands as an isolated commit before
   `CW-20260825-0065`**, extracting `extensions.RegisterAll(svc)` so the server
   and the dump tool cannot drift again (§9 S1).
5. `ui/src/lib/hitl-api.ts` is generated from the HITL `$defs` bundle in the
   same generator work; if that is too large, the accepted minimum is a digest
   stamp plus a test asserting the file matches the bundle (§4.7).
6. Sigil owns build-time declarative-renderer generation; Tangent ships the
   runtime primitives. Sigil stays out of the request path.
7. `tangent.wizard` is a Tangent package while its step/branch state is
   presentation navigation; a wizard carrying business gates moves to T4.
8. The three writing kinds are application-package by label and stay bundled
   until a writing application exists to own them.
9. `trust.assurance: unverified` is a reserved value with no v0.x producer.
10. A bundled kind's manifest is a standalone `manifest.yaml` plus schema files
    per package directory, embedded with `//go:embed`.
11. The version-indexed registry is built in Tangent, not upstream.
12. Tangent stays on `go-envelopes` v0.1.0 through this implementation; the
    v0.3.0 upgrade is a separate task after the digest-stamped generator lands.
13. `envelope.Service` gains `RegisterDefinition(manifest, requestSchema,
    responseSchema, pluginID)` carrying the response schema into
    `TypeSpec.PayloadSchema` and the binding digest.

The review disposition was: accept this decision with the defaults above folded
in, and with the dump-tool fix sequenced as an isolated prerequisite commit.

## References

- [`0001-lifecycle-boundaries.md`](0001-lifecycle-boundaries.md) — §3
  definition binding and `TypeVersion` resolution, §10 compatibility
  vocabulary, §11.7 removal requires a separate ADR
- [`0002-retention-and-draft-custody.md`](0002-retention-and-draft-custody.md)
  — §1 custody pair, §3 precedence, §5 browser persistence derivation, §8
  telemetry floor, §9 `external-reference` refusal
- [`0004-caller-participant-and-room-access-authority.md`](0004-caller-participant-and-room-access-authority.md)
  — §1 the plugin publisher holds no capability, §2 the object-access
  capability namespace
- [`../interactive-collaboration-direction.md`](../interactive-collaboration-direction.md)
- [`../contracts/hitl-inbox-v1.md`](../contracts/hitl-inbox-v1.md) — `$defs`
  named as code-generation targets, version semantics
- [`../../internal/interaction/catalog.go`](../../internal/interaction/catalog.go)
  — `EnvelopeDefinitionCatalog`, `binding()`,
  `envelopeDefinitionValidatorRevision`, `currentPinnedSpec`,
  `ValidateInteractionResponse`
- [`../../internal/interaction/records.go`](../../internal/interaction/records.go)
  — `DefinitionBinding`
- `internal/db/migrations/0003_durable_interactions.up.sql` —
  `definition_bindings` and its immutability triggers
- [`../../cmd/tangent/main.go`](../../cmd/tangent/main.go) — the 18 extension
  registrations at lines 118-190
- [`../../cmd/tangent-dump-types/main.go`](../../cmd/tangent-dump-types/main.go)
- [`../../ui/src/main.tsx`](../../ui/src/main.tsx) — the 17 string-literal
  renderer registrations at lines 282-298
- [`../../ui/src/lib/envelope-registry.ts`](../../ui/src/lib/envelope-registry.ts),
  [`../../ui/src/generated/envelope-types.ts`](../../ui/src/generated/envelope-types.ts),
  [`../../ui/src/lib/hitl-api.ts`](../../ui/src/lib/hitl-api.ts),
  [`../../ui/src/routes/HITLInbox.tsx`](../../ui/src/routes/HITLInbox.tsx)
- [`../../internal/mcp/tools.go`](../../internal/mcp/tools.go) —
  `workflowEntry.Capabilities`, hard-coded empty
- `internal/mcp/form_collect_handler.go`, `internal/mcp/diff_review_handler.go`,
  `internal/mcp/wizard_handler.go`, `internal/mcp/triage_handler.go` —
  hand-written per-kind response validation
- [`../../internal/hitl/surface_policy.go`](../../internal/hitl/surface_policy.go)
  — the reserved-surface prototype of a Tangent package
- `ui/src/lib/*-draft-storage.ts` — the nine browser-local draft modules
- `github.com/hollis-labs/go-envelopes` v0.1.0 — `Registry`, `TypeSpec`,
  `LoadCore`, `RegisterTypeFromManifest`, `ValidateResponse`,
  `envelopestest.RunContract`, `docs/extension-api.md`
- Torque tasks `CW-20260825-0060` (this decision), `CW-20260825-0065`
  (versioned registry), `CW-20260825-0073` (renderer trust classes),
  `CW-20260825-0074` (bundled package boundary proof), `CW-20260825-0077`
  (host-mediated capabilities)
