import { Fragment, type ReactNode } from "react";

import { boundedText, HITL_EVIDENCE_LIMITS } from "./types";

interface MarkdownEvidenceProps {
  content: string;
}

interface MarkdownBlock {
  kind: "heading" | "paragraph" | "quote" | "code" | "unordered" | "ordered";
  level?: number;
  language?: string;
  lines: string[];
}

// This deliberately small renderer never creates HTML from evidence. Unsupported
// markdown remains readable text, and link destinations never become authority.
export function MarkdownEvidence({ content }: MarkdownEvidenceProps) {
  const bounded = boundedText(content, HITL_EVIDENCE_LIMITS.inlineText);
  const blocks = parseBlocks(bounded.text);
  return (
    <div className="space-y-4 text-sm leading-7 text-[#c5cad1] [overflow-wrap:anywhere]">
      {blocks.map((block, index) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: parsed markdown blocks are immutable and never reordered
        <MarkdownBlockView key={`${block.kind}-${index}`} block={block} />
      ))}
      {bounded.truncated ? <TruncationNotice /> : null}
    </div>
  );
}

function MarkdownBlockView({ block }: { block: MarkdownBlock }) {
  if (block.kind === "heading") {
    const Heading = block.level === 1 ? "h4" : block.level === 2 ? "h5" : "h6";
    return (
      <Heading className="border-b border-[#30343b] pb-2 text-base font-semibold tracking-[-0.015em] text-[#eef0f2]">
        {renderInline(block.lines.join(" "))}
      </Heading>
    );
  }
  if (block.kind === "code") {
    return (
      <div>
        {block.language ? (
          <p className="mb-1 font-mono text-[10px] uppercase tracking-[0.13em] text-[#8e959f]">
            {block.language}
          </p>
        ) : null}
        <pre className="overflow-x-auto border-l-2 border-[#57606b] bg-[#0b0d10] px-4 py-3 font-mono text-xs leading-6 text-[#cbd1d8]">
          <code>{block.lines.join("\n")}</code>
        </pre>
      </div>
    );
  }
  if (block.kind === "quote") {
    return (
      <blockquote className="border-l-2 border-[#f2b84b] pl-4 text-[#aeb4bc]">
        {block.lines.map((line, index) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: parsed quote lines are immutable and never reordered
          <Fragment key={index}>
            {index > 0 ? <br /> : null}
            {renderInline(line)}
          </Fragment>
        ))}
      </blockquote>
    );
  }
  if (block.kind === "unordered" || block.kind === "ordered") {
    const List = block.kind === "unordered" ? "ul" : "ol";
    return (
      <List
        className={
          block.kind === "unordered"
            ? "list-disc space-y-1 pl-5 marker:text-[#f2b84b]"
            : "list-decimal space-y-1 pl-5 marker:font-mono marker:text-[#8e959f]"
        }
      >
        {block.lines.map((line, index) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: parsed list entries are immutable and never reordered
          <li key={index}>{renderInline(line)}</li>
        ))}
      </List>
    );
  }
  return <p>{renderInline(block.lines.join(" "))}</p>;
}

function renderInline(value: string): ReactNode[] {
  const parts = value.split(/(`[^`]+`|\*\*[^*]+\*\*|\[[^\]]+\]\([^)]+\))/g);
  return parts.map((part, index) => {
    if (part.startsWith("`") && part.endsWith("`")) {
      return (
        // biome-ignore lint/suspicious/noArrayIndexKey: parsed inline tokens are immutable and never reordered
        <code key={index} className="bg-[#1b1e23] px-1.5 py-0.5 font-mono text-xs text-[#e2e6ea]">
          {part.slice(1, -1)}
        </code>
      );
    }
    if (part.startsWith("**") && part.endsWith("**")) {
      return (
        // biome-ignore lint/suspicious/noArrayIndexKey: parsed inline tokens are immutable and never reordered
        <strong key={index} className="font-semibold text-[#eef0f2]">
          {part.slice(2, -2)}
        </strong>
      );
    }
    const link = part.match(/^\[([^\]]+)\]\(([^)]+)\)$/);
    if (link) {
      return (
        <span
          key={`${link[1]}-${link[2]}`}
          className="text-[#d7dce2]"
          title="Evidence links are displayed without navigation authority"
        >
          {link[1]}
          <span className="ml-1 font-mono text-[9px] uppercase tracking-[0.12em] text-[#8e959f]">
            link withheld
          </span>
        </span>
      );
    }
    return (
      // biome-ignore lint/suspicious/noArrayIndexKey: parsed inline tokens are immutable and never reordered
      <Fragment key={index}>{part}</Fragment>
    );
  });
}

function parseBlocks(value: string): MarkdownBlock[] {
  const lines = value.replaceAll("\r\n", "\n").split("\n");
  const blocks: MarkdownBlock[] = [];
  let index = 0;
  while (index < lines.length) {
    const line = lines[index];
    if (!line.trim()) {
      index++;
      continue;
    }
    const fence = line.match(/^\s*```([A-Za-z0-9_+-]{0,32})\s*$/);
    if (fence) {
      const code: string[] = [];
      index++;
      while (index < lines.length && !/^\s*```\s*$/.test(lines[index])) code.push(lines[index++]);
      if (index < lines.length) index++;
      blocks.push({ kind: "code", language: fence[1], lines: code });
      continue;
    }
    const heading = line.match(/^\s*(#{1,6})\s+(.+)$/);
    if (heading) {
      blocks.push({ kind: "heading", level: heading[1].length, lines: [heading[2]] });
      index++;
      continue;
    }
    if (/^\s*>\s?/.test(line)) {
      const quote: string[] = [];
      while (index < lines.length && /^\s*>\s?/.test(lines[index])) {
        quote.push(lines[index++].replace(/^\s*>\s?/, ""));
      }
      blocks.push({ kind: "quote", lines: quote });
      continue;
    }
    if (/^\s*[-*+]\s+/.test(line)) {
      const items: string[] = [];
      while (index < lines.length && /^\s*[-*+]\s+/.test(lines[index])) {
        items.push(lines[index++].replace(/^\s*[-*+]\s+/, ""));
      }
      blocks.push({ kind: "unordered", lines: items });
      continue;
    }
    if (/^\s*\d+[.)]\s+/.test(line)) {
      const items: string[] = [];
      while (index < lines.length && /^\s*\d+[.)]\s+/.test(lines[index])) {
        items.push(lines[index++].replace(/^\s*\d+[.)]\s+/, ""));
      }
      blocks.push({ kind: "ordered", lines: items });
      continue;
    }
    const paragraph = [line.trim()];
    index++;
    while (index < lines.length && lines[index].trim() && !startsBlock(lines[index])) {
      paragraph.push(lines[index++].trim());
    }
    blocks.push({ kind: "paragraph", lines: paragraph });
  }
  return blocks;
}

function startsBlock(line: string): boolean {
  return /^(\s*```|\s*#{1,6}\s+|\s*>\s?|\s*[-*+]\s+|\s*\d+[.)]\s+)/.test(line);
}

function TruncationNotice() {
  return (
    <p role="status" className="border-l-2 border-[#b17c3c] pl-3 text-xs text-[#d5b486]">
      Content stopped at Tangent’s 64 KiB inline evidence limit.
    </p>
  );
}
