import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { OutputRender, type OutputRenderEnvelope } from "./OutputRender";

describe("<OutputRender>", () => {
  const envelope: OutputRenderEnvelope = {
    v: 1,
    id: "output-1",
    type: "tangent.output-render",
    title: "Final draft",
    data: {
      title: "Final draft",
      markdown: "# Final heading\n\nTight final paragraph.",
      filename: "final-draft.md",
      format: "markdown",
      summary: "Final polished output.",
      word_count: 5,
    },
  };

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("renders the final markdown and metadata", () => {
    render(<OutputRender envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("output-render-markdown")).toHaveTextContent("Final heading");
    expect(screen.getByText("final-draft.md")).toBeInTheDocument();
    expect(screen.getByText("5 words")).toBeInTheDocument();
  });

  it("copies markdown to the clipboard", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(globalThis.navigator, "clipboard", {
      value: { writeText },
      configurable: true,
    });

    render(<OutputRender envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);
    fireEvent.click(screen.getByTestId("output-render-copy"));

    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith("# Final heading\n\nTight final paragraph.");
    });
    expect(screen.getByTestId("output-render-copy-status")).toHaveTextContent("Copied");
  });

  it("downloads the markdown artifact", () => {
    const createObjectURL = vi.fn(() => "blob:download");
    const revokeObjectURL = vi.fn();
    const click = vi.fn();
    const appendSpy = vi.spyOn(document.body, "appendChild");
    const removeSpy = vi.spyOn(HTMLAnchorElement.prototype, "remove").mockImplementation(() => {});
    vi.stubGlobal("URL", { createObjectURL, revokeObjectURL });
    vi.spyOn(document, "createElement").mockImplementation(((tagName: string) => {
      const element = document.createElementNS(
        "http://www.w3.org/1999/xhtml",
        tagName,
      ) as HTMLElement;
      if (tagName === "a") {
        Object.defineProperty(element, "click", { value: click });
      }
      return element;
    }) as typeof document.createElement);

    render(<OutputRender envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);
    fireEvent.click(screen.getByTestId("output-render-download"));

    expect(createObjectURL).toHaveBeenCalledTimes(1);
    expect(click).toHaveBeenCalledTimes(1);
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:download");
    expect(appendSpy).toHaveBeenCalled();
    expect(removeSpy).toHaveBeenCalled();
  });

  it("submits an acknowledgement when done", () => {
    const onSubmit = vi.fn();
    render(<OutputRender envelope={envelope} onSubmit={onSubmit} onCancel={vi.fn()} />);

    fireEvent.click(screen.getByTestId("output-render-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0][0]).toMatchObject({
      v: 1,
      envelopeId: "output-1",
      kind: "ack",
      status: "submitted",
    });
  });
});
