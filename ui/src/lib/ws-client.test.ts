import { describe, expect, it, vi } from "vitest";

import type { UiEvent } from "./ui-channel";
import { connect, getTabClientID, getTabClientKind, seedShellClientIdentity } from "./ws-client";

class MockWebSocket extends EventTarget {
  static instances: MockWebSocket[] = [];
  static OPEN = 1;
  static CONNECTING = 0;
  static CLOSING = 2;
  static CLOSED = 3;

  readyState = MockWebSocket.CONNECTING;
  sent: string[] = [];
  url: string;

  constructor(url: string) {
    super();
    this.url = url;
    MockWebSocket.instances.push(this);
  }

  send(payload: string) {
    this.sent.push(payload);
  }

  close(_code?: number, _reason?: string) {
    this.readyState = MockWebSocket.CLOSED;
  }

  emitOpen() {
    this.readyState = MockWebSocket.OPEN;
    this.dispatchEvent(new Event("open"));
  }

  emitMessage(data: string) {
    this.dispatchEvent(new MessageEvent("message", { data }));
  }

  emitClose(code = 1000, reason = "") {
    this.readyState = MockWebSocket.CLOSED;
    this.dispatchEvent(new CloseEvent("close", { code, reason }));
  }
}

describe("ws-client", () => {
  it("adapts UI frames on the exact room socket, independently of drafts and room refusals", () => {
    MockWebSocket.instances = [];
    vi.stubGlobal("WebSocket", MockWebSocket);
    const onError = vi.fn(),
      onServerError = vi.fn(),
      onEnvelope = vi.fn();
    const client = connect("synthetic-room", {
      onEnvelope,
      onError,
      onServerError,
      heartbeatMs: 0,
      clientID: "routing-label",
    });
    const transport = client.uiTransport;
    expect(transport).toBeDefined();
    const events: UiEvent[] = [];
    const unsubscribe = transport?.subscribe((event) => events.push(event));
    try {
      expect(transport?.send({ type: "view.active", active: true })).toBe(false);
      const first = MockWebSocket.instances[0];
      first.emitOpen();
      first.emitMessage(JSON.stringify({ type: "view.published", view_revision: 1 }));
      const command = {
        type: "ui.command",
        command_id: "synthetic-command",
        view_revision: 1,
        name: "open_modal",
        scope: "ephemeral",
        arguments: { id: "synthetic-item" },
      };
      first.emitMessage(JSON.stringify(command));
      expect(events).toEqual([
        { type: "attached" },
        { type: "view.published", view_revision: 1 },
        command,
      ]);
      expect(
        transport?.send({
          type: "ui.ack",
          ack: { command_id: "synthetic-command", view_revision: 1, status: "applied" },
        }),
      ).toBe(true);
      expect(JSON.parse(first.sent[0]).type).toBe("ui.ack");
      first.emitMessage(JSON.stringify({ ...command, view_revision: 0 }));
      expect(onError).toHaveBeenCalledTimes(1);
      first.emitMessage(
        JSON.stringify({
          type: "error",
          code: "ui_command_rejected",
          message: "verified binding unavailable",
        }),
      );
      expect(events.at(-1)).toEqual({ type: "refused" });
      expect(onServerError).not.toHaveBeenCalled();
      expect(onEnvelope).not.toHaveBeenCalled();
      client.switchRoom("synthetic-other-room");
      expect(events.at(-1)).toEqual({ type: "detached" });
      const before = events.length;
      first.emitMessage(JSON.stringify(command));
      expect(events).toHaveLength(before);
      expect(transport?.connected()).toBe(false);
      MockWebSocket.instances[1].emitOpen();
      expect(transport?.connected()).toBe(true);
      client.close();
      expect(events.at(-1)).toEqual({ type: "detached" });
      expect(transport?.connected()).toBe(false);
      expect(first.sent.some((frame) => JSON.parse(frame).type === "draft")).toBe(false);
    } finally {
      unsubscribe?.();
      client.close();
      vi.unstubAllGlobals();
    }
  });

  it("ignores stale socket events after switchRoom", () => {
    const originalWS = globalThis.WebSocket;
    Object.assign(MockWebSocket, {
      OPEN: 1,
      CONNECTING: 0,
      CLOSING: 2,
      CLOSED: 3,
      instances: [],
    });
    vi.stubGlobal("WebSocket", MockWebSocket);

    const onOpen = vi.fn();
    const onClose = vi.fn();
    const onEnvelope = vi.fn();
    const onError = vi.fn();

    try {
      const client = connect("room-a", {
        wsURL: "ws://example.test/ws",
        clientID: "tab-1",
        heartbeatMs: 0,
        onOpen,
        onClose,
        onEnvelope,
        onError,
      });

      const first = MockWebSocket.instances[0];
      expect(first.url).toContain("roomID=room-a");

      client.switchRoom("room-b");

      const second = MockWebSocket.instances[1];
      expect(second.url).toContain("roomID=room-b");

      first.emitOpen();
      first.emitMessage(
        JSON.stringify({
          type: "envelope",
          envelopeId: "old-env",
          revision: 1,
          envelope: { stale: true },
        }),
      );
      first.emitClose(1006, "stale");

      expect(onOpen).not.toHaveBeenCalled();
      expect(onEnvelope).not.toHaveBeenCalled();
      expect(onClose).not.toHaveBeenCalled();

      second.emitOpen();
      second.emitMessage(
        JSON.stringify({
          type: "envelope",
          envelopeId: "new-env",
          revision: 2,
          envelope: { fresh: true },
        }),
      );
      second.emitClose(1000, "done");

      expect(onOpen).toHaveBeenCalledTimes(1);
      expect(onEnvelope).toHaveBeenCalledWith("new-env", { fresh: true }, 2);
      expect(onClose).toHaveBeenCalledWith("done");
      expect(onError).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
      globalThis.WebSocket = originalWS;
    }
  });

  it("echoes the presentation revision on response and cancel frames", () => {
    const originalWS = globalThis.WebSocket;
    Object.assign(MockWebSocket, { instances: [] });
    vi.stubGlobal("WebSocket", MockWebSocket);

    try {
      const client = connect("room-a", {
        wsURL: "ws://example.test/ws",
        clientID: "tab-1",
        heartbeatMs: 0,
        onEnvelope: vi.fn(),
      });
      const socket = MockWebSocket.instances[0];
      socket.emitOpen();

      expect(client.submitResponse("env-1", { ok: true }, 7)).toBe(true);
      expect(client.cancel("env-2", 8)).toBe(true);

      expect(socket.sent.map((frame) => JSON.parse(frame))).toEqual([
        { type: "response", envelopeId: "env-1", revision: 7, response: { ok: true } },
        { type: "cancel", envelopeId: "env-2", revision: 8 },
      ]);

      socket.readyState = MockWebSocket.CLOSING;
      expect(client.cancel("env-3", 9)).toBe(false);
    } finally {
      vi.unstubAllGlobals();
      globalThis.WebSocket = originalWS;
    }
  });
  it("carries the tab client id and reports connection, sync, and refusal frames", () => {
    const originalWS = globalThis.WebSocket;
    Object.assign(MockWebSocket, { instances: [] });
    vi.stubGlobal("WebSocket", MockWebSocket);

    const onConnectionState = vi.fn();
    const onSync = vi.fn();
    const onServerError = vi.fn();
    const onError = vi.fn();

    try {
      const client = connect("room-a", {
        wsURL: "ws://example.test/ws",
        clientID: "tab-7",
        heartbeatMs: 0,
        onEnvelope: vi.fn(),
        onConnectionState,
        onSync,
        onServerError,
        onError,
      });
      const socket = MockWebSocket.instances[0];
      // The client id is what tells the server a reconnect is a refresh of
      // this tab rather than a second tab.
      expect(socket.url).toContain("clientID=tab-7");
      expect(client.clientID()).toBe("tab-7");
      socket.emitOpen();

      socket.emitMessage(
        JSON.stringify({
          type: "connection",
          connectionId: "conn-1",
          role: "observer",
          roomId: "room-a",
          lease: { connection_id: "conn-2", label: "tab a" },
          connections: [
            { connection_id: "conn-1", role: "observer", self: true },
            { connection_id: "conn-2", role: "resolver", label: "tab a" },
          ],
        }),
      );
      expect(onConnectionState).toHaveBeenCalledWith(
        expect.objectContaining({
          connectionId: "conn-1",
          role: "observer",
          lease: expect.objectContaining({ connection_id: "conn-2" }),
        }),
      );

      socket.emitMessage(
        JSON.stringify({
          type: "sync",
          sync: {
            room_id: "room-a",
            surface_revision: 12,
            presentations: [
              {
                envelope_id: "env-1",
                durable: true,
                interaction_id: "int-1",
                interaction_revision: 3,
              },
            ],
          },
        }),
      );
      expect(onSync).toHaveBeenCalledWith(expect.objectContaining({ surface_revision: 12 }));

      socket.emitMessage(
        JSON.stringify({
          type: "error",
          code: "resolver_lease_held",
          message: "held by tab a",
          envelopeId: "env-1",
          lease: { connection_id: "conn-2", label: "tab a" },
        }),
      );
      expect(onServerError).toHaveBeenCalledWith(
        expect.objectContaining({ code: "resolver_lease_held", envelopeId: "env-1" }),
      );

      expect(client.claimResolver(true)).toBe(true);
      expect(client.releaseResolver()).toBe(true);
      expect(client.resync()).toBe(true);
      expect(socket.sent.map((frame) => JSON.parse(frame))).toEqual([
        { type: "claim_resolver", takeover: true },
        { type: "release_resolver" },
        { type: "resync" },
      ]);

      // A frame type this build does not know about is a forward-compatible
      // server, not a protocol fault.
      socket.emitMessage(JSON.stringify({ type: "future-frame", whatever: 1 }));
      expect(onError).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
      globalThis.WebSocket = originalWS;
    }
  });

  it("attaches as an observer when asked", () => {
    const originalWS = globalThis.WebSocket;
    Object.assign(MockWebSocket, { instances: [] });
    vi.stubGlobal("WebSocket", MockWebSocket);
    try {
      connect("room-a", {
        wsURL: "ws://example.test/ws",
        clientID: "tab-9",
        observer: true,
        heartbeatMs: 0,
        onEnvelope: vi.fn(),
      });
      expect(MockWebSocket.instances[0].url).toContain("role=observer");
    } finally {
      vi.unstubAllGlobals();
      globalThis.WebSocket = originalWS;
    }
  });
});

describe("ws-client draft revisions", () => {
  it("advances the draft sequence per envelope so a caller never supplies one", () => {
    const originalWS = globalThis.WebSocket;
    Object.assign(MockWebSocket, {
      OPEN: 1,
      CONNECTING: 0,
      CLOSING: 2,
      CLOSED: 3,
      instances: [],
    });
    vi.stubGlobal("WebSocket", MockWebSocket);

    try {
      const client = connect("room-a", {
        wsURL: "ws://example.test/ws",
        clientID: "tab-1",
        heartbeatMs: 0,
        onEnvelope: vi.fn(),
      });
      const socket = MockWebSocket.instances[0];
      socket.emitOpen();
      socket.sent.length = 0;

      client.saveDraft("board-1", { filters: ["a"] });
      client.saveDraft("board-1", { filters: ["a", "b"] });
      // A second envelope keeps its own sequence — the store counts per
      // interaction, so a shared counter would conflict on the first save.
      client.saveDraft("board-2", { filters: [] });

      const drafts = socket.sent
        .map((raw: string) => JSON.parse(raw))
        .filter((frame: { type: string }) => frame.type === "draft");

      expect(drafts).toHaveLength(3);
      expect(drafts[0]).toMatchObject({ envelopeId: "board-1", draftRevision: 1 });
      expect(drafts[1]).toMatchObject({ envelopeId: "board-1", draftRevision: 2 });
      expect(drafts[2]).toMatchObject({ envelopeId: "board-2", draftRevision: 1 });
      expect(drafts[0].draft).toEqual({ filters: ["a"] });
    } finally {
      vi.unstubAllGlobals();
      globalThis.WebSocket = originalWS;
    }
  });

  // CW-20260910-0134. The draft sequence is the one revision this client has to
  // PREDICT rather than echo, so losing the count — a reload, a reconnect, a
  // second tab — used to be unrecoverable: it guessed 1, was refused, and
  // healed only by accident after as many clicks as it had missed drafts.
  //
  // The refusal now carries the revision the record wanted, so one correction
  // gets it back.
  function draftHarness() {
    Object.assign(MockWebSocket, {
      OPEN: 1,
      CONNECTING: 0,
      CLOSING: 2,
      CLOSED: 3,
      instances: [],
    });
    vi.stubGlobal("WebSocket", MockWebSocket);
    const onServerError = vi.fn();
    const client = connect("room-a", {
      wsURL: "ws://example.test/ws",
      clientID: "tab-1",
      heartbeatMs: 0,
      onEnvelope: vi.fn(),
      onServerError,
    });
    const socket = MockWebSocket.instances[0];
    socket.emitOpen();
    socket.sent.length = 0;
    const drafts = () =>
      socket.sent
        .map((raw: string) => JSON.parse(raw))
        .filter((frame: { type: string }) => frame.type === "draft");
    const refuse = (expectedRevision?: number) =>
      socket.emitMessage(
        JSON.stringify({
          type: "error",
          code: "stale_draft",
          message: "the draft revision this update was built on is no longer current",
          envelopeId: "board-1",
          revision: 1,
          ...(expectedRevision === undefined ? {} : { expectedRevision }),
        }),
      );
    return { client, socket, drafts, refuse, onServerError };
  }

  it("resynchronizes to the revision the record wanted and re-sends once", () => {
    const originalWS = globalThis.WebSocket;
    try {
      const { client, drafts, refuse, onServerError } = draftHarness();

      // A client that lost its count sends 1 against a record holding five.
      client.saveDraft("board-1", { filters: ["a"] });
      refuse(6);

      const sent = drafts();
      expect(sent).toHaveLength(2);
      // The retry carries the SAME payload at the corrected revision — a draft
      // is a whole snapshot, so replaying it is replaying the participant's
      // current view, not an older one.
      expect(sent[1]).toMatchObject({ envelopeId: "board-1", draftRevision: 6 });
      expect(sent[1].draft).toEqual({ filters: ["a"] });
      // Repaired, so the consumer is not told about a failure that did not
      // survive. It would have to distinguish "your work is lost" from "a
      // number was off by five", which is what this client is here to do.
      expect(onServerError).not.toHaveBeenCalled();

      // And the sequence continues from the corrected point.
      client.saveDraft("board-1", { filters: ["a", "b"] });
      expect(drafts()[2]).toMatchObject({ draftRevision: 7 });
    } finally {
      vi.unstubAllGlobals();
      globalThis.WebSocket = originalWS;
    }
  });

  it("retries once and then reports, so two writers cannot loop", () => {
    const originalWS = globalThis.WebSocket;
    try {
      const { client, drafts, refuse, onServerError } = draftHarness();

      client.saveDraft("board-1", { filters: ["a"] });
      refuse(6);
      expect(drafts()).toHaveLength(2);

      // A second refusal means someone else is genuinely moving the record.
      // Grinding against that turns a refusal the participant can act on into
      // a loop they cannot.
      refuse(7);
      expect(drafts()).toHaveLength(2);
      expect(onServerError).toHaveBeenCalledTimes(1);
      expect(onServerError.mock.calls[0][0]).toMatchObject({
        code: "stale_draft",
        expectedRevision: 7,
      });
    } finally {
      vi.unstubAllGlobals();
      globalThis.WebSocket = originalWS;
    }
  });

  it("refills the retry budget on the next draft, so one hiccup is not a tab's only recovery", () => {
    const originalWS = globalThis.WebSocket;
    try {
      const { client, drafts, refuse, onServerError } = draftHarness();

      client.saveDraft("board-1", { filters: ["a"] });
      refuse(6);
      client.saveDraft("board-1", { filters: ["b"] });
      refuse(9);

      // Two retries across two separate attempts, neither reported.
      expect(drafts()).toHaveLength(4);
      expect(drafts()[3]).toMatchObject({ draftRevision: 9 });
      expect(onServerError).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
      globalThis.WebSocket = originalWS;
    }
  });

  it("reports a refusal that carries no expected revision instead of guessing", () => {
    const originalWS = globalThis.WebSocket;
    try {
      const { client, drafts, refuse, onServerError } = draftHarness();

      client.saveDraft("board-1", { filters: ["a"] });
      // An older server, or a conflict the store could not put a number on.
      refuse(undefined);

      expect(drafts()).toHaveLength(1);
      expect(onServerError).toHaveBeenCalledTimes(1);
    } finally {
      vi.unstubAllGlobals();
      globalThis.WebSocket = originalWS;
    }
  });
});

describe("shell client identity", () => {
  const ID_KEY = "tangent:v2:room:client-id";
  const KIND_KEY = "tangent:v2:room:client-kind";

  function withURL(path: string, run: () => void) {
    const original = window.location.href;
    window.history.replaceState(null, "", path);
    try {
      run();
    } finally {
      window.history.replaceState(null, "", original);
    }
  }

  it("survives the first in-app navigation when seeded at boot", () => {
    window.sessionStorage.clear();
    // The shell opens the window with the params on the root URL...
    withURL("/?clientId=shell-abc&clientKind=desktop", () => {
      seedShellClientIdentity();
    });
    // ...and React Router's first pushState drops the query string before
    // connect() ever runs on the room route.
    withURL("/r/room-1", () => {
      expect(getTabClientID()).toBe("shell-abc");
      expect(getTabClientKind()).toBe("desktop");
    });
  });

  it("is a no-op for a plain browser tab and still mints a tab id", () => {
    window.sessionStorage.clear();
    withURL("/", () => {
      seedShellClientIdentity();
      expect(window.sessionStorage.getItem(ID_KEY)).toBeNull();
      expect(window.sessionStorage.getItem(KIND_KEY)).toBeNull();
      const id = getTabClientID();
      expect(id.startsWith("room-tab-")).toBe(true);
      expect(getTabClientID()).toBe(id);
      expect(getTabClientKind()).toBe("");
    });
  });

  it("lets an explicit clientID option win over the seeded slot", () => {
    window.sessionStorage.clear();
    withURL("/?clientId=shell-abc&clientKind=desktop", () => {
      seedShellClientIdentity();
    });
    Object.assign(MockWebSocket, { instances: [] });
    vi.stubGlobal("WebSocket", MockWebSocket);
    withURL("/r/room-1", () => {
      const client = connect("room-1", {
        wsURL: "ws://example.test/ws",
        clientID: "explicit-id",
        onOpen: vi.fn(),
        onClose: vi.fn(),
        onEnvelope: vi.fn(),
        onError: vi.fn(),
      });
      const url = new URL(MockWebSocket.instances[0].url);
      expect(url.searchParams.get("clientID")).toBe("explicit-id");
      expect(url.searchParams.get("clientKind")).toBe("desktop");
      client.close();
    });
    vi.unstubAllGlobals();
  });
});
