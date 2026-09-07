// Typed adapter over /api/channels — the channel pane's browser API
// (CW-20260907-0017). Unlike hitl-api.ts, this is not a frozen versioned
// wire contract (no `contract_version` field, no drift-detection stamp):
// the backend types (internal/channelpane) and this file are maintained
// by hand together, the same way ui/src/lib/rooms-api.ts tracks
// internal/room's browser-facing shapes.

export type ChannelMessageDirection = "operator" | "agent";

// Exactly three values, by design (internal/channelpane.MessageView's own
// doc comment): "queued" (the agent's own receive is open right now —
// delivery imminent), "awaiting-peer" (the agent is idle or gone), and
// "accepted-by-peer" (the agent acked it). A "failed" state does not
// exist — under the pull model nothing ever produces one.
export type ChannelDeliveryState = "queued" | "awaiting-peer" | "accepted-by-peer";

export interface ChannelSummary {
  channel_id: string;
  title?: string;
  unread_count: number;
  needs_input_count: number;
  last_message_at?: string;
  last_message_preview?: string;
}

export interface ChannelMessage {
  exchange_id: string;
  direction: ChannelMessageDirection;
  body: string;
  created_at: string;
  delivery_state?: ChannelDeliveryState;
}

export interface ChannelHITLItem {
  item_id: string;
  item_url: string;
  state: string;
  enqueued_at: string;
}

export interface ChannelAgentPresence {
  open: boolean;
  last_seen_at?: string;
}

export interface ChannelDetail {
  channel_id: string;
  title?: string;
  messages: ChannelMessage[]; // newest first
  hitl_items: ChannelHITLItem[];
  agent_presence?: ChannelAgentPresence;
}

export interface ChannelAPIErrorBody {
  code?: string;
  message?: string;
}

export class ChannelAPIError extends Error {
  readonly status: number;
  readonly detail: ChannelAPIErrorBody;

  constructor(status: number, detail: ChannelAPIErrorBody) {
    super(detail.message || `Channel request failed with HTTP ${status}`);
    this.name = "ChannelAPIError";
    this.status = status;
    this.detail = detail;
  }
}

export async function fetchChannels(signal?: AbortSignal): Promise<ChannelSummary[]> {
  const body = await channelRequest<{ channels: ChannelSummary[] }>("/api/channels", { signal });
  return body.channels;
}

export async function fetchChannel(
  channelID: string,
  signal?: AbortSignal,
): Promise<ChannelDetail> {
  return channelRequest<ChannelDetail>(`/api/channels/${encodeURIComponent(channelID)}`, {
    signal,
  });
}

export async function sendChannelMessage(channelID: string, body: string): Promise<ChannelMessage> {
  return channelRequest<ChannelMessage>(`/api/channels/${encodeURIComponent(channelID)}/messages`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ body }),
  });
}

export async function markChannelRead(channelID: string): Promise<number> {
  const result = await channelRequest<{ marked_read: number }>(
    `/api/channels/${encodeURIComponent(channelID)}/read`,
    { method: "POST" },
  );
  return result.marked_read;
}

async function channelRequest<T>(path: string, init: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: { Accept: "application/json", ...init.headers },
  });
  const body = (await response.json().catch(() => ({}))) as T | ChannelAPIErrorBody;
  if (!response.ok) {
    throw new ChannelAPIError(response.status, body as ChannelAPIErrorBody);
  }
  return body as T;
}
