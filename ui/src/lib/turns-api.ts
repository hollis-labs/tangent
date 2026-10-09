/**
 * Client adapter for Tangent's durable Agent Turns FIFO inbox API (/api/turns).
 * Contracts 1.0, 1.1 and 1.2 (CW-20260913-0019).
 */

export type TurnKind = "question" | "approval" | "checkpoint" | "failure" | "terminal";

export type TurnState =
  | "submitted"
  | "validated"
  | "staged"
  | "presented"
  | "in_progress"
  | "resolved"
  | "canceled"
  | "expired"
  | "failed"
  | "superseded";

export type TurnDeliveryState =
  | "queued"
  | "delivering"
  | "delivered"
  | "acknowledged"
  | "retryable_failure"
  | "terminal_failure";

export interface TurnOption {
  label: string;
  value: string;
  description?: string;
  recommended?: boolean;
}

export interface TurnResolution {
  interrupt?: boolean;
  resolution_id: string;
  action: string;
  response_text?: string;
  selected_option?: string;
  note?: string;
  resolved_at: string;
  resolved_by: string;
}

export interface TurnItemView {
  contract_version: "1.0" | "1.1" | "1.2";
  item_id: string;
  turn_id?: string;
  session_id?: string;
  agent_id: string;
  agent_label?: string;
  application_id?: string;
  kind: TurnKind;
  title: string;
  summary?: string;
  content: string;
  annotations?: TurnAnnotation[];
  stage_trace?: TurnStageTrace[];
  source_message?: TurnSourceMessage;
  replyable?: boolean;
  options?: TurnOption[];
  correlations?: Record<string, unknown>;
  state: TurnState;
  queue_sequence: number;
  queue_position?: number;
  revision: number;
  created_at: string;
  updated_at: string;
  expires_at?: string;
  resolution?: TurnResolution;
  delivery_state: TurnDeliveryState;
}

export interface TurnsInbox {
  contract_version: "1.0" | "1.1" | "1.2";
  surface_id: string;
  revision: string;
  synced_at: string;
  pending: TurnItemView[];
  history: TurnItemView[];
  total_pending: number;
  total_terminal: number;
}

export interface TurnReplyInput {
  interrupt?: boolean;
  expected_revision: number;
  action: string;
  response_text?: string;
  selected_option?: string;
  note?: string;
}

export interface TurnDismissInput {
  expected_revision: number;
  reason?: string;
}

export interface TurnsErrorResponse {
  contract_version: string;
  code: string;
  message: string;
}

export class TurnsAPIError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "TurnsAPIError";
    this.status = status;
    this.code = code;
  }
}

async function requestJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, {
    ...init,
    headers: {
      Accept: "application/json",
      ...(init?.headers ?? {}),
    },
  });

  if (!response.ok) {
    let code = "http_error";
    let message = `Request failed: ${response.status} ${response.statusText}`;
    try {
      const errorBody = (await response.json()) as TurnsErrorResponse;
      if (errorBody?.code) code = errorBody.code;
      if (errorBody?.message) message = errorBody.message;
    } catch {
      // Body not JSON; keep fallback
    }
    throw new TurnsAPIError(response.status, code, message);
  }

  return (await response.json()) as T;
}

export async function fetchTurnsInbox(): Promise<TurnsInbox> {
  return requestJSON<TurnsInbox>("/api/turns");
}

export async function fetchTurnItem(itemID: string): Promise<TurnItemView> {
  return requestJSON<TurnItemView>(`/api/turns/items/${encodeURIComponent(itemID)}`);
}

export async function replyTurn(itemID: string, input: TurnReplyInput): Promise<TurnItemView> {
  return requestJSON<TurnItemView>(`/api/turns/items/${encodeURIComponent(itemID)}/reply`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(input),
  });
}

export async function dismissTurn(itemID: string, input: TurnDismissInput): Promise<TurnItemView> {
  return requestJSON<TurnItemView>(`/api/turns/items/${encodeURIComponent(itemID)}/dismiss`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(input),
  });
}

export async function ackTurn(itemID: string, replyID?: string): Promise<{ status: string }> {
  return requestJSON<{ status: string }>(`/api/turns/items/${encodeURIComponent(itemID)}/ack`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ reply_id: replyID }),
  });
}

export async function fetchSessionReplies(
  sessionID: string,
): Promise<{ contract_version: string; session_id: string; replies: TurnItemView[] }> {
  return requestJSON<{ contract_version: string; session_id: string; replies: TurnItemView[] }>(
    `/api/turns/sessions/${encodeURIComponent(sessionID)}/replies`,
  );
}

export interface TurnAnnotation {
  schema_version: 1;
  stage_id: string;
  stage_version: string;
  kind: "summary";
  summary: { text: string };
}
export interface TurnStageTrace {
  stage_id: string;
  stage_version: string;
  outcome: "passed" | "failed" | "timed_out";
  duration_ms: number;
  failure_code?:
    | "stage_error"
    | "stage_timeout"
    | "stage_panic"
    | "stage_refused"
    | "invalid_output"
    | "annotation_limit";
}
export interface TurnSourceMessage {
  schema_version: 1;
  origin: "routed" | "publication";
  endpoint_ref: string;
  channel: string;
  message_id: string;
  sequence: number;
  sender_urn: string;
  output_id?: string;
  attribution?: {
    kind?: "final" | "question" | "approval" | "failure";
    logical_agent_id?: string;
    project_id?: string;
    workstream_id?: string;
    launch_id?: string;
    launch_display_name?: string;
    runtime?: string;
    stop_reason?: string;
    confidence?: "exact" | "heuristic" | "none" | "unknown";
  };
}
export function canReplyTurn(item: TurnItemView): boolean {
  return item.replyable !== false && item.source_message?.origin !== "publication";
}

/** Plugin-owned, participant-guarded metadata. Acceptance is not delivery. */
export interface ReplyDeliveryView {
  schema_version: 1;
  item_id: string;
  version: number;
  resolution_id?: string;
  action_id?: string;
  state:
    | "not_submitted"
    | "nonreplyable"
    | "prepared"
    | "accepted"
    | "pending"
    | "queued"
    | "delivering"
    | "delivered"
    | "undeliverable"
    | "refused"
    | "unknown";
  reason?: string;
  failure_code?: string;
  failure_status?: number;
  accepted: boolean;
  acknowledged: boolean;
  reply_id?: string;
  original_session_id?: string;
  target_session_id?: string;
  delivered_to_session_id?: string;
  attempts: number;
  reply_supported: boolean;
  interrupt_supported: boolean;
  interrupt_requested: boolean;
  retry_allowed: boolean;
}

export async function fetchReplyDelivery(
  itemID: string,
  signal?: AbortSignal,
): Promise<ReplyDeliveryView> {
  const view = await requestJSON<ReplyDeliveryView>(
    `/api/plugins/messaging/delivery?item_id=${encodeURIComponent(itemID)}`,
    { signal },
  );
  return validateReplyDelivery(view, itemID);
}

function validateReplyDelivery(view: ReplyDeliveryView, itemID: string): ReplyDeliveryView {
  if (
    view?.schema_version !== 1 ||
    view.item_id !== itemID ||
    !Number.isSafeInteger(view.version) ||
    view.version < 0 ||
    !Number.isSafeInteger(view.attempts) ||
    view.attempts < 0 ||
    [
      view.accepted,
      view.acknowledged,
      view.reply_supported,
      view.interrupt_supported,
      view.interrupt_requested,
      view.retry_allowed,
    ].some((flag) => typeof flag !== "boolean") ||
    ![
      "not_submitted",
      "nonreplyable",
      "prepared",
      "accepted",
      "pending",
      "queued",
      "delivering",
      "delivered",
      "undeliverable",
      "refused",
      "unknown",
    ].includes(view.state)
  ) {
    throw new Error("Reply delivery projection does not match this item");
  }
  return view;
}

export async function retryReplyDelivery(
  itemID: string,
  expectedVersion: number,
  actionID: string,
  interrupt: boolean,
): Promise<ReplyDeliveryView> {
  const view = await requestJSON<ReplyDeliveryView>("/api/plugins/messaging/retry", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      item_id: itemID,
      expected_version: expectedVersion,
      action_id: actionID,
      interrupt,
    }),
    signal: AbortSignal.timeout(8000),
  });
  return validateReplyDelivery(view, itemID);
}
