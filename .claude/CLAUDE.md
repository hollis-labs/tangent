# Tangent — agent context

## Project conventions

- Module: `github.com/hollis-labs/tangent`. Go 1.26.1.
- Two-root contract: code lives here; session tracking, plans, BLGs,
  and handoff notes go in `~/Projects-apps/agent-workspaces/execution/tangent/...`.
  Don't write tracking artifacts inside this repo.
- License: MIT. Don't add license headers per file — the top-level
  `LICENSE` is sufficient.
- HTTP layer is intentionally separated from app logic so a Wails
  wrapper is mechanical to add later. Don't import `cmd/tangent` from
  `internal/...`; the dependency is one-way.
- No Nanite-style plugin sandbox / importmap / shared-chunk pattern in
  v0.x. Adding one needs an explicit decision, not drift.

## nanite

<!-- TODO: this repo does not (yet) carry a `.nanite/` directory.
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
