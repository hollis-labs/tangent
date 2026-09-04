# Changelog

All notable changes to Tangent are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- **Separate liveness, readiness, and capability health.** `/healthz` proved
  only that the process was responding while reporting `{"status":"ok"}` for a
  host whose database, migrations, registry, renderer host, or requested kind
  was unavailable. Three probes now answer three questions: `GET /healthz`
  (liveness — touches no dependency, so a supervisor never restarts a
  repairable process in a loop), `GET /readyz` (readiness — database, schema
  version against the version this binary embeds, definition registry, renderer
  host, delivery-worker authorization; `ok`/`degraded`/`unavailable`, 503 only
  on the last), and `GET /healthz/capability[/{kind}]` (per-kind, reported in
  `internal/definition`'s existing `available`/`incompatible`/`quarantined`/
  `unavailable` vocabulary rather than a second one). `tangent.health_report`
  exposes all three over MCP for a Tether-connected client with no HTTP path to
  the host; production `tools/list` is now 44. Every non-passing check carries
  an operator action, and no report carries a payload, participant text,
  filesystem path, session, or capability material.
- **Versioned interaction-definition registry.** Every Tangent-owned kind now
  ships as a package under `internal/envelope/extensions/packages/` with an
  authored `manifest.yaml`: identity, request/response/error schemas, renderer
  binding and trust class, host-mediated effect capabilities, draft custody and
  sensitivity, trust evidence, telemetry, and compatibility ranges. See
  [ADR 0003](docs/adr/0003-definition-and-package-ownership.md).
- **Durable definition material.** Migration `0007` adds the immutable
  `definition_manifests` table and extends `definition_bindings` with the full
  manifest identity. A submitted interaction retains the exact material behind
  its pinned digest, so it can still be validated and replayed after a restart,
  after the installed catalog changes, and after the current version moves on.
- **`tangent.definition_registry_list`, `tangent.definition_get`,
  `tangent.definition_registry_diagnostics`.** Payload-bounded registry
  diagnostics reporting materialization state, ownership, renderer binding,
  digests, and retained-material coverage.
- **Response-schema validation.** `envelope.Service.RegisterDefinition` carries
  a response schema into `TypeSpec.PayloadSchema` and into the binding digest.
  `tangent.hitl-item` is the first kind to use it; the other seventeen declare
  `compatibility_response_schema: absent` and keep today's response-kind-only
  check until they are backfilled.
- **Definition source digests on generated artifacts.** Every generated file
  carries a `@definition-source` stamp, `make check-envelopes` reports which
  kind drifted rather than which byte, and `ui/src/generated/renderer-bindings.ts`
  makes the manifest's renderer binding checkable against `main.tsx`.
- **Authenticated browser participant sessions.** A same-origin loopback
  document navigation with no session cookie mints one: an `HttpOnly`,
  `SameSite=Lax` cookie naming a durable row (migration `0008`) that stores
  only the SHA-256 of the cookie value. No idle expiry, absolute 30-day
  lifetime, rotation on assurance change, and revocation via
  `tangent --revoke-participant-sessions`. See
  [ADR 0004](docs/adr/0004-caller-participant-and-room-access-authority.md).
- **Object-access authorization.** Seven capabilities (`view`, `submit`,
  `draft`, `resolve`, `cancel`, `close`, `administer`) evaluated against a
  named surface or interaction through one decision function in
  `internal/authz`. Caller scope is `<authority>:<partition>`, with the
  authority host-assigned from admission facts and the partition
  caller-declared.
- **`/api/rooms`.** A participant-authenticated, origin-guarded browser room
  API mirroring `/api/hitl`, replacing the SPA's direct `/mcp` JSON-RPC POSTs
  for `session_list`, `session_get`, and `session_close`.
- **Process-wide `Referrer-Policy: no-referrer` and
  `X-Content-Type-Options: nosniff`,** applied in the middleware that wraps the
  mux so no route can forget them.

### Changed

- **`definition_bindings.revision` is a real field.** It previously held a copy
  of `version`. Migration `0007` normalizes existing rows to `1`; new bindings
  carry the manifest's own monotonic revision.
- **Definitions that cannot be served are distinguishable.** `incompatible`,
  `quarantined`, and `unavailable` surface as `definition_incompatible`,
  `definition_quarantined`, and `definition_unavailable`, each carrying a reused
  go-envelopes error code. No shipped kind is in any of those states.
- **A room UUID is no longer an answer credential.** The `/ws` upgrade requires
  a valid participant session immediately, with no grace period. A raw
  WebSocket client that dials `/ws?roomID=…` with no cookie now receives a 403.
  Opening the room URL in a browser mints the session and is unchanged for a
  human; scripted attachments need the cookie recipe in
  [`docs/mcp-integration.md`](docs/mcp-integration.md#room-access-and-caller-scope).
- **`tangent.session_close` and `tangent.surface_close` are
  partition-enforcing.** Closing dispositions another caller's pending human
  work. `session_list` and `session_get` stay authority-wide, so "show me all
  my rooms" is unchanged. The seventeen workflow tools and
  `tangent.session_advance` can no longer push into a room another partition
  owns. Refusals are 403-shaped within an authority and 404-shaped across one.
- **`caller.scope`, `requester_scope`, and `owner_scope` are no longer the
  authorization value.** The arguments are still accepted so shipped schemas do
  not break, but only their partition half survives; the authority is
  host-assigned. `tangent.surface_open` derives `owner_scope` from the caller
  and keeps the supplied value as an attribution label.
- **`tangent.interaction_submit` checks the target surface.** A caller can no
  longer create an interaction on a surface it neither owns nor opened.
- **Caller scope spelling.** `direct-loopback:<app>` becomes
  `standalone-local:<app>`. `hitl_*` request shapes are unchanged, existing rows
  are not rewritten, and the old spelling reads as the same caller through a
  fixed alias.
- **`/mcp`, `/sse`, and `/ws` carry the same-origin guard** `/api/hitl/*`
  already had. It permits header-less non-browser clients, so MCP clients and
  the documented `curl` recipes are unaffected.

**`standalone-local` partitions are advisory, not a security boundary.** Any
local caller can assert any partition; isolation is enforced only across
authorities, where the prefix is host-assigned. Loopback admission is not
authentication: a hostile local process running as the same user can still mint
a participant session.

No wire name, version, request schema, response payload, MCP tool name, room
id, or phase projection changed. Two routes were added (`/api/rooms`,
`/api/rooms/{roomID}`); none was removed.

## [v0.12.0] - 2026-05-10

Wizard. Tangent now ships a persistent room-backed guided wizard
workflow with explicit partial updates, canonical branch-aware step
progress, browser-local draft recovery, and final completion through
the existing room/tool contract.

### Added

- **`tangent.wizard`.** A bundled room-backed wizard workflow with
  canonical step definitions, explicit partial updates, and final
  completion through the existing room/tool contract.
- **Wizard room substrate.** `phase_outputs["wizard"]` now persists
  `wizard_id`, canonical `steps`, `current_step_id`, accepted
  `progress`, deterministic `branch_selections`, summary fields, and
  `updated_at`, all surfaced through `tangent.session_get.wizard`.
- **Wizard browser host.** Tangent now ships a step-based wizard host
  with reopen hydration, browser-local draft recovery, review summary,
  back/next navigation, attachment-style response capture, and action
  output affordances.
- **Wizard manual test coverage.** Added
  `docs/manual-tests/wizard-e2e.md`.
- **Repo-local Tangent workflow skills.** Added tight copy/paste
  launcher skills under `.agents/skills/` for the bundled workflows and
  the multi-phase writing flow so operators can reuse known-good
  prompts and payload shapes during manual runs.

### Changed

- **`tangent.session_get` now projects wizard state.** Agents can read
  canonical wizard room state directly without scraping envelope
  history.
- **Wizard submit semantics are explicit.** Partial room-backed saves
  stay `status: "partial"` while final completion resolves with
  `status: "submitted"` and a completed summary payload.
- **Manual smoke docs are stricter about launch discipline.** The smoke
  runbook now calls out payload-sensitive workflows, shared-room
  writing-flow sequencing, and the one-tab-per-room rule while a submit
  is pending.

### Fixed

- **Wizard manual docs now include a minimal valid seed payload.**
  Operators no longer need to infer the required `steps`,
  `current_step_id`, and branch target shape from tests or handlers.

## [v0.11.0] - 2026-05-10

Dashboard. Tangent now ships a persistent room-backed dashboard
workflow for explicit refresh/update turns, saved layout reuse,
browser-local layout draft recovery, room/artifact drill-down, and
concise accepted snapshot export metadata.

### Added

- **`tangent.dashboard`.** A bundled room-backed dashboard workflow
  with canonical tiles, explicit refresh/update submits, and room reuse
  via `meta.roomID`.
- **Dashboard room substrate.** `phase_outputs["dashboard"]` now
  persists `dashboard_id`, canonical `tiles`, normalized `layout`,
  room-backed `saved_layouts`, `active_layout_id`, normalized
  `query_state`, summary fields, append-only `snapshot_history`, and
  accepted `export_state`.
- **Saved layouts and drill-down flows.** The shipped host now supports
  reusable saved layouts, browser-local draft recovery for in-progress
  layout edits, room-route drill-down, and artifact-ref handoff actions.
- **Accepted export/share metadata.** Accepted dashboard submits persist
  a deterministic `export_state` payload with snapshot id, active
  layout, tile count, and stable room/artifact refs for downstream
  inspection.
- **Dashboard e2e coverage.** Added
  `docs/manual-tests/dashboard-e2e.md`.

### Changed

- **`tangent.session_get` now projects dashboard state.** It exposes a
  dedicated `dashboard` payload with canonical tiles, normalized
  layout/query state, saved layouts, accepted snapshot history, and the
  latest export/share metadata.
- **Docs now describe dashboard as the newest bundled workflow.**
  README, MCP integration, and manual test docs now reflect the new
  tool surface and the localhost-only single-user posture of the
  dashboard workflow.

### Fixed

- **Rejected dashboard submits no longer risk mutating accepted export
  metadata.** Invalid payloads still return explicit rejected response
  shapes while preserving the last accepted room-backed snapshot and
  export state for reopen.

## [v0.10.0] - 2026-05-10

Progress panel. Tangent now ships a persistent room-backed
progress-panel workflow for explicit update/reopen turns, append-only
status history, checkpoint summaries, operator recovery, and concise
export snapshots.

### Added

- **`tangent.progress-panel`.** A bundled room-backed progress workflow
  with canonical tracked items, explicit update submits, and room reuse
  via `meta.roomID`.
- **Progress-panel room substrate.** `phase_outputs["progress_panel"]`
  now persists `panel_id`, canonical `items`, append-only `updates`,
  derived `checkpoints`, and room-backed summary fields surfaced
  through `tangent.session_get.progress_panel`.
- **Timeline/log/checkpoint inspection.** The shipped host includes
  practical timeline, checkpoint, and structured log views with
  browser-local view-state recovery across refresh.
- **Operator controls and recovery.** The host now exposes explicit
  pause/resume/cancel affordances, preserves unsent operator context in
  browser storage, and restores that context on reopen without
  overwriting canonical room state.
- **Summary/export surface.** Progress summaries now carry latest
  checkpoint label and completion-result metadata, and the browser host
  can copy a concise export snapshot for downstream inspection.
- **Progress-panel e2e coverage.** Added
  `docs/manual-tests/progress-panel-e2e.md`.

### Changed

- **`tangent.session_get` now projects progress-panel state.** It
  exposes a dedicated `progress_panel` payload with canonical items,
  append-only updates, derived checkpoints, and concise summary fields
  for agent reasoning.
- **Docs now describe progress panel as the newest bundled workflow.**
  README, MCP integration, and manual docs now reflect the new tool
  surface and the fact that updates still ride the existing Tangent
  room/tool contract.

### Fixed

- **Rejected progress updates no longer corrupt room history.** Invalid
  update payloads return an explicit rejected response shape while
  preserving the last accepted room-backed progress snapshot for reopen.

## [v0.9.0] - 2026-05-09

File picker. Tangent now ships a persistent room-backed file-picker
workflow for explicit local artifact selection, persisted browse/query
state, browser-local draft recovery, accepted selection revisions, and
reusable handoff payloads.

### Added

- **`tangent.file-picker`.** A bundled room-backed file-picker workflow
  with allowed browse roots, explicit submit/cancel, and room reuse via
  `meta.roomID`.
- **File-picker room substrate.** `phase_outputs["file_picker"]` now
  persists `picker_id`, canonical `browse_roots`, durable
  `selected_refs`, normalized `query_state`, append-only
  `selection_revisions`, and accepted submission metadata.
- **Browser-local draft recovery and preview.** File-picker turns keep
  unsent staged selection/query state in browser storage keyed by room +
  picker and recover it on refresh without overwriting canonical room
  state.
- **Artifact handoff metadata.** Accepted file-picker submits now
  persist a stable `handoff` payload plus `submission_summary` so later
  workflows can consume selected artifact refs without reparsing the UI
  state.
- **File-picker e2e coverage.** Added
  `docs/manual-tests/file-picker-e2e.md`.

### Changed

- **`tangent.session_get` now projects file-picker state.** It exposes
  a dedicated `file_picker` payload with canonical roots, durable
  selected refs, normalized query state, accepted selection revisions,
  and the latest handoff summary.
- **Docs now describe file picker as the newest bundled workflow.**
  README, MCP integration, and manual test docs now reflect the new
  tool surface and localhost-only artifact-ref selection model.

### Fixed

- **Invalid file-picker submits no longer create fake accepted room
  state.** Rejected payloads return an explicit non-accepted response
  shape and preserve the last accepted selection snapshot for reopen.

## [v0.6.0] - 2026-05-09

Form collect. Tangent now ships a persistent room-backed generalized
form workflow for schema-driven field collection, conditional sections,
repeatable groups, saved drafts/templates, attachment refs, and durable
submission summaries.

### Added

- **`tangent.form-collect`.** A bundled room-backed structured form
  workflow with canonical agent-provided schema, explicit submit/cancel,
  and room reuse via `meta.roomID`.
- **Form room substrate.** `phase_outputs["form-collect"]` now
  persists `form_id`, canonical `schema`, normalized `answers`,
  `notes`, `updated_at`, room-backed `saved_drafts`, `templates`,
  lightweight `attachment_refs`, and `submission_summary`.
- **Conditional and repeatable sections.** The shipped form host
  supports deterministic show/hide logic plus repeatable groups backed
  by the persisted answer state.
- **Local refresh recovery.** In-progress form state autosaves in the
  browser for the active room + form and recovers across refresh until
  submit/cancel.
- **Form submit summary/export.** Submitted forms now persist a durable
  summary/export payload so agents can reopen the room and inspect the
  last canonical submission without parsing UI-local state.
- **Form-collect e2e coverage.** Added
  `docs/manual-tests/form-collect-e2e.md` and
  `scripts/form-collect-mock-call.mjs`.

### Changed

- **`tangent.session_get` now projects form-collect state.** It exposes
  a dedicated `form_collect` payload with schema, answers, room-backed
  drafts/templates, attachments, and the last submission summary.
- **Docs now describe form collect as the newest bundled workflow.**
  README, MCP integration, architecture, and developer docs now reflect
  the shipped v0.6 tool surface.

### Fixed

- **Refresh no longer cancels active form work by default.** The room
  keeps the pending form alive while the browser restores its local
  unsent draft state.

### Security

- **The trust model is unchanged.** Tangent remains localhost-only and
  single-user. Form collect adds richer persisted room state and browser
  draft recovery, but no auth, TLS, remote sync, or live multi-user
  collaboration.

## [v0.5.0] - 2026-05-09

Spreadsheet review. Tangent now ships a persistent room-backed
spreadsheet-review workflow for dense tabular review, explicit submit,
local refresh recovery, CSV export metadata, and saved query views.

### Added

- **`tangent.spreadsheet-review`.** A bundled room-backed table review
  workflow with canonical agent-provided rows, row selection, bulk
  actions, and explicit submit/cancel.
- **Spreadsheet-review room substrate.** `phase_outputs["spreadsheet-review"]`
  now persists canonical `columns` and `rows`, normalized `query_state`,
  `notes`, selected rows, bulk `action_id`, saved views, and export
  metadata.
- **Saved views and local draft recovery.** Spreadsheet-review turns
  support room-backed named query views plus host-local refresh recovery
  for unsent browser state.
- **CSV export metadata.** Exported CSVs stay browser-local as files,
  while lightweight export refs persist on the room for later agent
  reasoning.
- **Spreadsheet-review e2e coverage.** Added
  `docs/manual-tests/spreadsheet-review-e2e.md` and
  `scripts/spreadsheet-review-mock-call.mjs` for submit -> reopen ->
  cancel verification in one room.

### Changed

- **`tangent.session_get` now projects spreadsheet-review state.** It
  exposes a dedicated `spreadsheet_review` payload with canonical table
  state, normalized query metadata, saved views, selected rows, and
  export refs.
- **Docs now describe spreadsheet review as the current workflow
  expansion surface.** README, MCP integration, architecture, and
  developer docs now reflect the shipped v0.5 workflow set.
- **The roadmap advances past the initial spreadsheet-review expansion.**
  v0.5 is now the current shipped release rather than a future intent
  section.

### Deprecated

_None._

### Removed

_None._

### Fixed

- **Saved-view persistence now reuses the same normalization and
  validation path as full spreadsheet snapshots.** Invalid names and
  stray `query_state` keys are rejected before room state is updated.
- **Spreadsheet-review writes now reject unsupported persisted blob
  versions before overwriting phase state.** This matches the room-state
  guards used by other persisted workflow projections.

### Security

- **The trust model is unchanged.** Tangent remains localhost-only and
  single-user. Spreadsheet review adds richer persisted room state and
  browser draft recovery, but no auth, TLS, remote sync, or live
  multi-user collaboration.

## [v0.4.0] - 2026-05-09

Shared Whiteboard. Tangent now supports a persistent MCP-driven
whiteboard workflow in one room, with explicit submit/reopen turns,
local refresh recovery, PNG export, artifact-backed reference images,
and revision browsing for agent handoff from revision N.

### Added

- **`tangent.whiteboard`.** A bundled tldraw-backed whiteboard workflow
  with room reuse via `meta.roomID`, blank/seeded board load, and a
  full-scene submit contract.
- **Whiteboard room substrate.** `phase_outputs["whiteboard"]` now
  persists canonical scene JSON, lightweight asset refs, export refs,
  notes, updated-at metadata, and append-only revision history.
- **Local draft recovery.** Whiteboard edits autosave in the browser for
  the active room + board and recover after refresh until submit/cancel.
- **PNG export and artifact-backed refs.** Export metadata now persists
  on the room, and reference-image assets survive reload through durable
  artifact-style URIs instead of browser-only state.
- **Revision browser.** The room UI now lists prior whiteboard
  revisions, supports reopening a snapshot for inspection, and supports
  explicit continuation from an older revision with recorded lineage.
- **Whiteboard e2e coverage.** Added
  `docs/manual-tests/whiteboard-e2e.md` and
  `scripts/whiteboard-mock-call.mjs` for submit -> reopen -> continue
  verification in one room.

### Changed

- **`tangent.session_get` now projects whiteboard state.** It exposes
  the latest persisted board plus lightweight revision metadata for
  agent reasoning, while full historical snapshots stay room-local to
  the whiteboard browser UI.
- **Docs now describe Tangent as a multi-workflow surface that includes
  whiteboard collaboration.** README, MCP integration, architecture,
  and developer docs now reflect the shipped v0.4 tool surface and
  single-user localhost scope.
- **The roadmap advances past the planned whiteboard phase.** v0.4 is
  now the current shipped release rather than a future intent section.

### Deprecated

_None._

### Removed

_None._

### Fixed

- **Whiteboard submit normalization now rejects fake persistence on
  invalid payloads.** Invalid board submits do not create bogus
  revisions in room history.
- **Refresh no longer cancels active whiteboard work by default.**
  `beforeunload` preserves local draft recovery rather than treating a
  refresh like an explicit cancel.

### Security

- **The trust model is unchanged.** Tangent remains localhost-only and
  single-user. The whiteboard adds richer persisted local state and
  browser draft recovery, but no auth, TLS, remote sync, or live
  multi-user presence.

## [v0.3.0] - 2026-05-09

Interview Protocol — Writing. Tangent now supports the first full multi-phase
workflow on top of persistent rooms and the session substrate: interview,
synthesis, drafting, revision, and final output in one room, including
explicit jump-back to earlier phases.

### Added

- **Phase-state substrate.** Rooms now persist `current_phase`,
  append-only `phases_visited`, and versioned `phase_outputs`, with MCP
  tools `tangent.session_advance_phase` and
  `tangent.session_set_phase_output`.
- **`tangent.interview_question`.** Long-form question/answer turns with
  optional quick picks, topic/thread metadata, and explicit output-shape
  prompting.
- **`tangent.synthesis_notes`.** Private synthesis state with
  server-enforced hidden/visible projection and optional outline preview.
- **`tangent.block_draft`.** Section-first drafting loop with accept,
  revise, inline-edit, and redirect actions. Accepted blocks now persist
  in order on the room.
- **`tangent.prose_revision`.** Unified review/copy/style workflow with
  explicit per-suggestion accept/reject/comment outcomes.
- **`tangent.output_render`.** Final markdown renderer with copy and
  download affordances, backed by persisted final-output room state.
- **Writing workflow manual recipe.**
  `docs/manual-tests/writing-flow-e2e.md` now covers interview -> output
  in one room, including one jump-back.

### Changed

- **`tangent.session_get` is now the writing-room checkpoint surface.**
  It exposes structured interview history, synthesis projection, accepted
  draft blocks, reconstructed current draft, prose revision outcomes, and
  the final output artifact.
- **Local Tangent skill/command docs now describe the canonical writing
  choreography.** The Tangent Nanite skill/command point at the exact
  phase/tool sequence instead of forcing agents to reconstruct it ad hoc.
- **Architecture and MCP integration docs now describe the v0.3 workflow
  surface.** Docs now reflect sixteen advertised tools and the canonical
  phase progression with explicit jump-back.

### Deprecated

- The old `fast-triage` redirect stub remains legacy-only and should not
  be extended for new workflows.

### Removed

_None._

### Fixed

_None._

### Security

- **The trust model is unchanged.** Tangent remains localhost-only and
  single-user. The writing workflow adds richer persisted state, but no
  auth, TLS, or capability-gated distribution yet.

## [v0.2.0] - 2026-05-08

Persistence, multi-envelope rooms, and three bundled workflows. Tangent now
absorbs the Fast-Triage use case while remaining a single localhost binary
with MCP and browser transports.

### Added

- **SQLite-backed persistence layer.** Rooms and resolved envelope history
  now persist in `~/.tangent/tangent.db` and survive server restart.
- **Multi-envelope-per-room session model.** Added the `tangent.session_*`
  MCP tools: `tangent.session_create`, `tangent.session_advance`,
  `tangent.session_get`, `tangent.session_close`, and
  `tangent.session_list`.
- **`tangent.feedback` workflow.** Bundled structured-form workflow with the
  frontend `<Feedback>` component.
- **`tangent.design-iteration` workflow.** Sandboxed HTML preview with the
  bundled `<DesignIteration>` component and click-event capture.
- **Multi-room tab strip UX.** The SPA can switch active rooms inside one
  browser tab while preserving per-room state.
- **Explicit cancel flow.** Active envelopes expose cancel, and browser
  unload triggers a best-effort cancel back to the agent.
- **`internal/room.ErrUserCancelled` sentinel.** Cancellation handling now
  uses a typed sentinel instead of string matching.

### Changed

- **`tangent.triage` now routes through the session substrate.** It is a
  thin wrapper over `tangent.session_create` plus
  `tangent.session_advance`; the public contract for v0.1 callers is
  preserved.
- **Fast-Triage has been retired in favor of Tangent.** The archived
  predecessor repo is documented in
  `docs/migrating-from-fast-triage.md`, and the local Nanite skill name
  moves from `fast-triage` to `tangent`.

### Deprecated

- The old `fast-triage` skill/command name remains as a redirect stub for
  v0.2 compatibility and is planned for removal in v0.3.

### Removed

- `triage_handler.containsUserCancel` string-match cancel detection,
  replaced by `ErrUserCancelled`.

### Fixed

_None._

### Security

- **Localhost-only bind remains unchanged.** Tangent still assumes a
  single-user trusted machine with no auth, TLS, or capability gating.
- **Persistence is local-user scoped.** The new database lives at
  `~/.tangent/tangent.db`; room state is no longer purely in-memory, but it
  remains a local user-controlled file.

## [v0.1.0] - 2026-05-08

First release. "Prove the shape." Any MCP-speaking agent can launch a
bundled Tangent workflow, the user resolves it in the browser, and the
agent receives a structured response back.

Verified end-to-end against a real Claude Code session
(`claude mcp add --transport http tangent http://localhost:7842/mcp`).

### Added

- **Go HTTP server with embedded Vite SPA.** Single binary on `:7842`,
  override via `TANGENT_HTTP_PORT`. Per-session rooms at
  `http://localhost:7842/r/<roomID>`.
- **`go-envelopes` v0.1.0 integration.** Envelope catalog loaded at
  startup (26 core kinds), TS types code-generated into
  `ui/src/generated/envelope-types.ts`, staleness gated in CI via
  `make check-envelopes`.
- **MCP server (Streamable HTTP + SSE).** Mounted on the same port as
  the SPA at `/mcp` and `/sse`. Two tools advertised:
  `tangent.list_workflows` and `tangent.triage`. Stateless +
  JSONResponse mode for v0.1, so one-shot `tools/list` and
  `tools/call` curl probes work without an `initialize` handshake.
- **WebSocket bridge with per-room state.** Each MCP triage call
  creates a fresh room; the SPA connects over WS and receives the
  envelope; user decisions flow back to the agent through the same
  room. Multi-room concurrency from day one.
- **Bundled `triage` workflow.** Plugin extension registers the
  `tangent.triage` envelope kind; the React `<Triage>` component
  renders prompt + items with Accept / Backlog / Delete decisions and
  Submit / Cancel controls. Submit is gated on every item having a
  decision.
- **Envelope component registry on the FE side.** `envelope-registry.ts`
  + `EnvelopeRouter.tsx` map envelope `type` to component, mirroring the
  Go-side plugin extension pattern. Future kinds register through the
  same surface.
- **Tooling.** `Makefile` for `build` / `dev` / `test` / `lint` /
  `generate-envelopes` / `check-envelopes`. `lefthook` pre-commit /
  pre-push hooks for Go format, vet, lint, and frontend lint /
  type-check / test.
- **Manual test recipes.** `docs/manual-tests/triage-e2e.md` (real
  Claude Code) and `scripts/triage-mock-call.mjs` (Node-driven, no
  LLM) for the same loop.
- **Documentation.** `README`, `docs/architecture.md`,
  `docs/developing.md`, `docs/mcp-integration.md`,
  `docs/mcp-smoketest.md`.

### Deprecated

_None._

### Removed

_None._

### Fixed

_None — first release._

### Security

- **Localhost-only, single-user, no auth.** Tangent listens on
  `localhost` only and assumes a trusted single-user machine. There is
  no authentication, no capability gating, and no isolation between
  rooms beyond room IDs. Distribution / trust model is a v0.6+ topic.
- **Ephemeral state.** Rooms and their envelopes live in memory for
  the lifetime of the server process. No persistence, no recovery
  across restarts.

[Unreleased]: https://github.com/hollis-labs/tangent/compare/v0.12.0...HEAD
[v0.12.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.12.0
[v0.11.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.11.0
[v0.10.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.10.0
[v0.9.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.9.0
[v0.8.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.8.0
[v0.7.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.7.0
[v0.6.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.6.0
[v0.5.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.5.0
[v0.4.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.4.0
[v0.3.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.3.0
[v0.2.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.2.0
[v0.1.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.1.0
