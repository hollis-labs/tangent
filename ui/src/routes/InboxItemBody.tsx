import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { EvidenceDrawer } from "@/components/hitl-evidence";
import { Markdown } from "@/components/markdown";
import { ResponseSummary } from "@/components/ResponseSummary";
import {
  acknowledgeDoc,
  archiveDoc,
  type DocItemView,
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
import { dismissTurn, fetchTurnItem, replyTurn, type TurnItemView } from "@/lib/turns-api";
import { type ComposerIntent, ItemDetail } from "./HITLInbox";

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
    } catch (reason) {
      setError((reason as Error).message);
      await props.onChange();
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
  const presentingID = useRef("");
  const terminal = isTerminal(props.entry);
  useEffect(() => {
    if (
      !item ||
      terminal ||
      item.presented_projection_revision ||
      presentingID.current === item.item_id
    )
      return;
    let canceled = false;
    presentingID.current = item.item_id;
    setPresenting(true);
    setPresentationError("");
    void presentHITLItem(item, getHITLConnectionID())
      .then((next) => {
        if (!canceled) setItem(next);
      })
      .catch((reason) => {
        if (!canceled) setPresentationError((reason as Error).message);
      })
      .finally(() => {
        if (!canceled) setPresenting(false);
        presentingID.current = "";
      });
    return () => {
      canceled = true;
    };
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
  if (!item)
    return (
      <p role={error ? "alert" : "status"} className="p-6">
        {error || "Loading document…"}
      </p>
    );
  const terminal = isTerminal(props.entry);
  return (
    <article className="flex min-h-full flex-col p-4 sm:p-6 lg:p-8">
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
      {error ? (
        <p role="alert" className="mb-3 text-danger">
          {error}
        </p>
      ) : null}
      {terminal ? (
        <SavedReply entry={props.entry} />
      ) : (
        <div className="space-y-3 border-t border-border pt-4">
          <label className="block text-sm">
            Note (optional)
            <textarea
              className={`${inputClass} mt-2`}
              value={note}
              onChange={(event) => setNote(event.target.value)}
            />
          </label>
          <div className="flex flex-wrap gap-2">
            {!item.read_at ? (
              <button
                type="button"
                disabled={busy}
                className={buttonClass}
                onClick={() => void act(() => markDocRead(item.item_id))}
              >
                Mark read
              </button>
            ) : null}
            {item.requires_ack ? (
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
            ) : null}
            <button
              type="button"
              disabled={busy}
              className={buttonClass}
              onClick={() =>
                void act(() =>
                  archiveDoc(item.item_id, {
                    expected_revision: item.revision,
                    reason: note || undefined,
                  }),
                )
              }
            >
              Archive
            </button>
          </div>
        </div>
      )}
    </article>
  );
}

function TurnBody(props: Props) {
  const { item, error, busy, act } = useItem<TurnItemView>(props, fetchTurnItem);
  const [response, setResponse] = useState("");
  const [option, setOption] = useState("");
  const [note, setNote] = useState("");
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
        {item.agent_label || item.agent_id} · {item.kind} ·{" "}
        {item.delivery_state.replaceAll("_", " ")}
      </p>
      <div className="my-6 flex-1">
        <Markdown content={item.content} />
      </div>
      {error ? (
        <p role="alert" className="mb-3 text-danger">
          {error}
        </p>
      ) : null}
      {terminal ? (
        <SavedReply entry={props.entry} />
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
          <div className="flex flex-wrap gap-2">
            <button
              type="submit"
              disabled={busy || (!response.trim() && !option)}
              className={buttonClass}
            >
              Send reply
            </button>
            {item.kind === "approval" ? (
              <>
                <button
                  type="button"
                  disabled={busy}
                  className={buttonClass}
                  onClick={() => respond("approve")}
                >
                  Approve
                </button>
                <button
                  type="button"
                  disabled={busy}
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
