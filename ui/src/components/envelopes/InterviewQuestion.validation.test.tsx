// Validation-affordance coverage for the interview question.
//
// This workflow has exactly one requirement and it was invisible: the Answer
// box is mandatory in every envelope, was labelled "Answer" with no marker and
// no `aria-required`, and sat above an envelope-authored second box whose
// label ("Preferred output shape") reads every bit as mandatory while being
// entirely optional. The quick-pick row conveyed selection by fill colour
// alone.
//
// Each test names the failure pattern it pins.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import {
  InterviewQuestion,
  type InterviewQuestionEnvelope,
  type InterviewQuestionResponse,
} from "./InterviewQuestion";

function envelope(): InterviewQuestionEnvelope {
  return {
    v: 1,
    id: "interview-1",
    type: "tangent.interview-question",
    title: "Interview",
    data: {
      prompt: "What should this workflow optimize for next?",
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

function renderInterview(onSubmit = vi.fn<(response: InterviewQuestionResponse) => void>()) {
  render(<InterviewQuestion envelope={envelope()} onSubmit={onSubmit} onCancel={vi.fn()} />);
  return onSubmit;
}

describe("InterviewQuestion validation affordances", () => {
  it("explains the disabled Submit beside the button and marks the answer required", () => {
    renderInterview();

    expect(screen.getByTestId("interview-question-submit")).toBeDisabled();
    const notice = screen.getByTestId("interview-question-submit-gate");
    expect(notice).toHaveAttribute("role", "status");
    expect(notice).toHaveAttribute("aria-live", "polite");
    expect(screen.getByTestId("interview-question-submit-gate-reason")).toHaveTextContent(
      "Submit is disabled: the answer is still empty.",
    );
    expect(screen.getByTestId("interview-question-submit")).toHaveAttribute(
      "aria-describedby",
      "interview-question-submit-gate",
    );

    expect(screen.getByTestId("interview-answer-required")).toBeInTheDocument();
    const answer = screen.getByTestId("interview-question-answer");
    expect(answer).toHaveAttribute("aria-required", "true");
    expect(answer).toHaveAttribute("aria-invalid", "true");
    expect(document.querySelector("label[for='interview-answer']")).toHaveTextContent("Answer");

    fireEvent.change(answer, { target: { value: "Optimize for reviewability." } });
    expect(screen.getByTestId("interview-question-submit")).not.toBeDisabled();
    expect(screen.queryByTestId("interview-question-submit-gate-reason")).not.toBeInTheDocument();
  });

  it("keeps the optional output-shape box from reading as a second requirement", () => {
    renderInterview();

    // The label itself is the agent's wording and is left alone; the badge and
    // the hint are what say the box is optional.
    expect(document.querySelector("label[for='interview-output-shape']")).toHaveTextContent(
      "Preferred output shape",
    );
    expect(screen.getByTestId("interview-output-shape-optional")).toHaveTextContent("optional");

    const control = screen.getByTestId("interview-question-output-shape-input");
    expect(control).not.toHaveAttribute("aria-required");
    // The help text used to render as a loose paragraph no control referenced.
    expect(control).toHaveAttribute("aria-describedby", "interview-output-shape-hint");
    expect(document.getElementById("interview-output-shape-hint")).toHaveTextContent(
      "If you want a particular format, say so explicitly. Optional — Submit never waits on it.",
    );
  });

  it("announces which quick pick is selected rather than showing it in colour only", () => {
    renderInterview();

    const group = screen.getByRole("group");
    expect(group).toHaveAttribute("aria-labelledby", "interview-question-choices-label");
    expect(document.getElementById("interview-question-choices-label")).toHaveTextContent(
      "Quick picks",
    );

    expect(screen.getByTestId("interview-question-choice-speed")).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    fireEvent.click(screen.getByTestId("interview-question-choice-speed"));
    expect(screen.getByTestId("interview-question-choice-speed")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByTestId("interview-question-choice-quality")).toHaveAttribute(
      "aria-pressed",
      "false",
    );
  });

  it("takes the interviewee to the answer box and states the problem there", () => {
    renderInterview();
    const scrollIntoView = vi.fn();
    Element.prototype.scrollIntoView = scrollIntoView;

    // A quick pick alone never satisfies the gate — that is the confusion the
    // hint calls out, so it is worth pinning.
    fireEvent.click(screen.getByTestId("interview-question-choice-quality"));
    expect(screen.getByTestId("interview-question-submit")).toBeDisabled();

    fireEvent.click(screen.getByTestId("interview-question-submit-gate-go"));

    const answer = screen.getByTestId("interview-question-answer");
    expect(document.activeElement).toBe(answer);
    expect(scrollIntoView).toHaveBeenCalledWith({ block: "center" });
    expect(screen.getByTestId("interview-answer-error")).toHaveAttribute("role", "alert");
    expect(answer).toHaveAttribute(
      "aria-describedby",
      "interview-answer-hint interview-answer-error",
    );
  });

  it("holds the answer error back until the box has been used", () => {
    renderInterview();

    expect(screen.queryByTestId("interview-answer-error")).not.toBeInTheDocument();
    expect(screen.getByTestId("interview-question-answer")).toHaveAttribute(
      "aria-describedby",
      "interview-answer-hint",
    );

    fireEvent.change(screen.getByTestId("interview-question-answer"), {
      target: { value: "Reviewability." },
    });
    fireEvent.change(screen.getByTestId("interview-question-answer"), { target: { value: "  " } });

    expect(screen.getByTestId("interview-answer-error")).toHaveTextContent(
      "Write the answer before submitting.",
    );
  });

  it("leaves the submitted payload shape untouched", () => {
    const onSubmit = renderInterview();

    fireEvent.click(screen.getByTestId("interview-question-choice-quality"));
    fireEvent.change(screen.getByTestId("interview-question-answer"), {
      target: { value: "  Bias toward quality.  " },
    });
    fireEvent.change(screen.getByTestId("interview-question-output-shape-input"), {
      target: { value: " Bullets first. " },
    });
    fireEvent.click(screen.getByTestId("interview-question-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const response = onSubmit.mock.calls[0][0];
    expect(response).toMatchObject({
      v: 1,
      envelopeId: "interview-1",
      kind: "data",
      status: "submitted",
    });
    expect(response.payload).toEqual({
      answer_text: "Bias toward quality.",
      selected_choice_id: "quality",
      thread_id: "goals",
      topic_label: "Workflow goals",
      output_shape_signal: "Bullets first.",
    });
    expect(typeof response.completedAt).toBe("string");
  });
});
