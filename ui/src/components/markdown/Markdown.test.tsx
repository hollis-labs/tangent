import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Markdown } from "./Markdown";

describe("<Markdown>", () => {
  it("renders the GFM the hand-rolled renderer could not", () => {
    render(
      <Markdown
        content={[
          "# Proposal",
          "",
          "A *tentative* plan with ~~one dropped option~~.",
          "",
          "- outer",
          "  - inner",
          "- [x] done",
          "- [ ] pending",
          "",
          "| Option | Cost |",
          "| --- | --- |",
          "| Ship | low |",
        ].join("\n")}
      />,
    );

    expect(screen.getByRole("heading", { name: "Proposal" })).toBeInTheDocument();
    expect(screen.getByText("tentative").tagName).toBe("EM");
    expect(screen.getByText("one dropped option").tagName).toBe("DEL");

    // Nesting survives: the inner list is a descendant of the outer item, not a
    // sibling flattened next to it.
    const outer = screen.getByText("outer").closest("li");
    expect(outer).not.toBeNull();
    expect(within(outer as HTMLElement).getByRole("list")).toBeInTheDocument();

    const boxes = screen.getAllByRole("checkbox") as HTMLInputElement[];
    expect(boxes).toHaveLength(2);
    expect(boxes[0].checked).toBe(true);
    expect(boxes[1].checked).toBe(false);
    // The reader is looking at the agent's state, not answering a question.
    expect(boxes.every((box) => box.disabled)).toBe(true);

    const table = screen.getByRole("table");
    expect(within(table).getByRole("columnheader", { name: "Option" })).toBeInTheDocument();
    expect(within(table).getByRole("cell", { name: "Ship" })).toBeInTheDocument();
  });

  it("never builds HTML from the source and never hides it either", () => {
    render(<Markdown content={"<script>alert(1)</script>\n\n<b>bold?</b> and <img src=x>"} />);

    expect(document.querySelector("script")).toBeNull();
    expect(document.querySelector("img")).toBeNull();
    expect(document.querySelector("b")).toBeNull();
    // Dropping it silently would leave a reviewer deciding on content they were
    // never shown, so the same bytes render inert instead.
    expect(screen.getByText(/<script>alert\(1\)<\/script>/)).toBeInTheDocument();
    expect(screen.getByText(/<b>bold\?<\/b>/)).toBeInTheDocument();
  });

  it("grants no navigation authority under either link policy", () => {
    const content = "[Run action](javascript:alert(1)) and [Docs](https://example.com/spec)";

    const withheld = render(<Markdown content={content} linkPolicy="withhold" />);
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    expect(screen.getByText("Docs").parentElement).toHaveTextContent("link withheld");
    expect(screen.queryByText("(https://example.com/spec)")).not.toBeInTheDocument();
    withheld.unmount();

    render(<Markdown content={content} linkPolicy="reveal" />);
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    // Reveal shows the destination so a citation is readable; it is still text.
    expect(screen.getByText("(https://example.com/spec)")).toBeInTheDocument();
    // The `javascript:` destination is blanked by react-markdown's sanitizer
    // before it reaches us; reveal says so rather than showing a bare label.
    expect(screen.queryByText(/javascript:/)).not.toBeInTheDocument();
    expect(screen.getByText("Run action").parentElement).toHaveTextContent("link withheld");
  });

  it("renders an image as its alt text rather than fetching it", () => {
    render(<Markdown content={"![a chart](https://example.com/track.png)"} />);

    expect(document.querySelector("img")).toBeNull();
    expect(screen.getByText("image: a chart")).toBeInTheDocument();
  });

  it("prints a bare autolink once", () => {
    render(<Markdown content={"See https://example.com/spec for detail."} />);

    expect(screen.getAllByText(/example\.com\/spec/)).toHaveLength(1);
  });

  it("bounds the display and says so", () => {
    render(<Markdown content={"x".repeat(120)} maxLength={64} truncationNotice="Stopped early." />);

    expect(screen.getByText("x".repeat(64))).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Stopped early.");
  });

  it("labels a fenced block with its language and does not reflow it", () => {
    render(<Markdown content={"```go\nfunc main() {\n\treturn\n}\n```"} />);

    expect(screen.getByText("go")).toBeInTheDocument();
    expect(document.querySelector("pre")?.textContent).toBe("func main() {\n\treturn\n}\n");
  });
});
