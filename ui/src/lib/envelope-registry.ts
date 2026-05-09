// envelope-registry — type→component map consulted by EnvelopeRouter.
//
// Components register themselves at app boot (see ui/src/main.tsx).
// v0.1 keeps registration manual; auto-registration from codegen
// (mirroring EnvelopeKindMap from generated/envelope-types.ts) is a
// v0.3 concern, after we have more than one component to register.
//
// The registry is keyed by the canonical wire `type` string. For
// plugin-extension types (like Tangent's `tangent.triage`) the wire
// name includes the namespace prefix; for go-envelopes core types
// (e.g. `info-card`) it's the bare name.
//
// Components MUST accept the `EnvelopeComponentProps` shape: a typed
// envelope plus onSubmit/onCancel callbacks. The Room owns the WS
// transport; components only fire these props.

import type { ComponentType } from "react";

/**
 * Common props every envelope component receives. `envelope` is left
 * loosely typed (`unknown`) at the registry boundary; concrete
 * components narrow via their own typed wrapper (see Triage.tsx).
 */
export type EnvelopeComponentProps = {
  envelope: unknown;
  onSubmit: (response: unknown) => void;
  onCancel: () => void;
  roomID?: string;
};

export type EnvelopeComponent = ComponentType<EnvelopeComponentProps>;

const registry = new Map<string, EnvelopeComponent>();

/**
 * Register a component for an envelope type. Calling register() with
 * the same type replaces the previous binding — this is intentional
 * for HMR / hot-reload, but boot-time call sites should treat duplicate
 * registration as a programming error.
 */
export function register(type: string, component: EnvelopeComponent): void {
  if (!type) {
    throw new Error("envelope-registry: type is required");
  }
  registry.set(type, component);
}

/**
 * Look up a component by envelope type. Returns null when no
 * component is registered, signalling EnvelopeRouter to fall back to
 * the JSON debug renderer.
 */
export function lookup(type: string): EnvelopeComponent | null {
  return registry.get(type) ?? null;
}

/**
 * Test-only — drop all registrations. Production code never calls
 * this; the export keeps the registry deterministic across vitest's
 * module-graph reuse.
 */
export function _resetRegistryForTests(): void {
  registry.clear();
}
