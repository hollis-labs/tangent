import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { expectProseRendered, proseProbe } from "@/components/markdown/prose-probe";
import {
  FORM_COLLECT_AUTOSAVE_DEBOUNCE_MS,
  getFormCollectDraftStorageKey,
} from "@/lib/form-collect-draft-storage";
import { FormCollect, type FormCollectEnvelope } from "./FormCollect";

const envelope: FormCollectEnvelope = {
  v: 1,
  id: "form-1",
  type: "tangent.form-collect",
  title: "Collect launch facts",
  data: {
    form_id: "launch",
    intent: "Collect the launch submission.",
    schema: {
      fields: [
        { id: "headline", type: "text", label: "Headline", required: true },
        {
          id: "needs_assets",
          type: "checkbox",
          label: "Need assets?",
        },
      ],
      sections: [
        {
          id: "assets",
          title: "Assets",
          repeatable: true,
          min_items: 1,
          show_when: { field_id: "needs_assets", truthy: true },
          fields: [{ id: "name", type: "text", label: "Asset name", required: true }],
        },
      ],
    },
    actions: [{ id: "ship", label: "Ship" }],
  },
};

describe("FormCollect", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

  it("submits answers, action, and attachment refs", () => {
    const onSubmit = vi.fn();
    render(
      <FormCollect envelope={envelope} onSubmit={onSubmit} onCancel={vi.fn()} roomID="room-1" />,
    );

    fireEvent.change(screen.getByTestId("form-collect-input-headline"), {
      target: { value: "Launch week" },
    });
    fireEvent.change(screen.getByTestId("form-collect-action"), {
      target: { value: "ship" },
    });
    fireEvent.change(screen.getByTestId("form-collect-attachment-name"), {
      target: { value: "Spec PDF" },
    });
    fireEvent.change(screen.getByTestId("form-collect-attachment-uri"), {
      target: { value: "artifact://spec" },
    });
    fireEvent.click(screen.getByTestId("form-collect-add-attachment"));
    fireEvent.click(screen.getByTestId("form-collect-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0][0]).toMatchObject({
      payload: {
        form_id: "launch",
        answers: {
          headline: "Launch week",
          needs_assets: false,
        },
        action_id: "ship",
        attachment_refs: [{ name: "Spec PDF", uri: "artifact://spec" }],
      },
    });
  });

  it("supports conditional repeatable sections", () => {
    render(
      <FormCollect envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} roomID="room-1" />,
    );

    expect(screen.queryByTestId("form-collect-section-assets")).not.toBeInTheDocument();
    // The checkbox's accessible name is the field's own label now; it used to
    // be the literal string "Checked" (see FormCollect's checkbox branch).
    fireEvent.click(screen.getByLabelText("Need assets?"));
    expect(screen.getByTestId("form-collect-section-assets")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("form-collect-add-row-assets"));
    expect(screen.getAllByText(/Assets #/)).toHaveLength(2);
  });

  it("recovers unsent draft state across refresh", () => {
    vi.useFakeTimers();
    const { unmount } = render(
      <FormCollect envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} roomID="room-1" />,
    );
    fireEvent.change(screen.getByTestId("form-collect-input-headline"), {
      target: { value: "Recovered headline" },
    });
    fireEvent.change(screen.getByTestId("form-collect-notes"), {
      target: { value: "Recovered notes" },
    });
    vi.advanceTimersByTime(FORM_COLLECT_AUTOSAVE_DEBOUNCE_MS + 50);
    expect(
      window.localStorage.getItem(getFormCollectDraftStorageKey("room-1", "launch")),
    ).toBeTruthy();

    unmount();
    render(
      <FormCollect envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} roomID="room-1" />,
    );

    expect(screen.getByTestId("form-collect-message")).toHaveTextContent("Recovered");
    expect(screen.getByTestId("form-collect-input-headline")).toHaveValue("Recovered headline");
    expect(screen.getByTestId("form-collect-notes")).toHaveValue("Recovered notes");
  });

  it("routes every prose surface through the shared markdown renderer", () => {
    const envelope: FormCollectEnvelope = {
      v: 1,
      id: "form-md",
      type: "tangent.form-collect",
      context: proseProbe("form-context"),
      data: {
        form_id: "form-md",
        intent: proseProbe("form-intent"),
        schema: {
          sections: [
            {
              id: "plain",
              title: "Plain section",
              description: proseProbe("form-section"),
              fields: [{ id: "one", label: "One", type: "text" }],
            },
            {
              id: "repeat",
              title: "Repeatable section",
              description: proseProbe("form-repeatable"),
              repeatable: true,
              min_items: 1,
              fields: [{ id: "two", label: "Two", type: "text" }],
            },
          ],
        },
      },
    };

    render(<FormCollect envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expectProseRendered("form-context");
    expectProseRendered("form-intent");
    expectProseRendered("form-section");
    expectProseRendered("form-repeatable");
  });

  describe("secret fields", () => {
    const SECRET = "SECRETCANARY-5d21b0aa";
    const secretEnvelope: FormCollectEnvelope = {
      v: 1,
      id: "form-secret",
      type: "tangent.form-collect",
      data: {
        form_id: "secret-probe",
        schema: {
          fields: [
            { id: "name", type: "text", label: "Name" },
            { id: "api_key", type: "text", label: "API key", secret: true },
          ],
        },
      },
    };

    it("treats a password-typed field as a secret without the flag", () => {
      vi.useFakeTimers();
      const passwordEnvelope: FormCollectEnvelope = {
        v: 1,
        id: "form-pw",
        type: "tangent.form-collect",
        data: {
          form_id: "pw-probe",
          schema: { fields: [{ id: "pw", type: "password", label: "Password" }] },
        },
      };
      render(
        <FormCollect
          envelope={passwordEnvelope}
          onSubmit={vi.fn()}
          onCancel={vi.fn()}
          roomID="r"
        />,
      );
      expect(screen.getByTestId("form-collect-input-pw")).toHaveAttribute("type", "password");
      fireEvent.change(screen.getByTestId("form-collect-input-pw"), { target: { value: SECRET } });
      vi.advanceTimersByTime(FORM_COLLECT_AUTOSAVE_DEBOUNCE_MS + 50);
      const stored = window.localStorage.getItem(getFormCollectDraftStorageKey("r", "pw-probe"));
      expect(stored).toBeTruthy();
      expect(stored).not.toContain(SECRET);
    });

    it("renders a secret field masked", () => {
      render(
        <FormCollect envelope={secretEnvelope} onSubmit={vi.fn()} onCancel={vi.fn()} roomID="r" />,
      );
      expect(screen.getByTestId("form-collect-input-api_key")).toHaveAttribute("type", "password");
      expect(screen.getByTestId("form-collect-input-name")).not.toHaveAttribute("type", "password");
    });

    it("never autosaves the secret, in saved drafts, or in templates, but still submits it", () => {
      vi.useFakeTimers();
      const onSubmit = vi.fn();
      const { unmount } = render(
        <FormCollect
          envelope={secretEnvelope}
          onSubmit={onSubmit}
          onCancel={vi.fn()}
          roomID="room-s"
        />,
      );
      fireEvent.change(screen.getByTestId("form-collect-input-name"), {
        target: { value: "visible" },
      });
      fireEvent.change(screen.getByTestId("form-collect-input-api_key"), {
        target: { value: SECRET },
      });
      fireEvent.change(screen.getByTestId("form-collect-draft-label"), { target: { value: "d1" } });
      fireEvent.click(screen.getByTestId("form-collect-draft-label-save"));
      vi.advanceTimersByTime(FORM_COLLECT_AUTOSAVE_DEBOUNCE_MS + 50);

      const stored = window.localStorage.getItem(
        getFormCollectDraftStorageKey("room-s", "secret-probe"),
      );
      expect(stored).toBeTruthy();
      expect(stored).toContain("visible");
      expect(stored).not.toContain(SECRET);

      unmount();
      render(
        <FormCollect
          envelope={secretEnvelope}
          onSubmit={onSubmit}
          onCancel={vi.fn()}
          roomID="room-s"
        />,
      );
      expect(screen.getByTestId("form-collect-input-name")).toHaveValue("visible");
      expect(screen.getByTestId("form-collect-input-api_key")).toHaveValue("");

      fireEvent.change(screen.getByTestId("form-collect-input-api_key"), {
        target: { value: SECRET },
      });
      fireEvent.click(screen.getByTestId("form-collect-submit"));
      const response = onSubmit.mock.calls[0][0];
      expect(response.payload.answers.api_key).toBe(SECRET);
      expect(JSON.stringify(response.payload.saved_drafts)).not.toContain(SECRET);
    });
  });
});
