import { Markdown } from "@/components/markdown";

import { HITL_EVIDENCE_LIMITS } from "./types";

interface MarkdownEvidenceProps {
  content: string;
}

// The drawer's markdown pane is the shared renderer under HITL's own policy:
// the published inline-evidence bound, and link destinations withheld because
// here the destination is part of what is being reviewed rather than a citation
// the reader asked to follow.
export function MarkdownEvidence({ content }: MarkdownEvidenceProps) {
  return (
    <Markdown
      content={content}
      maxLength={HITL_EVIDENCE_LIMITS.inlineText}
      linkPolicy="withhold"
      tone="evidence"
      truncationNotice="Content stopped at Tangent’s 64 KiB inline evidence limit."
    />
  );
}
