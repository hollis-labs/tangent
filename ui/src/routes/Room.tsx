// Room is the per-session view rendered at /r/:roomID. Connects to the
// Tangent /ws endpoint and delegates rendering to EnvelopeRouter.
//
// PR 5 changes from PR 4:
//   - Mock submit/cancel buttons removed; the registered envelope
//     component owns its own UX (e.g. <Triage> renders Submit/Cancel
//     buttons that call back through onSubmit/onCancel).
//   - The Room only owns the WS transport; envelope-shape decisions
//     live in the component layer.
//
// Lifecycle:
//   1. On mount, open WSClient(roomID).
//   2. On `onEnvelope`, store the envelope id + payload in state.
//   3. EnvelopeRouter dispatches by type and fires onSubmit/onCancel.
//   4. On unmount or onClose, close the WS.

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
      onOpen: () => {
        setStatus("connected");
      },
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
    return () => {
      client.close();
      clientRef.current = null;
    };
  }, [roomID]);

  const handleSubmit = (response: unknown) => {
    if (!pending || !clientRef.current) return;
    clientRef.current.submitResponse(pending.envelopeId, response);
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
          <div className="text-xs text-zinc-500">envelope: {pending.envelopeId}</div>
          <EnvelopeRouter
            envelope={pending.envelope}
            onSubmit={handleSubmit}
            onCancel={handleCancel}
          />
        </section>
      ) : (
        <p className="text-sm text-zinc-500">waiting for envelope...</p>
      )}
    </main>
  );
}
