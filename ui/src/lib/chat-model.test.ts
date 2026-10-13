import { describe, expect, it } from "vitest";
import { turn } from "./chat-fixtures";
import {
  buildStream,
  composerMode,
  isLivePending,
  isUnseen,
  loadPrefs,
  loadSent,
  pendingFinal,
  rosterAgents,
  SENT_PER_AGENT,
  type SentMessage,
  savePrefs,
  saveSent,
  sortAgents,
  staleItemIds,
  upsertSent,
} from "./chat-model";
import type { RosterAgent } from "./chat-roster-api";
import type { TurnItemView } from "./turns-api";

const PM = { agentID: "pm", tetherAgentID: "pm" };
const entry = (id: string, over: Partial<RosterAgent> = {}): RosterAgent => ({
  launch_id: id,
  agent_id: id,
  display_name: id.toUpperCase(),
  channel: "owner-inbox",
  session: null,
  ...over,
});

const resolved = (id: string, at: string, text: string): Partial<TurnItemView> => ({
  state: "resolved",
  resolution: {
    resolution_id: `res-${id}`,
    action: "respond",
    response_text: text,
    resolved_at: at,
    resolved_by: "operator",
  },
});

describe("chat model", () => {
  it("merges both directions in order and de-duplicates", () => {
    const a = turn({
      item_id: "a",
      created_at: "2026-10-12T10:00:00Z",
      ...resolved("a", "2026-10-12T10:05:00Z", "do it"),
    });
    const b = turn({ item_id: "b", created_at: "2026-10-12T10:10:00Z", content: "done" });
    const stream = buildStream([b, a, a], PM);
    expect(stream.map((m) => [m.from, m.body])).toEqual([
      ["agent", "hello"],
      ["operator", "do it"],
      ["agent", "done"],
    ]);
  });
  it("ignores other agents and resolutions without text", () => {
    const other = turn({ item_id: "x", agent_id: "arch" });
    const dismissed = turn({
      item_id: "d",
      state: "resolved",
      resolution: {
        resolution_id: "r",
        action: "dismiss",
        resolved_at: "2026-10-12T11:00:00Z",
        resolved_by: "operator",
      },
    });
    expect(buildStream([other, dismissed], PM)).toHaveLength(1);
  });
  it("offers a composer target only for a pending replyable final", () => {
    expect(pendingFinal([turn({ item_id: "a" })], PM)?.item_id).toBe("a");
    expect(pendingFinal([turn({ item_id: "a", state: "resolved" })], PM)).toBeUndefined();
    expect(pendingFinal([turn({ item_id: "a", kind: "approval" })], PM)).toBeUndefined();
    expect(pendingFinal([turn({ item_id: "a", replyable: false })], PM)).toBeUndefined();
  });
  it("builds the roster from Tether only, with message times from the turns", () => {
    const agents = rosterAgents(
      [entry("a"), entry("b"), entry("idle")],
      [
        turn({ item_id: "1", agent_id: "a", created_at: "2026-10-12T09:00:00Z" }),
        turn({ item_id: "2", agent_id: "b", created_at: "2026-10-12T10:00:00Z" }),
        turn({ item_id: "3", agent_id: "not-on-roster" }),
      ],
    );
    expect(agents.map((x) => [x.agentID, x.name, x.lastMessageAt])).toEqual([
      ["a", "A", "2026-10-12T09:00:00Z"],
      ["b", "B", "2026-10-12T10:00:00Z"],
      ["idle", "IDLE", undefined],
    ]);
    expect(sortAgents(agents, []).map((x) => x.agentID)).toEqual(["b", "a", "idle"]);
    expect(sortAgents(agents, ["a"]).map((x) => x.agentID)).toEqual(["a", "b", "idle"]);
    expect(isUnseen(agents[0], {})).toBe(true);
    expect(isUnseen(agents[0], { a: "2026-10-12T09:00:00Z" })).toBe(false);
    expect(isUnseen(agents[2], {})).toBe(false);
  });
  it("matches a turn to its launch before its agent id", () => {
    const viaLaunch = turn({
      item_id: "l",
      agent_id: "logical-7",
      source_message: {
        channel: "c",
        message_id: "m",
        sequence: 1,
        attribution: { launch_id: "pm-launch" },
      },
    } as Partial<TurnItemView> & { item_id: string });
    const ref = { agentID: "pm-launch", tetherAgentID: "pm" };
    // A turn naming no launch falls back to the agent id.
    expect(buildStream([viaLaunch, turn({ item_id: "x", agent_id: "pm" })], ref)).toHaveLength(2);
    expect(buildStream([viaLaunch, turn({ item_id: "y", agent_id: "arch" })], ref)).toHaveLength(1);
    // A turn from a different launch of the same agent is not this chat.
    expect(buildStream([viaLaunch], { agentID: "other-launch", tetherAgentID: "pm" })).toHaveLength(
      0,
    );
  });
  describe("composerMode decision table", () => {
    const base = { agentID: "pm", tetherAgentID: "pm", name: "PM" };
    const live = turn({ item_id: "p", session_id: "s" });
    const stale = turn({ item_id: "old", session_id: "dead" });
    const session = (over: object) => ({
      id: "s",
      state: "running",
      last_activity_at: "",
      at_turn_boundary: false,
      ...over,
    });
    const mode = (s: ReturnType<typeof session> | null, p?: TurnItemView) =>
      composerMode({ ...base, session: s }, p).kind;
    it("running + pending of the same session -> reply", () => {
      expect(composerMode({ ...base, session: session({}) }, live)).toEqual({
        kind: "reply",
        item: live,
      });
    });
    it("running mid-turn + no pending -> working", () => {
      expect(mode(session({}))).toBe("working");
    });
    it("running at turn boundary + no pending -> send", () => {
      expect(mode(session({ at_turn_boundary: true }))).toBe("send");
    });
    it("not running + stale pending -> send", () => {
      for (const state of ["completed", "stopped", "failed"]) {
        expect(mode(session({ state }), stale)).toBe("send");
        expect(mode(session({ state }), live)).toBe("send");
      }
    });
    it("no session + pending or none -> send", () => {
      expect(mode(null, stale)).toBe("send");
      expect(mode(null)).toBe("send");
    });
    it("running + pending from another session -> working mid-turn, send at boundary", () => {
      expect(mode(session({}), stale)).toBe("working");
      expect(mode(session({ at_turn_boundary: true }), stale)).toBe("send");
    });
    it("a pending item naming no session is never replied to", () => {
      expect(mode(session({}), turn({ item_id: "n" }))).toBe("working");
    });
    it("isLivePending only for the running session", () => {
      expect(isLivePending({ session: session({}) }, live)).toBe(true);
      expect(isLivePending({ session: session({}) }, stale)).toBe(false);
      expect(isLivePending({ session: session({ state: "completed" }) }, live)).toBe(false);
      expect(isLivePending({ session: null }, live)).toBe(false);
    });
  });
  it("round-trips preferences, clamps a bad page size and drops a stored roster", () => {
    savePrefs({ pinned: ["pm"], lastSeen: {}, pageSize: 3 });
    expect(loadPrefs().pinned).toEqual(["pm"]);
    expect(loadPrefs().pageSize).toBe(50);
    localStorage.setItem(
      "tangent.chat.prefs.v1",
      JSON.stringify({ agents: ["old"], pinned: ["x"], lastSeen: {}, pageSize: 25 }),
    );
    const loaded = loadPrefs();
    expect(loaded).toEqual({ pinned: ["x"], lastSeen: {}, pageSize: 25 });
    expect("agents" in loaded).toBe(false);
    localStorage.clear();
  });
});

describe("staleItemIds", () => {
  const agent = {
    agentID: "pm",
    tetherAgentID: "pm",
    name: "PM",
    session: { id: "s", state: "running", last_activity_at: "", at_turn_boundary: true },
  };
  it("marks pending items of other sessions, and all of them when nothing runs", () => {
    const live = turn({ item_id: "l", session_id: "s", queue_sequence: 2 });
    const old = turn({ item_id: "o", session_id: "dead", queue_sequence: 1 });
    expect([...staleItemIds([live, old], agent)]).toEqual(["o"]);
    expect([...staleItemIds([live, old], { ...agent, session: null })].sort()).toEqual(["l", "o"]);
  });
});

const sentMsg = (
  key: string,
  text: string,
  at: string,
  over: Partial<SentMessage> = {},
): SentMessage => ({
  key,
  agentID: "pm",
  text,
  at,
  status: "sent",
  ...over,
});

describe("sent messages in the stream", () => {
  it("merges a sent message in time order with both other directions", () => {
    const a = turn({ item_id: "a", created_at: "2026-10-12T10:00:00Z", content: "first" });
    const b = turn({ item_id: "b", created_at: "2026-10-12T10:10:00Z", content: "reply" });
    const stream = buildStream([b, a], PM, [sentMsg("k1", "hi there", "2026-10-12T10:05:00Z")]);
    expect(stream.map((m) => [m.from, m.body])).toEqual([
      ["agent", "first"],
      ["operator", "hi there"],
      ["agent", "reply"],
    ]);
    expect(stream[1]?.item).toBeUndefined();
  });

  it("shows a sent message when there is no turn data at all", () => {
    const stream = buildStream([], PM, [sentMsg("k1", "hello", "2026-10-12T10:00:00Z")]);
    expect(stream.map((m) => m.body)).toEqual(["hello"]);
  });

  it("de-duplicates by send key and ignores other agents' sends", () => {
    const stream = buildStream([], PM, [
      sentMsg("k1", "once", "2026-10-12T10:00:00Z"),
      sentMsg("k1", "once", "2026-10-12T10:00:00Z"),
      sentMsg("k2", "elsewhere", "2026-10-12T10:00:00Z", { agentID: "arch" }),
    ]);
    expect(stream.map((m) => m.body)).toEqual(["once"]);
  });

  it("does not double up when the same text also arrives as a reply resolution", () => {
    const item = turn({
      item_id: "a",
      created_at: "2026-10-12T10:00:00Z",
      ...resolved("a", "2026-10-12T10:05:01Z", "do it"),
    });
    const stream = buildStream([item], PM, [sentMsg("k1", "do it", "2026-10-12T10:05:00Z")]);
    expect(stream.filter((m) => m.from === "operator")).toHaveLength(1);
    expect(stream.find((m) => m.from === "operator")?.key).toBe("o:res-a");
  });

  it("keeps two identical sends when only one resolution exists, and a send after the resolution", () => {
    const item = turn({
      item_id: "a",
      created_at: "2026-10-12T10:00:00Z",
      ...resolved("a", "2026-10-12T10:05:00Z", "ok"),
    });
    const stream = buildStream([item], PM, [
      sentMsg("k1", "ok", "2026-10-12T10:04:00Z"),
      sentMsg("k2", "ok", "2026-10-12T10:04:30Z"),
      sentMsg("k3", "ok", "2026-10-12T10:09:00Z"),
    ]);
    expect(stream.filter((m) => m.from === "operator")).toHaveLength(3);
  });

  it("carries a failed send's status and error so the view can offer a retry", () => {
    const [m] = buildStream([], PM, [
      sentMsg("k1", "lost", "2026-10-12T10:00:00Z", { status: "failed", error: "nope" }),
    ]);
    expect(m?.sent).toEqual({ key: "k1", status: "failed", error: "nope" });
    expect(m?.body).toBe("lost");
  });
});

describe("sent message persistence", () => {
  const storage = () => {
    localStorage.clear();
    return localStorage;
  };

  it("round-trips through storage", () => {
    const s = storage();
    const records = [sentMsg("k1", "hi", "2026-10-12T10:00:00Z")];
    saveSent(records, s);
    expect(loadSent(s)).toEqual(records);
  });

  it("loads an unconfirmed send as failed and drops malformed records", () => {
    const s = storage();
    s.setItem(
      "tangent.chat.sent.v1",
      JSON.stringify([
        sentMsg("k1", "mid-flight", "2026-10-12T10:00:00Z", { status: "sending" }),
        { key: 1 },
        "junk",
      ]),
    );
    const loaded = loadSent(s);
    expect(loaded).toHaveLength(1);
    expect(loaded[0]).toMatchObject({ key: "k1", status: "failed" });
    s.setItem("tangent.chat.sent.v1", "not json");
    expect(loadSent(s)).toEqual([]);
  });

  it("upserts by key and caps the records kept per agent", () => {
    let list: SentMessage[] = [];
    list = upsertSent(list, sentMsg("k1", "a", "2026-10-12T10:00:00Z", { status: "sending" }));
    list = upsertSent(list, sentMsg("k1", "a", "2026-10-12T10:00:00Z"));
    expect(list).toHaveLength(1);
    expect(list[0]?.status).toBe("sent");
    for (let i = 0; i < SENT_PER_AGENT + 5; i++) {
      const at = new Date(Date.UTC(2026, 9, 12, 11, 0, i)).toISOString();
      list = upsertSent(list, sentMsg(`n${i}`, "x", at));
    }
    expect(list.filter((r) => r.agentID === "pm")).toHaveLength(SENT_PER_AGENT);
    expect(list.some((r) => r.key === "k1")).toBe(false);
    expect(list.some((r) => r.key === `n${SENT_PER_AGENT + 4}`)).toBe(true);
  });
});
