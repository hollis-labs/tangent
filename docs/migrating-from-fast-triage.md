# Migrating From Fast-Triage

Fast-Triage proved that an MCP-driven browser surface was a useful shape.
Tangent is the successor: same basic envelope-driven UX, but generalized
into a persistent multi-room host with more than one bundled workflow.

If you have an existing Fast-Triage setup, this guide is the shortest path
to move it onto Tangent v0.2.

## Why We Did This

Fast-Triage was intentionally narrow: feedback collection and triage in a
single ephemeral browser flow. Tangent keeps that working shape, then adds:

- bundled `triage`, `feedback`, and `design-iteration` workflows
- persistent rooms that survive server restart
- multi-envelope room history
- multi-room session management and a tab strip
- one localhost app surface instead of a one-off prototype repo

The migration stance is simple: Fast-Triage is end-of-life. New work should
move to Tangent rather than expecting further Fast-Triage fixes.

## MCP Config Translation

If you previously registered Fast-Triage in Claude Code, the old shape was:

```bash
claude mcp add fast-triage --transport http http://localhost:5177/mcp
```

Replace it with Tangent:

```bash
claude mcp add --transport http tangent http://localhost:7842/mcp
```

If your client still prefers SSE, Tangent also serves:

```bash
claude mcp add --transport sse tangent http://localhost:7842/sse
```

Tangent now issues room URLs on `http://127.0.0.1:7842/r/<roomID>` when a
workflow starts.

## Tool Name Mapping

| Fast-Triage | Tangent |
| --- | --- |
| `fast_triage.triage` | `tangent.triage` |
| `fast_triage.feedback` | `tangent.feedback` |
| _(none)_ | `tangent.design-iteration` |
| _(none)_ | `tangent.session_create` |
| _(none)_ | `tangent.session_advance` |
| _(none)_ | `tangent.session_get` |
| _(none)_ | `tangent.session_list` |
| _(none)_ | `tangent.session_close` |

The compatibility promise for v0.2 is at the workflow level, not the old
Fast-Triage server surface. Update your MCP config and tool calls.

## Behavioral Diffs

- Tangent persists rooms in SQLite and restores them after restart;
  Fast-Triage was ephemeral.
- Tangent supports multiple envelopes per room; Fast-Triage was effectively
  one-shot.
- Tangent owns and returns the room URL for each call; Fast-Triage generated
  its own browser session flow.
- Tangent's SPA has a tab strip for switching active rooms; Fast-Triage
  assumed a single active tab.
- Tangent rejects overlapping work on the same room with `SESSION_BUSY`
  instead of queueing it.

## Skill Upgrade

If you have the old Nanite skill installed, it now upgrades to `tangent`.
For v0.2, the `fast-triage` skill name can remain as a thin redirect stub so
existing slash-command habits keep working while users migrate. The actual
skill body should point at Tangent's tools:

- `tangent.triage`
- `tangent.feedback`
- `tangent.design-iteration`
- `tangent.session_*`

The canonical room URL form is `http://127.0.0.1:7842/r/<roomID>`.

## What Didn't Change

Most of the user-facing workflow shape stays the same:

- triage still returns structured `decisions`
- feedback still returns structured `answers`
- submit / cancel semantics remain explicit
- suggestions still exist to speed up user choice
- the browser UI is still the right place for multi-step structured input

Some field names differ between Tangent workflows and the original
Fast-Triage prototype. If you were relying on prototype-only envelope quirks,
update to Tangent's bundled tool contracts rather than expecting wire-level
compatibility with the archived repo.
