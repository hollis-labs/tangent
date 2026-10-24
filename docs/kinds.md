# Tangent kinds

What Tangent can show an operator and what comes back. This is the catalog as the
running host reports it (`tangent_definition_list`: 22 kinds, host v0.20.0 plus the
artifact work), with the MCP tool that opens each one, the agent skill that teaches it,
and where it is tested by hand. Counts drift; the host is the authority, this page is
the map.

A **kind** is an envelope definition: a request schema, a renderer and a response shape.
Most kinds are *room workflows*: the agent calls one tool, Tangent opens a room in the
browser, and the call waits for the operator's explicit submit or cancel (or returns a
durable handle if it is not answered in time, see `docs/room-workflow-completion.md`). A
few kinds are *inbox items*: they land in the operator's Inbox and wait there.

## Room workflows

| Kind | Tool | Use it to | Comes back as | Skill |
|---|---|---|---|---|
| `tangent.triage` | `tangent_triage` | Ask the operator to accept, reject or annotate an item | `data` | `tangent-triage` |
| `tangent.feedback` | `tangent_feedback` | Ask a short structured questionnaire | `data` (answers) | `tangent-feedback` |
| `tangent.form-collect` | `tangent_form_collect` | Collect structured input with a schema-driven form (drafts, templates, attachments) | `data` | `tangent-form-collect` |
| `tangent.interview-question` | `tangent_interview_question` | Ask one open question and get a long-form answer | `data` | `tangent-writing-flow` |
| `tangent.wizard` | `tangent_wizard` | Walk the operator through steps with branches and partial or final completion | `data` | `tangent-wizard` |
| `tangent.design-iteration` | `tangent_design_iteration` | Show sandboxed HTML iterations and take the operator's chosen action on each | `data` | `tangent-design-iteration` |
| `tangent.dashboard` | `tangent_dashboard` | Show tiles, layouts and query state with explicit refresh or update | `data` | `tangent-dashboard` |
| `tangent.progress-panel` | `tangent_progress_panel` | Show long-running work: items, summary, checkpoints, operator updates | `data` | `tangent-progress-panel` |
| `tangent.file-picker` | `tangent_file_picker` | Let the operator pick files from allowed roots; returns artifact refs | `data` | `tangent-file-picker` |
| `tangent.diff-review` | `tangent_diff_review` | Review a diff by file and hunk with comments and an exportable summary | `data` | `tangent-diff-review` |
| `tangent.spreadsheet-review` | `tangent_spreadsheet_review` | Review a dense table: filter, select rows, add notes or row actions | `data` | `tangent-spreadsheet-review` |
| `tangent.approval-queue` | `tangent_approval_queue` | Approve or reject a queue of items with evidence and an audit export | `data` | `tangent-approval-queue` |
| `tangent.external-review` | no `tangent_*` tool of its own (served by a plugin through the dispatcher) | Review an external resource from a retained snapshot, with plugin-served actions; the plugin owns the application and reports each outcome | `data` | none |
| `tangent.app-board` | `tangent_app_board` | Show a board of cards you supply, in columns with a filter bar and detail pane; you own the records and apply every consequence | `data` | none |

### Writing flow

A five-kind workflow that shares one room and phases: `interview-question` to gather,
`block-draft` to accept or reject draft blocks, `prose-revision` for per-suggestion
accept, reject and comment, `synthesis-notes` for private notes with a phase-gated
preview, and `output-render` for the final Markdown with copy and export. One skill,
`tangent-writing-flow`, covers all five. Tools: `tangent_interview_question`,
`tangent_block_draft`, `tangent_prose_revision`, `tangent_synthesis_notes`,
`tangent_output_render`; `tangent_session_phase_advance` and
`tangent_session_phase_output_set` move the phases.

## Inbox items

These do not open a room. They land in the Inbox and wait for the operator.

| Kind | Tool | Use it to | Skill |
|---|---|---|---|
| `tangent.artifact-item` | `tangent_artifact_create`, `_update`, `_get`, `_message`, `_await`, `_withdraw` | Show a mock, diagram, image, PDF or chart with revisions, a reply thread and optional sign-off | `tangent-artifacts` |
| `tangent.hitl-item` | `tangent_hitl_enqueue`, `_get`, `_await`, `_withdraw` | Ask for one durable approval or persistent-attention request | `tangent-hitl-inbox` |
| `tangent.doc-item` | `tangent_docs_enqueue` | Leave a document to read at the operator's pace, with an optional acknowledgment | none |
| `tangent.agent-turn` | `tangent_turns_enqueue`, `_await`, `_ack` | Deliver an agent turn (question, approval, checkpoint, failure, result) | none (relay skill covers a different tool family) |

### Artifact types

`tangent_artifact_create` takes a `type`. Content formats and limits are in the tool's
`type` description; the longer guide is `.agents/skills/tangent-artifacts/SKILL.md`.

| Type | Content | Renders |
|---|---|---|
| `html` | One self-contained HTML document | Sandboxed iframe, no network, https CDN scripts allowed |
| `markdown` | Markdown text | In the viewer |
| `image` | PNG, JPEG, GIF or WebP as base64; SVG as text | `<img>` |
| `excalidraw` | JSON `{"elements": [...]}` | Excalidraw |
| `flow` | JSON nodes and edges (at most 500 and 2000), `LR` or `TB` | React Flow, laid out with dagre |
| `mermaid` | Mermaid source | Mermaid, strict security level |
| `d2` | D2 source, no imports | Drawn on the server, shown as an image |
| `vega-lite` | A spec with inline data | Vega-Lite, no URL loading |
| `pdf` | Base64 | The browser's PDF viewer |
| `video` | mp4 or webm as base64 | `<video>` with range requests |
| `figma` | A figma.com URL | Embed |

Limits: 5 MiB for text types, 25 MiB for binary types. Files are kept on disk until the
operator deletes the item; retention and TTL are not decided yet (CW-20261024-0076).
Design: `docs/adr/0015-artifact-inbox-kind.md`.

## Generic tool families

These work across kinds rather than opening one.

| Family | Tools | For |
|---|---|---|
| Inbox read | `tangent_inbox_list`, `_search`, `_get` | Find items you or others sent |
| Interactions | `tangent_interaction_get`, `_await`, `_ack`, `_cancel`, `_submit`, `_supersede`, `_kind_list`, `_definition_resolve` | Recover a result or act on any durable interaction; the shared completion contract |
| Sessions and rooms | `tangent_session_create`, `_get`, `_list`, `_advance`, `_close`, `_phase_advance`, `_phase_output_set` | Room state and phases |
| Surfaces | `tangent_surface_open`, `_get`, `_close` | Named inbox surfaces |
| Relay | `tangent_relay_channel_open`, `_attach`, `_detach`, `_send`, `_receive`, `_ack`, `_capabilities_get` | Agent-to-agent and operator channels (`tangent-relay-inbox`) |
| UI control | `tangent_ui_navigate`, `_drawer_open`, `_drawer_close`, `_modal_open`, `_modal_close`, `_item_focus`, `tangent_view_get` | Move the operator's browser |
| Diagnostics | `tangent_health_get`, `tangent_definition_list`, `_get`, `_diagnostics_get`, `tangent_telemetry_query`, `tangent_retention_get`, `tangent_workflow_list` | Is the host healthy, which kinds does it serve, and why not |

## Trust

Every kind but one renders in the main origin from reviewed React components
(`renderer_isolation: main-origin`). `tangent.design-iteration` runs agent-authored
markup, so it renders in a sandboxed frame; HTML artifacts do the same through their own
sandboxed content route. See `docs/renderer-trust-classes.md` and ADR 0009.

## Removed

`tangent.whiteboard` and its `tangent_whiteboard` tool were removed with the `tldraw`
dependency (see the ADR 0015 addendum). Excalidraw and artifact items replace it.

## Skill coverage

Skills live in `.agents/skills/`. Kinds and families without one: `tangent.external-review`,
`tangent.app-board`, `tangent.doc-item` (`tangent_docs_enqueue`), `tangent.agent-turn`
(`tangent_turns_*`), inbox read and interaction tools, session and surface tools, UI
control, and diagnostics. The writing flow is one skill for five kinds.
