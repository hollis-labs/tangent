// AUTO-GENERATED FILE — DO NOT EDIT MANUALLY
// Generated from go-envelopes v0.1.0 — do not edit.
// Run `make generate-envelopes` to regenerate.
//
// Source of truth: github.com/hollis-labs/go-envelopes
// Pipeline: cmd/tangent-dump-types -> scripts/generate-envelope-types.mjs

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
export type TimelineCardEnvelope = EnvelopeBase<"timeline-card", TimelineCardData>;
export type TodoListEnvelope = EnvelopeBase<"todo-list", TodoListData>;

/** Discriminated union of every envelope type known to go-envelopes core. */
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
  "timeline-card": TimelineCardData;
  "todo-list": TodoListData;
}

/** Maps envelope type -> component import slug from the YAML manifest. */
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
  "timeline-card",
  "todo-list",
] as const;
