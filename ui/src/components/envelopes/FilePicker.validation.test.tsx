// Validation-affordance coverage for the file picker.
//
// This is the canonical "attempted submit focuses the first invalid control"
// case for CW-20260904-0067: Submit stays live and validates on click, and the
// refusal used to render inside the file-browser card, above the file list,
// while the button sits below the whole preview card. An operator scrolled to
// Submit clicked it, nothing moved, and no message was anywhere near them.
//
// Each test names the pattern it pins so a regression reads as the pattern
// coming back rather than as an assertion count changing.

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { getFilePickerDraftStorageKey } from "@/lib/file-picker-draft-storage";
import { FilePicker, type FilePickerEnvelope, type FilePickerResponse } from "./FilePicker";

const envelope: FilePickerEnvelope = {
  v: 1,
  id: "picker-env-1",
  type: "tangent.file-picker",
  title: "Pick files",
  data: {
    picker_id: "picker-1",
    browse_roots: [
      { root_id: "workspace", label: "Workspace", path: "/tmp/workspace" },
      { root_id: "docs", label: "Docs", path: "/tmp/docs" },
    ],
    files: [
      {
        artifact_id: "artifact-1",
        name: "README.md",
        uri: "artifact://artifact-1",
        root_id: "workspace",
        relative_path: "README.md",
      },
      {
        artifact_id: "artifact-2",
        name: "spec.md",
        uri: "artifact://artifact-2",
        root_id: "workspace",
        relative_path: "docs/spec.md",
      },
    ],
  },
};

const emptyRootEnvelope: FilePickerEnvelope = {
  v: 1,
  id: "picker-env-empty",
  type: "tangent.file-picker",
  data: {
    picker_id: "picker-empty",
    browse_roots: [{ root_id: "workspace", label: "Workspace", path: "/tmp/workspace" }],
  },
};

let scrollIntoView: ReturnType<typeof vi.fn>;
let originalScrollIntoView: typeof Element.prototype.scrollIntoView;

function renderPicker(
  onSubmit = vi.fn<(response: FilePickerResponse) => void>(),
  envelopeOverride: FilePickerEnvelope = envelope,
) {
  render(
    <FilePicker
      envelope={envelopeOverride}
      onSubmit={onSubmit}
      onCancel={vi.fn()}
      roomID="room-a"
    />,
  );
  return onSubmit;
}

describe("FilePicker validation affordances", () => {
  beforeEach(() => {
    originalScrollIntoView = Element.prototype.scrollIntoView;
    scrollIntoView = vi.fn();
    Element.prototype.scrollIntoView = scrollIntoView;
  });

  afterEach(() => {
    Element.prototype.scrollIntoView = originalScrollIntoView;
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

  it("focuses and scrolls to the first file checkbox when Submit is attempted empty", () => {
    const onSubmit = renderPicker();

    const submit = screen.getByTestId("file-picker-submit");
    // The button stays live — this picker's own interaction is what reports
    // "nothing selected", and that shape is preserved.
    expect(submit).not.toBeDisabled();
    expect(submit).toHaveAttribute("aria-describedby", "file-picker-submit-gate");

    fireEvent.click(submit);

    expect(onSubmit).not.toHaveBeenCalled();
    const control = screen.getByTestId("file-picker-file-checkbox-workspace:README.md");
    expect(document.activeElement).toBe(control);
    expect(scrollIntoView).toHaveBeenCalledTimes(1);
    expect(scrollIntoView).toHaveBeenCalledWith({ block: "center" });
  });

  it("puts the refusal in the Submit row, announced politely", () => {
    renderPicker();

    const notice = screen.getByTestId("file-picker-submit-gate");
    expect(notice).toHaveAttribute("role", "status");
    expect(notice).toHaveAttribute("aria-live", "polite");
    expect(screen.getByTestId("file-picker-submit-gate-reason")).toHaveTextContent(
      "Cannot submit yet: no files are selected — tick at least one file in the list.",
    );
    // It is a sibling of the button, not a paragraph three cards above it.
    const submitRow = screen.getByTestId("file-picker-submit").parentElement;
    expect(submitRow).toContainElement(notice);
    // And it is the blocked surface, not the server-refusal surface.
    expect(notice).not.toHaveAttribute("role", "alert");
    expect(screen.queryByText(/^Not submitted:/)).not.toBeInTheDocument();
  });

  it("clears the gate through the control it points at", () => {
    const onSubmit = renderPicker();

    fireEvent.click(screen.getByTestId("file-picker-file-checkbox-workspace:README.md"));

    expect(screen.queryByTestId("file-picker-submit-gate-reason")).not.toBeInTheDocument();
    expect(screen.getByTestId("file-picker-submit")).not.toHaveAttribute("aria-describedby");
    expect(screen.queryByTestId("file-picker-files-required")).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId("file-picker-submit"));
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it("follows the visible order, so the focused checkbox is the one at the top of the list", () => {
    renderPicker();

    fireEvent.change(screen.getByTestId("file-picker-sort"), { target: { value: "name:desc" } });
    fireEvent.click(screen.getByTestId("file-picker-submit-gate-go"));

    expect(document.activeElement).toBe(
      screen.getByTestId("file-picker-file-checkbox-workspace:docs/spec.md"),
    );

    fireEvent.change(screen.getByTestId("file-picker-sort"), { target: { value: "name:asc" } });
    fireEvent.click(screen.getByTestId("file-picker-submit-gate-go"));

    expect(document.activeElement).toBe(
      screen.getByTestId("file-picker-file-checkbox-workspace:README.md"),
    );
  });

  it("says so plainly when the root has nothing to tick, and offers nowhere to go", () => {
    const onSubmit = renderPicker(vi.fn(), emptyRootEnvelope);

    fireEvent.click(screen.getByTestId("file-picker-submit"));

    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByTestId("file-picker-submit-gate-reason")).toHaveTextContent(
      "Cannot submit yet: no files are selected, and this root has no file candidates to tick.",
    );
    // No control to send anyone to, so no dead "Go to" affordance is offered.
    expect(screen.queryByTestId("file-picker-submit-gate-go")).not.toBeInTheDocument();
    expect(scrollIntoView).not.toHaveBeenCalled();
  });

  it("expresses 'at least one' as a group requirement rather than only in prose", () => {
    renderPicker();

    const group = screen.getByRole("group", { name: /Files/ });
    expect(group).toBe(screen.getByTestId("file-picker-files"));
    expect(group).toHaveAttribute("aria-describedby", "file-picker-files-hint");
    expect(document.getElementById("file-picker-files-hint")).toHaveTextContent(
      "Select at least one file. Every ticked file is submitted as a durable artifact ref.",
    );
    expect(screen.getByTestId("file-picker-files-required")).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("file-picker-file-checkbox-workspace:README.md"));
    expect(screen.queryByTestId("file-picker-files-required")).not.toBeInTheDocument();
  });

  it("gives every file checkbox an id, a name and an accessible name", () => {
    renderPicker();

    const readme = screen.getByTestId("file-picker-file-checkbox-workspace:README.md");
    expect(readme).toHaveAttribute("id", "file-picker-file-checkbox-workspace:README.md");
    expect(readme).toHaveAttribute("name", "file-picker-selection");
    expect(
      document.querySelector("label[for='file-picker-file-checkbox-workspace:README.md']"),
    ).toBe(screen.getByTestId("file-picker-file-workspace:README.md"));
    expect(screen.getByRole("checkbox", { name: "README.md (README.md)" })).toBe(readme);
    expect(screen.getByRole("checkbox", { name: "spec.md (docs/spec.md)" })).toBe(
      screen.getByTestId("file-picker-file-checkbox-workspace:docs/spec.md"),
    );
  });

  it("labels the browse controls and marks the current root and directory", () => {
    renderPicker();

    expect(document.querySelector("label[for='file-picker-search']")).toHaveTextContent(
      "Search files",
    );
    expect(screen.getByTestId("file-picker-search")).toHaveAttribute("id", "file-picker-search");
    expect(document.querySelector("label[for='file-picker-sort']")).toHaveTextContent("Sort files");
    expect(screen.getByTestId("file-picker-sort")).toHaveAttribute("id", "file-picker-sort");

    expect(screen.getByRole("group", { name: "Roots" })).toBe(
      screen.getByTestId("file-picker-roots"),
    );
    expect(screen.getByTestId("file-picker-root-workspace")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByTestId("file-picker-root-docs")).toHaveAttribute("aria-pressed", "false");

    fireEvent.click(screen.getByTestId("file-picker-root-docs"));
    expect(screen.getByTestId("file-picker-root-docs")).toHaveAttribute("aria-pressed", "true");

    // The root crumb's whole accessible name used to be the literal "/".
    const rootCrumb = screen.getByRole("button", { name: "Root of this browse root" });
    expect(rootCrumb).toHaveAttribute("aria-current", "location");
  });

  it("keeps the recovered-draft notice where the state it describes lives", () => {
    window.localStorage.setItem(
      getFilePickerDraftStorageKey("room-a", "picker-1"),
      JSON.stringify({
        activeRootID: "workspace",
        currentDir: "",
        search: "",
        sort: "name:asc",
        selectedKeys: ["workspace:README.md"],
        previewKey: "workspace:README.md",
      }),
    );

    renderPicker();

    const message = screen.getByTestId("file-picker-message");
    expect(message).toHaveAttribute("role", "status");
    expect(message).toHaveAttribute("aria-live", "polite");
    expect(message).toHaveTextContent("Recovered unsent file-picker state from this browser.");
    // A recovered selection satisfies the gate, so nothing is flagged on load.
    expect(screen.queryByTestId("file-picker-submit-gate-reason")).not.toBeInTheDocument();
    expect(screen.getByTestId("file-picker-selected-count")).toHaveTextContent("1");
  });

  it("leaves the submitted payload shape untouched", () => {
    const onSubmit = renderPicker();

    fireEvent.change(screen.getByTestId("file-picker-search"), { target: { value: "read" } });
    fireEvent.change(screen.getByTestId("file-picker-sort"), { target: { value: "path:desc" } });
    fireEvent.click(screen.getByTestId("file-picker-file-checkbox-workspace:README.md"));
    fireEvent.click(screen.getByTestId("file-picker-submit"));

    expect(onSubmit).toHaveBeenCalledWith({
      v: 1,
      envelopeId: "picker-env-1",
      kind: "data",
      status: "submitted",
      payload: {
        picker_id: "picker-1",
        selected_refs: [
          {
            artifact_id: "artifact-1",
            name: "README.md",
            uri: "artifact://artifact-1",
            mime_type: undefined,
            kind: undefined,
            size_bytes: undefined,
            root_id: "workspace",
            relative_path: "README.md",
          },
        ],
        query_state: {
          current_root_id: "workspace",
          current_dir: undefined,
          search: "read",
          sort: "path:desc",
        },
      },
    });
    expect(
      window.localStorage.getItem(getFilePickerDraftStorageKey("room-a", "picker-1")),
    ).toBeNull();
  });
});
