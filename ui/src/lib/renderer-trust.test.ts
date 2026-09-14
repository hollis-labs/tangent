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
} from "./renderer-trust";

/**
 * The isolation each shipped kind carries, as reviewed.
 *
 * Everything Tangent draws in its own React tree is `main-origin`; one kind,
 * `tangent.design-iteration`, is `sandboxed-frame` because it renders markup an
 * agent produced.
 *
 * ADR 0009 reduced this table. It used to carry five trust classes, and the
 * distinction it spent the most words on — `core-trusted` versus
 * `portfolio-trusted`, the whiteboard being "portfolio" because it embeds
 * tldraw — described a supply chain Tangent does not have: both ran in the same
 * origin, under the same release review, differing by a capability with no
 * executor. A dependency choice inside a component Tangent ships is a
 * code-review fact, not a runtime boundary.
 *
 * What the reduction did NOT touch is the one entry that matters. The sandbox
 * stays exactly as it was, for the reason ADR 0009 §2 states: the agent whose
 * markup lands there is a conduit for content from a web page, a file or a
 * model's output, not the adversary — so "the agent could already run code on
 * the box" is true and does not reach this path.
 */
const SHIPPED_TRUST: Record<string, string> = {
  "tangent.agent-turn": "main-origin",
  "tangent.app-board": "main-origin",
  "tangent.approval-queue": "main-origin",
  "tangent.block-draft": "main-origin",
  "tangent.dashboard": "main-origin",
  "tangent.design-iteration": "sandboxed-frame",
  "tangent.diff-review": "main-origin",
  "tangent.feedback": "main-origin",
  "tangent.file-picker": "main-origin",
  "tangent.form-collect": "main-origin",
  "tangent.hitl-item": "main-origin",
  "tangent.interview-question": "main-origin",
  "tangent.output-render": "main-origin",
  "tangent.progress-panel": "main-origin",
  "tangent.prose-revision": "main-origin",
  "tangent.spreadsheet-review": "main-origin",
  "tangent.synthesis-notes": "main-origin",
  "tangent.triage": "main-origin",
  "tangent.whiteboard": "main-origin",
  "tangent.wizard": "main-origin",
};

describe("the shipped renderers are classified", () => {
  it("covers every binding, and only the bindings", () => {
    expect(RENDERER_BINDINGS.map((binding) => binding.kind).sort()).toEqual(
      Object.keys(SHIPPED_TRUST).sort(),
    );
  });

  it("carries the reviewed isolation for each kind", () => {
    for (const binding of RENDERER_BINDINGS) {
      expect(`${binding.kind}=${binding.isolation}`).toBe(
        `${binding.kind}=${SHIPPED_TRUST[binding.kind]}`,
      );
    }
  });

  it("keeps the declared isolation coherent with the renderer shape", () => {
    for (const binding of RENDERER_BINDINGS) {
      const expected =
        binding.rendererClass === "sandboxed-frame" ? "sandboxed-frame" : "main-origin";
      expect(`${binding.kind}=${binding.isolation}`).toBe(`${binding.kind}=${expected}`);
    }
  });

  it("admits every shipped kind for dispatch", () => {
    for (const binding of RENDERER_BINDINGS) {
      const classification = classifyRenderer(binding.kind);
      expect(`${binding.kind}:${classification.admitted}`).toBe(`${binding.kind}:true`);
    }
  });
});

describe("the trust model the SPA reads is the host's", () => {
  it("implements all four isolations, ordered from most host authority to least", () => {
    expect(RENDERER_TRUST_PROFILES.map((profile) => profile.isolation)).toEqual([
      "main-origin",
      "host-primitive",
      "sandboxed-frame",
      "external-surface",
    ]);
  });

  it("gives ambient host authority to the main origin and nowhere else", () => {
    for (const profile of RENDERER_TRUST_PROFILES) {
      expect(`${profile.isolation}=${profile.ambientHostAuthority}`).toBe(
        `${profile.isolation}=${profile.isolation === "main-origin"}`,
      );
    }
    // The acceptance criterion, stated as a test: untrusted code never runs
    // with Tangent's own authority.
    expect(hasAmbientHostAuthority("tangent.design-iteration")).toBe(false);
    expect(hasAmbientHostAuthority("tangent.triage")).toBe(true);
  });

  // The strict capability-ceiling ordering this used to assert is gone: ADR
  // 0009 removed the class-based ceiling rather than demoting it, so there is
  // no ordered set of permitted capabilities left to check. What replaced it as
  // the thing worth holding is the shape/isolation coherence above — the only
  // check standing between a declared isolation and the one in force.
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
