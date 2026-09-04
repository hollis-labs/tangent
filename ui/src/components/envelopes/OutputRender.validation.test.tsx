// Validation-affordance coverage for the final output render.
//
// Like SynthesisNotes this workflow has NO terminal gate by design — it is a
// read-only acknowledgement and "Done" is always available. Its defects were in
// the side-effect reporting: the copy status had no live region, so a failure
// was announced to nobody; `copyState` was never reset, so a stale label sat
// beside a later action; and `handleDownload` had no error path at all.

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { OutputRender, type OutputRenderEnvelope } from "./OutputRender";

const envelope: OutputRenderEnvelope = {
  v: 1,
  id: "output-1",
  type: "tangent.output-render",
  title: "Final output",
  data: { markdown: "# Result\n\nBody.", filename: "result.md", format: "markdown" },
};

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("OutputRender validation affordances", () => {
  it("has no submit gate, because it has nothing to gate", () => {
    render(<OutputRender envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    const cta = screen.getByTestId("output-render-submit");
    expect(cta).not.toBeDisabled();
    expect(cta).not.toHaveAttribute("aria-describedby");
    expect(screen.queryByTestId("output-render-submit-gate")).not.toBeInTheDocument();
    expect(document.querySelectorAll("input, textarea, select")).toHaveLength(0);
  });

  it("announces a copy failure instead of showing it to nobody", async () => {
    vi.stubGlobal("navigator", {
      clipboard: { writeText: vi.fn().mockRejectedValue(new Error("denied")) },
    });
    render(<OutputRender envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    // The live region exists before the outcome, so the change is announced
    // rather than the region's own appearance.
    const status = screen.getByTestId("output-render-copy-status");
    expect(status).toHaveAttribute("role", "status");
    expect(status).toHaveAttribute("aria-live", "polite");
    expect(status.textContent).toBe("");

    fireEvent.click(screen.getByTestId("output-render-copy"));

    await waitFor(() =>
      expect(screen.getByTestId("output-render-copy-status")).toHaveTextContent("Copy failed"),
    );
  });

  it("clears a stale outcome instead of leaving it beside the next action", async () => {
    // `copyState` used to have no reset at all, so "Copied" or "Copy failed"
    // stayed on screen indefinitely and read as the result of whatever the
    // operator did next.
    vi.useFakeTimers();
    vi.stubGlobal("navigator", {
      clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
    render(<OutputRender envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    fireEvent.click(screen.getByTestId("output-render-copy"));
    await act(async () => {});
    expect(screen.getByTestId("output-render-copy-status")).toHaveTextContent("Copied");

    act(() => {
      vi.advanceTimersByTime(5000);
    });
    expect(screen.getByTestId("output-render-copy-status").textContent).toBe("");
  });

  it("reports a download failure instead of throwing into silence", () => {
    vi.stubGlobal("URL", {
      createObjectURL: () => {
        throw new Error("blocked");
      },
      revokeObjectURL: vi.fn(),
    });
    render(<OutputRender envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    fireEvent.click(screen.getByTestId("output-render-download"));

    expect(screen.getByTestId("output-render-copy-status")).toHaveTextContent("Download failed");
  });

  it("uses a real heading rather than a styled paragraph", () => {
    render(<OutputRender envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByRole("heading", { name: "Markdown" })).toBeInTheDocument();
  });

  it("leaves the acknowledgement payload untouched", () => {
    const onSubmit = vi.fn();
    render(<OutputRender envelope={envelope} onSubmit={onSubmit} onCancel={vi.fn()} />);

    fireEvent.click(screen.getByTestId("output-render-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const response = onSubmit.mock.calls[0][0];
    expect(response.v).toBe(1);
    expect(response.envelopeId).toBe("output-1");
    expect(response.kind).toBe("ack");
    expect(response.status).toBe("submitted");
    expect(typeof response.completedAt).toBe("string");
  });
});
