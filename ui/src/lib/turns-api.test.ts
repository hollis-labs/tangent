import { afterEach, expect, it, vi } from "vitest";
import { fetchReplyDelivery, replyTurn, retryReplyDelivery } from "./turns-api";

const view = {
  schema_version: 1,
  item_id: "item",
  version: 3,
  state: "queued",
  accepted: true,
  acknowledged: false,
  attempts: 1,
  reply_supported: true,
  interrupt_supported: true,
  interrupt_requested: false,
  retry_allowed: false,
};
afterEach(() => vi.unstubAllGlobals());
it("keeps participant projection reads separate from user mutations", async () => {
  const fetcher = vi.fn(
    async (_input: RequestInfo | URL, _init?: RequestInit) =>
      new Response(JSON.stringify(view), { status: 200 }),
  );
  vi.stubGlobal("fetch", fetcher);
  const result = await fetchReplyDelivery("item");
  expect(result.accepted).toBe(true);
  expect(result.state).toBe("queued");
  expect(fetcher.mock.calls[0]?.[0]).toBe("/api/plugins/messaging/delivery?item_id=item");
  await retryReplyDelivery("item", 3, "explicit-action", false);
  const init = fetcher.mock.calls[1]?.[1] as RequestInit;
  expect(init.method).toBe("POST");
  expect(JSON.parse(String(init.body))).toEqual({
    item_id: "item",
    expected_version: 3,
    action_id: "explicit-action",
    interrupt: false,
  });
  await replyTurn("item", {
    expected_revision: 4,
    action: "respond",
    response_text: "Exact text",
    interrupt: true,
  });
  const resolution = fetcher.mock.calls[2]?.[1] as RequestInit;
  expect(JSON.parse(String(resolution.body)).interrupt).toBe(true);
});
it("refuses foreign item and non-boolean capability metadata", async () => {
  for (const invalid of [
    { ...view, item_id: "other" },
    { ...view, interrupt_supported: "true" },
    { ...view, state: "invented" },
  ]) {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify(invalid), { status: 200 })),
    );
    await expect(fetchReplyDelivery("item")).rejects.toThrow("does not match");
  }
});
