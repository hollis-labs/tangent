import { boundedText, HITL_EVIDENCE_LIMITS } from "./types";

interface DiffEvidenceProps {
  content: string;
  label: string;
  baseLabel?: string;
  headLabel?: string;
}

interface DiffLine {
  content: string;
  kind: "addition" | "deletion" | "context" | "hunk" | "header" | "notice";
  oldLine?: number;
  newLine?: number;
}

export function DiffEvidence({ content, label, baseLabel, headLabel }: DiffEvidenceProps) {
  const bounded = boundedText(content, HITL_EVIDENCE_LIMITS.inlineDiff);
  const parsed = parseUnifiedDiff(bounded.text);
  const visible = parsed.slice(0, HITL_EVIDENCE_LIMITS.renderedDiffLines);
  const truncated = bounded.truncated || visible.length < parsed.length;
  return (
    <div className="overflow-hidden border border-border-subtle bg-bg">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border-subtle px-3 py-2 font-mono text-[10px] text-fg-muted">
        <span>{baseLabel || "base"}</span>
        <span aria-hidden="true">→</span>
        <span>{headLabel || "head"}</span>
      </div>
      <div className="max-h-[34rem] overflow-auto">
        <table className="w-full border-collapse font-mono text-[11px] leading-5">
          <caption className="sr-only">Unified diff: {label}</caption>
          <tbody>
            {visible.map((line, index) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: parsed diff lines are immutable and never reordered
              <tr key={index} className={diffRowClass(line.kind)}>
                <td className="w-10 select-none border-r border-border-subtle px-2 text-right align-top text-fg-faint">
                  {line.oldLine ?? ""}
                </td>
                <td className="w-10 select-none border-r border-border-subtle px-2 text-right align-top text-fg-faint">
                  {line.newLine ?? ""}
                </td>
                <td className="min-w-full whitespace-pre px-3 align-top text-fg-secondary">
                  {line.content}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {truncated ? (
        <p
          role="status"
          className="border-t border-warning bg-warning-muted px-3 py-2 text-xs text-warning"
        >
          Diff display stopped at Tangent’s safe size boundary.
        </p>
      ) : null}
    </div>
  );
}

function parseUnifiedDiff(content: string): DiffLine[] {
  const result: DiffLine[] = [];
  let oldLine: number | undefined;
  let newLine: number | undefined;
  for (const contentLine of content.replaceAll("\r\n", "\n").split("\n")) {
    const hunk = contentLine.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
    if (hunk) {
      oldLine = Number(hunk[1]);
      newLine = Number(hunk[2]);
      result.push({ kind: "hunk", content: contentLine });
      continue;
    }
    if (
      contentLine.startsWith("diff --git ") ||
      contentLine.startsWith("index ") ||
      contentLine.startsWith("--- ") ||
      contentLine.startsWith("+++ ")
    ) {
      result.push({ kind: "header", content: contentLine });
      continue;
    }
    if (contentLine.startsWith("+") && !contentLine.startsWith("+++")) {
      result.push({ kind: "addition", content: contentLine, newLine });
      if (newLine !== undefined) newLine++;
      continue;
    }
    if (contentLine.startsWith("-") && !contentLine.startsWith("---")) {
      result.push({ kind: "deletion", content: contentLine, oldLine });
      if (oldLine !== undefined) oldLine++;
      continue;
    }
    if (contentLine.startsWith("\\ No newline")) {
      result.push({ kind: "notice", content: contentLine });
      continue;
    }
    result.push({ kind: "context", content: contentLine, oldLine, newLine });
    if (oldLine !== undefined) oldLine++;
    if (newLine !== undefined) newLine++;
  }
  return result;
}

function diffRowClass(kind: DiffLine["kind"]): string {
  switch (kind) {
    case "addition":
      return "bg-success-muted [&>td:last-child]:text-success";
    case "deletion":
      return "bg-danger-muted [&>td:last-child]:text-danger";
    case "hunk":
      return "bg-info-muted [&>td:last-child]:text-info";
    case "header":
      return "bg-surface font-semibold [&>td:last-child]:text-fg-secondary";
    case "notice":
      return "italic [&>td:last-child]:text-fg-muted";
    default:
      return "";
  }
}
