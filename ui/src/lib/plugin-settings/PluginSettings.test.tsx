import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { PluginSettings } from "./PluginSettings";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it("renders published flat fields, never returns a saved secret, and drops replacement drafts on scope changes", async () => {
  const scopes = [
    { kind: "client", id: "owner" },
    { kind: "project", id: "demo" },
  ];
  const group = {
    id: "example",
    label: "Example",
    schema: {
      type: "object",
      additionalProperties: false,
      properties: {
        token: { type: "string", title: "Token", writeOnly: true },
        enabled: { type: "boolean", title: "Enabled" },
      },
      required: [],
    },
    fields: {
      token: { editable: true, secret: true, restart_required: true, apply_target: "example" },
      enabled: { editable: true, secret: false, restart_required: true, apply_target: "example" },
    },
    capabilities: { can_read: true, can_update: true, can_validate: true, can_reset: true },
  };
  const fetch = vi.fn(async (url: string) => ({
    ok: true,
    status: 200,
    json: async () =>
      url === "/api/plugin-management/config"
        ? { groups: [group], scopes }
        : {
            plugin_id: "example",
            scope: url.includes("demo") ? scopes[1] : scopes[0],
            revision: "r1",
            schema_digest: "s",
            values: {
              token: { present: true, secret_present: true, editable: true, has_override: true },
              enabled: { present: true, value: false, editable: true, has_override: false },
            },
            pending_restart: false,
          },
  }));
  vi.stubGlobal("fetch", fetch);
  render(<PluginSettings />);
  const token = await screen.findByLabelText("Token");
  expect(token).toHaveAttribute("type", "password");
  expect(token).toHaveValue("");
  fireEvent.change(token, { target: { value: "temporary-secret-fixture" } });
  expect(token).toHaveValue("temporary-secret-fixture");
  fireEvent.change(screen.getByLabelText("Settings scope"), { target: { value: "1" } });
  await waitFor(() => expect(screen.getByLabelText("Token")).toHaveValue(""));
  expect(screen.queryByText("temporary-secret-fixture")).not.toBeInTheDocument();
  expect(fetch.mock.calls.every(([url]) => !url.includes("temporary-secret-fixture"))).toBe(true);
});
