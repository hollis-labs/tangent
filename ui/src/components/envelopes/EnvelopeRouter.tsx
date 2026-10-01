// EnvelopeRouter is the type → component dispatcher, and the one place a
// renderer's trust classification is checked before it runs.
//
// Lookup rules:
//   - Pull the envelope's `type` field if present.
//   - Classify the kind against its manifest (lib/renderer-trust). A kind
//     nothing classified, or one this host will not serve, is refused here
//     rather than dispatched — until CW-20260825-0073 a registered component
//     ran in Tangent's own origin whether or not any manifest had ever said it
//     could.
//   - Defer to lib/envelope-registry for the component.
//   - Fall back to the JSON debug renderer when the type is missing or
//     unregistered. That fallback executes no publisher code — it prints the
//     payload — which is why it stays reachable for an unknown type while a
//     *classified but unservable* one is refused instead.
//
// The Router itself remains imperative-state-free: callbacks flow
// down from the owning Room, which owns the WSClient.

import { lookup } from "@/lib/envelope-registry";
import { classifyRenderer, describeRendererRefusal } from "@/lib/renderer-trust";

type Props = {
  envelope: unknown;
  onSubmit: (response: unknown) => void;
  onCancel: () => void;
  roomID?: string;
  readOnly?: boolean;
  /** Records non-terminal view state. See EnvelopeComponentProps.onDraft. */
  onDraft?: (draft: unknown) => void;
};

export function EnvelopeRouter({ envelope, onSubmit, onCancel, roomID, onDraft, readOnly }: Props) {
  const type = readType(envelope);
  const Component = type ? lookup(type) : null;

  if (Component && type) {
    const classification = classifyRenderer(type);
    if (!classification.admitted) {
      // The Refused surface from docs/room-validation-affordances.md — red,
      // `role="alert"`, "Not submitted: <reason>. <what to do>." Trust denial
      // is a fact about the world that no amount of clicking fixes, which is
      // what that surface is for, and Cancel stays available so the caller is
      // released rather than left waiting on a room timeout.
      return (
        <div className="space-y-2" data-testid="envelope-router-refused">
          <p role="alert" className="text-sm text-red-300">
            {describeRendererRefusal(classification)}
          </p>
          {classification.fallbackRendererID ? (
            <p className="text-xs text-zinc-500">
              A safe fallback renderer is declared ({classification.fallbackRendererID}) and is not
              loaded in this build.
            </p>
          ) : null}
          <div className="flex justify-end">
            <button
              type="button"
              onClick={onCancel}
              className="rounded bg-zinc-800 hover:bg-zinc-700 px-3 py-1 text-xs"
            >
              Cancel
            </button>
          </div>
        </div>
      );
    }
    return (
      <Component
        envelope={envelope}
        onSubmit={onSubmit}
        onCancel={onCancel}
        roomID={roomID}
        readOnly={readOnly}
        onDraft={onDraft}
      />
    );
  }

  // Fallback: unknown / missing type. Render the raw JSON for
  // debuggability AND a Cancel control so the user can release the
  // server-side Push (otherwise the MCP caller waits until the room
  // timeout, which is bad UX for a typo'd envelope type).
  return (
    <div className="space-y-2" data-testid="envelope-router-fallback">
      <p className="text-xs text-zinc-500">
        {type
          ? `No component registered for "${type}". Showing raw payload.`
          : "Envelope has no type. Showing raw payload."}
      </p>
      <pre className="whitespace-pre-wrap break-words rounded border border-zinc-800 bg-zinc-900 p-3 text-xs text-zinc-200">
        {safeJson(envelope)}
      </pre>
      <div className="flex justify-end">
        <button
          type="button"
          onClick={onCancel}
          className="rounded bg-zinc-800 hover:bg-zinc-700 px-3 py-1 text-xs"
        >
          Cancel
        </button>
      </div>
    </div>
  );
}

function readType(envelope: unknown): string | null {
  if (!envelope || typeof envelope !== "object") return null;
  const t = (envelope as { type?: unknown }).type;
  return typeof t === "string" && t.length > 0 ? t : null;
}

function safeJson(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2);
  } catch (err) {
    return `(failed to stringify: ${(err as Error).message})`;
  }
}
