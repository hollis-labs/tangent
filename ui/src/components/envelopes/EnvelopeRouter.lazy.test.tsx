// Renderers are code-split: main.tsx registers React.lazy components, so the
// router must supply the Suspense boundary and show the renderer once its chunk
// resolves.

import { render, screen } from "@testing-library/react";
import { lazy } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { _resetRegistryForTests, register } from "@/lib/envelope-registry";
import { EnvelopeRouter } from "./EnvelopeRouter";

describe("EnvelopeRouter with a code-split renderer", () => {
  beforeEach(() => {
    _resetRegistryForTests();
  });

  it("shows a loading state, then the renderer once its chunk resolves", async () => {
    const Triage = lazy(async () => ({
      default: () => <div data-testid="lazy-triage">triage ready</div>,
    }));
    register("tangent.triage", Triage);

    render(
      <EnvelopeRouter
        envelope={{ v: 1, id: "e1", type: "tangent.triage", data: { items: [] } }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getByTestId("envelope-router-loading")).toBeInTheDocument();
    expect(await screen.findByTestId("lazy-triage")).toHaveTextContent("triage ready");
    expect(screen.queryByTestId("envelope-router-loading")).not.toBeInTheDocument();
  });
});
