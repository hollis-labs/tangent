// Validation-affordance coverage for the feedback form.
//
// Feedback carried the family's one outright accessibility bug: every
// question rendered `<label htmlFor={question.id}>`, but radio, select,
// checkbox and multiselect questions never rendered an element with that id.
// The label pointed at nothing, so a screen reader user reached a bare set of
// options with no idea what was being asked — and the only thing marking the
// question required was a red asterisk with no legend and no `aria-required`.
//
// Each test names the failure pattern it pins.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { Feedback, type FeedbackEnvelope, type FeedbackResponse } from "./Feedback";

function envelope(): FeedbackEnvelope {
  return {
    v: 1,
    id: "feedback-1",
    type: "tangent.feedback",
    title: "Collect feedback",
    data: {
      prompt: "Answer the questions below.",
      questions: [
        {
          id: "q-headline",
          type: "text",
          label: "Headline",
          required: true,
          placeholder: "Write a headline",
          allowNote: true,
        },
        { id: "q-details", type: "textarea", label: "Details" },
        {
          id: "q-direction",
          type: "radio",
          label: "Direction",
          required: true,
          options: [
            { value: "yes", label: "Yes" },
            { value: "no", label: "No" },
          ],
          suggestion: { value: "yes", rationale: "The agent leans yes." },
        },
        {
          id: "q-tags",
          type: "multiselect",
          label: "Tags",
          required: true,
          help: "Pick at least one.",
          options: [
            { value: "ux", label: "UX" },
            { value: "bug", label: "Bug" },
          ],
        },
      ],
    },
  };
}

function renderFeedback(onSubmit = vi.fn<(response: FeedbackResponse) => void>()) {
  render(<Feedback envelope={envelope()} onSubmit={onSubmit} onCancel={vi.fn()} />);
  return onSubmit;
}

function answerEverything() {
  fireEvent.change(screen.getByTestId("feedback-input-q-headline"), {
    target: { value: "Ship it" },
  });
  fireEvent.click(screen.getByTestId("feedback-option-q-direction-yes"));
  fireEvent.click(screen.getByTestId("feedback-option-q-tags-ux"));
}

describe("Feedback validation affordances", () => {
  it("never renders a label pointing at an id that does not exist", () => {
    // The regression test for the dangling `htmlFor`. It sweeps the whole
    // card rather than naming the four broken types, so a new question type
    // that reintroduces the bug fails here too.
    renderFeedback();

    const labels = Array.from(document.querySelectorAll("label[for]"));
    expect(labels.length).toBeGreaterThan(0);
    for (const label of labels) {
      const target = label.getAttribute("for") ?? "";
      expect(document.getElementById(target), `label[for="${target}"] points at nothing`).not.toBe(
        null,
      );
    }
  });

  it("labels the option groups by the question text instead of a dangling htmlFor", () => {
    renderFeedback();

    expect(document.querySelector("label[for='q-direction']")).toBeNull();
    expect(document.querySelector("label[for='q-tags']")).toBeNull();

    const radioGroup = screen.getByTestId("feedback-radio-q-direction");
    expect(radioGroup).toHaveAttribute("role", "radiogroup");
    expect(radioGroup).toHaveAttribute("aria-labelledby", "q-direction-label");
    expect(document.getElementById("q-direction-label")).toHaveTextContent("Direction");

    // A checkbox set is a native <fieldset>, whose implicit role is `group`.
    // `aria-required` is not supported there, so the requirement rides on the
    // marker in the label and on the hint the group is described by.
    const checkboxGroup = screen.getByTestId("feedback-checkbox-q-tags");
    expect(checkboxGroup.tagName).toBe("FIELDSET");
    expect(checkboxGroup).toHaveAttribute("aria-labelledby", "q-tags-label");
    expect(checkboxGroup).toHaveAttribute("aria-describedby", "q-tags-hint");
    expect(screen.getByTestId("feedback-required-q-tags")).toBeInTheDocument();
  });

  it("marks required questions visibly and to assistive technology", () => {
    renderFeedback();

    expect(screen.getByTestId("feedback-required-q-headline")).toBeInTheDocument();
    expect(screen.queryByTestId("feedback-required-q-details")).not.toBeInTheDocument();

    expect(screen.getByTestId("feedback-input-q-headline")).toHaveAttribute(
      "aria-required",
      "true",
    );
    expect(screen.getByTestId("feedback-textarea-q-details")).toHaveAttribute(
      "aria-required",
      "false",
    );

    // The legend the asterisk never had, stated per question and bound to the
    // control so it is announced rather than merely seen.
    expect(screen.getByTestId("feedback-hint-q-headline")).toHaveTextContent("Required.");
    expect(screen.getByTestId("feedback-hint-q-details")).toHaveTextContent("Optional.");
    expect(screen.getByTestId("feedback-hint-q-tags")).toHaveTextContent(
      "Pick at least one. Required.",
    );
  });

  it("explains the disabled Submit, names the first unanswered question, and counts the rest", () => {
    renderFeedback();

    expect(screen.getByTestId("feedback-submit")).toBeDisabled();
    const notice = screen.getByTestId("feedback-submit-gate");
    expect(notice).toHaveAttribute("role", "status");
    expect(notice).toHaveAttribute("aria-live", "polite");
    expect(screen.getByTestId("feedback-submit-gate-reason")).toHaveTextContent(
      'Submit is disabled: "Headline" is required and still needs an answer.',
    );
    expect(screen.getByTestId("feedback-submit-gate-more")).toHaveTextContent("+2 more to resolve");
    expect(screen.getByTestId("feedback-submit")).toHaveAttribute(
      "aria-describedby",
      "feedback-submit-gate",
    );

    answerEverything();
    expect(screen.getByTestId("feedback-submit")).not.toBeDisabled();
    expect(screen.queryByTestId("feedback-submit-gate-reason")).not.toBeInTheDocument();
  });

  it("takes the operator to the first unanswered question, including into an option group", () => {
    renderFeedback();
    const scrollIntoView = vi.fn();
    Element.prototype.scrollIntoView = scrollIntoView;

    fireEvent.change(screen.getByTestId("feedback-input-q-headline"), {
      target: { value: "Ship it" },
    });
    fireEvent.click(screen.getByTestId("feedback-submit-gate-go"));

    // A group has no single control, so the gate goes to its first option —
    // which is also the target of that option's own label.
    expect(document.activeElement).toBe(screen.getByTestId("feedback-option-q-direction-yes"));
    expect(scrollIntoView).toHaveBeenCalledWith({ block: "center" });
    // Being sent to a question is also what makes its error line fair.
    expect(screen.getByTestId("feedback-error-q-direction")).toHaveAttribute("role", "alert");
  });

  it("holds the error line back until the question has been engaged with", () => {
    renderFeedback();

    // Envelope-declared requiredness is not a mistake the operator has made
    // yet, so nothing is red on mount.
    expect(screen.queryByTestId("feedback-error-q-headline")).not.toBeInTheDocument();
    expect(screen.getByTestId("feedback-input-q-headline")).toHaveAttribute(
      "aria-describedby",
      "q-headline-hint",
    );

    fireEvent.change(screen.getByTestId("feedback-input-q-headline"), {
      target: { value: "Ship it" },
    });
    fireEvent.change(screen.getByTestId("feedback-input-q-headline"), { target: { value: "" } });

    expect(screen.getByTestId("feedback-error-q-headline")).toHaveTextContent(
      "This question is required. Answer it before submitting.",
    );
    expect(screen.getByTestId("feedback-input-q-headline")).toHaveAttribute(
      "aria-describedby",
      "q-headline-hint q-headline-error",
    );
    expect(screen.getByTestId("feedback-input-q-headline")).toHaveAttribute("aria-invalid", "true");
  });

  it("distinguishes the per-question note from the question above it", () => {
    renderFeedback();

    // "Note" sat directly under a control that can itself be a required
    // textarea, in the same weight. It now says what it is, and says it twice:
    // once in the label and once in the description the control points at.
    expect(document.querySelector("label[for='q-headline-note']")).toHaveTextContent(
      "Optional note",
    );
    expect(screen.getByTestId("feedback-note-q-headline")).toHaveAttribute(
      "aria-describedby",
      "q-headline-note-hint",
    );
    expect(document.getElementById("q-headline-note-hint")).toHaveTextContent(
      "Optional. Kept alongside the answer above and never required to submit.",
    );
    expect(screen.getByTestId("feedback-note-q-headline")).not.toHaveAttribute("aria-required");
  });

  it("explains an envelope with no answerable questions and offers nowhere to go", () => {
    render(
      <Feedback
        envelope={{ v: 1, id: "feedback-empty", type: "tangent.feedback", data: { questions: [] } }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getByTestId("feedback-submit")).toBeDisabled();
    expect(screen.getByTestId("feedback-submit-gate-reason")).toHaveTextContent(
      "Submit is disabled: this feedback envelope has no answerable questions.",
    );
    expect(screen.queryByTestId("feedback-submit-gate-go")).not.toBeInTheDocument();
  });

  it("leaves the submitted payload shape untouched", () => {
    const onSubmit = renderFeedback();

    answerEverything();
    fireEvent.change(screen.getByTestId("feedback-textarea-q-details"), {
      target: { value: "Details here" },
    });
    fireEvent.change(screen.getByTestId("feedback-note-q-headline"), {
      target: { value: "Worth a second look." },
    });
    fireEvent.click(screen.getByTestId("feedback-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const response = onSubmit.mock.calls[0][0];
    expect(response).toMatchObject({
      v: 1,
      envelopeId: "feedback-1",
      kind: "data",
      status: "submitted",
    });
    expect(response.payload.answers).toEqual([
      {
        questionId: "q-headline",
        value: "Ship it",
        acceptedSuggestion: undefined,
        note: "Worth a second look.",
      },
      {
        questionId: "q-details",
        value: "Details here",
        acceptedSuggestion: undefined,
        note: undefined,
      },
      {
        questionId: "q-direction",
        value: "yes",
        acceptedSuggestion: true,
        note: undefined,
      },
      { questionId: "q-tags", value: ["ux"], acceptedSuggestion: undefined, note: undefined },
    ]);
    expect(typeof response.completedAt).toBe("string");
  });
});
