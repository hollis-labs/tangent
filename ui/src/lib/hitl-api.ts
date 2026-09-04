import type { HITLEvidence } from "@/components/hitl-evidence";

export type HITLState =
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

export interface HITLSource {
  application_id: string;
  application_label?: string;
  agent_id: string;
  agent_label?: string;
}

export interface HITLExternalRef {
  authority: string;
  id: string;
  revision?: string;
  label?: string;
}

export interface HITLCorrelations {
  project?: HITLExternalRef;
  task?: HITLExternalRef;
  session?: HITLExternalRef;
  additional?: Array<HITLExternalRef & { kind: string }>;
}

export interface HITLApprovalLabels {
  approve?: string;
  approve_with_note?: string;
  deny?: string;
  deny_with_note?: string;
}

export interface HITLAttentionLabels {
  acknowledge?: string;
  acknowledge_with_note?: string;
  reply?: string;
}

export interface HITLRequest {
  contract_version: "1.0";
  kind: "approval" | "attention";
  idempotency_key: string;
  title: string;
  summary: string;
  request: string;
  source: HITLSource;
  recommendation?: string;
  impact?: { approve: string; deny: string };
  action_labels?: HITLApprovalLabels | HITLAttentionLabels;
  correlations?: HITLCorrelations;
  details_markdown?: string;
  evidence?: HITLEvidence[];
  expires_at?: string;
}

export interface HITLResolution {
  resolution_id: string;
  response:
    | { kind: "approval"; decision: "approved" | "denied"; note?: string }
    | { kind: "attention"; decision: "acknowledged"; note?: string; reply?: string };
  resolved_at: string;
  interaction_revision: number;
  presented_projection_revision: number;
  participant: {
    principal_ref: string;
    authority: string;
    assurance: string;
  };
}

export interface HITLTerminalOutcome {
  contract_version: "1.0";
  state: HITLState;
  item_id: string;
  interaction_revision: number;
  resolution?: HITLResolution;
  cause?: string;
  reason?: string;
  policy_ref?: string;
  error_code?: string;
  message?: string;
  replacement_item_id?: string;
  terminated_at?: string;
}

export interface HITLOperatorItem {
  contract_version: "1.0";
  surface_id: string;
  item_id: string;
  state: HITLState;
  revision: number;
  queue_sequence: number;
  queue_position: number | null;
  request_snapshot: HITLRequest;
  enqueued_at: string;
  updated_at: string;
  presented_projection_revision?: number;
  terminal_outcome?: HITLTerminalOutcome;
}

export interface HITLInbox {
  contract_version: "1.0";
  surface_id: string;
  revision: string;
  synced_at: string;
  pending: HITLOperatorItem[];
  history: HITLOperatorItem[];
}

export interface HITLAPIErrorBody {
  contract_version?: string;
  code?: string;
  message?: string;
  operation?: string;
  item_id?: string;
  revision_kind?: string;
  expected_revision?: number;
  actual_revision?: number;
  current_state?: HITLState;
  terminal_outcome?: HITLTerminalOutcome;
}

export class HITLAPIError extends Error {
  readonly status: number;
  readonly detail: HITLAPIErrorBody;

  constructor(status: number, detail: HITLAPIErrorBody) {
    super(detail.message || `HITL request failed with HTTP ${status}`);
    this.name = "HITLAPIError";
    this.status = status;
    this.detail = detail;
  }
}

export async function fetchHITLInbox(signal?: AbortSignal): Promise<HITLInbox> {
  return hitlRequest<HITLInbox>("/api/hitl", { signal });
}

export async function fetchHITLItem(
  itemID: string,
  signal?: AbortSignal,
): Promise<HITLOperatorItem> {
  return hitlRequest<HITLOperatorItem>(`/api/hitl/items/${encodeURIComponent(itemID)}`, { signal });
}

export async function presentHITLItem(
  item: HITLOperatorItem,
  connectionID: string,
): Promise<HITLOperatorItem> {
  return hitlRequest<HITLOperatorItem>(
    `/api/hitl/items/${encodeURIComponent(item.item_id)}/present`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        expected_revision: item.revision,
        presented_projection_revision: item.revision,
        connection_id: connectionID,
      }),
    },
  );
}

export async function resolveHITLApproval(
  item: HITLOperatorItem,
  decision: "approved" | "denied",
  note?: string,
): Promise<HITLTerminalOutcome> {
  if (!item.presented_projection_revision) {
    throw new Error("This item has not been presented yet. Resynchronize before deciding.");
  }
  return hitlRequest<HITLTerminalOutcome>(
    `/api/hitl/items/${encodeURIComponent(item.item_id)}/resolve`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        expected_revision: item.revision,
        presented_projection_revision: item.presented_projection_revision,
        response: {
          kind: "approval",
          decision,
          ...(note ? { note } : {}),
        },
      }),
    },
  );
}

export async function resolveHITLAttention(
  item: HITLOperatorItem,
  response?: { note?: string; reply?: string },
): Promise<HITLTerminalOutcome> {
  if (!item.presented_projection_revision) {
    throw new Error("This item has not been presented yet. Resynchronize before acknowledging.");
  }
  return hitlRequest<HITLTerminalOutcome>(
    `/api/hitl/items/${encodeURIComponent(item.item_id)}/resolve`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        expected_revision: item.revision,
        presented_projection_revision: item.presented_projection_revision,
        response: {
          kind: "attention",
          decision: "acknowledged",
          ...response,
        },
      }),
    },
  );
}

export function getHITLConnectionID(): string {
  const key = "tangent:hitl:connection-id";
  try {
    const existing = window.sessionStorage.getItem(key);
    if (existing) return existing;
  } catch {
    // Privacy modes can disable sessionStorage. The durable interaction CAS,
    // not this diagnostic connection label, remains the authority.
  }
  const randomID =
    globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  const id = `hitl-browser-${randomID}`;
  try {
    window.sessionStorage.setItem(key, id);
  } catch {
    // Keep using the in-memory identifier for this presentation request.
  }
  return id;
}

async function hitlRequest<T>(path: string, init: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: { Accept: "application/json", ...init.headers },
  });
  const body = (await response.json().catch(() => ({}))) as T | HITLAPIErrorBody;
  if (!response.ok) {
    throw new HITLAPIError(response.status, body as HITLAPIErrorBody);
  }
  return body as T;
}
