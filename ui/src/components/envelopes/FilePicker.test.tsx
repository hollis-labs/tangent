import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { FilePicker, type FilePickerEnvelope, type FilePickerResponse } from "./FilePicker";

describe("<FilePicker>", () => {
  afterEach(() => {
    window.localStorage.clear();
  });

  it("renders seeded state and submits canonical selection payloads", () => {
    const onSubmit = vi.fn<(response: FilePickerResponse) => void>();
    const onCancel = vi.fn();
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
        selected_refs: [
          {
            artifact_id: "artifact-1",
            name: "spec.md",
            uri: "artifact://artifact-1",
            root_id: "workspace",
            relative_path: "docs/spec.md",
          },
        ],
        files: [
          {
            artifact_id: "artifact-1",
            name: "spec.md",
            uri: "artifact://artifact-1",
            root_id: "workspace",
            relative_path: "docs/spec.md",
          },
          {
            artifact_id: "artifact-2",
            name: "README.md",
            uri: "artifact://artifact-2",
            root_id: "workspace",
            relative_path: "README.md",
          },
        ],
        query_state: {
          current_root_id: "workspace",
          current_dir: "",
          search: "read",
          sort: "path:desc",
        },
      },
    };

    render(
      <FilePicker envelope={envelope} onSubmit={onSubmit} onCancel={onCancel} roomID="room-1" />,
    );

    expect(screen.getByTestId("file-picker-selected-count")).toHaveTextContent("1");

    const readmeCheckbox = screen
      .getByTestId("file-picker-file-workspace:README.md")
      .querySelector("input");
    expect(readmeCheckbox).not.toBeNull();
    if (!readmeCheckbox) {
      throw new Error("README checkbox missing");
    }
    fireEvent.change(screen.getByTestId("file-picker-search"), {
      target: { value: "read" },
    });
    fireEvent.change(screen.getByTestId("file-picker-sort"), {
      target: { value: "path:desc" },
    });
    fireEvent.click(readmeCheckbox);
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
            name: "spec.md",
            uri: "artifact://artifact-1",
            mime_type: undefined,
            kind: undefined,
            size_bytes: undefined,
            root_id: "workspace",
            relative_path: "docs/spec.md",
          },
          {
            artifact_id: "artifact-2",
            name: "README.md",
            uri: "artifact://artifact-2",
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
  });

  it("blocks empty submit locally", () => {
    const onSubmit = vi.fn<(response: FilePickerResponse) => void>();
    const envelope: FilePickerEnvelope = {
      v: 1,
      id: "picker-env-2",
      type: "tangent.file-picker",
      data: {
        picker_id: "picker-2",
        browse_roots: [{ root_id: "workspace", label: "Workspace", path: "/tmp/workspace" }],
        files: [
          {
            artifact_id: "artifact-2",
            name: "README.md",
            uri: "artifact://artifact-2",
            root_id: "workspace",
            relative_path: "README.md",
          },
        ],
        selected_refs: [
          {
            artifact_id: "artifact-2",
            name: "README.md",
            uri: "artifact://artifact-2",
            root_id: "workspace",
            relative_path: "README.md",
          },
        ],
      },
    };

    render(<FilePicker envelope={envelope} onSubmit={onSubmit} onCancel={() => {}} />);

    const checkbox = screen
      .getByTestId("file-picker-file-workspace:README.md")
      .querySelector("input");
    expect(checkbox).not.toBeNull();
    if (!checkbox) {
      throw new Error("README checkbox missing");
    }
    fireEvent.click(checkbox);
    fireEvent.click(screen.getByTestId("file-picker-submit"));

    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByTestId("file-picker-submit-error")).toHaveTextContent(
      "Select at least one file before submit.",
    );
  });

  it("recovers browser-local draft state before submit and shows preview metadata", () => {
    window.localStorage.setItem(
      "tangent.file-picker.draft.v1:room-2:picker-3",
      JSON.stringify({
        activeRootID: "workspace",
        currentDir: "docs",
        search: "spec",
        sort: "path:asc",
        selectedKeys: ["workspace:docs/spec.md"],
        previewKey: "workspace:docs/spec.md",
      }),
    );
    const envelope: FilePickerEnvelope = {
      v: 1,
      id: "picker-env-3",
      type: "tangent.file-picker",
      data: {
        picker_id: "picker-3",
        browse_roots: [{ root_id: "workspace", label: "Workspace", path: "/tmp/workspace" }],
        files: [
          {
            artifact_id: "artifact-3",
            name: "spec.md",
            uri: "artifact://artifact-3",
            mime_type: "text/markdown",
            size_bytes: 128,
            root_id: "workspace",
            relative_path: "docs/spec.md",
          },
        ],
      },
    };

    render(
      <FilePicker envelope={envelope} onSubmit={() => {}} onCancel={() => {}} roomID="room-2" />,
    );

    expect(screen.getByTestId("file-picker-message")).toHaveTextContent(
      "Recovered unsent file-picker state from this browser.",
    );
    expect(screen.getByTestId("file-picker-selected-count")).toHaveTextContent("1");
    expect(screen.getByTestId("file-picker-preview")).toHaveTextContent("artifact://artifact-3");
    expect(screen.getByTestId("file-picker-preview")).toHaveTextContent("128 bytes");
  });

  it("clears the local draft after successful submit", () => {
    const onSubmit = vi.fn<(response: FilePickerResponse) => void>();
    const envelope: FilePickerEnvelope = {
      v: 1,
      id: "picker-env-4",
      type: "tangent.file-picker",
      data: {
        picker_id: "picker-4",
        browse_roots: [{ root_id: "workspace", label: "Workspace", path: "/tmp/workspace" }],
        selected_refs: [
          {
            artifact_id: "artifact-4",
            name: "notes.md",
            uri: "artifact://artifact-4",
            root_id: "workspace",
            relative_path: "notes.md",
          },
        ],
        files: [
          {
            artifact_id: "artifact-4",
            name: "notes.md",
            uri: "artifact://artifact-4",
            root_id: "workspace",
            relative_path: "notes.md",
          },
        ],
      },
    };

    render(
      <FilePicker envelope={envelope} onSubmit={onSubmit} onCancel={() => {}} roomID="room-4" />,
    );

    fireEvent.click(screen.getByTestId("file-picker-submit"));

    expect(window.localStorage.getItem("tangent.file-picker.draft.v1:room-4:picker-4")).toBeNull();
  });
});
