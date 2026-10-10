import { describe, expect, it, vi } from "vitest";
import type { SettingsAPI, Snapshot } from "./api";
import { SettingsController, type ViewState } from "./controller";

const scope = { kind: "client", id: "owner" };
const snapshot: Snapshot = {
  plugin_id: "example",
  scope,
  revision: "r1",
  schema_digest: "s1",
  values: { token: { present: true, secret_present: true, editable: true, has_override: true } },
  pending_restart: false,
};
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}
describe("host settings controller", () => {
  it("retains revision and drafts after refusal, then saves only with the refreshed revision", async () => {
    let state: ViewState | undefined;
    const api: SettingsAPI = {
      read: vi.fn().mockResolvedValue(snapshot),
      action: vi.fn().mockRejectedValue(new Error("secret-provider-value")),
    };
    const c = new SettingsController(api, "example", scope, (s) => {
      state = s;
    });
    await c.refresh();
    c.draft({ token: { kind: "value", value: "synthetic-replacement" } });
    await c.action("save", { set: { token: "synthetic-replacement" }, unset: [] });
    expect(state?.snapshot?.revision).toBe("r1");
    expect(state?.draft.token).toBeDefined();
    expect(state?.error).not.toContain("secret-provider");
    api.read = vi.fn().mockResolvedValue({ ...snapshot, revision: "r2" });
    await c.refresh();
    api.action = vi.fn().mockResolvedValue({ ...snapshot, revision: "r3", pending_restart: true });
    await c.action("save", { set: { token: "synthetic-replacement" }, unset: [] });
    expect(api.action).toHaveBeenCalledWith(
      "example",
      scope,
      "r2",
      "save",
      { set: { token: "synthetic-replacement" }, unset: [] },
      expect.any(AbortSignal),
    );
    expect(state?.draft).toEqual({});
    expect(state?.values.token.value).toBeUndefined();
    expect(state?.snapshot?.pending_restart).toBe(true);
    c.dispose();
  });
  it("does not publish a late save into a disposed plugin or scope", async () => {
    const pending = deferred<Snapshot>();
    const states: ViewState[] = [];
    const api: SettingsAPI = {
      read: async () => snapshot,
      action: vi.fn().mockReturnValue(pending.promise),
    };
    const c = new SettingsController(api, "example", scope, (s) => states.push(s));
    await c.refresh();
    c.draft({ token: { kind: "value", value: "private-fixture" } });
    const save = c.action("save", { set: { token: "private-fixture" }, unset: [] });
    c.dispose();
    const before = states.length;
    pending.resolve({ ...snapshot, revision: "late" });
    await save;
    expect(states).toHaveLength(before);
    expect((api.action as ReturnType<typeof vi.fn>).mock.calls[0][5].aborted).toBe(true);
  });
  it("serializes saves and validation rather than duplicating a pending write", async () => {
    const pending = deferred<Snapshot>();
    const api: SettingsAPI = {
      read: async () => snapshot,
      action: vi.fn().mockReturnValue(pending.promise),
    };
    const c = new SettingsController(api, "example", scope, () => {});
    await c.refresh();
    const save = c.action("save", { set: {}, unset: [] });
    await c.action("save", { set: {}, unset: [] });
    await c.action("validate", { set: {}, unset: [] });
    expect(api.action).toHaveBeenCalledTimes(1);
    pending.resolve(snapshot);
    await save;
    c.dispose();
  });
});
