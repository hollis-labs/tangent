/**
 * Client adapter for Tangent's durable artifact inbox API (/api/artifacts).
 * Contract version 1.0 (ADR 0015, CW-20261024-0061).
 */

import { fetchWithElevation } from "@/lib/operator-elevation";

export type ArtifactType =
  | "html"
  | "image"
  | "markdown"
  | "excalidraw"
  | "flow"
  | "mermaid"
  | "d2"
  | "vega-lite"
  | "pdf"
  | "video"
  | "figma";

export type ArtifactState =
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

export interface ArtifactRevision {
  revision: number;
  filename: string;
  media_type: string;
  size_bytes: number;
  sha256: string;
  note?: string;
  /** A server-drawn picture of this revision exists (see artifactRenderedURL). */
  rendered?: boolean;
  created_at: string;
}

export interface ArtifactMessage {
  seq: number;
  author: "operator" | "agent";
  kind: "message" | "changes_requested" | "approved";
  body: string;
  revision?: number;
  created_at: string;
}

export interface ArtifactResolution {
  resolution_id: string;
  action: string;
  note?: string;
  revision?: number;
  resolved_at: string;
  resolved_by: string;
}

export interface ArtifactView {
  contract_version: "1.0";
  item_id: string;
  agent_id: string;
  agent_label?: string;
  application_id?: string;
  title: string;
  summary?: string;
  type: ArtifactType;
  requires_signoff: boolean;
  tags?: string[];
  correlations?: Record<string, unknown>;
  state: ArtifactState;
  queue_sequence: number;
  revision: number;
  created_at: string;
  updated_at: string;
  read_at?: string;
  resolution?: ArtifactResolution;
  revisions: ArtifactRevision[];
  latest_revision: number;
  messages: ArtifactMessage[];
  inbox_path: string;
}

export interface ArtifactErrorResponse {
  contract_version: string;
  code: string;
  message: string;
}

export class ArtifactAPIError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "ArtifactAPIError";
    this.status = status;
    this.code = code;
  }
}

const TERMINAL: ArtifactState[] = ["resolved", "canceled", "expired", "failed", "superseded"];

export function isArtifactTerminal(state: ArtifactState): boolean {
  return TERMINAL.includes(state);
}

async function requestJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetchWithElevation(url, {
    ...init,
    headers: { Accept: "application/json", ...(init?.headers ?? {}) },
  });
  if (!response.ok) {
    let code = "http_error";
    let message = `Request failed: ${response.status} ${response.statusText}`;
    try {
      const body = (await response.json()) as ArtifactErrorResponse;
      if (body?.code) code = body.code;
      if (body?.message) message = body.message;
    } catch {
      // Body not JSON; keep fallback
    }
    throw new ArtifactAPIError(response.status, code, message);
  }
  return (await response.json()) as T;
}

const itemURL = (itemID: string) => `/api/artifacts/items/${encodeURIComponent(itemID)}`;

/** The URL a revision's bytes are served from. `download` asks for an attachment. */
export function artifactContentURL(itemID: string, revision: number, download = false): string {
  return `${itemURL(itemID)}/revisions/${revision}/content${download ? "?download=1" : ""}`;
}

/** The URL of the picture the server drew from a revision, for types it draws (D2). */
export function artifactRenderedURL(itemID: string, revision: number): string {
  return `${itemURL(itemID)}/revisions/${revision}/rendered`;
}

/** The absolute link that opens this artifact in the Inbox. */
export function artifactDirectLink(itemID: string): string {
  return `${window.location.origin}/inbox/items/${encodeURIComponent(itemID)}`;
}

export async function fetchArtifactItem(itemID: string): Promise<ArtifactView> {
  return requestJSON<ArtifactView>(itemURL(itemID));
}

export async function markArtifactRead(itemID: string): Promise<ArtifactView> {
  return requestJSON<ArtifactView>(`${itemURL(itemID)}/read`, { method: "POST" });
}

export async function sendArtifactMessage(
  itemID: string,
  input: { body: string; kind?: "message" | "changes_requested" },
): Promise<ArtifactView> {
  return requestJSON<ArtifactView>(`${itemURL(itemID)}/messages`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}

export async function signoffArtifact(
  itemID: string,
  input: { expected_revision: number; note?: string },
): Promise<ArtifactView> {
  return requestJSON<ArtifactView>(`${itemURL(itemID)}/signoff`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}

export async function archiveArtifact(
  itemID: string,
  input: { expected_revision: number; reason?: string },
): Promise<ArtifactView> {
  return requestJSON<ArtifactView>(`${itemURL(itemID)}/archive`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}

export async function deleteArtifact(
  itemID: string,
  input: { expected_revision: number },
): Promise<ArtifactView> {
  return requestJSON<ArtifactView>(itemURL(itemID), {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}

/** Reads one revision as text, for the types that are text. */
export async function fetchArtifactText(itemID: string, revision: number): Promise<string> {
  const response = await fetch(artifactContentURL(itemID, revision), {
    credentials: "same-origin",
  });
  if (!response.ok) {
    throw new ArtifactAPIError(
      response.status,
      "http_error",
      `Could not load revision ${revision}`,
    );
  }
  return response.text();
}
