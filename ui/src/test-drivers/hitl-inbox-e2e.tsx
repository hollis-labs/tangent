import { createInterface } from "node:readline";
import { Window } from "happy-dom";

type DriverCommand = {
  command: "run" | "shutdown";
  baseURL?: string;
  approvalID?: string;
  denialID?: string;
  attentionID?: string;
  attentionNoteID?: string;
  attentionPlainID?: string;
};

type EventListener = () => void;

class ForbiddenNotification {
  constructor() {
    throw new Error("HITL inbox attempted to create an OS notification");
  }
}

const nodeFetch = globalThis.fetch.bind(globalThis);
const browserWindow = new Window({ url: "http://127.0.0.1/" });

function installBrowserGlobals() {
  const globals: Record<string, unknown> = {
    window: browserWindow,
    document: browserWindow.document,
    navigator: browserWindow.navigator,
    location: browserWindow.location,
    HTMLElement: browserWindow.HTMLElement,
    HTMLAnchorElement: browserWindow.HTMLAnchorElement,
    HTMLButtonElement: browserWindow.HTMLButtonElement,
    HTMLDivElement: browserWindow.HTMLDivElement,
    HTMLFormElement: browserWindow.HTMLFormElement,
    HTMLInputElement: browserWindow.HTMLInputElement,
    HTMLLabelElement: browserWindow.HTMLLabelElement,
    HTMLSelectElement: browserWindow.HTMLSelectElement,
    HTMLTextAreaElement: browserWindow.HTMLTextAreaElement,
    Element: browserWindow.Element,
    SVGElement: browserWindow.SVGElement,
    Node: browserWindow.Node,
    NodeFilter: browserWindow.NodeFilter,
    Event: browserWindow.Event,
    EventTarget: browserWindow.EventTarget,
    CustomEvent: browserWindow.CustomEvent,
    DOMException: browserWindow.DOMException,
    KeyboardEvent: browserWindow.KeyboardEvent,
    MouseEvent: browserWindow.MouseEvent,
    PointerEvent: browserWindow.PointerEvent,
    FocusEvent: browserWindow.FocusEvent,
    MutationObserver: browserWindow.MutationObserver,
    ResizeObserver: browserWindow.ResizeObserver,
    getComputedStyle: browserWindow.getComputedStyle.bind(browserWindow),
    requestAnimationFrame: browserWindow.requestAnimationFrame.bind(browserWindow),
    cancelAnimationFrame: browserWindow.cancelAnimationFrame.bind(browserWindow),
    Notification: ForbiddenNotification,
    IS_REACT_ACT_ENVIRONMENT: true,
  };
  for (const [name, value] of Object.entries(globals)) {
    Object.defineProperty(globalThis, name, { configurable: true, writable: true, value });
  }
  Object.assign(browserWindow, {
    open: () => {
      throw new Error("HITL inbox attempted to open a new window");
    },
    matchMedia: () => ({
      matches: true,
      media: "(min-width: 1024px)",
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent: () => true,
    }),
  });
}

class NetworkEventSource {
  static instances: NetworkEventSource[] = [];
  static revisionEvents = 0;
  readonly url: string;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private readonly controller = new AbortController();
  private readonly listeners = new Map<string, Set<EventListener>>();
  private closed = false;

  constructor(url: string) {
    this.url = url;
    NetworkEventSource.instances.push(this);
    queueMicrotask(() => void this.connect());
  }

  addEventListener(type: string, listener: EventListener) {
    const listeners = this.listeners.get(type) ?? new Set<EventListener>();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  removeEventListener(type: string, listener: EventListener) {
    this.listeners.get(type)?.delete(listener);
  }

  close() {
    this.closed = true;
    this.controller.abort();
  }

  private dispatch(type: string) {
    if (type === "revision") NetworkEventSource.revisionEvents++;
    for (const listener of this.listeners.get(type) ?? []) listener();
  }

  private async connect() {
    try {
      const response = await nodeFetch(new URL(this.url, browserWindow.location.href), {
        headers: { Accept: "text/event-stream" },
        signal: this.controller.signal,
      });
      if (!response.ok || !response.body) {
        throw new Error(`SSE returned ${response.status}`);
      }
      this.onopen?.();
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffered = "";
      let event = "message";
      while (!this.closed) {
        const chunk = await reader.read();
        if (chunk.done) break;
        buffered += decoder.decode(chunk.value, { stream: true });
        while (buffered.includes("\n")) {
          const newline = buffered.indexOf("\n");
          const line = buffered.slice(0, newline).replace(/\r$/, "");
          buffered = buffered.slice(newline + 1);
          if (line.startsWith("event:")) {
            event = line.slice("event:".length).trim();
          } else if (line === "") {
            this.dispatch(event);
            event = "message";
          }
        }
      }
    } catch (error) {
      if (!this.closed && !(error instanceof DOMException && error.name === "AbortError")) {
        this.onerror?.();
      }
    }
  }
}

function emit(event: Record<string, unknown>) {
  process.stdout.write(`${JSON.stringify(event)}\n`);
}

function invariant(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message);
}

function describeError(error: unknown): string {
  if (error instanceof AggregateError) {
    return `${error.stack ?? error.message}\n${error.errors.map(describeError).join("\n")}`;
  }
  return error instanceof Error ? (error.stack ?? error.message) : String(error);
}

async function callMCPTool(
  fetchFromApp: (input: string | URL | Request, init?: RequestInit) => Promise<Response>,
  name: string,
  args: Record<string, unknown>,
) {
  const response = await fetchFromApp("/mcp", {
    method: "POST",
    headers: {
      Accept: "application/json, text/event-stream",
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      jsonrpc: "2.0",
      id: `browser-${name}`,
      method: "tools/call",
      params: { name, arguments: args },
    }),
  });
  invariant(response.ok, `${name} transport returned ${response.status}`);
  const rpc = (await response.json()) as {
    result?: { isError?: boolean; content?: Array<{ text?: string }> };
    error?: unknown;
  };
  invariant(!rpc.error, `${name} JSON-RPC error: ${JSON.stringify(rpc.error)}`);
  invariant(!rpc.result?.isError, `${name} tool error: ${rpc.result?.content?.[0]?.text}`);
  const text = rpc.result?.content?.[0]?.text;
  invariant(text, `${name} returned no text result`);
  return JSON.parse(text) as Record<string, unknown>;
}

async function run(command: DriverCommand) {
  invariant(command.baseURL, "run requires baseURL");
  invariant(command.approvalID, "run requires approvalID");
  invariant(command.denialID, "run requires denialID");
  invariant(command.attentionID, "run requires attentionID");
  invariant(command.attentionNoteID, "run requires attentionNoteID");
  invariant(command.attentionPlainID, "run requires attentionPlainID");

  await browserWindow.happyDOM.setURL(`${command.baseURL}/hitl/items/${command.approvalID}`);
  const relativeFetch = (input: string | URL | Request, init?: RequestInit) => {
    const resolved =
      typeof input === "string" || input instanceof URL
        ? new URL(String(input), command.baseURL)
        : new Request(new URL(input.url, command.baseURL), input);
    return nodeFetch(resolved, init);
  };
  Object.assign(globalThis, { fetch: relativeFetch, EventSource: NetworkEventSource });
  Object.assign(browserWindow, { fetch: relativeFetch, EventSource: NetworkEventSource });

  const React = await import("react");
  const { configure, fireEvent, render, screen, waitFor, within } = await import(
    "@testing-library/react"
  );
  const { default: App } = await import("../App");
  configure({ asyncUtilTimeout: 5_000 });

  const transientSurfaceSelector = [
    "[data-sonner-toaster]",
    "[data-sonner-toast]",
    "[data-radix-toast-viewport]",
    "[data-radix-toast-root]",
    "[data-toast]",
    '[class*="toast" i]',
    '[id*="toast" i]',
    '[class*="snackbar" i]',
    '[id*="snackbar" i]',
    '[aria-label*="notification" i]',
  ].join(",");
  const forbiddenTransientSurfaces: string[] = [];
  const transientSurfaceObserver = new MutationObserver((records) => {
    for (const record of records) {
      for (const addedNode of record.addedNodes) {
        if (!(addedNode instanceof Element)) continue;
        const surface = addedNode.matches(transientSurfaceSelector)
          ? addedNode
          : addedNode.querySelector(transientSurfaceSelector);
        if (surface) forbiddenTransientSurfaces.push(surface.outerHTML.slice(0, 240));
      }
    }
  });
  transientSurfaceObserver.observe(document.body, { childList: true, subtree: true });

  const renderApprovalRoute = () => {
    browserWindow.history.replaceState({}, "", `/inbox/items/${command.approvalID}`);
    return render(React.createElement(App));
  };
  const joinedTitles = [
    "Approve joined release",
    "Acknowledge joined warning",
    "Race operator and caller",
    "Deny joined release",
    "Log joined warning",
    "Acknowledge joined notice",
  ];
  const nextPendingTitle = (currentTitle: string) => {
    const pendingQueue = screen.getByRole("list", { name: "Pending requests, oldest first" });
    const rows = within(pendingQueue).getAllByRole("button");
    const currentIndex = rows.findIndex((row) => row.textContent?.includes(currentTitle));
    invariant(currentIndex >= 0, `pending row not found for ${currentTitle}`);
    invariant(rows.length > 1, `no pending successor exists for ${currentTitle}`);
    const nextRow = rows[(currentIndex + 1) % rows.length];
    const nextTitle = joinedTitles.find((title) => nextRow.textContent?.includes(title));
    invariant(nextTitle, `could not identify successor row: ${nextRow.textContent}`);
    return nextTitle;
  };
  const settlePendingHandoff = async (title: string, count: number) => {
    // A committed reply remains in the detail pane until the operator selects
    // another request. The queue shrinks without silently moving their focus.
    await screen.findByRole("region", { name: "Committed outcome" });
    await screen.findByText(`${count} pending`);
    const pendingQueue = screen.getByRole("list", { name: "Pending requests, oldest first" });
    const row = within(pendingQueue)
      .getAllByRole("button")
      .find((button) => button.textContent?.includes(title));
    invariant(row, `pending successor missing for ${title}`);
    fireEvent.click(row);
    await screen.findByRole("heading", { name: title });
    await waitFor(() =>
      invariant(row.getAttribute("aria-current") === "true", `selection did not follow ${title}`),
    );
  };
  let rendered = renderApprovalRoute();

  await screen.findByRole("heading", { name: "Approve joined release" });
  await waitFor(() => {
    invariant(
      !(screen.getByRole("button", { name: "Approve with note" }) as HTMLButtonElement).disabled,
      "approval controls remained disabled after durable presentation",
    );
  });

  // The production component keeps mixed kinds in the same stored FIFO while
  // filters remain projections over the same queue_sequence values.
  const queue = screen.getByRole("list", { name: "Pending requests, oldest first" });
  const orderedRows = within(queue)
    .getAllByRole("button")
    .map((button) => button.textContent ?? "");
  invariant(orderedRows.length === 6, `joined queue has ${orderedRows.length} rows, want 6`);
  invariant(
    orderedRows.every((row, index) => row.includes(`#${index + 1}`)),
    `joined queue lost stored ordinals: ${JSON.stringify(orderedRows)}`,
  );
  const approvalRow = within(queue).getByRole("button", { name: /Approve joined release/ });
  const attentionRowBeforeResolution = within(queue).getByRole("button", {
    name: /Acknowledge joined warning/,
  });
  const approvalIndex = within(queue).getAllByRole("button").indexOf(approvalRow);
  const attentionIndex = within(queue).getAllByRole("button").indexOf(attentionRowBeforeResolution);
  approvalRow.focus();
  fireEvent.keyDown(approvalRow, { key: attentionIndex > approvalIndex ? "ArrowDown" : "ArrowUp" });
  await screen.findByRole("heading", { name: "Acknowledge joined warning" });
  await waitFor(() =>
    invariant(
      (browserWindow.document.activeElement as unknown) === attentionRowBeforeResolution,
      "FIFO keyboard focus did not follow selection",
    ),
  );
  fireEvent.keyDown(attentionRowBeforeResolution, {
    key: attentionIndex > approvalIndex ? "ArrowUp" : "ArrowDown",
  });
  await screen.findByRole("heading", { name: "Approve joined release" });
  fireEvent.change(screen.getByLabelText("Interaction type"), { target: { value: "document" } });
  await waitFor(() =>
    invariant(
      within(screen.getByRole("complementary", { name: "Inbox queue" })).queryAllByRole("button")
        .length === 0,
      "document filter included approval items",
    ),
  );
  fireEvent.change(screen.getByLabelText("Interaction type"), { target: { value: "all" } });

  // An MCP mutation made outside the component must appear and disappear only
  // after the production EventSource causes a durable resync.
  const sseHandle = await callMCPTool(relativeFetch, "tangent.hitl_enqueue", {
    contract_version: "1.0",
    kind: "attention",
    idempotency_key: "joined-browser-sse-only",
    title: "SSE-only incoming warning",
    summary: "This item is created outside the React adapter.",
    request: "Observe it through the durable revision stream.",
    source: { application_id: "sse-driver", agent_id: "external-worker" },
  });
  await screen.findByRole("button", { name: /SSE-only incoming warning/ });
  await callMCPTool(relativeFetch, "tangent.hitl_withdraw", {
    contract_version: "1.0",
    item_id: sseHandle.item_id,
    expected_revision: sseHandle.revision,
    reason: "complete SSE projection check",
    caller: { application_id: "sse-driver", principal_ref: "external-worker" },
  });
  await waitFor(() =>
    invariant(
      screen.queryByRole("button", { name: /SSE-only incoming warning/ }) === null,
      "withdrawn external item remained in the React projection",
    ),
  );

  // Normal browser detachment closes the real stream. A fresh production route
  // mount reconnects and retains the durable presentation token instead of
  // canceling or re-presenting the item.
  rendered.unmount();
  rendered = renderApprovalRoute();
  await screen.findByRole("heading", { name: "Approve joined release" });
  await waitFor(() =>
    invariant(
      !(screen.getByRole("button", { name: "Approve with note" }) as HTMLButtonElement).disabled,
      "approval controls did not recover after browser reconnect",
    ),
  );
  invariant(
    NetworkEventSource.instances.length >= 2,
    "browser did not establish a fresh SSE stream",
  );

  // Evidence stays in context, restores focus, sanitizes active markdown, and
  // exposes adapter failures inline instead of opening another window.
  const evidenceButton = screen.getByRole("button", { name: /open case file/i });
  fireEvent.click(evidenceButton);
  const drawer = await screen.findByRole("dialog", { name: "Evidence" });
  invariant(
    within(drawer).getByRole("heading", { name: "Validation" }),
    "markdown heading missing",
  );
  invariant(within(drawer).getByText("link withheld"), "unsafe markdown link was not withheld");
  invariant(within(drawer).getByText("Version bump"), "unified diff evidence missing");
  const previewButtons = within(drawer).getAllByRole("button", { name: "Request safe preview" });
  fireEvent.click(previewButtons[0]);
  await within(drawer).findByText(/No explicitly authorized host adapter/i);
  fireEvent.click(screen.getByRole("button", { name: "Close evidence" }));
  await waitFor(() =>
    invariant(
      (browserWindow.document.activeElement as unknown) === evidenceButton,
      "evidence focus was not restored",
    ),
  );

  fireEvent.click(screen.getByRole("button", { name: "Approve with note" }));
  const approvalNote = screen.getByLabelText("Approval note") as HTMLTextAreaElement;
  const approvalComposer = approvalNote.closest("div");
  invariant(approvalComposer, "approval note composer missing");
  fireEvent.change(approvalNote, { target: { value: "  release evidence reviewed  " } });
  const approvalSuccessor = nextPendingTitle("Approve joined release");
  fireEvent.click(within(approvalComposer).getByRole("button", { name: "Approve with note" }));
  await settlePendingHandoff(approvalSuccessor, 5);

  const pendingQueue = await screen.findByRole("list", { name: "Pending requests, oldest first" });
  const denialRow = within(pendingQueue).getByRole("button", { name: /Deny joined release/ });
  fireEvent.click(denialRow);
  await screen.findByRole("heading", { name: "Deny joined release" });
  await waitFor(() => {
    invariant(
      !(screen.getByRole("button", { name: "Deny" }) as HTMLButtonElement).disabled,
      "denial controls remained disabled after durable presentation",
    );
  });
  const denialSuccessor = nextPendingTitle("Deny joined release");
  fireEvent.click(screen.getByRole("button", { name: "Deny" }));
  await settlePendingHandoff(denialSuccessor, 4);

  const attentionRow = within(pendingQueue).getByRole("button", {
    name: /Acknowledge joined warning/,
  });
  fireEvent.click(attentionRow);
  await screen.findByRole("heading", { name: "Acknowledge joined warning" });
  await waitFor(() => {
    invariant(
      !(screen.getByRole("button", { name: "Respond to worker" }) as HTMLButtonElement).disabled,
      "attention controls remained disabled after durable presentation",
    );
  });
  invariant(
    screen.getByText(/Acknowledgement records receipt only/),
    "caller-owned downstream-action boundary is missing",
  );
  fireEvent.click(screen.getByRole("button", { name: "Respond to worker" }));
  const reply = screen.getByLabelText("Reply to caller") as HTMLTextAreaElement;
  invariant(
    (browserWindow.document.activeElement as unknown) === reply,
    "attention composer did not receive focus",
  );
  const replyComposer = reply.closest("div");
  invariant(replyComposer, "attention reply composer missing");
  fireEvent.change(reply, { target: { value: "  worker is safe  " } });
  const attentionSuccessor = nextPendingTitle("Acknowledge joined warning");
  fireEvent.click(within(replyComposer).getByRole("button", { name: "Submit Respond to worker" }));
  await settlePendingHandoff(attentionSuccessor, 3);

  const noteRow = within(
    await screen.findByRole("list", { name: "Pending requests, oldest first" }),
  ).getByRole("button", { name: /Log joined warning/ });
  fireEvent.click(noteRow);
  await screen.findByRole("heading", { name: "Log joined warning" });
  await waitFor(() =>
    invariant(
      !(screen.getByRole("button", { name: "Log context" }) as HTMLButtonElement).disabled,
      "attention note controls remained disabled",
    ),
  );
  fireEvent.click(screen.getByRole("button", { name: "Log context" }));
  const acknowledgementNote = screen.getByLabelText("Acknowledgement note") as HTMLTextAreaElement;
  const acknowledgementComposer = acknowledgementNote.closest("div");
  invariant(acknowledgementComposer, "attention note composer missing");
  fireEvent.change(acknowledgementNote, { target: { value: "   " } });
  fireEvent.click(
    within(acknowledgementComposer).getByRole("button", {
      name: "Submit Log context",
    }),
  );
  invariant(
    screen.getByText("Write a note before submitting."),
    "blank attention note was accepted",
  );
  fireEvent.change(acknowledgementNote, { target: { value: "  recorded for shift  " } });
  await waitFor(() =>
    invariant(
      !(
        within(acknowledgementComposer).getByRole("button", {
          name: "Submit Log context",
        }) as HTMLButtonElement
      ).disabled,
      "attention note submit remained disabled after valid input",
    ),
  );
  const noteSuccessor = nextPendingTitle("Log joined warning");
  fireEvent.click(
    within(acknowledgementComposer).getByRole("button", {
      name: "Submit Log context",
    }),
  );
  await settlePendingHandoff(noteSuccessor, 2);

  const plainRow = within(
    await screen.findByRole("list", { name: "Pending requests, oldest first" }),
  ).getByRole("button", { name: /Acknowledge joined notice/ });
  fireEvent.click(plainRow);
  await screen.findByRole("heading", { name: "Acknowledge joined notice" });
  await waitFor(() =>
    invariant(
      !(screen.getByRole("button", { name: "Mark seen" }) as HTMLButtonElement).disabled,
      "plain attention controls remained disabled",
    ),
  );
  const plainSuccessor = nextPendingTitle("Acknowledge joined notice");
  fireEvent.click(screen.getByRole("button", { name: "Mark seen" }));
  await settlePendingHandoff(plainSuccessor, 1);
  invariant(
    plainSuccessor === "Race operator and caller",
    `final handoff selected ${plainSuccessor}, want the remaining race item`,
  );
  await waitFor(() =>
    invariant(
      !(screen.getByRole("button", { name: "Approve" }) as HTMLButtonElement).disabled,
      "final FIFO handoff did not durably present the remaining item",
    ),
  );

  const durable = await relativeFetch("/api/hitl").then((response) => response.json());
  const historyIDs = (durable.history as Array<{ item_id: string }>).map((item) => item.item_id);
  invariant(historyIDs.includes(command.approvalID), "approval missing from durable history");
  invariant(historyIDs.includes(command.denialID), "denial missing from durable history");
  invariant(historyIDs.includes(command.attentionID), "attention missing from durable history");
  invariant(
    historyIDs.includes(command.attentionNoteID),
    "attention note missing from durable history",
  );
  invariant(
    historyIDs.includes(command.attentionPlainID),
    "plain attention missing from durable history",
  );
  await waitFor(() =>
    invariant(
      NetworkEventSource.revisionEvents >= 2,
      "live SSE did not deliver a post-mutation revision",
    ),
  );
  transientSurfaceObserver.disconnect();
  invariant(
    forbiddenTransientSurfaces.length === 0,
    `HITL inbox rendered a forbidden toast or notification surface: ${forbiddenTransientSurfaces.join("; ")}`,
  );

  rendered.unmount();
  emit({
    event: "completed",
    approvalID: command.approvalID,
    attentionID: command.attentionID,
    itemID: sseHandle.item_id,
    queueSequence: sseHandle.queue_sequence,
    pending: durable.pending.length,
    history: durable.history.length,
  });
}

installBrowserGlobals();
const input = createInterface({ input: process.stdin, crlfDelay: Number.POSITIVE_INFINITY });
input.on("line", (line) => {
  void (async () => {
    try {
      const command = JSON.parse(line) as DriverCommand;
      if (command.command === "shutdown") {
        emit({ event: "shutdown" });
        input.close();
        return;
      }
      await run(command);
    } catch (error) {
      emit({
        event: "driver-error",
        message: describeError(error),
      });
      process.exitCode = 1;
      input.close();
    }
  })();
});
input.on("close", () => {
  void browserWindow.happyDOM.abort();
});

emit({ event: "ready" });
