import type { WSClient } from "./ws-client";

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
};

// createRoomLifecycle is the authoritative owner of SPA room transport
// transitions. Room.tsx supplies rendering state around it; integration tests
// drive this exact implementation against Tangent's real WebSocket handler.
export function createRoomLifecycle(initialRoomID: string, client: WSClient): RoomLifecycle {
  let roomID = initialRoomID;
  let pending: ActiveRoomEnvelope | null = null;
  let disposed = false;

  return {
    currentRoomID: () => roomID,
    activeEnvelope: () => pending,
    receiveEnvelope: (envelopeId, envelope, revision = 0) => {
      pending = { envelopeId, envelope, revision };
    },
    clearEnvelope: () => {
      pending = null;
    },
    switchRoom: (nextRoomID) => {
      if (!nextRoomID || nextRoomID === roomID) {
        return false;
      }
      client.switchRoom(nextRoomID);
      roomID = nextRoomID;
      pending = null;
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
    dispose: () => {
      if (disposed) {
        return;
      }
      disposed = true;
      client.close();
      pending = null;
    },
  };
}
