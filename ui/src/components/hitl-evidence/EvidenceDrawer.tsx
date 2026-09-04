import {
  AlertTriangle,
  Archive,
  BookOpenText,
  ExternalLink,
  FileText,
  GitCompareArrows,
  LockKeyhole,
  RefreshCw,
  X,
} from "lucide-react";
import { Dialog } from "radix-ui";
import { useCallback, useEffect, useMemo, useState } from "react";

import { cn } from "@/lib/utils";

import { EvidenceRequestError, fetchArtifactPreview, fetchTangentReference } from "./api";
import { DiffEvidence } from "./DiffEvidence";
import { MarkdownEvidence } from "./MarkdownEvidence";
import {
  type ArtifactPreview,
  type ArtifactRefEvidence,
  boundedText,
  HITL_EVIDENCE_LIMITS,
  type HITLEvidence,
  normalizeEvidence,
  safeHTTPSURL,
  type TangentReferenceEvidence,
  type TangentReferenceView,
} from "./types";

interface EvidenceDrawerProps {
  itemID: string;
  itemTitle: string;
  evidence: unknown;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function EvidenceDrawer({
  itemID,
  itemTitle,
  evidence,
  open,
  onOpenChange,
}: EvidenceDrawerProps) {
  const entries = useMemo(() => normalizeEvidence(evidence), [evidence]);
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-[#050608]/80" />
        <Dialog.Content className="fixed inset-y-0 right-0 z-50 flex w-full flex-col border-l border-[#343840] bg-[#0d0f12] text-[#d7dce2] shadow-[-24px_0_70px_rgba(0,0,0,0.55)] outline-none sm:max-w-[46rem]">
          <header className="shrink-0 border-b border-[#30343b] px-5 py-5 sm:px-7">
            <div className="flex items-start justify-between gap-5">
              <div className="min-w-0">
                <p className="flex items-center gap-2 font-mono text-[10px] uppercase tracking-[0.16em] text-[#f2b84b]">
                  <LockKeyhole className="size-3" aria-hidden="true" /> Read-only case file
                </p>
                <Dialog.Title className="mt-2 text-xl font-semibold tracking-[-0.025em] text-[#f2f4f6]">
                  Evidence
                </Dialog.Title>
                <Dialog.Description className="mt-1 truncate text-sm text-[#8e959f]">
                  {itemTitle} · {entries.length} {entries.length === 1 ? "record" : "records"}
                </Dialog.Description>
              </div>
              <Dialog.Close asChild>
                <button
                  type="button"
                  className="-mr-2 shrink-0 p-2 text-[#a5abb4] outline-none hover:bg-[#1b1e23] hover:text-white focus-visible:ring-2 focus-visible:ring-[#f2b84b]"
                  aria-label="Close evidence"
                >
                  <X className="size-5" aria-hidden="true" />
                </button>
              </Dialog.Close>
            </div>
            <p className="mt-4 border-l-2 border-[#57606b] pl-3 text-xs leading-5 text-[#9ca3ad]">
              Inspecting these durable records cannot resolve, cancel, disconnect, or change FIFO
              order. Links and identifiers carry no ambient action authority.
            </p>
          </header>

          <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-5 py-2 sm:px-7">
            {entries.length === 0 ? (
              <div className="py-16 text-center">
                <Archive className="mx-auto size-7 text-[#69717b]" aria-hidden="true" />
                <p className="mt-4 text-sm font-medium text-[#c5cad1]">No evidence attached</p>
                <p className="mx-auto mt-1 max-w-sm text-xs leading-5 text-[#8e959f]">
                  The request remains fully actionable; there are no supporting records to inspect.
                </p>
              </div>
            ) : (
              <ol className="m-0 list-none divide-y divide-[#30343b] p-0">
                {entries.map((entry) => (
                  <li key={entry.index} className="py-7">
                    {entry.evidence ? (
                      <EvidenceRecord
                        itemID={itemID}
                        index={entry.index}
                        evidence={entry.evidence}
                      />
                    ) : (
                      <InlineEvidenceState
                        title={`Evidence ${entry.index + 1} unavailable`}
                        message={entry.issue || "This evidence entry cannot be displayed."}
                        tone="warning"
                      />
                    )}
                  </li>
                ))}
              </ol>
            )}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

function EvidenceRecord({
  itemID,
  index,
  evidence,
}: {
  itemID: string;
  index: number;
  evidence: HITLEvidence;
}) {
  const icon = evidenceIcon(evidence.type);
  return (
    <section aria-labelledby={`hitl-evidence-${itemID}-${index}`}>
      <div className="mb-4 flex items-start justify-between gap-4">
        <div className="min-w-0">
          <p className="flex items-center gap-2 font-mono text-[9px] uppercase tracking-[0.15em] text-[#8e959f]">
            {icon} {evidenceTypeLabel(evidence.type)} · {String(index + 1).padStart(2, "0")}
          </p>
          <h3
            id={`hitl-evidence-${itemID}-${index}`}
            className="mt-1 break-words text-base font-semibold text-[#eef0f2] [overflow-wrap:anywhere]"
          >
            {evidence.label}
          </h3>
        </div>
      </div>
      {evidence.type === "markdown" ? <MarkdownEvidence content={evidence.content} /> : null}
      {evidence.type === "text" ? <PlainTextEvidence evidence={evidence} /> : null}
      {evidence.type === "diff" ? (
        <DiffEvidence
          content={evidence.content}
          label={evidence.label}
          baseLabel={evidence.base_label}
          headLabel={evidence.head_label}
        />
      ) : null}
      {evidence.type === "tangent_reference" ? (
        <TangentReferenceRecord itemID={itemID} index={index} evidence={evidence} />
      ) : null}
      {evidence.type === "artifact_ref" ? (
        <ArtifactReferenceRecord itemID={itemID} index={index} evidence={evidence} />
      ) : null}
    </section>
  );
}

function PlainTextEvidence({ evidence }: { evidence: Extract<HITLEvidence, { type: "text" }> }) {
  const bounded = boundedText(evidence.content, HITL_EVIDENCE_LIMITS.inlineText);
  return (
    <div>
      {evidence.language ? (
        <p className="mb-1 font-mono text-[10px] uppercase tracking-[0.13em] text-[#8e959f]">
          {evidence.language}
        </p>
      ) : null}
      <pre
        className={cn(
          "max-h-[28rem] overflow-auto whitespace-pre-wrap break-words border-l-2 px-4 py-3 text-sm leading-6 [overflow-wrap:anywhere]",
          evidence.language
            ? "border-[#57606b] bg-[#0b0d10] font-mono text-xs text-[#cbd1d8]"
            : "border-[#30343b] bg-[#111317] font-sans text-[#c5cad1]",
        )}
      >
        {bounded.text}
      </pre>
      {bounded.truncated ? (
        <p role="status" className="mt-2 text-xs text-[#d5b486]">
          Content stopped at Tangent’s 64 KiB inline evidence limit.
        </p>
      ) : null}
    </div>
  );
}

function TangentReferenceRecord({
  itemID,
  index,
  evidence,
}: {
  itemID: string;
  index: number;
  evidence: TangentReferenceEvidence;
}) {
  const [attempt, setAttempt] = useState(0);
  const [view, setView] = useState<TangentReferenceView | null>(null);
  const [error, setError] = useState<EvidenceRequestError | null>(null);
  useEffect(() => {
    void attempt;
    const controller = new AbortController();
    setView(null);
    setError(null);
    fetchTangentReference(itemID, index, controller.signal)
      .then(setView)
      .catch((reason) => {
        if (reason instanceof DOMException && reason.name === "AbortError") return;
        setError(
          reason instanceof EvidenceRequestError
            ? reason
            : new EvidenceRequestError(0, {
                message: "The durable Tangent reference could not be loaded.",
              }),
        );
      });
    return () => controller.abort();
  }, [attempt, index, itemID]);

  if (error) {
    return (
      <InlineEvidenceState
        title={evidenceStateTitle(error.code)}
        message={error.message}
        tone={error.code === "evidence_unauthorized" ? "danger" : "warning"}
        onRetry={() => setAttempt((value) => value + 1)}
      />
    );
  }
  if (!view) return <EvidenceLoading label="Loading durable Tangent record" />;
  return <TangentReferenceProjection evidence={evidence} view={view} />;
}

function TangentReferenceProjection({
  evidence,
  view,
}: {
  evidence: TangentReferenceEvidence;
  view: TangentReferenceView;
}) {
  const readOnlyURL = safeReadOnlyHITLURL(view.read_only_url);
  return (
    <div className="border-l-2 border-[#6e8fb3] bg-[#10151b] px-4 py-4">
      {evidence.description ? (
        <p className="mb-4 text-sm leading-6 text-[#b8bec6]">{evidence.description}</p>
      ) : null}
      {view.status === "revision_mismatch" ? (
        <p role="status" className="mb-4 text-xs leading-5 text-[#d5b486]">
          The reference requested revision {view.requested_revision}; the durable record is now
          revision {view.interaction?.revision}. The current read-only record is shown below.
        </p>
      ) : null}
      {view.status === "expired" ? (
        <p role="status" className="mb-4 text-xs leading-5 text-[#d5b486]">
          This surface is expired, but its retained durable record remains readable.
        </p>
      ) : null}
      <dl className="grid gap-x-5 gap-y-3 text-xs sm:grid-cols-2">
        <EvidenceMetadata term="Surface" value={view.surface.surface_id} mono />
        <EvidenceMetadata
          term="Surface state"
          value={`${view.surface.state} · rev ${view.surface.revision}`}
        />
        {view.interaction ? (
          <>
            <EvidenceMetadata term="Interaction" value={view.interaction.interaction_id} mono />
            <EvidenceMetadata
              term="Interaction state"
              value={`${view.interaction.state} · rev ${view.interaction.revision}`}
            />
            <EvidenceMetadata
              term="Definition"
              value={`${view.interaction.definition_kind}@${view.interaction.definition_version}`}
              mono
            />
            <EvidenceMetadata term="Updated" value={formatDate(view.interaction.updated_at)} />
          </>
        ) : null}
      </dl>
      {view.interaction?.request_snapshot !== undefined ? (
        <details className="mt-4 border-t border-[#2c3540] pt-3">
          <summary className="cursor-pointer text-xs font-semibold text-[#b8cbe0] outline-none focus-visible:ring-2 focus-visible:ring-[#f2b84b]">
            Durable request snapshot
          </summary>
          <pre className="mt-3 max-h-64 overflow-auto whitespace-pre-wrap break-words bg-[#0b0d10] p-3 font-mono text-[11px] leading-5 text-[#aeb8c4] [overflow-wrap:anywhere]">
            {formatJSON(view.interaction.request_snapshot)}
          </pre>
        </details>
      ) : null}
      {view.interaction?.request_omitted ? (
        <p className="mt-4 text-xs text-[#d5b486]">
          The referenced request exceeds the safe inline display boundary.
        </p>
      ) : null}
      {readOnlyURL ? (
        <a
          href={readOnlyURL}
          className="mt-4 inline-flex items-center gap-2 text-xs font-semibold text-[#a9c7e5] underline decoration-[#526a81] underline-offset-4 outline-none hover:text-white focus-visible:ring-2 focus-visible:ring-[#f2b84b]"
        >
          Open durable inbox record <ExternalLink className="size-3" aria-hidden="true" />
        </a>
      ) : (
        <p className="mt-4 text-[11px] leading-5 text-[#8e959f]">
          No separate room connection is opened for this reference.
        </p>
      )}
    </div>
  );
}

function ArtifactReferenceRecord({
  itemID,
  index,
  evidence,
}: {
  itemID: string;
  index: number;
  evidence: ArtifactRefEvidence;
}) {
  const [preview, setPreview] = useState<ArtifactPreview | null>(null);
  const [error, setError] = useState<EvidenceRequestError | null>(null);
  const [loading, setLoading] = useState(false);
  const loadPreview = useCallback(() => {
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    fetchArtifactPreview(itemID, index, controller.signal)
      .then(setPreview)
      .catch((reason) =>
        setError(
          reason instanceof EvidenceRequestError
            ? reason
            : new EvidenceRequestError(0, { message: "The artifact preview could not be loaded." }),
        ),
      )
      .finally(() => setLoading(false));
    return () => controller.abort();
  }, [index, itemID]);

  const externalURL =
    preview?.kind === "external_link" ? safeHTTPSURL(preview.external_url) : undefined;
  return (
    <div className="border-l-2 border-[#57606b] pl-4">
      <dl className="grid gap-x-5 gap-y-3 text-xs sm:grid-cols-2">
        <EvidenceMetadata term="Authority" value={evidence.authority} mono />
        <EvidenceMetadata term="Artifact" value={evidence.artifact_id} mono />
        {evidence.revision ? (
          <EvidenceMetadata term="Revision" value={evidence.revision} mono />
        ) : null}
        {evidence.digest ? <EvidenceMetadata term="Digest" value={evidence.digest} mono /> : null}
        {evidence.logical_kind ? (
          <EvidenceMetadata term="Kind" value={evidence.logical_kind} />
        ) : null}
        {evidence.media_type ? (
          <EvidenceMetadata term="Media type" value={evidence.media_type} mono />
        ) : null}
        {evidence.size_bytes !== undefined ? (
          <EvidenceMetadata term="Size" value={formatBytes(evidence.size_bytes)} />
        ) : null}
        {evidence.sensitivity ? (
          <EvidenceMetadata term="Sensitivity" value={evidence.sensitivity} />
        ) : null}
        {evidence.retention_policy ? (
          <EvidenceMetadata term="Retention" value={evidence.retention_policy} />
        ) : null}
        {evidence.expires_at ? (
          <EvidenceMetadata term="Preview expires" value={formatDate(evidence.expires_at)} />
        ) : null}
      </dl>
      <div className="mt-4 border-t border-[#292c32] pt-4">
        {evidence.retrieval_capability_id ? (
          <button
            type="button"
            onClick={loadPreview}
            disabled={loading}
            className="inline-flex min-h-9 items-center gap-2 border border-[#52606d] bg-[#15191e] px-3 py-2 text-xs font-semibold text-[#cbd3dc] outline-none hover:border-[#788898] hover:text-white focus-visible:ring-2 focus-visible:ring-[#f2b84b] disabled:cursor-wait disabled:opacity-50"
          >
            {loading ? (
              <RefreshCw
                className="size-3 animate-spin motion-reduce:animate-none"
                aria-hidden="true"
              />
            ) : (
              <BookOpenText className="size-3" aria-hidden="true" />
            )}
            {loading ? "Checking host capability…" : "Request safe preview"}
          </button>
        ) : (
          <p className="text-xs leading-5 text-[#8e959f]">
            Metadata only. The request declared no preview capability.
          </p>
        )}
        {error ? (
          <div className="mt-3">
            <InlineEvidenceState
              title={evidenceStateTitle(error.code)}
              message={error.message}
              tone="warning"
            />
          </div>
        ) : null}
        {preview?.kind === "text" ? (
          <pre className="mt-3 max-h-72 overflow-auto whitespace-pre-wrap break-words bg-[#0b0d10] p-3 font-mono text-[11px] leading-5 text-[#c5cad1] [overflow-wrap:anywhere]">
            {boundedText(preview.content || "", HITL_EVIDENCE_LIMITS.inlineText).text}
          </pre>
        ) : null}
        {externalURL ? (
          <div className="mt-3 border-l-2 border-[#f2b84b] pl-3">
            <p className="text-xs leading-5 text-[#aeb4bc]">
              The host adapter offers no inline preview. Opening the owning system is an explicit
              external action.
            </p>
            <a
              href={externalURL}
              target="_blank"
              rel="noreferrer noopener"
              className="mt-2 inline-flex items-center gap-2 text-xs font-semibold text-[#e6c47f] underline underline-offset-4 outline-none hover:text-white focus-visible:ring-2 focus-visible:ring-[#f2b84b]"
            >
              Open in owning system <ExternalLink className="size-3" aria-hidden="true" />
            </a>
          </div>
        ) : null}
        {preview?.kind === "external_link" && !externalURL ? (
          <InlineEvidenceState
            title="External link withheld"
            message="The adapter returned a destination outside Tangent’s HTTPS-only link policy."
            tone="danger"
          />
        ) : null}
      </div>
    </div>
  );
}

function InlineEvidenceState({
  title,
  message,
  tone,
  onRetry,
}: {
  title: string;
  message: string;
  tone: "warning" | "danger";
  onRetry?: () => void;
}) {
  return (
    <div
      role="status"
      className={cn(
        "border-l-2 px-4 py-3",
        tone === "danger" ? "border-[#d96b67] bg-[#1a1213]" : "border-[#b17c3c] bg-[#17130d]",
      )}
    >
      <p className="flex items-center gap-2 text-xs font-semibold text-[#e3e6e9]">
        <AlertTriangle className="size-3.5" aria-hidden="true" /> {title}
      </p>
      <p className="mt-1 text-xs leading-5 text-[#aeb4bc]">{message}</p>
      {onRetry ? (
        <button
          type="button"
          onClick={onRetry}
          className="mt-2 inline-flex items-center gap-1.5 text-xs font-semibold text-[#d5b486] outline-none hover:text-white focus-visible:ring-2 focus-visible:ring-[#f2b84b]"
        >
          <RefreshCw className="size-3" aria-hidden="true" /> Retry durable read
        </button>
      ) : null}
    </div>
  );
}

function EvidenceLoading({ label }: { label: string }) {
  return (
    <div role="status" className="flex items-center gap-2 text-xs text-[#8e959f]">
      <RefreshCw className="size-3 animate-spin motion-reduce:animate-none" aria-hidden="true" />{" "}
      {label}
    </div>
  );
}

function EvidenceMetadata({
  term,
  value,
  mono = false,
}: {
  term: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="min-w-0">
      <dt className="text-[#8e959f]">{term}</dt>
      <dd
        className={cn(
          "mt-0.5 break-words text-[#c5cad1] [overflow-wrap:anywhere]",
          mono && "font-mono text-[11px]",
        )}
      >
        {value}
      </dd>
    </div>
  );
}

function evidenceIcon(type: HITLEvidence["type"]) {
  const className = "size-3";
  if (type === "diff") return <GitCompareArrows className={className} aria-hidden="true" />;
  if (type === "artifact_ref") return <Archive className={className} aria-hidden="true" />;
  if (type === "tangent_reference")
    return <BookOpenText className={className} aria-hidden="true" />;
  return <FileText className={className} aria-hidden="true" />;
}

function evidenceTypeLabel(type: HITLEvidence["type"]): string {
  return type.replaceAll("_", " ");
}

function evidenceStateTitle(code: string): string {
  switch (code) {
    case "evidence_missing":
      return "Reference missing";
    case "evidence_expired":
      return "Preview expired";
    case "evidence_unauthorized":
      return "Preview not authorized";
    case "evidence_unsupported":
      return "Preview unsupported";
    case "evidence_too_large":
      return "Preview too large";
    default:
      return "Evidence unavailable";
  }
}

function formatJSON(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return "The durable request snapshot could not be formatted.";
  }
}

function formatDate(value: string): string {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? "Unknown" : parsed.toLocaleString();
}

function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value < 0) return "Unknown";
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
  return `${(value / (1024 * 1024)).toFixed(1)} MiB`;
}

function safeReadOnlyHITLURL(value: string | undefined): string | undefined {
  return value && /^\/hitl\/items\/[A-Za-z0-9._~%@+-]+$/.test(value) ? value : undefined;
}
