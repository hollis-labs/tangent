import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { turn } from "@/lib/chat-fixtures";
import { savePrefs } from "@/lib/chat-model";
import type { TurnItemView } from "@/lib/turns-api";
import Chat from "./Chat";

class FakeES {
  addEventListener() {}
  removeEventListener() {}
  close() {}
}
function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
function inbox(pending: TurnItemView[], history: TurnItemView[] = []) {
  return {
    contract_version: "1.2",
    surface_id: "s",
    revision: "r",
    synced_at: "2026-10-12T12:00:00Z",
    pending,
    history,
    total_pending: pending.length,
    total_terminal: history.length,
  };
}
function mount(path = "/chat/pm") {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/chat" element={<Chat />} />
        <Route path="/chat/:agentID" element={<Chat />} />
      </Routes>
    </MemoryRouter>,
  );
}
beforeEach(() => {
  localStorage.clear();
  vi.stubGlobal("EventSource", FakeES);
  savePrefs({ agents: ["pm"], pinned: [], lastSeen: {}, pageSize: 50 });
});
afterEach(() => vi.unstubAllGlobals());

it("shows only chat agents and renders messages without header or summary", async () => {
  const withSummary = turn({
    item_id: "a",
    title: "SECRET TITLE",
    summary: "AI SUMMARY",
    content: "the final",
    state: "resolved",
  });
  const other = turn({ item_id: "o", agent_id: "arch", agent_label: "Architect" });
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => json(inbox([other], [withSummary]))),
  );
  mount();
  expect(await screen.findByText("the final")).toBeInTheDocument();
  expect(screen.queryByText("SECRET TITLE")).not.toBeInTheDocument();
  expect(screen.queryByText("AI SUMMARY")).not.toBeInTheDocument();
  expect(within(screen.getByRole("list")).queryByText("Architect")).not.toBeInTheDocument();
});

it("disables the composer while no pending final exists", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => json(inbox([], [turn({ item_id: "a", state: "resolved" })]))),
  );
  mount();
  await screen.findByText("hello");
  expect(screen.getByLabelText("Message")).toBeDisabled();
  expect(screen.getByPlaceholderText(/is working/)).toBeInTheDocument();
});

it("replies with respond against the pending final and keeps the draft on 409", async () => {
  const calls: Array<{ url: string; body?: string }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ url, body: init?.body as string | undefined });
      if (url.endsWith("/reply")) return json({ code: "conflict", message: "stale revision" }, 409);
      return json(inbox([turn({ item_id: "p", revision: 7 })]));
    }),
  );
  mount();
  const box = await screen.findByLabelText("Message");
  await waitFor(() => expect(box).toBeEnabled());
  fireEvent.change(box, { target: { value: "go ahead" } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await screen.findByRole("alert");
  const reply = calls.find((c) => c.url === "/api/turns/items/p/reply");
  expect(JSON.parse(reply?.body ?? "{}")).toEqual({
    expected_revision: 7,
    action: "respond",
    response_text: "go ahead",
  });
  expect(screen.getByLabelText("Message")).toHaveValue("go ahead");
});

it("pages older history on demand", async () => {
  savePrefs({ agents: ["pm"], pinned: [], lastSeen: {}, pageSize: 5 });
  const history = Array.from({ length: 8 }, (_, i) =>
    turn({
      item_id: `h${i}`,
      content: `msg ${i}`,
      state: "resolved",
      created_at: `2026-10-12T10:0${i}:00Z`,
    }),
  );
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => json(inbox([], history))),
  );
  mount();
  await screen.findByText("msg 7");
  expect(screen.queryByText("msg 2")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Load older messages" }));
  expect(screen.getByText("msg 0")).toBeInTheDocument();
});

it("puts trace and source behind the info modal", async () => {
  const item = turn({
    item_id: "a",
    state: "resolved",
    stage_trace: [
      { stage_id: "summarize", stage_version: "1", outcome: "passed", duration_ms: 12 },
    ],
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => json(inbox([], [item]))),
  );
  mount();
  await screen.findByText("hello");
  expect(screen.queryByText("Stage trace")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Message details" }));
  expect(await screen.findByText("Stage trace")).toBeInTheDocument();
});

it("marks an unseen agent and clears it on open", async () => {
  savePrefs({ agents: ["pm", "arch"], pinned: [], lastSeen: {}, pageSize: 50 });
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      json(
        inbox(
          [],
          [
            turn({ item_id: "a", state: "resolved" }),
            turn({ item_id: "b", agent_id: "arch", state: "resolved" }),
          ],
        ),
      ),
    ),
  );
  mount("/chat/pm");
  await screen.findByText("hello");
  expect(screen.getAllByRole("img", { name: "New messages" })).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: /^(New messages )?arch/ }));
  await waitFor(() =>
    expect(screen.queryByRole("img", { name: "New messages" })).not.toBeInTheDocument(),
  );
});
