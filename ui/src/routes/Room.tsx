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
      onEnvelope: async (envelopeId, envelope) => {
        const enriched = await enrichEnvelope(roomID, envelope);
        setPending({ envelopeId, envelope: enriched });
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

async function enrichEnvelope(roomID: string, envelope: unknown): Promise<unknown> {
  const type = readEnvelopeType(envelope);
  if (type !== "tangent.design-iteration") {
    return envelope;
  }
  try {
    const history = await fetchRoomHistory(roomID);
    return attachPriorVariants(envelope, history);
  } catch {
    return envelope;
  }
}

function readEnvelopeType(envelope: unknown): string | null {
  if (!envelope || typeof envelope !== "object") {
    return null;
  }
  const type = (envelope as { type?: unknown }).type;
  return typeof type === "string" ? type : null;
}

async function fetchRoomHistory(roomID: string): Promise<unknown[]> {
  const response = await fetch("/mcp", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json, text/event-stream",
    },
    body: JSON.stringify({
      jsonrpc: "2.0",
      id: 1,
      method: "tools/call",
      params: {
        name: "tangent.session_get",
        arguments: { roomID },
      },
    }),
  });
  if (!response.ok) {
    throw new Error(`session_get HTTP ${response.status}`);
  }
  const payload = await response.json();
  const text = payload?.result?.content?.[0]?.text;
  if (typeof text !== "string") {
    return [];
  }
  const parsed = JSON.parse(text) as { envelopes_history?: unknown[] };
  return parsed.envelopes_history ?? [];
}

function attachPriorVariants(envelope: unknown, history: unknown[]): unknown {
  if (!envelope || typeof envelope !== "object") {
    return envelope;
  }
  const typed = envelope as Record<string, unknown>;
  const data =
    typed.data && typeof typed.data === "object"
      ? { ...(typed.data as Record<string, unknown>) }
      : {};
  const priorVariants = history
    .map((item) => {
      if (!item || typeof item !== "object") {
        return null;
      }
      const record = item as Record<string, unknown>;
      if (record.type !== "tangent.design-iteration") {
        return null;
      }
      const req = record.envelope;
      if (!req || typeof req !== "object") {
        return null;
      }
      const env = req as Record<string, unknown>;
      const envData =
        env.data && typeof env.data === "object" ? (env.data as Record<string, unknown>) : {};
      const variantID = envData.variant_id;
      const html = envData.html;
      const envelopeID = env.id;
      if (
        typeof variantID !== "string" ||
        typeof html !== "string" ||
        typeof envelopeID !== "string"
      ) {
        return null;
      }
      return {
        envelope_id: envelopeID,
        variant_id: variantID,
        title: typeof env.title === "string" ? env.title : undefined,
        caption: typeof envData.caption === "string" ? envData.caption : undefined,
        html,
      };
    })
    .filter((item): item is NonNullable<typeof item> => item !== null);

  data.prior_variants = priorVariants;
  return { ...typed, data };
}
