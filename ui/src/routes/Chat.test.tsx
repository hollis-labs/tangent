import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { turn } from "@/lib/chat-fixtures";
import { savePrefs } from "@/lib/chat-model";
import type { RosterAgent } from "@/lib/chat-roster-api";
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
function entry(id: string, over: Partial<RosterAgent> = {}): RosterAgent {
  return {
    launch_id: id,
    agent_id: id,
    display_name: id === "pm" ? "Portfolio Manager" : id,
    channel: "owner-inbox",
    session: null,
    ...over,
  };
}
const running = (at_turn_boundary: boolean) => ({
  id: "s1",
  state: "running",
  last_activity_at: "2026-10-12T10:00:00Z",
  at_turn_boundary,
});
interface Backend {
  turns: ReturnType<typeof inbox>;
  roster: RosterAgent[] | "missing";
  send?: (body: Record<string, string>) => Response;
  calls: Array<{ url: string; body?: string }>;
  reply?: Response;
}
function backend(b: Omit<Backend, "calls">): Backend {
  const full: Backend = { ...b, calls: [] };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      full.calls.push({ url, body: init?.body as string | undefined });
      if (url === "/api/plugins/messaging/chat/agents") {
        return full.roster === "missing"
          ? json({ code: "not_found" }, 404)
          : json({ available: true, agents: full.roster });
      }
      if (url === "/api/plugins/messaging/chat/send") {
        return full.send?.(JSON.parse(init?.body as string)) ?? json({ ok: true, mode: "turn" });
      }
      if (url.endsWith("/reply")) return full.reply ?? json({}, 200);
      return json(full.turns);
    }),
  );
  return full;
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
  savePrefs({ pinned: [], lastSeen: {}, pageSize: 50 });
});
afterEach(() => vi.unstubAllGlobals());

it("lists only the Tether roster and offers no add or remove controls", async () => {
  const withSummary = turn({
    item_id: "a",
    title: "SECRET TITLE",
    summary: "AI SUMMARY",
    content: "the final",
    state: "resolved",
  });
  const other = turn({ item_id: "o", agent_id: "arch", agent_label: "Architect" });
  backend({ turns: inbox([other], [withSummary]), roster: [entry("pm")] });
  mount();
  expect(await screen.findByText("the final")).toBeInTheDocument();
  expect(screen.queryByText("SECRET TITLE")).not.toBeInTheDocument();
  expect(screen.queryByText("AI SUMMARY")).not.toBeInTheDocument();
  const list = screen.getByRole("list");
  expect(within(list).getByText("Portfolio Manager")).toBeInTheDocument();
  expect(within(list).queryByText("Architect")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("Add chat agent")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Remove from Chat/ })).not.toBeInTheDocument();
});

it("ignores a roster saved in localStorage by the earlier version", async () => {
  localStorage.setItem(
    "tangent.chat.prefs.v1",
    JSON.stringify({ agents: ["legacy"], pinned: [], lastSeen: {}, pageSize: 50 }),
  );
  backend({
    turns: inbox([], [turn({ item_id: "l", agent_id: "legacy" })]),
    roster: [entry("pm")],
  });
  mount("/chat");
  expect(await screen.findByText("Portfolio Manager")).toBeInTheDocument();
  expect(screen.queryByText("legacy")).not.toBeInTheDocument();
});

it("disables the composer with 'is working' while the session is mid-turn", async () => {
  backend({
    turns: inbox([], [turn({ item_id: "a", state: "resolved" })]),
    roster: [entry("pm", { session: running(false) })],
  });
  mount();
  await screen.findByText("hello");
  expect(screen.getByLabelText("Message")).toBeDisabled();
  expect(screen.getByPlaceholderText(/is working/)).toBeInTheDocument();
});

it("replies with respond against the pending final and keeps the draft on 409", async () => {
  const be = backend({
    turns: inbox([turn({ item_id: "p", revision: 7, session_id: "s1" })]),
    roster: [entry("pm", { session: running(false) })],
    reply: json({ code: "conflict", message: "stale revision" }, 409),
  });
  mount();
  const box = await screen.findByLabelText("Message");
  await waitFor(() => expect(box).toBeEnabled());
  fireEvent.change(box, { target: { value: "go ahead" } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await screen.findByRole("alert");
  const reply = be.calls.find((c) => c.url === "/api/turns/items/p/reply");
  expect(JSON.parse(reply?.body ?? "{}")).toEqual({
    expected_revision: 7,
    action: "respond",
    response_text: "go ahead",
  });
  expect(be.calls.some((c) => c.url.endsWith("/chat/send"))).toBe(false);
  expect(screen.getByLabelText("Message")).toHaveValue("go ahead");
});

it("sends instead of replying when the pending final belongs to an ended session", async () => {
  const be = backend({
    turns: inbox([turn({ item_id: "old", revision: 3, session_id: "dead" })]),
    roster: [entry("pm", { session: { ...running(true), id: "dead", state: "completed" } })],
  });
  mount();
  expect(
    await screen.findByText("Session ended; this message is no longer awaiting a reply."),
  ).toBeInTheDocument();
  const box = screen.getByLabelText("Message");
  await waitFor(() => expect(box).toBeEnabled());
  fireEvent.change(box, { target: { value: "wake up" } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(screen.getByLabelText("Message")).toHaveValue(""));
  expect(be.calls.some((c) => c.url.endsWith("/reply"))).toBe(false);
  const sent = be.calls.find((c) => c.url.endsWith("/chat/send"));
  expect(JSON.parse(sent?.body ?? "{}")).toMatchObject({ launch_id: "pm", text: "wake up" });
});

it("re-reads the roster at send time and does not reply to a session that just ended", async () => {
  const be = backend({
    turns: inbox([turn({ item_id: "p", revision: 3, session_id: "s1" })]),
    roster: [entry("pm", { session: running(true) })],
  });
  mount();
  const box = await screen.findByLabelText("Message");
  await waitFor(() => expect(box).toBeEnabled());
  be.roster = [entry("pm", { session: { ...running(true), state: "stopped" } })];
  fireEvent.change(box, { target: { value: "hi" } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(be.calls.some((c) => c.url.endsWith("/chat/send"))).toBe(true));
  expect(be.calls.some((c) => c.url.endsWith("/reply"))).toBe(false);
});

it("initiates a chat through Tether when nothing is pending", async () => {
  const be = backend({ turns: inbox([], []), roster: [entry("pm")] });
  mount();
  const box = await screen.findByLabelText("Message");
  await waitFor(() => expect(box).toBeEnabled());
  fireEvent.change(box, { target: { value: "start here" } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(screen.getByLabelText("Message")).toHaveValue(""));
  const sent = be.calls.find((c) => c.url === "/api/plugins/messaging/chat/send");
  const body = JSON.parse(sent?.body ?? "{}");
  expect(body).toMatchObject({ launch_id: "pm", text: "start here" });
  expect(body.idempotency_key).toBeTruthy();
  expect(be.calls.some((c) => c.url.endsWith("/reply"))).toBe(false);
});

it("keeps the draft on agent_working and retries with the same idempotency key", async () => {
  let attempts = 0;
  const be = backend({
    turns: inbox([], []),
    roster: [entry("pm", { session: running(true) })],
    send: () =>
      ++attempts === 1 ? json({ code: "agent_working" }, 409) : json({ ok: true, mode: "turn" }),
  });
  mount();
  const box = await screen.findByLabelText("Message");
  await waitFor(() => expect(box).toBeEnabled());
  fireEvent.change(box, { target: { value: "ping" } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(/is working/);
  expect(screen.getByLabelText("Message")).toHaveValue("ping");
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(screen.getByLabelText("Message")).toHaveValue(""));
  const keys = be.calls
    .filter((c) => c.url.endsWith("/chat/send"))
    .map((c) => JSON.parse(c.body ?? "{}").idempotency_key);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
});

it("says the roster is unavailable when the endpoint is absent", async () => {
  backend({ turns: inbox([], []), roster: "missing" });
  mount("/chat");
  expect(await screen.findByText(/Chat roster unavailable/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
});

it("says so when Tether has no chat agents", async () => {
  backend({ turns: inbox([], []), roster: [] });
  mount("/chat");
  expect(await screen.findAllByText(/No chat agents are configured/)).not.toHaveLength(0);
});

it("pages older history on demand", async () => {
  savePrefs({ pinned: [], lastSeen: {}, pageSize: 5 });
  const history = Array.from({ length: 8 }, (_, i) =>
    turn({
      item_id: `h${i}`,
      content: `msg ${i}`,
      state: "resolved",
      created_at: `2026-10-12T10:0${i}:00Z`,
    }),
  );
  backend({ turns: inbox([], history), roster: [entry("pm")] });
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
  backend({ turns: inbox([], [item]), roster: [entry("pm")] });
  mount();
  await screen.findByText("hello");
  expect(screen.queryByText("Stage trace")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Message details" }));
  expect(await screen.findByText("Stage trace")).toBeInTheDocument();
});

it("marks an unseen agent and clears it on open", async () => {
  backend({
    turns: inbox(
      [],
      [
        turn({ item_id: "a", state: "resolved" }),
        turn({ item_id: "b", agent_id: "arch", state: "resolved" }),
      ],
    ),
    roster: [entry("pm"), entry("arch")],
  });
  mount("/chat/pm");
  await screen.findByText("hello");
  expect(screen.getAllByRole("img", { name: "New messages" })).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: /^(New messages )?arch/ }));
  await waitFor(() =>
    expect(screen.queryByRole("img", { name: "New messages" })).not.toBeInTheDocument(),
  );
});

async function sendFrom(text: string) {
  const box = await screen.findByLabelText("Message");
  await waitFor(() => expect(box).toBeEnabled());
  fireEvent.change(box, { target: { value: text } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
}

it("shows a sent message as the operator's, and keeps it after the agent replies and a reload", async () => {
  const be = backend({ turns: inbox([], []), roster: [entry("pm")] });
  const first = render(
    <MemoryRouter initialEntries={["/chat/pm"]}>
      <Routes>
        <Route path="/chat/:agentID" element={<Chat />} />
      </Routes>
    </MemoryRouter>,
  );
  await sendFrom("start here");
  const log = await screen.findByRole("log");
  expect(await within(log).findByText("start here")).toBeInTheDocument();
  expect(within(log).getByText("You")).toBeInTheDocument();
  expect(JSON.parse(localStorage.getItem("tangent.chat.sent.v1") ?? "[]")).toMatchObject([
    { agentID: "pm", text: "start here", status: "sent" },
  ]);
  first.unmount();

  // The agent answers, and the page is opened again.
  be.turns = inbox(
    [],
    [turn({ item_id: "r", content: "READY", created_at: "2099-01-01T00:00:00Z" })],
  );
  mount();
  const reloaded = await screen.findByRole("log");
  expect(await within(reloaded).findByText("READY")).toBeInTheDocument();
  const text = reloaded.textContent ?? "";
  expect(text.indexOf("start here")).toBeGreaterThanOrEqual(0);
  expect(text.indexOf("start here")).toBeLessThan(text.indexOf("READY"));
});

it("shows a failed send as failed, keeps the text, and retries it once under the same key", async () => {
  let attempts = 0;
  const be = backend({
    turns: inbox([], []),
    roster: [entry("pm")],
    send: () => (++attempts === 1 ? json({ code: "tether_unreachable" }, 502) : json({ ok: true })),
  });
  mount();
  await sendFrom("please");
  const log = await screen.findByRole("log");
  expect(await within(log).findByText(/Not sent/)).toBeInTheDocument();
  expect(within(log).getByText("please")).toBeInTheDocument();
  expect(screen.getByLabelText("Message")).toHaveValue("please");

  fireEvent.click(within(log).getByRole("button", { name: "Retry" }));
  await waitFor(() => expect(within(log).queryByText(/Not sent/)).not.toBeInTheDocument());
  await waitFor(() => expect(screen.getByLabelText("Message")).toHaveValue(""));
  expect(within(log).getAllByText("please")).toHaveLength(1);
  const keys = be.calls
    .filter((c) => c.url.endsWith("/chat/send"))
    .map((c) => JSON.parse(c.body ?? "{}").idempotency_key);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
});

it("shows a send left unconfirmed by a reload as failed with a retry", async () => {
  localStorage.setItem(
    "tangent.chat.sent.v1",
    JSON.stringify([
      {
        key: "k1",
        agentID: "pm",
        text: "half sent",
        at: "2026-10-12T10:00:00Z",
        status: "sending",
      },
    ]),
  );
  backend({ turns: inbox([], []), roster: [entry("pm")] });
  mount();
  const log = await screen.findByRole("log");
  expect(await within(log).findByText("half sent")).toBeInTheDocument();
  expect(within(log).getByRole("button", { name: "Retry" })).toBeInTheDocument();
});

it("does not show a sent message twice when it also arrives as a reply resolution", async () => {
  localStorage.setItem(
    "tangent.chat.sent.v1",
    JSON.stringify([
      { key: "k1", agentID: "pm", text: "do it", at: "2026-10-12T10:00:00Z", status: "sent" },
    ]),
  );
  backend({
    turns: inbox(
      [],
      [
        turn({
          item_id: "a",
          state: "resolved",
          created_at: "2026-10-12T09:00:00Z",
          resolution: {
            resolution_id: "res-a",
            action: "respond",
            response_text: "do it",
            resolved_at: "2026-10-12T10:00:02Z",
            resolved_by: "operator",
          },
        }),
      ],
    ),
    roster: [entry("pm")],
  });
  mount();
  const log = await screen.findByRole("log");
  await within(log).findByText("hello");
  expect(within(log).getAllByText("do it")).toHaveLength(1);
});

// ── Responsiveness guards (#102 behaviours, kept under the shared event hub) ──

class EmitES {
  static all: EmitES[] = [];
  private listeners = new Map<string, Array<() => void>>();
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor() {
    EmitES.all.push(this);
  }
  addEventListener(type: string, fn: () => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }
  removeEventListener() {}
  emit(type: string) {
    for (const fn of this.listeners.get(type) ?? []) fn();
  }
  close() {}
}

function etagBackend(be: Backend) {
  const inner = globalThis.fetch as unknown as (u: string, i?: RequestInit) => Promise<Response>;
  const seen: Array<{ url: string; ifNoneMatch?: string }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const headers = (init?.headers ?? {}) as Record<string, string>;
      seen.push({ url, ifNoneMatch: headers["If-None-Match"] });
      if (url === "/api/turns") {
        be.calls.push({ url });
        if (headers["If-None-Match"] === '"t1"')
          return new Response(null, { status: 304, headers: { ETag: '"t1"' } });
        return new Response(JSON.stringify(be.turns), {
          status: 200,
          headers: { "Content-Type": "application/json", ETag: '"t1"' },
        });
      }
      return inner(url, init);
    }),
  );
  return seen;
}

it("keeps message DOM and refetches nothing while the operator types", async () => {
  const be = backend({
    turns: inbox([], [turn({ item_id: "a", state: "resolved", content: "the final" })]),
    roster: [entry("pm")],
  });
  const seen = etagBackend(be);
  mount();
  const log = await screen.findByRole("log");
  const message = await within(log).findByText("the final");
  const before = seen.length;
  const box = screen.getByLabelText("Message");
  for (const text of ["h", "he", "hel", "hell", "hello"])
    fireEvent.change(box, { target: { value: text } });
  expect((box as HTMLTextAreaElement).value).toBe("hello");
  expect(within(screen.getByRole("log")).getByText("the final")).toBe(message);
  expect(seen.length).toBe(before);
});

it("revalidates turns with If-None-Match on a revision event and keeps the DOM on 304", async () => {
  EmitES.all = [];
  vi.stubGlobal("EventSource", EmitES);
  const be = backend({
    turns: inbox([], [turn({ item_id: "a", state: "resolved", content: "the final" })]),
    roster: [entry("pm")],
  });
  const seen = etagBackend(be);
  mount();
  const message = await within(await screen.findByRole("log")).findByText("the final");
  for (const source of EmitES.all) source.emit("revision");
  await waitFor(() =>
    expect(
      seen.filter((c) => c.url === "/api/turns" && c.ifNoneMatch === '"t1"').length,
    ).toBeGreaterThan(0),
  );
  expect(within(screen.getByRole("log")).getByText("the final")).toBe(message);
});

it("does not poll the roster while the tab is hidden", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const visibility = vi.spyOn(document, "visibilityState", "get");
  try {
    visibility.mockReturnValue("hidden");
    const be = backend({ turns: inbox([], []), roster: [entry("pm")] });
    mount("/chat");
    await vi.advanceTimersByTimeAsync(200);
    const rosterCalls = () => be.calls.filter((c) => c.url.endsWith("/chat/agents")).length;
    const initial = rosterCalls();
    await vi.advanceTimersByTimeAsync(120_000);
    expect(rosterCalls()).toBe(initial);
  } finally {
    visibility.mockRestore();
    vi.useRealTimers();
  }
});
