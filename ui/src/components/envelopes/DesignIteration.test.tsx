import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { DesignIteration, type DesignIterationEnvelope } from "./DesignIteration";

function sampleEnvelope(): DesignIterationEnvelope {
  return {
    v: 1,
    id: "design-1",
    type: "tangent.design-iteration",
    title: "Explore a landing page",
    data: {
      caption: "Click the area that feels strongest.",
      variant_id: "variant-a",
      html: "<section><button id='hero'>Hero button</button></section>",
      prompts: [
        {
          id: "hero",
          kind: "click-region",
          label: "Hero button",
          selector: "#hero",
        },
        {
          id: "needs-copy",
          kind: "text-input",
          label: "What copy should change?",
          placeholder: "Describe the change",
        },
      ],
      prior_variants: [
        {
          envelope_id: "design-old",
          variant_id: "variant-0",
          title: "Previous concept",
          caption: "Earlier draft",
          html: "<div>Old</div>",
        },
      ],
    },
  };
}

describe("<DesignIteration>", () => {
  it("renders the iframe with the supplied HTML", () => {
    render(<DesignIteration envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    const iframe = screen.getByTestId("design-iteration-iframe");
    expect(iframe).toHaveAttribute("srcdoc");
    expect(iframe.getAttribute("srcdoc")).toContain("Hero button");
  });

  it("keeps the iframe sandbox locked to allow-scripts only", () => {
    render(<DesignIteration envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("design-iteration-iframe")).toHaveAttribute(
      "sandbox",
      "allow-scripts",
    );
  });

  it("submits the shaped response on iframe postMessage", () => {
    const onSubmit = vi.fn();
    render(<DesignIteration envelope={sampleEnvelope()} onSubmit={onSubmit} onCancel={vi.fn()} />);

    const iframe = screen.getByTestId("design-iteration-iframe") as HTMLIFrameElement;
    window.dispatchEvent(
      new MessageEvent("message", {
        data: {
          type: "tangent:design-iteration",
          variant_id: "variant-a",
          action_id: "hero",
          action_kind: "click-region",
          value: "Hero button",
        },
        source: iframe.contentWindow ?? null,
      }),
    );

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0][0]).toMatchObject({
      v: 1,
      envelopeId: "design-1",
      kind: "data",
      status: "submitted",
      payload: {
        variant_id: "variant-a",
        action_id: "hero",
        action_kind: "click-region",
        value: "Hero button",
      },
    });
  });

  it("renders history tabs when prior variants are supplied", () => {
    render(<DesignIteration envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("design-iteration-tab-variant-0")).toBeInTheDocument();
    expect(screen.getByTestId("design-iteration-tab-variant-a")).toBeInTheDocument();
  });

  it("Cancel calls onCancel", () => {
    const onCancel = vi.fn();
    render(<DesignIteration envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={onCancel} />);

    fireEvent.click(screen.getByTestId("design-iteration-cancel"));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});
