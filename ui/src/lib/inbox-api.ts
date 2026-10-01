export type InboxCategory = "approval" | "document" | "turn" | "workflow";

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

export async function fetchInbox(signal?: AbortSignal): Promise<InboxEntry[]> {
  const response = await fetch("/api/inbox", {
    signal,
    credentials: "same-origin",
    headers: { Accept: "application/json" },
  });
  if (!response.ok)
    throw new Error(
      `Inbox unavailable (HTTP ${response.status}). Reload to refresh your session or try again.`,
    );
  return ((await response.json()) as { items: InboxEntry[] }).items;
}
