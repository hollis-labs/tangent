// Validation-affordance coverage for the schema-driven form.
//
// The case this file exists for: a checkbox near the top of the form reveals a
// whole section further down, every field in that section is required, and the
// only thing that changed on screen was a counter in the header and a Submit
// button that went dead. The operator gets no name for what appeared and no way
// to reach it. Each test below names the failure pattern it pins, so a
// regression reads as the pattern coming back rather than as a count changing.

import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  FORM_COLLECT_AUTOSAVE_DEBOUNCE_MS,
  getFormCollectDraftStorageKey,
} from "@/lib/form-collect-draft-storage";
import { FormCollect, type FormCollectEnvelope, type FormCollectResponse } from "./FormCollect";

const envelope: FormCollectEnvelope = {
  v: 1,
  id: "form-1",
  type: "tangent.form-collect",
  title: "Collect launch facts",
  data: {
    form_id: "launch",
    schema: {
      fields: [
        {
          id: "headline",
          type: "text",
          label: "Headline",
          required: true,
          help: "One line, present tense.",
        },
        {
          id: "needs_assets",
          type: "checkbox",
          label: "Need assets?",
          placeholder: "Yes, assets are needed",
        },
      ],
      sections: [
        {
          id: "assets",
          title: "Assets",
          repeatable: true,
          min_items: 1,
          // The spatial separation that made this form's gate unreadable: the
          // control that reveals the section sits at the top of the page, the
          // required fields it reveals sit at the bottom.
          show_when: { field_id: "needs_assets", truthy: true },
          fields: [
            { id: "name", type: "text", label: "Asset name", required: true },
            {
              id: "format",
              type: "radio",
              label: "Format",
              required: true,
              options: [
                { value: "png", label: "PNG" },
                { value: "svg", label: "SVG" },
              ],
            },
          ],
        },
      ],
    },
    actions: [{ id: "ship", label: "Ship" }],
  },
};

function renderForm(
  onSubmit = vi.fn<(response: FormCollectResponse) => void>(),
  override?: FormCollectEnvelope,
) {
  render(
    <FormCollect
      envelope={override ?? envelope}
      onSubmit={onSubmit}
      onCancel={vi.fn()}
      roomID="room-a"
    />,
  );
  return onSubmit;
}

describe("FormCollect validation affordances", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

  it("names the outstanding required field beside the disabled Submit", () => {
    renderForm();

    expect(screen.getByTestId("form-collect-submit")).toBeDisabled();
    const notice = screen.getByTestId("form-collect-submit-gate");
    expect(notice).toHaveAttribute("role", "status");
    expect(notice).toHaveAttribute("aria-live", "polite");
    expect(screen.getByTestId("form-collect-submit-gate-reason")).toHaveTextContent(
      'Submit form is disabled: "Headline" is required and still empty.',
    );
    expect(screen.getByTestId("form-collect-submit")).toHaveAttribute(
      "aria-describedby",
      "form-collect-submit-gate",
    );

    fireEvent.change(screen.getByTestId("form-collect-input-headline"), {
      target: { value: "Launch week" },
    });

    expect(screen.getByTestId("form-collect-submit")).not.toBeDisabled();
    expect(screen.queryByTestId("form-collect-submit-gate-reason")).not.toBeInTheDocument();
  });

  it("makes a revealed section's hidden requirements visible and named", () => {
    renderForm();
    fireEvent.change(screen.getByTestId("form-collect-input-headline"), {
      target: { value: "Launch week" },
    });

    // Nothing about assets exists yet, and Submit is live.
    expect(screen.queryByTestId("form-collect-section-assets")).not.toBeInTheDocument();
    expect(screen.getByTestId("form-collect-submit")).not.toBeDisabled();

    // One click, several screens up, and two required fields appear at the
    // bottom of the page. Before: the header count changed and Submit died.
    fireEvent.click(screen.getByLabelText("Need assets?"));

    const section = screen.getByTestId("form-collect-section-assets");
    expect(within(section).getAllByText("required")).toHaveLength(2);
    expect(screen.getByTestId("form-collect-input-name")).toHaveAttribute("aria-required", "true");
    expect(screen.getByTestId("form-collect-radio-format-png")).toHaveAttribute(
      "aria-required",
      "true",
    );

    expect(screen.getByTestId("form-collect-submit")).toBeDisabled();
    expect(screen.getByTestId("form-collect-submit-gate-reason")).toHaveTextContent(
      'Submit form is disabled: "Asset name (Assets #1)" is required and still empty.',
    );
    expect(screen.getByTestId("form-collect-submit-gate-more")).toHaveTextContent(
      "+1 more to resolve",
    );
  });

  it("takes the operator to the first outstanding control and scrolls it into view", () => {
    renderForm();
    const scrollIntoView = vi.fn();
    Element.prototype.scrollIntoView = scrollIntoView;

    fireEvent.change(screen.getByTestId("form-collect-input-headline"), {
      target: { value: "Launch week" },
    });
    fireEvent.click(screen.getByLabelText("Need assets?"));

    fireEvent.click(screen.getByTestId("form-collect-submit-gate-go"));
    expect(document.activeElement).toBe(screen.getByTestId("form-collect-input-name"));
    expect(scrollIntoView).toHaveBeenCalledWith({ block: "center" });

    // Clearing it hands the gate to the radio group, whose id lives on the
    // first option rather than on the field.
    fireEvent.change(screen.getByTestId("form-collect-input-name"), {
      target: { value: "Hero image" },
    });
    fireEvent.click(screen.getByTestId("form-collect-submit-gate-go"));
    expect(document.activeElement).toBe(screen.getByTestId("form-collect-radio-format-png"));
  });

  it("binds a required field's hint and error to the control for screen readers", () => {
    renderForm();

    const control = screen.getByTestId("form-collect-input-headline");
    expect(screen.getByTestId("form-collect-required-headline")).toBeInTheDocument();
    expect(control).toHaveAttribute("aria-required", "true");
    expect(control).toHaveAttribute("aria-invalid", "true");
    expect(control).toHaveAttribute("aria-describedby", "headline-hint headline-error");
    // The help text finally has an id, and the label targets the same id the
    // gate focuses, so "marked required" and "where the gate goes" cannot drift.
    expect(document.getElementById("headline-hint")).toHaveTextContent("One line, present tense.");
    expect(document.querySelector("label[for='headline']")).toHaveTextContent("Headline");
    expect(screen.getByTestId("form-collect-error-headline")).toHaveAttribute("role", "alert");

    fireEvent.change(control, { target: { value: "Launch week" } });
    expect(control).toHaveAttribute("aria-describedby", "headline-hint");
    expect(control).toHaveAttribute("aria-invalid", "false");
    expect(screen.queryByTestId("form-collect-error-headline")).not.toBeInTheDocument();
  });

  it("distinguishes the optional submission notes from a required field", () => {
    renderForm();

    // Both used to be a bordered section under identical `text-sm font-medium`
    // headings; only one of them was ever waiting on the operator.
    const notesLabel = document.querySelector("label[for='form-collect-notes']");
    expect(notesLabel).toHaveTextContent("Notes");
    expect(within(notesLabel as HTMLElement).queryByText("required")).not.toBeInTheDocument();
    expect(document.getElementById("form-collect-notes-hint")).toHaveTextContent(
      "Optional. A freeform note kept alongside the submission",
    );
    expect(screen.getByTestId("form-collect-notes")).not.toHaveAttribute("aria-required");
    expect(document.querySelector("label[for='headline']")).toHaveTextContent("required");

    // The submit action select had no label and no id at all.
    expect(document.querySelector("label[for='form-collect-action']")).toHaveTextContent(
      "Submit action",
    );
    expect(document.getElementById("form-collect-action-hint")).toHaveTextContent("Optional.");
  });

  it("gives a lone checkbox the field's own label as its accessible name", () => {
    renderForm();

    // Was "Checked": the placeholder stood in as the label, so the thing being
    // agreed to was only ever in a legend the control never referenced.
    const checkbox = screen.getByLabelText("Need assets?");
    expect(checkbox).toBe(screen.getByTestId("form-collect-input-needs_assets"));
    expect(screen.getByText("Yes, assets are needed")).toBeInTheDocument();
    expect(screen.queryByLabelText("Yes, assets are needed")).not.toBeInTheDocument();
  });

  it("explains a live Submit that cannot fire because the envelope has no form id", () => {
    // `handleSubmit` has always refused without a form id, but `disabled` never
    // said so: the operator got a live button that did nothing.
    const onSubmit = renderForm(vi.fn<(response: FormCollectResponse) => void>(), {
      ...envelope,
      id: "form-2",
      data: { form_id: "", schema: { fields: [{ id: "note", type: "text", label: "Note" }] } },
    });

    const submit = screen.getByTestId("form-collect-submit");
    expect(submit).not.toBeDisabled();
    expect(screen.getByTestId("form-collect-submit-gate-reason")).toHaveTextContent(
      "Cannot submit form yet: this envelope carries no form id, so a submission cannot be recorded against it.",
    );
    // Nothing to go to — the operator cannot fix an envelope.
    expect(screen.queryByTestId("form-collect-submit-gate-go")).not.toBeInTheDocument();

    fireEvent.click(submit);
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("reports an empty saved-draft name instead of doing nothing", () => {
    renderForm();

    const control = screen.getByTestId("form-collect-draft-label");
    expect(document.querySelector("label[for='form-collect-draft-label']")).toHaveTextContent(
      "Draft name",
    );
    expect(screen.getByTestId("form-collect-draft-label-required")).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("form-collect-draft-label-save"));

    expect(screen.getByTestId("form-collect-draft-label-error")).toHaveTextContent(
      "Enter a draft name before saving.",
    );
    expect(control).toHaveAttribute("aria-invalid", "true");
    expect(document.activeElement).toBe(control);

    fireEvent.change(control, { target: { value: "Half-written" } });
    expect(screen.queryByTestId("form-collect-draft-label-error")).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId("form-collect-draft-label-save"));
    expect(screen.getByText("Half-written")).toBeInTheDocument();
  });

  it("reports an empty attachment display name instead of doing nothing", () => {
    renderForm();

    expect(document.querySelector("label[for='form-collect-attachment-name']")).toHaveTextContent(
      "Display name",
    );
    fireEvent.click(screen.getByTestId("form-collect-add-attachment"));

    expect(screen.getByTestId("form-collect-attachment-name-error")).toHaveTextContent(
      "Enter a display name before adding the attachment ref.",
    );
    expect(document.activeElement).toBe(screen.getByTestId("form-collect-attachment-name"));
    // The three optional siblings were placeholder-only boxes too.
    expect(document.querySelector("label[for='form-collect-attachment-uri']")).toHaveTextContent(
      "URI",
    );
  });

  it("keeps a repeatable row's label and control paired across re-renders", () => {
    renderForm();
    fireEvent.click(screen.getByLabelText("Need assets?"));

    const before = screen.getByTestId("form-collect-input-name").id;
    // Any unrelated edit re-renders the row; the id used to be re-minted from
    // Math.random(), which broke the label association it was paired with.
    fireEvent.change(screen.getByTestId("form-collect-input-headline"), {
      target: { value: "Launch week" },
    });
    const after = screen.getByTestId("form-collect-input-name").id;

    expect(after).toBe(before);
    expect(document.querySelector(`label[for='${after}']`)).toHaveTextContent("Asset name");
  });

  it("announces recovered draft state politely", () => {
    vi.useFakeTimers();
    const { unmount } = render(
      <FormCollect envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} roomID="room-a" />,
    );
    fireEvent.change(screen.getByTestId("form-collect-input-headline"), {
      target: { value: "Recovered headline" },
    });
    vi.advanceTimersByTime(FORM_COLLECT_AUTOSAVE_DEBOUNCE_MS + 50);
    expect(
      window.localStorage.getItem(getFormCollectDraftStorageKey("room-a", "launch")),
    ).toBeTruthy();

    unmount();
    render(
      <FormCollect envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} roomID="room-a" />,
    );

    const message = screen.getByTestId("form-collect-message");
    expect(message).toHaveAttribute("role", "status");
    expect(message).toHaveAttribute("aria-live", "polite");
    expect(screen.getByTestId("form-collect-input-headline")).toHaveValue("Recovered headline");
  });

  it("leaves the submitted payload shape untouched", () => {
    const onSubmit = renderForm();

    fireEvent.change(screen.getByTestId("form-collect-input-headline"), {
      target: { value: "Launch week" },
    });
    fireEvent.change(screen.getByTestId("form-collect-action"), { target: { value: "ship" } });
    fireEvent.change(screen.getByTestId("form-collect-attachment-name"), {
      target: { value: "Spec PDF" },
    });
    fireEvent.change(screen.getByTestId("form-collect-attachment-uri"), {
      target: { value: "artifact://spec" },
    });
    fireEvent.click(screen.getByTestId("form-collect-add-attachment"));
    fireEvent.click(screen.getByTestId("form-collect-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const response = onSubmit.mock.calls[0][0];
    expect(response.v).toBe(1);
    expect(response.envelopeId).toBe("form-1");
    expect(response.kind).toBe("data");
    expect(response.status).toBe("submitted");
    expect(response.payload).toEqual({
      form_id: "launch",
      answers: {
        headline: "Launch week",
        needs_assets: false,
        assets: [{ name: "", format: "" }],
      },
      notes: undefined,
      action_id: "ship",
      saved_drafts: [],
      templates: [],
      attachment_refs: [{ id: "spec-pdf-1", name: "Spec PDF", uri: "artifact://spec" }],
      submission_summary: undefined,
    });
  });
});
