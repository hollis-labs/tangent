import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { HITLOperatorItem } from "@/lib/hitl-api";
import { resolveHITLApproval } from "@/lib/hitl-api";
import { cancelOperatorElevation, ELEVATE_PATH } from "@/lib/operator-elevation";
import { OperatorTokenPrompt } from "./OperatorTokenPrompt";

const TOKEN = "typed-operator-token-for-the-ui-test-0123456789";

const item = {
  item_id: "item-1",
  revision: 3,
  presented_projection_revision: 3,
} as HITLOperatorItem;

afterEach(() => {
  act(() => cancelOperatorElevation());
  vi.unstubAllGlobals();
});

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

it("asks for the operator token on assurance_insufficient, elevates, and retries once", async () => {
  let resolveCalls = 0;
  const fetcher = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
    if (String(input) === ELEVATE_PATH) return json(200, { elevated: true });
    resolveCalls++;
    return resolveCalls === 1
      ? json(403, { code: "assurance_insufficient", message: "elevate" })
      : json(200, { contract_version: "1.0", state: "resolved", item_id: "item-1" });
  });
  vi.stubGlobal("fetch", fetcher);
  render(<OperatorTokenPrompt />);

  const decision = resolveHITLApproval(item, "approved");
  const field = await screen.findByLabelText("Operator token");
  expect(field).toHaveAttribute("type", "password");
  fireEvent.change(field, { target: { value: TOKEN } });
  fireEvent.click(screen.getByRole("button", { name: "Elevate" }));

  await expect(decision).resolves.toMatchObject({ state: "resolved" });
  expect(resolveCalls).toBe(2);
  const elevate = fetcher.mock.calls.find(([input]) => String(input) === ELEVATE_PATH);
  expect(JSON.parse(String(elevate?.[1]?.body))).toEqual({ token: TOKEN });
  await waitFor(() => expect(screen.queryByLabelText("Operator token")).toBeNull());
  for (const storage of [window.localStorage, window.sessionStorage]) {
    for (let index = 0; index < storage.length; index++) {
      const key = storage.key(index) ?? "";
      expect(storage.getItem(key) ?? "").not.toContain(TOKEN);
    }
  }
});

it("clears the input after a refused token and returns the refusal when dismissed", async () => {
  const fetcher = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) =>
    String(input) === ELEVATE_PATH
      ? json(403, { code: "elevation_refused", message: "operator elevation refused" })
      : json(403, { code: "assurance_insufficient", message: "elevate" }),
  );
  vi.stubGlobal("fetch", fetcher);
  render(<OperatorTokenPrompt />);

  const decision = resolveHITLApproval(item, "denied");
  const field = (await screen.findByLabelText("Operator token")) as HTMLInputElement;
  fireEvent.change(field, { target: { value: "wrong" } });
  fireEvent.click(screen.getByRole("button", { name: "Elevate" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("not accepted");
  expect(field.value).toBe("");

  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  await expect(decision).rejects.toMatchObject({ status: 403 });
});
