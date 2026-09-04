// What the presentation sandbox actually denies, and how much of that this
// suite can honestly claim to have proved.
//
// **There is no browser in CI.** Vitest runs against happy-dom, which parses
// `sandbox` attributes and `<meta http-equiv="Content-Security-Policy">` as
// text and enforces neither. So no test in this file observes a blocked
// navigation, a blocked `fetch`, a blocked form submission, or a blocked
// download — the runtime that would block them is not present.
//
// What is provable here is the layer above that, and it is the layer that
// actually regresses: **the policy Tangent hands the browser denies each of
// those four, by construction, and cannot be weakened without a test failing.**
// Each case below names the exact token or source that would have to appear for
// the capability to work, and asserts it appears nowhere — in the sandbox
// attribute, in the frame's own CSP, or in the assembled document. An
// enforcement gap therefore requires two independent things to be wrong: this
// policy, and the browser.
//
// The one property that is genuinely end-to-end assertable is the script hash:
// the document's executable scripts are enumerable, their digests computable,
// and exactly one of them may match the policy. That is proved rather than
// asserted, at the bottom of this file.
//
// Real-browser verification lives in docs/manual-tests/renderer-sandbox-e2e.md.

import { createHash } from "node:crypto";
import { describe, expect, it } from "vitest";

import {
  buildSandboxDocument,
  checkSandboxPayload,
  createSandboxNonce,
  DEFAULT_SANDBOX_PAYLOAD_LIMIT_BYTES,
  payloadBytes,
  readSandboxMessage,
  SANDBOX_FRAME_CSP,
  SANDBOX_FRAME_ORIGIN,
  SANDBOX_FRAME_SANDBOX,
  SANDBOX_FRAME_SHIM,
  SANDBOX_FRAME_SHIM_SHA256,
  type SandboxMessageExpectations,
} from "./sandbox-frame";

/** The frame CSP as a directive map, so assertions are structural not textual. */
function directives(): Map<string, string[]> {
  const out = new Map<string, string[]>();
  for (const clause of SANDBOX_FRAME_CSP.split(";")) {
    const parts = clause.trim().split(/\s+/);
    if (parts.length === 0 || parts[0] === "") continue;
    out.set(parts[0], parts.slice(1));
  }
  return out;
}

function sandboxTokens(): string[] {
  return SANDBOX_FRAME_SANDBOX.split(/\s+/).filter((token) => token !== "");
}

function sampleDocument(html: string): string {
  return buildSandboxDocument({
    html,
    regions: [{ id: "hero", selector: "#hero", label: "Hero" }],
    variantId: "variant-a",
    messageType: "tangent:design-iteration",
    nonce: "nonce-under-test",
    parentOrigin: "http://127.0.0.1:7842",
  });
}

describe("the sandbox denies the four active-content capabilities", () => {
  // Each case names the mechanism, the single thing that would enable it, and
  // where that thing would have to appear. The assertion is that it does not.

  it("blocks navigation: no top-navigation token, and no base-uri to rewrite into", () => {
    const tokens = sandboxTokens();
    expect(tokens).not.toContain("allow-top-navigation");
    expect(tokens).not.toContain("allow-top-navigation-by-user-activation");
    expect(tokens).not.toContain("allow-top-navigation-to-custom-protocols");
    expect(tokens).not.toContain("allow-popups");
    expect(tokens).not.toContain("allow-popups-to-escape-sandbox");
    // Without `allow-same-origin` the frame is opaque-origin, so even an
    // in-frame navigation cannot reach anything of Tangent's.
    expect(tokens).not.toContain("allow-same-origin");
    // `<base href>` is the other navigation lever: it re-points every relative
    // URL in the document at once.
    expect(directives().get("base-uri")).toEqual(["'none'"]);
  });

  it("blocks network: connect-src denies, and no fallback directive re-opens it", () => {
    const csp = directives();
    expect(csp.get("connect-src")).toEqual(["'none'"]);
    // `connect-src` is the single directive covering fetch, XMLHttpRequest,
    // WebSocket, EventSource and sendBeacon. The rest of the fetch surface is
    // closed separately so a subresource cannot be used as a side channel.
    expect(csp.get("default-src")).toEqual(["'none'"]);
    expect(csp.get("img-src")).toEqual(["data:", "blob:"]);
    expect(csp.get("font-src")).toEqual(["data:", "blob:"]);
    expect(csp.get("media-src")).toEqual(["data:", "blob:"]);
    expect(csp.get("manifest-src")).toEqual(["'none'"]);
    expect(csp.get("worker-src")).toEqual(["'none'"]);
    for (const [directive, sources] of csp) {
      if (directive === "style-src" || directive === "script-src") continue;
      // No remote scheme is admitted anywhere: every allowed source is a
      // keyword or an inline scheme.
      for (const source of sources) {
        expect(source).not.toMatch(/^https?:/);
        expect(source).not.toMatch(/^wss?:/);
        expect(source).not.toBe("*");
      }
    }
  });

  it("blocks forms: no allow-forms token, and form-action denies independently", () => {
    expect(sandboxTokens()).not.toContain("allow-forms");
    // Two independent mechanisms. The sandbox token governs submission from a
    // sandboxed frame; `form-action` governs it regardless of sandboxing. Both
    // deny, so removing either one alone does not open the hole.
    expect(directives().get("form-action")).toEqual(["'none'"]);
  });

  it("blocks downloads: no allow-downloads token", () => {
    // There is no CSP directive for downloads. `allow-downloads` is the only
    // control the platform offers, which is exactly why `export.download` is
    // enforceable inside a frame and only declared in the main origin —
    // see effect.MediationFor.
    expect(sandboxTokens()).not.toContain("allow-downloads");
    expect(sandboxTokens()).not.toContain("allow-downloads-without-user-activation");
  });

  it("grants exactly one token, and it is allow-scripts", () => {
    // The security property is in what is absent, so the whole grant is
    // asserted rather than a subset of it: a token added here has to be added
    // to this test too, which is the point.
    expect(sandboxTokens()).toEqual(["allow-scripts"]);
  });
});

describe("only Tangent's own shim can execute inside the sandbox", () => {
  it("hashes the shim to the constant both languages carry", () => {
    const digest = createHash("sha256").update(SANDBOX_FRAME_SHIM, "utf8").digest("base64");
    expect(`sha256-${digest}`).toBe(SANDBOX_FRAME_SHIM_SHA256);
  });

  it("admits exactly one executable script, whatever the preview contains", () => {
    // This is the one end-to-end assertion in the file: the document is real,
    // the scripts in it are real, and CSP hash matching is a pure function of
    // their bytes. If an agent-authored script could run, its digest would have
    // to be in the policy — and it is not.
    const doc = sampleDocument(
      "<section><script>fetch('https://example.test')</script>" +
        "<img src=x onerror=\"alert(1)\"><button id='hero'>Hero</button></section>",
    );
    const allowed = new Set(directives().get("script-src") ?? []);

    const executable = [...doc.matchAll(/<script(?![^>]*type=)[^>]*>([\s\S]*?)<\/script>/g)].map(
      (match) => match[1],
    );
    expect(executable.length).toBe(2); // the agent's script, and the shim

    const admitted = executable.filter((body) =>
      allowed.has(`'sha256-${createHash("sha256").update(body, "utf8").digest("base64")}'`),
    );
    expect(admitted).toEqual([SANDBOX_FRAME_SHIM]);
  });

  it("carries the payload in a non-executable data block, so the shim's bytes never vary", () => {
    const one = sampleDocument("<p>a</p>");
    const two = buildSandboxDocument({
      html: "<p>b</p>",
      regions: [],
      variantId: "other",
      messageType: "tangent:design-iteration",
      nonce: "a-different-nonce",
      parentOrigin: "http://localhost:9999",
    });
    for (const doc of [one, two]) {
      expect(doc).toContain(`<script>${SANDBOX_FRAME_SHIM}</script>`);
      expect(doc).toContain('<script type="application/json" id="tangent-sandbox-payload">');
    }
  });

  it("neutralizes a payload that would close its own data block", () => {
    const doc = buildSandboxDocument({
      html: "<p>hi</p>",
      regions: [{ id: "x", selector: "#x", label: "</script><script>alert(1)</script>" }],
      variantId: "v",
      messageType: "tangent:design-iteration",
      nonce: "n",
      parentOrigin: "http://127.0.0.1:7842",
    });
    const start = doc.indexOf('id="tangent-sandbox-payload">');
    const end = doc.indexOf("</script>", start);
    expect(doc.slice(start, end)).not.toContain("</script");
    expect(JSON.parse(doc.slice(doc.indexOf(">", start) + 1, end)).regions[0].label).toContain(
      "</script>",
    );
  });

  it("puts the policy in the document before anything it governs", () => {
    const doc = sampleDocument("<p>hi</p>");
    expect(doc.indexOf("Content-Security-Policy")).toBeLessThan(doc.indexOf("<body"));
    expect(doc).toContain(`content="${SANDBOX_FRAME_CSP.replace(/"/g, "&quot;")}"`);
  });
});

describe("readSandboxMessage refuses on absence, not only on mismatch", () => {
  const frameWindow = { name: "frame" } as unknown as Window;
  const base: SandboxMessageExpectations = {
    frameWindow,
    nonce: "n1",
    messageType: "tangent:design-iteration",
  };
  const payload = {
    type: "tangent:design-iteration",
    nonce: "n1",
    variant_id: "v1",
    action_id: "hero",
    action_kind: "click-region",
    value: "Hero",
  };
  const event = (over: Partial<MessageEvent> & { data?: unknown }) =>
    ({
      data: payload,
      origin: SANDBOX_FRAME_ORIGIN,
      source: frameWindow,
      ...over,
    }) as MessageEvent;

  it("accepts a message that proves all four facts", () => {
    expect(readSandboxMessage(event({}), base)).toEqual({
      variantId: "v1",
      actionId: "hero",
      actionKind: "click-region",
      value: "Hero",
    });
  });

  // The four absence cases. Each one passed the old
  // `iframeWindow && event.source && event.source !== iframeWindow` guard.
  it("refuses when there is no frame yet", () => {
    expect(readSandboxMessage(event({}), { ...base, frameWindow: null })).toBeNull();
    expect(readSandboxMessage(event({}), { ...base, frameWindow: undefined })).toBeNull();
  });

  it("refuses a null source", () => {
    expect(readSandboxMessage(event({ source: null }), base)).toBeNull();
  });

  it("refuses a missing origin", () => {
    expect(readSandboxMessage(event({ origin: "" }), base)).toBeNull();
  });

  it("refuses a missing nonce", () => {
    expect(readSandboxMessage(event({ data: { ...payload, nonce: undefined } }), base)).toBeNull();
  });

  it("refuses a different window, a real origin, a stale nonce, and a wrong type", () => {
    const other = { name: "other" } as unknown as Window;
    expect(readSandboxMessage(event({ source: other }), base)).toBeNull();
    expect(readSandboxMessage(event({ origin: "http://127.0.0.1:7842" }), base)).toBeNull();
    expect(readSandboxMessage(event({ data: { ...payload, nonce: "n0" } }), base)).toBeNull();
    expect(readSandboxMessage(event({ data: { ...payload, type: "other" } }), base)).toBeNull();
  });

  it("refuses an action kind the sandbox has no affordance for", () => {
    for (const kind of ["button", "text-input", "", null, 7]) {
      expect(
        readSandboxMessage(event({ data: { ...payload, action_kind: kind } }), base),
      ).toBeNull();
    }
  });

  it("refuses a message with no action id, and non-object data", () => {
    expect(readSandboxMessage(event({ data: { ...payload, action_id: "" } }), base)).toBeNull();
    expect(readSandboxMessage(event({ data: "hello" }), base)).toBeNull();
    expect(readSandboxMessage(event({ data: null }), base)).toBeNull();
  });

  it("does not let a string field become a non-string one", () => {
    const activation = readSandboxMessage(
      event({ data: { ...payload, variant_id: 3, value: { toString: () => "x" } } }),
      base,
    );
    expect(activation).toEqual({
      variantId: "",
      actionId: "hero",
      actionKind: "click-region",
      value: "",
    });
  });
});

describe("payload bounds", () => {
  it("measures bytes rather than UTF-16 code units", () => {
    expect(payloadBytes("aaa")).toBe(3);
    // Three characters, nine bytes. A length-based limit would let this
    // through at three times the intended size.
    expect(payloadBytes("あああ")).toBe(9);
  });

  it("refuses rather than truncating", () => {
    const refusal = checkSandboxPayload("x".repeat(2048), 1024);
    expect(refusal).not.toBeNull();
    expect(refusal?.code).toBe("payload_too_large");
    // The Refused copy shape from docs/room-validation-affordances.md.
    expect(`Not submitted: ${refusal?.reason}. ${refusal?.remedy}`).toMatch(
      /^Not submitted: .+\. .+\.$/,
    );
  });

  it("bounds content even when the caller supplies no limit", () => {
    expect(checkSandboxPayload("x", 0)).toBeNull();
    expect(
      checkSandboxPayload("x".repeat(DEFAULT_SANDBOX_PAYLOAD_LIMIT_BYTES + 1), 0),
    ).not.toBeNull();
  });
});

describe("nonces", () => {
  it("differ per mount", () => {
    expect(createSandboxNonce()).not.toBe(createSandboxNonce());
  });
});
