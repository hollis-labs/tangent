import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import {
  expectProseLiteral,
  expectProseRendered,
  proseProbe,
} from "@/components/markdown/prose-probe";
import { ProseRevision, type ProseRevisionEnvelope } from "./ProseRevision";

describe("<ProseRevision>", () => {
  const envelope: ProseRevisionEnvelope = {
    v: 1,
    id: "rev-1",
    type: "tangent.prose-revision",
    title: "Review the opening",
    data: {
      lens: "review",
      revision_id: "opening-pass",
      block_id: "intro",
      source_text: "The original opening paragraph.",
      suggestions: [
        {
          id: "s1",
          label: "Clarify the claim",
          original_text: "The original opening paragraph.",
          suggested_text: "Open with the main claim before the setup.",
        },
        {
          id: "s2",
          label: "Tighten the example",
          suggested_text: "Cut the second example and keep one concrete case.",
          reason: "The current version feels repetitive.",
        },
      ],
      current_draft: {
        block_count: 1,
        markdown: "Accepted intro draft.",
        blocks: [{ block_id: "intro", content: "Accepted intro draft." }],
      },
    },
  };

  it("renders the active lens and current draft context", () => {
    render(<ProseRevision envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("prose-revision-lens")).toHaveTextContent("Review lens");
    expect(screen.getByTestId("prose-revision-current-draft")).toHaveTextContent(
      "Accepted intro draft.",
    );
  });

  it("requires a decision for every suggestion", () => {
    render(<ProseRevision envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("prose-revision-submit")).toBeDisabled();
    fireEvent.click(screen.getByTestId("prose-revision-decision-s1-accept"));
    expect(screen.getByTestId("prose-revision-submit")).toBeDisabled();
    fireEvent.click(screen.getByTestId("prose-revision-decision-s2-reject"));
    expect(screen.getByTestId("prose-revision-submit")).not.toBeDisabled();
  });

  it("requires comment text for comment decisions and submits explicit outcomes", () => {
    const onSubmit = vi.fn();
    render(<ProseRevision envelope={envelope} onSubmit={onSubmit} onCancel={vi.fn()} />);

    fireEvent.click(screen.getByTestId("prose-revision-decision-s1-accept"));
    fireEvent.click(screen.getByTestId("prose-revision-decision-s2-comment"));
    expect(screen.getByTestId("prose-revision-submit")).toBeDisabled();

    fireEvent.change(screen.getByTestId("prose-revision-comment-s2"), {
      target: { value: "Keep the example, but rewrite it more concretely." },
    });
    fireEvent.change(screen.getByTestId("prose-revision-general-comment"), {
      target: { value: "Prefer the structural fix over new flourishes." },
    });
    fireEvent.click(screen.getByTestId("prose-revision-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0][0].payload).toEqual({
      lens: "review",
      revision_id: "opening-pass",
      block_id: "intro",
      outcomes: [
        { suggestion_id: "s1", decision: "accept", comment: undefined },
        {
          suggestion_id: "s2",
          decision: "comment",
          comment: "Keep the example, but rewrite it more concretely.",
        },
      ],
      general_comment: "Prefer the structural fix over new flourishes.",
    });
  });

  it("routes every prose surface through the shared markdown renderer", () => {
    render(
      <ProseRevision
        envelope={{
          v: 1,
          id: "rev-md",
          type: "tangent.prose-revision",
          context: proseProbe("rev-context"),
          data: {
            lens: "review",
            revision_id: "pass-1",
            summary: proseProbe("rev-summary"),
            source_text: "The original opening paragraph.",
            suggestions: [
              {
                id: "s1",
                label: "Clarify",
                suggested_text: "Open with the claim.",
                reason: proseProbe("rev-reason"),
              },
            ],
            current_draft: {
              block_count: 1,
              markdown: proseProbe("rev-draft"),
              blocks: [{ block_id: "intro", content: "Accepted." }],
            },
          },
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expectProseRendered("rev-context");
    expectProseRendered("rev-summary");
    expectProseRendered("rev-draft");
    expectProseRendered("rev-reason");
  });

  // The Leave bucket, pinned. `source_text` and the two per-suggestion wordings
  // are what the reviewer compares character by character — the suggestions
  // quote exact substrings of the source — so a renderer that reflowed them
  // would destroy the comparison. Same reasoning as a diff hunk.
  it("keeps the source text and both suggestion wordings literal", () => {
    render(
      <ProseRevision
        envelope={{
          v: 1,
          id: "rev-literal",
          type: "tangent.prose-revision",
          data: {
            lens: "review",
            revision_id: "pass-2",
            source_text: proseProbe("rev-source"),
            suggestions: [
              {
                id: "s1",
                label: "Clarify",
                original_text: proseProbe("rev-original"),
                suggested_text: proseProbe("rev-suggested"),
              },
            ],
          },
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expectProseLiteral(screen.getByTestId("prose-revision-source"), "rev-source");
    expectProseLiteral(screen.getByTestId("prose-revision-original-s1"), "rev-original");
    expectProseLiteral(screen.getByTestId("prose-revision-suggested-s1"), "rev-suggested");
  });
});
