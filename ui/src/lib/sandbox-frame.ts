// The presentation sandbox: one place that knows how Tangent runs untrusted
// active content, and one rule for deciding whether a message from it is real.
//
// Before CW-20260825-0073 this lived inside `DesignIteration.tsx` — the sandbox
// attributes, the frame CSP, the srcdoc assembly, and the `window` message
// listener were all local to the one component that happened to need them.
// That is why the message listener's provenance check could be wrong without
// anyone noticing (`CW-20260904-0138`): there was nothing to compare it to.
// Everything a renderer in `isolation: "sandboxed-frame"` needs is here now,
// and the rules below are the rules, not one component's version of them.
//
// ## What the sandbox is
//
// Three independent layers, none of which is trusted to be the only one:
//
//  1. **`sandbox="allow-scripts"` and nothing else.** No `allow-same-origin`,
//     so the frame is an *opaque origin*: it cannot read Tangent's DOM,
//     storage, cookies, or session, and it cannot call `/api/*` as the
//     participant. No `allow-downloads`, so a download it starts is inert. No
//     `allow-forms`, no `allow-popups`, no `allow-top-navigation`, no
//     `allow-modals`.
//  2. **A `default-src 'none'` frame CSP** with a hash source for exactly one
//     script — Tangent's shim. `connect-src 'none'` blocks fetch, XHR,
//     WebSocket, EventSource and beacons; `form-action 'none'` blocks form
//     submission independently of the sandbox token; `base-uri 'none'` blocks
//     the `<base>` rewrite that would otherwise re-point relative URLs.
//  3. **A `Permissions-Policy` on the Tangent document** whose `clipboard-write`
//     allowlist is `(self)` — an opaque-origin frame is not `self`, so the
//     Clipboard API is unavailable inside it.
//
// Layer 2 is why `SANDBOX_FRAME_SHIM` may not be interpolated into: its bytes
// are hashed by the Go server (`internal/server/security.go`) so the *parent*
// document's policy, which an `<iframe srcdoc>` inherits, also admits it.
//
// ## The message rule
//
// [readSandboxMessage] is the general form of the check `DesignIteration`'s
// listener got wrong. The bug was structural rather than local: the source
// comparison was written as "if we have a frame window *and* the event has a
// source, they must match", which skips the entire check whenever either input
// is absent — a message arriving before the iframe mounts, or one forged with a
// null source, passed straight through to `onSubmit`.
//
// **A provenance check that is skipped when its input is absent is not a
// check.** Every branch here refuses on absence, and the four facts a
// `postMessage` channel can be validated on — source, origin, nonce, schema —
// are all required rather than any of them being sufficient.

import { SANDBOX_FRAME_SHIM } from "./sandbox-frame-shim";

export { SANDBOX_FRAME_SHIM };

/**
 * Base64 SHA-256 of `SANDBOX_FRAME_SHIM`, in CSP source form.
 *
 * Duplicated in Go as `sandboxFrameShimSHA256`; `TestSandboxFrameShimMatchesTheSPA`
 * recomputes it from this repository's own bytes and fails with the correct
 * value when the shim changes. `sandbox-frame.test.ts` recomputes it here too,
 * so neither language can drift alone.
 */
export const SANDBOX_FRAME_SHIM_SHA256 = "sha256-I1swIBtbR1QnY1/IgmK00boupM7TEi+/BltRerODeSY=";

/**
 * The sandbox token list, in full.
 *
 * Written as one constant rather than assembled, because the security property
 * is in what is *absent*. A reviewer has to be able to read the whole grant in
 * one place and see that `allow-same-origin`, `allow-downloads`, `allow-forms`,
 * `allow-popups`, `allow-modals`, `allow-top-navigation`, and
 * `allow-storage-access-by-user-activation` are all missing.
 */
export const SANDBOX_FRAME_SANDBOX = "allow-scripts";

/**
 * The origin a message from an opaque-origin frame carries.
 *
 * A frame sandboxed without `allow-same-origin` has an opaque origin, which
 * serializes to the literal string `"null"`. Requiring it is not decoration: if
 * someone removes the sandbox attribute, the frame gets Tangent's real origin,
 * this check fails, and the channel closes. The sandbox weakening turns into a
 * broken workflow rather than a silent escalation.
 */
export const SANDBOX_FRAME_ORIGIN = "null";

/** The `id` of the non-executable data block the shim reads its payload from. */
export const SANDBOX_PAYLOAD_ELEMENT_ID = "tangent-sandbox-payload";

/**
 * The frame's own Content-Security-Policy.
 *
 * `script-src` is a hash and nothing else. The previous policy said
 * `'unsafe-inline'`, which admitted Tangent's shim *and* every script the
 * agent-authored preview happened to contain; a hash admits exactly one script.
 * This is the change that makes "no script execution unless the definition
 * declares and policy grants it" true rather than aspirational.
 *
 * `default-src 'none'` means every directive not named below denies, so a
 * directive added to the platform later is denied by default rather than
 * silently permitted.
 */
export const SANDBOX_FRAME_CSP = [
  "default-src 'none'",
  `script-src '${SANDBOX_FRAME_SHIM_SHA256}'`,
  "style-src 'unsafe-inline'",
  "img-src data: blob:",
  "font-src data: blob:",
  "media-src data: blob:",
  "connect-src 'none'",
  "frame-src 'none'",
  "child-src 'none'",
  "worker-src 'none'",
  "manifest-src 'none'",
  "object-src 'none'",
  "base-uri 'none'",
  "form-action 'none'",
].join("; ");

/**
 * The default ceiling on untrusted display content, in bytes.
 *
 * Every shipped manifest declares 262144, and a binding's own
 * `inlinePayloadLimitBytes` is preferred over this when one is available. The
 * constant exists so a caller with no binding still has a bound: "bounded
 * payload sizes" is a presentation-safety rule, and an unbounded default would
 * make it conditional on a lookup succeeding.
 */
export const DEFAULT_SANDBOX_PAYLOAD_LIMIT_BYTES = 262_144;

/** One activatable region inside the sandboxed document. */
export interface SandboxRegion {
  id: string;
  selector: string;
  label?: string;
}

export interface SandboxDocumentOptions {
  /** Untrusted, publisher- or agent-authored markup. */
  html: string;
  regions: readonly SandboxRegion[];
  variantId: string;
  /** The wire message type the frame posts back under. */
  messageType: string;
  /** The per-mount nonce the parent will require on every reply. */
  nonce: string;
  /** The parent's real origin, so the frame never posts to `"*"`. */
  parentOrigin: string;
}

/** Why a sandbox refused to render active content. */
export type SandboxRefusal = {
  code: "payload_too_large";
  /** Sized for the operator, not for a log line. */
  reason: string;
  remedy: string;
};

/**
 * Measure untrusted content the way the limit is expressed: in bytes.
 *
 * `String.length` counts UTF-16 code units, which under-counts every non-ASCII
 * character an agent might emit. A limit that can be exceeded by a factor of
 * three by writing in Japanese is not a limit.
 */
export function payloadBytes(value: string): number {
  return new TextEncoder().encode(value).length;
}

/**
 * Decide whether untrusted content may be rendered as active content at all.
 *
 * Returning a refusal rather than truncating is deliberate. Truncated markup is
 * markup with a different meaning — an unclosed tag swallows the rest of the
 * document — and a renderer that silently shows a different design than the one
 * the agent produced is worse than one that says it will not show it.
 */
export function checkSandboxPayload(html: string, limitBytes: number): SandboxRefusal | null {
  const limit = limitBytes > 0 ? limitBytes : DEFAULT_SANDBOX_PAYLOAD_LIMIT_BYTES;
  const size = payloadBytes(html);
  if (size <= limit) {
    return null;
  }
  return {
    code: "payload_too_large",
    reason: `this preview is ${Math.ceil(size / 1024)} KB and the limit for sandboxed content is ${Math.floor(limit / 1024)} KB`,
    remedy: "Ask the agent to send a smaller variant.",
  };
}

/** A nonce for one frame mount. */
export function createSandboxNonce(): string {
  const runtime = globalThis.crypto;
  if (runtime && typeof runtime.randomUUID === "function") {
    return runtime.randomUUID();
  }
  // A nonce is the weakest of the four checks — source and origin already
  // pin the sender — so a fallback that is merely unguessable-per-mount is
  // enough, and is better than refusing to render where `crypto` is absent.
  return `sbx-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}

/**
 * Assemble the sandboxed document.
 *
 * The CSP goes first in `<head>` so it governs everything after it, and the
 * shim goes last in `<body>` so the untrusted markup it binds to already
 * exists. The payload rides in a `<script type="application/json">` data block:
 * CSP does not govern non-executable script blocks, so the shim's bytes stay
 * constant no matter what the payload contains, which is what lets the hash be
 * a fixed constant in two languages.
 */
export function buildSandboxDocument(options: SandboxDocumentOptions): string {
  const csp = `<meta http-equiv="Content-Security-Policy" content="${escapeAttribute(SANDBOX_FRAME_CSP)}">`;
  const payload = JSON.stringify({
    regions: options.regions,
    variantId: options.variantId,
    messageType: options.messageType,
    nonce: options.nonce,
    parentOrigin: options.parentOrigin,
  })
    // `</script` inside a JSON string would close the data block early, and
    // the two raw line terminators JSON allows but JavaScript does not would
    // break the parse. All three are escaped here rather than trusted to the
    // serializer, because the data block sits inside markup Tangent did not
    // author.
    .replace(/</g, "\\u003c")
    .replace(/\u2028/g, "\\u2028")
    .replace(/\u2029/g, "\\u2029");
  const block = `<script type="application/json" id="${SANDBOX_PAYLOAD_ELEMENT_ID}">${payload}</script>`;
  // Concatenated with no surrounding whitespace: the shim's hash is computed
  // over exactly `SANDBOX_FRAME_SHIM`, so a stray newline inside the tag would
  // not change it, but keeping the assembly literal makes that obvious.
  const shim = `${block}<script>${SANDBOX_FRAME_SHIM}</script>`;
  const html = options.html;

  if (/<head[^>]*>/i.test(html)) {
    let doc = html.replace(/<head([^>]*)>/i, `<head$1>${csp}`);
    if (/<\/body>/i.test(doc)) {
      doc = doc.replace(/<\/body>/i, `${shim}</body>`);
    } else {
      doc += shim;
    }
    return doc;
  }

  if (/<\/body>/i.test(html)) {
    return `${csp}${html.replace(/<\/body>/i, `${shim}</body>`)}`;
  }

  return `<!doctype html><html><head>${csp}</head><body>${html}${shim}</body></html>`;
}

/** What a caller must know to trust a message from its frame. */
export interface SandboxMessageExpectations {
  /** The mounted frame's `contentWindow`. Absent means no message can be real. */
  frameWindow: Window | null | undefined;
  /** The nonce handed to that frame at mount. */
  nonce: string;
  /** The wire message type this channel carries. */
  messageType: string;
}

/** A structurally valid activation from inside a sandboxed frame. */
export interface SandboxActivation {
  variantId: string;
  actionId: string;
  actionKind: "click-region";
  value: string;
}

/**
 * The provenance rule, in one place.
 *
 * Returns the activation only when **all four** of the direction document's
 * checks pass — origin, source, nonce, schema — and `null` otherwise. There is
 * no partial success and no branch that skips a check because its input is
 * missing; absence is a refusal, which is the whole correction over the
 * listener this replaces.
 *
 * The order is cheapest-and-least-disclosing first, matching `authz.Authorize`
 * and `effect.Broker.Request`, so a message from an unrelated window is dropped
 * before anything looks at its contents.
 */
export function readSandboxMessage(
  event: MessageEvent,
  expectations: SandboxMessageExpectations,
): SandboxActivation | null {
  // 1. The frame must exist. Before the iframe mounts there is no window a
  //    legitimate message could have come from, so every message in that
  //    window is by definition not from it.
  const frameWindow = expectations.frameWindow;
  if (!frameWindow) {
    return null;
  }
  // 2. The sender must be identified, and must be that frame. `event.source`
  //    is null for messages from a closed or detached context — and for a
  //    forged `MessageEvent` — so a null source is refused rather than waved
  //    through.
  if (!event.source || event.source !== frameWindow) {
    return null;
  }
  // 3. The origin must be the opaque one a correctly sandboxed frame has.
  if (event.origin !== SANDBOX_FRAME_ORIGIN) {
    return null;
  }
  const data = event.data;
  if (!data || typeof data !== "object") {
    return null;
  }
  const typed = data as Record<string, unknown>;
  // 4. The nonce must be this mount's. It is compared before the payload is
  //    read so a replay of a previous variant's message is dropped as early as
  //    a foreign one.
  if (typeof typed.nonce !== "string" || typed.nonce !== expectations.nonce) {
    return null;
  }
  if (typed.type !== expectations.messageType) {
    return null;
  }
  // 5. Schema. The frame may only ever activate a click region: buttons and
  //    text inputs are the parent's own controls, so accepting those kinds
  //    from inside the sandbox would let untrusted content synthesize a
  //    participant act it has no affordance for.
  if (typed.action_kind !== "click-region") {
    return null;
  }
  const actionId = typeof typed.action_id === "string" ? typed.action_id : "";
  if (actionId === "") {
    return null;
  }
  return {
    variantId: typeof typed.variant_id === "string" ? typed.variant_id : "",
    actionId,
    actionKind: "click-region",
    value: typeof typed.value === "string" ? typed.value : "",
  };
}

function escapeAttribute(value: string): string {
  return value.replace(/"/g, "&quot;");
}
