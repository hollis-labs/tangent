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
- `scripts/whiteboard-mock-call.mjs` — end-to-end whiteboard submit ->
  reopen -> continue verification, including revision lineage.

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

## Known limitations (v0.4)

See [`CHANGELOG.md`](../CHANGELOG.md) Security section. Headlines:

- Localhost only, single-user, no auth.
- One active pending envelope per room.
- Seventeen tools advertised; bundled workflows now include the full
  writing path (`interview_question`, `synthesis_notes`, `block_draft`,
  `prose_revision`, `output_render`) alongside `triage`, `feedback`,
  `design-iteration`, and `whiteboard`.

## Persistence layer

Local state now lives in SQLite at `~/.tangent/tangent.db`
(`TANGENT_DB_PATH` overrides).

Useful commands:

```bash
make db-migrate
make db-rollback
sqlite3 ~/.tangent/tangent.db
```

When you add a migration, create matching files in
`internal/db/migrations/`:

- `000N_name.up.sql`
- `000N_name.down.sql`

Migrations are embedded into the Go binary, so every schema change must
land with both directions present.

## Adding a new envelope kind

The current precedents are `triage`, `feedback`, `design-iteration`,
`whiteboard`, `interview-question`, `synthesis-notes`, `block-draft`,
`prose-revision`, and `output-render`. Use at least one simple workflow,
the whiteboard's long-lived room surface, and one multi-phase writing
workflow as references instead of assuming one shape fits every kind.

**Go (registration + handler):**

1. Define the envelope type in `internal/envelope/extensions/<kind>.go`,
   following `triage.go`. Register it via the plugin extension API
   alongside `triage` so `go-envelopes` validates it.
2. If the kind drives an MCP tool, add the tool wiring under
   `internal/mcp/` — see `triage_handler.go` and `triage_schema.go`
   for the hand-rolled JSON Schema pattern. The handler either creates a
   room directly or routes through the session substrate, sends the
   envelope through the WS bridge, and returns the user's response as
   the MCP tool result.

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
6. At three extension kinds, hand-registration is still fine. Once a
   fourth or fifth Tangent-specific kind lands, revisit
   `followups.tangent.v01.dump_types_includes_extensions` and consider
   codegen-driven auto-registration.

## Adding a new workflow

A workflow is one envelope kind plus an MCP tool that creates a room
and dispatches it. Follow the steps above; `tangent.triage`,
`tangent.feedback`, `tangent.design-iteration`, `tangent.whiteboard`,
and the writing workflow kinds are the worked examples. If the workflow
is multi-step,
prefer reusing the `tangent.session_*` substrate and the room phase
state rather than inventing a parallel room lifecycle.

## Migration note

If you are coming from the archived Fast-Triage repo or skill, start
with [`migrating-from-fast-triage.md`](./migrating-from-fast-triage.md).
