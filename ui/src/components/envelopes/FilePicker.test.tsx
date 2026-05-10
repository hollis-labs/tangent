import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { FilePicker, type FilePickerEnvelope, type FilePickerResponse } from "./FilePicker";

describe("<FilePicker>", () => {
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
        },
      },
    };

    render(<FilePicker envelope={envelope} onSubmit={onSubmit} onCancel={onCancel} />);

    expect(screen.getByTestId("file-picker-selected-count")).toHaveTextContent("1");

    const readmeCheckbox = screen
      .getByTestId("file-picker-file-workspace:README.md")
      .querySelector("input");
    expect(readmeCheckbox).not.toBeNull();
    if (!readmeCheckbox) {
      throw new Error("README checkbox missing");
    }
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
        },
      },
    });
  });
});
