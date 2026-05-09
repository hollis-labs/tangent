import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { Feedback, type FeedbackEnvelope } from "./Feedback";

function sampleEnvelope(): FeedbackEnvelope {
  return {
    v: 1,
    id: "feedback-1",
    type: "tangent.feedback",
    title: "Collect feedback",
    data: {
      prompt: "Answer the questions below.",
      questions: [
        {
          id: "q-text",
          type: "text",
          label: "Headline",
          required: true,
          placeholder: "Write a headline",
        },
        {
          id: "q-long",
          type: "textarea",
          label: "Details",
        },
        {
          id: "q-one",
          type: "radio",
          label: "Direction",
          required: true,
          options: [
            { value: "yes", label: "Yes" },
            { value: "no", label: "No" },
          ],
        },
        {
          id: "q-many",
          type: "multiselect",
          label: "Tags",
          options: [
            { value: "ux", label: "UX" },
            { value: "bug", label: "Bug" },
          ],
        },
      ],
    },
  };
}

describe("<Feedback>", () => {
  it("renders each supported field kind", () => {
    render(<Feedback envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("feedback-input-q-text")).toBeInTheDocument();
    expect(screen.getByTestId("feedback-textarea-q-long")).toBeInTheDocument();
    expect(screen.getByTestId("feedback-radio-q-one")).toBeInTheDocument();
    expect(screen.getByTestId("feedback-checkbox-q-many")).toBeInTheDocument();
  });

  it("Submit stays disabled until required fields are filled", () => {
    render(<Feedback envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    const submit = screen.getByTestId("feedback-submit");
    expect(submit).toBeDisabled();

    fireEvent.change(screen.getByTestId("feedback-input-q-text"), { target: { value: "Ship it" } });
    expect(submit).toBeDisabled();

    fireEvent.click(screen.getByTestId("feedback-option-q-one-yes"));
    expect(submit).not.toBeDisabled();
  });

  it("Submit emits the shaped response payload", () => {
    const onSubmit = vi.fn();
    render(<Feedback envelope={sampleEnvelope()} onSubmit={onSubmit} onCancel={vi.fn()} />);

    fireEvent.change(screen.getByTestId("feedback-input-q-text"), { target: { value: "Ship it" } });
    fireEvent.change(screen.getByTestId("feedback-textarea-q-long"), {
      target: { value: "Details here" },
    });
    fireEvent.click(screen.getByTestId("feedback-option-q-one-yes"));
    fireEvent.click(screen.getByTestId("feedback-option-q-many-ux"));
    fireEvent.click(screen.getByTestId("feedback-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const payload = onSubmit.mock.calls[0][0];
    expect(payload).toMatchObject({
      v: 1,
      envelopeId: "feedback-1",
      kind: "data",
      status: "submitted",
      payload: {
        answers: [
          { questionId: "q-text", value: "Ship it" },
          { questionId: "q-long", value: "Details here" },
          { questionId: "q-one", value: "yes" },
          { questionId: "q-many", value: ["ux"] },
        ],
      },
    });
    expect(typeof payload.completedAt).toBe("string");
  });

  it("Cancel calls onCancel", () => {
    const onCancel = vi.fn();
    render(<Feedback envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={onCancel} />);

    fireEvent.click(screen.getByTestId("feedback-cancel"));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});
