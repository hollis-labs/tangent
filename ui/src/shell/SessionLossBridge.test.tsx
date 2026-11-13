import { render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { apiFetch } from "@/lib/api";

const listSessions = vi.hoisted(() => vi.fn(() => Promise.reject(new Error("401"))));
vi.mock("@hollis-labs/kit-identity/client", () => ({
  useIdentity: () => ({ client: { listSessions } }),
}));

import { SessionLossBridge } from "./SessionLossBridge";

const answer = (status: number, body: unknown) =>
  vi.fn().mockResolvedValue(new Response(JSON.stringify(body), { status }));

afterEach(() => {
  vi.unstubAllGlobals();
  listSessions.mockClear();
});

describe("<SessionLossBridge>", () => {
  it("asks the identity kit a question only a live session answers when the app's own request is 401", async () => {
    vi.stubGlobal("fetch", answer(401, { code: "unauthorized", message: "sign in" }));
    render(<SessionLossBridge />);
    await apiFetch("/api/inbox");
    await waitFor(() => expect(listSessions).toHaveBeenCalledTimes(1));
  });

  it("stays quiet for a wrong password and for other failures", async () => {
    render(<SessionLossBridge />);
    vi.stubGlobal("fetch", answer(401, { code: "invalid_login", message: "wrong" }));
    await apiFetch("/api/login", { method: "POST" });
    vi.stubGlobal("fetch", answer(403, { code: "scope_required", message: "no" }));
    await apiFetch("/api/inbox");
    expect(listSessions).not.toHaveBeenCalled();
  });

  it("stops listening when it unmounts", async () => {
    vi.stubGlobal("fetch", answer(401, { code: "unauthorized", message: "sign in" }));
    render(<SessionLossBridge />).unmount();
    await apiFetch("/api/inbox");
    expect(listSessions).not.toHaveBeenCalled();
  });
});
