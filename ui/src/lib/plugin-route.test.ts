import { describe, expect, it } from "vitest";
import { isPluginRoutePath } from "./plugin-route";

describe("isPluginRoutePath", () => {
  it("accepts plain plugin route paths", () => {
    for (const path of [
      "/api/plugins/torque-board/sync",
      "/api/plugins/x/a/b",
      "/api/plugins/my.plugin_1/state-v2",
      "/api/plugins/x/a.b/c",
    ]) {
      expect(isPluginRoutePath(path), path).toBe(true);
    }
  });

  it("refuses everything that is not a plugin route", () => {
    for (const path of [
      undefined,
      null,
      42,
      "",
      "https://evil.test/steal",
      "//evil.test/steal",
      "/api/hitl/items/1/resolve",
      "/api/plugins",
      "/api/plugins/",
      "/api/pluginsx/a/b",
      "api/plugins/x/a",
    ]) {
      expect(isPluginRoutePath(path), String(path)).toBe(false);
    }
  });

  it("refuses dot segments, including the one that reaches the inbox", () => {
    for (const path of [
      "/api/plugins/../hitl",
      "/api/plugins/x/a/../../../inbox/items/abc/archive",
      "/api/plugins/x/./a",
      "/api/plugins/x/a/.",
      "/api/plugins/x/a/..",
      "/api/plugins/x/..a/..",
    ]) {
      expect(isPluginRoutePath(path), path).toBe(false);
    }
  });

  it("refuses encoded and alternate separators", () => {
    for (const path of [
      "/api/plugins/x/%2e%2e/%2e%2e/inbox/items/abc/archive",
      "/api/plugins/x/%2E%2E/inbox",
      "/api/plugins/x/a%2fb",
      "/api/plugins/x/a%5cb",
      "/api/plugins/x/a\\..\\..\\inbox",
      "/api/plugins/x/%00",
      "/api/plugins/x/a%",
    ]) {
      expect(isPluginRoutePath(path), path).toBe(false);
    }
  });

  it("refuses doubled slashes, queries, fragments, whitespace and control or non-ASCII characters", () => {
    for (const path of [
      "/api/plugins/x//a",
      "/api/plugins/x/a/",
      "/api/plugins/x/sync?to=elsewhere",
      "/api/plugins/x/sync#frag",
      "/api/plugins/x/a b",
      "/api/plugins/x/a\tb",
      "/api/plugins/x/a\nb",
      "/api/plugins/x/a\u0000b",
      "/api/plugins/x/é",
      "/api/plugins/x/．．/inbox",
    ]) {
      expect(isPluginRoutePath(path), JSON.stringify(path)).toBe(false);
    }
  });

  it("only accepts a path the browser would request unchanged", () => {
    for (const path of ["/api/plugins/x/a", "/api/plugins/x/a.b"]) {
      expect(new URL(path, window.location.origin).pathname).toBe(path);
      expect(isPluginRoutePath(path)).toBe(true);
    }
  });
});
