import type { ReactNode } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";

import { boundedText } from "@/lib/bounded-text";
import { cn } from "@/lib/utils";

// The one markdown renderer in Tangent. Every surface that displays
// agent-authored prose routes through it, so the safety properties below are
// stated once and hold everywhere rather than being re-argued per call site.
//
// Three properties, and each is a consequence of something absent:
//
//   - **No HTML is ever constructed from agent text.** `react-markdown` builds
//     a React element tree; it does not set `innerHTML`. The one plugin that
//     would change that, `rehype-raw`, is not installed and must not be.
//   - **Raw HTML in the source stays visible as text.** `remarkHTMLAsText`
//     below rewrites mdast `html` nodes to `text` nodes, so `<script>` renders
//     as the five characters an operator can read rather than being silently
//     dropped. A renderer that hides part of the evidence is worse for a
//     decision surface than one that renders it inert.
//   - **Link destinations carry no authority by default.** `linkPolicy`
//     decides how much of a destination the reader sees; neither shipped value
//     emits an anchor, so nothing here can navigate. `react-markdown`'s own URL
//     sanitizer still runs underneath and blanks `javascript:` and `data:`
//     destinations before this component sees them, so the guarantee does not
//     rest on that components map staying anchor-free.
//
// Images are rendered as their alt text for the same reason: an `<img>` is a
// network fetch the agent chose, and the document CSP is not the right place to
// discover that.
export const MARKDOWN_DISPLAY_LIMIT = 65_536;

// How much of a link destination the reader is shown. Neither value produces an
// anchor — the difference is only whether the URL itself is legible.
//
//   - `withhold` — the label alone, tagged. What the HITL evidence drawer
//     shows, where the destination is part of what is under review.
//   - `reveal` — the label and the URL as text. What envelope panes show, where
//     the reader asked to see the agent's own output and a hidden citation is
//     just a worse document.
export type MarkdownLinkPolicy = "withhold" | "reveal";

export type MarkdownTone = "default" | "evidence";

interface MarkdownProps {
  content: string;
  /** Display ceiling in characters. Exceeding it renders `truncationNotice`. */
  maxLength?: number;
  linkPolicy?: MarkdownLinkPolicy;
  tone?: MarkdownTone;
  truncationNotice?: string;
  className?: string;
  "data-testid"?: string;
}

export function Markdown({
  content,
  maxLength = MARKDOWN_DISPLAY_LIMIT,
  linkPolicy = "reveal",
  tone = "default",
  truncationNotice = "Content stopped at Tangent’s display limit.",
  className,
  "data-testid": testID,
}: MarkdownProps) {
  const bounded = boundedText(content, maxLength);
  return (
    <div
      data-testid={testID}
      className={cn(
        "space-y-4 text-sm leading-7 [overflow-wrap:anywhere]",
        tone === "evidence" ? "text-[#c5cad1]" : "text-zinc-300",
        className,
      )}
    >
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkHTMLAsText]}
        components={markdownComponents(tone, linkPolicy)}
      >
        {bounded.text}
      </ReactMarkdown>
      {bounded.truncated ? (
        <p role="status" className="border-l-2 border-[#b17c3c] pl-3 text-xs text-[#d5b486]">
          {truncationNotice}
        </p>
      ) : null}
    </div>
  );
}

// Headings are demoted three levels. Every surface that renders markdown here
// sits under a real page heading, so an agent's `#` is a subsection of ours,
// not a peer of it. Three visual tiers survive the clamp, which is as much
// hierarchy as the original renderer offered.
const HEADING_TAGS = ["h4", "h5", "h6", "h6", "h6", "h6"] as const;

const HEADING_CLASSES = [
  "mt-6 border-b pb-2 text-base font-semibold tracking-[-0.015em] first:mt-0",
  "mt-5 text-sm font-semibold tracking-[-0.01em] first:mt-0",
  "mt-4 text-xs font-semibold uppercase tracking-[0.08em] first:mt-0",
  "mt-4 text-xs font-semibold uppercase tracking-[0.08em] first:mt-0",
  "mt-4 text-xs font-semibold uppercase tracking-[0.08em] first:mt-0",
  "mt-4 text-xs font-semibold uppercase tracking-[0.08em] first:mt-0",
] as const;

function markdownComponents(tone: MarkdownTone, linkPolicy: MarkdownLinkPolicy): Components {
  const evidence = tone === "evidence";
  const strongText = evidence ? "text-[#eef0f2]" : "text-zinc-100";
  const mutedText = evidence ? "text-[#8e959f]" : "text-zinc-500";
  const rule = evidence ? "border-[#30343b]" : "border-zinc-800";
  const accent = evidence ? "border-[#f2b84b]" : "border-zinc-600";
  const marker = evidence ? "marker:text-[#f2b84b]" : "marker:text-zinc-500";
  const codeSurface = evidence ? "bg-[#1b1e23] text-[#e2e6ea]" : "bg-zinc-800 text-zinc-100";
  const blockSurface = evidence
    ? "border-[#57606b] bg-[#0b0d10] text-[#cbd1d8]"
    : "border-zinc-700 bg-zinc-950 text-zinc-100";

  const heading = (level: number) =>
    function MarkdownHeading({ children }: { children?: ReactNode }) {
      const Tag = HEADING_TAGS[level - 1];
      return (
        <Tag className={cn(HEADING_CLASSES[level - 1], strongText, level === 1 && rule)}>
          {children}
        </Tag>
      );
    };

  return {
    h1: heading(1),
    h2: heading(2),
    h3: heading(3),
    h4: heading(4),
    h5: heading(5),
    h6: heading(6),
    p: ({ children }) => <p>{children}</p>,
    strong: ({ children }) => (
      <strong className={cn("font-semibold", strongText)}>{children}</strong>
    ),
    em: ({ children }) => <em className="italic">{children}</em>,
    del: ({ children }) => <del className={cn("line-through", mutedText)}>{children}</del>,
    hr: () => <hr className={cn("border-t", rule)} />,
    ul: ({ children }) => <ul className={cn("list-disc space-y-1 pl-5", marker)}>{children}</ul>,
    ol: ({ children }) => (
      <ol className={cn("list-decimal space-y-1 pl-5 marker:font-mono", marker)}>{children}</ol>
    ),
    // A nested list is a child of its `li`, so the item's own `space-y` would
    // not reach it; the margin here is what keeps nesting legible.
    li: ({ children }) => <li className="[&>ul]:mt-1 [&>ol]:mt-1 [&>p]:m-0">{children}</li>,
    blockquote: ({ children }) => (
      <blockquote className={cn("border-l-2 pl-4 [&>p]:m-0", accent, evidence && "text-[#aeb4bc]")}>
        {children}
      </blockquote>
    ),
    code: ({ children, className: codeClassName }) => (
      <code className={cn("px-1.5 py-0.5 font-mono text-xs", codeSurface, codeClassName)}>
        {children}
      </code>
    ),
    // The block wrapper, and the reset that undoes the inline `code` pill for
    // the `code` element every fenced block nests inside it.
    pre: ({ children }) => {
      const language = fencedLanguage(children);
      return (
        <div>
          {language ? (
            <p className={cn("mb-1 font-mono text-[10px] uppercase tracking-[0.13em]", mutedText)}>
              {language}
            </p>
          ) : null}
          <pre
            className={cn(
              "overflow-x-auto border-l-2 px-4 py-3 font-mono text-xs leading-6",
              "[&>code]:bg-transparent [&>code]:p-0 [&>code]:text-xs",
              blockSurface,
            )}
          >
            {children}
          </pre>
        </div>
      );
    },
    table: ({ children }) => (
      <div className="overflow-x-auto">
        <table className={cn("w-full border-collapse text-left text-xs", strongText)}>
          {children}
        </table>
      </div>
    ),
    thead: ({ children }) => <thead className={cn("border-b", rule)}>{children}</thead>,
    tr: ({ children }) => <tr className={cn("border-b last:border-b-0", rule)}>{children}</tr>,
    th: ({ children }) => (
      <th className={cn("px-2 py-1.5 font-semibold", strongText)}>{children}</th>
    ),
    td: ({ children }) => <td className="px-2 py-1.5 align-top">{children}</td>,
    // GFM task lists. `disabled` is the point: the checkbox reports the agent's
    // state and is not an input the reader can answer through.
    input: ({ checked, type }) =>
      type === "checkbox" ? (
        <input type="checkbox" checked={Boolean(checked)} disabled readOnly className="mr-1.5" />
      ) : null,
    img: ({ alt }) => (
      <span className={cn("font-mono text-[10px] uppercase tracking-[0.12em]", mutedText)}>
        {alt ? `image: ${alt}` : "image withheld"}
      </span>
    ),
    a: ({ children, href }) => (
      <MarkdownLink href={href} policy={linkPolicy} muted={mutedText}>
        {children}
      </MarkdownLink>
    ),
  };
}

function MarkdownLink({
  children,
  href,
  policy,
  muted,
}: {
  children?: ReactNode;
  href?: string;
  policy: MarkdownLinkPolicy;
  muted: string;
}) {
  if (policy === "withhold") {
    return (
      <span title="Links are displayed without navigation authority">
        {children}
        <span className={cn("ml-1 font-mono text-[9px] uppercase tracking-[0.12em]", muted)}>
          link withheld
        </span>
      </span>
    );
  }
  // GFM turns a bare URL into a link whose label already is the URL. Printing
  // it twice is noise, not disclosure.
  const label = typeof children === "string" ? children : undefined;
  return (
    <span title="Links are displayed without navigation authority">
      {children}
      {href ? (
        href === label ? null : (
          <span className={cn("ml-1 font-mono text-[11px] break-all", muted)}>({href})</span>
        )
      ) : (
        // `react-markdown`'s own URL sanitizer blanked the destination — a
        // `javascript:` or `data:` scheme, or a genuinely empty target. Saying
        // so beats rendering a bare label that looks like ordinary prose.
        <span className={cn("ml-1 font-mono text-[9px] uppercase tracking-[0.12em]", muted)}>
          link withheld
        </span>
      )}
    </span>
  );
}

function fencedLanguage(children: ReactNode): string | undefined {
  if (!children || typeof children !== "object" || !("props" in children)) return undefined;
  const props = (children as { props?: { className?: unknown } }).props;
  if (typeof props?.className !== "string") return undefined;
  return props.className.match(/language-([\w+#.-]+)/)?.[1];
}

interface MDASTNode {
  type: string;
  value?: string;
  children?: MDASTNode[];
}

// Rewrite every mdast `html` node to a `text` node before it reaches hast.
//
// Without this, `react-markdown` drops raw HTML entirely: correct for safety,
// wrong for a review surface, because the operator deciding on the content is
// never told a piece of it existed. As text the same bytes are inert and
// readable, which is the property both audiences actually want.
function remarkHTMLAsText() {
  return (tree: MDASTNode) => {
    const visit = (node: MDASTNode) => {
      if (node.type === "html") {
        node.type = "text";
        node.value = node.value ?? "";
      }
      for (const child of node.children ?? []) visit(child);
    };
    visit(tree);
  };
}
