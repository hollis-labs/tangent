// Client for the Tether chat roster, reached through the messaging plugin's
// routes (Tangent core has no Tether client). Nothing here is stored: the
// roster is whatever Tether reports, and "unavailable" is a first-class state.

export interface RosterSession {
  id: string;
  state: string;
  last_activity_at: string;
  at_turn_boundary: boolean;
}

export interface RosterAgent {
  launch_id: string;
  agent_id: string;
  display_name: string;
  channel: string;
  session: RosterSession | null;
}

export interface Roster {
  available: boolean;
  /** Why the roster is unavailable; empty when it is available. */
  reason: string;
  agents: RosterAgent[];
}

export const ROSTER_UNAVAILABLE: Roster = {
  available: false,
  reason: "roster_unavailable",
  agents: [],
};

const ROSTER_URL = "/api/plugins/messaging/chat/agents";
const SEND_URL = "/api/plugins/messaging/chat/send";

function isAgent(value: unknown): value is RosterAgent {
  const v = value as RosterAgent | null;
  return (
    !!v &&
    typeof v.launch_id === "string" &&
    v.launch_id !== "" &&
    typeof v.agent_id === "string" &&
    typeof v.display_name === "string"
  );
}

/** Never throws: any failure to reach a roster is an unavailable roster. */
export async function fetchChatRoster(signal?: AbortSignal): Promise<Roster> {
  try {
    const response = await fetch(ROSTER_URL, { headers: { Accept: "application/json" }, signal });
    if (!response.ok) return ROSTER_UNAVAILABLE;
    const body = (await response.json()) as Partial<Roster>;
    if (!body.available || !Array.isArray(body.agents)) {
      return { ...ROSTER_UNAVAILABLE, reason: body.reason || ROSTER_UNAVAILABLE.reason };
    }
    return { available: true, reason: "", agents: body.agents.filter(isAgent) };
  } catch {
    return ROSTER_UNAVAILABLE;
  }
}

export class ChatSendError extends Error {
  readonly status: number;
  readonly code: string;
  constructor(status: number, code: string) {
    super(code);
    this.name = "ChatSendError";
    this.status = status;
    this.code = code;
  }
}

export interface ChatSendResult {
  mode: string;
  sessionID: string;
  replayed: boolean;
}

/** Sends one message; a retry with the same key never delivers twice. */
export async function sendChat(
  launchID: string,
  text: string,
  idempotencyKey: string,
): Promise<ChatSendResult> {
  const response = await fetch(SEND_URL, {
    method: "POST",
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    body: JSON.stringify({ launch_id: launchID, text, idempotency_key: idempotencyKey }),
    signal: AbortSignal.timeout(20000),
  });
  let body: { code?: string; mode?: string; session_id?: string; replayed?: boolean } = {};
  try {
    body = await response.json();
  } catch {
    // Not JSON; the status carries the failure.
  }
  if (!response.ok) throw new ChatSendError(response.status, body.code || "http_error");
  return {
    mode: body.mode ?? "",
    sessionID: body.session_id ?? "",
    replayed: Boolean(body.replayed),
  };
}

const REASONS: Record<string, string> = {
  tether_endpoint_missing: "This Tether does not offer a chat roster yet.",
  tether_forbidden: "Tether refused the plugin's token for the chat roster.",
  tether_unreachable: "Tether could not be reached.",
  tether_error: "Tether returned an error for the chat roster.",
};

export function describeUnavailable(reason: string): string {
  return (
    REASONS[reason] ??
    "The chat roster is unavailable (the messaging plugin or Tether did not answer)."
  );
}
