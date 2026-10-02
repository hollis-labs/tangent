// The router refuses to dispatch to a renderer no manifest classified.
//
// Before CW-20260825-0073 this path did not exist: `register()` was the whole
// admission decision, so a component registered under any string ran in
// Tangent's own origin whether or not a manifest had ever classified it. That
// is the "ambient host authority is never inherited" rule (ADR 0003 §7 T6)
// failing at the only place a browser could enforce it.
//
// The two cases below are the two shapes that failure takes: a component
// registered for a kind with no binding, and one whose binding says the host
// will not serve it.

import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { _resetRegistryForTests, register } from "@/lib/envelope-registry";
import { EnvelopeRouter } from "./EnvelopeRouter";

function Untrusted() {
  return <div data-testid="untrusted-renderer">rendered</div>;
}

describe("EnvelopeRouter enforces renderer classification", () => {
  beforeEach(() => {
    _resetRegistryForTests();
  });

  it("refuses a registered component whose kind no manifest classifies", () => {
    register("tangent.smuggled", Untrusted);

    render(
      <EnvelopeRouter
        envelope={{ v: 1, id: "e1", type: "tangent.smuggled" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.queryByTestId("untrusted-renderer")).not.toBeInTheDocument();
    const refusal = screen.getByTestId("envelope-router-refused");
    expect(refusal.querySelector('[role="alert"]')?.textContent ?? "").toMatch(
      /^Not submitted: nothing in this build classifies a renderer for "tangent\.smuggled"\. .+\.$/,
    );
  });

  it("releases the caller rather than leaving the room to time out", () => {
    const onCancel = vi.fn();
    register("tangent.smuggled", Untrusted);

    render(
      <EnvelopeRouter
        envelope={{ v: 1, id: "e1", type: "tangent.smuggled" }}
        onSubmit={vi.fn()}
        onCancel={onCancel}
      />,
    );
    screen.getByText("Cancel").click();

    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("still dispatches a classified kind", () => {
    // tangent.triage is core-trusted and available in the generated table.
    register("tangent.triage", Untrusted);

    render(
      <EnvelopeRouter
        envelope={{ v: 1, id: "e1", type: "tangent.triage" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getByTestId("untrusted-renderer")).toBeInTheDocument();
    expect(screen.queryByTestId("envelope-router-refused")).not.toBeInTheDocument();
  });

  it("keeps the JSON debug fallback for a kind with no component at all", () => {
    // The fallback executes no publisher code — it prints the payload — which
    // is why it stays reachable while a classified-but-unservable kind does
    // not. Losing it would make an unknown envelope invisible in development.
    render(
      <EnvelopeRouter
        envelope={{ v: 1, id: "e1", type: "tangent.unknown" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getByTestId("envelope-router-fallback")).toBeInTheDocument();
  });

  it("renders a wire-only core kind through the host fallback and releases the caller", () => {
    const onCancel = vi.fn();
    const onSubmit = vi.fn();
    render(
      <EnvelopeRouter
        envelope={{
          v: 1,
          id: "core-info",
          type: "info-card",
          data: { title: "Core catalog", body: "Payload remains visible" },
        }}
        onSubmit={onSubmit}
        onCancel={onCancel}
      />,
    );

    const fallback = screen.getByTestId("envelope-router-fallback");
    expect(fallback).toHaveTextContent('No component registered for "info-card"');
    expect(fallback).toHaveTextContent("Payload remains visible");
    screen.getByRole("button", { name: "Cancel" }).click();
    expect(onCancel).toHaveBeenCalledOnce();
    expect(onSubmit).not.toHaveBeenCalled();
  });
});
