// Whether a renderer may draw a kind at all, decided from the manifest.
//
// This is the browser half of ADR 0003 §2.3's "a manifest requests a trust
// class; Tangent policy decides". The Go half runs at materialization
// (`internal/definition/trust.go`) and refuses a definition whose evidence does
// not support its class, or whose declared capabilities its class does not
// permit. Nothing carried that decision into the SPA: `main.tsx` registered
// components by string literal, and `EnvelopeRouter` rendered whatever the
// registry returned. A kind with no manifest at all, or one the host had
// quarantined, drew its React component in Tangent's own origin exactly like a
// reviewed one.
//
// The table this reads is generated from the Go trust table, so there is one
// policy and not two (see ui/src/generated/renderer-bindings.ts).
//
// # What this can and cannot enforce
//
// It gates *dispatch*: an unclassified or unservable kind does not reach a
// component. It does not sandbox a component that is dispatched — a
// `react-component` renderer in `isolation: "main-origin"` runs with the
// document's full authority by construction, which is what that isolation
// means. Containment for untrusted code is the sandboxed frame in
// `sandbox-frame.ts`, and the two are different jobs: this one decides whether
// code runs, that one decides what it can reach once it does.

import {
  type RendererBinding,
  type RendererTrustProfile,
  rendererBindingFor,
  trustProfileFor,
} from "@/generated/renderer-bindings";
import { describeRefusal } from "./refusal";

/** A renderer Tangent will dispatch to, with the policy that admitted it. */
export interface AdmittedRenderer {
  admitted: true;
  binding: RendererBinding;
  profile: RendererTrustProfile;
}

/**
 * A renderer Tangent refuses to dispatch to, and why.
 *
 * The three codes are the browser-visible half of the states
 * `definition.Materialized` distinguishes, and they stay distinguishable for
 * the reason ADR 0003 §8 C7 gives: "unavailable is a state, not an error to
 * paper over". An operator who sees `unclassified` has a manifest to write; one
 * who sees `unavailable` has a host to ask.
 */
export interface RefusedRenderer {
  admitted: false;
  code: "renderer_unclassified" | "renderer_unavailable" | "trust_class_unimplemented";
  /** The middle clause of "Not submitted: <reason>. <what to do>." */
  reason: string;
  /** The last clause. */
  remedy: string;
  /**
   * The declared safe fallback, present only when the manifest declared one
   * *and* `preserves_meaning` is true — the same rule
   * `definition.Materialized.SafeFallback` applies. A fallback that does not
   * preserve the interaction's required decision is not offered, because
   * degrading a structured decision to something that cannot express it is
   * worse than refusing (ADR 0003 §8 C5).
   */
  fallbackRendererID: string;
}

export type RendererClassification = AdmittedRenderer | RefusedRenderer;

/**
 * Classify one wire kind.
 *
 * Order matters and mirrors `definition.Materialize`: identity first, then
 * trust, then servability. A kind nothing classified is refused before anyone
 * asks whether the host would have served it, because "we have no idea what
 * this is" and "we know what it is and will not serve it" are different
 * answers and only the second one is actionable by an operator.
 */
export function classifyRenderer(kind: string): RendererClassification {
  const binding = rendererBindingFor(kind);
  if (!binding) {
    return {
      admitted: false,
      code: "renderer_unclassified",
      reason: `nothing in this build classifies a renderer for "${kind}"`,
      remedy: "Ask the agent to send a kind this Tangent ships.",
      fallbackRendererID: "",
    };
  }
  const profile = trustProfileFor(binding.isolation);
  if (!profile) {
    return {
      admitted: false,
      code: "trust_class_unimplemented",
      reason: `this build does not implement the renderer isolation "${binding.isolation}"`,
      remedy: "Update Tangent, then reload.",
      fallbackRendererID: safeFallback(binding),
    };
  }
  if (binding.state !== "available") {
    return {
      admitted: false,
      code: "renderer_unavailable",
      reason: `this Tangent will not serve "${kind}" (${binding.state})`,
      remedy: "Ask the agent to use a different workflow.",
      fallbackRendererID: safeFallback(binding),
    };
  }
  return { admitted: true, binding, profile };
}

/**
 * Whether a kind's renderer runs with Tangent's own authority.
 *
 * Exported so a caller that needs to know — a component deciding whether it may
 * touch browser storage, a test asserting the classification of the shipped
 * set — reads the manifest's answer rather than inferring one from the
 * component's file path.
 */
export function hasAmbientHostAuthority(kind: string): boolean {
  const classification = classifyRenderer(kind);
  return classification.admitted && classification.profile.ambientHostAuthority;
}

/** The Refused copy for a classification, through the one owner of that shape. */
export function describeRendererRefusal(refusal: RefusedRenderer): string {
  return describeRefusal(refusal.reason, refusal.remedy);
}

function safeFallback(binding: RendererBinding): string {
  return binding.fallbackPreservesMeaning ? binding.fallbackRendererId : "";
}
