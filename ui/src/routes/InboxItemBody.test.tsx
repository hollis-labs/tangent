import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { HITLOperatorItem } from "@/lib/hitl-api";
import type { InboxEntry } from "@/lib/inbox-api";
import { InboxItemBody } from "./InboxItemBody";

function attention(id = "attention-1"): HITLOperatorItem {
  return {
    contract_version: "1.0",
    surface_id: "operator",
    item_id: id,
    state: "staged",
    revision: 3,
    queue_sequence: 1,
    queue_position: 1,
    request_snapshot: {
      contract_version: "1.0",
      kind: "attention",
      idempotency_key: id,
      title: `Notice ${id}`,
      summary: "Acknowledge this notice",
      request: "Review before acknowledging",
      source: { application_id: "test", agent_id: "agent" },
      action_labels: { acknowledge: "Mark seen" },
    },
    enqueued_at: "2026-10-09T00:00:00Z",
    updated_at: "2026-10-09T00:00:00Z",
  };
}

function entry(item: HITLOperatorItem): InboxEntry {
  return {
    sequence: item.queue_sequence,
    interaction: {
      interaction_id: item.item_id,
      definition_binding: { kind: "tangent.hitl-item" },
      request_snapshot: { ...item.request_snapshot },
      caller_scope: "test",
      state: item.state,
      revision: item.revision,
      created_at: item.enqueued_at,
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

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

describe("<InboxItemBody> presentation", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    window.sessionStorage.clear();
  });

  it("finishes a deferred presentation after an SSE revision refresh without consuming delivery", async () => {
    const staged = attention();
    const presented: HITLOperatorItem = {
      ...staged,
      state: "presented",
      revision: 4,
      presented_projection_revision: 3,
    };
    let current = staged;
    const presentation = deferred<Response>();
    const requests: Array<{ path: string; body?: unknown }> = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input);
        requests.push({ path, body: init?.body ? JSON.parse(String(init.body)) : undefined });
        if (path.endsWith("/present")) return presentation.promise;
        if (path.endsWith("/resolve")) return json({});
        return json(current);
      }),
    );
    const onChange = vi.fn(async () => {});
    const view = (item: HITLOperatorItem) => (
      <MemoryRouter>
        <InboxItemBody entry={entry(item)} onChange={onChange} />
      </MemoryRouter>
    );
    const rendered = render(view(staged));
    await waitFor(() =>
      expect(requests.some((request) => request.path.endsWith("/present"))).toBe(true),
    );
    expect(screen.getByRole("button", { name: "Mark seen" })).toBeDisabled();

    // Inbox's SSE refresh replaces the selected entry and triggers another GET
    // while the presentation POST's response is still in flight.
    current = presented;
    rendered.rerender(view(presented));
    await waitFor(() =>
      expect(
        requests.filter((request) => request.path === "/api/hitl/items/attention-1"),
      ).toHaveLength(2),
    );
    await screen.findByText("Rev 4");
    expect(screen.getByRole("button", { name: "Mark seen" })).toBeDisabled();
    await act(async () => presentation.resolve(json(presented)));
    await waitFor(() => expect(screen.getByRole("button", { name: "Mark seen" })).toBeEnabled());
    expect(requests.filter((request) => request.path.endsWith("/present"))).toEqual([
      {
        path: "/api/hitl/items/attention-1/present",
        body: {
          expected_revision: 3,
          presented_projection_revision: 3,
          connection_id: expect.any(String),
        },
      },
    ]);
    expect(requests.some((request) => request.path.endsWith("/resolve"))).toBe(false);
    expect(onChange).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Mark seen" }));
    await waitFor(() => expect(onChange).toHaveBeenCalledOnce());
    expect(requests.find((request) => request.path.endsWith("/resolve"))).toEqual({
      path: "/api/hitl/items/attention-1/resolve",
      body: {
        expected_revision: 4,
        presented_projection_revision: 3,
        response: { kind: "attention", decision: "acknowledged" },
      },
    });
  });

  it("reports a deferred presentation refusal after a same-item refresh", async () => {
    const staged = attention();
    let current = staged;
    const presentation = deferred<Response>();
    const post = vi.fn(() => presentation.promise);
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        return String(input).endsWith("/present") ? post() : json(current);
      }),
    );
    const view = (item: HITLOperatorItem) => (
      <MemoryRouter>
        <InboxItemBody entry={entry(item)} onChange={async () => {}} />
      </MemoryRouter>
    );
    const rendered = render(view(staged));
    await waitFor(() => expect(post).toHaveBeenCalledOnce());
    current = { ...staged, revision: 4 };
    rendered.rerender(view(current));
    await screen.findByText("Rev 4");
    await act(async () =>
      presentation.resolve(
        json(
          {
            code: "revision_conflict",
            message: "Presentation revision changed",
          },
          409,
        ),
      ),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("Presentation revision changed");
    expect(screen.getByRole("button", { name: "Mark seen" })).toBeDisabled();
    expect(post).toHaveBeenCalledOnce();
  });

  it("keeps a newer refreshed revision when an older presentation response arrives", async () => {
    const staged = attention();
    const presented: HITLOperatorItem = {
      ...staged,
      state: "presented",
      revision: 4,
      presented_projection_revision: 3,
    };
    let current = staged;
    const presentation = deferred<Response>();
    const requests: Array<{ path: string; body?: unknown }> = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input);
        requests.push({ path, body: init?.body ? JSON.parse(String(init.body)) : undefined });
        if (path.endsWith("/present")) return presentation.promise;
        return json(current);
      }),
    );
    const view = (item: HITLOperatorItem) => (
      <MemoryRouter>
        <InboxItemBody entry={entry(item)} onChange={async () => {}} />
      </MemoryRouter>
    );
    const rendered = render(view(staged));
    await waitFor(() =>
      expect(requests.some((request) => request.path.endsWith("/present"))).toBe(true),
    );
    current = { ...presented, revision: 5 };
    rendered.rerender(view(current));
    await screen.findByText("Rev 5");
    await act(async () => presentation.resolve(json(presented)));
    await waitFor(() => expect(screen.getByRole("button", { name: "Mark seen" })).toBeEnabled());
    expect(screen.getByText("Rev 5")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Mark seen" }));
    await waitFor(() =>
      expect(requests.find((request) => request.path.endsWith("/resolve"))?.body).toEqual({
        expected_revision: 5,
        presented_projection_revision: 3,
        response: { kind: "attention", decision: "acknowledged" },
      }),
    );
  });

  it("does not let an unmounted item's response unlock the newly selected item", async () => {
    const first = attention("first");
    const second = attention("second");
    const firstResponse = deferred<Response>();
    const secondResponse = deferred<Response>();
    const posts: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input);
        if (path.endsWith("/present")) {
          posts.push(path);
          return path.includes("/first/") ? firstResponse.promise : secondResponse.promise;
        }
        return json(path.endsWith("/first") ? first : second);
      }),
    );
    const view = (item: HITLOperatorItem) => (
      <MemoryRouter>
        <InboxItemBody key={item.item_id} entry={entry(item)} onChange={async () => {}} />
      </MemoryRouter>
    );
    const rendered = render(view(first));
    await waitFor(() => expect(posts).toEqual(["/api/hitl/items/first/present"]));
    rendered.rerender(view(second));
    await waitFor(() => expect(posts).toContain("/api/hitl/items/second/present"));
    await act(async () =>
      firstResponse.resolve(
        json({ ...first, state: "presented", revision: 4, presented_projection_revision: 3 }),
      ),
    );
    expect(screen.getByRole("heading", { name: "Notice second" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Mark seen" })).toBeDisabled();
    await act(async () =>
      secondResponse.resolve(
        json({ ...second, state: "presented", revision: 4, presented_projection_revision: 3 }),
      ),
    );
    await waitFor(() => expect(screen.getByRole("button", { name: "Mark seen" })).toBeEnabled());
    expect(screen.getByRole("heading", { name: "Notice second" })).toBeInTheDocument();
  });

  it("does not reopen a terminal item when its earlier presentation completes", async () => {
    const staged = attention();
    let current = staged;
    const presentation = deferred<Response>();
    const post = vi.fn(() => presentation.promise);
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        return String(input).endsWith("/present") ? post() : json(current);
      }),
    );
    const view = (item: HITLOperatorItem) => (
      <MemoryRouter>
        <InboxItemBody entry={entry(item)} onChange={async () => {}} />
      </MemoryRouter>
    );
    const rendered = render(view(staged));
    await waitFor(() => expect(post).toHaveBeenCalledOnce());
    current = { ...staged, state: "canceled", revision: 5 };
    rendered.rerender(view(current));
    await screen.findByText("Rev 5");
    await act(async () =>
      presentation.resolve(
        json({ ...staged, state: "presented", revision: 4, presented_projection_revision: 3 }),
      ),
    );
    expect(screen.getByText("Rev 5")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Mark seen" })).not.toBeInTheDocument();
    expect(post).toHaveBeenCalledOnce();
  });
});
