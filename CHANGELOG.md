# Changelog

All notable changes to Tangent are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

_v0.2-bound work lands here. See `README.md` Roadmap for the planned
shape: bundled `feedback` workflow, multi-tab per-session rooms, cancel
handling, and verified multi-agent concurrency._

### Changed

- Fast-Triage is now the archived predecessor to Tangent. Added a migration
  guide at `docs/migrating-from-fast-triage.md`, covering MCP config updates,
  tool-name mapping, and the v0.2 behavior differences.

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

[Unreleased]: https://github.com/hollis-labs/tangent/compare/v0.1.0...HEAD
[v0.1.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.1.0
