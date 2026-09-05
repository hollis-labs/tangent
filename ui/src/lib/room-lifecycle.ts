import type { ConnectionState, ServerError, SurfaceSync, WSClient } from "./ws-client";

export type ActiveRoomEnvelope = {
  envelopeId: string;
  envelope: unknown;
  revision: number;
};

export type RoomLifecycle = {
  currentRoomID: () => string;
  activeEnvelope: () => ActiveRoomEnvelope | null;
  receiveEnvelope: (envelopeId: string, envelope: unknown, revision?: number) => void;
  clearEnvelope: () => void;
  switchRoom: (roomID: string) => boolean;
  submit: (response: unknown) => boolean;
  cancel: () => boolean;
  dispose: () => void;

  /** Connection lifecycle, tracked independently of any envelope. */
  connectionState: () => ConnectionState | null;
  receiveConnectionState: (state: ConnectionState) => void;
  /** Durable revisions this tab is synchronized to. */
  sync: () => SurfaceSync | null;
  receiveSync: (sync: SurfaceSync) => void;
  /** The last action the server refused, until it is cleared. */
  lastServerError: () => ServerError | null;
  receiveServerError: (error: ServerError) => void;
  clearServerError: () => void;
  /** True when this tab may produce a terminal action on the surface. */
  canResolve: () => boolean;
  claimResolver: (takeover?: boolean) => boolean;
  releaseResolver: () => boolean;
  resync: () => boolean;
};

// createRoomLifecycle is the authoritative owner of SPA room transport
// transitions. Room.tsx supplies rendering state around it; integration tests
// drive this exact implementation against Tangent's real WebSocket handler.
//
// It deliberately keeps two independent pieces of state. `pending` is the
// interaction this tab is looking at. `connection` is who is attached and who
// holds the resolver lease. Neither implies the other: a tab can be connected
// with nothing to answer, and an envelope stays presented while every tab goes
// away.
export function createRoomLifecycle(initialRoomID: string, client: WSClient): RoomLifecycle {
  let roomID = initialRoomID;
  let pending: ActiveRoomEnvelope | null = null;
  // lastPresented is what the server last showed this tab, kept apart from
  // `pending` so an optimistic clear can be undone. A submit that the server
  // refuses did not happen, and the operator must get their envelope back
  // rather than an empty pane.
  let lastPresented: ActiveRoomEnvelope | null = null;
  let connection: ConnectionState | null = null;
  let synced: SurfaceSync | null = null;
  let serverError: ServerError | null = null;
  let disposed = false;

  const resetTransportState = () => {
    pending = null;
    lastPresented = null;
    connection = null;
    synced = null;
    serverError = null;
  };

  return {
    currentRoomID: () => roomID,
    activeEnvelope: () => pending,
    receiveEnvelope: (envelopeId, envelope, revision = 0) => {
      pending = { envelopeId, envelope, revision };
      lastPresented = pending;
      // A fresh presentation supersedes whatever the server last refused: the
      // client is now looking at current state again.
      serverError = null;
    },
    clearEnvelope: () => {
      pending = null;
      lastPresented = null;
    },
    switchRoom: (nextRoomID) => {
      if (!nextRoomID || nextRoomID === roomID) {
        return false;
      }
      client.switchRoom(nextRoomID);
      roomID = nextRoomID;
      resetTransportState();
      return true;
    },
    submit: (response) => {
      if (!pending) {
        return false;
      }
      const sent =
        pending.revision > 0
          ? client.submitResponse(pending.envelopeId, response, pending.revision)
          : client.submitResponse(pending.envelopeId, response);
      if (!sent) {
        return false;
      }
      pending = null;
      return true;
    },
    cancel: () => {
      if (!pending) {
        return false;
      }
      const sent =
        pending.revision > 0
          ? client.cancel(pending.envelopeId, pending.revision)
          : client.cancel(pending.envelopeId);
      if (!sent) {
        return false;
      }
      pending = null;
      return true;
    },
    connectionState: () => connection,
    receiveConnectionState: (state) => {
      connection = state;
      // Regaining the lease clears a stale lease complaint; a revision
      // complaint is cleared by the next presentation instead.
      if (state.role === "resolver" && serverError?.code === "resolver_lease_held") {
        serverError = null;
      }
    },
    sync: () => synced,
    receiveSync: (next) => {
      synced = next;
    },
    lastServerError: () => serverError,
    receiveServerError: (error) => {
      serverError = error;
      // A refused action did not happen. Undo the optimistic clear so the
      // operator is looking at the same envelope the server still has open.
      if (pending === null && lastPresented !== null) {
        pending = lastPresented;
      }
    },
    clearServerError: () => {
      serverError = null;
    },
    canResolve: () => connection === null || connection.role === "resolver",
    claimResolver: (takeover = false) => client.claimResolver(takeover),
    releaseResolver: () => client.releaseResolver(),
    resync: () => client.resync(),
    dispose: () => {
      if (disposed) {
        return;
      }
      disposed = true;
      client.close();
      resetTransportState();
    },
  };
}
