// Pure model behind the Chat tab. The Chat view is a projection of the same
// Agent Turns data the Inbox reads (/api/turns): an item is the agent's side of
// the conversation and its resolution's response_text is the operator's side.
// Nothing here writes; sending goes through replyTurn like the Inbox does.

import type { RosterAgent, RosterSession } from "@/lib/chat-roster-api";
import { canReplyTurn, type TurnItemView, type TurnsInbox } from "@/lib/turns-api";

export const DEFAULT_PAGE_SIZE = 50;

export interface ChatAgent {
  /** The Tether launch id: the roster key and the /chat/:agentID route param. */
  agentID: string;
  /** The Tether agent id, matched against a turn's agent when it names no launch. */
  tetherAgentID: string;
  name: string;
  session: RosterSession | null;
  lastMessageAt?: string;
  lastAgentMessageAt?: string;
}

type ChatAgentRef = Pick<ChatAgent, "agentID" | "tetherAgentID">;

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

/**
 * Whether a turn belongs to a roster agent: by the launch the publication was
 * made from when it names one, otherwise by the agent identity.
 */
export function belongsTo(item: TurnItemView, agent: Pick<ChatAgent, "agentID" | "tetherAgentID">) {
  const launch = item.source_message?.attribution?.launch_id;
  return launch ? launch === agent.agentID : item.agent_id === agent.tetherAgentID;
}

/** The roster, as Tether reports it, with each agent's message times. */
export function rosterAgents(roster: RosterAgent[], items: TurnItemView[]): ChatAgent[] {
  return roster.map((entry) => {
    const agent: ChatAgent = {
      agentID: entry.launch_id,
      tetherAgentID: entry.agent_id,
      name: entry.display_name || entry.agent_id,
      session: entry.session,
    };
    for (const item of items) {
      if (!belongsTo(item, agent)) continue;
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
    }
    return agent;
  });
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
export function buildStream(items: TurnItemView[], agent: ChatAgentRef): ChatMessage[] {
  const seen = new Set<string>();
  const out: ChatMessage[] = [];
  for (const item of items) {
    if (!belongsTo(item, agent)) continue;
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

/** Pending items of this agent that the reply path would accept, newest first. */
export function pendingReplyable(items: TurnItemView[], agent: ChatAgentRef): TurnItemView[] {
  return items
    .filter(
      (item) =>
        belongsTo(item, agent) &&
        !item.resolution &&
        ["presented", "staged", "validated", "submitted", "in_progress"].includes(item.state) &&
        RESPONDABLE.has(item.kind) &&
        canReplyTurn(item),
    )
    .sort((a, b) => b.queue_sequence - a.queue_sequence);
}

/** The newest pending item the composer can answer, if any. */
export function pendingFinal(items: TurnItemView[], agent: ChatAgentRef): TurnItemView | undefined {
  return pendingReplyable(items, agent)[0];
}

/**
 * Ids of pending items the agent can no longer receive a reply to, because the
 * session that produced them is not the agent's running session. UI-only: the
 * Inbox still lists them; the stream just stops presenting them as awaiting.
 */
export function staleItemIds(items: TurnItemView[], agent: ChatAgent): Set<string> {
  return new Set(
    pendingReplyable(items, agent)
      .filter((item) => !isLivePending(agent, item))
      .map((item) => item.item_id),
  );
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
  pinned: string[];
  lastSeen: Record<string, string>;
  pageSize: number;
}

// Pins, last-seen times and page size are per-browser view state (ADR 0007).
// The roster itself is never stored here: it comes from Tether. A roster saved
// by an earlier version (an `agents` list) is ignored and dropped on next save.
const PREFS_KEY = "tangent.chat.prefs.v1";

export function loadPrefs(storage: Storage = window.localStorage): ChatPrefs {
  const empty: ChatPrefs = { pinned: [], lastSeen: {}, pageSize: DEFAULT_PAGE_SIZE };
  try {
    const raw = JSON.parse(storage.getItem(PREFS_KEY) ?? "{}") as Partial<ChatPrefs>;
    const strings = (v: unknown) =>
      Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : [];
    const size = Number(raw.pageSize);
    return {
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

/** How the composer sends right now. */
export type ComposerMode =
  | { kind: "reply"; item: TurnItemView }
  | { kind: "send" }
  | { kind: "working" };

/** Whether Tether's roster says this agent has a live session. */
export function runningSession(agent: Pick<ChatAgent, "session">): RosterSession | null {
  const session = agent.session;
  return session?.id && session.state === "running" ? session : null;
}

/**
 * A pending item is answerable only while the session that produced it is the
 * agent's running session. A reply to a dead session is accepted by the server
 * (HTTP 200) and delivered nowhere, so such an item is stale.
 */
export function isLivePending(agent: Pick<ChatAgent, "session">, pending?: TurnItemView): boolean {
  const session = runningSession(agent);
  return Boolean(session && pending && pending.session_id === session.id);
}

/**
 * Decision table (roster state from Tether decides; the pending item only
 * qualifies if it belongs to the running session):
 *
 *   running, pending of that session          -> reply
 *   running, otherwise, mid-turn              -> working (disabled)
 *   running, otherwise, at a turn boundary    -> send (Tether delivers the next turn)
 *   no running session (none/completed/...)   -> send (starts the launch), any pending is stale
 *
 * A pending item from another session never makes the composer reply, and
 * mid-turn it never makes the composer send either.
 */
export function composerMode(agent: ChatAgent, pending: TurnItemView | undefined): ComposerMode {
  const session = runningSession(agent);
  if (!session) return { kind: "send" };
  if (pending && isLivePending(agent, pending)) return { kind: "reply", item: pending };
  return session.at_turn_boundary ? { kind: "send" } : { kind: "working" };
}
