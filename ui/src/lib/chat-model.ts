// Pure model behind the Chat tab. The Chat view is a projection of the same
// Agent Turns data the Inbox reads (/api/turns): an item is the agent's side of
// the conversation and its resolution's response_text is the operator's side.
// Nothing here writes; sending goes through replyTurn like the Inbox does.

import { canReplyTurn, type TurnItemView, type TurnsInbox } from "@/lib/turns-api";

export const DEFAULT_PAGE_SIZE = 50;

export interface ChatAgent {
  agentID: string;
  name: string;
  lastMessageAt?: string;
  lastAgentMessageAt?: string;
}

export interface ChatMessage {
  key: string;
  from: "agent" | "operator";
  body: string;
  at: string;
  item: TurnItemView;
}

/** Kinds the composer may answer with the plain `respond` action. */
const RESPONDABLE = new Set<TurnItemView["kind"]>(["terminal", "question", "failure"]);

export function allItems(inbox: TurnsInbox | null): TurnItemView[] {
  return inbox ? [...inbox.pending, ...inbox.history] : [];
}

/** Every agent seen in the turns data, whether or not it is a chat agent. */
export function knownAgents(items: TurnItemView[]): ChatAgent[] {
  const byID = new Map<string, ChatAgent>();
  for (const item of items) {
    const agent = byID.get(item.agent_id) ?? { agentID: item.agent_id, name: item.agent_id };
    if (item.agent_label) agent.name = item.agent_label;
    for (const message of messagesOf(item)) {
      if (!agent.lastMessageAt || message.at > agent.lastMessageAt)
        agent.lastMessageAt = message.at;
      if (
        message.from === "agent" &&
        (!agent.lastAgentMessageAt || message.at > agent.lastAgentMessageAt)
      ) {
        agent.lastAgentMessageAt = message.at;
      }
    }
    byID.set(item.agent_id, agent);
  }
  return [...byID.values()];
}

function messagesOf(item: TurnItemView): ChatMessage[] {
  const out: ChatMessage[] = [
    { key: `a:${item.item_id}`, from: "agent", body: item.content, at: item.created_at, item },
  ];
  const text = item.resolution?.response_text?.trim();
  if (item.resolution && text) {
    out.push({
      key: `o:${item.resolution.resolution_id}`,
      from: "operator",
      body: text,
      at: item.resolution.resolved_at,
      item,
    });
  }
  return out;
}

/**
 * The two directions of one agent's conversation in one ascending stream.
 * De-duplicated by key (item id / resolution id) and ordered by time; an agent
 * message sorts before an operator reply stamped the same instant.
 */
export function buildStream(items: TurnItemView[], agentID: string): ChatMessage[] {
  const seen = new Set<string>();
  const out: ChatMessage[] = [];
  for (const item of items) {
    if (item.agent_id !== agentID) continue;
    for (const message of messagesOf(item)) {
      if (seen.has(message.key)) continue;
      seen.add(message.key);
      out.push(message);
    }
  }
  return out.sort(
    (a, b) =>
      Date.parse(a.at) - Date.parse(b.at) ||
      (a.from === b.from ? a.key.localeCompare(b.key) : a.from === "agent" ? -1 : 1),
  );
}

/** The newest pending item the composer can answer, if any. */
export function pendingFinal(items: TurnItemView[], agentID: string): TurnItemView | undefined {
  return items
    .filter(
      (item) =>
        item.agent_id === agentID &&
        !item.resolution &&
        ["presented", "staged", "validated", "submitted", "in_progress"].includes(item.state) &&
        RESPONDABLE.has(item.kind) &&
        canReplyTurn(item),
    )
    .sort((a, b) => b.queue_sequence - a.queue_sequence)[0];
}

/** Pinned agents first, then newest message first, then name. */
export function sortAgents(agents: ChatAgent[], pinned: string[]): ChatAgent[] {
  return [...agents].sort(
    (a, b) =>
      Number(pinned.includes(b.agentID)) - Number(pinned.includes(a.agentID)) ||
      (b.lastMessageAt ?? "").localeCompare(a.lastMessageAt ?? "") ||
      a.name.localeCompare(b.name),
  );
}

export function isUnseen(agent: ChatAgent, lastSeen: Record<string, string>): boolean {
  const seen = lastSeen[agent.agentID];
  return Boolean(agent.lastAgentMessageAt && (!seen || agent.lastAgentMessageAt > seen));
}

export interface ChatPrefs {
  agents: string[];
  pinned: string[];
  lastSeen: Record<string, string>;
  pageSize: number;
}

// Operator preferences are per-browser view state (ADR 0007); the chat-agent
// roster in a plugin setting is a recorded follow-up, not part of this slice.
const PREFS_KEY = "tangent.chat.prefs.v1";

export function loadPrefs(storage: Storage = window.localStorage): ChatPrefs {
  const empty: ChatPrefs = { agents: [], pinned: [], lastSeen: {}, pageSize: DEFAULT_PAGE_SIZE };
  try {
    const raw = JSON.parse(storage.getItem(PREFS_KEY) ?? "{}") as Partial<ChatPrefs>;
    const strings = (v: unknown) =>
      Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : [];
    const size = Number(raw.pageSize);
    return {
      agents: strings(raw.agents),
      pinned: strings(raw.pinned),
      lastSeen: raw.lastSeen && typeof raw.lastSeen === "object" ? { ...raw.lastSeen } : {},
      pageSize: Number.isInteger(size) && size >= 5 && size <= 500 ? size : DEFAULT_PAGE_SIZE,
    };
  } catch {
    return empty;
  }
}

export function savePrefs(prefs: ChatPrefs, storage: Storage = window.localStorage): void {
  try {
    storage.setItem(PREFS_KEY, JSON.stringify(prefs));
  } catch {
    // Quota or private mode: preferences are a convenience, never a failure.
  }
}
