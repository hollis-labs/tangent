import { describe, expect, it } from "vitest";
import { surfacePath } from "./paths";

describe("surfacePath", () => {
  it.each([
    ["/", "/"],
    ["/inbox", "/"],
    ["/inbox/items/abc", "/"],
    ["/r/room-1", "/"],
    ["/chat", "/chat"],
    ["/chat/agent-1", "/chat"],
    ["/channels/c1", "/channels"],
    ["/settings", "/settings"],
    ["/identity/keys", "/identity/keys"],
    ["/chatter", "/chatter"],
  ])("%s -> %s", (from, to) => {
    expect(surfacePath(from)).toBe(to);
  });
});
