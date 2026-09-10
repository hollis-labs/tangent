import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { HITLInbox, HITLOperatorItem } from "@/lib/hitl-api";
import HITLInboxRoute from "./HITLInbox";

class MockEventSource {
  static instances: MockEventSource[] = [];
  readonly url: string;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private listeners = new Map<string, Set<() => void>>();

  constructor(url: string) {
    this.url = url;
    MockEventSource.instances.push(this);
    queueMicrotask(() => this.onopen?.());
  }

  addEventListener(type: string, listener: () => void) {
    const listeners = this.listeners.get(type) ?? new Set();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  removeEventListener(type: string, listener: () => void) {
    this.listeners.get(type)?.delete(listener);
  }

  emit(type: string) {
    for (const listener of this.listeners.get(type) ?? []) listener();
  }

  close() {}
}

describe("<HITLInboxRoute>", () => {
  beforeEach(() => {
    MockEventSource.instances = [];
    vi.stubGlobal("EventSource", MockEventSource);
    window.sessionStorage.clear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("renders a useful always-addressable empty inbox", async () => {
    mockFetch(async () => jsonResponse(inbox([], [])));
    renderRoute("/hitl");

    expect(await screen.findByRole("heading", { name: "Human input" })).toBeInTheDocument();
    // The heading is static header markup, so awaiting it proves only that the
    // route mounted — not that the inbox fetch settled. Until it does, the list
    // is a QueueSkeleton and the empty state has not rendered, so this has to
    // wait for the empty state itself rather than assume the line above did.
    expect(await screen.findByText("No pending requests")).toBeInTheDocument();
    expect(
      screen.getByText("This surface is always available, even when no agent room is open."),
    ).toBeInTheDocument();
    expect(MockEventSource.instances[0]?.url).toBe("/api/hitl/events");
  });

  it("presents a deep-linked item and commits a plain decision immediately", async () => {
    const staged = approvalItem("item-1", "staged", 3);
    const presented = {
      ...staged,
      state: "presented" as const,
      revision: 4,
      presented_projection_revision: 3,
    };
    const resolved = {
      ...presented,
      state: "resolved" as const,
      revision: 5,
      queue_position: null,
      terminal_outcome: {
        contract_version: "1.0" as const,
        state: "resolved" as const,
        item_id: "item-1",
        interaction_revision: 5,
        resolution: {
          resolution_id: "resolution-1",
          response: { kind: "approval" as const, decision: "approved" as const },
          participant: {
            principal_ref: "local-operator",
            authority: "tangent-loopback",
            assurance: "loopback-unverified",
          },
          resolved_at: "2026-09-04T15:02:00Z",
          interaction_revision: 5,
          presented_projection_revision: 3,
        },
      },
    };
    let durable = inbox([staged], []);
    const requests: Array<{ path: string; body?: Record<string, unknown> }> = [];
    mockFetch(async (path, init) => {
      requests.push({ path, body: init?.body ? JSON.parse(String(init.body)) : undefined });
      if (path.endsWith("/present")) return jsonResponse(presented);
      if (path.endsWith("/resolve")) {
        durable = inbox([], [resolved]);
        return jsonResponse(resolved.terminal_outcome);
      }
      return jsonResponse(durable);
    });

    renderRoute("/hitl/items/item-1");
    expect(await screen.findByRole("heading", { name: "Approve deployment" })).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));

    expect(await screen.findByText("Approved")).toBeInTheDocument();
    const resolve = requests.find((request) => request.path.endsWith("/resolve"));
    expect(resolve?.body).toEqual({
      expected_revision: 4,
      presented_projection_revision: 3,
      response: { kind: "approval", decision: "approved" },
    });
    expect(requests.filter((request) => request.path.endsWith("/resolve"))).toHaveLength(1);
    expect(screen.getByRole("status")).toHaveTextContent(
      "Approved: Approve deployment. Decision committed.",
    );
  });

  it("hands the first approval off to the next canonical FIFO item and focuses its queue row", async () => {
    const first = queuedApproval("handoff-first", "Approve first release", 1);
    const second = queuedAttention("handoff-second", "Review second warning", 2);
    const third = queuedApproval("handoff-third", "Approve third release", 3);
    const resolved = resolvedApproval(first, "approved");
    let durable = inbox([first, second, third], []);
    mockFetch(async (path) => {
      if (path.endsWith("/resolve")) {
        durable = inbox([second, third], [resolved]);
        return jsonResponse(resolved.terminal_outcome);
      }
      return jsonResponse(durable);
    });

    renderRoute("/hitl/items/handoff-first");
    await screen.findByRole("heading", { name: "Approve first release" });
    const approve = screen.getByRole("button", { name: "Approve" });
    approve.focus();
    fireEvent.click(approve);

    await screen.findByRole("heading", { name: "Review second warning" });
    const nextRow = screen.getByRole("button", {
      name: /Review second warning.*Recovery worker/,
    });
    await waitFor(() => expect(document.activeElement).toBe(nextRow));
    expect(screen.getByTestId("path-probe")).toHaveTextContent("/hitl/items/handoff-second");
    expect(screen.getByRole("button", { name: "Pending 02" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(nextRow).toHaveAttribute("aria-current", "true");
    expect(screen.getByRole("status")).toHaveTextContent(
      "Approved: Approve first release. Decision committed.",
    );
    expect(MockEventSource.instances).toHaveLength(1);
  });

  it("advances a resolved middle approval to the next FIFO item after a note path", async () => {
    const first = queuedAttention("middle-first", "Review oldest warning", 1);
    const middle = queuedApproval("middle-selected", "Approve inspected release", 2);
    const last = queuedApproval("middle-last", "Approve newest release", 3);
    const resolved = resolvedApproval(middle, "approved", "Rollback evidence checked.");
    let durable = inbox([first, middle, last], []);
    let resolveBody: Record<string, unknown> | undefined;
    mockFetch(async (path, init) => {
      if (path.endsWith("/resolve")) {
        resolveBody = init?.body ? JSON.parse(String(init.body)) : undefined;
        durable = inbox([first, last], [resolved]);
        return jsonResponse(resolved.terminal_outcome);
      }
      return jsonResponse(durable);
    });

    renderRoute("/hitl/items/middle-selected");
    await screen.findByRole("heading", { name: "Approve inspected release" });
    fireEvent.click(screen.getByRole("button", { name: "Approve with note" }));
    const note = screen.getByLabelText("Approval note");
    const composer = note.closest("div");
    if (!composer) throw new Error("approval note composer missing");
    fireEvent.change(note, { target: { value: "  Rollback evidence checked.  " } });
    fireEvent.click(within(composer).getByRole("button", { name: "Approve with note" }));

    await screen.findByRole("heading", { name: "Approve newest release" });
    const nextRow = screen.getByRole("button", {
      name: /Approve newest release.*Release worker/,
    });
    await waitFor(() => expect(document.activeElement).toBe(nextRow));
    expect(screen.getByTestId("path-probe")).toHaveTextContent("/hitl/items/middle-last");
    expect(resolveBody).toMatchObject({
      response: {
        kind: "approval",
        decision: "approved",
        note: "Rollback evidence checked.",
      },
    });
  });

  it("preserves a kind filter while a resolved last-row attention reply hands off globally", async () => {
    const first = queuedApproval("filtered-first", "Approve oldest release", 1);
    const middle = queuedApproval("filtered-middle", "Approve middle release", 2);
    const last = queuedAttention("filtered-last", "Reply to newest warning", 3);
    const resolved = resolvedAttention(last, { reply: "Replica owner notified." });
    let durable = inbox([first, middle, last], []);
    mockFetch(async (path) => {
      if (path.endsWith("/resolve")) {
        durable = inbox([first, middle], [resolved]);
        return jsonResponse(resolved.terminal_outcome);
      }
      return jsonResponse(durable);
    });

    renderRoute("/hitl/items/filtered-last");
    await screen.findByRole("heading", { name: "Reply to newest warning" });
    fireEvent.click(screen.getByRole("button", { name: "Attention 01" }));
    fireEvent.click(screen.getByRole("button", { name: "Reply" }));
    const reply = screen.getByLabelText("Reply to caller");
    const composer = reply.closest("div");
    if (!composer) throw new Error("attention reply composer missing");
    fireEvent.change(reply, { target: { value: "Replica owner notified." } });
    fireEvent.click(
      within(composer).getByRole("button", { name: "Submit acknowledgement and reply" }),
    );

    await screen.findByRole("heading", { name: "Approve oldest release" });
    const nextHeading = screen.getByRole("heading", { name: "Approve oldest release" });
    await waitFor(() => expect(document.activeElement).toBe(nextHeading));
    expect(screen.getByTestId("path-probe")).toHaveTextContent("/hitl/items/filtered-first");
    expect(screen.getByRole("button", { name: "Attention 00" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(
      screen.queryByRole("button", { name: /Approve oldest release.*Release worker/ }),
    ).toBeNull();
  });

  it("moves the final attention note into Resolved and refocuses its retained deep link", async () => {
    vi.stubGlobal(
      "matchMedia",
      vi.fn().mockReturnValue({
        matches: false,
        media: "(min-width: 1024px)",
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    );
    const only = queuedAttention("last-pending", "Log final warning", 1);
    const resolved = resolvedAttention(only, { note: "Recorded for handoff." });
    let durable = inbox([only], []);
    mockFetch(async (path) => {
      if (path.endsWith("/resolve")) {
        durable = inbox([], [resolved]);
        return jsonResponse(resolved.terminal_outcome);
      }
      return jsonResponse(durable);
    });

    renderRoute("/hitl/items/last-pending");
    const originalHeading = await screen.findByRole("heading", { name: "Log final warning" });
    fireEvent.click(screen.getByRole("button", { name: "Add note" }));
    const note = screen.getByLabelText("Acknowledgement note");
    const composer = note.closest("div");
    if (!composer) throw new Error("attention note composer missing");
    fireEvent.change(note, { target: { value: "Recorded for handoff." } });
    fireEvent.click(
      within(composer).getByRole("button", { name: "Submit acknowledgement with note" }),
    );

    expect(await screen.findByText("Acknowledged")).toBeInTheDocument();
    await waitFor(() => expect(document.activeElement).toBe(originalHeading));
    expect(screen.getByTestId("path-probe")).toHaveTextContent("/hitl/items/last-pending");
    expect(screen.getByRole("button", { name: "Resolved 01" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByRole("button", { name: "Back to queue" })).toBeInTheDocument();
  });

  it("waits through an SSE-before-response race, then hands off once to the FIFO head", async () => {
    const first = queuedApproval("race-first", "Approve racing release", 1);
    const second = queuedAttention("race-second", "Review queued warning", 2);
    const resolved = resolvedApproval(first, "approved");
    let durable = inbox([first, second], []);
    let finishResolve: ((response: Response) => void) | undefined;
    const resolveResponse = new Promise<Response>((resolve) => {
      finishResolve = resolve;
    });
    let resolveCalls = 0;
    mockFetch(async (path) => {
      if (path.endsWith("/resolve")) {
        resolveCalls++;
        return resolveResponse;
      }
      return jsonResponse(durable);
    });

    renderRoute("/hitl/items/race-first");
    await screen.findByRole("heading", { name: "Approve racing release" });
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() => expect(resolveCalls).toBe(1));

    durable = inbox([second], [resolved]);
    act(() => MockEventSource.instances[0]?.emit("revision"));
    expect(await screen.findByText("Approved")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Pending 01" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await act(async () => {
      finishResolve?.(jsonResponse(resolved.terminal_outcome));
      await resolveResponse;
    });

    await screen.findByRole("heading", { name: "Review queued warning" });
    const nextRow = screen.getByRole("button", {
      name: /Review queued warning.*Recovery worker/,
    });
    await waitFor(() => expect(document.activeElement).toBe(nextRow));
    expect(screen.getByTestId("path-probe")).toHaveTextContent("/hitl/items/race-second");
    expect(resolveCalls).toBe(1);
  });

  it("does not overwrite a newer manual selection when a resolution response arrives late", async () => {
    const first = queuedApproval("manual-first", "Approve delayed release", 1);
    const second = queuedAttention("manual-second", "Review manually chosen warning", 2);
    const resolved = resolvedApproval(first, "approved");
    let durable = inbox([first, second], []);
    let finishResolve: ((response: Response) => void) | undefined;
    const resolveResponse = new Promise<Response>((resolve) => {
      finishResolve = resolve;
    });
    let resolveCalls = 0;
    mockFetch(async (path) => {
      if (path.endsWith("/resolve")) {
        resolveCalls++;
        durable = inbox([second], [resolved]);
        return resolveResponse;
      }
      return jsonResponse(durable);
    });

    renderRoute("/hitl/items/manual-first");
    await screen.findByRole("heading", { name: "Approve delayed release" });
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() => expect(resolveCalls).toBe(1));

    fireEvent.click(
      screen.getByRole("button", { name: /Review manually chosen warning.*Recovery worker/ }),
    );
    const chosenHeading = await screen.findByRole("heading", {
      name: "Review manually chosen warning",
    });
    await act(async () => {
      finishResolve?.(jsonResponse(resolved.terminal_outcome));
      await resolveResponse;
    });

    await waitFor(() => expect(chosenHeading).toHaveFocus());
    expect(screen.getByTestId("path-probe")).toHaveTextContent("/hitl/items/manual-second");
    expect(screen.getByRole("button", { name: "Pending 01" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByRole("status")).toHaveTextContent(
      "Approved: Approve delayed release. Decision committed.",
    );
    expect(MockEventSource.instances).toHaveLength(1);
  });

  it("does not overwrite a newer manual selection while the post-resolution refresh is pending", async () => {
    const first = queuedApproval("refresh-first", "Approve before delayed refresh", 1);
    const second = queuedAttention("refresh-second", "Review automatic successor", 2);
    const third = queuedApproval("refresh-third", "Approve manually chosen item", 3);
    const resolved = resolvedApproval(first, "approved");
    const initial = inbox([first, second, third], []);
    const durable = inbox([second, third], [resolved]);
    let inboxReads = 0;
    let finishRefresh: ((response: Response) => void) | undefined;
    const delayedRefresh = new Promise<Response>((resolve) => {
      finishRefresh = resolve;
    });
    mockFetch(async (path) => {
      if (path.endsWith("/resolve")) return jsonResponse(resolved.terminal_outcome);
      inboxReads++;
      if (inboxReads === 1) return jsonResponse(initial);
      if (inboxReads === 2) return delayedRefresh;
      return jsonResponse(durable);
    });

    renderRoute("/hitl/items/refresh-first");
    await screen.findByRole("heading", { name: "Approve before delayed refresh" });
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() => expect(inboxReads).toBe(2));

    fireEvent.click(
      screen.getByRole("button", { name: /Approve manually chosen item.*Release worker/ }),
    );
    const chosenHeading = await screen.findByRole("heading", {
      name: "Approve manually chosen item",
    });
    await act(async () => {
      finishRefresh?.(jsonResponse(durable));
      await delayedRefresh;
    });

    await waitFor(() => expect(chosenHeading).toHaveFocus());
    await waitFor(() => expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled());
    expect(screen.getByTestId("path-probe")).toHaveTextContent("/hitl/items/refresh-third");
    expect(screen.queryByRole("heading", { name: "Review automatic successor" })).toBeNull();
    expect(screen.getByRole("button", { name: "Pending 02" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(MockEventSource.instances).toHaveLength(1);
  });

  it("keeps actions preparing until a delayed durable presentation completes", async () => {
    const staged = approvalItem("item-delayed", "staged", 3);
    const presented = {
      ...staged,
      state: "presented" as const,
      revision: 4,
      presented_projection_revision: 3,
    };
    let finishPresentation: ((response: Response) => void) | undefined;
    const delayedPresentation = new Promise<Response>((resolve) => {
      finishPresentation = resolve;
    });
    let presentationCalls = 0;
    mockFetch(async (path) => {
      if (path.endsWith("/present")) {
        presentationCalls++;
        return delayedPresentation;
      }
      return jsonResponse(inbox([staged], []));
    });

    renderRoute("/hitl/items/item-delayed");
    await screen.findByRole("heading", { name: "Approve deployment" });
    await waitFor(() => expect(presentationCalls).toBe(1));
    expect(screen.getByText("Preparing an exact decision revision…")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Deny" })).toBeDisabled();

    await act(async () => {
      finishPresentation?.(jsonResponse(presented));
      await delayedPresentation;
    });

    await waitFor(() => expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled());
    expect(presentationCalls).toBe(1);
  });

  it("requires a non-empty note for a note variant", async () => {
    const presented = {
      ...approvalItem("item-note", "presented", 4),
      presented_projection_revision: 3,
    };
    const requests: Array<{ path: string; body?: Record<string, unknown> }> = [];
    mockFetch(async (path, init) => {
      requests.push({ path, body: init?.body ? JSON.parse(String(init.body)) : undefined });
      if (path.endsWith("/resolve")) {
        return jsonResponse({
          contract_version: "1.0",
          state: "resolved",
          item_id: "item-note",
          interaction_revision: 5,
        });
      }
      return jsonResponse(inbox([presented], []));
    });

    renderRoute("/hitl/items/item-note");
    await screen.findByRole("heading", { name: "Approve deployment" });
    fireEvent.click(screen.getByRole("button", { name: "Approve with note" }));
    const textarea = screen.getByLabelText("Approval note");
    const composer = textarea.closest("div");
    if (!composer) throw new Error("note composer not found");
    fireEvent.click(within(composer).getByRole("button", { name: "Approve with note" }));
    expect(screen.getByText("Write a note before submitting this variant.")).toBeInTheDocument();
    expect(requests.some((request) => request.path.endsWith("/resolve"))).toBe(false);

    fireEvent.change(textarea, { target: { value: "  Reviewed rollback evidence.  " } });
    fireEvent.click(within(composer).getByRole("button", { name: "Approve with note" }));
    await waitFor(() =>
      expect(requests.some((request) => request.path.endsWith("/resolve"))).toBe(true),
    );
    expect(requests.find((request) => request.path.endsWith("/resolve"))?.body).toMatchObject({
      response: { kind: "approval", decision: "approved", note: "Reviewed rollback evidence." },
    });
  });

  it("reports a stale client conflict and resynchronizes without overwriting", async () => {
    const presented = {
      ...approvalItem("item-stale", "presented", 4),
      presented_projection_revision: 3,
    };
    const resolved = {
      ...presented,
      state: "resolved" as const,
      revision: 5,
      queue_position: null,
      terminal_outcome: {
        contract_version: "1.0" as const,
        state: "resolved" as const,
        item_id: "item-stale",
        interaction_revision: 5,
        resolution: {
          resolution_id: "resolution-stale",
          response: { kind: "approval" as const, decision: "approved" as const },
          participant: {
            principal_ref: "local-operator",
            authority: "tangent-loopback",
            assurance: "loopback-unverified",
          },
          resolved_at: "2026-09-04T15:02:00Z",
          interaction_revision: 5,
          presented_projection_revision: 3,
        },
      },
    };
    const next = queuedAttention("item-after-stale", "Review after stale winner", 2);
    let reads = 0;
    mockFetch(async (path) => {
      if (path.endsWith("/resolve")) {
        return jsonResponse(
          {
            contract_version: "1.0",
            code: "stale_revision",
            message: "revision conflict",
            expected_revision: 4,
            actual_revision: 5,
            current_state: "resolved",
            terminal_outcome: resolved.terminal_outcome,
          },
          409,
        );
      }
      reads++;
      return jsonResponse(reads === 1 ? inbox([presented, next], []) : inbox([next], [resolved]));
    });

    renderRoute("/hitl/items/item-stale");
    await screen.findByRole("heading", { name: "Approve deployment" });
    fireEvent.click(screen.getByRole("button", { name: "Deny" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Another client already committed Approved at revision 5. Your action was not applied.",
    );
    expect(await screen.findByText("Approved")).toBeInTheDocument();
    expect(screen.getByTestId("path-probe")).toHaveTextContent("/hitl/items/item-stale");
    expect(screen.getByRole("button", { name: "Resolved 01" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.queryByRole("heading", { name: "Review after stale winner" })).toBeNull();
  });

  it("supports keyboard FIFO navigation and preserves a stable deep link", async () => {
    const first = approvalItem("item-first", "staged", 3);
    const second = {
      ...approvalItem("item-second", "staged", 3),
      queue_sequence: 2,
      queue_position: 2,
    };
    const third = {
      ...approvalItem("item-third", "staged", 3),
      queue_sequence: 3,
      queue_position: 3,
    };
    mockFetch(async (path) => {
      if (path.endsWith("/present")) {
        const selected = path.includes("item-second")
          ? second
          : path.includes("item-third")
            ? third
            : first;
        return jsonResponse({
          ...selected,
          state: "presented",
          revision: 4,
          presented_projection_revision: 3,
        });
      }
      return jsonResponse(inbox([first, second, third], []));
    });

    renderRoute("/hitl");
    await screen.findByRole("list", { name: "Pending requests, oldest first" });
    const queueButtons = screen.getAllByRole("button", { name: /Approve deployment/ });
    queueButtons[0].focus();
    fireEvent.keyDown(queueButtons[0], { key: "ArrowDown" });
    expect(await screen.findByTestId("path-probe")).toHaveTextContent("/hitl/items/item-second");
    await waitFor(() => expect(document.activeElement).toBe(queueButtons[1]));

    fireEvent.keyDown(document.activeElement as HTMLElement, { key: "ArrowDown" });
    expect(await screen.findByTestId("path-probe")).toHaveTextContent("/hitl/items/item-third");
    await waitFor(() => expect(document.activeElement).toBe(queueButtons[2]));
  });

  it("moves keyboard focus into the visible detail at a narrow viewport", async () => {
    vi.stubGlobal(
      "matchMedia",
      vi.fn().mockReturnValue({
        matches: false,
        media: "(min-width: 1024px)",
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }),
    );
    const first = approvalItem("item-mobile-first", "staged", 3);
    const secondBase = approvalItem("item-mobile-second", "staged", 3);
    const second = {
      ...secondBase,
      queue_sequence: 2,
      queue_position: 2,
      request_snapshot: { ...secondBase.request_snapshot, title: "Review narrow deployment" },
    };
    mockFetch(async (path) => {
      if (path.endsWith("/present")) {
        return jsonResponse({
          ...second,
          state: "presented",
          revision: 4,
          presented_projection_revision: 3,
        });
      }
      return jsonResponse(inbox([first, second], []));
    });

    renderRoute("/hitl");
    const firstButton = await screen.findByRole("button", {
      name: /Approve deployment.*Release worker/,
    });
    firstButton.focus();
    fireEvent.keyDown(firstButton, { key: "ArrowDown" });

    const detailHeading = await screen.findByRole("heading", { name: "Review narrow deployment" });
    await waitFor(() => expect(document.activeElement).toBe(detailHeading));
    expect(screen.getByRole("button", { name: "Back to queue" })).toBeInTheDocument();
  });

  it("resynchronizes new work from revision hints without replacing the open item", async () => {
    const selected = {
      ...approvalItem("item-open", "presented", 4),
      presented_projection_revision: 3,
    };
    const incoming = {
      ...approvalItem("item-new", "staged", 3),
      queue_sequence: 2,
      queue_position: 2,
    };
    let reads = 0;
    mockFetch(async () => {
      reads++;
      return jsonResponse(reads === 1 ? inbox([selected], []) : inbox([selected, incoming], []));
    });

    renderRoute("/hitl/items/item-open");
    await screen.findByRole("heading", { name: "Approve deployment" });
    MockEventSource.instances[0]?.emit("revision");

    await waitFor(() => expect(reads).toBeGreaterThanOrEqual(2));
    expect(screen.getAllByText("Approve deployment")).toHaveLength(3);
    expect(screen.getByTestId("path-probe")).toHaveTextContent("/hitl/items/item-open");
  });

  it("filters a mixed ledger as a stable projection without renumbering or duplicating items", async () => {
    const firstApproval = approvalItem("approval-first", "staged", 3);
    const attention = {
      ...attentionItem("attention-middle", "staged", 3),
      queue_sequence: 2,
      queue_position: 2,
    };
    const finalApprovalBase = approvalItem("approval-final", "staged", 3);
    const finalApproval = {
      ...finalApprovalBase,
      queue_sequence: 3,
      queue_position: 3,
      request_snapshot: { ...finalApprovalBase.request_snapshot, title: "Approve final release" },
    };
    mockFetch(async () => jsonResponse(inbox([firstApproval, attention, finalApproval], [])));

    renderRoute("/hitl");
    const queue = await screen.findByRole("list", { name: "Pending requests, oldest first" });
    expect(
      within(queue)
        .getAllByRole("button")
        .map((button) => button.textContent),
    ).toEqual([
      expect.stringContaining("01Approve deployment"),
      expect.stringContaining("02Review worker warning"),
      expect.stringContaining("03Approve final release"),
    ]);

    fireEvent.click(screen.getByRole("button", { name: "Attention 01" }));
    expect(within(queue).getAllByRole("button")).toHaveLength(1);
    expect(within(queue).getByRole("button")).toHaveTextContent("02Review worker warning");
    expect(within(queue).queryByText("Approve deployment")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "All 03" }));
    expect(within(queue).getAllByRole("button")).toHaveLength(3);
    expect(within(queue).getAllByText("Review worker warning")).toHaveLength(1);
  });

  it("commits a plain attention acknowledgement through the durable CAS", async () => {
    const presented = {
      ...attentionItem("attention-plain", "presented", 4),
      presented_projection_revision: 3,
    };
    const resolved = resolvedAttention(presented, {});
    let durable = inbox([presented], []);
    const requests: Array<{ path: string; body?: Record<string, unknown> }> = [];
    mockFetch(async (path, init) => {
      requests.push({ path, body: init?.body ? JSON.parse(String(init.body)) : undefined });
      if (path.endsWith("/resolve")) {
        durable = inbox([], [resolved]);
        return jsonResponse(resolved.terminal_outcome);
      }
      return jsonResponse(durable);
    });

    renderRoute("/hitl/items/attention-plain");
    await screen.findByRole("heading", { name: "Review worker warning" });
    expect(
      screen.getByText(
        "Acknowledgement records receipt only. It does not accept work or perform an external action.",
      ),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Acknowledge" }));

    expect(await screen.findByText("Acknowledged")).toBeInTheDocument();
    expect(requests.find((request) => request.path.endsWith("/resolve"))?.body).toEqual({
      expected_revision: 4,
      presented_projection_revision: 3,
      response: { kind: "attention", decision: "acknowledged" },
    });
    expect(screen.getByRole("status")).toHaveTextContent(
      "Acknowledged: Review worker warning. Tangent recorded receipt only; downstream action remains caller-owned.",
    );
  });

  it("requires and trims a custom-labelled attention reply, then renders it separately", async () => {
    const base = attentionItem("attention-reply", "presented", 4);
    const presented = {
      ...base,
      presented_projection_revision: 3,
      request_snapshot: {
        ...base.request_snapshot,
        action_labels: {
          acknowledge: "Mark seen",
          acknowledge_with_note: "Log context",
          reply: "Respond now",
        },
      },
    };
    const resolved = resolvedAttention(presented, { reply: "Worker restarted safely." });
    let durable = inbox([presented], []);
    const requests: Array<{ path: string; body?: Record<string, unknown> }> = [];
    mockFetch(async (path, init) => {
      requests.push({ path, body: init?.body ? JSON.parse(String(init.body)) : undefined });
      if (path.endsWith("/resolve")) {
        durable = inbox([], [resolved]);
        return jsonResponse(resolved.terminal_outcome);
      }
      return jsonResponse(durable);
    });

    renderRoute("/hitl/items/attention-reply");
    await screen.findByRole("heading", { name: "Review worker warning" });
    fireEvent.click(screen.getByRole("button", { name: "Respond now" }));
    const textarea = screen.getByLabelText("Reply to caller");
    expect(textarea).toHaveFocus();
    const composer = textarea.closest("div");
    if (!composer) throw new Error("attention reply composer not found");
    fireEvent.click(within(composer).getByRole("button", { name: "Submit Respond now" }));
    expect(screen.getByText("Write a reply before submitting.")).toBeInTheDocument();
    expect(requests.some((request) => request.path.endsWith("/resolve"))).toBe(false);

    fireEvent.change(textarea, { target: { value: "  Worker restarted safely.  " } });
    fireEvent.click(within(composer).getByRole("button", { name: "Submit Respond now" }));
    await waitFor(() =>
      expect(requests.some((request) => request.path.endsWith("/resolve"))).toBe(true),
    );
    expect(requests.find((request) => request.path.endsWith("/resolve"))?.body).toMatchObject({
      response: {
        kind: "attention",
        decision: "acknowledged",
        reply: "Worker restarted safely.",
      },
    });
    expect(await screen.findByText("Reply")).toBeInTheDocument();
    expect(screen.getByText("Worker restarted safely.")).toBeInTheDocument();
  });

  it("requires a non-blank acknowledgement note and sends it as note, not reply", async () => {
    const presented = {
      ...attentionItem("attention-note", "presented", 4),
      presented_projection_revision: 3,
    };
    const requests: Array<{ path: string; body?: Record<string, unknown> }> = [];
    mockFetch(async (path, init) => {
      requests.push({ path, body: init?.body ? JSON.parse(String(init.body)) : undefined });
      if (path.endsWith("/resolve")) {
        return jsonResponse({
          contract_version: "1.0",
          state: "resolved",
          item_id: "attention-note",
          interaction_revision: 5,
        });
      }
      return jsonResponse(inbox([presented], []));
    });

    renderRoute("/hitl/items/attention-note");
    await screen.findByRole("heading", { name: "Review worker warning" });
    fireEvent.click(screen.getByRole("button", { name: "Add note" }));
    let textarea = screen.getByLabelText("Acknowledgement note");
    expect(textarea).toHaveFocus();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Add note" })).toHaveFocus());
    fireEvent.click(screen.getByRole("button", { name: "Add note" }));
    textarea = screen.getByLabelText("Acknowledgement note");
    const composer = textarea.closest("div");
    if (!composer) throw new Error("attention note composer not found");
    fireEvent.change(textarea, { target: { value: "   " } });
    fireEvent.click(
      within(composer).getByRole("button", { name: "Submit acknowledgement with note" }),
    );
    expect(screen.getByText("Write a note before submitting.")).toBeInTheDocument();

    fireEvent.change(textarea, { target: { value: "  Logged for the next shift.  " } });
    fireEvent.click(
      within(composer).getByRole("button", { name: "Submit acknowledgement with note" }),
    );
    await waitFor(() =>
      expect(requests.some((request) => request.path.endsWith("/resolve"))).toBe(true),
    );
    expect(requests.find((request) => request.path.endsWith("/resolve"))?.body).toMatchObject({
      response: {
        kind: "attention",
        decision: "acknowledged",
        note: "Logged for the next shift.",
      },
    });
    expect(
      (
        requests.find((request) => request.path.endsWith("/resolve"))?.body?.response as
          | Record<string, unknown>
          | undefined
      )?.reply,
    ).toBeUndefined();
  });

  it("keeps an attention item pending through connection loss and a fresh browser mount", async () => {
    const presented = {
      ...attentionItem("attention-disconnect", "presented", 4),
      presented_projection_revision: 3,
    };
    let reads = 0;
    let presentationCalls = 0;
    mockFetch(async (path) => {
      if (path.endsWith("/present")) presentationCalls++;
      reads++;
      return jsonResponse(inbox([presented], []));
    });

    const firstMount = renderRoute("/hitl/items/attention-disconnect");
    await screen.findByRole("heading", { name: "Review worker warning" });
    expect(screen.getByRole("button", { name: "Acknowledge" })).toBeEnabled();
    act(() => MockEventSource.instances[0]?.onerror?.());
    expect(
      screen.getByText("Connection interrupted — durable state preserved"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Acknowledge" })).toBeEnabled();
    expect(screen.getByTestId("path-probe")).toHaveTextContent("/hitl/items/attention-disconnect");

    firstMount.unmount();
    renderRoute("/hitl/items/attention-disconnect");
    await screen.findByRole("heading", { name: "Review worker warning" });
    expect(screen.getByRole("button", { name: "Acknowledge" })).toBeEnabled();
    expect(presentationCalls).toBe(0);
    expect(reads).toBeGreaterThanOrEqual(2);
  });

  it("opens and closes inline evidence without changing lifecycle, connection, or FIFO order", async () => {
    const firstBase = approvalItem("evidence-first", "presented", 4);
    const first = {
      ...firstBase,
      presented_projection_revision: 3,
      request_snapshot: {
        ...firstBase.request_snapshot,
        title: "Approve evidence release",
        evidence: [
          {
            type: "markdown" as const,
            label: "Release checks",
            content: "## Result\n\nAll **42 checks** passed.",
          },
        ],
      },
    };
    const secondBase = approvalItem("evidence-second", "presented", 4);
    const second = {
      ...secondBase,
      queue_sequence: 2,
      queue_position: 2,
      presented_projection_revision: 3,
      request_snapshot: { ...secondBase.request_snapshot, title: "Approve later release" },
    };
    const requests: string[] = [];
    mockFetch(async (path) => {
      requests.push(path);
      return jsonResponse(inbox([first, second], []));
    });

    renderRoute("/hitl/items/evidence-first");
    await screen.findByRole("heading", { name: "Approve evidence release" });
    const opener = screen.getByRole("button", { name: /open case file/i });
    fireEvent.click(opener);

    expect(screen.getByRole("dialog", { name: "Evidence" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Result" })).toBeInTheDocument();
    expect(MockEventSource.instances).toHaveLength(1);
    expect(requests.some((path) => path.endsWith("/resolve") || path.endsWith("/present"))).toBe(
      false,
    );

    fireEvent.click(screen.getByRole("button", { name: "Close evidence" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Evidence" })).toBeNull());
    await waitFor(() => expect(opener).toHaveFocus());
    const queue = screen.getByRole("list", { name: "Pending requests, oldest first" });
    expect(
      within(queue)
        .getAllByRole("button")
        .map((button) => button.textContent),
    ).toEqual([
      expect.stringContaining("01Approve evidence release"),
      expect.stringContaining("02Approve later release"),
    ]);
    expect(requests).toEqual(["/api/hitl"]);
  });
});

function renderRoute(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route
          path="/hitl"
          element={
            <>
              <HITLInboxRoute />
              <PathProbe />
            </>
          }
        />
        <Route
          path="/hitl/items/:itemID"
          element={
            <>
              <HITLInboxRoute />
              <PathProbe />
            </>
          }
        />
      </Routes>
    </MemoryRouter>,
  );
}

function PathProbe() {
  const location = useLocation();
  return <div data-testid="path-probe">{location.pathname}</div>;
}

function approvalItem(
  itemID: string,
  state: HITLOperatorItem["state"],
  revision: number,
): HITLOperatorItem {
  return {
    contract_version: "1.0",
    surface_id: "surface_hitl_default",
    item_id: itemID,
    state,
    revision,
    queue_sequence: 1,
    queue_position: 1,
    request_snapshot: {
      contract_version: "1.0",
      kind: "approval",
      idempotency_key: `key:${itemID}`,
      title: "Approve deployment",
      summary: "All checks passed and the preview is ready.",
      request: "Approve or deny deployment to production.",
      recommendation: "Approve after checking rollback evidence.",
      impact: { approve: "The caller may deploy.", deny: "The caller keeps production unchanged." },
      source: {
        application_id: "codex",
        application_label: "Codex",
        agent_id: "worker-7",
        agent_label: "Release worker",
      },
      correlations: {
        project: { authority: "torque", id: "PRJ-1", label: "Tangent" },
        task: { authority: "torque", id: "CW-1", label: "Deploy" },
        session: { authority: "codex", id: "session-1" },
      },
    },
    enqueued_at: "2026-09-04T15:00:00Z",
    updated_at: "2026-09-04T15:00:00Z",
  };
}

function attentionItem(
  itemID: string,
  state: HITLOperatorItem["state"],
  revision: number,
): HITLOperatorItem {
  return {
    contract_version: "1.0",
    surface_id: "surface_hitl_default",
    item_id: itemID,
    state,
    revision,
    queue_sequence: 1,
    queue_position: 1,
    request_snapshot: {
      contract_version: "1.0",
      kind: "attention",
      idempotency_key: `key:${itemID}`,
      title: "Review worker warning",
      summary: "A durable warning needs operator attention.",
      request: "Acknowledge the warning after reading it.",
      source: {
        application_id: "codex",
        application_label: "Codex",
        agent_id: "worker-8",
        agent_label: "Recovery worker",
      },
      correlations: {
        task: { authority: "torque", id: "CW-2", label: "Recover service" },
      },
    },
    enqueued_at: "2026-09-04T15:01:00Z",
    updated_at: "2026-09-04T15:01:00Z",
  };
}

function queuedApproval(itemID: string, title: string, sequence: number): HITLOperatorItem {
  const item = approvalItem(itemID, "presented", 4);
  return {
    ...item,
    queue_sequence: sequence,
    queue_position: sequence,
    presented_projection_revision: 3,
    request_snapshot: { ...item.request_snapshot, title },
  };
}

function queuedAttention(itemID: string, title: string, sequence: number): HITLOperatorItem {
  const item = attentionItem(itemID, "presented", 4);
  return {
    ...item,
    queue_sequence: sequence,
    queue_position: sequence,
    presented_projection_revision: 3,
    request_snapshot: { ...item.request_snapshot, title },
  };
}

function resolvedApproval(
  item: HITLOperatorItem,
  decision: "approved" | "denied",
  note?: string,
): HITLOperatorItem {
  const projectionRevision = item.presented_projection_revision ?? 3;
  return {
    ...item,
    state: "resolved",
    revision: item.revision + 1,
    queue_position: null,
    terminal_outcome: {
      contract_version: "1.0",
      state: "resolved",
      item_id: item.item_id,
      interaction_revision: item.revision + 1,
      resolution: {
        resolution_id: `resolution-${item.item_id}`,
        response: { kind: "approval", decision, ...(note ? { note } : {}) },
        participant: {
          principal_ref: "local-operator",
          authority: "tangent-loopback",
          assurance: "loopback-unverified",
        },
        resolved_at: "2026-09-04T15:04:00Z",
        interaction_revision: item.revision + 1,
        presented_projection_revision: projectionRevision,
      },
    },
  };
}

function resolvedAttention(
  item: HITLOperatorItem,
  responseFields: { note?: string; reply?: string },
): HITLOperatorItem {
  const projectionRevision = item.presented_projection_revision ?? 3;
  return {
    ...item,
    state: "resolved",
    revision: item.revision + 1,
    queue_position: null,
    terminal_outcome: {
      contract_version: "1.0",
      state: "resolved",
      item_id: item.item_id,
      interaction_revision: item.revision + 1,
      resolution: {
        resolution_id: `resolution-${item.item_id}`,
        response: { kind: "attention", decision: "acknowledged", ...responseFields },
        participant: {
          principal_ref: "local-operator",
          authority: "tangent-loopback",
          assurance: "loopback-unverified",
        },
        resolved_at: "2026-09-04T15:04:00Z",
        interaction_revision: item.revision + 1,
        presented_projection_revision: projectionRevision,
      },
    },
  };
}

function inbox(pending: HITLOperatorItem[], history: HITLOperatorItem[]): HITLInbox {
  return {
    contract_version: "1.0",
    surface_id: "surface_hitl_default",
    revision: `${pending.map((item) => item.revision).join("-")}:${history.map((item) => item.revision).join("-")}`,
    synced_at: "2026-09-04T15:03:00Z",
    pending,
    history,
  };
}

function mockFetch(handler: (path: string, init?: RequestInit) => Promise<Response>) {
  vi.spyOn(globalThis, "fetch").mockImplementation((input, init) => handler(String(input), init));
}

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  } as Response;
}
