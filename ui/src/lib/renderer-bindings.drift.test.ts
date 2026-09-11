// Manifest-vs-boot-registration drift for the SPA's renderer table.
//
// ADR 0003 §2.3 makes the interaction definition manifest the single answer to
// "what draws this kind", and §4.5 says the renderer registry is what replaces
// `ui/src/main.tsx`'s string-literal registration. Rewiring boot to read the
// generated table is a larger change than this file; until it lands, the
// generated table and the hand-typed `register()` calls are two sources of
// truth for the same fact, and this suite is what keeps them from diverging in
// silence.
//
// This is the manifest-side counterpart to
// `components/envelopes/registry-smoke.test.tsx`, which checks the same
// `register()` calls against the Go `EnvelopeType` constants. Registration,
// server-side kind, and manifest are three separate declarations; two checks
// are needed to pin all three together.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import { RENDERER_BINDINGS } from "@/generated/renderer-bindings";

const MAIN_TSX = resolve(dirname(fileURLToPath(import.meta.url)), "..", "main.tsx");

/** Boot-time `register("<kind>", Adapter)` calls, in source order. */
function registeredKinds(): string[] {
  const source = readFileSync(MAIN_TSX, "utf8");
  return [...source.matchAll(/\bregister\(\s*"([^"]+)"/g)].map((match) => match[1]);
}

/**
 * Kinds whose manifest names a React renderer that is deliberately absent from
 * the envelope registry.
 *
 * `tangent.hitl-item` is the durable operator inbox: its manifest entry is
 * `routes/HITLInbox#HITLInbox` and it is reached through the `/hitl` route, not
 * by an envelope arriving in a room. Registering it would claim it can be
 * dispatched by `EnvelopeRouter`, which is exactly the presentation contract
 * the HITL ledger does not have.
 */
const ROUTE_RENDERED_KINDS = new Set(["tangent.hitl-item"]);

/**
 * Registered kinds whose manifest classes them as something other than
 * `react-component`, and the class each one is expected to declare.
 *
 * `tangent.design-iteration` registers a React adapter like every other room
 * workflow, but the adapter is only a shell: the payload renders inside a
 * `sandbox`ed `<iframe>` (see `components/envelopes/DesignIteration.tsx`), so
 * the manifest classes it `sandboxed-frame` and requests `sandboxed-code`
 * trust. That mismatch between "registered as React" and "classed as a frame"
 * is deliberate and is asserted below rather than filtered away — if this entry
 * ever disappears because the manifest was relaxed to `react-component`, the
 * iframe boundary went with it.
 */
const NON_REACT_REGISTRATIONS: Record<string, string> = {
  "tangent.design-iteration": "sandboxed-frame",
};

/** Every digest the manifest pipeline emits is a `sha256:`-prefixed hex string. */
const DIGEST_PATTERN = /^sha256:[0-9a-f]{64}$/;

describe("renderer binding drift", () => {
  const registered = registeredKinds();
  const bindingsByKind = new Map(RENDERER_BINDINGS.map((binding) => [binding.kind, binding]));

  /**
   * A `register()` call with no manifest behind it is a renderer the host
   * cannot describe: nothing declares its trust class, its capabilities, or the
   * contract digest §4.5 wants the registry to assert. The browser would draw
   * the envelope anyway, so the gap never surfaces as an error — it surfaces as
   * a kind that quietly escaped every policy the manifest is supposed to carry.
   *
   * The emptiness check guards the check itself: if the `register()` shape in
   * main.tsx changes and the regex stops matching, every assertion in this file
   * passes over an empty list.
   */
  it("has a manifest for every kind main.tsx registers", () => {
    expect(registered.length, "no register() calls matched — the regex is stale").toBeGreaterThan(
      0,
    );
    expect(new Set(registered).size, "a kind is registered twice").toBe(registered.length);

    const orphans = registered.filter((kind) => !bindingsByKind.has(kind));
    expect(orphans, "registered kinds with no renderer manifest").toEqual([]);
  });

  /**
   * The envelope registry hands a kind to `EnvelopeRouter`, which mounts it as
   * an ordinary component in Tangent's own React tree with whatever authority
   * that tree has. The manifest's `renderer_class` is the declaration that this
   * is the correct treatment. Registering a kind the manifest classes
   * `declarative` or `external-surface` would grant it in-tree React authority
   * the manifest never asked for — ADR 0003 §7 T6's "ambient host authority is
   * never inherited", violated by a one-line registration.
   */
  it("registers each kind under the renderer class its manifest declares", () => {
    for (const kind of registered) {
      const binding = bindingsByKind.get(kind);
      if (!binding) continue; // reported by the manifest-coverage test above

      const expected = NON_REACT_REGISTRATIONS[kind] ?? "react-component";
      expect(
        binding.rendererClass,
        `${kind} is registered but classed ${binding.rendererClass}`,
      ).toBe(expected);
    }
  });

  /**
   * The other direction, and the one that fails on the day someone ships a
   * manifest. A kind whose manifest promises a React renderer but which nobody
   * registered reaches the operator as `EnvelopeRouter`'s JSON fallback: the
   * server accepts the envelope, the browser renders raw payload, and it reads
   * as a broken workflow rather than a missing line in main.tsx.
   *
   * The exception list is asserted, not assumed: an entry that stops being a
   * genuine exception fails here rather than silently masking a real gap.
   */
  it("registers every react-component binding except the route-rendered ones", () => {
    const reactKinds = RENDERER_BINDINGS.filter(
      (binding) => binding.rendererClass === "react-component",
    ).map((binding) => binding.kind);

    const expected = reactKinds.filter((kind) => !ROUTE_RENDERED_KINDS.has(kind));
    const missing = expected.filter((kind) => !registered.includes(kind));
    expect(missing, "react-component manifests with no register() call").toEqual([]);

    for (const kind of ROUTE_RENDERED_KINDS) {
      expect(reactKinds, `${kind} is listed as route-rendered but has no react manifest`).toContain(
        kind,
      );
      expect(
        registered,
        `${kind} renders through its own route and must not be registered`,
      ).not.toContain(kind);
    }
  });

  /**
   * ADR 0003 §7 T6: a renderer that executes code, loads third-party assets, or
   * renders untrusted markup must declare its trust class and cannot be
   * `core-trusted` unless it ships and is reviewed with the release. Design
   * iteration runs publisher-supplied HTML, so it is the one binding where a
   * silent relaxation of `trust_class` would hand arbitrary third-party markup
   * the same authority as Tangent's own components — while the UI looks
   * identical either way, because the iframe attributes live in a different
   * file from the manifest that justifies them.
   */
  it("keeps sandboxed frames out of core trust", () => {
    const designIteration = bindingsByKind.get("tangent.design-iteration");
    expect(designIteration).toBeDefined();
    expect(designIteration?.rendererClass).toBe("sandboxed-frame");
    expect(designIteration?.isolation).toBe("sandboxed-frame");

    const overreaching = RENDERER_BINDINGS.filter(
      (binding) =>
        binding.rendererClass === "sandboxed-frame" && binding.isolation !== "sandboxed-frame",
    ).map((binding) => `${binding.kind}:${binding.isolation}`);
    expect(overreaching, "a sandboxed frame requested trust it cannot have").toEqual([]);
  });

  /**
   * §4.5 has the registry assert the `contract_digest` it was generated against
   * and treat a mismatched renderer as unavailable. That check can only exist
   * if every binding actually carries a digest and a materialization state — a
   * blank digest or a non-`available` state means the generated table shipped a
   * placeholder, and the runtime comparison §4.5 describes would compare
   * against nothing while reporting success.
   */
  it("ships every binding available and stamped with its contract digest", () => {
    for (const binding of RENDERER_BINDINGS) {
      expect(binding.state, `${binding.kind} is not materialized`).toBe("available");
      expect(binding.contractDigest, `${binding.kind} has no contract digest`).toMatch(
        DIGEST_PATTERN,
      );
    }
  });
});
