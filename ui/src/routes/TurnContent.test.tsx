import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { TurnItemView } from "@/lib/turns-api";
import { TurnContent } from "./TurnContent";

const original = "Original\n  **Markdown** 😺\n<script>not executable</script>";
const item: TurnItemView = {
  contract_version: "1.1",
  item_id: "item",
  agent_id: "test-sender",
  kind: "checkpoint",
  title: "Source publication",
  content: original,
  summary: "<button>Do not execute me</button>",
  annotations: [
    {
      schema_version: 1,
      stage_id: "summarize",
      stage_version: "1",
      kind: "summary",
      summary: { text: "<button>Do not execute me</button>" },
    },
  ],
  stage_trace: [
    {
      stage_id: "summarize",
      stage_version: "1",
      outcome: "timed_out",
      duration_ms: 15000,
      failure_code: "stage_timeout",
    },
  ],
  source_message: {
    schema_version: 1,
    origin: "publication",
    endpoint_ref: "test-endpoint",
    channel: "owner-inbox",
    message_id: "actual-publication",
    sequence: 42,
    sender_urn: "msg://agent/local/test-sender",
  },
  state: "presented",
  queue_sequence: 1,
  revision: 1,
  created_at: "2026-10-09T00:00:00Z",
  updated_at: "2026-10-09T00:00:00Z",
  delivery_state: "queued",
  replyable: false,
};

describe("TurnContent", () => {
  it("escapes summaries and preserves the original separately from failures and trace", () => {
    const { container } = render(<TurnContent item={item} />);
    expect(screen.getByText(item.summary ?? "")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Do not execute me" })).not.toBeInTheDocument();
    expect(container.querySelector("script")).toBeNull();
    expect(container.querySelector("pre")?.textContent).toBe(original);
    const detail = screen.getByText("Original message").closest("details");
    expect(detail?.hasAttribute("open")).toBe(false);
    fireEvent.click(screen.getByText("Original message"));
    expect(screen.getByRole("status")).toHaveTextContent(
      "Stage summarize timed out: stage_timeout",
    );
    expect(screen.getByText(/15000 ms/)).toHaveTextContent("summarize (1): timed_out");
    expect(screen.getByText("msg://agent/local/test-sender")).toBeInTheDocument();
    expect(screen.getByText(/actual-publication/)).toHaveTextContent("sequence 42");
  });

  it("reads a 1.0 item with no annotations or traces without changing its content", () => {
    render(
      <TurnContent
        item={{
          ...item,
          contract_version: "1.0",
          annotations: undefined,
          stage_trace: undefined,
          source_message: undefined,
          summary: undefined,
          content: "Legacy original",
        }}
      />,
    );
    expect(screen.getByText("Legacy original")).toBeInTheDocument();
    expect(screen.queryByText("Stage trace")).not.toBeInTheDocument();
    expect(screen.queryByText("Original message")).not.toBeInTheDocument();
  });

  it("shows a failed-stage marker even when no summary was produced", () => {
    render(
      <TurnContent
        item={{
          ...item,
          annotations: undefined,
          summary: undefined,
          content: "Unsummarized original",
        }}
      />,
    );
    expect(screen.getByText("Unsummarized original")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("stage_timeout");
  });
});
