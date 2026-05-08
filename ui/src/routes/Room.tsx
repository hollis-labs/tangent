// Room is the per-session view rendered at /r/:roomID. PR 4 wires it
// to the WebSocket client and renders the inbound envelope as raw
// JSON with two action buttons (submit-mock / cancel). PR 5 replaces
// the placeholder body with the real EnvelopeRouter component map.
//
// Lifecycle:
//
//   1. On mount, open WSClient(roomID).
//   2. On `onEnvelope`, store the envelope id + payload in state.
//   3. Submit/Cancel buttons call ws.submitResponse / ws.cancel and
//      clear local state.
//   4. On unmount or onClose, close the WS.
//
// The component does NOT validate inbound envelopes beyond what
// ws-client already enforces (zod). Per-type rendering and validation
// is PR 5's responsibility.

import { useEffect, useRef, useState } from "react";
import { useParams } from "react-router-dom";

import { EnvelopeRouter } from "../components/envelopes/EnvelopeRouter";
import { connect, type WSClient } from "../lib/ws-client";

type Pending = {
  envelopeId: string;
  envelope: unknown;
};

export default function Room() {
  const { roomID } = useParams<{ roomID: string }>();
  const [pending, setPending] = useState<Pending | null>(null);
  const [status, setStatus] = useState<string>("connecting...");
  const [error, setError] = useState<string | null>(null);
  const clientRef = useRef<WSClient | null>(null);

  useEffect(() => {
    if (!roomID) {
      setError("missing roomID in URL");
      return;
    }
    const client = connect(roomID, {
      onEnvelope: (envelopeId, envelope) => {
        setPending({ envelopeId, envelope });
        setStatus("envelope received");
      },
      onClose: (reason) => {
        setStatus(`disconnected: ${reason}`);
        setPending(null);
      },
      onError: (err) => {
        setError(err.message);
      },
    });
    clientRef.current = client;
    setStatus("connected");
    return () => {
      client.close();
      clientRef.current = null;
    };
  }, [roomID]);

  const handleSubmit = () => {
    if (!pending || !clientRef.current) return;
    // PR 4 mock response — PR 5 wires the real form output.
    clientRef.current.submitResponse(pending.envelopeId, {
      v: 1,
      envelopeId: pending.envelopeId,
      kind: "data",
      status: "submitted",
      payload: { accepted: true },
    });
    setPending(null);
    setStatus("response submitted");
  };

  const handleCancel = () => {
    if (!pending || !clientRef.current) return;
    clientRef.current.cancel(pending.envelopeId);
    setPending(null);
    setStatus("cancelled");
  };

  return (
    <main className="min-h-screen bg-zinc-950 text-zinc-100 p-6">
      <header className="mb-4 space-y-1">
        <h1 className="text-lg font-medium">Room {roomID}</h1>
        <p className="text-xs text-zinc-400">status: {status}</p>
        {error ? <p className="text-xs text-red-400">error: {error}</p> : null}
      </header>

      {pending ? (
        <section className="space-y-3">
          <div className="text-xs text-zinc-400">envelope: {pending.envelopeId}</div>
          <EnvelopeRouter envelope={pending.envelope} />
          <div className="flex gap-2">
            <button
              type="button"
              onClick={handleSubmit}
              className="rounded bg-emerald-600 hover:bg-emerald-500 px-3 py-1 text-sm"
            >
              Submit (mock response)
            </button>
            <button
              type="button"
              onClick={handleCancel}
              className="rounded bg-zinc-800 hover:bg-zinc-700 px-3 py-1 text-sm"
            >
              Cancel
            </button>
          </div>
        </section>
      ) : (
        <p className="text-sm text-zinc-500">waiting for envelope...</p>
      )}
    </main>
  );
}
