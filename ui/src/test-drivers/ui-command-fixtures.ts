// Authored synthetic display data. No live API, identity, credential or provider.
import type { ChannelDetail, ChannelSummary } from "@/lib/channel-api";
import type { DocItemView } from "@/lib/docs-api";
import type { InboxEntry } from "@/lib/inbox-api";
export const syntheticInbox: InboxEntry = {
  sequence: 1,
  interaction: {
    interaction_id: "synthetic-item",
    definition_binding: { kind: "tangent.doc-item" },
    request_snapshot: {
      title: "Synthetic review",
      content_markdown: "Private fixture body",
      agent_id: "fixture",
      requires_ack: false,
    },
    caller_scope: "standalone-local:synthetic",
    state: "presented",
    revision: 1,
    created_at: "2026-10-01T10:00:00Z",
    updated_at: "2026-10-01T10:00:00Z",
  },
};
export const syntheticDoc: DocItemView = {
  contract_version: "1.0",
  item_id: "synthetic-doc",
  agent_id: "fixture",
  title: "Synthetic document",
  summary: "Private fixture summary",
  content_markdown: "Private fixture body",
  requires_ack: true,
  state: "presented",
  queue_sequence: 1,
  revision: 1,
  created_at: "2026-10-01T10:00:00Z",
  updated_at: "2026-10-01T10:00:00Z",
};
export const syntheticChannel: ChannelSummary = {
  channel_id: "synthetic-channel",
  title: "Synthetic channel",
  unread_count: 2,
  needs_input_count: 0,
  last_message_preview: "Private fixture message",
};
export const syntheticDetail: ChannelDetail = {
  channel_id: "synthetic-channel",
  title: "Synthetic channel",
  messages: [],
  hitl_items: [],
};
export const fixtureResponse = (body: unknown) =>
  new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
export function fixtureFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  if (init?.method && init.method !== "GET")
    return Promise.reject(new Error("fixture refuses writes"));
  const path = String(input);
  if (path === "/api/inbox") return Promise.resolve(fixtureResponse({ items: [syntheticInbox] }));
  if (path === "/api/docs")
    return Promise.resolve(
      fixtureResponse({
        contract_version: "1.0",
        synced_at: "2026-10-01T10:00:00Z",
        pending: [syntheticDoc],
        history: [],
      }),
    );
  if (path === "/api/channels")
    return Promise.resolve(fixtureResponse({ channels: [syntheticChannel] }));
  if (path === "/api/channels/synthetic-channel")
    return Promise.resolve(fixtureResponse(syntheticDetail));
  return Promise.reject(new Error("unknown synthetic fixture request"));
}
export class FixtureEventSource {
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  addEventListener() {}
  removeEventListener() {}
  close() {}
}
