import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { PluginLifecycle } from "./PluginLifecycle";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it("viewing an unknown plugin is read-only; explicit opt-in and disable show actual state", async () => {
  let enabled = false;
  const fetch = vi.fn(async (path: string, init: RequestInit) => {
    if (init.method === "POST") enabled = path.endsWith("/enable");
    return {
      ok: true,
      json: async () => ({
        plugins: [
          { id: "new.plugin", enabled, loaded: enabled, state: enabled ? "running" : "disabled" },
        ],
      }),
    };
  });
  vi.stubGlobal("fetch", fetch);
  render(<PluginLifecycle />);
  const enable = await screen.findByRole("button", { name: "Enable new.plugin" });
  await waitFor(() => expect(enable).toBeEnabled());
  expect(fetch.mock.calls.every(([, init]) => init.method !== "POST")).toBe(true);
  expect(screen.getByText("Intent: disabled")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Reload new.plugin" })).toBeDisabled();
  fireEvent.click(enable);
  const disable = await screen.findByRole("button", { name: "Disable new.plugin" });
  await waitFor(() => expect(disable).toBeEnabled());
  expect(screen.getByText("Runtime: running (loaded)")).toBeInTheDocument();
  fireEvent.click(disable);
  await screen.findByText("Runtime: disabled (not loaded)");
  expect(
    fetch.mock.calls.filter(([, init]) => init.method === "POST").map(([path]) => path),
  ).toEqual([
    "/api/plugin-management/new.plugin/enable",
    "/api/plugin-management/new.plugin/disable",
  ]);
});

it("a refused startup can leave enabled intent without falsely claiming readiness", async () => {
  let attempted = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      if (init.method === "POST") {
        attempted = true;
        return { ok: false };
      }
      return {
        ok: true,
        json: async () => ({
          plugins: [
            {
              id: "broken",
              enabled: attempted,
              loaded: false,
              state: attempted ? "failed" : "disabled",
            },
          ],
        }),
      };
    }),
  );
  render(<PluginLifecycle />);
  const enable = await screen.findByRole("button", { name: "Enable broken" });
  await waitFor(() => expect(enable).toBeEnabled());
  fireEvent.click(enable);
  await screen.findByText("Runtime: failed (not loaded)");
  expect(screen.getByText("Intent: enabled")).toBeInTheDocument();
  expect(await screen.findByRole("alert")).toHaveTextContent("Plugin operation refused");
});

it("disposing the settings view cancels its pending read without any enable effect", async () => {
  let signal: AbortSignal | undefined;
  const fetch = vi.fn((_path: string, init: RequestInit) => {
    signal = init.signal as AbortSignal;
    return new Promise(() => {});
  });
  vi.stubGlobal("fetch", fetch);
  const view = render(<PluginLifecycle />);
  view.unmount();
  expect(signal?.aborted).toBe(true);
  expect(fetch).toHaveBeenCalledTimes(1);
});
