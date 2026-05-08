# Developing Tangent

Onboarding for contributors and future-you. For the system shape see
[`architecture.md`](./architecture.md). For the user-facing MCP setup
see [`mcp-integration.md`](./mcp-integration.md).

## Prerequisites

- **Go 1.26.1** (matches `go-envelopes`; see `go.mod`).
- **Node 22.12.0**. The repo pins this via [`mise.toml`](../mise.toml);
  run `mise install` after cloning.
- **`lefthook`** for the pre-commit / pre-push hooks:
  `brew install lefthook`.
- Optional: `golangci-lint` (lint target skips it cleanly if missing),
  `jq` (for the curl probes in `mcp-smoketest.md`).

## First-time setup

```bash
git clone git@github.com:hollis-labs/tangent.git
cd tangent
mise install
make install-hooks      # lefthook install — sets up pre-commit/pre-push
cd ui && npm install && cd ..
```

## Dev loop

```bash
make dev
```

Runs Vite (`:5173`) and the Go server (`:7842`) in parallel. The Go
server proxies non-API requests to Vite, so HMR works as if the SPA
were standalone, and the same routes you'll hit in production are
reachable on `:7842` during dev.

For a production-shaped run (no Vite proxy, embedded build):

```bash
make build              # frontend build → embedded into Go binary → ./tangent
./tangent
```

## Tests

```bash
make test               # Go (-race) + vitest
make test-go            # Go only
make test-frontend      # vitest only
```

Notable suites:

- `internal/server/integration_test.go::TestIntegration_TriageRoundTrip`
  — in-process e2e using the MCP SDK's in-memory transports. Canonical
  proof the MCP → WS → response loop closes.
- `ui/src/components/envelopes/Triage.test.tsx` — frontend behavior for
  the bundled triage component.
- `scripts/triage-mock-call.mjs` — Node-from-the-outside variant. Not
  in CI; used to verify the wire shape with no LLM.

## Lint

```bash
make lint               # Go (gofmt + vet + golangci-lint) + frontend (biome)
make lint-go
make lint-frontend
```

## Conventions

- **Branches.** `feat/...`, `fix/...`, `docs/...`, `chore/...`. The
  v0.1 cycle used `feat/v01-NN-<slug>` per PR; future cycles can drop
  the prefix or keep it as taste dictates.
- **Commits.** Conventional-commits-ish. Subject prefix matches the
  area (`feat(mcp): ...`, `fix(ws): ...`, `docs: ...`, `chore(ci): ...`).
  Keep subject ≤ 72 chars; body explains the *why*.
- **PRs.** One logical change per PR. Test plan in the description.
  Review fixes go in their own commit (`fix(prN): address Copilot review
  feedback` is the v0.1 pattern).

## Architecture pointer

For the system layers (HTTP, MCP, WS bridge, envelope dispatcher,
go-envelopes registry) and the Wails-deferral note, see
[`architecture.md`](./architecture.md).

## Known limitations (v0.1)

See [`CHANGELOG.md`](../CHANGELOG.md) Security section. Headlines:

- Localhost only, single-user, no auth.
- Ephemeral state — restarts drop all rooms.
- Two tools advertised (`tangent.list_workflows`, `tangent.triage`).

## Adding a new envelope kind

The v0.1 reference shape — use `triage` as the precedent on both sides.

**Go (registration + handler):**

1. Define the envelope type in `internal/envelope/extensions/<kind>.go`,
   following `triage.go`. Register it via the plugin extension API
   alongside `triage` so `go-envelopes` validates it.
2. If the kind drives an MCP tool, add the tool wiring under
   `internal/mcp/` — see `triage_handler.go` and `triage_schema.go`
   for the hand-rolled JSON Schema pattern. The handler creates a room,
   sends the envelope through the WS bridge, and returns the user's
   response as the MCP tool result.

**Frontend (rendering):**

3. Add the React component at
   `ui/src/components/envelopes/<Kind>.tsx`. Match the `<Triage>`
   shape: take a typed envelope prop, render the UI, call the
   submit/cancel callbacks the registry provides.
4. Register the component in `ui/src/lib/envelope-registry.ts` so
   `EnvelopeRouter.tsx` knows to render your component when an envelope
   of that `type` arrives. The registry mirrors the Go-side plugin
   extension pattern.
5. Codegen TS types: `make generate-envelopes`. Commit the regenerated
   `ui/src/generated/envelope-types.ts`. CI fails the build if it's
   stale (`make check-envelopes`).

## Adding a new workflow

A workflow is one envelope kind plus an MCP tool that creates a room
and dispatches it. Follow the steps above; `tangent.triage` is the
worked example. v0.2 will likely formalize this into a more declarative
shape; for now the pattern is "follow triage."
