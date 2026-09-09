# Tangent

An agent-summoned, app-sized interactive surface. Tangent is a single localhost binary — a Go server with an embedded SPA, opened in your browser — that any agent — Claude Code, Nanite, Cursor, Codex, Gemini CLI, or anything else that speaks the Envelope UI Protocol — can summon when chat is the wrong shape for the work.

Use cases the chat window can't carry well:

- diff-approval queues
- design-comp triage
- dashboard snapshots
- screenshot annotation
- persistent forms
- brainstorm iteration
- spreadsheet review
- progress tracking

Tangent is the *separate-window app surface* for an interactive collaboration system. Inline modals, fast-triage popovers, and spatial canvases are deliberately handled elsewhere — keeping Tangent's scope to a single shape is intentional. [ADR 0007](./docs/adr/0007-collaboration-surface-plugin-host-and-view-state.md) adds an addressed multi-agent collaboration surface to that shape; the channel pane, the relay journal and the relay tools are shipped, and that ADR stamps each claim about what is and is not.

## Status

The current release is **`v0.13.0`**, the first stable cut for personal use:
the foundation phase, the durable `/hitl` inbox, and the desktop shell as an
unsigned, locally built `Tangent.app`. `v0.12.0` was documented but never
tagged. The release version has one source, `ui/package.json`, and a test that
fails when this file, the CHANGELOG, the Go host version, or the bundle
disagree with it. See [`CHANGELOG.md`](./CHANGELOG.md) for what is and is not
in the release.

Since v0.12.0 Tangent has completed a foundation phase that changed the shape
of the product rather than adding another workflow:

- **Resumable completion.** A room workflow's wait is no longer the work. A
  wait answered within 45 seconds returns inline; past that it returns a
  successful *pending receipt* with a durable handle. Transport loss, caller
  timeout, browser disconnect, and process restart stop only the waiter.
- **Multiple connections per room.** One room accepts many tabs and clients.
  Exactly one holds the resolver lease and can produce a terminal outcome; the
  rest observe. Rooms are a compatibility projection of a durable *surface* —
  they are not agent sessions.
- **A definition registry.** Immutable versioned manifests with retained
  material and pinned replay, so an interaction stays validatable after a
  restart and after the catalog moves on.
- **Scoped authorization.** A room URL is a locator, not a credential. Browser
  authority is a participant session; caller authority is a host-assigned
  `<authority>:<partition>` scope over a seven-capability matrix.
- **Renderer trust classes, CSP, and sandboxing**, plus a host-mediated effect
  broker.
- **Operability.** Separate liveness / readiness / capability health, payload-
  safe correlated telemetry, and backup / restore / repair with six distinct
  deletion kinds.

Fast-Triage has been migrated and archived; if you are moving an existing
setup, see
[`docs/migrating-from-fast-triage.md`](./docs/migrating-from-fast-triage.md).

**Read [Current limitations](#current-limitations) before building on any of
this.** Several capabilities above are declared or proven-by-construction
rather than exercised in production, and the list says which.

Tangent is a Go HTTP server with an embedded Vite SPA. It is served in an
ordinary browser, and since v0.13.0 also in `Tangent.app`, a Wails v3 webview
over the same loopback server (adopt-or-boot; see the Roadmap). The shell is
unsigned, has no tray, and has not been through its acceptance matrix; the
architecture and agent documents are reconciled to its presence by
`CW-20260905-0051`, not yet.

## Quickstart

Install:

```bash
# Latest tagged release (the headless daemon):
go install github.com/hollis-labs/tangent/cmd/tangent@v0.13.0

# Or build from source; `make build-app` additionally assembles Tangent.app on macOS:
git clone git@github.com:hollis-labs/tangent.git && cd tangent && make build
```

Run:

```bash
tangent
# tangent listening addr=:7842
# MCP server ready http_url=http://localhost:7842/mcp sse_url=http://localhost:7842/sse
```

Wire it into Claude Code (verified against `claude` CLI as of 2026-05-08):

```bash
claude mcp add --transport http tangent http://localhost:7842/mcp
```

If your `claude` rejects `--transport http`, fall back to SSE:

```bash
claude mcp add --transport sse tangent http://localhost:7842/sse
```

Then in any Claude Code session: ask Claude to use either one of the bundled workflow tools (`tangent.triage`, `tangent.feedback`, `tangent.form-collect`, `tangent.design-iteration`, `tangent.whiteboard`, `tangent.spreadsheet-review`, `tangent.approval-queue`, `tangent.diff-review`, `tangent.file-picker`, `tangent.progress-panel`, `tangent.dashboard`, `tangent.wizard`) or the writing flow tools (`tangent.session_*`, `tangent.interview_question`, `tangent.synthesis_notes`, `tangent.block_draft`, `tangent.prose_revision`, `tangent.output_render`). Tangent logs a room URL like `http://localhost:7842/r/<roomID>` — open it in a browser, resolve the workflow, and Claude receives the structured response.

For durable asynchronous approvals or persistent-attention items, use the four
`tangent.hitl_*` tools and the operator-owned `/hitl` inbox. The repo-local
[`tangent-hitl-inbox` skill](./.agents/skills/tangent-hitl-inbox/SKILL.md)
provides the handle-first flow and known-good direct Tangent and Tether
native-flat payloads. This is separate from the room-backed
`tangent.approval-queue` batch workflow.

Rooms now persist across server restart in `~/.tangent/tangent.db`, so a
resolved session history survives a process bounce.

For Cursor, Codex, the curl verification, and troubleshooting, see [`docs/mcp-integration.md`](./docs/mcp-integration.md).

For the full shipped behavior, see [`CHANGELOG.md`](./CHANGELOG.md).

## How it works

**Stack.** A Go HTTP server (`net/http`) with a React / TypeScript / Tailwind v4 / shadcn SPA embedded into the binary via `go:embed`, served to an ordinary browser on `:7842`, or to the `Tangent.app` desktop shell, a Wails v3 webview pointed at that same server (v0.13.0). No system tray yet — see [Roadmap](#roadmap).

**One transport today, one envelope schema.**

- **MCP (Streamable HTTP `/mcp` + legacy `/sse`)** — the portable lowest-common-denominator, and the only agent-facing transport that exists. Any MCP-speaking agent can launch Tangent workflows and receive structured responses.
- **Nanite-native side-channel** — *intended, not implemented.* A premium tier that would add mid-turn event injection on top of the same envelope schema when Tangent runs as a managed child of a Nanite session. Nothing in the tree implements it and no release commits to it.

**Rooms are surface projections, not agent sessions.** A room (`/r/<roomID>`) is how the workflow tools project a durable *surface* into a browser; the canonical record is the interaction behind it. Nothing binds a room to one agent, one caller, or one tab. Several callers can hold live rooms at once without bleeding state, and one room accepts many simultaneous connections — exactly one holds the resolver lease and may produce a terminal outcome, the rest observe.

**Authority is a session, not a URL.** Room URLs appear in tool output, transcripts, and browser history and carry no authority. Browser access is an `HttpOnly` participant session; caller access is a host-assigned `<authority>:<partition>` scope over a seven-capability matrix.

**Envelope renderer.** A React component registry maps each envelope `type` to a component. The registry is shared with Nanite rather than re-implemented, so Tangent inherits its existing envelope kinds. Each definition carries a renderer *trust class* that decides where its code runs and what it may ask the host to do; untrusted presentation is drawn in an opaque-origin sandboxed frame. See [`docs/renderer-trust-classes.md`](./docs/renderer-trust-classes.md).

**Persistence.** Configurable per envelope kind (markdown, diffs, screenshots, design comps, etc.). The defaults are config-overridable, and users can flip persistence per instance.

## Roadmap

The phases below are illustrative — they sketch the intended shape of releases, not a contract.

### v0.1 — Prove the shape

Shipped. HTTP server with an embedded SPA, MCP server, one bundled workflow (triage). The goal was end-to-end: any MCP-speaking agent can launch a rich workflow in Tangent and receive a structured response back. (The original sketch said "Wails shell"; v0.1 shipped the localhost binary instead, and the desktop wrap moved to the deferred item below.)

### v0.4 — Shared whiteboard

Shipped. Tangent now includes a persistent, MCP-driven whiteboard workflow for layout, annotation, and spatial collaboration. The v0.4 whiteboard is single-user localhost first, shared between user and agent through one room, backed by tldraw scene JSON, explicit submit, local refresh recovery, PNG export, and revision browsing.

### v0.5 — Spreadsheet review

Shipped. Tangent now includes a persistent, MCP-driven spreadsheet-review workflow for dense row review, canonical query state, saved views, local refresh recovery, CSV export metadata, and explicit submit in one room.

### v0.6 — Generalized form collect

Shipped. Tangent now includes a persistent, MCP-driven schema-first form workflow for rich field collection, conditional sections, repeatable groups, room-backed drafts/templates, attachment refs, and durable submission summaries.

### v0.7 — Approval queue

Shipped. Tangent now includes a room-backed approval queue for serialized review with evidence panes, durable decision history, defer reasons, batch controls, audit export metadata, and explicit submit/reopen turns.

### v0.8 — Diff review

Shipped on the current branch line before file-picker. Tangent added a room-backed diff review workflow with per-file/per-hunk decisions, durable comments, artifact-backed before/after refs, batch review controls, draft recovery, and exportable summaries.

### v0.9 — File picker

Shipped. Tangent added a room-backed file-picker workflow with allowed local browse roots, persisted query state, browser-local draft recovery, accepted selection revision history, and reusable artifact-ref handoff payloads.

### v0.10 — Progress panel

Shipped. Tangent added a room-backed progress workflow with append-only updates, timeline/checkpoint/log inspection, local operator-context recovery, and concise summary/export inspection.

### v0.11 — Dashboard

Shipped. Tangent adds a room-backed dashboard workflow that summarizes Tangent room/workflow state through reusable saved layouts, explicit refresh/update submits, room/artifact drill-down, and deterministic export metadata.

### v0.12 — Wizard

Shipped (untagged). Tangent adds a room-backed guided wizard workflow for bounded step progression, branch selections, explicit partial saves, local draft recovery, and final review/completion.

### Foundation phase — durability, identity, and trust

Shipped (untagged), and not a workflow release. Resumable canonical completion,
multi-connection rooms, an immutable definition registry, scoped authorization,
interaction packages, a host-mediated effect broker, renderer trust classes with
CSP and sandboxing, split liveness/readiness/capability health, correlated
payload-safe telemetry, and database backup/restore/repair. The decisions behind
it are [ADRs 0001–0005](./docs/adr/). What it does *not* yet do is
[Current limitations](#current-limitations).

### v0.13 — Stable cut: desktop shell, launch at login, gateway fix

Shipped, tagged `v0.13.0`, as the first stable install for personal use.
**What shipped:** `Tangent.app`, a Wails v3 webview over the same loopback
server, with adopt-or-boot against a running daemon, persisted window geometry
and always-on-top, a stable client id so a relaunch is a reconnect,
`make build-app` packaging verified by `internal/packagecheck`, a macOS CI job
for the shell; the launch-at-login facility (a user LaunchAgent that starts the
headless daemon, installed by the stable install script, not automatically);
acceptance of gateway trace metadata so the default mux configuration can call
the HITL tools; the dev/stable split (dev-facing targets on 7843 with a
workspace database); and one version source with a drift test.
**What did not:** the system tray, close-to-hide, and single-instance UX
(`CW-20260905-0030`, stable 1.1); code signing and notarization; the
desktop-shell acceptance matrix (`CW-20260905-0050`) and the shell's decision
record (`CW-20260905-0051`); any relay or channel capability, which was still
direction only at that cut and shipped afterwards. The install itself, and
moving dev to 7843, is `CW-20260907-0020`.

### Deferred — distribution and third-party workflows

Installation, capability gating for externally authored definitions, and the
trust model for third-party workflow packages. Renderer trust classes are the
shipped half of this; publisher distribution is not started.

### Not committed — Nanite-native channel

A second transport adding mid-turn event injection over the same envelope
schema. Intended direction only: nothing in the tree implements it, and no
release commits to it.

## Composition

Tangent is built to compose with the rest of the `hollis-labs` Go ecosystem rather than re-implement equivalents.

**Depends on:**

- `hollis-labs/go-envelopes` — Go envelope schemas, manifest, and plugin extension API.
- `ts-envelopes` — the TypeScript counterpart, once it lands.

**Reuses:**

- Nanite's React envelope component registry. Because the registry is shared, Tangent inherits the go-envelopes core catalog for free — approval-card, diff-card, document-viewer, question-form, table-card, and the rest — and registers its own kinds beside them through the plugin-extension API. The exact split is printed by the running binary (`loaded envelope types` / `registered tangent envelope extensions`) and stamped into the generated header of `ui/src/generated/envelope-types.ts`; it is deliberately not written as a number here, because that number has been wrong in this repository more often than right.

**Likely to compose with:**

- `go-plugin` — plugin SDK.
- `go-strutil` — slugify, namespace validation, and similar string utilities.
- `go-runner` — subprocess management if Tangent ever spawns helpers.
- `go-mcp` — MCP server implementation.

Where one of these libraries already covers a need, Tangent should reach for it before writing an equivalent.

## Non-goals

A few things Tangent deliberately is not, to keep scope honest:

- **Not a chat runtime.** That's Nanite. Tangent is the separate-window surface — same envelope schema, complementary host.
- **Not a build-time codegen tool.** That's Sigil. Tangent renders envelopes at runtime through the React registry.
- **No central registry, cloud service, or remote access.** Single-user, localhost only. Tangent *does* now have object-scoped authorization (participant sessions and a caller capability matrix), but loopback admission is not authentication — see [Current limitations](#current-limitations).

## Development

Prerequisites:

- Go 1.26.1 (matches `go-envelopes`)
- Node 22.12.0 (`mise.toml` pins the repo toolchain)
- `lefthook` (`brew install lefthook`) for the pre-commit / pre-push hooks

```bash
# One-time
mise install
make install-hooks
cd ui && npm install && cd ..

# Build (frontend → embedded into Go binary)
make build           # produces ./tangent

# Dev (Go server proxies non-API requests to Vite at :5173)
make dev

# Lint + test
make lint
make test            # go test -race + vitest

# Run the binary
./tangent            # serves on :7842 (override via TANGENT_HTTP_PORT)
```

Database operations (one at a time; each exits before the server would start):

```bash
./tangent --db-check                              # integrity, schema, guards, storage, ownership
./tangent --db-backup ~/backups/tangent.db        # consistent while the service runs
./tangent --db-backup ~/backups/tangent.db --db-drain   # checkpoints first; requires exclusive access
./tangent --db-restore ~/backups/tangent.db --confirm
./tangent --db-repair                             # recreate missing immutability guards
./tangent --retention-plan                        # what is past its retention window
./tangent --erase-interaction <id> --dry-run      # see what an erasure would remove
```

Anything that removes content or replaces the database requires `--confirm`,
and refuses while a Tangent is serving. See
[`docs/database-operations.md`](./docs/database-operations.md).

## Current limitations

Tangent's foundation phase shipped a lot of mechanism. Some of it is enforced,
some is declared, and some is proven by construction rather than observed. The
canonical list with full context is
[`docs/architecture.md`](./docs/architecture.md#current-limitations); these are
the ones that will mislead you if you skip them.

- **Localhost only, single-user.** Object access is scoped (ADR 0004), but
  loopback admission is not authentication: a hostile local process running as
  the same user can still mint a participant session.
- **`standalone-local` caller partitions are advisory, not a security
  boundary.** Any local caller can assert any partition; nothing verifies it.
  Enforcement exists only *across* authorities, where the prefix is
  host-assigned. **Do not present a partition as isolation downstream.**
- **No definition declares a host-mediated effect capability**, so every
  request through the effect broker refuses `effect_capability_undeclared`.
  That is the designed posture, not a fault — but it means the broker has zero
  production traffic (`CW-20260905-0010`).
- **`clipboard.write` and `export.download` are enforced only inside a
  sandboxed frame.** On the main origin they remain declared-not-enforced; no
  CSP directive covers either. `network.fetch` *is* genuinely enforced, by the
  document CSP's `connect-src`.
- **There is no browser in CI.** The CSP and the sandbox are proven by
  construction and by unit tests over the emitted policy, not by observing a
  real browser refuse anything.
  [`docs/manual-tests/renderer-sandbox-e2e.md`](./docs/manual-tests/renderer-sandbox-e2e.md)
  is the actual verification, and it is manual (`CW-20260904-0171`).
- **The OpenTelemetry bridge has never been observed against a collector.**
  It is API-only, off by default, and gated behind `TANGENT_OTEL`
  (`CW-20260905-0011`).
- **`SaveDraft` has no production caller.** Browser `localStorage` is the only
  draft custody actually running, which means drafts are per-browser and are
  not covered by the durable retention model (`CW-20260905-0001`).
- **`renderer.entry` loads nothing.** A manifest's renderer entry is recorded
  and digested but never used to load code; `ui/src/main.tsx` registers
  renderers by string literal (`CW-20260905-0004`).
- **The ADR 0002 §3 custody-precedence engine is not implemented.** Retention
  uses host windows only (`CW-20260905-0008`).
- **One active pending envelope per room.** History persists, but a room holds
  one envelope awaiting submission at a time; an overlapping advance is
  refused with `SESSION_BUSY` rather than queued.

Open work is tracked in Torque under project `PRJ-20260825-0002`. Anything in
this README that is not shipped is labelled *deferred* or *not committed*
above.

More docs:

- [`docs/architecture.md`](./docs/architecture.md) — system shape, layers, and the canonical limitations list
- [`docs/adr/`](./docs/adr/) — the accepted decision records (0001 lifecycle boundaries, 0002 retention and draft custody, 0003 definition and package ownership, 0004 access authority, 0005 product boundary, 0007 collaboration surface, plugin host and view state, superseding 0006 and superseding 0005 in part)
- [`docs/database-operations.md`](./docs/database-operations.md) — single-writer ownership, backup and restore modes, repair, and the six deletion kinds
- [`docs/host-mediated-capabilities.md`](./docs/host-mediated-capabilities.md) — the two capability namespaces, scoped handles, and what is enforced versus declared
- [`docs/developing.md`](./docs/developing.md) — contributor onboarding and toolchain setup
- [`docs/installing.md`](./docs/installing.md) — install and upgrade the stable Tangent on macOS (daemon under launchd, Tangent.app, one database), and the two-instance layout
- [`docs/launch-at-login.md`](./docs/launch-at-login.md) — the macOS LaunchAgent that starts the headless daemon at login (the facility; not installed on the dev machine)
- [`docs/mcp-integration.md`](./docs/mcp-integration.md) — Claude Code / Cursor / curl recipes
- [`docs/manual-tests/workflow-smoke-tests.md`](./docs/manual-tests/workflow-smoke-tests.md) — quick smoke pass across every shipped workflow
- [`docs/manual-tests/triage-e2e.md`](./docs/manual-tests/triage-e2e.md) — full e2e recipe
- [`docs/manual-tests/feedback-e2e.md`](./docs/manual-tests/feedback-e2e.md) — full feedback workflow
- [`docs/manual-tests/design-iteration-e2e.md`](./docs/manual-tests/design-iteration-e2e.md) — full design-iteration workflow
- [`docs/manual-tests/writing-flow-e2e.md`](./docs/manual-tests/writing-flow-e2e.md) — full Interview Protocol writing workflow
- [`docs/manual-tests/whiteboard-e2e.md`](./docs/manual-tests/whiteboard-e2e.md) — full whiteboard workflow with autosave, export, and revision browser
- [`docs/manual-tests/spreadsheet-review-e2e.md`](./docs/manual-tests/spreadsheet-review-e2e.md) — full spreadsheet-review workflow with recovery, saved views, and CSV export metadata
- [`docs/manual-tests/form-collect-e2e.md`](./docs/manual-tests/form-collect-e2e.md) — full form-collect workflow with conditional sections, recovery, and attachment refs
- [`docs/manual-tests/approval-queue-e2e.md`](./docs/manual-tests/approval-queue-e2e.md) — full approval-queue workflow with reopen, defer reasons, and audit export metadata
- [`docs/manual-tests/hitl-inbox-e2e.md`](./docs/manual-tests/hitl-inbox-e2e.md) — durable HITL inbox with concurrent callers, evidence, reconnect, restart, and later handle retrieval
- [`docs/manual-tests/diff-review-e2e.md`](./docs/manual-tests/diff-review-e2e.md) — full diff-review workflow with reopen, batch decisions, and summary export
- [`docs/manual-tests/file-picker-e2e.md`](./docs/manual-tests/file-picker-e2e.md) — full file-picker workflow with reopen, local draft recovery, and artifact-ref handoff inspection
- [`docs/manual-tests/progress-panel-e2e.md`](./docs/manual-tests/progress-panel-e2e.md) — full progress-panel workflow with update/reopen, checkpoint inspection, and export snapshot verification
- [`docs/manual-tests/dashboard-e2e.md`](./docs/manual-tests/dashboard-e2e.md) — full dashboard workflow with saved layouts, drill-down, and export/share snapshot verification
- [`docs/manual-tests/wizard-e2e.md`](./docs/manual-tests/wizard-e2e.md) — full wizard workflow with partial saves, recovery, and final completion
- [`docs/manual-tests/room-workflow-completion-e2e.md`](./docs/manual-tests/room-workflow-completion-e2e.md) — resumable completion: pending receipts, reconnect, restart, and recovery
- [`docs/manual-tests/renderer-sandbox-e2e.md`](./docs/manual-tests/renderer-sandbox-e2e.md) — **the only real verification of CSP and renderer sandboxing**; there is no browser in CI
- [`docs/manual-tests/multi-agent-e2e.md`](./docs/manual-tests/multi-agent-e2e.md) — concurrent callers across independent rooms
- [`docs/manual-tests/multi-envelope-session-e2e.md`](./docs/manual-tests/multi-envelope-session-e2e.md) — many envelopes across one room's lifetime

## License

MIT — see [LICENSE](./LICENSE).
