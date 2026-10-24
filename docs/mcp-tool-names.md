# MCP tool names

Every MCP tool Tangent serves is named `tangent_<noun>_<verb>` and matches
`^[a-z][a-z0-9_]*$`. Tool names no longer contain dots or hyphens.

## Why

OpenAI-style function-calling clients (Codex among them) accept only
`[A-Za-z0-9_-]` in a function name, so a tool called `tangent.docs_enqueue` was
uncallable from them, and the tether MCP gateway lints every upstream name
against `[a-z0-9_]` and deliberately never normalizes one. The names are fixed
at the source.

## The scheme

- **Prefix.** The `tangent_` prefix is kept on the Tangent side. The gateway adds
  its own origin prefix only when a catalog entry declares `tool_prefix`; with
  none declared, agents see exactly `tangent_<noun>_<verb>`, e.g.
  `tangent_docs_enqueue` and `tangent_inbox_list`. Declaring `tool_prefix:
  tangent` for this server would double it (`tangent_tangent_...`).
- **Noun first.** The noun is the thing acted on: `session`, `interaction`,
  `surface`, `hitl`, `docs`, `turns`, `relay`, `inbox`, `definition`, `ui`,
  `workflow`, `health`, `telemetry`, `retention`, `view`. For the three inboxes
  the noun is the inbox itself and is plural, as the inbox is (`docs`, `turns`),
  so `tangent_turns_enqueue`, `tangent_turns_await` and `tangent_turns_ack` share
  one noun.
- **Verb from a small vocabulary.** `get`, `list`, `search`, `query`, `enqueue`,
  `submit`, `await`, `ack`, `cancel`, `withdraw`, `supersede`, `resolve`,
  `open`, `close`, `send`, `receive`, `attach`, `detach`, `advance`, `set`.
  `acknowledge` is `ack` everywhere.
- **No verb-first names.** `list_workflows` is `tangent_workflow_list`,
  `health_report` is `tangent_health_get`, and `ui_open_modal` is
  `tangent_ui_modal_open`.
- **Interactive surfaces keep their recognizable names**, underscored:
  `tangent_app_board`, `tangent_approval_queue`, `tangent_design_iteration`,
  `tangent_diff_review`, `tangent_file_picker`, `tangent_form_collect`,
  `tangent_progress_panel`, `tangent_spreadsheet_review`, `tangent_triage`,
  `tangent_feedback`, `tangent_dashboard`,
  `tangent_wizard`. Calling one is the act of opening it.

Only names changed. Arguments, results, annotations and behavior are the same.
The envelope **kinds** these surfaces carry (`tangent.app-board`,
`tangent.triage`, ...), definition ids, package ids and the persisted data are
not tool names and did not change: an envelope's `type` is still
`tangent.triage`, and the tool that carries it is `tangent_triage`.

## Old to new

| Old name (deprecated alias) | New name |
|---|---|
| `tangent.app-board` | `tangent_app_board` (spelling only) |
| `tangent.approval-queue` | `tangent_approval_queue` (spelling only) |
| `tangent.block_draft` | `tangent_block_draft` (spelling only) |
| `tangent.dashboard` | `tangent_dashboard` (spelling only) |
| `tangent.definition_registry_diagnostics` | `tangent_definition_diagnostics_get` |
| `tangent.definition_get` | `tangent_definition_get` (spelling only) |
| `tangent.definition_registry_list` | `tangent_definition_list` |
| `tangent.design-iteration` | `tangent_design_iteration` (spelling only) |
| `tangent.diff-review` | `tangent_diff_review` (spelling only) |
| `tangent.docs_enqueue` | `tangent_docs_enqueue` (spelling only) |
| `tangent.feedback` | `tangent_feedback` (spelling only) |
| `tangent.file-picker` | `tangent_file_picker` (spelling only) |
| `tangent.form-collect` | `tangent_form_collect` (spelling only) |
| `tangent.health_report` | `tangent_health_get` |
| `tangent.hitl_await` | `tangent_hitl_await` (spelling only) |
| `tangent.hitl_enqueue` | `tangent_hitl_enqueue` (spelling only) |
| `tangent.hitl_get` | `tangent_hitl_get` (spelling only) |
| `tangent.hitl_withdraw` | `tangent_hitl_withdraw` (spelling only) |
| `tangent.inbox_get` | `tangent_inbox_get` (spelling only) |
| `tangent.inbox_list` | `tangent_inbox_list` (spelling only) |
| `tangent.inbox_search` | `tangent_inbox_search` (spelling only) |
| `tangent.interaction_acknowledge` | `tangent_interaction_ack` |
| `tangent.interaction_await` | `tangent_interaction_await` (spelling only) |
| `tangent.interaction_cancel` | `tangent_interaction_cancel` (spelling only) |
| `tangent.interaction_resolve_definition` | `tangent_interaction_definition_resolve` |
| `tangent.interaction_get` | `tangent_interaction_get` (spelling only) |
| `tangent.interaction_list_kinds` | `tangent_interaction_kind_list` |
| `tangent.interaction_submit` | `tangent_interaction_submit` (spelling only) |
| `tangent.interaction_supersede` | `tangent_interaction_supersede` (spelling only) |
| `tangent.interview_question` | `tangent_interview_question` (spelling only) |
| `tangent.output_render` | `tangent_output_render` (spelling only) |
| `tangent.progress-panel` | `tangent_progress_panel` (spelling only) |
| `tangent.prose_revision` | `tangent_prose_revision` (spelling only) |
| `tangent.relay_ack` | `tangent_relay_ack` (spelling only) |
| `tangent.relay_attach` | `tangent_relay_attach` (spelling only) |
| `tangent.relay_capabilities` | `tangent_relay_capabilities_get` |
| `tangent.relay_open_channel` | `tangent_relay_channel_open` |
| `tangent.relay_detach` | `tangent_relay_detach` (spelling only) |
| `tangent.relay_receive` | `tangent_relay_receive` (spelling only) |
| `tangent.relay_send` | `tangent_relay_send` (spelling only) |
| `tangent.retention_status` | `tangent_retention_get` |
| `tangent.session_advance` | `tangent_session_advance` (spelling only) |
| `tangent.session_close` | `tangent_session_close` (spelling only) |
| `tangent.session_create` | `tangent_session_create` (spelling only) |
| `tangent.session_get` | `tangent_session_get` (spelling only) |
| `tangent.session_list` | `tangent_session_list` (spelling only) |
| `tangent.session_advance_phase` | `tangent_session_phase_advance` |
| `tangent.session_set_phase_output` | `tangent_session_phase_output_set` |
| `tangent.spreadsheet-review` | `tangent_spreadsheet_review` (spelling only) |
| `tangent.surface_close` | `tangent_surface_close` (spelling only) |
| `tangent.surface_get` | `tangent_surface_get` (spelling only) |
| `tangent.surface_open` | `tangent_surface_open` (spelling only) |
| `tangent.synthesis_notes` | `tangent_synthesis_notes` (spelling only) |
| `tangent.telemetry_query` | `tangent_telemetry_query` (spelling only) |
| `tangent.triage` | `tangent_triage` (spelling only) |
| `tangent.turn_ack` | `tangent_turns_ack` |
| `tangent.turn_await` | `tangent_turns_await` |
| `tangent.turns_enqueue` | `tangent_turns_enqueue` (spelling only) |
| `tangent.ui_close_drawer` | `tangent_ui_drawer_close` |
| `tangent.ui_open_drawer` | `tangent_ui_drawer_open` |
| `tangent.ui_focus_item` | `tangent_ui_item_focus` |
| `tangent.ui_close_modal` | `tangent_ui_modal_close` |
| `tangent.ui_open_modal` | `tangent_ui_modal_open` |
| `tangent.ui_navigate` | `tangent_ui_navigate` (spelling only) |
| `tangent.view_get` | `tangent_view_get` (spelling only) |
| `tangent.wizard` | `tangent_wizard` (spelling only) |
| `tangent.list_workflows` | `tangent_workflow_list` |

`tangent_ui_*` tools are generated from the core UI commands. The command names
the browser sees (`open_modal`, `close_modal`, `open_drawer`, `close_drawer`,
`focus_item`, `navigate`) are unchanged.

## Compatibility: deprecated aliases

Each old name is registered as a **deprecated alias** that routes to the same
handler as the new name, with the same annotations and input schema.

- **Callable by default.** `tools/call` with an old name keeps working, so a
  client or plugin pinned to the old name is not broken the day this ships.
- **Not listed by default.** The old names violate `[a-z0-9_]`; putting them in
  `tools/list` would hand a strict client (and the gateway lint) the very names
  that broke it. They are withheld from `tools/list` unless the operator sets
  `TANGENT_MCP_LEGACY_TOOL_NAMES=1` on `tangent` (not needed for strict
  clients; only for a client that discovers tools by listing and still wants
  the old names). When listed, each description starts with `DEPRECATED alias of
  <new name>`.
- **Window.** The aliases are scheduled for removal; the alias table lives in
  `internal/mcp/tool_names.go` (`legacyToolNames`) and removing it, and the
  hiding middleware, is the whole removal. Move callers to the new names now.
- A plugin-contributed tool is refused if it claims an alias name.

A test (`TestHostToolNamesMatchTheCharset`) fails if any non-alias tool name does
not match `^[a-z][a-z0-9_]*$`, and `TestLegacyToolAliases` pins the alias behavior.

## Plugin-contributed tools

Names a plugin contributes through `RegisterMCPTool` are chosen by the plugin and
are not renamed here. A plugin tool whose name contains a dot (for example the
GitHub plugin's `tangent.github_open_pr`) still trips the gateway lint and needs
the same rename in its own repository.

## `requester_scope` on the inbox tools

`tangent_inbox_list`, `tangent_inbox_search` and `tangent_inbox_get` require a
`requester_scope` argument. Pass **a stable string that names you, the calling
agent or application** (for example your own agent name, `my-chat-app`), the
same value on every call and the same one you use as your caller scope on other
tools that take one. It selects which items you may see. It is a declared
partition inside the `standalone-local` authority: **advisory, not a security
boundary.** Any local caller can assert any partition; isolation is enforced
only across authorities. The schema requires the argument; an empty or blank value resolves to the
`standalone-local:anonymous` partition, which is shared by every caller that
does the same, so name yourself.
