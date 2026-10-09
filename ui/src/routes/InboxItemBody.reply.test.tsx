import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import type { InboxEntry } from "@/lib/inbox-api";
import type { ReplyDeliveryView, TurnItemView } from "@/lib/turns-api";
import { InboxItemBody } from "./InboxItemBody";

const item: TurnItemView = {
  contract_version: "1.2",
  item_id: "item",
  session_id: "session",
  turn_id: "turn",
  agent_id: "agent",
  kind: "question",
  title: "Routed question",
  content: "Intact original",
  replyable: true,
  state: "presented",
  queue_sequence: 1,
  revision: 3,
  created_at: "2026-10-09T00:00:00Z",
  updated_at: "2026-10-09T00:00:00Z",
  delivery_state: "queued",
  source_message: {
    schema_version: 1,
    origin: "routed",
    endpoint_ref: "endpoint",
    channel: "owner",
    message_id: "source",
    sequence: 1,
    sender_urn: "msg://session/local/session",
  },
};
function entry(state = item.state): InboxEntry {
  return {
    sequence: 1,
    interaction: {
      interaction_id: item.item_id,
      definition_binding: { kind: "tangent.agent-turn" },
      request_snapshot: {},
      caller_scope: "synthetic",
      state,
      revision: item.revision,
      created_at: item.created_at,
      updated_at: item.updated_at,
    },
  };
}
function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
function delivery(overrides: Partial<ReplyDeliveryView> = {}): ReplyDeliveryView {
  return {
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
    ...overrides,
  };
}
function mount(state = item.state) {
  render(
    <MemoryRouter>
      <InboxItemBody entry={entry(state)} onChange={async () => {}} />
    </MemoryRouter>,
  );
}
afterEach(() => vi.unstubAllGlobals());
it("shows acceptance separately from delivery and sends only explicit resolution interrupt", async () => {
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      requests.push({ path, init });
      return json(path.includes("/delivery?") ? delivery() : item);
    }),
  );
  mount();
  expect(
    await screen.findByText("Accepted by Tether; delivery has not been confirmed."),
  ).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Retry reply" })).not.toBeInTheDocument();
  expect(requests.every((r) => !r.init?.method)).toBe(true);
  fireEvent.change(screen.getByRole("textbox", { name: "Your reply" }), {
    target: { value: "Exact typed response" },
  });
  fireEvent.click(screen.getByRole("checkbox", { name: "Interrupt the sender's active turn" }));
  fireEvent.click(screen.getByRole("button", { name: "Send reply" }));
  await waitFor(() => expect(requests.some((r) => r.path.endsWith("/reply"))).toBe(true));
  const sent = requests.find((r) => r.path.endsWith("/reply"));
  expect(JSON.parse(String(sent?.init?.body))).toMatchObject({
    expected_revision: 3,
    response_text: "Exact typed response",
    interrupt: true,
  });
  expect(requests.some((r) => r.path.endsWith("/ack") || r.path.endsWith("/retry"))).toBe(false);
});
it("retains one explicit retry action across ambiguous HTTP and never auto-retries terminal failure", async () => {
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  let retries = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      requests.push({ path, init });
      if (path.endsWith("/retry")) {
        retries++;
        return retries === 1 ? json({ code: "reply_unavailable" }, 503) : json(delivery());
      }
      return json(
        path.includes("/delivery?")
          ? delivery({
              state: "undeliverable",
              reason: "interrupt_unconfirmed",
              retry_allowed: true,
            })
          : { ...item, state: "resolved" },
      );
    }),
  );
  mount("resolved");
  const retry = await screen.findByRole("button", { name: "Retry reply" });
  expect(retries).toBe(0);
  fireEvent.click(retry);
  await screen.findByText(
    "Retry was not confirmed; repeat this action to check the same reply attempt",
  );
  fireEvent.click(screen.getByRole("button", { name: "Retry reply" }));
  await screen.findByText("Accepted by Tether; delivery has not been confirmed.");
  const sent = requests.filter((r) => r.path.endsWith("/retry"));
  expect(sent).toHaveLength(2);
  expect(sent[0]?.init?.body).toBe(sent[1]?.init?.body);
  expect(JSON.parse(String(sent[0]?.init?.body))).toMatchObject({
    item_id: "item",
    expected_version: 3,
    interrupt: false,
  });
  expect(screen.queryByRole("button", { name: "Retry reply" })).not.toBeInTheDocument();
  expect(requests.some((r) => r.path.endsWith("/reply") || r.path.endsWith("/ack"))).toBe(false);
});
it("keeps unknown or unavailable capability non-interrupting and non-replyable", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) =>
      json(
        String(input).includes("/delivery?")
          ? delivery({ state: "unknown", reply_supported: false, interrupt_supported: false })
          : item,
      ),
    ),
  );
  mount();
  await screen.findByText("Reply unknown");
  expect(
    screen.queryByRole("checkbox", { name: "Interrupt the sender's active turn" }),
  ).not.toBeInTheDocument();
  fireEvent.change(screen.getByRole("textbox", { name: "Your reply" }), {
    target: { value: "Typed" },
  });
  expect(screen.getByRole("button", { name: "Send reply" })).toBeDisabled();
  expect(screen.queryByRole("button", { name: "Retry reply" })).not.toBeInTheDocument();
});
