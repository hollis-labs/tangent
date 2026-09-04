// AUTO-GENERATED FILE — DO NOT EDIT MANUALLY
// Generated from go-envelopes v0.1.0 — do not edit.
// Run `make generate-envelopes` to regenerate.
//
// Coverage: 44 registered kinds — 26 go-envelopes core,
// 18 Tangent-owned (internal/envelope/extensions).
//
// Sources of truth: github.com/hollis-labs/go-envelopes (core catalog)
// and internal/envelope/extensions (Tangent kinds).
// Pipeline: cmd/tangent-dump-types -> scripts/generate-envelope-types.mjs

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

/** Envelope data for "artifact-mini" — Compact downloadable-artifact card surfaced in the bottom chat drawer. Renders name, mime, size, Download CTA, and a Dismiss button. Lifetime is transient — auto-closes after the user downloads or dismisses. Use for ephemeral 'your file is ready' surfaces. Durable session-wide artifact list lives in the right-rail Artifacts panel. (C2, CW-20260428-0013) */
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

/** Envelope data for "chat-loop-budget-soft-warning" — Emitted once per generation when the chat loop crosses the strategy planner's soft max_turns budget without terminating. Signal-only — the agent continues running until it self-terminates via end_turn or trips a hard circuit-breaker (runaway_tool_failures, hard_ceiling, idle_timeout, retry_budget_exhausted). Devmode-gated on the SSE stream; always logged at INFO. CW-20260504-0001. */
export interface ChatLoopBudgetSoftWarningData {
  /** Loop iteration at which the budget was crossed. */
  iteration: number;
  /** The soft budget the loop just crossed (from the strategy planner or the default fallback). */
  max_turns: number;
  /** Human-readable explanation of the signal. */
  reason: string;
  /** ISO 8601 timestamp of the warning emission. */
  timestamp: string;
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

/** Envelope data for "confirmation-card" — A confirmation dialog with risk-level-styled buttons. */
export interface ConfirmationCardData {
  /** Label for the cancel button. Defaults to Cancel. */
  cancel_label?: string;
  /** Label for the confirm button. Defaults to Confirm. */
  confirm_label?: string;
  /** Confirmation message body. */
  message: string;
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
  /** Where the elicitation was issued: server (Nanite's own tool) or client (external MCP server). */
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

/** Envelope data for "list-card" — An ordered or unordered list with optional item actions. */
export interface ListCardData {
  /** List items. */
  items: { action?: { label: string; type: string }; description?: string; icon?: string; label: string }[];
  /** Whether to show numbered list. Defaults to false. */
  ordered?: boolean;
  /** Optional list title. */
  title?: string;
}

/** Envelope data for "message-handoff" (no schema registered). */
export type MessageHandoffData = Record<string, unknown>;

/** Envelope data for "message-notification" (no schema registered). */
export type MessageNotificationData = Record<string, unknown>;

/** Envelope data for "message-reply" (no schema registered). */
export type MessageReplyData = Record<string, unknown>;

/** Envelope data for "message-request" (no schema registered). */
export type MessageRequestData = Record<string, unknown>;

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

/** Envelope data for "plan-review" (no schema registered). */
export type PlanReviewData = Record<string, unknown>;

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

/** Envelope data for "question-form" — An interactive form with multiple question fields. */
export interface QuestionFormData {
  /** Array of questions. */
  questions: { default?: string; options?: string[]; prompt: string; required: boolean; type: "text" | "textarea" | "select" | "radio" | "checkbox" }[];
}

/** Envelope data for "report-card" — A metrics report with summary and action buttons. */
export interface ReportCardData {
  /** Action buttons shown at the bottom. */
  actions?: { action: string; id?: string; label: string }[];
  /** ISO 8601 timestamp of report generation. */
  generated_at?: string;
  /** Array of metric entries. */
  metrics: { color?: string; label: string; percent?: number; value: string }[];
  /** Markdown summary text shown below metrics. */
  summary?: string;
  /** Report title. */
  title: string;
}

/** Envelope data for "session-task" — A session-scoped task card with status transitions. */
export interface SessionTaskData {
  /** Optional description shown below the task title. */
  description?: string;
  /** Current task status. */
  status: "pending" | "in_progress" | "completed" | "failed" | "cancelled";
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

/** Envelope data for "table-card" — A sortable data table. */
export interface TableCardData {
  /** Optional table caption. */
  caption?: string;
  /** Column definitions. */
  columns: { key: string; label: string; sortable?: boolean }[];
  /** Row data. Keys must match column keys. */
  rows: Record<string, unknown>[];
  /** Optional table title. */
  title?: string;
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

/** Envelope data for "todo-list" (no schema registered). */
export type TodoListData = Record<string, unknown>;

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
export type ChatLoopBudgetSoftWarningEnvelope = EnvelopeBase<"chat-loop-budget-soft-warning", ChatLoopBudgetSoftWarningData>;
export type ChatLoopTerminatedEnvelope = EnvelopeBase<"chat-loop-terminated", ChatLoopTerminatedData>;
export type ConfirmationCardEnvelope = EnvelopeBase<"confirmation-card", ConfirmationCardData>;
export type DiffCardEnvelope = EnvelopeBase<"diff-card", DiffCardData>;
export type DocumentViewerEnvelope = EnvelopeBase<"document-viewer", DocumentViewerData>;
export type ElicitationPromptEnvelope = EnvelopeBase<"elicitation-prompt", ElicitationPromptData>;
export type ErrorReportEnvelope = EnvelopeBase<"error-report", ErrorReportData>;
export type InfoCardEnvelope = EnvelopeBase<"info-card", InfoCardData>;
export type ListCardEnvelope = EnvelopeBase<"list-card", ListCardData>;
export type MessageHandoffEnvelope = EnvelopeBase<"message-handoff", MessageHandoffData>;
export type MessageNotificationEnvelope = EnvelopeBase<"message-notification", MessageNotificationData>;
export type MessageReplyEnvelope = EnvelopeBase<"message-reply", MessageReplyData>;
export type MessageRequestEnvelope = EnvelopeBase<"message-request", MessageRequestData>;
export type MetricCardEnvelope = EnvelopeBase<"metric-card", MetricCardData>;
export type PlanReviewEnvelope = EnvelopeBase<"plan-review", PlanReviewData>;
export type ProgressCardEnvelope = EnvelopeBase<"progress-card", ProgressCardData>;
export type ProposalCardEnvelope = EnvelopeBase<"proposal-card", ProposalCardData>;
export type QuestionFormEnvelope = EnvelopeBase<"question-form", QuestionFormData>;
export type ReportCardEnvelope = EnvelopeBase<"report-card", ReportCardData>;
export type SessionTaskEnvelope = EnvelopeBase<"session-task", SessionTaskData>;
export type SubagentSpawnApprovalEnvelope = EnvelopeBase<"subagent-spawn-approval", SubagentSpawnApprovalData>;
export type TableCardEnvelope = EnvelopeBase<"table-card", TableCardData>;
export type TangentApprovalQueueEnvelope = EnvelopeBase<"tangent.approval-queue", TangentApprovalQueueData>;
export type TangentBlockDraftEnvelope = EnvelopeBase<"tangent.block-draft", TangentBlockDraftData>;
export type TangentDashboardEnvelope = EnvelopeBase<"tangent.dashboard", TangentDashboardData>;
export type TangentDesignIterationEnvelope = EnvelopeBase<"tangent.design-iteration", TangentDesignIterationData>;
export type TangentDiffReviewEnvelope = EnvelopeBase<"tangent.diff-review", TangentDiffReviewData>;
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
export type TodoListEnvelope = EnvelopeBase<"todo-list", TodoListData>;

/** Discriminated union of every envelope type Tangent has registered. */
export type Envelope =
  | ApprovalCardEnvelope
  | ArtifactMiniEnvelope
  | ChatLoopBudgetSoftWarningEnvelope
  | ChatLoopTerminatedEnvelope
  | ConfirmationCardEnvelope
  | DiffCardEnvelope
  | DocumentViewerEnvelope
  | ElicitationPromptEnvelope
  | ErrorReportEnvelope
  | InfoCardEnvelope
  | ListCardEnvelope
  | MessageHandoffEnvelope
  | MessageNotificationEnvelope
  | MessageReplyEnvelope
  | MessageRequestEnvelope
  | MetricCardEnvelope
  | PlanReviewEnvelope
  | ProgressCardEnvelope
  | ProposalCardEnvelope
  | QuestionFormEnvelope
  | ReportCardEnvelope
  | SessionTaskEnvelope
  | SubagentSpawnApprovalEnvelope
  | TableCardEnvelope
  | TangentApprovalQueueEnvelope
  | TangentBlockDraftEnvelope
  | TangentDashboardEnvelope
  | TangentDesignIterationEnvelope
  | TangentDiffReviewEnvelope
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
  | TimelineCardEnvelope
  | TodoListEnvelope;

/** String literal union of every registered envelope type name. */
export type EnvelopeType =
  | "approval-card"
  | "artifact-mini"
  | "chat-loop-budget-soft-warning"
  | "chat-loop-terminated"
  | "confirmation-card"
  | "diff-card"
  | "document-viewer"
  | "elicitation-prompt"
  | "error-report"
  | "info-card"
  | "list-card"
  | "message-handoff"
  | "message-notification"
  | "message-reply"
  | "message-request"
  | "metric-card"
  | "plan-review"
  | "progress-card"
  | "proposal-card"
  | "question-form"
  | "report-card"
  | "session-task"
  | "subagent-spawn-approval"
  | "table-card"
  | "tangent.approval-queue"
  | "tangent.block-draft"
  | "tangent.dashboard"
  | "tangent.design-iteration"
  | "tangent.diff-review"
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
  | "timeline-card"
  | "todo-list";

/** Maps each envelope type string to its data interface. */
export interface EnvelopeDataMap {
  "approval-card": ApprovalCardData;
  "artifact-mini": ArtifactMiniData;
  "chat-loop-budget-soft-warning": ChatLoopBudgetSoftWarningData;
  "chat-loop-terminated": ChatLoopTerminatedData;
  "confirmation-card": ConfirmationCardData;
  "diff-card": DiffCardData;
  "document-viewer": DocumentViewerData;
  "elicitation-prompt": ElicitationPromptData;
  "error-report": ErrorReportData;
  "info-card": InfoCardData;
  "list-card": ListCardData;
  "message-handoff": MessageHandoffData;
  "message-notification": MessageNotificationData;
  "message-reply": MessageReplyData;
  "message-request": MessageRequestData;
  "metric-card": MetricCardData;
  "plan-review": PlanReviewData;
  "progress-card": ProgressCardData;
  "proposal-card": ProposalCardData;
  "question-form": QuestionFormData;
  "report-card": ReportCardData;
  "session-task": SessionTaskData;
  "subagent-spawn-approval": SubagentSpawnApprovalData;
  "table-card": TableCardData;
  "tangent.approval-queue": TangentApprovalQueueData;
  "tangent.block-draft": TangentBlockDraftData;
  "tangent.dashboard": TangentDashboardData;
  "tangent.design-iteration": TangentDesignIterationData;
  "tangent.diff-review": TangentDiffReviewData;
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
  "todo-list": TodoListData;
}

/** Maps envelope type -> component slug declared in the kind's manifest. */
/** Empty string means the type has no frontend component yet. */
export const EnvelopeKindMap = {
  "approval-card": "components/chat/envelopes/ApprovalCard",
  "artifact-mini": "components/chat/envelopes/ArtifactMiniCard",
  "chat-loop-budget-soft-warning": "",
  "chat-loop-terminated": "components/chat/envelopes/ChatLoopTerminatedCard",
  "confirmation-card": "components/chat/envelopes/primitives/ConfirmationCard",
  "diff-card": "components/chat/envelopes/primitives/DiffCard",
  "document-viewer": "components/chat/envelopes/DocumentViewerCard",
  "elicitation-prompt": "components/chat/envelopes/ElicitationPromptCard",
  "error-report": "components/chat/envelopes/ErrorCard",
  "info-card": "components/chat/envelopes/primitives/InfoCard",
  "list-card": "components/chat/envelopes/primitives/ListCard",
  "message-handoff": "",
  "message-notification": "",
  "message-reply": "",
  "message-request": "",
  "metric-card": "components/chat/envelopes/primitives/MetricCard",
  "plan-review": "components/chat/envelopes/PlanReviewCard",
  "progress-card": "components/chat/envelopes/primitives/ProgressCard",
  "proposal-card": "components/chat/envelopes/ProposalCard",
  "question-form": "",
  "report-card": "components/chat/envelopes/ReportCard",
  "session-task": "",
  "subagent-spawn-approval": "components/chat/envelopes/SubagentSpawnApprovalCard",
  "table-card": "components/chat/envelopes/primitives/TableCard",
  "tangent.approval-queue": "ApprovalQueueView",
  "tangent.block-draft": "BlockDraftView",
  "tangent.dashboard": "DashboardView",
  "tangent.design-iteration": "DesignIterationView",
  "tangent.diff-review": "DiffReviewView",
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
  "timeline-card": "components/chat/envelopes/primitives/TimelineCard",
  "todo-list": "components/chat/envelopes/TodoListCard",
} as const satisfies Record<EnvelopeType, string>;

/** All registered envelope type strings, sorted by name. */
export const ENVELOPE_TYPES: readonly EnvelopeType[] = [
  "approval-card",
  "artifact-mini",
  "chat-loop-budget-soft-warning",
  "chat-loop-terminated",
  "confirmation-card",
  "diff-card",
  "document-viewer",
  "elicitation-prompt",
  "error-report",
  "info-card",
  "list-card",
  "message-handoff",
  "message-notification",
  "message-reply",
  "message-request",
  "metric-card",
  "plan-review",
  "progress-card",
  "proposal-card",
  "question-form",
  "report-card",
  "session-task",
  "subagent-spawn-approval",
  "table-card",
  "tangent.approval-queue",
  "tangent.block-draft",
  "tangent.dashboard",
  "tangent.design-iteration",
  "tangent.diff-review",
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
  "todo-list",
] as const;
