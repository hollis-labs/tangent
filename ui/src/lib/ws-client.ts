// Typed WebSocket client for the Tangent /ws endpoint.
//
// Wire shape (mirrors internal/ws/handler.go):
//
//   server → client : {type:"envelope", envelopeId, revision, envelope}
//   server → client : {type:"connection", connectionId, role, lease, connections}
//   server → client : {type:"sync", sync:{surface_revision, presentations}}
//   server → client : {type:"error", code, message, envelopeId, revision, lease}
//   client → server : {type:"response", envelopeId, revision, response}
//   client → server : {type:"cancel", envelopeId, revision}
//   client → server : {type:"claim_resolver", takeover}
//   client → server : {type:"release_resolver"}
//   client → server : {type:"resync"}
//   client → server : {type:"heartbeat"}
//
// Inbound messages are zod-validated; malformed frames are reported via
// onError instead of being silently dropped. Frames of an unrecognized type
// are ignored rather than reported, so a newer server can add one without
// making every older tab shout.
//
// Connection identity: the server issues the connection id. This client
// supplies only a clientID — the identity of *this tab* — which is what tells
// the server that a reconnect is a refresh (replace my previous socket) rather
// than a second tab (join alongside it). It is held in sessionStorage, which
// is per-tab and survives a reload, exactly matching that meaning. It is a
// routing label and grants nothing.

import { z } from "zod";

const ConnectionViewSchema = z.object({
  connection_id: z.string(),
  client_id: z.string().optional(),
  label: z.string().optional(),
  client_kind: z.string().optional(),
  role: z.enum(["resolver", "observer"]),
  attached_at: z.string().optional(),
  participant_ref: z.string().optional(),
  self: z.boolean().optional(),
});

const LeaseViewSchema = z.object({
  connection_id: z.string(),
  client_id: z.string().optional(),
  label: z.string().optional(),
  granted_at: z.string().optional(),
  expires_at: z.string().optional(),
  expired: z.boolean().optional(),
});

const PresentationSyncSchema = z.object({
  envelope_id: z.string(),
  durable: z.boolean().optional(),
  interaction_id: z.string().optional(),
  interaction_revision: z.number().optional(),
  presented_projection_revision: z.number().optional(),
  state: z.string().optional(),
});

const SurfaceSyncSchema = z.object({
  room_id: z.string(),
  surface_revision: z.number(),
  synced_at: z.string().optional(),
  presentations: z.array(PresentationSyncSchema).default([]),
});

const EnvelopeMessageSchema = z.object({
  type: z.literal("envelope"),
  envelopeId: z.string().min(1),
  revision: z.number().int().positive(),
  envelope: z.unknown(),
});

const ConnectionMessageSchema = z.object({
  type: z.literal("connection"),
  connectionId: z.string().min(1),
  role: z.enum(["resolver", "observer"]),
  roomId: z.string().optional(),
  lease: LeaseViewSchema.nullish(),
  connections: z.array(ConnectionViewSchema).default([]),
});

const SyncMessageSchema = z.object({
  type: z.literal("sync"),
  sync: SurfaceSyncSchema,
});

const ErrorMessageSchema = z.object({
  type: z.literal("error"),
  code: z.string(),
  message: z.string().default(""),
  envelopeId: z.string().optional(),
  revision: z.number().optional(),
  connectionId: z.string().optional(),
  lease: LeaseViewSchema.nullish(),
});

const InboundSchema = z.discriminatedUnion("type", [
  EnvelopeMessageSchema,
  ConnectionMessageSchema,
  SyncMessageSchema,
  ErrorMessageSchema,
]);

type InboundMessage = z.infer<typeof InboundSchema>;

export type ConnectionRole = "resolver" | "observer";
export type ConnectionView = z.infer<typeof ConnectionViewSchema>;
export type LeaseView = z.infer<typeof LeaseViewSchema>;
export type SurfaceSync = z.infer<typeof SurfaceSyncSchema>;

/** Connection lifecycle state for one surface, as this tab sees it. */
export type ConnectionState = {
  connectionId: string;
  role: ConnectionRole;
  roomID?: string;
  lease: LeaseView | null;
  connections: ConnectionView[];
};

/** A refused client action, reported by the server rather than guessed at. */
export type ServerError = {
  code: string;
  message: string;
  envelopeId?: string;
  revision?: number;
  lease: LeaseView | null;
};

/** How often this tab proves it is alive to the resolver lease. */
export const HEARTBEAT_MS = 10_000;

export type WSClientOptions = {
  /**
   * Callback for inbound envelope frames. Receives the parsed
   * envelope (kept as `unknown` until PR 5 introduces the typed
   * envelope union); the caller is responsible for narrowing.
   */
  onEnvelope: (envelopeId: string, envelope: unknown, revision: number) => void;

  /**
   * Called whenever the surface's connection lifecycle changes — this tab
   * attaching, a peer arriving or leaving, or the resolver lease moving. It
   * is deliberately independent of any envelope: a surface with no pending
   * work still has connections.
   */
  onConnectionState?: (state: ConnectionState) => void;

  /**
   * Called with the durable revision snapshot this tab is synchronized to.
   */
  onSync?: (sync: SurfaceSync) => void;

  /**
   * Called when the server refuses a client action — a lease conflict or a
   * stale presentation. Distinct from onError, which reports transport and
   * protocol faults.
   */
  onServerError?: (error: ServerError) => void;

  /**
   * Called once the underlying WebSocket transitions to OPEN. Use
   * this rather than treating the return of `connect()` as a
   * connected signal — the WS may still be in CONNECTING when
   * connect() returns and never reach OPEN if the URL is bad or
   * the room has gone away on the server.
   */
  onOpen?: () => void;

  /**
   * Called when the WebSocket closes. `reason` is best-effort — most
   * browsers redact the WS close reason for security, so callers
   * should treat it as a hint, not an authoritative signal.
   */
  onClose?: (reason: string) => void;

  /**
   * Called for transport-level errors and malformed frames. The
   * default is to log and continue.
   */
  onError?: (err: Error) => void;

  /**
   * Override the WS URL. Defaults to /ws on the current origin.
   * Useful for tests or when the SPA is served from a different
   * host than the API.
   */
  wsURL?: string;

  /**
   * Override this tab's client identity. Defaults to a sessionStorage-backed
   * per-tab id, itself defaulting to a `?clientId=` query param on the page
   * URL when one is present (see getTabClientID) — that is how the desktop
   * shell's stable id reaches the socket without a Wails runtime bridge.
   * Tests and the lifecycle e2e driver set it explicitly to model "the same
   * tab refreshing" versus "a second tab".
   */
  clientID?: string;

  /**
   * Descriptive label for the sort of client behind this connection, shown
   * to the operator when more than one is attached. Defaults to a
   * `?clientKind=` query param on the page URL, and then to the server's own
   * "browser" default. Purely descriptive — see room.AttachOptions.ClientKind.
   */
  clientKind?: string;

  /** Attach as an observer, declining the resolver lease even when it is free. */
  observer?: boolean;

  /** Heartbeat interval override, in milliseconds. Zero disables it. */
  heartbeatMs?: number;
};

export type WSClient = {
  /** True between successful open and close. */
  isConnected: () => boolean;

  /** Sends a response frame for the given envelope id. */
  submitResponse: (envelopeId: string, response: unknown, revision?: number) => boolean;

  /** Sends a cancel frame for the given envelope id. */
  cancel: (envelopeId: string, revision?: number) => boolean;

  /**
   * Asks for the resolver lease. `takeover` revokes it from a live peer and
   * is only ever sent from an explicit operator action.
   */
  claimResolver: (takeover?: boolean) => boolean;

  /** Gives up the resolver lease so a peer can take it without a takeover. */
  releaseResolver: () => boolean;

  /** Asks the server for a fresh durable snapshot and re-presentation. */
  resync: () => boolean;

  /** Switches the underlying socket to a different room. */
  switchRoom: (roomID: string) => void;

  /** This tab's stable client identity. */
  clientID: () => string;

  /** Closes the underlying WebSocket cleanly. */
  close: () => void;
};

/**
 * Opens a WebSocket connection to the Tangent server for the given
 * room id. The returned client is alive until close() is called or
 * onClose fires.
 */
export function connect(roomID: string, opts: WSClientOptions): WSClient {
  if (!roomID) {
    throw new Error("ws-client: roomID is required");
  }

  const baseURL = opts.wsURL ?? defaultWSURL();
  const clientID = opts.clientID ?? getTabClientID();
  const clientKind = opts.clientKind ?? getTabClientKind();
  const heartbeatMs = opts.heartbeatMs ?? HEARTBEAT_MS;
  let connected = false;
  let currentRoomID = roomID;
  let heartbeat: ReturnType<typeof setInterval> | null = null;
  let ws = openSocket(currentRoomID);

  function stopHeartbeat() {
    if (heartbeat !== null) {
      clearInterval(heartbeat);
      heartbeat = null;
    }
  }

  function startHeartbeat(socket: WebSocket) {
    stopHeartbeat();
    if (heartbeatMs <= 0) return;
    // The heartbeat exists only to renew the resolver lease. Losing it costs
    // this tab the lease after the server-side TTL, never the interaction.
    heartbeat = setInterval(() => {
      if (socket !== ws) {
        stopHeartbeat();
        return;
      }
      send(ws, { type: "heartbeat" });
    }, heartbeatMs);
  }

  function openSocket(nextRoomID: string): WebSocket {
    const socket = new WebSocket(
      socketURL(baseURL, nextRoomID, clientID, clientKind, opts.observer === true),
    );
    socket.addEventListener("open", () => {
      if (socket !== ws) return;
      connected = true;
      startHeartbeat(socket);
      opts.onOpen?.();
    });

    socket.addEventListener("message", (ev) => {
      if (socket !== ws) return;
      const raw = typeof ev.data === "string" ? ev.data : "";
      if (!raw) return;
      let parsed: unknown;
      try {
        parsed = JSON.parse(raw);
      } catch (err) {
        opts.onError?.(new Error(`ws-client: bad JSON: ${(err as Error).message}`));
        return;
      }
      if (!isKnownFrameType(parsed)) {
        // A frame type this build does not know about is a forward-compatible
        // server, not a fault. Ignore it rather than reporting an error.
        return;
      }
      const result = InboundSchema.safeParse(parsed);
      if (!result.success) {
        opts.onError?.(new Error(`ws-client: schema rejected frame: ${result.error.message}`));
        return;
      }
      dispatch(result.data);
    });

    socket.addEventListener("close", (ev) => {
      if (socket !== ws) return;
      connected = false;
      stopHeartbeat();
      const reason = ev.reason || `closed (code=${ev.code})`;
      opts.onClose?.(reason);
    });

    socket.addEventListener("error", () => {
      if (socket !== ws) return;
      opts.onError?.(new Error("ws-client: transport error"));
    });
    return socket;
  }

  function dispatch(msg: InboundMessage) {
    switch (msg.type) {
      case "envelope":
        opts.onEnvelope(msg.envelopeId, msg.envelope, msg.revision);
        return;
      case "connection":
        opts.onConnectionState?.({
          connectionId: msg.connectionId,
          role: msg.role,
          roomID: msg.roomId,
          lease: msg.lease ?? null,
          connections: msg.connections,
        });
        return;
      case "sync":
        opts.onSync?.(msg.sync);
        return;
      case "error":
        opts.onServerError?.({
          code: msg.code,
          message: msg.message,
          envelopeId: msg.envelopeId,
          revision: msg.revision,
          lease: msg.lease ?? null,
        });
    }
  }

  return {
    isConnected: () => connected,
    clientID: () => clientID,
    submitResponse: (envelopeId, response, revision = 0) => {
      return send(ws, { type: "response", envelopeId, revision, response });
    },
    cancel: (envelopeId, revision = 0) => {
      return send(ws, { type: "cancel", envelopeId, revision });
    },
    claimResolver: (takeover = false) => {
      return send(ws, { type: "claim_resolver", takeover });
    },
    releaseResolver: () => {
      return send(ws, { type: "release_resolver" });
    },
    resync: () => {
      return send(ws, { type: "resync" });
    },
    switchRoom: (roomID) => {
      if (!roomID || roomID === currentRoomID) {
        return;
      }
      try {
        ws.close(1000, "switch room");
      } catch {
        // ignore close errors during handoff
      }
      connected = false;
      stopHeartbeat();
      currentRoomID = roomID;
      ws = openSocket(roomID);
    },
    close: () => {
      stopHeartbeat();
      try {
        ws.close(1000, "client closed");
      } catch {
        // already closed — ignore
      }
    },
  };
}

const KNOWN_FRAME_TYPES = new Set(["envelope", "connection", "sync", "error"]);

function isKnownFrameType(parsed: unknown): boolean {
  if (!parsed || typeof parsed !== "object") return false;
  const type = (parsed as { type?: unknown }).type;
  return typeof type === "string" && KNOWN_FRAME_TYPES.has(type);
}

function send(ws: WebSocket, frame: object): boolean {
  if (ws.readyState !== WebSocket.OPEN) {
    // The brief lets us drop or queue; we drop with a console warning
    // because v0.1 doesn't have a reliable backoff strategy and silent
    // queuing risks confusion. Future PRs may queue.
    console.warn("ws-client: drop frame, socket not open", frame);
    return false;
  }
  try {
    ws.send(JSON.stringify(frame));
    return true;
  } catch {
    console.warn("ws-client: failed to send frame", frame);
    return false;
  }
}

function socketURL(
  base: string,
  roomID: string,
  clientID: string,
  clientKind: string,
  observer: boolean,
): string {
  let url = appendQuery(base, "roomID", roomID);
  if (clientID) {
    url = appendQuery(url, "clientID", clientID);
  }
  if (clientKind) {
    url = appendQuery(url, "clientKind", clientKind);
  }
  if (observer) {
    url = appendQuery(url, "role", "observer");
  }
  return url;
}

/**
 * queryParam reads one parameter off the current page URL. Used only for the
 * one-time signals the app shell injects (?clientId=, ?clientKind=) — an
 * external-URL Wails window gets no runtime bridge to pass these any other
 * way (CW-20260905-0025's findings). A plain browser tab never carries them,
 * so this is a no-op there.
 */
function queryParam(name: string): string | null {
  try {
    return new URLSearchParams(window.location.search).get(name);
  } catch {
    return null;
  }
}

const CLIENT_ID_KEY = "tangent:v2:room:client-id";
const CLIENT_KIND_KEY = "tangent:v2:room:client-kind";

/**
 * seedShellClientIdentity copies the app shell's one-time `?clientId=` and
 * `?clientKind=` signals off the page URL into this tab's sessionStorage.
 *
 * It must run once at SPA boot, before any route renders. The shell opens the
 * window at `/?clientId=…&clientKind=desktop`, but the first in-app navigation
 * is a React Router pushState that drops the query string, and `connect()`
 * only runs on the room route — so reading the params lazily from inside
 * `connect()` would find nothing and mint a random tab id, which is exactly
 * the second-tab (observer) outcome the shell's stable id exists to avoid.
 *
 * A plain browser tab carries neither param, so this is a no-op there.
 */
export function seedShellClientIdentity(): void {
  const id = queryParam("clientId");
  const kind = queryParam("clientKind");
  try {
    if (id) window.sessionStorage.setItem(CLIENT_ID_KEY, id);
    if (kind) window.sessionStorage.setItem(CLIENT_KIND_KEY, kind);
  } catch {
    // Privacy modes can disable sessionStorage. getTabClientID still honours
    // the query param directly while the URL carries it.
  }
}

/**
 * getTabClientKind returns the descriptive client kind the shell declared for
 * this tab, or "" for a plain browser tab (the server then applies its own
 * "browser" default). Query param first, then the slot seeded at boot.
 */
export function getTabClientKind(): string {
  const fromQuery = queryParam("clientKind");
  if (fromQuery) return fromQuery;
  try {
    return window.sessionStorage.getItem(CLIENT_KIND_KEY) ?? "";
  } catch {
    return "";
  }
}

/**
 * getTabClientID returns this tab's stable identity.
 *
 * sessionStorage is the right store: it is scoped to one tab and survives a
 * reload, which is exactly the distinction the server uses to tell a refresh
 * from a second tab. This is a new v2-shaped key and does not participate in
 * the localStorage draft migration described in ADR 0002.
 *
 * A `?clientId=` query param on the page URL takes precedence and is written
 * into the same slot: the app shell's window carries one so that quitting and
 * relaunching the app reads as a reconnect (same id, inherits the resolver
 * lease) rather than a second tab. sessionStorage alone cannot do this — it
 * resets with every new webview instance, which is every app relaunch. See
 * seedShellClientIdentity for why the slot is filled at boot, not here.
 */
export function getTabClientID(): string {
  const key = CLIENT_ID_KEY;
  const fromQuery = queryParam("clientId");
  if (fromQuery) {
    try {
      window.sessionStorage.setItem(key, fromQuery);
    } catch {
      // Privacy modes can disable sessionStorage; the query param still
      // answers this call directly below.
    }
    return fromQuery;
  }
  try {
    const existing = window.sessionStorage.getItem(key);
    if (existing) return existing;
  } catch {
    // Privacy modes can disable sessionStorage. An in-memory id still works;
    // the tab simply loses refresh-replacement semantics for this session.
  }
  const randomID =
    globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  const id = `room-tab-${randomID}`;
  try {
    window.sessionStorage.setItem(key, id);
  } catch {
    // Keep using the in-memory identifier for this page load.
  }
  return id;
}

function defaultWSURL(): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/ws`;
}

function appendQuery(base: string, key: string, value: string): string {
  const sep = base.includes("?") ? "&" : "?";
  return `${base}${sep}${encodeURIComponent(key)}=${encodeURIComponent(value)}`;
}
