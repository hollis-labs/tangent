import { fetchWithElevation } from "@/lib/operator-elevation";

export type InboxCategory = "approval" | "document" | "artifact" | "turn" | "workflow";

export interface InboxEntry {
  sequence: number;
  interaction: {
    interaction_id: string;
    definition_binding: { kind: string };
    request_snapshot: Record<string, unknown>;
    caller_principal_ref?: string;
    caller_scope: string;
    state: string;
    revision: number;
    created_at: string;
    updated_at: string;
    legacy_room_id?: string;
    legacy_envelope_id?: string;
    terminal_reason?: string;
    terminal_error_code?: string;
  };
  resolution?: {
    resolution_id: string;
    response_payload: unknown;
    recorded_at: string;
  };
}

export function categoryOf(entry: InboxEntry): InboxCategory {
  switch (entry.interaction.definition_binding.kind) {
    case "tangent.hitl-item":
      return "approval";
    case "tangent.doc-item":
      return "document";
    case "tangent.artifact-item":
      return "artifact";
    case "tangent.agent-turn":
      return "turn";
    default:
      return "workflow";
  }
}

export function isTerminal(entry: InboxEntry): boolean {
  return ["resolved", "canceled", "expired", "failed", "superseded"].includes(
    entry.interaction.state,
  );
}

export function entryTitle(entry: InboxEntry): string {
  const request = entry.interaction.request_snapshot;
  for (const key of ["title", "queue_title", "prompt", "question", "name"]) {
    if (typeof request[key] === "string" && request[key]) return request[key] as string;
  }
  return entry.interaction.definition_binding.kind.replace(/^tangent\./, "").replaceAll("-", " ");
}

export function entrySource(entry: InboxEntry): string {
  const source = entry.interaction.request_snapshot.source;
  if (source && typeof source === "object") {
    const value = source as Record<string, unknown>;
    for (const key of ["agent_label", "agent_id", "application_label", "application_id"]) {
      if (typeof value[key] === "string" && value[key]) return value[key] as string;
    }
  }
  return entry.interaction.caller_principal_ref || entry.interaction.caller_scope;
}

/**
 * The list read is a SUMMARY: each entry carries only what a row renders (a bounded title
 * and source in `request_snapshot`, no resolution payload). Open an item with
 * `fetchInboxItem`, and search content with `searchInbox`.
 */

/** Remembers the last list and its ETag so an unchanged poll costs a 304, not a re-parse. */
export interface InboxCache {
  etag?: string;
  items?: InboxEntry[];
}

/**
 * With a cache, an unchanged Inbox returns the SAME array the cache already holds, so
 * a React `setState` with it is a no-op and no row re-renders.
 */
export async function fetchInbox(signal?: AbortSignal, cache?: InboxCache): Promise<InboxEntry[]> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (cache?.etag && cache.items) headers["If-None-Match"] = cache.etag;
  const response = await fetch("/api/inbox?view=summary", {
    signal,
    credentials: "same-origin",
    headers,
  });
  if (response.status === 304 && cache?.items) return cache.items;
  if (!response.ok)
    throw new Error(
      `Inbox unavailable (HTTP ${response.status}). Reload to refresh your session or try again.`,
    );
  const items = ((await response.json()) as { items: InboxEntry[] }).items;
  if (cache) {
    cache.items = items;
    cache.etag = response.headers.get("ETag") ?? undefined;
  }
  return items;
}

/** The full entry (complete request snapshot and resolution) for one interaction. */
export async function fetchInboxItem(id: string, signal?: AbortSignal): Promise<InboxEntry> {
  const response = await fetch(`/api/inbox/items/${encodeURIComponent(id)}`, {
    signal,
    credentials: "same-origin",
    headers: { Accept: "application/json" },
  });
  if (!response.ok) throw new Error(`Interaction unavailable (HTTP ${response.status}).`);
  return (await response.json()) as InboxEntry;
}

async function operatorAction(url: string, method: "POST" | "DELETE"): Promise<void> {
  const response = await fetchWithElevation(url, {
    method,
    headers: { Accept: "application/json" },
  });
  if (!response.ok) throw new Error(`The action failed (HTTP ${response.status}).`);
}

/** Ends an open item as canceled; it moves to History. Any kind, operator only. */
export function archiveInboxItem(id: string): Promise<void> {
  return operatorAction(`/api/inbox/items/${encodeURIComponent(id)}/archive`, "POST");
}

/** Removes an item from the Inbox, ending it first when it is still open. */
export function deleteInboxItem(id: string): Promise<void> {
  return operatorAction(`/api/inbox/items/${encodeURIComponent(id)}`, "DELETE");
}

/** The whole item (request, answer, state) as a JSON file the operator can keep. */
export function downloadInboxItem(entry: InboxEntry): void {
  const blob = new Blob([JSON.stringify(entry, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = `${entry.interaction.definition_binding.kind.replace(/^tangent\./, "")}-${entry.interaction.interaction_id.slice(0, 8)}.json`;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}

/** Ids of interactions whose full request or reply text contains `query`. */
export async function searchInbox(query: string, signal?: AbortSignal): Promise<string[]> {
  const response = await fetch(`/api/inbox/search?q=${encodeURIComponent(query)}`, {
    signal,
    credentials: "same-origin",
    headers: { Accept: "application/json" },
  });
  if (!response.ok) throw new Error(`Inbox search unavailable (HTTP ${response.status}).`);
  return ((await response.json()) as { ids: string[] }).ids;
}
