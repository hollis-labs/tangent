// AUTO-GENERATED FILE — DO NOT EDIT MANUALLY
// @definition-source sha256:7ea9b8d113d0314bae1375d65b0b3501c9ab1365fb167419f4a49274542a5dfb
// Generated from libs/ui-go v0.1.0 (envelopes) — do not edit.
// Run `make generate-envelopes` to regenerate.
//
// Coverage: 40 registered kinds — 18 go-envelopes core,
// 22 Tangent-owned (internal/envelope/extensions).
//
// Sources of truth: github.com/hollis-labs/libs/ui-go/envelopes (core catalog)
// and internal/envelope/extensions (Tangent kinds).
// Pipeline: cmd/tangent-dump-types -> scripts/generate-envelope-types.mjs

/** Shared type used by "table-card" */
export interface action {
  /** When true, the client asks the user to confirm before submitting the response. Defaults to false. */
  confirm?: boolean;
  /** Message shown in the confirmation prompt. Only meaningful when confirm is true. */
  confirm_message?: string;
  /** Stable action identifier. Echoed back verbatim as `action_id` in the response payload posted to POST /api/envelopes/{id}/respond. Not shown to the user. */
  id: string;
  /** Button text. */
  label: string;
  /** Visual emphasis. Defaults to "default" (a subtle/secondary button). */
  style?: "default" | "primary" | "destructive";
}

/** Shared type used by "tangent.hitl-item" */
export interface AdditionalCorrelationV1 {
  authority: string;
  id: string;
  kind: "agent" | "turn" | "workflow_run" | "step" | "conversation" | "business_object" | "other";
  label?: string;
  revision?: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface ApprovalActionLabelsV1 {
  approve?: string;
  approve_with_note?: string;
  deny?: string;
  deny_with_note?: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface ApprovalImpactV1 {
  approve: string;
  deny: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface ApprovalResponseV1 {
  decision: "approved" | "denied";
  kind: "approval";
  note?: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface ArtifactRefEvidenceV1 {
  /** Opaque authority-local identifier, not a path or URI. */
  artifact_id: string;
  authority: string;
  digest?: string;
  expires_at?: string;
  label: string;
  logical_kind?: string;
  media_type?: string;
  retention_policy?: string;
  retrieval_capability_id?: string;
  revision?: string;
  /** Opaque identifier owned by the same authority, not a path or URI. */
  safe_preview_artifact_id?: string;
  sensitivity?: "public" | "internal" | "confidential" | "restricted";
  size_bytes?: number;
  type: "artifact_ref";
}

/** Shared type used by "tangent.hitl-item" */
export interface AttentionActionLabelsV1 {
  acknowledge?: string;
  acknowledge_with_note?: string;
  reply?: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface AttentionResponseV1 {
  decision: "acknowledged";
  kind: "attention";
  note?: string;
  reply?: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface CallerAssertionV1 {
  application_id: string;
  principal_ref?: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface CanceledTerminalOutcomeV1 {
  cause: "caller_withdrawn" | "caller_canceled" | "participant_canceled" | "administrator_canceled" | "surface_policy";
  contract_version: "1.0";
  interaction_revision: number;
  item_id: string;
  reason?: string;
  state: "canceled";
  terminated_at: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface CorrelationsV1 {
  additional?: AdditionalCorrelationV1[];
  project?: ExternalRefV1;
  session?: ExternalRefV1;
  task?: ExternalRefV1;
}

/** Shared type used by "tangent.hitl-item" */
export type EvidenceV1 = InlineMarkdownEvidenceV1 | InlineTextEvidenceV1 | InlineDiffEvidenceV1 | TangentReferenceEvidenceV1 | ArtifactRefEvidenceV1;

/** Shared type used by "tangent.hitl-item" */
export interface ExpiredTerminalOutcomeV1 {
  contract_version: "1.0";
  interaction_revision: number;
  item_id: string;
  policy_ref?: string;
  state: "expired";
  terminated_at: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface ExternalRefV1 {
  /** System that owns and interprets the reference. */
  authority: string;
  id: string;
  label?: string;
  revision?: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface FailedTerminalOutcomeV1 {
  contract_version: "1.0";
  error_code: string;
  interaction_revision: number;
  item_id: string;
  message: string;
  state: "failed";
  terminated_at: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface HITLAwaitCommandV1 {
  caller: CallerAssertionV1;
  contract_version: "1.0";
  item_id: string;
  /** Bounded below Tangent's 60-second HTTP write timeout; values outside the range are rejected. */
  wait_ms?: number;
}

/** Shared type used by "tangent.hitl-item" */
export interface HITLAwaitTerminalResultV1 {
  contract_version: "1.0";
  item: unknown;
  mode: "await";
  retrieved_at: string;
  wait_status: "terminal";
}

/** Shared type used by "tangent.hitl-item" */
export interface HITLAwaitTimeoutResultV1 {
  contract_version: "1.0";
  item: unknown;
  mode: "await";
  retrieved_at: string;
  wait_status: "timeout";
}

/** Shared type used by "tangent.hitl-item" */
export interface HITLGetCommandV1 {
  caller: CallerAssertionV1;
  contract_version: "1.0";
  item_id: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface HITLGetRetrievalResultV1 {
  contract_version: "1.0";
  item: HITLItemViewV1;
  mode: "get";
  retrieved_at: string;
  wait_status: "not_waited";
}

/** Shared type used by "tangent.hitl-item" */
export interface HITLIdempotencyConflictErrorV1 {
  code: "idempotency_conflict";
  contract_version: "1.0";
  existing_item_id: string;
  idempotency_key: string;
  message?: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface HITLItemHandleV1 {
  contract_version: "1.0";
  inbox_url: string;
  item_id: string;
  item_url: string;
  queue_position: number | null;
  queue_sequence: number;
  revision: number;
  state: "staged" | "presented" | "in_progress" | "resolved" | "canceled" | "expired" | "failed" | "superseded";
  surface_id: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface HITLItemRequestV1 {
  action_labels?: unknown;
  contract_version: "1.0";
  correlations?: CorrelationsV1;
  details_markdown?: string;
  evidence?: EvidenceV1[];
  expires_at?: string;
  idempotency_key: string;
  impact?: unknown;
  kind: "approval" | "attention";
  recommendation?: string;
  request: string;
  source: SourceAssertionV1;
  summary: string;
  title: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface HITLItemViewV1 {
  contract_version: "1.0";
  enqueued_at: string;
  item_id: string;
  queue_position: number | null;
  queue_sequence: number;
  request_snapshot: HITLItemRequestV1;
  revision: number;
  state: "submitted" | "validated" | "staged" | "presented" | "in_progress" | "resolved" | "canceled" | "expired" | "failed" | "superseded";
  surface_id: string;
  terminal_outcome?: HITLTerminalOutcomeV1;
  updated_at: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface HITLResolutionCommandV1 {
  contract_version: "1.0";
  expected_revision: number;
  item_id: string;
  presented_projection_revision: number;
  response: HITLResponseV1;
}

/** Shared type used by "tangent.hitl-item" */
export interface HITLResolveStaleRevisionErrorV1 {
  actual_revision: number;
  code: "stale_revision";
  contract_version: "1.0";
  current_state: "submitted" | "validated" | "staged" | "presented" | "in_progress" | "resolved" | "canceled" | "expired" | "failed" | "superseded";
  expected_revision: number;
  item_id: string;
  operation: "resolve";
  revision_kind: "interaction" | "presented_projection";
  terminal_outcome?: HITLTerminalOutcomeV1;
}

/** Shared type used by "tangent.hitl-item" */
export type HITLResponseV1 = ApprovalResponseV1 | AttentionResponseV1;

/** Shared type used by "tangent.hitl-item" */
export type HITLRetrievalResultV1 = HITLGetRetrievalResultV1 | HITLAwaitTerminalResultV1 | HITLAwaitTimeoutResultV1;

/** Shared type used by "tangent.hitl-item" */
export type HITLStaleRevisionErrorV1 = HITLResolveStaleRevisionErrorV1 | HITLWithdrawStaleRevisionErrorV1;

/** Shared type used by "tangent.hitl-item" */
export type HITLTerminalOutcomeV1 = ResolvedTerminalOutcomeV1 | CanceledTerminalOutcomeV1 | ExpiredTerminalOutcomeV1 | FailedTerminalOutcomeV1 | SupersededTerminalOutcomeV1;

/** Shared type used by "tangent.hitl-item" */
export interface HITLWithdrawCommandV1 {
  caller: CallerAssertionV1;
  contract_version: "1.0";
  expected_revision?: number;
  item_id: string;
  reason?: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface HITLWithdrawStaleRevisionErrorV1 {
  actual_revision: number;
  code: "stale_revision";
  contract_version: "1.0";
  current_state: "submitted" | "validated" | "staged" | "presented" | "in_progress" | "resolved" | "canceled" | "expired" | "failed" | "superseded";
  expected_revision: number;
  item_id: string;
  operation: "withdraw";
  revision_kind: "interaction";
  terminal_outcome?: HITLTerminalOutcomeV1;
}

/** Shared type used by "tangent.hitl-item" */
export interface InlineDiffEvidenceV1 {
  base_label?: string;
  content: string;
  format?: "unified";
  head_label?: string;
  label: string;
  type: "diff";
}

/** Shared type used by "tangent.hitl-item" */
export interface InlineMarkdownEvidenceV1 {
  content: string;
  label: string;
  type: "markdown";
}

/** Shared type used by "tangent.hitl-item" */
export interface InlineTextEvidenceV1 {
  content: string;
  label: string;
  language?: string;
  type: "text";
}

/** Shared type used by "tangent.hitl-item" */
export interface ParticipantCaptureV1 {
  assurance: "loopback-unverified" | "asserted" | "authenticated";
  authority: string;
  principal_ref: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface ResolutionRecordV1 {
  interaction_revision: number;
  participant: ParticipantCaptureV1;
  presented_projection_revision: number;
  resolution_id: string;
  resolved_at: string;
  response: HITLResponseV1;
}

/** Shared type used by "tangent.hitl-item" */
export interface ResolvedTerminalOutcomeV1 {
  contract_version: "1.0";
  interaction_revision: number;
  item_id: string;
  resolution: ResolutionRecordV1;
  state: "resolved";
}

/** Shared type used by "tangent.hitl-item" */
export interface SourceAssertionV1 {
  agent_id: string;
  agent_label?: string;
  application_id: string;
  application_label?: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface SupersededTerminalOutcomeV1 {
  contract_version: "1.0";
  interaction_revision: number;
  item_id: string;
  replacement_item_id: string;
  state: "superseded";
  terminated_at: string;
}

/** Shared type used by "tangent.hitl-item" */
export interface TangentReferenceEvidenceV1 {
  description?: string;
  interaction_id?: string;
  label: string;
  revision?: number;
  surface_id: string;
  type: "tangent_reference";
}

/** Envelope data for "approval-card" — An approval request with risk level indicator. */
export interface ApprovalCardData {
  /** What is being requested for approval. */
  description: string;
  /** Additional details about the request. */
  details?: string;
  /** Risk level of the action. Defaults to low. */
  risk_level?: "low" | "medium" | "high";
}

/** Envelope data for "artifact-mini" — Compact downloadable-artifact card surfaced in the bottom chat drawer. Renders name, mime, size, Download CTA, and a Dismiss button. Lifetime is transient — auto-closes after the user downloads or dismisses. Use for ephemeral 'your file is ready' surfaces. Durable session-wide artifact lists are a host concern. */
export interface ArtifactMiniData {
  /** ID of the artifact row in the artifacts table. Drives the /api/artifacts/{id}/download URL. */
  artifact_id: string;
  /** MIME type, e.g. application/zip, text/markdown, image/png. */
  mime_type: string;
  /** Filename shown in the card. */
  name: string;
  /** Optional origin attribution, e.g. 'auto', 'placed', 'uploaded'. */
  origin?: string;
  /** Size in bytes; rendered as a human-friendly KB/MB string. */
  size_bytes?: number;
}

/** Envelope data for "chat-loop-terminated" — Emitted when the chat loop exits abnormally (hard circuit-breaker, idle timeout, max turns, hard ceiling, retry budget). Carries the structured reason so the FE can render a terminal pause card instead of truncating the assistant bubble. */
export interface ChatLoopTerminatedData {
  /** Machine-readable termination code. */
  code: "runaway_tool_failures" | "max_turns" | "hard_ceiling" | "idle_timeout" | "retry_budget_exhausted";
  /** Number of consecutive tool failures at termination. */
  consecutive_failures: number;
  /** Loop iteration at which termination occurred. */
  iteration: number;
  /** Last tool error message (if any), truncated for display. */
  last_error?: string;
  /** Name of the last tool whose failure contributed to termination. */
  last_tool?: string;
  /** Human-readable reason the loop terminated. */
  reason: string;
  /** ISO 8601 timestamp of termination. */
  timestamp: string;
}

/** Envelope data for "confirmation-card" — A confirmation dialog with risk-level-styled buttons. Also supports a live `data_source` pointer (composition target for the retired standalone `plan-review` type — see TASKS/phase-6/02-rebuild-plan-review-as-composition.md) so the frontend can re-fetch live status at render time instead of trusting only `prior_response`. */
export interface ConfirmationCardData {
  /** Label for the cancel button. Defaults to Cancel. */
  cancel_label?: string;
  /** Label for the confirm button. Defaults to Confirm. */
  confirm_label?: string;
  /** Optional live-data-source pointer. When present, the frontend re-fetches live status from this source at render time (rather than trusting only `prior_response`) and routes confirm/cancel through the source's own mutation instead of the generic typed-response POST. */
  data_source?: { kind: "plan_approval"; plan_id?: string };
  /** Confirmation message body. */
  message: string;
  /** Optional label for a third, non-committal action (e.g. "Request changes"). Only rendered when present. Currently only meaningful when `data_source.kind` is "plan_approval". */
  request_changes_label?: string;
  /** Risk level. Defaults to low. */
  risk?: "low" | "medium" | "high";
  /** Confirmation title. */
  title: string;
}

/** Envelope data for "diff-card" — A side-by-side before/after comparison. */
export interface DiffCardData {
  /** The after state. */
  after: { content: string; label: string };
  /** The before state. */
  before: { content: string; label: string };
  /** Content format. Defaults to text. */
  format?: "text" | "code";
  /** Optional diff title. */
  title?: string;
}

/** Envelope data for "document-viewer" — Renders a document with optional download and section navigation. */
export interface DocumentViewerData {
  /** The document body (HTML or Markdown). */
  content: string;
  /** Whether the download button is shown. */
  download_enabled?: boolean;
  /** Filename used when the user downloads the document. */
  download_filename?: string;
  /** Content format. Defaults to markdown. */
  format?: "html" | "markdown";
  /** Section names for navigation links. */
  sections?: string[];
  /** Document title shown in the header. */
  title: string;
}

/** Envelope data for "elicitation-prompt" — A mid-tool-call user prompt issued by MCP elicitation/create (spec 2025-06-18). Rendered inline in chat with a text input (string schema) or accept/decline buttons (boolean schema). */
export interface ElicitationPromptData {
  /** Unique ID of the pending elicitation request. Must be echoed back in the response. */
  elicitation_id: string;
  /** The question or prompt shown to the user. */
  message: string;
  /** Where the elicitation was issued: server (the host's own tool) or client (external MCP server). */
  origin: "server" | "client";
  /** Optional helper text shown below the input widget. */
  schema_description?: string;
  /** Optional short label shown above the input widget. */
  schema_title?: string;
  /** Response type: boolean renders accept/decline buttons; string renders a text input. */
  schema_type: "boolean" | "string";
  /** ISO 8601 timestamp when the server will auto-cancel if no response arrives (default: 5 min from creation). */
  timeout_at: string;
  /** Originating tool-call ID (MCP tool_use_id) for client-side correlation. */
  tool_call_id?: string;
}

/** Envelope data for "error-report" — An error card with code, message, details, and optional Giphy illustration. */
export interface ErrorReportData {
  /** Error code (e.g. rate_limit, tool_error, provider_error, internal_error). */
  code: string;
  /** Arbitrary error details. */
  details?: Record<string, unknown>;
  /** Search query for the Giphy illustration. */
  giphy_query: string;
  /** Human-readable error message. */
  message: string;
  /** ISO 8601 timestamp of the error. */
  timestamp: string;
}

/** Envelope data for "info-card" — A simple informational card with variant styling. */
export interface InfoCardData {
  /** Card body text. */
  body: string;
  /** Card title. */
  title: string;
  /** Visual variant. Defaults to info. */
  variant?: "info" | "success" | "warning" | "danger";
}

/** Envelope data for "list-card" — An ordered or unordered list with optional item actions. Also supports a live `data_source` pointer (composition target for the retired standalone `todo-list` type — see TASKS/phase-6/01-rebuild-todo-list-as-composition.md — and the retired standalone `plan-review` type — see TASKS/phase-6/02-rebuild-plan-review-as-composition.md) so the frontend can re-fetch items at render time instead of trusting a static snapshot. */
export interface ListCardData {
  /** Optional live-data-source pointer. When present, the frontend re-fetches items from this source at render time (and re-fetches again after each mutation) instead of trusting the static `items` array above. */
  data_source?: { kind: "todos" | "plans"; plan_id?: string; scope?: string; scope_id?: string };
  /** List items. May be an empty placeholder array when `data_source` is set — the frontend re-fetches the real items live and does not trust this snapshot in that case. */
  items: { action?: { label: string; type: string }; description?: string; icon?: string; id?: string; label: string; status?: "pending" | "done" | "in_progress" | "skipped" }[];
  /** Whether to show numbered list. Defaults to false. */
  ordered?: boolean;
  /** Optional list title. */
  title?: string;
}

/** Envelope data for "metric-card" — A single metric display with optional trend indicator. */
export interface MetricCardData {
  /** Additional context. */
  description?: string;
  /** Metric label. */
  label: string;
  /** Previous value for comparison. */
  previous?: string | number;
  /** Trend direction. */
  trend?: "up" | "down" | "flat";
  /** Unit suffix (e.g. ms, %, GB). */
  unit?: string;
  /** The metric value. */
  value: string | number;
}

/** Envelope data for "progress-card" — A progress bar with optional step checklist. */
export interface ProgressCardData {
  /** Additional description. */
  description?: string;
  /** Progress percentage (0-100). */
  progress: number;
  /** Current status text. */
  status?: string;
  /** Step checklist. */
  steps?: { done: boolean; label: string }[];
  /** Progress title. */
  title: string;
}

/** Envelope data for "proposal-card" — A proposed action with editable fields that the user can apply or dismiss. */
export interface ProposalCardData {
  /** The proposed field values. */
  payload: Record<string, unknown>;
  /** Field schema definitions for rendering appropriate input types. */
  schema?: Record<string, { label?: string; options?: string[]; required?: boolean; type?: string }>;
  /** Proposal type (e.g. create_task, update_sprint). */
  type: string;
}

/** Envelope data for "report-card" — A metrics report with summary and action buttons. */
export interface ReportCardData {
  /** Action buttons shown at the bottom. */
  actions?: { action: string; id?: string; label: string }[];
  /** ISO 8601 timestamp of report generation. */
  generated_at?: string;
  /** Array of metric entries. */
  metrics: { color?: string; label: string; percent?: number; value: string }[];
  /** Optional link back to the full session/task/run this report distills — lets a host render 'summary + link' instead of a raw transcript dump. */
  session_link?: { label: string; url: string };
  /** Markdown summary text shown below metrics. */
  summary?: string;
  /** Report title. */
  title: string;
}

/** Envelope data for "session-task" — A session-scoped task card with status transitions. */
export interface SessionTaskData {
  /** Optional description shown below the task title. */
  description?: string;
  /** Current task status. Use "canceled" for new payloads. The legacy "cancelled" spelling is retained in go-envelopes v0.5.x. Removal is not scheduled and will be announced first. */
  status: "pending" | "in_progress" | "completed" | "failed" | "canceled" | "cancelled";
  /** Unique identifier for the task. */
  task_id: string;
  /** Display title of the task. */
  title: string;
}

/** Envelope data for "subagent-spawn-approval" — Requests user approval before a subagent spawn begins execution. */
export interface SubagentSpawnApprovalData {
  inputs_json?: string;
  mode: "sync" | "async" | "api" | "interactive";
  parent_agent_id?: string;
  prompt: string;
  risk_level?: "low" | "medium" | "high";
  role: string;
  run_id: string;
  timeout_seconds?: number;
}

/** Envelope data for "table-card" — A sortable data table, optionally with schema-validated row and/or column actions. */
export interface TableCardData {
  /** Row-scoped actions rendered as a button group on every row. A response for one of these carries action_id and row_index (no column_key). Interactive tables (root-level `actions` and/or any column `actions`) must be emitted through a path that persists an EnvelopeInstance (e.g. the same emit mechanism approval-card/elicitation-prompt use) so the envelope has an id the frontend can POST a response against — the passive card_show path does not create one. */
  actions?: action[];
  /** Optional table caption. */
  caption?: string;
  /** Column definitions. */
  columns: { actions?: action[]; key: string; label: string; sortable?: boolean }[];
  /** Row data. Keys must match column keys. */
  rows: Record<string, unknown>[];
  /** Optional table title. */
  title?: string;
}

/** Envelope data for "tangent.agent-turn" — Durable operator-facing agent turn item: captures live agent questions, approvals, checkpoints, failures, and completions in arrival order. */
export interface TangentAgentTurnData {
  annotations?: { kind: "summary"; schema_version: 1; stage_id: string; stage_version: string; summary: { text: string } }[];
  content: string;
  contract_version: "1.0" | "1.1";
  correlations?: { project_id?: string; runtime_ref?: string; task_id?: string };
  expires_at?: string;
  idempotency_key: string;
  kind: "question" | "approval" | "checkpoint" | "failure" | "terminal";
  options?: { description?: string; label: string; recommended?: boolean; value: string }[];
  session_id?: string;
  source: { agent_id: string; agent_label?: string; application_id?: string };
  source_message?: { attribution?: { confidence?: "exact" | "heuristic" | "none" | "unknown"; kind?: "final" | "question" | "approval" | "failure"; launch_display_name?: string; launch_id?: string; logical_agent_id?: string; project_id?: string; runtime?: string; stop_reason?: string; workstream_id?: string }; channel: string; endpoint_ref: string; message_id: string; origin: "routed" | "publication"; output_id?: string; schema_version: 1; sender_urn: string; sequence: number };
  stage_trace?: { duration_ms: number; failure_code?: "stage_error" | "stage_timeout" | "stage_panic" | "stage_refused" | "invalid_output" | "annotation_limit"; outcome: "passed" | "failed" | "timed_out"; stage_id: string; stage_version: string }[];
  summary?: string;
  title: string;
  turn_id?: string;
}

/** Envelope data for "tangent.app-board" — App board envelope: a domain-free board of agent-supplied cards in columns, with a filter bar, an optional detail pane, and participant view state recorded as tangent-custodied draft revisions. Filters are a VIEW over the card set the caller supplied — the host never re-runs them as a query and cannot reach a record the caller did not send. The application that owns the records supplies them and applies every consequence; nothing here writes to it. */
export interface TangentAppBoardData {
  board_id: string;
  cards: { badges?: { id?: string; label: string; tone?: "neutral" | "info" | "success" | "warning" | "danger" }[]; body?: string; fields?: Record<string, unknown>; id: string; note?: string; subtitle?: string; title: string }[];
  columns?: { card_ids?: string[]; id: string; label: string }[];
  detail?: { actions?: { id: string; label: string; tone?: "neutral" | "primary" | "danger" }[]; card_id?: string; open?: boolean; raised_by?: "agent" | "user"; sections?: { label: string; markdown?: string }[] };
  filters?: { field?: string; id: string; kind: "single" | "multi" | "text"; label: string; options?: { count?: number; label?: string; value: string }[]; selected?: string[] }[];
  source?: { app?: string; label?: string };
  /** What the board may use to talk back to the caller without an agent turn. Absent means the board is a read-only view. */
  sync?: { enabled?: boolean; endpoint?: string; label?: string; note_label?: string; scope?: string; stage_label?: string };
  title?: string;
  updated_at?: string;
}

/** Envelope data for "tangent.approval-queue" — Approval queue envelope: serialized review of queued items with explicit accept, reject, or defer decisions, evidence panes, and durable audit export metadata. */
export interface TangentApprovalQueueData {
  audit_trail?: Record<string, unknown>[];
  current_index?: number;
  decisions?: Record<string, unknown>[];
  export_refs?: Record<string, unknown>[];
  intent?: string;
  items: Record<string, unknown>[];
  notes?: string;
  queue_id: string;
  title?: string;
}

/** Envelope data for "tangent.block-draft" — Block-draft envelope: propose one draft block, capture accept/revise/inline-edit direction, and accumulate accepted blocks on the room. */
export interface TangentBlockDraftData {
  block_id: string;
  content: string;
  label?: string;
  mode?: "section" | "paragraph";
  outline_hint?: string;
  rationale?: string;
}

/** Envelope data for "tangent.dashboard" — Dashboard envelope: room-backed tiles, layouts, and explicit refresh or update turns. */
export interface TangentDashboardData {
  active_layout_id?: string;
  dashboard_id: string;
  layout?: { h: number; tile_id: string; w: number; x: number; y: number }[];
  query_state?: Record<string, unknown>;
  saved_layouts?: unknown[];
  summary?: Record<string, unknown>;
  tiles: { artifact_ref?: string; kind: string; metadata?: Record<string, unknown>; room_id?: string; status?: string; subtitle?: string; summary?: string; tile_id: string; title: string; unit?: string; value?: string; workflow?: string }[];
  title?: string;
  updated_at?: string;
}

/** Envelope data for "tangent.design-iteration" — Design-iteration envelope: render sandboxed HTML, collect click/input actions, and iterate through multiple variants in one room. */
export interface TangentDesignIterationData {
  /** Optional human-facing instruction shown above the preview. */
  caption?: string;
  /** Sandboxed HTML rendered into an iframe srcdoc. */
  html: string;
  prompts: { id: string; kind: "click-region" | "button" | "text-input"; label?: string; placeholder?: string; selector?: string }[];
  /** Agent-assigned identifier for this iteration variant. */
  variant_id: string;
}

/** Envelope data for "tangent.diff-review" — Diff-review envelope: room-backed before/after review with per-file and per-hunk decisions, comments, durable artifact refs, and explicit submit. */
export interface TangentDiffReviewData {
  after_ref?: Record<string, unknown>;
  before_ref?: Record<string, unknown>;
  current_file?: string;
  files: { after?: string; before?: string; hunks?: { after?: string; before?: string; header?: string; id?: string; status?: string }[]; id: string; path?: string; summary?: string }[];
  filter_state?: Record<string, unknown>;
  intent?: string;
  review_id: string;
  title?: string;
}

/** Envelope data for "tangent.doc-item" — Durable operator-facing document item: a markdown document an agent sends for reading, with independent read/unread tracking and an optional acknowledgment. */
export interface TangentDocItemData {
  content_markdown: string;
  contract_version: "1.0";
  correlations?: Record<string, unknown>;
  idempotency_key: string;
  requires_ack?: boolean;
  source: { agent_id: string; agent_label?: string; application_id?: string };
  summary?: string;
  tags?: string[];
  title: string;
}

/** Envelope data for "tangent.external-review" — Review an external resource with a retained snapshot, caller notes, and explicit plugin-served actions. The plugin owns the application and reports each action outcome. */
export interface TangentExternalReviewData {
  action_url: string;
  actions: { id: string; label: string; options?: { label: string; value: string }[] }[];
  content_markdown: string;
  fields?: { label: string; value: string }[];
  notes?: string;
  resource: { label?: string; revision: string; url: string };
  review_id: string;
  state_url: string;
  summary?: string;
  title: string;
}

/** Envelope data for "tangent.feedback" — Feedback envelope: ask a human to answer a short structured questionnaire. */
export interface TangentFeedbackData {
  layout?: "inline" | "walkthrough" | "auto";
  /** Optional human-facing instruction shown above the form. */
  prompt?: string;
  questions: { allowNote?: boolean; default?: unknown; help?: string; id: string; label: string; options?: { help?: string; label: string; value: string }[]; placeholder?: string; required?: boolean; suggestion?: { rationale?: string; value: unknown }; type: "radio" | "checkbox" | "select" | "multiselect" | "text" | "textarea" }[];
}

/** Envelope data for "tangent.file-picker" — File-picker envelope: room-backed local artifact selection with allowed roots and explicit submit. */
export interface TangentFilePickerData {
  browse_roots: { kind?: string; label?: string; path: string; root_id: string }[];
  files?: { artifact_id?: string; kind?: string; mime_type?: string; name?: string; relative_path: string; root_id: string; size_bytes?: number; uri?: string }[];
  picker_id: string;
  query_state?: Record<string, unknown>;
  selected_refs?: { artifact_id?: string; kind?: string; mime_type?: string; name?: string; relative_path: string; root_id: string; size_bytes?: number; uri?: string }[];
}

/** Envelope data for "tangent.form-collect" — Generalized schema-driven form workflow with room-backed persistence. */
export interface TangentFormCollectData {
  actions?: unknown[];
  answers?: Record<string, unknown>;
  attachment_refs?: unknown[];
  form_id: string;
  intent?: string;
  notes?: string;
  saved_drafts?: unknown[];
  schema: Record<string, unknown>;
  submission_summary?: Record<string, unknown>;
  templates?: unknown[];
}

/** Envelope data for "tangent.hitl-item" — Durable HITL inbox item: one operator-owned approval or persistent-attention interaction with typed evidence and an immutable per-item terminal outcome. */
export type TangentHitlItemData = HITLItemRequestV1;

/** Envelope data for "tangent.interview-question" — Interview-question envelope: ask one long-form question with optional quick-picks and explicit output-shape prompting. */
export interface TangentInterviewQuestionData {
  choices?: { description?: string; id: string; label: string }[];
  helper_text?: string;
  output_shape?: { help?: string; label: string; placeholder?: string };
  prompt?: string;
  prompt_markdown?: string;
  thread_id?: string;
  topic_label?: string;
}

/** Envelope data for "tangent.output-render" — Output-render envelope: present the final markdown artifact with copy/export affordances and preserve the room's canonical final output. */
export interface TangentOutputRenderData {
  filename?: string;
  format?: "markdown";
  markdown: string;
  summary?: string;
  title?: string;
}

/** Envelope data for "tangent.progress-panel" — Progress-panel envelope: room-backed progress items, summary state, and explicit operator updates. */
export interface TangentProgressPanelData {
  items: { completed_at?: string; created_at?: string; detail?: string; item_id: string; label: string; metadata?: Record<string, unknown>; status: string; updated_at?: string }[];
  panel_id: string;
  summary?: { completed_at?: string; current_status?: string; detail?: string; headline?: string; last_checkpoint_id?: string; last_update_id?: string };
  updates?: { checkpoint_id?: string; checkpoint_label?: string; created_at?: string; item_id?: string; kind: "status" | "checkpoint" | "summary"; metadata?: Record<string, unknown>; status?: string; summary?: string; update_id: string }[];
}

/** Envelope data for "tangent.prose-revision" — Prose-revision envelope: review suggested edits through one unified review/copy/style lens and capture explicit per-suggestion outcomes. */
export interface TangentProseRevisionData {
  block_id?: string;
  label?: string;
  lens: "review" | "copy" | "style";
  revision_id?: string;
  source_text: string;
  suggestions: { id: string; label?: string; original_text?: string; reason?: string; suggested_text: string }[];
  summary?: string;
}

/** Envelope data for "tangent.spreadsheet-review" — Spreadsheet review envelope: render canonical agent-provided rows in a persistent room with explicit submit, saved views, and CSV export metadata. */
export interface TangentSpreadsheetReviewData {
  action_id?: string;
  columns?: { id: string; label?: string; sortable?: boolean }[];
  export_refs?: { column_count?: number; created_at?: string; kind?: string; mime_type?: string; name: string; row_count?: number; size_bytes?: number }[];
  intent?: string;
  notes?: string;
  query_state?: Record<string, unknown>;
  row_actions?: { description?: string; id: string; label?: string }[];
  rows?: Record<string, unknown>[];
  saved_views?: { name: string; query_state?: Record<string, unknown> }[];
  selected_row_ids?: string[];
  selected_rows?: Record<string, unknown>[];
  table_id: string;
  title?: string;
  updated_at?: string;
}

/** Envelope data for "tangent.synthesis-notes" — Synthesis-notes envelope: persist private working notes, optionally carry an outline artifact, and expose only the phase-gated preview to the user. */
export interface TangentSynthesisNotesData {
  has_private_notes?: boolean;
  outline?: { items?: { description?: string; label?: string }[]; title?: string };
  outline_state?: "absent" | "present" | "skipped";
  private_notes?: string;
  summary?: string;
  visibility?: "hidden" | "visible";
}

/** Envelope data for "tangent.triage" — Triage envelope: ask a human to accept, reject, or annotate an item. Tangent v0.1 plugin-registered; planned for go-envelopes core in v0.3. */
export interface TangentTriageData {
  /** Free-form context bag (links, metadata) passed through to the frontend. */
  context?: Record<string, unknown>;
  /** Items to triage. Strings are rendered as labels; objects pass through to the frontend untouched. */
  items?: string | Record<string, unknown>[];
  /** Optional human-facing instruction shown above the items. */
  prompt?: string;
}

/** Envelope data for "tangent.whiteboard" — Whiteboard envelope: present a tldraw-backed board with persisted room snapshot hydration and a full-scene submit response. */
export interface TangentWhiteboardData {
  assets?: { artifact_id?: string; asset_id?: string; height?: number; kind?: "reference_image"; mime_type?: string; name?: string; source?: string; uri?: string; width?: number }[];
  board_id: string;
  export_refs?: { artifact_id?: string; created_at?: string; height?: number; kind?: "png"; mime_type?: string; name?: string; size_bytes?: number; uri?: string; width?: number }[];
  intent?: string;
  notes?: string;
  reference_images?: { artifact_id?: string; asset_id?: string; height?: number; kind?: "reference_image"; mime_type?: string; name?: string; source?: string; uri?: string; width?: number }[];
  revision_history?: { asset_count?: number; continued_from_revision_id?: string; revision_id: string; scene_size?: number; summary?: string; updated_at?: string }[];
  revision_id?: string;
  revisions?: { asset_count?: number; assets?: { artifact_id?: string; asset_id?: string; height?: number; kind?: "reference_image"; mime_type?: string; name?: string; source?: string; uri?: string; width?: number }[]; continued_from_revision_id?: string; export_refs?: { artifact_id?: string; created_at?: string; height?: number; kind?: "png"; mime_type?: string; name?: string; size_bytes?: number; uri?: string; width?: number }[]; notes?: string; reference_images?: { artifact_id?: string; asset_id?: string; height?: number; kind?: "reference_image"; mime_type?: string; name?: string; source?: string; uri?: string; width?: number }[]; revision_id: string; scene?: Record<string, unknown>; scene_size?: number; summary?: string; updated_at?: string }[];
  scene?: Record<string, unknown>;
  title?: string;
  tool_mode?: "select" | "draw" | "text" | "shape" | "arrow" | "note";
  updated_at?: string;
}

/** Envelope data for "tangent.wizard" — Step-based wizard workflow with room-backed progress, branching selections, review, and completion. */
export interface TangentWizardData {
  branch_selections?: unknown[];
  current_step_id?: string;
  description?: string;
  progress?: unknown[];
  steps: { branches?: unknown[]; description?: string; fields?: Record<string, unknown>; kind?: string; metadata?: Record<string, unknown>; optional?: boolean; step_id: string; title: string }[];
  summary?: Record<string, unknown>;
  title?: string;
  updated_at?: string;
  wizard_id: string;
}

/** Envelope data for "timeline-card" — A vertical timeline of events. */
export interface TimelineCardData {
  /** Timeline events in order. */
  events: { description?: string; icon?: string; label: string; status?: "completed" | "active" | "pending"; timestamp: string }[];
  /** Optional timeline title. */
  title?: string;
}

/** Trace metadata mirrored from go-envelopes Trace. */
export interface EnvelopeTrace {
  agentId?: string;
  sessionId?: string;
  parentEnvelopeId?: string;
  createdAt?: string;
}

/** Presentation hints. Hosts MAY honor and MUST NOT fail on unknown values. */
export type EnvelopePresentation =
  | 'inline'
  | 'modal'
  | 'drawer'
  | 'sidecar'
  | 'fullscreen';

interface EnvelopeBase<TType extends string, TData> {
  v: number;
  id: string;
  type: TType;
  typeVersion?: string;
  title?: string;
  context?: string;
  presentation?: EnvelopePresentation;
  data?: TData;
  trace?: EnvelopeTrace;
  meta?: Record<string, unknown>;
}

export type ApprovalCardEnvelope = EnvelopeBase<"approval-card", ApprovalCardData>;
export type ArtifactMiniEnvelope = EnvelopeBase<"artifact-mini", ArtifactMiniData>;
export type ChatLoopTerminatedEnvelope = EnvelopeBase<"chat-loop-terminated", ChatLoopTerminatedData>;
export type ConfirmationCardEnvelope = EnvelopeBase<"confirmation-card", ConfirmationCardData>;
export type DiffCardEnvelope = EnvelopeBase<"diff-card", DiffCardData>;
export type DocumentViewerEnvelope = EnvelopeBase<"document-viewer", DocumentViewerData>;
export type ElicitationPromptEnvelope = EnvelopeBase<"elicitation-prompt", ElicitationPromptData>;
export type ErrorReportEnvelope = EnvelopeBase<"error-report", ErrorReportData>;
export type InfoCardEnvelope = EnvelopeBase<"info-card", InfoCardData>;
export type ListCardEnvelope = EnvelopeBase<"list-card", ListCardData>;
export type MetricCardEnvelope = EnvelopeBase<"metric-card", MetricCardData>;
export type ProgressCardEnvelope = EnvelopeBase<"progress-card", ProgressCardData>;
export type ProposalCardEnvelope = EnvelopeBase<"proposal-card", ProposalCardData>;
export type ReportCardEnvelope = EnvelopeBase<"report-card", ReportCardData>;
export type SessionTaskEnvelope = EnvelopeBase<"session-task", SessionTaskData>;
export type SubagentSpawnApprovalEnvelope = EnvelopeBase<"subagent-spawn-approval", SubagentSpawnApprovalData>;
export type TableCardEnvelope = EnvelopeBase<"table-card", TableCardData>;
export type TangentAgentTurnEnvelope = EnvelopeBase<"tangent.agent-turn", TangentAgentTurnData>;
export type TangentAppBoardEnvelope = EnvelopeBase<"tangent.app-board", TangentAppBoardData>;
export type TangentApprovalQueueEnvelope = EnvelopeBase<"tangent.approval-queue", TangentApprovalQueueData>;
export type TangentBlockDraftEnvelope = EnvelopeBase<"tangent.block-draft", TangentBlockDraftData>;
export type TangentDashboardEnvelope = EnvelopeBase<"tangent.dashboard", TangentDashboardData>;
export type TangentDesignIterationEnvelope = EnvelopeBase<"tangent.design-iteration", TangentDesignIterationData>;
export type TangentDiffReviewEnvelope = EnvelopeBase<"tangent.diff-review", TangentDiffReviewData>;
export type TangentDocItemEnvelope = EnvelopeBase<"tangent.doc-item", TangentDocItemData>;
export type TangentExternalReviewEnvelope = EnvelopeBase<"tangent.external-review", TangentExternalReviewData>;
export type TangentFeedbackEnvelope = EnvelopeBase<"tangent.feedback", TangentFeedbackData>;
export type TangentFilePickerEnvelope = EnvelopeBase<"tangent.file-picker", TangentFilePickerData>;
export type TangentFormCollectEnvelope = EnvelopeBase<"tangent.form-collect", TangentFormCollectData>;
export type TangentHitlItemEnvelope = EnvelopeBase<"tangent.hitl-item", TangentHitlItemData>;
export type TangentInterviewQuestionEnvelope = EnvelopeBase<"tangent.interview-question", TangentInterviewQuestionData>;
export type TangentOutputRenderEnvelope = EnvelopeBase<"tangent.output-render", TangentOutputRenderData>;
export type TangentProgressPanelEnvelope = EnvelopeBase<"tangent.progress-panel", TangentProgressPanelData>;
export type TangentProseRevisionEnvelope = EnvelopeBase<"tangent.prose-revision", TangentProseRevisionData>;
export type TangentSpreadsheetReviewEnvelope = EnvelopeBase<"tangent.spreadsheet-review", TangentSpreadsheetReviewData>;
export type TangentSynthesisNotesEnvelope = EnvelopeBase<"tangent.synthesis-notes", TangentSynthesisNotesData>;
export type TangentTriageEnvelope = EnvelopeBase<"tangent.triage", TangentTriageData>;
export type TangentWhiteboardEnvelope = EnvelopeBase<"tangent.whiteboard", TangentWhiteboardData>;
export type TangentWizardEnvelope = EnvelopeBase<"tangent.wizard", TangentWizardData>;
export type TimelineCardEnvelope = EnvelopeBase<"timeline-card", TimelineCardData>;

/** Discriminated union of every envelope type Tangent has registered. */
export type Envelope =
  | ApprovalCardEnvelope
  | ArtifactMiniEnvelope
  | ChatLoopTerminatedEnvelope
  | ConfirmationCardEnvelope
  | DiffCardEnvelope
  | DocumentViewerEnvelope
  | ElicitationPromptEnvelope
  | ErrorReportEnvelope
  | InfoCardEnvelope
  | ListCardEnvelope
  | MetricCardEnvelope
  | ProgressCardEnvelope
  | ProposalCardEnvelope
  | ReportCardEnvelope
  | SessionTaskEnvelope
  | SubagentSpawnApprovalEnvelope
  | TableCardEnvelope
  | TangentAgentTurnEnvelope
  | TangentAppBoardEnvelope
  | TangentApprovalQueueEnvelope
  | TangentBlockDraftEnvelope
  | TangentDashboardEnvelope
  | TangentDesignIterationEnvelope
  | TangentDiffReviewEnvelope
  | TangentDocItemEnvelope
  | TangentExternalReviewEnvelope
  | TangentFeedbackEnvelope
  | TangentFilePickerEnvelope
  | TangentFormCollectEnvelope
  | TangentHitlItemEnvelope
  | TangentInterviewQuestionEnvelope
  | TangentOutputRenderEnvelope
  | TangentProgressPanelEnvelope
  | TangentProseRevisionEnvelope
  | TangentSpreadsheetReviewEnvelope
  | TangentSynthesisNotesEnvelope
  | TangentTriageEnvelope
  | TangentWhiteboardEnvelope
  | TangentWizardEnvelope
  | TimelineCardEnvelope;

/** String literal union of every registered envelope type name. */
export type EnvelopeType =
  | "approval-card"
  | "artifact-mini"
  | "chat-loop-terminated"
  | "confirmation-card"
  | "diff-card"
  | "document-viewer"
  | "elicitation-prompt"
  | "error-report"
  | "info-card"
  | "list-card"
  | "metric-card"
  | "progress-card"
  | "proposal-card"
  | "report-card"
  | "session-task"
  | "subagent-spawn-approval"
  | "table-card"
  | "tangent.agent-turn"
  | "tangent.app-board"
  | "tangent.approval-queue"
  | "tangent.block-draft"
  | "tangent.dashboard"
  | "tangent.design-iteration"
  | "tangent.diff-review"
  | "tangent.doc-item"
  | "tangent.external-review"
  | "tangent.feedback"
  | "tangent.file-picker"
  | "tangent.form-collect"
  | "tangent.hitl-item"
  | "tangent.interview-question"
  | "tangent.output-render"
  | "tangent.progress-panel"
  | "tangent.prose-revision"
  | "tangent.spreadsheet-review"
  | "tangent.synthesis-notes"
  | "tangent.triage"
  | "tangent.whiteboard"
  | "tangent.wizard"
  | "timeline-card";

/** Maps each envelope type string to its data interface. */
export interface EnvelopeDataMap {
  "approval-card": ApprovalCardData;
  "artifact-mini": ArtifactMiniData;
  "chat-loop-terminated": ChatLoopTerminatedData;
  "confirmation-card": ConfirmationCardData;
  "diff-card": DiffCardData;
  "document-viewer": DocumentViewerData;
  "elicitation-prompt": ElicitationPromptData;
  "error-report": ErrorReportData;
  "info-card": InfoCardData;
  "list-card": ListCardData;
  "metric-card": MetricCardData;
  "progress-card": ProgressCardData;
  "proposal-card": ProposalCardData;
  "report-card": ReportCardData;
  "session-task": SessionTaskData;
  "subagent-spawn-approval": SubagentSpawnApprovalData;
  "table-card": TableCardData;
  "tangent.agent-turn": TangentAgentTurnData;
  "tangent.app-board": TangentAppBoardData;
  "tangent.approval-queue": TangentApprovalQueueData;
  "tangent.block-draft": TangentBlockDraftData;
  "tangent.dashboard": TangentDashboardData;
  "tangent.design-iteration": TangentDesignIterationData;
  "tangent.diff-review": TangentDiffReviewData;
  "tangent.doc-item": TangentDocItemData;
  "tangent.external-review": TangentExternalReviewData;
  "tangent.feedback": TangentFeedbackData;
  "tangent.file-picker": TangentFilePickerData;
  "tangent.form-collect": TangentFormCollectData;
  "tangent.hitl-item": TangentHitlItemData;
  "tangent.interview-question": TangentInterviewQuestionData;
  "tangent.output-render": TangentOutputRenderData;
  "tangent.progress-panel": TangentProgressPanelData;
  "tangent.prose-revision": TangentProseRevisionData;
  "tangent.spreadsheet-review": TangentSpreadsheetReviewData;
  "tangent.synthesis-notes": TangentSynthesisNotesData;
  "tangent.triage": TangentTriageData;
  "tangent.whiteboard": TangentWhiteboardData;
  "tangent.wizard": TangentWizardData;
  "timeline-card": TimelineCardData;
}

/** Legacy component slugs from host-owned manifests; not runtime renderer bindings. */
/** Empty string means no legacy slug is declared, including wire-only core kinds. */
export const EnvelopeKindMap = {
  "approval-card": "",
  "artifact-mini": "",
  "chat-loop-terminated": "",
  "confirmation-card": "",
  "diff-card": "",
  "document-viewer": "",
  "elicitation-prompt": "",
  "error-report": "",
  "info-card": "",
  "list-card": "",
  "metric-card": "",
  "progress-card": "",
  "proposal-card": "",
  "report-card": "",
  "session-task": "",
  "subagent-spawn-approval": "",
  "table-card": "",
  "tangent.agent-turn": "",
  "tangent.app-board": "AppBoardView",
  "tangent.approval-queue": "ApprovalQueueView",
  "tangent.block-draft": "BlockDraftView",
  "tangent.dashboard": "DashboardView",
  "tangent.design-iteration": "DesignIterationView",
  "tangent.diff-review": "DiffReviewView",
  "tangent.doc-item": "",
  "tangent.external-review": "ExternalReviewView",
  "tangent.feedback": "FeedbackView",
  "tangent.file-picker": "FilePickerView",
  "tangent.form-collect": "FormCollectView",
  "tangent.hitl-item": "",
  "tangent.interview-question": "InterviewQuestionView",
  "tangent.output-render": "OutputRenderView",
  "tangent.progress-panel": "ProgressPanelView",
  "tangent.prose-revision": "ProseRevisionView",
  "tangent.spreadsheet-review": "SpreadsheetReviewView",
  "tangent.synthesis-notes": "SynthesisNotesView",
  "tangent.triage": "TriageView",
  "tangent.whiteboard": "WhiteboardView",
  "tangent.wizard": "WizardView",
  "timeline-card": "",
} as const satisfies Record<EnvelopeType, string>;

/** All registered envelope type strings, sorted by name. */
export const ENVELOPE_TYPES: readonly EnvelopeType[] = [
  "approval-card",
  "artifact-mini",
  "chat-loop-terminated",
  "confirmation-card",
  "diff-card",
  "document-viewer",
  "elicitation-prompt",
  "error-report",
  "info-card",
  "list-card",
  "metric-card",
  "progress-card",
  "proposal-card",
  "report-card",
  "session-task",
  "subagent-spawn-approval",
  "table-card",
  "tangent.agent-turn",
  "tangent.app-board",
  "tangent.approval-queue",
  "tangent.block-draft",
  "tangent.dashboard",
  "tangent.design-iteration",
  "tangent.diff-review",
  "tangent.doc-item",
  "tangent.external-review",
  "tangent.feedback",
  "tangent.file-picker",
  "tangent.form-collect",
  "tangent.hitl-item",
  "tangent.interview-question",
  "tangent.output-render",
  "tangent.progress-panel",
  "tangent.prose-revision",
  "tangent.spreadsheet-review",
  "tangent.synthesis-notes",
  "tangent.triage",
  "tangent.whiteboard",
  "tangent.wizard",
  "timeline-card",
] as const;

/** The @definition-source stamp above, as a value. */
export const DEFINITION_SOURCE_DIGEST = "sha256:7ea9b8d113d0314bae1375d65b0b3501c9ab1365fb167419f4a49274542a5dfb";

/** The Tangent release these types were generated against. */
export const DEFINITION_HOST_VERSION = "v0.18.0";

/** One manifest's contribution to the source digest. */
export interface DefinitionSourceEntry {
  kind: string;
  version: string;
  revision: number;
  manifestDigest: string;
  contractDigest: string;
}

/** Per-kind manifest identity, so drift can name the kind that moved. */
export const DEFINITION_SOURCE_ENTRIES: readonly DefinitionSourceEntry[] = [
  { kind: "tangent.agent-turn", version: "1.1", revision: 1, manifestDigest: "sha256:84dd525aa98edd364d67a04282d3b1e60fb21dfa26059fc1bf5ae062e9784e2b", contractDigest: "sha256:68b8f6e9050b46cb9df6dfeb1b6b4c360914699411f2c8df204f5cac5b9562d8" },
  { kind: "tangent.app-board", version: "0.3", revision: 1, manifestDigest: "sha256:21689b180436900fe77562eaf86f7c435cccc63235baacb0b4870b9d2e064bd5", contractDigest: "sha256:2d917db24c08e3cebc17ae46c2a47ecdee9f4fec519798ed79dec0f7b8dc0c8e" },
  { kind: "tangent.approval-queue", version: "0.8", revision: 1, manifestDigest: "sha256:40f8023c9c1efa1c83fdba1d80c1e5cab68e037f2296f1f1e5207358ad49bfef", contractDigest: "sha256:dbc7755ca2c7f84d78932ccd0eb4051508fc4fd9fda98e0b11d3c8e5e17504da" },
  { kind: "tangent.block-draft", version: "0.4", revision: 1, manifestDigest: "sha256:52ff9c8adfffd35ee43826ca5016dd798b1b9f19e8a8755fbd4d816f600218cd", contractDigest: "sha256:ecdfd3188a09c662143db4f18a4434603fbfc545b030a17630c5f6210501e485" },
  { kind: "tangent.dashboard", version: "0.12", revision: 1, manifestDigest: "sha256:24640bcaa0298cb9e89100002712fec80bc8360d5d6bd671beeaeb668425e999", contractDigest: "sha256:d98476dab3c21ac23b19c83852be3a3311f6a5130c39a379ddeca00cd058734c" },
  { kind: "tangent.design-iteration", version: "0.3", revision: 1, manifestDigest: "sha256:e98d28afa43b82490d6f2321add7ce8221830cb48f9c2051fa61b0ff9a7a2125", contractDigest: "sha256:bcd36530be7442b0eedb46c297d87e37f0dbb52b28390d25336f32536b98a6b4" },
  { kind: "tangent.diff-review", version: "0.9", revision: 1, manifestDigest: "sha256:fcdf9efa5c34f896efabcb951e2b0540efc02b147ce75215bfc9aad75d69942d", contractDigest: "sha256:6eee25c706f4eb1c5db3877b10765a55667e38a61986fa93666731b08f97f4d2" },
  { kind: "tangent.doc-item", version: "1.0", revision: 1, manifestDigest: "sha256:c2202c4352dc999ab9956ea9475d47bc945a405d5970256293c5282807ea0911", contractDigest: "sha256:d5f723e61d24c275a5296cf6d4dd6fa56c372ded33822b73143d893945d9728f" },
  { kind: "tangent.external-review", version: "0.1", revision: 1, manifestDigest: "sha256:682294cda3a2454b4ff908ca4d4c3a7b56e78e573cc025340d57157059c08f3b", contractDigest: "sha256:3ff91000a3126d71e4a9b559faf0f5bf850f6ad6443d54c0f78f65f260b06722" },
  { kind: "tangent.feedback", version: "0.3", revision: 1, manifestDigest: "sha256:c7501f59e1e40a5b1a938f668403837628fb333991b2db965df49823bfac7594", contractDigest: "sha256:1baace03c195ee236a0b9299d739794f70eaddcff5828bd0632ff7537facdb9f" },
  { kind: "tangent.file-picker", version: "0.10", revision: 1, manifestDigest: "sha256:4ef3363ccedea1c8b9afa8797d64233c4ed549c9e0a408ab08bde5dba1788939", contractDigest: "sha256:8498aef3e7f1c2c4c24f2174f5be1f16a5a1403865f76effb330f35b9f4d416a" },
  { kind: "tangent.form-collect", version: "0.7", revision: 1, manifestDigest: "sha256:a64ffd6d1de303b5d37176c36799ca81c0c4d8817d21020cb2890cb918c942d7", contractDigest: "sha256:9d100eaf194cc9de56d3b8fbb1af3c16fb273760c60b81d8a023ca46645f6ebe" },
  { kind: "tangent.hitl-item", version: "1.2", revision: 1, manifestDigest: "sha256:f81325d298d3770dac768ced6833cd8d7fa5fc0f3f6e236918ad3356a648f24a", contractDigest: "sha256:c2066ad73c1cfdd32e3bc2eaae2eea8cc5988105bee8e91aacecc11af489c3fd" },
  { kind: "tangent.interview-question", version: "0.4", revision: 1, manifestDigest: "sha256:051e73e63187b752716bc3a5cca51d44b2561c416cedc9fcf4c47dc8d5972730", contractDigest: "sha256:1cf9d36fb09b5f7c0be436c62bfb39a2405c09491bbdb5fe4726f14d116622bb" },
  { kind: "tangent.output-render", version: "0.4", revision: 1, manifestDigest: "sha256:f6107e6ab05224e4a24e31a40baa7fe2846c383a4b440b6c8420beed1b35f5c5", contractDigest: "sha256:b074ad702ae0de8a970a690d71f5b65cd9ae56944315fb70e2c318392e553840" },
  { kind: "tangent.progress-panel", version: "0.11", revision: 1, manifestDigest: "sha256:c9ce109e5a91be742395c83ab98f98ecadf33707cbdaf04a74bda29c0cac9ace", contractDigest: "sha256:6ac28915e72d3eaf60d849e50888c2d3b3f74f3a78f27698d874e2d766296b59" },
  { kind: "tangent.prose-revision", version: "0.4", revision: 1, manifestDigest: "sha256:98c214e0db86c7341ad04549590dc603e9c5fa9816cfe6e41cadf736968b9950", contractDigest: "sha256:0a1d37050af23dc3e6e61a6e6aea04dac6720b2644ba4d5f815f015347602f11" },
  { kind: "tangent.spreadsheet-review", version: "0.6", revision: 1, manifestDigest: "sha256:8bfd251bfdc6faa486ace91d642a937c46c2ae473701cfc80ae089331e30761d", contractDigest: "sha256:16282b257eff5d6c7e9ad27ca178980aa5ee7ddb67f1a2edba22af66b3c8c1d9" },
  { kind: "tangent.synthesis-notes", version: "0.4", revision: 1, manifestDigest: "sha256:a4c695e3c09557b9901839f9c1a317a0c630e33d09b143f8a0abb17b76deb09a", contractDigest: "sha256:9884a5ead5775b887689cf468454f8dca1eccc2c2eb0f9633640af85d0381342" },
  { kind: "tangent.triage", version: "0.2", revision: 1, manifestDigest: "sha256:b9513affa43bf0f88cd7c899731823332b43ffb2552ee1c221ea5e58b2751cf3", contractDigest: "sha256:8edeb85a3ff1b279b1f7dafa39b49c4a4f9465ff75400e105eaf31c1c8936368" },
  { kind: "tangent.whiteboard", version: "0.5", revision: 1, manifestDigest: "sha256:1fa1b5261c6255fd037c22582ab9229ccc09b4d6f7e9df83ad207b8e51ebb9bf", contractDigest: "sha256:69ffc26511c4df989fb8018feff2b7f488d72a35e1357178290e49ce0e5f7d9c" },
  { kind: "tangent.wizard", version: "0.13", revision: 1, manifestDigest: "sha256:920170905798d3372cf0f24453f50d9e844c6c4e51af8822e4f5a39d4707a776", contractDigest: "sha256:8b21894e32f6f8a70afff3c12be89dd8ad7219d7ee6d575f46adabd0c71477d8" },
] as const;

/**
 * Digest of a kind's request-schema `$defs` bundle, for the kinds that ship
 * one. A hand-written adapter over such a bundle stamps itself with this
 * value and asserts the match in a test — ADR 0003 §4.7's accepted floor
 * when full generation of that adapter is out of scope.
 */
export const DEFINITION_DEFS_DIGESTS: Readonly<Record<string, string>> = {
  "tangent.hitl-item": "sha256:971b6cfba540aa3f18951b3385c229011802655244d7be417136235f3a793c17",
};

/** Stable `$defs` entry points a kind declares, and what each is for. */
export const DEFINITION_NAMED_DEFINITIONS: Readonly<
  Record<string, Readonly<Record<string, string>>>
> = {
  "tangent.agent-turn": {
    "AgentTurnHandleV1": "Durable handle returned at turn enqueue.",
    "AgentTurnItemViewV1": "Operator-facing projection of one agent turn.",
    "AgentTurnReplyCommandV1": "Operator reply command.",
    "AgentTurnRequestV1": "Enqueue request root: one live agent turn.",
    "AgentTurnResponseV1": "Operator reply response payload.",
    "AgentTurnTerminalOutcomeV1": "Terminal outcome across resolved, canceled, expired, failed, and superseded.",
  },
  "tangent.doc-item": {
    "DocItemHandleV1": "Durable handle returned at doc enqueue.",
    "DocItemRequestV1": "Enqueue request root: one document for the operator's Docs inbox.",
    "DocItemResponseV1": "Operator acknowledgment payload, when the document requested one.",
    "DocItemViewV1": "Operator-facing projection of one doc item, including read state.",
  },
  "tangent.hitl-item": {
    "HITLAwaitCommandV1": "Wait for one item's terminal outcome without changing its lifecycle.",
    "HITLGetCommandV1": "Retrieve one item by durable handle.",
    "HITLIdempotencyConflictErrorV1": "Typed idempotency conflict for a reused key with different content.",
    "HITLItemHandleV1": "Durable handle returned at enqueue.",
    "HITLItemRequestV1": "Enqueue request root: one operator-owned approval or attention item.",
    "HITLItemViewV1": "Operator-facing projection of one item, including typed evidence.",
    "HITLResolutionCommandV1": "Operator resolution command, carrying the expected interaction and projection revisions.",
    "HITLResponseV1": "Resolution response payload: an approval decision or an attention acknowledgement.",
    "HITLRetrievalResultV1": "Retrieval projection distinguishing terminal delivery from acknowledgement.",
    "HITLStaleRevisionErrorV1": "Typed stale-revision conflict carrying the current revision.",
    "HITLTerminalOutcomeV1": "Immutable per-item terminal outcome across resolved, canceled, expired, failed, and superseded.",
    "HITLWithdrawCommandV1": "Caller withdrawal of an outstanding item.",
  },
};
