import { boundedText } from "@/lib/bounded-text";

export { boundedText };

export const HITL_EVIDENCE_LIMITS = {
  items: 24,
  inlineText: 65_536,
  inlineDiff: 131_072,
  renderedDiffLines: 2_000,
} as const;

interface EvidenceBase {
  label: string;
}

export interface MarkdownEvidence extends EvidenceBase {
  type: "markdown";
  content: string;
}

export interface TextEvidence extends EvidenceBase {
  type: "text";
  content: string;
  language?: string;
}

export interface DiffEvidence extends EvidenceBase {
  type: "diff";
  content: string;
  format?: "unified";
  base_label?: string;
  head_label?: string;
}

export interface TangentReferenceEvidence extends EvidenceBase {
  type: "tangent_reference";
  surface_id: string;
  interaction_id?: string;
  revision?: number;
  description?: string;
}

export interface ArtifactRefEvidence extends EvidenceBase {
  type: "artifact_ref";
  authority: string;
  artifact_id: string;
  revision?: string;
  digest?: string;
  media_type?: string;
  logical_kind?: string;
  size_bytes?: number;
  sensitivity?: "public" | "internal" | "confidential" | "restricted";
  retrieval_capability_id?: string;
  expires_at?: string;
  retention_policy?: string;
  safe_preview_artifact_id?: string;
}

export type HITLEvidence =
  | MarkdownEvidence
  | TextEvidence
  | DiffEvidence
  | TangentReferenceEvidence
  | ArtifactRefEvidence;

export interface NormalizedEvidence {
  index: number;
  evidence?: HITLEvidence;
  issue?: string;
}

export interface TangentReferenceView {
  status: "available" | "expired" | "revision_mismatch";
  label: string;
  description?: string;
  requested_revision?: number;
  surface: {
    surface_id: string;
    state: string;
    revision: number;
    updated_at: string;
  };
  interaction?: {
    interaction_id: string;
    state: string;
    revision: number;
    definition_kind: string;
    definition_version: string;
    request_snapshot?: unknown;
    request_omitted?: boolean;
    updated_at: string;
  };
  read_only_url?: string;
}

export interface ArtifactPreview {
  status: "available";
  kind: "text" | "external_link";
  media_type?: string;
  content?: string;
  external_url?: string;
  notice?: string;
}

export interface EvidenceAPIError {
  code: string;
  message: string;
}

export function normalizeEvidence(values: unknown): NormalizedEvidence[] {
  if (!Array.isArray(values)) return [];
  return values.slice(0, HITL_EVIDENCE_LIMITS.items).map((value, index) => {
    if (!isRecord(value) || typeof value.type !== "string" || !validLabel(value.label)) {
      return { index, issue: "This evidence entry is malformed and cannot be displayed." };
    }
    switch (value.type) {
      case "markdown":
      case "text":
      case "diff":
        if (typeof value.content !== "string") {
          return { index, issue: "This inline evidence has no readable content." };
        }
        return { index, evidence: value as unknown as HITLEvidence };
      case "tangent_reference":
        if (typeof value.surface_id !== "string") {
          return { index, issue: "This Tangent reference has no durable surface identifier." };
        }
        return { index, evidence: value as unknown as TangentReferenceEvidence };
      case "artifact_ref":
        if (typeof value.authority !== "string" || typeof value.artifact_id !== "string") {
          return { index, issue: "This artifact reference is missing durable metadata." };
        }
        return { index, evidence: value as unknown as ArtifactRefEvidence };
      default:
        return {
          index,
          issue: `Evidence type “${safeInlineString(value.type)}” is not supported by this Tangent version.`,
        };
    }
  });
}

export function safeHTTPSURL(value: string | undefined): string | undefined {
  if (!value) return undefined;
  try {
    const parsed = new URL(value);
    if (parsed.protocol !== "https:" || !parsed.hostname || parsed.username || parsed.password) {
      return undefined;
    }
    return parsed.toString();
  } catch {
    return undefined;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function validLabel(value: unknown): value is string {
  return typeof value === "string" && value.length > 0 && value.length <= 160;
}

function safeInlineString(value: string): string {
  return boundedText(value, 48).text.replaceAll("\n", " ");
}
