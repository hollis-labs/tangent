# Tangent — agent context

## Project conventions

- Module: `github.com/hollis-labs/tangent`. Go 1.26.1.
- Two-root contract: code lives here; session tracking, plans, and
  handoff notes go in `~/dev/agent-os/workspaces/execution/tangent/`,
  and drafts awaiting review go in
  `~/dev/agent-os/workspaces/drafts/tangent/`.
  Don't write tracking artifacts inside this repo.
  (The older `~/Projects-apps/agent-workspaces/execution/tangent/` path
  never existed on this machine; do not restore it.)
- License: MIT. Don't add license headers per file — the top-level
  `LICENSE` is sufficient.
- HTTP layer is intentionally separated from app logic so a Wails
  wrapper is mechanical to add later. Wails is a future migration, not a
  current dependency — nothing in the tree is Wails today. Don't import
  `cmd/tangent` from `internal/...`; the dependency is one-way.
- No Nanite-style plugin sandbox / importmap / shared-chunk pattern in
  v0.x. Adding one needs an explicit decision, not drift. Renderer trust
  classes and presentation sandboxing (`docs/renderer-trust-classes.md`)
  are the shipped isolation model; they are not a plugin system.
- Never write a tool, envelope-kind, or workflow count into prose without
  a test that fails when it drifts. `make smoke` derives the surface from
  the shipped build; `internal/smoke/docs_test.go` holds the doc gate.

## nanite

<!-- Verified: this repo carries no `.nanite/` directory, and none exists
     at any workspace root.
     If a project-level Nanite config is added later, mirror the block
     used in ~/.claude/CLAUDE.md and Nanite's own `.claude/CLAUDE.md`:

     - If `.nanite/boot-prompt.md` exists, read it first.
     - If the user says "Boot <agent>", look up the agent in
       `.nanite/config.yaml` and load roles + skills + per-agent context.
     - After context compaction, re-read the active role and project
       context files.
-->

## Sub-agent output

Sub-agent output stays in the sub-agent. Main context gets one-line
confirmations. Do not stream sub-agent transcripts back into the main
conversation.

## Output style

- Focused, minimal output. No trailing summaries unless requested.
- Stop and ask when uncertain. Don't guess at intent.
