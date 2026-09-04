import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { SANDBOX_FRAME_ORIGIN, SANDBOX_PAYLOAD_ELEMENT_ID } from "@/lib/sandbox-frame";
import { DesignIteration, type DesignIterationEnvelope } from "./DesignIteration";

/**
 * The nonce the component minted for this mount, read back out of the document
 * it handed the frame.
 *
 * A test that made its own nonce would be testing a different channel than the
 * one that ships. This reads the real one out of the non-executable data block,
 * which is also a check that the payload is where the shim expects it.
 */
function mountNonce(iframe: HTMLIFrameElement): string {
  const srcdoc = iframe.getAttribute("srcdoc") ?? "";
  const marker = `id="${SANDBOX_PAYLOAD_ELEMENT_ID}">`;
  const start = srcdoc.indexOf(marker) + marker.length;
  const end = srcdoc.indexOf("</script>", start);
  return JSON.parse(srcdoc.slice(start, end)).nonce as string;
}

/** A well-formed activation, with any one fact overridden to make it invalid. */
function activation(
  iframe: HTMLIFrameElement,
  overrides: {
    source?: Window | null;
    origin?: string;
    data?: Record<string, unknown>;
  },
): MessageEvent {
  return new MessageEvent("message", {
    data: {
      type: "tangent:design-iteration",
      nonce: mountNonce(iframe),
      variant_id: "variant-a",
      action_id: "hero",
      action_kind: "click-region",
      value: "Hero button",
      ...(overrides.data ?? {}),
    },
    origin: overrides.origin ?? SANDBOX_FRAME_ORIGIN,
    source: "source" in overrides ? overrides.source : (iframe.contentWindow ?? null),
  });
}

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

  it("submits the shaped response on a message that passes all four checks", () => {
    const onSubmit = vi.fn();
    render(<DesignIteration envelope={sampleEnvelope()} onSubmit={onSubmit} onCancel={vi.fn()} />);

    const iframe = screen.getByTestId("design-iteration-iframe") as HTMLIFrameElement;
    window.dispatchEvent(activation(iframe, {}));

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

  // CW-20260904-0138. The listener used to read
  // `if (iframeWindow && event.source && event.source !== iframeWindow) return;`
  // — a comparison that skips itself whenever either operand is absent. Both
  // absences were reachable: a message dispatched before the frame mounts, and
  // a `MessageEvent` constructed with a null source. Each case below fails
  // against that condition and passes against the rule in lib/sandbox-frame.
  describe("refuses a message that does not prove where it came from", () => {
    it("with no source at all", () => {
      const onSubmit = vi.fn();
      render(
        <DesignIteration envelope={sampleEnvelope()} onSubmit={onSubmit} onCancel={vi.fn()} />,
      );
      const iframe = screen.getByTestId("design-iteration-iframe") as HTMLIFrameElement;

      window.dispatchEvent(activation(iframe, { source: null }));

      expect(onSubmit).not.toHaveBeenCalled();
    });

    it("from another window", () => {
      const onSubmit = vi.fn();
      render(
        <DesignIteration envelope={sampleEnvelope()} onSubmit={onSubmit} onCancel={vi.fn()} />,
      );
      const iframe = screen.getByTestId("design-iteration-iframe") as HTMLIFrameElement;

      window.dispatchEvent(activation(iframe, { source: window }));

      expect(onSubmit).not.toHaveBeenCalled();
    });

    it("from a non-opaque origin", () => {
      const onSubmit = vi.fn();
      render(
        <DesignIteration envelope={sampleEnvelope()} onSubmit={onSubmit} onCancel={vi.fn()} />,
      );
      const iframe = screen.getByTestId("design-iteration-iframe") as HTMLIFrameElement;

      // What a frame that had lost its sandbox would send. Refusing it means a
      // weakened sandbox breaks the workflow instead of silently widening it.
      window.dispatchEvent(activation(iframe, { origin: window.location.origin }));

      expect(onSubmit).not.toHaveBeenCalled();
    });

    it("with the wrong nonce", () => {
      const onSubmit = vi.fn();
      render(
        <DesignIteration envelope={sampleEnvelope()} onSubmit={onSubmit} onCancel={vi.fn()} />,
      );
      const iframe = screen.getByTestId("design-iteration-iframe") as HTMLIFrameElement;

      window.dispatchEvent(activation(iframe, { data: { nonce: "not-this-mount" } }));

      expect(onSubmit).not.toHaveBeenCalled();
    });

    it("claiming an action kind the sandbox has no affordance for", () => {
      const onSubmit = vi.fn();
      render(
        <DesignIteration envelope={sampleEnvelope()} onSubmit={onSubmit} onCancel={vi.fn()} />,
      );
      const iframe = screen.getByTestId("design-iteration-iframe") as HTMLIFrameElement;

      // `button` and `text-input` are the parent's controls. Untrusted markup
      // must not be able to synthesize a participant act it cannot present.
      window.dispatchEvent(activation(iframe, { data: { action_kind: "button" } }));
      window.dispatchEvent(activation(iframe, { data: { action_kind: "text-input" } }));

      expect(onSubmit).not.toHaveBeenCalled();
    });
  });

  it("declares every click region as a keyboard-reachable control", () => {
    render(<DesignIteration envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    // The shim is what runs inside the frame, and happy-dom does not execute
    // srcdoc. What is assertable here is that the shim it injects is the one
    // that binds keyboard affordances, and that the parent tells an operator
    // the frame is interactive before they enter it.
    const srcdoc = screen.getByTestId("design-iteration-iframe").getAttribute("srcdoc") ?? "";
    expect(srcdoc).toContain('setAttribute("tabindex", "0")');
    expect(srcdoc).toContain('setAttribute("role", "button")');
    expect(srcdoc).toContain('addEventListener("keydown"');
    expect(screen.getByTestId("design-iteration-region-hint")).toHaveTextContent(
      /1 selectable region/,
    );
  });

  it("refuses active content over the manifest payload limit on the Refused surface", () => {
    const envelope = sampleEnvelope();
    // The manifest declares 262144 bytes for this kind.
    envelope.data = { ...envelope.data, html: "x".repeat(300_000) };

    render(<DesignIteration envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.queryByTestId("design-iteration-iframe")).not.toBeInTheDocument();
    const refusal = screen.getByTestId("design-iteration-sandbox-refusal");
    expect(refusal).toHaveAttribute("role", "alert");
    expect(refusal.textContent ?? "").toMatch(/^Not submitted: .+\. .+\.$/);
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
