// Every shipped renderer is classified, and the classification is the
// manifest's — not this file's.
//
// The expectations below are written out kind by kind rather than derived from
// the generated table, which would make the suite tautological. ADR 0003 §8 C2
// makes raising a renderer's trust class a major version bump; this list is
// what turns that rule into a failing build instead of a review note.

import { describe, expect, it } from "vitest";

import { RENDERER_BINDINGS, RENDERER_TRUST_PROFILES } from "@/generated/renderer-bindings";
import {
  classifyRenderer,
  describeRendererRefusal,
  hasAmbientHostAuthority,
  permittedCapabilities,
} from "./renderer-trust";

/**
 * The trust class each shipped kind carries, as reviewed.
 *
 * Seventeen `core-trusted` React components in Tangent's own tree, one
 * `portfolio-trusted` (whiteboard embeds tldraw, a third-party editor, so it is
 * classed as a portfolio package rather than as host core), and one
 * `sandboxed-code` (design-iteration renders agent-authored HTML).
 */
const SHIPPED_TRUST: Record<string, string> = {
  "tangent.approval-queue": "core-trusted",
  "tangent.block-draft": "core-trusted",
  "tangent.dashboard": "core-trusted",
  "tangent.design-iteration": "sandboxed-code",
  "tangent.diff-review": "core-trusted",
  "tangent.feedback": "core-trusted",
  "tangent.file-picker": "core-trusted",
  "tangent.form-collect": "core-trusted",
  "tangent.hitl-item": "core-trusted",
  "tangent.interview-question": "core-trusted",
  "tangent.output-render": "core-trusted",
  "tangent.progress-panel": "core-trusted",
  "tangent.prose-revision": "core-trusted",
  "tangent.spreadsheet-review": "core-trusted",
  "tangent.synthesis-notes": "core-trusted",
  "tangent.triage": "core-trusted",
  "tangent.whiteboard": "portfolio-trusted",
  "tangent.wizard": "core-trusted",
};

describe("the shipped renderers are classified", () => {
  it("covers every binding, and only the bindings", () => {
    expect(RENDERER_BINDINGS.map((binding) => binding.kind).sort()).toEqual(
      Object.keys(SHIPPED_TRUST).sort(),
    );
  });

  it("carries the reviewed trust class for each kind", () => {
    for (const binding of RENDERER_BINDINGS) {
      expect(`${binding.kind}=${binding.trustClass}`).toBe(
        `${binding.kind}=${SHIPPED_TRUST[binding.kind]}`,
      );
    }
  });

  it("derives isolation from the class rather than from the renderer shape", () => {
    for (const binding of RENDERER_BINDINGS) {
      const expected = binding.trustClass === "sandboxed-code" ? "sandboxed-frame" : "main-origin";
      expect(`${binding.kind}=${binding.isolation}`).toBe(`${binding.kind}=${expected}`);
    }
  });

  it("admits every shipped kind for dispatch", () => {
    for (const binding of RENDERER_BINDINGS) {
      const classification = classifyRenderer(binding.kind);
      expect(`${binding.kind}:${classification.admitted}`).toBe(`${binding.kind}:true`);
    }
  });

  it("declares no host-mediated effect capability anywhere", () => {
    // v0.x ships nothing capability-mediated. The ceiling is what a class *may*
    // declare; the shipped set declares none of it, which is what makes every
    // effect request refused with `effect_capability_undeclared` today.
    for (const binding of RENDERER_BINDINGS) {
      const ceiling = permittedCapabilities(binding.kind);
      expect(ceiling.includes("process.exec")).toBe(binding.trustClass === "core-trusted");
    }
  });
});

describe("the trust model the SPA reads is the host's", () => {
  it("implements all five classes, ordered from most authority to least", () => {
    expect(RENDERER_TRUST_PROFILES.map((profile) => profile.class)).toEqual([
      "core-trusted",
      "portfolio-trusted",
      "declarative",
      "sandboxed-code",
      "external-surface",
    ]);
  });

  it("gives ambient host authority to exactly the two main-origin classes", () => {
    for (const profile of RENDERER_TRUST_PROFILES) {
      expect(`${profile.class}=${profile.ambientHostAuthority}`).toBe(
        `${profile.class}=${profile.isolation === "main-origin"}`,
      );
    }
    // The acceptance criterion, stated as a test: untrusted code never runs
    // with Tangent's own authority.
    expect(hasAmbientHostAuthority("tangent.design-iteration")).toBe(false);
    expect(hasAmbientHostAuthority("tangent.triage")).toBe(true);
  });

  it("orders the capability ceilings strictly", () => {
    const ceiling = (name: string) =>
      new Set(RENDERER_TRUST_PROFILES.find((p) => p.class === name)?.capabilities ?? []);
    const core = ceiling("core-trusted");
    const portfolio = ceiling("portfolio-trusted");
    const sandboxed = ceiling("sandboxed-code");

    expect([...portfolio].every((id) => core.has(id))).toBe(true);
    expect([...sandboxed].every((id) => portfolio.has(id))).toBe(true);
    expect(core.size).toBeGreaterThan(portfolio.size);
    expect(portfolio.size).toBeGreaterThan(sandboxed.size);
    expect(ceiling("declarative").size).toBe(0);
    expect(ceiling("external-surface").size).toBe(0);

    // Untrusted code may read what the participant can already see; it may
    // never author bytes into the operator's workspace through a host proxy.
    expect(sandboxed.has("file.read_scoped")).toBe(true);
    expect(sandboxed.has("file.write_scoped")).toBe(false);
    expect(sandboxed.has("process.exec")).toBe(false);
  });
});

describe("an unclassified kind is refused, not dispatched", () => {
  it("names the three refusal codes apart", () => {
    const unknown = classifyRenderer("tangent.not-a-kind");
    expect(unknown.admitted).toBe(false);
    if (unknown.admitted) return;
    expect(unknown.code).toBe("renderer_unclassified");
    expect(unknown.fallbackRendererID).toBe("");
    expect(describeRendererRefusal(unknown)).toMatch(/^Not submitted: .+\. .+\.$/);
  });

  it("offers no fallback for a kind that declares one it cannot preserve", () => {
    // Every shipped manifest declares `preserves_meaning: false`, so nothing in
    // this build may fall back — the correct answer under ADR 0003 §8 C5, and
    // one worth pinning: a fallback that appears the day someone flips the flag
    // without authoring a renderer would be a silent downgrade.
    for (const binding of RENDERER_BINDINGS) {
      expect(`${binding.kind}=${binding.fallbackPreservesMeaning}`).toBe(`${binding.kind}=false`);
      expect(binding.fallbackRendererId).toBe("");
    }
  });
});
