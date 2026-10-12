import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { CopyObjectIdButton } from "./CopyObjectIdButton";

afterEach(() => vi.unstubAllGlobals());

it("copies the object id and confirms", async () => {
  const writeText = vi.fn(async () => {});
  vi.stubGlobal("navigator", { clipboard: { writeText } });
  render(<CopyObjectIdButton objectId="DOC-123" />);
  fireEvent.click(screen.getByRole("button", { name: "Copy object id" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Object id copied" })).toBeInTheDocument(),
  );
  expect(writeText).toHaveBeenCalledWith("DOC-123");
});

it("reports a failed copy instead of failing silently", async () => {
  vi.stubGlobal("navigator", {
    clipboard: { writeText: vi.fn(async () => Promise.reject(new Error("denied"))) },
  });
  render(<CopyObjectIdButton objectId="DOC-123" />);
  fireEvent.click(screen.getByRole("button", { name: "Copy object id" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Copy failed" })).toBeInTheDocument(),
  );
});
