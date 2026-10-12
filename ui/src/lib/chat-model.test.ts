import { describe, expect, it } from "vitest";
import { turn } from "./chat-fixtures";
import {
  buildStream,
  isUnseen,
  knownAgents,
  loadPrefs,
  pendingFinal,
  savePrefs,
  sortAgents,
} from "./chat-model";
import type { TurnItemView } from "./turns-api";

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
    const stream = buildStream([b, a, a], "pm");
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
    expect(buildStream([other, dismissed], "pm")).toHaveLength(1);
  });
  it("offers a composer target only for a pending replyable final", () => {
    expect(pendingFinal([turn({ item_id: "a" })], "pm")?.item_id).toBe("a");
    expect(pendingFinal([turn({ item_id: "a", state: "resolved" })], "pm")).toBeUndefined();
    expect(pendingFinal([turn({ item_id: "a", kind: "approval" })], "pm")).toBeUndefined();
    expect(pendingFinal([turn({ item_id: "a", replyable: false })], "pm")).toBeUndefined();
  });
  it("sorts pinned first then newest, and tracks unseen agent messages", () => {
    const agents = knownAgents([
      turn({ item_id: "1", agent_id: "a", created_at: "2026-10-12T09:00:00Z" }),
      turn({ item_id: "2", agent_id: "b", created_at: "2026-10-12T10:00:00Z" }),
    ]);
    expect(sortAgents(agents, []).map((x) => x.agentID)).toEqual(["b", "a"]);
    expect(sortAgents(agents, ["a"]).map((x) => x.agentID)).toEqual(["a", "b"]);
    expect(isUnseen(agents[0], {})).toBe(true);
    expect(isUnseen(agents[0], { a: "2026-10-12T09:00:00Z" })).toBe(false);
  });
  it("round-trips preferences and clamps a bad page size", () => {
    savePrefs({ agents: ["pm"], pinned: ["pm"], lastSeen: {}, pageSize: 3 });
    expect(loadPrefs().agents).toEqual(["pm"]);
    expect(loadPrefs().pageSize).toBe(50);
    localStorage.clear();
  });
});
