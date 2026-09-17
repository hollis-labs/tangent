/**
 * Client adapter for Tangent's durable Docs inbox API (/api/docs).
 * Contract version 1.0 (CW-20260917-0009).
 */

export type DocState =
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

export interface DocResolution {
  resolution_id: string;
  action: string;
  note?: string;
  resolved_at: string;
  resolved_by: string;
}

export interface DocItemView {
  contract_version: "1.0";
  item_id: string;
  agent_id: string;
  agent_label?: string;
  application_id?: string;
  title: string;
  summary?: string;
  content_markdown: string;
  requires_ack: boolean;
  tags?: string[];
  correlations?: Record<string, unknown>;
  state: DocState;
  queue_sequence: number;
  revision: number;
  created_at: string;
  updated_at: string;
  read_at?: string;
  resolution?: DocResolution;
}

export interface DocsInbox {
  contract_version: "1.0";
  surface_id: string;
  revision: string;
  synced_at: string;
  pending: DocItemView[];
  history: DocItemView[];
  total_pending: number;
  total_terminal: number;
}

export interface DocAcknowledgeInput {
  expected_revision: number;
  note?: string;
}

export interface DocArchiveInput {
  expected_revision: number;
  reason?: string;
}

export interface DocsErrorResponse {
  contract_version: string;
  code: string;
  message: string;
}

export class DocsAPIError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "DocsAPIError";
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
      const errorBody = (await response.json()) as DocsErrorResponse;
      if (errorBody?.code) code = errorBody.code;
      if (errorBody?.message) message = errorBody.message;
    } catch {
      // Body not JSON; keep fallback
    }
    throw new DocsAPIError(response.status, code, message);
  }

  return (await response.json()) as T;
}

export async function fetchDocsInbox(): Promise<DocsInbox> {
  return requestJSON<DocsInbox>("/api/docs");
}

export async function fetchDocItem(itemID: string): Promise<DocItemView> {
  return requestJSON<DocItemView>(`/api/docs/items/${encodeURIComponent(itemID)}`);
}

export async function markDocRead(itemID: string): Promise<DocItemView> {
  return requestJSON<DocItemView>(`/api/docs/items/${encodeURIComponent(itemID)}/read`, {
    method: "POST",
  });
}

export async function acknowledgeDoc(
  itemID: string,
  input: DocAcknowledgeInput,
): Promise<DocItemView> {
  return requestJSON<DocItemView>(`/api/docs/items/${encodeURIComponent(itemID)}/acknowledge`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}

export async function archiveDoc(itemID: string, input: DocArchiveInput): Promise<DocItemView> {
  return requestJSON<DocItemView>(`/api/docs/items/${encodeURIComponent(itemID)}/archive`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
