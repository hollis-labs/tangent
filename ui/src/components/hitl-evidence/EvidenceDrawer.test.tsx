import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  expectProseLiteral,
  expectProseRendered,
  proseProbe,
} from "@/components/markdown/prose-probe";
import { EvidenceDrawer } from "./EvidenceDrawer";

describe("<EvidenceDrawer>", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("renders markdown and unified diff as bounded text without HTML or link authority", () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    render(
      <EvidenceDrawer
        itemID="item-safe"
        itemTitle="Inspect safe evidence"
        evidence={[
          {
            type: "markdown",
            label: "Release notes",
            content:
              "## Result\n\n**Passed** `<code>`\n\n<img src=x onerror=alert(1)>\n\n[Run action](javascript:alert(1))",
          },
          {
            type: "diff",
            label: "Change",
            format: "unified",
            content: "--- a/file.ts\n+++ b/file.ts\n@@ -1 +1 @@\n-old\n+<script>alert(1)</script>",
          },
        ]}
        open
        onOpenChange={() => {}}
      />,
    );

    expect(screen.getByRole("dialog", { name: "Evidence" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Result" })).toBeInTheDocument();
    expect(screen.getByText("Run action")).toHaveTextContent("link withheld");
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    expect(document.querySelector("script")).toBeNull();
    expect(document.querySelector("img")).toBeNull();
    expect(screen.getByText("+<script>alert(1)</script>")).toBeInTheDocument();
    expect(screen.getByRole("table", { name: "Unified diff: Change" })).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("enforces inline and diff display bounds with useful notices", () => {
    render(
      <EvidenceDrawer
        itemID="item-bounds"
        itemTitle="Boundaries"
        evidence={[
          { type: "text", label: "Large text", content: "x".repeat(65_540) },
          {
            type: "diff",
            label: "Large diff",
            content: Array.from({ length: 2_005 }, (_, index) => `+line ${index}`).join("\n"),
          },
        ]}
        open
        onOpenChange={() => {}}
      />,
    );

    expect(
      screen.getByText("Content stopped at Tangent’s 64 KiB inline evidence limit."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Diff display stopped at Tangent’s safe size boundary."),
    ).toBeInTheDocument();
    expect(screen.queryByText("+line 2004")).not.toBeInTheDocument();
  });

  it("resolves a durable Tangent reference through the read-only evidence endpoint", async () => {
    const fetchMock = vi.fn(async () =>
      jsonResponse({
        status: "available",
        label: "Earlier review",
        surface: {
          surface_id: "surface_hitl_default",
          state: "active",
          revision: 8,
          updated_at: "2026-09-04T12:00:00Z",
        },
        interaction: {
          interaction_id: "interaction-1",
          state: "resolved",
          revision: 5,
          definition_kind: "tangent.hitl-item",
          definition_version: "1.0",
          request_snapshot: { title: "Earlier decision" },
          updated_at: "2026-09-04T11:00:00Z",
        },
        read_only_url: "/hitl/items/interaction-1",
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    render(
      <EvidenceDrawer
        itemID="item-reference"
        itemTitle="Inspect reference"
        evidence={[
          {
            type: "tangent_reference",
            label: "Earlier review",
            surface_id: "surface_hitl_default",
            interaction_id: "interaction-1",
          },
        ]}
        open
        onOpenChange={() => {}}
      />,
    );

    expect(await screen.findByText("tangent.hitl-item@1.0")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/hitl/items/item-reference/evidence/0/reference",
      expect.objectContaining({ headers: { Accept: "application/json" } }),
    );
    const link = screen.getByRole("link", { name: /open durable inbox record/i });
    expect(link).toHaveAttribute("href", "/hitl/items/interaction-1");
    fireEvent.click(screen.getByText("Durable request snapshot"));
    expect(screen.getByText(/Earlier decision/)).toBeInTheDocument();
  });

  it("keeps artifact metadata visible and previews only after an explicit capability action", async () => {
    const fetchMock = vi.fn(async () =>
      jsonResponse(
        {
          contract_version: "1.0",
          code: "evidence_unsupported",
          message: "No explicitly authorized host adapter can preview this evidence.",
        },
        422,
      ),
    );
    vi.stubGlobal("fetch", fetchMock);
    render(
      <EvidenceDrawer
        itemID="item-artifact"
        itemTitle="Inspect artifact"
        evidence={[
          {
            type: "artifact_ref",
            label: "Test report",
            authority: "torque",
            artifact_id: "artifact-544",
            digest: "sha256:abc",
            media_type: "application/pdf",
            size_bytes: 2048,
            sensitivity: "internal",
            retrieval_capability_id: "pdf-preview-v1",
          },
        ]}
        open
        onOpenChange={() => {}}
      />,
    );

    expect(screen.getByText("artifact-544")).toBeInTheDocument();
    expect(screen.getByText("2.0 KiB")).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Request safe preview" }));
    expect(await screen.findByText("Preview unsupported")).toBeInTheDocument();
    expect(screen.getByText("artifact-544")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/hitl/items/item-artifact/evidence/0/preview",
      expect.objectContaining({ headers: { Accept: "application/json" } }),
    );
  });

  it("shows useful missing and unsupported states and restores close semantics", async () => {
    const onOpenChange = vi.fn();
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(
          { code: "evidence_missing", message: "This evidence entry no longer exists." },
          404,
        ),
      ),
    );
    render(
      <EvidenceDrawer
        itemID="item-missing"
        itemTitle="Missing evidence"
        evidence={[
          { type: "future_media", label: "Future media" },
          {
            type: "tangent_reference",
            label: "Removed review",
            surface_id: "surface-missing",
          },
        ]}
        open
        onOpenChange={onOpenChange}
      />,
    );

    expect(screen.getByText(/Evidence type “future_media” is not supported/)).toBeInTheDocument();
    expect(await screen.findByText("Reference missing")).toBeInTheDocument();
    const dialog = screen.getByRole("dialog", { name: "Evidence" });
    expect(
      within(dialog).getByText(/cannot resolve, cancel, disconnect, or change FIFO order/),
    ).toBeInTheDocument();
    const close = within(dialog).getByRole("button", { name: "Close evidence" });
    await waitFor(() => expect(close).toHaveFocus());
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("keeps expired and unauthorized Tangent references as readable inline states", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: string | URL | Request) => {
        const path = String(input);
        return path.includes("/evidence/0/")
          ? jsonResponse(
              {
                code: "evidence_expired",
                message: "The authority reports that this evidence preview has expired.",
              },
              410,
            )
          : jsonResponse(
              {
                code: "evidence_unauthorized",
                message: "This Tangent host has no authority to open the referenced content.",
              },
              403,
            );
      }),
    );
    render(
      <EvidenceDrawer
        itemID="item-reference-states"
        itemTitle="Reference states"
        evidence={[
          { type: "tangent_reference", label: "Expired run", surface_id: "surface-expired" },
          { type: "tangent_reference", label: "Private run", surface_id: "surface-private" },
        ]}
        open
        onOpenChange={() => {}}
      />,
    );

    expect(await screen.findByText("Preview expired")).toBeInTheDocument();
    expect(await screen.findByText("Preview not authorized")).toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "Evidence" })).toBeInTheDocument();
  });

  it("routes a tangent reference description through the shared markdown renderer", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({
        ok: true,
        status: 200,
        json: async () => ({
          status: "available",
          label: "Earlier review",
          surface: {
            surface_id: "surface_1",
            state: "resolved",
            revision: 2,
            updated_at: "2026-09-04T15:00:00Z",
          },
        }),
      })),
    );

    render(
      <EvidenceDrawer
        itemID="item-reference"
        itemTitle="Inspect a reference"
        evidence={[
          {
            type: "tangent_reference",
            label: "Earlier review",
            surface_id: "surface_1",
            description: proseProbe("ev-description"),
          },
        ]}
        open
        onOpenChange={() => {}}
      />,
    );

    await waitFor(() => expectProseRendered("ev-description"));
  });

  // The Leave bucket, pinned: `type: "text"` is the caller declaring plain
  // text, and rendering it as markdown anyway would override that declaration.
  it("keeps declared plain-text evidence literal", () => {
    render(
      <EvidenceDrawer
        itemID="item-text"
        itemTitle="Inspect text"
        evidence={[{ type: "text", label: "Warning", content: proseProbe("ev-text") }]}
        open
        onOpenChange={() => {}}
      />,
    );

    expectProseLiteral(screen.getByTestId("hitl-evidence-text"), "ev-text");
  });
});

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
