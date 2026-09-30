import { Button, Callout, EmptyState, LiveDot } from "@hollis-labs/design-components";
import { ArrowLeft, BookOpenText, Check, Link2, RefreshCw, ShieldCheck, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";

import {
  AttentionActions,
  type AttentionComposerIntent,
} from "@/components/hitl-attention/AttentionActions";
import { EvidenceDrawer } from "@/components/hitl-evidence";
import { PageShell } from "@/components/layout/PageShell";
import { Markdown } from "@/components/markdown";
import {
  fetchHITLInbox,
  fetchHITLItem,
  getHITLConnectionID,
  HITLAPIError,
  type HITLApprovalLabels,
  type HITLAttentionLabels,
  type HITLExternalRef,
  type HITLInbox,
  type HITLOperatorItem,
  presentHITLItem,
  resolveHITLApproval,
  resolveHITLAttention,
} from "@/lib/hitl-api";
import { cn } from "@/lib/utils";

type InboxView = "pending" | "history";
type KindFilter = "all" | "approval" | "attention";
type ComposerIntent = "approved" | "denied" | "attention-note" | "attention-reply" | null;
type PostDispositionHandoff = {
  resolvedItemID: string;
  resolvedQueueSequence: number;
  targetItemID?: string;
};

const terminalStates = new Set(["resolved", "canceled", "expired", "failed", "superseded"]);

export default function HITLInboxRoute() {
  const { itemID } = useParams<{ itemID?: string }>();
  const navigate = useNavigate();
  const [inbox, setInbox] = useState<HITLInbox | null>(null);
  const [detachedItem, setDetachedItem] = useState<HITLOperatorItem | null>(null);
  const [view, setView] = useState<InboxView>("pending");
  const [kindFilter, setKindFilter] = useState<KindFilter>("all");
  const [error, setError] = useState("");
  const [syncState, setSyncState] = useState("Connecting to durable inbox…");
  const [conflict, setConflict] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [noteIntent, setNoteIntent] = useState<ComposerIntent>(null);
  const [note, setNote] = useState("");
  const [noteError, setNoteError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [postDisposition, setPostDisposition] = useState<PostDispositionHandoff | null>(null);
  const [presentingID, setPresentingID] = useState("");
  const [evidenceOpen, setEvidenceOpen] = useState(false);
  const syncSequence = useRef(0);
  const activeItemID = useRef(itemID);
  const detailHeading = useRef<HTMLHeadingElement>(null);
  const focusedItemID = useRef("");
  const keyboardSelectionID = useRef("");
  const queueButtons = useRef(new Map<string, HTMLButtonElement>());
  const presentingItems = useRef(new Set<string>());
  const evidenceTrigger = useRef<HTMLButtonElement>(null);
  activeItemID.current = itemID;

  const syncInbox = useCallback(async () => {
    const sequence = ++syncSequence.current;
    const requestedItemID = activeItemID.current;
    try {
      const next = await fetchHITLInbox();
      let directItem: HITLOperatorItem | null = null;
      if (
        requestedItemID &&
        !next.pending.some((item) => item.item_id === requestedItemID) &&
        !next.history.some((item) => item.item_id === requestedItemID)
      ) {
        try {
          directItem = await fetchHITLItem(requestedItemID);
        } catch (reason) {
          if (!(reason instanceof HITLAPIError) || reason.status !== 404) throw reason;
        }
      }
      if (sequence !== syncSequence.current) return;
      setInbox(next);
      setDetachedItem(directItem);
      setError("");
      setSyncState(`Synced ${formatSyncTime(next.synced_at)}`);
    } catch (reason) {
      if (sequence !== syncSequence.current) return;
      const message = humanizeError(reason);
      setError(message);
      setSyncState("Durable state unavailable — retrying");
    }
  }, []);

  useEffect(() => {
    void itemID;
    void syncInbox();
  }, [itemID, syncInbox]);

  useEffect(() => {
    const events = new EventSource("/api/hitl/events");
    const handleRevision = () => void syncInbox();
    events.addEventListener("revision", handleRevision);
    events.onopen = () =>
      setSyncState((current) =>
        current.startsWith("Synced") ? current : "Connected — resynchronizing",
      );
    events.onerror = () => setSyncState("Connection interrupted — durable state preserved");
    return () => {
      events.removeEventListener("revision", handleRevision);
      events.close();
    };
  }, [syncInbox]);

  const allItems = useMemo(
    () => [
      ...(inbox?.pending ?? []),
      ...(inbox?.history ?? []),
      ...(detachedItem ? [detachedItem] : []),
    ],
    [detachedItem, inbox],
  );
  const selected = itemID ? allItems.find((item) => item.item_id === itemID) : undefined;
  const selectedItemID = selected?.item_id;
  const selectedState = selected?.state;
  const projectedItems = view === "pending" ? (inbox?.pending ?? []) : (inbox?.history ?? []);
  const visibleItems = useMemo(
    () =>
      kindFilter === "all"
        ? projectedItems
        : projectedItems.filter((item) => item.request_snapshot.kind === kindFilter),
    [kindFilter, projectedItems],
  );

  useEffect(() => {
    if (!selectedState || submitting || postDisposition) return;
    setView(terminalStates.has(selectedState) ? "history" : "pending");
  }, [postDisposition, selectedState, submitting]);

  useEffect(() => {
    if (
      postDisposition &&
      itemID !== postDisposition.resolvedItemID &&
      itemID !== postDisposition.targetItemID
    ) {
      setPostDisposition(null);
      return;
    }
    if (
      !inbox ||
      !postDisposition ||
      postDisposition.targetItemID ||
      inbox.pending.some((item) => item.item_id === postDisposition.resolvedItemID)
    ) {
      return;
    }

    const target =
      inbox.pending.find((item) => item.queue_sequence > postDisposition.resolvedQueueSequence) ??
      inbox.pending[0] ??
      inbox.history.find((item) => item.item_id === postDisposition.resolvedItemID);
    if (!target) return;

    const targetView: InboxView = inbox.pending.length > 0 ? "pending" : "history";
    setView(targetView);
    setConflict("");
    setNoteIntent(null);
    setNote("");
    setEvidenceOpen(false);
    keyboardSelectionID.current = "";
    setPostDisposition((current) =>
      current?.resolvedItemID === postDisposition.resolvedItemID
        ? { ...current, targetItemID: target.item_id }
        : current,
    );
    navigate(`/hitl/items/${encodeURIComponent(target.item_id)}`, { replace: true });
  }, [inbox, itemID, navigate, postDisposition]);

  useEffect(() => {
    void selectedItemID;
    setEvidenceOpen(false);
  }, [selectedItemID]);

  useEffect(() => {
    const handoffTargeted = postDisposition?.targetItemID === selectedItemID;
    if (!selectedItemID || (!handoffTargeted && focusedItemID.current === selectedItemID)) return;
    focusedItemID.current = selectedItemID;
    if (handoffTargeted) {
      keyboardSelectionID.current = "";
      const visibleQueueButton = queueButtons.current.get(selectedItemID);
      if (queueIsVisible() && visibleQueueButton) {
        visibleQueueButton.focus({ preventScroll: true });
      } else {
        detailHeading.current?.focus({ preventScroll: true });
      }
      setPostDisposition((current) => (current?.targetItemID === selectedItemID ? null : current));
      return;
    }
    if (keyboardSelectionID.current === selectedItemID && queueIsVisible()) {
      keyboardSelectionID.current = "";
      queueButtons.current.get(selectedItemID)?.focus({ preventScroll: true });
      return;
    }
    keyboardSelectionID.current = "";
    detailHeading.current?.focus({ preventScroll: true });
  }, [postDisposition, selectedItemID]);

  useEffect(() => {
    if (selected?.state !== "staged" || presentingItems.current.has(selected.item_id)) {
      return;
    }
    const stagedItem = selected;
    presentingItems.current.add(stagedItem.item_id);
    setPresentingID(stagedItem.item_id);
    presentHITLItem(stagedItem, getHITLConnectionID())
      .then((presented) => {
        patchInboxItem(setInbox, presented);
        setDetachedItem((current) =>
          current?.item_id === presented.item_id ? presented : current,
        );
        setConflict("");
      })
      .catch(async (reason) => {
        if (reason instanceof HITLAPIError && reason.status === 409) {
          setConflict(staleConflictMessage(reason));
          await syncInbox();
        } else {
          setError(humanizeError(reason));
        }
      })
      .finally(() => {
        presentingItems.current.delete(stagedItem.item_id);
        setPresentingID((current) => (current === stagedItem.item_id ? "" : current));
      });
  }, [selected, syncInbox]);

  const chooseItem = (next: HITLOperatorItem, keyboard = false) => {
    setPostDisposition(null);
    keyboardSelectionID.current = keyboard ? next.item_id : "";
    setConflict("");
    setConfirmation("");
    setNoteIntent(null);
    setNote("");
    setEvidenceOpen(false);
    navigate(`/hitl/items/${encodeURIComponent(next.item_id)}`);
  };

  const moveSelection = (currentItemID: string, key: "ArrowDown" | "ArrowUp" | "Home" | "End") => {
    if (visibleItems.length === 0) return;
    const current = visibleItems.findIndex((item) => item.item_id === currentItemID);
    let nextIndex = current < 0 ? 0 : current;
    if (key === "ArrowDown") nextIndex++;
    if (key === "ArrowUp") nextIndex--;
    if (key === "Home") nextIndex = 0;
    if (key === "End") nextIndex = visibleItems.length - 1;
    nextIndex = Math.min(visibleItems.length - 1, Math.max(0, nextIndex));
    chooseItem(visibleItems[nextIndex], true);
  };

  const decide = async (decision: "approved" | "denied", decisionNote?: string) => {
    if (!selected || submitting || postDisposition) return;
    const cleanNote = decisionNote?.trim();
    if (decisionNote !== undefined && !cleanNote) {
      setNoteError("Write a note before submitting this variant.");
      return;
    }
    setSubmitting(true);
    setConflict("");
    setNoteError("");
    try {
      await resolveHITLApproval(selected, decision, cleanNote);
      setConfirmation(
        `${decision === "approved" ? "Approved" : "Denied"}: ${selected.request_snapshot.title}. Decision committed.`,
      );
      setNoteIntent(null);
      setNote("");
      if (activeItemID.current === selected.item_id) {
        setPostDisposition({
          resolvedItemID: selected.item_id,
          resolvedQueueSequence: selected.queue_sequence,
        });
      }
      await syncInbox();
    } catch (reason) {
      if (reason instanceof HITLAPIError && reason.status === 409) {
        const message = staleConflictMessage(reason);
        if (activeItemID.current === selected.item_id) setConflict(message);
        else setConfirmation(message);
        await syncInbox();
      } else {
        setError(humanizeError(reason));
      }
    } finally {
      setSubmitting(false);
    }
  };

  const acknowledge = async (field?: "note" | "reply", value?: string) => {
    if (!selected || submitting || postDisposition) return;
    const cleanValue = value?.trim();
    if (field && !cleanValue) {
      setNoteError(
        field === "reply" ? "Write a reply before submitting." : "Write a note before submitting.",
      );
      return;
    }
    setSubmitting(true);
    setConflict("");
    setNoteError("");
    try {
      await resolveHITLAttention(
        selected,
        field && cleanValue ? { [field]: cleanValue } : undefined,
      );
      setConfirmation(
        `Acknowledged: ${selected.request_snapshot.title}. Tangent recorded receipt only; downstream action remains caller-owned.`,
      );
      setNoteIntent(null);
      setNote("");
      if (activeItemID.current === selected.item_id) {
        setPostDisposition({
          resolvedItemID: selected.item_id,
          resolvedQueueSequence: selected.queue_sequence,
        });
      }
      await syncInbox();
    } catch (reason) {
      if (reason instanceof HITLAPIError && reason.status === 409) {
        const message = staleConflictMessage(reason);
        if (activeItemID.current === selected.item_id) setConflict(message);
        else setConfirmation(message);
        await syncInbox();
      } else {
        setError(humanizeError(reason));
      }
    } finally {
      setSubmitting(false);
    }
  };

  if (!inbox && error) {
    return <InboxFailure message={error} onRetry={() => void syncInbox()} />;
  }

  return (
    <PageShell as="main" className="overflow-y-auto text-fg-secondary">
      <header className="border-b border-border-subtle px-4 py-5 sm:px-6 lg:px-8">
        <div className="mx-auto flex max-w-[92rem] flex-col justify-between gap-4 sm:flex-row sm:items-end">
          <div>
            <div className="mb-2 flex items-center gap-2 text-label font-semibold uppercase tracking-label text-primary">
              <ShieldCheck className="size-3.5" aria-hidden="true" />
              Operator-owned surface
            </div>
            <h1 className="text-2xl font-semibold tracking-tight text-fg">Approvals</h1>
            <p className="mt-1 max-w-xl text-sm leading-6 text-fg-muted">
              Oldest requests stay first. Looking never changes their order; decisions commit one
              item at a time.
            </p>
          </div>
          <div className="flex items-center gap-2 text-xs text-fg-muted" aria-live="polite">
            <LiveDot
              tone={
                syncState.startsWith("Synced") || syncState.startsWith("Connected")
                  ? "success"
                  : "warning"
              }
              pulsing={!syncState.startsWith("Synced")}
            />
            {syncState}
            <button
              type="button"
              onClick={() => void syncInbox()}
              className="ml-1 rounded-sm p-2 text-fg-secondary outline-none hover:bg-surface hover:text-fg focus-visible:ring-2 focus-visible:ring-primary"
              aria-label="Resynchronize inbox"
            >
              <RefreshCw className="size-3.5" aria-hidden="true" />
            </button>
          </div>
        </div>
      </header>
      {error ? (
        <div className="px-4 py-3 sm:px-6 lg:px-8">
          <div className="mx-auto max-w-[92rem]">
            <Callout
              tone="danger"
              actions={
                <Button variant="outline" size="sm" onClick={() => void syncInbox()}>
                  Retry
                </Button>
              }
            >
              {error}
            </Callout>
          </div>
        </div>
      ) : null}

      <div className="mx-auto max-w-[92rem] lg:grid lg:min-h-[calc(100vh-186px)] lg:grid-cols-[23rem_minmax(0,1fr)]">
        <section
          className={cn("border-border-subtle lg:border-r", itemID ? "hidden lg:block" : "block")}
          aria-label="HITL queue"
        >
          <div className="flex items-end justify-between border-b border-border-subtle px-4 pt-4 sm:px-6">
            <nav className="flex gap-5" aria-label="Inbox views">
              <InboxTab
                active={view === "pending"}
                count={inbox?.pending.length ?? 0}
                label="Pending"
                onClick={() => setView("pending")}
              />
              <InboxTab
                active={view === "history"}
                count={inbox?.history.length ?? 0}
                label="Resolved"
                onClick={() => setView("history")}
              />
            </nav>
            <span className="pb-3 font-mono text-caption uppercase tracking-label text-fg-muted">
              {view === "pending" ? "FIFO" : "Newest"}
            </span>
          </div>

          <nav
            className="flex items-center gap-1 border-b border-divider bg-bg-elevated px-4 py-2 sm:px-6"
            aria-label={`Filter ${view} requests by kind`}
          >
            {(["all", "approval", "attention"] as const).map((kind) => (
              <KindFilterButton
                key={kind}
                active={kindFilter === kind}
                kind={kind}
                count={countKind(projectedItems, kind)}
                onClick={() => setKindFilter(kind)}
              />
            ))}
          </nav>

          <ul
            aria-label={
              view === "pending" ? "Pending requests, oldest first" : "Resolved request history"
            }
            className="m-0 list-none p-0 outline-none"
          >
            {!inbox ? (
              <li>
                <QueueSkeleton />
              </li>
            ) : visibleItems.length === 0 ? (
              <li>
                <QueueEmpty view={view} kindFilter={kindFilter} />
              </li>
            ) : (
              visibleItems.map((item) => (
                <QueueRow
                  key={item.item_id}
                  item={item}
                  ordinal={item.queue_sequence}
                  selected={item.item_id === itemID}
                  onClick={() => chooseItem(item)}
                  onMove={(key) => moveSelection(item.item_id, key)}
                  buttonRef={(node) => {
                    if (node) queueButtons.current.set(item.item_id, node);
                    else queueButtons.current.delete(item.item_id);
                  }}
                />
              ))
            )}
          </ul>
        </section>

        <section
          className={cn("min-w-0", itemID ? "block" : "hidden lg:block")}
          aria-label="Selected request"
        >
          {selected ? (
            <ItemDetail
              item={selected}
              headingRef={detailHeading}
              presenting={presentingID === selected.item_id}
              submitting={submitting || Boolean(postDisposition)}
              conflict={conflict}
              note={note}
              noteError={noteError}
              noteIntent={noteIntent}
              onBack={() => navigate("/hitl")}
              onDecision={(decision) => void decide(decision)}
              onNoteChange={(value) => {
                setNote(value);
                if (value.trim()) setNoteError("");
              }}
              onNoteIntent={(intent) => {
                if (intent !== noteIntent) setNote("");
                setNoteIntent(intent);
                setNoteError("");
              }}
              onSubmitNote={() =>
                noteIntent && (noteIntent === "approved" || noteIntent === "denied")
                  ? void decide(noteIntent, note)
                  : undefined
              }
              onAcknowledge={() => void acknowledge()}
              onSubmitAttention={(field) => void acknowledge(field, note)}
              onOpenEvidence={() => setEvidenceOpen(true)}
              evidenceTrigger={evidenceTrigger}
            />
          ) : itemID && inbox ? (
            <MissingItem itemID={itemID} onBack={() => navigate("/hitl")} />
          ) : (
            <NoSelection pendingCount={inbox?.pending.length ?? 0} />
          )}
        </section>
      </div>
      {selected ? (
        <EvidenceDrawer
          itemID={selected.item_id}
          itemTitle={selected.request_snapshot.title}
          evidence={selected.request_snapshot.evidence}
          open={evidenceOpen}
          onOpenChange={(open) => {
            setEvidenceOpen(open);
            if (!open) queueMicrotask(() => evidenceTrigger.current?.focus());
          }}
        />
      ) : null}
      <div className="sr-only" role="status" aria-live="polite" aria-atomic="true">
        {confirmation || conflict}
      </div>
    </PageShell>
  );
}

function InboxTab({
  active,
  count,
  label,
  onClick,
}: {
  active: boolean;
  count: number;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "border-b-2 pb-3 text-sm font-medium outline-none focus-visible:ring-2 focus-visible:ring-primary",
        active
          ? "border-primary text-fg"
          : "border-transparent text-fg-muted hover:text-fg-secondary",
      )}
    >
      {label}{" "}
      <span className="ml-1 font-mono text-label text-fg-muted">
        {String(count).padStart(2, "0")}
      </span>
    </button>
  );
}

function KindFilterButton({
  active,
  kind,
  count,
  onClick,
}: {
  active: boolean;
  kind: KindFilter;
  count: number;
  onClick: () => void;
}) {
  const label = kind === "all" ? "All" : kind === "approval" ? "Approvals" : "Attention";
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "min-h-8 border px-2.5 py-1 font-mono text-caption uppercase tracking-label outline-none focus-visible:ring-2 focus-visible:ring-primary",
        active
          ? kind === "attention"
            ? "border-info bg-info-muted text-info"
            : "border-fg-faint bg-surface-hover text-fg"
          : "border-transparent text-fg-muted hover:border-border hover:text-fg-secondary",
      )}
    >
      {label} {String(count).padStart(2, "0")}
    </button>
  );
}

function QueueRow({
  item,
  ordinal,
  selected,
  onClick,
  onMove,
  buttonRef,
}: {
  item: HITLOperatorItem;
  ordinal: number;
  selected: boolean;
  onClick: () => void;
  onMove: (key: "ArrowDown" | "ArrowUp" | "Home" | "End") => void;
  buttonRef: (node: HTMLButtonElement | null) => void;
}) {
  const request = item.request_snapshot;
  return (
    <li>
      <button
        ref={buttonRef}
        type="button"
        aria-current={selected ? "true" : undefined}
        onClick={onClick}
        onKeyDown={(event) => {
          if (
            event.key === "ArrowDown" ||
            event.key === "ArrowUp" ||
            event.key === "Home" ||
            event.key === "End"
          ) {
            event.preventDefault();
            onMove(event.key);
          }
        }}
        className={cn(
          "group relative grid w-full grid-cols-[2.25rem_1fr] gap-3 border-b border-divider px-4 py-4 text-left outline-none transition-colors sm:px-6",
          selected ? "bg-surface" : "bg-bg-elevated hover:bg-bg-elevated",
          "focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary",
        )}
      >
        <span
          className={cn(
            "mt-0.5 border-l-2 pl-2 font-mono text-caption leading-5",
            selected
              ? "border-primary text-primary"
              : "border-border text-fg-muted group-hover:border-primary/50",
          )}
        >
          {String(ordinal).padStart(2, "0")}
        </span>
        <span className="min-w-0">
          <span className="flex items-center justify-between gap-3">
            <span className="truncate text-sm font-medium text-fg">{request.title}</span>
            <span className="shrink-0 font-mono text-caption text-fg-muted">
              {relativeAge(item.enqueued_at)}
            </span>
          </span>
          <span className="mt-1 line-clamp-2 text-xs leading-5 text-fg-muted">
            {request.summary}
          </span>
          <span className="mt-2 flex items-center gap-2 text-caption uppercase tracking-label text-fg-muted">
            <span
              className={cn(
                "size-1 rounded-full",
                request.kind === "approval" ? "bg-primary" : "bg-info",
              )}
              aria-hidden="true"
            />
            {request.kind}
            <span aria-hidden="true">/</span>
            <span className="truncate normal-case tracking-normal">
              {sourceName(request.source)}
            </span>
          </span>
        </span>
      </button>
    </li>
  );
}

interface ItemDetailProps {
  item: HITLOperatorItem;
  headingRef: React.RefObject<HTMLHeadingElement | null>;
  presenting: boolean;
  submitting: boolean;
  conflict: string;
  note: string;
  noteError: string;
  noteIntent: ComposerIntent;
  onBack: () => void;
  onDecision: (decision: "approved" | "denied") => void;
  onNoteChange: (value: string) => void;
  onNoteIntent: (intent: ComposerIntent) => void;
  onSubmitNote: () => void;
  onAcknowledge: () => void;
  onSubmitAttention: (field: "note" | "reply") => void;
  onOpenEvidence: () => void;
  evidenceTrigger: React.RefObject<HTMLButtonElement | null>;
}

function ItemDetail(props: ItemDetailProps) {
  const { item } = props;
  const request = item.request_snapshot;
  const terminal = terminalStates.has(item.state);
  const approvalLabels = (request.action_labels ?? {}) as HITLApprovalLabels;
  const attentionLabels = (request.action_labels ?? {}) as HITLAttentionLabels;
  const attentionIntent: AttentionComposerIntent =
    props.noteIntent === "attention-note"
      ? "note"
      : props.noteIntent === "attention-reply"
        ? "reply"
        : null;
  return (
    <article className="flex min-h-full flex-col">
      <div className="flex-1 px-4 py-5 sm:px-7 sm:py-7 lg:px-10 lg:py-9">
        <button
          type="button"
          onClick={props.onBack}
          className="mb-5 inline-flex items-center gap-2 text-xs text-fg-muted outline-none hover:text-fg focus-visible:ring-2 focus-visible:ring-primary lg:hidden"
        >
          <ArrowLeft className="size-3.5" aria-hidden="true" /> Back to queue
        </button>

        <div className="mx-auto max-w-4xl">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2 font-mono text-caption uppercase tracking-label text-fg-muted">
            <span
              className={cn(
                "border-l-2 pl-2",
                terminal
                  ? "border-success"
                  : request.kind === "attention"
                    ? "border-info"
                    : "border-primary",
              )}
            >
              {item.state.replace("_", " ")}
            </span>
            <span>Queue {String(item.queue_sequence).padStart(4, "0")}</span>
            <span>Rev {item.revision}</span>
            <span className="normal-case tracking-normal">{relativeAge(item.enqueued_at)} old</span>
          </div>
          <h2
            ref={props.headingRef}
            tabIndex={-1}
            className="mt-4 scroll-mt-6 text-2xl font-semibold leading-tight tracking-tight text-fg outline-none sm:text-3xl"
          >
            {request.title}
          </h2>
          <Markdown
            data-testid="hitl-detail-summary"
            content={request.summary}
            tone="evidence"
            linkPolicy="withhold"
            className="mt-3 max-w-3xl leading-6 text-fg-muted sm:text-base"
          />

          {props.conflict ? (
            <div
              role="alert"
              className="mt-6 border-l-2 border-danger bg-danger-muted px-4 py-3 text-sm leading-6 text-danger"
            >
              <strong className="font-semibold text-danger">State changed.</strong> {props.conflict}
            </div>
          ) : null}

          {terminal ? <TerminalOutcome item={item} /> : null}

          <div className="mt-8 grid gap-8 xl:grid-cols-[minmax(0,1.35fr)_minmax(16rem,0.65fr)]">
            <div className="space-y-8">
              <DetailSection eyebrow="Request" text={request.request} />
              {request.recommendation ? (
                <DetailSection eyebrow="Recommendation" text={request.recommendation} accent />
              ) : null}
              {request.impact ? (
                <section aria-labelledby={`impact-${item.item_id}`}>
                  <h3
                    id={`impact-${item.item_id}`}
                    className="text-label font-semibold uppercase tracking-label text-fg-muted"
                  >
                    Decision impact
                  </h3>
                  <div className="mt-3 divide-y divide-border-subtle border-y border-border-subtle">
                    <ImpactRow label="Approve" value={request.impact.approve} tone="approve" />
                    <ImpactRow label="Deny" value={request.impact.deny} tone="deny" />
                  </div>
                </section>
              ) : null}
            </div>
            <aside className="space-y-7 border-t border-border-subtle pt-7 xl:border-l xl:border-t-0 xl:pl-7 xl:pt-0">
              {request.evidence && request.evidence.length > 0 ? (
                <EvidenceSummary
                  count={request.evidence.length}
                  onOpen={props.onOpenEvidence}
                  buttonRef={props.evidenceTrigger}
                />
              ) : null}
              <SourceDetails item={item} />
              <CorrelationDetails correlations={request.correlations} />
            </aside>
          </div>
        </div>
      </div>

      {!terminal ? (
        <div className="sticky bottom-0 border-t border-border-subtle bg-bg-elevated/95 px-4 py-4 backdrop-blur sm:px-7 lg:px-10">
          <div className="mx-auto max-w-4xl">
            {request.kind === "approval" ? (
              <>
                {props.noteIntent === "approved" || props.noteIntent === "denied" ? (
                  <div className="mb-4 border-l-2 border-primary pl-4">
                    <label
                      htmlFor="hitl-decision-note"
                      className="block text-xs font-medium text-fg-secondary"
                    >
                      {props.noteIntent === "approved" ? "Approval note" : "Denial note"}
                    </label>
                    <textarea
                      id="hitl-decision-note"
                      rows={3}
                      maxLength={4000}
                      value={props.note}
                      onChange={(event) => props.onNoteChange(event.target.value)}
                      aria-invalid={Boolean(props.noteError)}
                      aria-describedby={props.noteError ? "hitl-note-error" : undefined}
                      className="mt-2 w-full resize-y border border-border bg-bg-elevated px-3 py-2 text-sm leading-6 text-fg outline-none placeholder:text-fg-muted focus:border-primary focus:ring-1 focus:ring-primary"
                      placeholder="Record the reasoning the caller should receive…"
                    />
                    {props.noteError ? (
                      <p id="hitl-note-error" className="mt-1 text-xs text-danger">
                        {props.noteError}
                      </p>
                    ) : null}
                    <div className="mt-3 flex justify-end gap-2">
                      <ActionButton
                        label="Cancel note"
                        variant="quiet"
                        onClick={() => props.onNoteIntent(null)}
                      />
                      <ActionButton
                        label={
                          props.noteIntent === "approved"
                            ? approvalLabels.approve_with_note || "Approve with note"
                            : approvalLabels.deny_with_note || "Deny with note"
                        }
                        variant={props.noteIntent === "approved" ? "approve" : "deny"}
                        disabled={props.submitting}
                        onClick={props.onSubmitNote}
                      />
                    </div>
                  </div>
                ) : null}
                <div className="flex flex-col justify-between gap-3 sm:flex-row sm:items-center">
                  <p className="text-xs text-fg-muted">
                    {props.presenting || item.state === "staged"
                      ? "Preparing an exact decision revision…"
                      : "One action commits this item immediately."}
                  </p>
                  <fieldset className="grid grid-cols-2 gap-2 sm:flex">
                    <legend className="sr-only">Approval actions</legend>
                    <ActionButton
                      label={approvalLabels.deny || "Deny"}
                      variant="deny"
                      disabled={props.presenting || props.submitting || item.state === "staged"}
                      onClick={() => props.onDecision("denied")}
                    />
                    <ActionButton
                      label={approvalLabels.deny_with_note || "Deny with note"}
                      variant="quiet"
                      disabled={props.presenting || props.submitting || item.state === "staged"}
                      onClick={() => props.onNoteIntent("denied")}
                    />
                    <ActionButton
                      label={approvalLabels.approve_with_note || "Approve with note"}
                      variant="quiet"
                      disabled={props.presenting || props.submitting || item.state === "staged"}
                      onClick={() => props.onNoteIntent("approved")}
                    />
                    <ActionButton
                      label={approvalLabels.approve || "Approve"}
                      variant="approve"
                      disabled={props.presenting || props.submitting || item.state === "staged"}
                      onClick={() => props.onDecision("approved")}
                    />
                  </fieldset>
                </div>
              </>
            ) : (
              <AttentionActions
                labels={attentionLabels}
                intent={attentionIntent}
                value={props.note}
                error={props.noteError}
                disabled={props.presenting || props.submitting || item.state === "staged"}
                preparing={props.presenting || item.state === "staged"}
                onIntent={(intent) =>
                  props.onNoteIntent(
                    intent === "note"
                      ? "attention-note"
                      : intent === "reply"
                        ? "attention-reply"
                        : null,
                  )
                }
                onValueChange={props.onNoteChange}
                onAcknowledge={props.onAcknowledge}
                onSubmit={() =>
                  attentionIntent ? props.onSubmitAttention(attentionIntent) : undefined
                }
              />
            )}
          </div>
        </div>
      ) : null}
    </article>
  );
}

function EvidenceSummary({
  count,
  onOpen,
  buttonRef,
}: {
  count: number;
  onOpen: () => void;
  buttonRef: React.RefObject<HTMLButtonElement | null>;
}) {
  return (
    <section aria-labelledby="hitl-evidence-summary">
      <h3
        id="hitl-evidence-summary"
        className="text-label font-semibold uppercase tracking-label text-fg-muted"
      >
        Evidence
      </h3>
      <button
        ref={buttonRef}
        type="button"
        onClick={onOpen}
        className="mt-3 flex w-full items-center justify-between gap-4 border-l-2 border-primary bg-bg-elevated px-3 py-3 text-left outline-none hover:bg-surface focus-visible:ring-2 focus-visible:ring-primary"
      >
        <span className="flex items-center gap-2 text-xs font-semibold text-fg-secondary">
          <BookOpenText className="size-3.5 text-primary" aria-hidden="true" /> Open case file
        </span>
        <span className="font-mono text-caption text-fg-muted">
          {String(Math.min(count, 24)).padStart(2, "0")} records
        </span>
      </button>
      <p className="mt-2 text-label leading-5 text-fg-muted">
        Opens in context. Queue state and order stay unchanged.
      </p>
    </section>
  );
}

function ActionButton({
  label,
  variant,
  disabled,
  onClick,
}: {
  label: string;
  variant: "approve" | "deny" | "quiet";
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "min-h-10 border px-3 py-2 text-xs font-semibold outline-none transition-colors focus-visible:ring-2 focus-visible:ring-primary disabled:cursor-not-allowed disabled:opacity-45",
        variant === "approve" && "border-success bg-success text-success-muted hover:bg-success",
        variant === "deny" && "border-danger/70 bg-transparent text-danger hover:bg-danger-muted",
        variant === "quiet" &&
          "border-border bg-surface text-fg-secondary hover:border-fg-faint hover:text-fg",
      )}
    >
      {label}
    </button>
  );
}

function DetailSection({
  eyebrow,
  text,
  accent = false,
}: {
  eyebrow: string;
  text: string;
  accent?: boolean;
}) {
  return (
    <section>
      <h3 className="text-label font-semibold uppercase tracking-label text-fg-muted">{eyebrow}</h3>
      <Markdown
        content={text}
        tone="evidence"
        linkPolicy="withhold"
        className={cn(
          "mt-3",
          accent ? "border-l-2 border-primary pl-4 text-fg-secondary" : "text-fg-secondary",
        )}
      />
    </section>
  );
}

function ImpactRow({
  label,
  value,
  tone,
}: {
  label: string;
  value: string;
  tone: "approve" | "deny";
}) {
  return (
    <div className="grid grid-cols-[5rem_1fr] gap-3 py-3 text-sm leading-6">
      <span className={tone === "approve" ? "text-success" : "text-danger"}>{label}</span>
      {/*
        A grid cell rather than a <span>: the impact prose is the caller's own
        words, up to 2000 characters of them, and <Markdown> renders block
        content that cannot nest inside phrasing content.
      */}
      <div className="min-w-0">
        <Markdown
          content={value}
          tone="evidence"
          linkPolicy="withhold"
          className="leading-6 text-fg-muted"
        />
      </div>
    </div>
  );
}

function SourceDetails({ item }: { item: HITLOperatorItem }) {
  const source = item.request_snapshot.source;
  return (
    <section aria-labelledby={`source-${item.item_id}`}>
      <h3
        id={`source-${item.item_id}`}
        className="text-label font-semibold uppercase tracking-label text-fg-muted"
      >
        Source
      </h3>
      <dl className="mt-3 space-y-3 text-xs">
        <MetadataRow
          term="Application"
          value={source.application_label || source.application_id}
          detail={source.application_label ? source.application_id : undefined}
        />
        <MetadataRow
          term="Agent"
          value={source.agent_label || source.agent_id}
          detail={source.agent_label ? source.agent_id : undefined}
        />
        <MetadataRow term="Enqueued" value={new Date(item.enqueued_at).toLocaleString()} />
        {item.request_snapshot.expires_at ? (
          <MetadataRow
            term="Expires"
            value={new Date(item.request_snapshot.expires_at).toLocaleString()}
          />
        ) : null}
      </dl>
    </section>
  );
}

function CorrelationDetails({
  correlations,
}: {
  correlations?: HITLOperatorItem["request_snapshot"]["correlations"];
}) {
  const entries = correlations
    ? (["project", "task", "session"] as const)
        .map((kind) => [kind, correlations[kind]] as const)
        .filter((entry): entry is readonly ["project" | "task" | "session", HITLExternalRef] =>
          Boolean(entry[1]),
        )
    : [];
  if (entries.length === 0) return null;
  return (
    <section>
      <h3 className="flex items-center gap-2 text-label font-semibold uppercase tracking-label text-fg-muted">
        <Link2 className="size-3" aria-hidden="true" /> Correlations
      </h3>
      <dl className="mt-3 space-y-3 text-xs">
        {entries.map(([kind, reference]) => (
          <MetadataRow
            key={kind}
            term={kind}
            value={reference.label || reference.id}
            detail={`${reference.authority}:${reference.id}${reference.revision ? ` @ ${reference.revision}` : ""}`}
          />
        ))}
      </dl>
    </section>
  );
}

function MetadataRow({ term, value, detail }: { term: string; value: string; detail?: string }) {
  return (
    <div>
      <dt className="capitalize text-fg-muted">{term}</dt>
      <dd className="mt-0.5 break-words text-fg-secondary">{value}</dd>
      {detail ? (
        <dd className="mt-0.5 break-all font-mono text-caption text-fg-muted">{detail}</dd>
      ) : null}
    </div>
  );
}

function TerminalOutcome({ item }: { item: HITLOperatorItem }) {
  const outcome = item.terminal_outcome;
  if (!outcome) return null;
  const response = outcome.resolution?.response;
  const decision = response && "decision" in response ? response.decision : item.state;
  const positive = decision === "approved" || decision === "acknowledged";
  const note = response && "note" in response ? response.note : undefined;
  const reply = response && "reply" in response ? response.reply : undefined;
  const reason = outcome.reason || outcome.message;
  return (
    <section
      className={cn(
        "mt-7 border-l-2 px-4 py-3",
        positive ? "border-success bg-success-muted" : "border-danger bg-danger-muted",
      )}
      aria-label="Committed outcome"
    >
      <div className="flex items-center gap-2 text-sm font-semibold text-fg">
        {positive ? (
          <Check className="size-4 text-success" aria-hidden="true" />
        ) : (
          <X className="size-4 text-danger" aria-hidden="true" />
        )}
        {humanizeDecision(decision)}
      </div>
      {note ? <OutcomeText label="Note" text={note} /> : null}
      {reply ? <OutcomeText label="Reply" text={reply} /> : null}
      {!note && reason ? <OutcomeText label="Reason" text={reason} /> : null}
      <p className="mt-2 font-mono text-caption text-fg-muted">
        Committed at revision {outcome.interaction_revision}
      </p>
    </section>
  );
}

function OutcomeText({ label, text }: { label: string; text: string }) {
  return (
    <div className="mt-3">
      <p className="font-mono text-caption uppercase tracking-label text-fg-muted">{label}</p>
      <Markdown
        content={text}
        tone="evidence"
        linkPolicy="withhold"
        className="mt-1 leading-6 text-fg-muted"
      />
    </div>
  );
}

function QueueEmpty({ view, kindFilter }: { view: InboxView; kindFilter: KindFilter }) {
  const kindLabel = kindFilter === "approval" ? "approval" : "attention";
  const filtered = kindFilter !== "all";
  return (
    <EmptyState
      variant={filtered ? "no-results" : "empty"}
      title={
        filtered
          ? `No ${view === "pending" ? "pending" : "resolved"} ${kindLabel} requests`
          : view === "pending"
            ? "No pending requests"
            : "No resolved requests yet"
      }
      description={
        filtered
          ? `This filter is a view of the same ledger; it does not change ${kindLabel} state or order.`
          : view === "pending"
            ? "New agent requests will enter this FIFO stream."
            : "Committed outcomes remain available here."
      }
    />
  );
}

function QueueSkeleton() {
  return (
    <div role="status" aria-label="Loading inbox" className="space-y-px bg-divider">
      {[0, 1, 2].map((row) => (
        <div key={row} className="h-28 animate-pulse bg-bg-elevated" />
      ))}
    </div>
  );
}

function NoSelection({ pendingCount }: { pendingCount: number }) {
  return (
    <div className="flex min-h-[32rem] items-center justify-center px-8 text-center">
      <div className="max-w-sm">
        <p className="font-mono text-caption uppercase tracking-label text-primary">
          No request selected
        </p>
        <h2 className="mt-3 text-xl font-semibold text-fg">
          Inspect the ledger without disturbing it.
        </h2>
        <p className="mt-2 text-sm leading-6 text-fg-muted">
          {pendingCount > 0
            ? "Choose any row. The FIFO order stays fixed until an item is resolved or withdrawn."
            : "This surface is always available, even when no agent room is open."}
        </p>
      </div>
    </div>
  );
}

function MissingItem({ itemID, onBack }: { itemID: string; onBack: () => void }) {
  return (
    <div className="flex min-h-[32rem] items-center justify-center px-8 text-center">
      <div className="max-w-md">
        <p className="font-mono text-caption uppercase tracking-label text-danger">
          Item unavailable
        </p>
        <h2 className="mt-3 text-xl font-semibold text-fg">
          This deep link does not name a HITL item.
        </h2>
        <p className="mt-2 break-all font-mono text-xs text-fg-muted">{itemID}</p>
        <button
          type="button"
          onClick={onBack}
          className="mt-6 border border-border px-4 py-2 text-sm text-fg-secondary hover:border-fg-faint hover:text-fg"
        >
          Return to inbox
        </button>
      </div>
    </div>
  );
}

function InboxFailure({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <PageShell
      as="main"
      className="flex items-center justify-center overflow-y-auto px-6 text-center text-fg-secondary"
    >
      <EmptyState
        variant="error"
        eyebrow="Inbox unavailable"
        title="Durable state could not be loaded."
        description={message}
        action={{ label: "Retry", onClick: onRetry }}
      />
    </PageShell>
  );
}

function patchInboxItem(
  setInbox: React.Dispatch<React.SetStateAction<HITLInbox | null>>,
  next: HITLOperatorItem,
) {
  setInbox((current) => {
    if (!current) return current;
    return {
      ...current,
      pending: current.pending.map((item) => (item.item_id === next.item_id ? next : item)),
      history: current.history.map((item) => (item.item_id === next.item_id ? next : item)),
    };
  });
}

function countKind(items: HITLOperatorItem[], kind: KindFilter): number {
  return kind === "all"
    ? items.length
    : items.filter((item) => item.request_snapshot.kind === kind).length;
}

function sourceName(source: HITLOperatorItem["request_snapshot"]["source"]): string {
  return source.agent_label || source.application_label || source.agent_id;
}

function relativeAge(value: string): string {
  const milliseconds = Date.now() - new Date(value).getTime();
  const minutes = Math.max(0, Math.floor(milliseconds / 60_000));
  if (minutes < 1) return "now";
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}

function formatSyncTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? "from durable state"
    : date.toLocaleTimeString([], { hour: "numeric", minute: "2-digit", second: "2-digit" });
}

function humanizeDecision(value: string): string {
  return value.replaceAll("_", " ").replace(/^./, (letter) => letter.toUpperCase());
}

function humanizeError(reason: unknown): string {
  if (reason instanceof HITLAPIError) {
    if (reason.status === 404) return "The requested item was not found.";
    return reason.detail.message || reason.message;
  }
  return reason instanceof Error ? reason.message : "An unexpected inbox error occurred.";
}

function staleConflictMessage(error: HITLAPIError): string {
  const actual = error.detail.actual_revision;
  const state = error.detail.current_state;
  if (error.detail.terminal_outcome) {
    const response = error.detail.terminal_outcome.resolution?.response;
    const outcome =
      response && "decision" in response ? response.decision : error.detail.terminal_outcome.state;
    return `Another client already committed ${humanizeDecision(outcome)} at revision ${error.detail.terminal_outcome.interaction_revision}. Your action was not applied.`;
  }
  return `Your view was revision ${error.detail.expected_revision ?? "unknown"}; the durable item is now revision ${actual ?? "newer"}${state ? ` (${humanizeDecision(state)})` : ""}. Nothing was overwritten.`;
}

function queueIsVisible(): boolean {
  return (
    typeof window.matchMedia !== "function" || window.matchMedia("(min-width: 1024px)").matches
  );
}
