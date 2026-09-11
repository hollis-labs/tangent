import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { expectProseRendered, proseProbe } from "@/components/markdown/prose-probe";
import { InterviewQuestion, type InterviewQuestionEnvelope } from "./InterviewQuestion";

function sampleEnvelope(): InterviewQuestionEnvelope {
  return {
    v: 1,
    id: "interview-1",
    type: "tangent.interview-question",
    title: "Interview",
    data: {
      prompt: "What should this workflow optimize for next?",
      helper_text: "Long-form answer first, then any formatting preferences.",
      thread_id: "goals",
      topic_label: "Workflow goals",
      choices: [
        { id: "speed", label: "Speed" },
        { id: "quality", label: "Quality" },
      ],
      output_shape: {
        label: "Preferred output shape",
        help: "If you want a particular format, say so explicitly.",
      },
    },
  };
}

describe("<InterviewQuestion>", () => {
  it("renders prompt, metadata, quick picks, and output-shape prompt", () => {
    render(<InterviewQuestion envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("interview-question-prompt")).toHaveTextContent(
      "What should this workflow optimize for next?",
    );
    expect(screen.getByText("thread goals")).toBeInTheDocument();
    expect(screen.getByText("topic Workflow goals")).toBeInTheDocument();
    expect(screen.getByTestId("interview-question-choice-speed")).toBeInTheDocument();
    expect(screen.getByTestId("interview-question-output-shape-input")).toBeInTheDocument();
  });

  it("keeps submit disabled until answer text is present", () => {
    render(<InterviewQuestion envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    const submit = screen.getByTestId("interview-question-submit");
    expect(submit).toBeDisabled();

    fireEvent.change(screen.getByTestId("interview-question-answer"), {
      target: { value: "We should optimize for reviewability." },
    });

    expect(submit).not.toBeDisabled();
  });

  it("emits the shaped response payload", () => {
    const onSubmit = vi.fn();
    render(
      <InterviewQuestion envelope={sampleEnvelope()} onSubmit={onSubmit} onCancel={vi.fn()} />,
    );

    fireEvent.click(screen.getByTestId("interview-question-choice-quality"));
    fireEvent.change(screen.getByTestId("interview-question-answer"), {
      target: { value: "Bias toward quality, especially around narrative coherence." },
    });
    fireEvent.change(screen.getByTestId("interview-question-output-shape-input"), {
      target: { value: "Bullets first, then a short synthesis." },
    });
    fireEvent.click(screen.getByTestId("interview-question-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const payload = onSubmit.mock.calls[0][0];
    expect(payload).toMatchObject({
      v: 1,
      envelopeId: "interview-1",
      kind: "data",
      status: "submitted",
      payload: {
        answer_text: "Bias toward quality, especially around narrative coherence.",
        selected_choice_id: "quality",
        thread_id: "goals",
        topic_label: "Workflow goals",
        output_shape_signal: "Bullets first, then a short synthesis.",
      },
    });
    expect(typeof payload.completedAt).toBe("string");
  });

  it("cancel calls onCancel", () => {
    const onCancel = vi.fn();
    render(
      <InterviewQuestion envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={onCancel} />,
    );

    fireEvent.click(screen.getByTestId("interview-question-cancel"));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("routes every prose surface through the shared markdown renderer", () => {
    render(
      <InterviewQuestion
        envelope={{
          v: 1,
          id: "interview-md",
          type: "tangent.interview-question",
          context: proseProbe("iq-context"),
          data: {
            prompt: proseProbe("iq-prompt"),
            helper_text: proseProbe("iq-helper"),
          },
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expectProseRendered("iq-context");
    expectProseRendered("iq-prompt");
    expectProseRendered("iq-helper");
  });
});
