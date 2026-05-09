# Changelog

All notable changes to Tangent are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

_Post-v0.4 work lands here. See `README.md` Roadmap for the next phase._

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

[Unreleased]: https://github.com/hollis-labs/tangent/compare/v0.4.0...HEAD
[v0.4.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.4.0
[v0.3.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.3.0
[v0.2.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.2.0
[v0.1.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.1.0
