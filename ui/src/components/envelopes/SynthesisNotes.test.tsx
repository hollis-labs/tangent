import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { expectProseRendered, proseProbe } from "@/components/markdown/prose-probe";
import { SynthesisNotes, type SynthesisNotesEnvelope } from "./SynthesisNotes";

function hiddenEnvelope(): SynthesisNotesEnvelope {
  return {
    v: 1,
    id: "synth-1",
    type: "tangent.synthesis-notes",
    title: "Synthesis",
    data: {
      visibility: "hidden",
      has_private_notes: true,
      outline_state: "present",
    },
  };
}

function visibleEnvelope(): SynthesisNotesEnvelope {
  return {
    v: 1,
    id: "synth-2",
    type: "tangent.synthesis-notes",
    title: "Draft orientation",
    data: {
      visibility: "visible",
      summary: "The draft should foreground workflow constraints before examples.",
      outline_state: "present",
      outline: {
        title: "Draft outline",
        items: [
          { label: "Intro", description: "Set the writing goal." },
          { label: "Constraints", description: "Call out the scope limits." },
        ],
      },
    },
  };
}

describe("<SynthesisNotes>", () => {
  it("renders a hidden placeholder before the drafting gate opens", () => {
    render(<SynthesisNotes envelope={hiddenEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("synthesis-notes-hidden")).toHaveTextContent(
      "Private synthesis notes are saved on this room.",
    );
    expect(screen.queryByTestId("synthesis-notes-outline")).not.toBeInTheDocument();
  });

  it("renders summary and outline once the preview is visible", () => {
    render(<SynthesisNotes envelope={visibleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("synthesis-notes-visible")).toHaveTextContent(
      "The draft should foreground workflow constraints before examples.",
    );
    expect(screen.getByTestId("synthesis-notes-outline")).toHaveTextContent("Draft outline");
    expect(screen.getByText("Constraints")).toBeInTheDocument();
  });

  it("shows the explicit skipped state when no outline should be shown", () => {
    render(
      <SynthesisNotes
        envelope={{
          ...hiddenEnvelope(),
          id: "synth-3",
          data: { visibility: "visible", outline_state: "skipped" },
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getByTestId("synthesis-notes-skipped")).toHaveTextContent(
      "Outline preview was explicitly skipped",
    );
  });

  it("submits an ack response", () => {
    const onSubmit = vi.fn();
    render(<SynthesisNotes envelope={visibleEnvelope()} onSubmit={onSubmit} onCancel={vi.fn()} />);

    fireEvent.click(screen.getByTestId("synthesis-notes-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0][0]).toMatchObject({
      v: 1,
      envelopeId: "synth-2",
      kind: "ack",
      status: "submitted",
    });
  });

  it("routes every prose surface through the shared markdown renderer", () => {
    render(
      <SynthesisNotes
        envelope={{
          v: 1,
          id: "synth-md",
          type: "tangent.synthesis-notes",
          context: proseProbe("synth-context"),
          data: {
            visibility: "visible",
            summary: proseProbe("synth-summary"),
            outline_state: "present",
            outline: {
              title: "Draft outline",
              items: [{ label: "Intro", description: proseProbe("synth-outline") }],
            },
          },
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expectProseRendered("synth-context");
    expectProseRendered("synth-summary");
    expectProseRendered("synth-outline");
  });
});
