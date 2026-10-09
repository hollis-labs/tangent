import { Archive, Check, Copy, Download, MailCheck, Trash2 } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { EvidenceDrawer } from "@/components/hitl-evidence";
import { Markdown } from "@/components/markdown";
import { ResponseSummary } from "@/components/ResponseSummary";
import {
  acknowledgeDoc,
  archiveDoc,
  type DocItemView,
  deleteDoc,
  fetchDocItem,
  markDocRead,
} from "@/lib/docs-api";
import {
  fetchHITLItem,
  getHITLConnectionID,
  presentHITLItem,
  resolveHITLApproval,
  resolveHITLAttention,
} from "@/lib/hitl-api";
import { categoryOf, type InboxEntry, isTerminal } from "@/lib/inbox-api";
import {
  canReplyTurn,
  dismissTurn,
  fetchReplyDelivery,
  fetchTurnItem,
  type ReplyDeliveryView,
  replyTurn,
  retryReplyDelivery,
  type TurnItemView,
} from "@/lib/turns-api";
import { type ComposerIntent, ItemDetail } from "./HITLInbox";
import { TurnContent } from "./TurnContent";

interface Props {
  entry: InboxEntry;
  onChange: () => Promise<void>;
}
const buttonClass =
  "rounded border border-border px-4 py-2 text-sm hover:bg-surface-hover disabled:opacity-50";
const inputClass = "w-full rounded border border-border bg-bg-elevated px-3 py-2 text-sm";

export function InboxItemBody(props: Props) {
  switch (categoryOf(props.entry)) {
    case "approval":
      return <ApprovalBody {...props} />;
    case "document":
      return <DocumentBody {...props} />;
    case "turn":
      return <TurnBody {...props} />;
    default:
      return null;
  }
}

// Fetching a body never consumes or acknowledges an agent's delivery. Mutation
// remains explicit and uses each kind's own revision-pinned application API.
function useItem<T>(props: Props, fetchItem: (id: string) => Promise<T>) {
  const [item, setItem] = useState<T | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const sequence = useRef(0);
  const id = props.entry.interaction.interaction_id;
  const refresh = useCallback(async () => {
    const current = ++sequence.current;
    try {
      const next = await fetchItem(id);
      if (current === sequence.current) {
        setItem(next);
        setError("");
      }
    } catch (reason) {
      if (current === sequence.current) setError((reason as Error).message);
    }
  }, [fetchItem, id]);
  useEffect(() => {
    void props.entry.interaction.revision;
    void refresh();
    return () => {
      sequence.current++;
    };
  }, [refresh, props.entry.interaction.revision]);
  const act = async (action: () => Promise<unknown>) => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await action();
      await refresh();
      await props.onChange();
      return true;
    } catch (reason) {
      setError((reason as Error).message);
      await props.onChange();
      return false;
    } finally {
      setBusy(false);
    }
  };
  return { item, setItem, error, busy, act };
}

function ApprovalBody(props: Props) {
  const { item, setItem, error, busy, act } = useItem(props, fetchHITLItem);
  const [note, setNote] = useState("");
  const [noteError, setNoteError] = useState("");
  const navigate = useNavigate();
  const [intent, setIntent] = useState<ComposerIntent>(null);
  const [evidenceOpen, setEvidenceOpen] = useState(false);
  const [presenting, setPresenting] = useState(false);
  const [presentationError, setPresentationError] = useState("");
  const heading = useRef<HTMLHeadingElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const presentationRequest = useRef<object | null>(null);
  const presentationAttempted = useRef(false);
  const terminal = isTerminal(props.entry);
  // Inbox keys this body by interaction ID. A refreshed projection of that
  // same item must not cancel its in-flight presentation; only unmount does.
  useEffect(() => {
    return () => {
      presentationRequest.current = null;
    };
  }, []);
  useEffect(() => {
    if (
      !item ||
      terminal ||
      item.presented_projection_revision ||
      presentationRequest.current ||
      presentationAttempted.current
    )
      return;
    const request = {};
    presentationAttempted.current = true;
    presentationRequest.current = request;
    setPresenting(true);
    setPresentationError("");
    void presentHITLItem(item, getHITLConnectionID())
      .then((next) => {
        if (presentationRequest.current === request) {
          setItem((current) =>
            current?.item_id === next.item_id && current.revision <= next.revision ? next : current,
          );
        }
      })
      .catch((reason) => {
        if (presentationRequest.current === request) {
          // A projection refresh is not a new operator attempt. Retain this
          // item's refusal instead of silently issuing another POST.
          setPresentationError((reason as Error).message);
        }
      })
      .finally(() => {
        if (presentationRequest.current === request) {
          presentationRequest.current = null;
          setPresenting(false);
        }
      });
  }, [item, terminal, setItem]);
  if (!item)
    return (
      <p role={error ? "alert" : "status"} className="p-6">
        {error || "Loading request…"}
      </p>
    );
  const decision = (value: "approved" | "denied", withNote = false) => {
    if (withNote && !note.trim()) {
      setNoteError("Write a note before submitting.");
      return;
    }
    void act(() => resolveHITLApproval(item, value, withNote ? note.trim() : undefined));
  };
  const attention = (field?: "note" | "reply") => {
    if (field && !note.trim()) {
      setNoteError(
        field === "note" ? "Write a note before submitting." : "Write a reply before submitting.",
      );
      return;
    }
    void act(() => resolveHITLAttention(item, field ? { [field]: note.trim() } : undefined));
  };
  return (
    <>
      <ItemDetail
        item={item}
        headingRef={heading}
        presenting={presenting}
        submitting={busy}
        conflict={error || presentationError}
        note={note}
        noteError={noteError}
        noteIntent={intent}
        onBack={() => navigate("/")}
        onDecision={(value) => decision(value)}
        onNoteChange={(value) => {
          setNote(value);
          setNoteError("");
        }}
        onNoteIntent={(value) => {
          setIntent(value);
          setNote("");
          setNoteError("");
        }}
        onSubmitNote={() => {
          if (intent === "approved" || intent === "denied") decision(intent, true);
        }}
        onAcknowledge={() => attention()}
        onSubmitAttention={attention}
        onOpenEvidence={() => setEvidenceOpen(true)}
        evidenceTrigger={trigger}
      />
      <EvidenceDrawer
        itemID={item.item_id}
        itemTitle={item.request_snapshot.title}
        evidence={item.request_snapshot.evidence}
        open={evidenceOpen}
        onOpenChange={(open) => {
          setEvidenceOpen(open);
          if (!open) queueMicrotask(() => trigger.current?.focus());
        }}
      />
    </>
  );
}

function DocumentBody(props: Props) {
  const { item, error, busy, act } = useItem<DocItemView>(props, fetchDocItem);
  const [note, setNote] = useState("");
  const [copied, setCopied] = useState(false);
  const [actionError, setActionError] = useState("");
  const navigate = useNavigate();
  const copyTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(
    () => () => {
      if (copyTimer.current) clearTimeout(copyTimer.current);
    },
    [],
  );
  if (!item)
    return (
      <p role={error ? "alert" : "status"} className="p-6">
        {error || "Loading document…"}
      </p>
    );
  const terminal = isTerminal(props.entry);
  const iconClass =
    "inline-flex h-9 w-9 shrink-0 items-center justify-center rounded hover:bg-surface-hover focus-visible:outline-2 focus-visible:outline-primary disabled:opacity-40";
  const copy = async () => {
    setActionError("");
    try {
      await navigator.clipboard.writeText(item.content_markdown);
      setCopied(true);
      if (copyTimer.current) clearTimeout(copyTimer.current);
      copyTimer.current = setTimeout(() => setCopied(false), 2000);
    } catch {
      setActionError("Could not copy the document. Check your browser's clipboard permission.");
    }
  };
  const download = () => {
    const url = URL.createObjectURL(
      new Blob([item.content_markdown], { type: "text/markdown;charset=utf-8" }),
    );
    const link = document.createElement("a");
    link.href = url;
    link.download = `${
      item.title
        .replace(/[^a-zA-Z0-9 ._-]/g, "_")
        .trim()
        .slice(0, 120) || "document"
    }.md`;
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  };
  return (
    <article className="flex min-h-full flex-col p-4 sm:p-6 lg:p-8">
      <div
        className="mb-4 flex flex-wrap items-center gap-1 border-b border-border pb-3"
        role="toolbar"
        aria-label="Document actions"
      >
        <button
          type="button"
          aria-label={item.read_at ? "Marked read" : "Mark read"}
          title={item.read_at ? "Marked read" : "Mark read"}
          disabled={busy || !!item.read_at}
          className={iconClass}
          onClick={() => void act(() => markDocRead(item.item_id))}
        >
          <MailCheck size={18} />
        </button>
        <button
          type="button"
          aria-label="Archive"
          title={terminal ? "Already in history" : "Archive"}
          disabled={busy || terminal}
          className={iconClass}
          onClick={() =>
            void act(() =>
              archiveDoc(item.item_id, {
                expected_revision: item.revision,
                reason: note || undefined,
              }),
            )
          }
        >
          <Archive size={18} />
        </button>
        <button
          type="button"
          aria-label="Delete"
          title="Delete"
          disabled={busy}
          className={`${iconClass} hover:text-danger`}
          onClick={() =>
            void (async () => {
              if (await act(() => deleteDoc(item.item_id, { expected_revision: item.revision })))
                navigate("/");
            })()
          }
        >
          <Trash2 size={18} />
        </button>
        <span className="mx-2 h-5 border-l border-border" aria-hidden="true" />
        <button
          type="button"
          aria-label="Download document"
          title="Download Markdown"
          className={iconClass}
          onClick={download}
        >
          <Download size={18} />
        </button>
        <button
          type="button"
          aria-label="Copy document"
          title={copied ? "Copied" : "Copy document"}
          className={iconClass}
          onClick={() => void copy()}
        >
          {copied ? <Check size={18} /> : <Copy size={18} />}
        </button>
        <span role="status" className="ml-2 text-xs text-fg-muted">
          {copied ? "Copied" : ""}
        </span>
      </div>
      <h2 className="text-xl font-semibold">{item.title}</h2>
      <p className="mt-2 text-xs text-fg-muted">
        {item.agent_label || item.agent_id} · {item.read_at ? "Read" : "Unread"}
      </p>
      {item.summary ? (
        <div className="mt-4">
          <Markdown content={item.summary} />
        </div>
      ) : null}
      <div className="my-6 flex-1">
        <Markdown content={item.content_markdown} />
      </div>
      {error || actionError ? (
        <p role="alert" className="mb-3 text-danger">
          {error || actionError}
        </p>
      ) : null}
      {terminal ? (
        <SavedReply entry={props.entry} />
      ) : item.requires_ack ? (
        <div className="space-y-3 border-t border-border pt-4">
          <label className="block text-sm">
            Note (optional)
            <textarea
              className={`${inputClass} mt-2`}
              value={note}
              onChange={(event) => setNote(event.target.value)}
            />
          </label>
          <button
            type="button"
            disabled={busy}
            className={buttonClass}
            onClick={() =>
              void act(() =>
                acknowledgeDoc(item.item_id, { expected_revision: item.revision, note }),
              )
            }
          >
            Acknowledge
          </button>
        </div>
      ) : null}
    </article>
  );
}

function TurnBody(props: Props) {
  const { item, error, busy, act } = useItem<TurnItemView>(props, fetchTurnItem);
  const [response, setResponse] = useState("");
  const [option, setOption] = useState("");
  const [note, setNote] = useState("");
  const [interrupt, setInterrupt] = useState(false);
  const [interruptSupported, setInterruptSupported] = useState(false);
  const [replySupported, setReplySupported] = useState(false);
  const onCapability = useCallback((supported: boolean, canInterrupt: boolean) => {
    setReplySupported(supported);
    setInterruptSupported(supported && canInterrupt);
  }, []);
  if (!item)
    return (
      <p role={error ? "alert" : "status"} className="p-6">
        {error || "Loading agent turn…"}
      </p>
    );
  const terminal = isTerminal(props.entry);
  const respond = (action: string) =>
    void act(() =>
      replyTurn(item.item_id, {
        expected_revision: item.revision,
        interrupt: interruptSupported && interrupt,
        action,
        response_text: response || undefined,
        selected_option: option || undefined,
        note: note || undefined,
      }),
    );
  return (
    <article className="flex min-h-full flex-col p-4 sm:p-6 lg:p-8">
      <h2 className="text-xl font-semibold">{item.title}</h2>
      <p className="mt-2 text-xs text-fg-muted">
        {item.agent_label || item.agent_id} · {item.kind} · Inbox{" "}
        {item.delivery_state.replaceAll("_", " ")}
      </p>
      <div className="my-6 flex-1">
        <TurnContent item={item} />
      </div>
      {error ? (
        <p role="alert" className="mb-3 text-danger">
          {error}
        </p>
      ) : null}
      {item.source_message?.origin === "routed" ? (
        <ReplyDelivery key={item.item_id} item={item} onCapability={onCapability} />
      ) : null}
      {terminal ? (
        <SavedReply entry={props.entry} />
      ) : !canReplyTurn(item) ? (
        <section className="space-y-3 border-t border-border pt-4">
          <p>Informational publication — no runtime reply target.</p>
          <button
            type="button"
            className={buttonClass}
            disabled={busy}
            onClick={() =>
              void act(() => dismissTurn(item.item_id, { expected_revision: item.revision }))
            }
          >
            Dismiss
          </button>
        </section>
      ) : (
        <form
          className="space-y-3 border-t border-border pt-4"
          onSubmit={(event) => {
            event.preventDefault();
            respond("respond");
          }}
        >
          {item.options?.length ? (
            <fieldset>
              <legend className="mb-2 text-sm">Choose an option</legend>
              <div className="flex flex-wrap gap-2">
                {item.options.map((choice) => (
                  <label
                    key={choice.value}
                    className={`${buttonClass} inline-flex items-center gap-2`}
                  >
                    <input
                      type="radio"
                      name="turn-option"
                      value={choice.value}
                      checked={option === choice.value}
                      onChange={() => setOption(choice.value)}
                    />
                    {choice.label}
                    {choice.description ? (
                      <span className="text-xs text-fg-muted">{choice.description}</span>
                    ) : null}
                  </label>
                ))}
              </div>
            </fieldset>
          ) : null}
          <label className="block text-sm">
            Your reply
            <textarea
              className={`${inputClass} mt-2 min-h-24`}
              value={response}
              onChange={(event) => setResponse(event.target.value)}
            />
          </label>
          <label className="block text-sm">
            Note (optional)
            <input
              className={`${inputClass} mt-2`}
              value={note}
              onChange={(event) => setNote(event.target.value)}
            />
          </label>
          {interruptSupported ? (
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={interrupt}
                disabled={busy}
                onChange={(event) => setInterrupt(event.target.checked)}
              />
              Interrupt the sender's active turn
            </label>
          ) : null}
          <div className="flex flex-wrap gap-2">
            <button
              type="submit"
              disabled={
                busy ||
                (item.source_message?.origin === "routed" && !replySupported) ||
                (!response.trim() && !option)
              }
              className={buttonClass}
            >
              Send reply
            </button>
            {item.kind === "approval" ? (
              <>
                <button
                  type="button"
                  disabled={busy || (item.source_message?.origin === "routed" && !replySupported)}
                  className={buttonClass}
                  onClick={() => respond("approve")}
                >
                  Approve
                </button>
                <button
                  type="button"
                  disabled={busy || (item.source_message?.origin === "routed" && !replySupported)}
                  className={buttonClass}
                  onClick={() => respond("reject")}
                >
                  Reject
                </button>
              </>
            ) : null}
            <button
              type="button"
              disabled={busy}
              className={buttonClass}
              onClick={() =>
                void act(() =>
                  dismissTurn(item.item_id, {
                    expected_revision: item.revision,
                    reason: note || undefined,
                  }),
                )
              }
            >
              Dismiss
            </button>
          </div>
        </form>
      )}
    </article>
  );
}

function replyReason(reason: string): string {
  const meanings: Record<string, string> = {
    waiting_for_idle: "Waiting for the sender runtime to become idle.",
    handed_off: "The reply target moved to a successor session; delivery is not implied.",
    turn_failed: "The runtime accepted delivery, but that turn failed.",
    interrupt_unconfirmed:
      "The interrupt could not be confirmed; no automatic retry will send another reply.",
    session_ended_no_binding: "The original session ended without a current runtime binding.",
    bound_session_not_running: "The bound runtime session is not running.",
    pull_only_binding: "This runtime binding cannot receive a pushed reply.",
    resolve_failed: "The runtime target could not be resolved.",
    submit_failed: "The runtime refused reply submission.",
    no_turn_feed: "This runtime has no supported reply delivery feed.",
    daemon_restarted_during_delivery: "Tether restarted while delivery was in progress.",
    body_purged: "The saved reply body is no longer available to Tether.",
  };
  return meanings[reason] ?? "Tether reported an unknown delivery reason.";
}

// The plugin owns dispatch and persistence. Rendering polls metadata only and
// never acknowledges, re-resolves, or automatically submits a reply.
function ReplyDelivery({
  item,
  onCapability,
}: {
  item: TurnItemView;
  onCapability: (supported: boolean, interrupt: boolean) => void;
}) {
  const [view, setView] = useState<ReplyDeliveryView | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [interrupt, setInterrupt] = useState(false);
  const action = useRef<{ id: string; version: number; interrupt: boolean } | null>(null);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let controller: AbortController | undefined;
    const refresh = async () => {
      controller = new AbortController();
      const deadline = setTimeout(() => controller?.abort(), 8000);
      try {
        const next = await fetchReplyDelivery(item.item_id, controller.signal);
        if (!stopped) {
          setView(next);
          setError("");
          onCapability(next.reply_supported, next.interrupt_supported);
        }
      } catch {
        if (!stopped) {
          setError("Reply delivery is unavailable");
          onCapability(false, false);
        }
      } finally {
        clearTimeout(deadline);
        if (!stopped) timer = setTimeout(() => void refresh(), 2000);
      }
    };
    void refresh();
    return () => {
      stopped = true;
      alive.current = false;
      controller?.abort();
      clearTimeout(timer);
      onCapability(false, false);
    };
  }, [item.item_id, onCapability]);
  const retry = async () => {
    if (!view || busy || (!view.retry_allowed && !action.current)) return;
    const choice = action.current ?? {
      id: crypto.randomUUID(),
      version: view.version,
      interrupt: view.interrupt_supported && interrupt,
    };
    action.current = choice; // Preserve this click after an ambiguous HTTP outcome.
    setBusy(true);
    setError("");
    try {
      const next = await retryReplyDelivery(
        item.item_id,
        choice.version,
        choice.id,
        choice.interrupt,
      );
      if (next.item_id !== item.item_id || next.schema_version !== 1)
        throw new Error("Mismatched delivery projection");
      if (alive.current) {
        setView(next);
        action.current = null;
      }
    } catch {
      if (alive.current)
        setError("Retry was not confirmed; repeat this action to check the same reply attempt");
    } finally {
      if (alive.current) setBusy(false);
    }
  };
  return (
    <section
      aria-label="Reply delivery"
      className="mb-4 space-y-2 rounded border border-border p-3"
    >
      <p role="status">
        {view ? `Reply ${view.state.replaceAll("_", " ")}` : "Checking reply delivery…"}
      </p>
      {view?.accepted && !["delivered", "undeliverable"].includes(view.state) ? (
        <p>Accepted by Tether; delivery has not been confirmed.</p>
      ) : null}
      {view?.reason ? <p>{replyReason(view.reason)}</p> : null}
      {view?.failure_code ? (
        <p role="alert">
          {view.failure_code.replaceAll("_", " ")}
          {view.failure_status ? ` (${view.failure_status})` : ""}
        </p>
      ) : null}
      {view?.acknowledged ? <p>Delivered reply acknowledged.</p> : null}
      {view?.retry_allowed || action.current ? (
        <div className="space-y-2">
          {view?.interrupt_supported ? (
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                checked={interrupt}
                disabled={busy || action.current !== null}
                onChange={(event) => setInterrupt(event.target.checked)}
              />
              Interrupt on this retry
            </label>
          ) : null}
          <button
            type="button"
            className={buttonClass}
            disabled={busy}
            onClick={() => void retry()}
          >
            Retry reply
          </button>
        </div>
      ) : null}
      {error ? <p role="alert">{error}</p> : null}
    </section>
  );
}

function SavedReply({ entry }: { entry: InboxEntry }) {
  return (
    <section className="space-y-3 rounded border border-border bg-surface p-4">
      <h3 className="font-semibold">{entry.resolution ? "Your reply" : "Outcome"}</h3>
      {entry.resolution ? (
        <>
          <ResponseSummary value={entry.resolution.response_payload} />
          <time className="block text-xs text-fg-muted">
            Saved {new Date(entry.resolution.recorded_at).toLocaleString()}
          </time>
        </>
      ) : (
        <p>{entry.interaction.terminal_reason || entry.interaction.state}</p>
      )}
    </section>
  );
}
