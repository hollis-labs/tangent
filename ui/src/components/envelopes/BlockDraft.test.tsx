import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { expectProseRendered, proseProbe } from "@/components/markdown/prose-probe";
import { BlockDraft, type BlockDraftEnvelope } from "./BlockDraft";

describe("<BlockDraft>", () => {
  const envelope: BlockDraftEnvelope = {
    v: 1,
    id: "draft-1",
    type: "tangent.block-draft",
    title: "Intro block",
    data: {
      block_id: "intro",
      mode: "section",
      label: "Intro",
      content: "This is the candidate draft block.",
      rationale: "Lead with the main point.",
      outline_hint: "Outline item: introduction",
      current_draft: {
        block_count: 1,
        markdown: "Accepted block so far.",
        blocks: [{ block_id: "prior", content: "Accepted block so far." }],
      },
    },
  };

  it("renders the candidate and the reconstructed current draft", () => {
    render(<BlockDraft envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("block-draft-current-draft")).toHaveTextContent(
      "Accepted block so far.",
    );
    expect(screen.getByTestId("block-draft-candidate")).toHaveTextContent(
      "This is the candidate draft block.",
    );
  });

  it("submits accept responses without requiring extra notes", () => {
    const onSubmit = vi.fn();
    render(<BlockDraft envelope={envelope} onSubmit={onSubmit} onCancel={vi.fn()} />);

    fireEvent.click(screen.getByTestId("block-draft-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0][0].payload).toMatchObject({
      decision: "accept",
      block_id: "intro",
      mode: "section",
    });
  });

  it("requires edited text for inline edits and sends the final text", () => {
    const onSubmit = vi.fn();
    render(<BlockDraft envelope={envelope} onSubmit={onSubmit} onCancel={vi.fn()} />);

    fireEvent.click(screen.getByTestId("block-draft-decision-inline_edit"));
    fireEvent.change(screen.getByTestId("block-draft-edited-text"), {
      target: { value: "Edited inline block." },
    });
    fireEvent.change(screen.getByTestId("block-draft-feedback"), {
      target: { value: "Tightened the wording." },
    });
    fireEvent.click(screen.getByTestId("block-draft-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0][0].payload).toMatchObject({
      decision: "inline_edit",
      edited_text: "Edited inline block.",
      feedback: "Tightened the wording.",
    });
  });

  it("requires notes for revision requests", () => {
    const onSubmit = vi.fn();
    render(<BlockDraft envelope={envelope} onSubmit={onSubmit} onCancel={vi.fn()} />);

    fireEvent.click(screen.getByTestId("block-draft-decision-revise"));
    expect(screen.getByTestId("block-draft-submit")).toBeDisabled();

    fireEvent.change(screen.getByTestId("block-draft-feedback"), {
      target: { value: "Push harder on constraints." },
    });
    expect(screen.getByTestId("block-draft-submit")).not.toBeDisabled();
  });

  it("routes every prose surface through the shared markdown renderer", () => {
    render(
      <BlockDraft
        envelope={{
          v: 1,
          id: "draft-md",
          type: "tangent.block-draft",
          context: proseProbe("draft-context"),
          data: {
            block_id: "intro",
            mode: "section",
            content: proseProbe("draft-content"),
            rationale: proseProbe("draft-rationale"),
            outline_hint: proseProbe("draft-hint"),
            current_draft: {
              block_count: 1,
              markdown: proseProbe("draft-current"),
              blocks: [{ block_id: "prior", content: "Accepted." }],
            },
          },
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expectProseRendered("draft-context");
    expectProseRendered("draft-current");
    expectProseRendered("draft-hint");
    expectProseRendered("draft-content");
    expectProseRendered("draft-rationale");
  });
});
