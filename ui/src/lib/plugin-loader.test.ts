import { afterEach, describe, expect, it, vi } from "vitest";
import { syncPlugins } from "./plugin-loader";

// The body internal/server buildRegistryResponse emits today (registry v2, empty).
const hostV2Empty =
  '{"registry_version":2,"host_instance":"test-host","revision":1,"plugins":{},"kinds":{},"regions":{},"contributions":{},"refusals":[]}';

function syncedLog(spy: { mock: { calls: unknown[][] } }) {
  const call = spy.mock.calls.find((c) => c[0] === "[plugin-loader] plugins synced");
  return call?.[1] as { accepted: boolean; loaded: number; failed: number } | undefined;
}

describe("syncPlugins", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("admits the host's registry v2 snapshot", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(hostV2Empty)));
    const info = vi.spyOn(console, "info").mockImplementation(() => {});
    const error = vi.spyOn(console, "error").mockImplementation(() => {});

    await syncPlugins();

    expect(fetch).toHaveBeenCalledWith("/api/plugins/registry");
    expect(syncedLog(info)).toMatchObject({ accepted: true, loaded: 0, failed: 0 });
    expect(error).not.toHaveBeenCalled();
  });

  it("does not throw or sync when the registry fetch fails", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("nope", { status: 500 })));
    const info = vi.spyOn(console, "info").mockImplementation(() => {});
    const error = vi.spyOn(console, "error").mockImplementation(() => {});

    await expect(syncPlugins()).resolves.toBeUndefined();

    expect(error).toHaveBeenCalledWith("[plugin-loader] registry fetch failed: 500");
    expect(syncedLog(info)).toBeUndefined();
  });

  it("does not throw on a body the registry refuses", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response('{"protocol":1}')));
    const info = vi.spyOn(console, "info").mockImplementation(() => {});
    vi.spyOn(console, "error").mockImplementation(() => {});

    await expect(syncPlugins()).resolves.toBeUndefined();

    expect(syncedLog(info)?.accepted).not.toBe(true);
  });
});
