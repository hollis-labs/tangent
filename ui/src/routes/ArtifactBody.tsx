import {
  Archive,
  Check,
  ChevronLeft,
  ChevronRight,
  Copy,
  Download,
  Hash,
  Link2,
  MailCheck,
  Maximize2,
  Minimize2,
  Monitor,
  RefreshCw,
  Smartphone,
  Tablet,
  Trash2,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ArtifactViewer, isTextArtifact } from "@/components/artifacts/ArtifactViewer";
import { Markdown } from "@/components/markdown";
import {
  type ArtifactMessage,
  type ArtifactView,
  archiveArtifact,
  artifactContentURL,
  artifactDirectLink,
  deleteArtifact,
  fetchArtifactItem,
  fetchArtifactText,
  isArtifactTerminal,
  markArtifactRead,
  sendArtifactMessage,
  signoffArtifact,
} from "@/lib/artifacts-api";
import { subscribeRevision } from "@/lib/event-hub";
import type { InboxEntry } from "@/lib/inbox-api";

interface Props {
  entry: InboxEntry;
  onChange: () => Promise<void>;
}

const buttonClass =
  "rounded border border-border px-4 py-2 text-sm hover:bg-surface-hover disabled:opacity-50";
const iconClass =
  "inline-flex h-9 w-9 shrink-0 items-center justify-center rounded hover:bg-surface-hover focus-visible:outline-2 focus-visible:outline-primary disabled:opacity-40";
const inputClass = "w-full rounded border border-border bg-bg-elevated px-3 py-2 text-sm";

const WIDTHS = [
  { id: "desktop", label: "Desktop", width: 0, Icon: Monitor },
  { id: "tablet", label: "Tablet (768 px)", width: 768, Icon: Tablet },
  { id: "mobile", label: "Mobile (390 px)", width: 390, Icon: Smartphone },
] as const;

/** Fetches the artifact and refetches whenever the artifact event stream reports a change. */
function useArtifact(itemID: string) {
  const [item, setItem] = useState<ArtifactView | null>(null);
  const [error, setError] = useState("");
  const sequence = useRef(0);
  const refresh = useCallback(async () => {
    const current = ++sequence.current;
    try {
      const next = await fetchArtifactItem(itemID);
      if (current === sequence.current) {
        setItem(next);
        setError("");
      }
    } catch (reason) {
      if (current === sequence.current) setError((reason as Error).message);
    }
  }, [itemID]);
  useEffect(() => {
    void refresh();
    const subscription = subscribeRevision("/api/artifacts/events", () => void refresh());
    return () => {
      sequence.current++;
      subscription.close();
    };
  }, [refresh]);
  return { item, error, setError, refresh };
}

export function ArtifactBody(props: Props) {
  const itemID = props.entry.interaction.interaction_id;
  const { item, error, setError, refresh } = useArtifact(itemID);
  const [busy, setBusy] = useState(false);
  const [draft, setDraft] = useState("");
  const [copied, setCopied] = useState("");
  const [width, setWidth] = useState(0);
  const [reloadKey, setReloadKey] = useState(0);
  const [maximized, setMaximized] = useState(false);
  // null follows the latest revision; a number pins the one the operator picked.
  const [pinned, setPinned] = useState<number | null>(null);
  const [notice, setNotice] = useState("");
  const seenLatest = useRef(0);
  const copyTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const navigate = useNavigate();

  useEffect(
    () => () => {
      if (copyTimer.current) clearTimeout(copyTimer.current);
    },
    [],
  );
  useEffect(() => {
    if (!maximized) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setMaximized(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [maximized]);
  // Say so when the agent revises the artifact while it is open.
  useEffect(() => {
    if (!item) return;
    if (seenLatest.current && item.latest_revision > seenLatest.current) {
      setNotice(
        pinned === null
          ? `Updated to revision ${item.latest_revision}`
          : `Revision ${item.latest_revision} is available`,
      );
    }
    seenLatest.current = item.latest_revision;
  }, [item, pinned]);

  const act = async (action: () => Promise<unknown>): Promise<boolean> => {
    if (busy) return false;
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
  const flash = (what: string) => {
    setCopied(what);
    if (copyTimer.current) clearTimeout(copyTimer.current);
    copyTimer.current = setTimeout(() => setCopied(""), 2000);
  };

  if (!item)
    return (
      <p role={error ? "alert" : "status"} className="p-6">
        {error || "Loading artifact…"}
      </p>
    );

  const terminal = isArtifactTerminal(item.state);
  const shown = pinned !== null && pinned <= item.latest_revision ? pinned : item.latest_revision;
  const current = item.revisions.find((revision) => revision.revision === shown);
  const isHTML = item.type === "html";

  const copyText = async (value: string, label: string, failure: string) => {
    try {
      await navigator.clipboard.writeText(value);
      flash(label);
    } catch {
      setError(failure);
    }
  };
  const copyContent = async () => {
    setError("");
    try {
      if (isTextArtifact(item.type)) {
        await copyText(
          await fetchArtifactText(item.item_id, shown),
          "Copied",
          "Could not copy. Check your browser's clipboard permission.",
        );
      } else if (item.type === "image") {
        const blob = await (await fetch(artifactContentURL(item.item_id, shown))).blob();
        await navigator.clipboard.write([new ClipboardItem({ [blob.type]: blob })]);
        flash("Copied");
      } else {
        await copyText(
          artifactDirectLink(item.item_id),
          "Link copied",
          "Could not copy. Check your browser's clipboard permission.",
        );
      }
    } catch {
      setError("Could not copy this artifact. Check your browser's clipboard permission.");
    }
  };

  const goto = (revision: number) => {
    setNotice("");
    setPinned(revision >= item.latest_revision ? null : revision);
  };

  const preview = (
    <div
      className={
        maximized ? "fixed inset-0 z-50 flex flex-col gap-2 bg-bg p-3" : "flex flex-col gap-2"
      }
      {...(maximized
        ? { role: "dialog", "aria-modal": true, "aria-label": `${item.title} preview` }
        : {})}
    >
      <div
        className="flex flex-wrap items-center gap-1"
        role="toolbar"
        aria-label="Preview controls"
      >
        <button
          type="button"
          className={iconClass}
          aria-label="Previous revision"
          title="Previous revision"
          disabled={shown <= 1}
          onClick={() => goto(shown - 1)}
        >
          <ChevronLeft size={18} />
        </button>
        <span className="min-w-28 text-center text-sm" aria-live="polite">
          Revision {shown} of {item.latest_revision}
        </span>
        <button
          type="button"
          className={iconClass}
          aria-label="Next revision"
          title="Next revision"
          disabled={shown >= item.latest_revision}
          onClick={() => goto(shown + 1)}
        >
          <ChevronRight size={18} />
        </button>
        {pinned !== null && shown < item.latest_revision ? (
          <button
            type="button"
            className="rounded px-2 py-1 text-xs underline"
            onClick={() => goto(item.latest_revision)}
          >
            Show latest
          </button>
        ) : null}
        {isHTML ? (
          <>
            <span className="mx-2 h-5 border-l border-border" aria-hidden="true" />
            {WIDTHS.map(({ id, label, width: pixels, Icon }) => (
              <button
                key={id}
                type="button"
                className={`${iconClass} ${width === pixels ? "bg-surface-hover" : ""}`}
                aria-label={label}
                aria-pressed={width === pixels}
                title={label}
                onClick={() => setWidth(pixels)}
              >
                <Icon size={18} />
              </button>
            ))}
            <button
              type="button"
              className={iconClass}
              aria-label="Reload preview"
              title="Reload preview"
              onClick={() => setReloadKey((key) => key + 1)}
            >
              <RefreshCw size={18} />
            </button>
          </>
        ) : null}
        <span className="flex-1" />
        <button
          type="button"
          className={iconClass}
          aria-label={maximized ? "Exit full size" : "Maximize preview"}
          title={maximized ? "Exit full size (Esc)" : "Maximize preview"}
          onClick={() => setMaximized((value) => !value)}
        >
          {maximized ? <Minimize2 size={18} /> : <Maximize2 size={18} />}
        </button>
      </div>
      {current?.note ? (
        <p className="text-xs text-fg-muted">
          Revision {shown}: {current.note}
        </p>
      ) : null}
      {notice ? (
        <p role="status" className="text-xs text-primary">
          {notice}
        </p>
      ) : null}
      <div
        className={`overflow-hidden rounded border border-border ${maximized ? "min-h-0 flex-1" : "h-[70vh] min-h-[420px]"}`}
      >
        <ArtifactViewer
          type={item.type}
          itemID={item.item_id}
          revision={shown}
          title={item.title}
          width={width}
          reloadKey={reloadKey}
          rendered={current?.rendered ?? false}
        />
      </div>
    </div>
  );

  return (
    <article className="flex min-h-full flex-col p-4 sm:p-6 lg:p-8">
      <div
        className="mb-4 flex flex-wrap items-center gap-1 border-b border-border pb-3"
        role="toolbar"
        aria-label="Artifact actions"
      >
        <button
          type="button"
          aria-label={item.read_at ? "Marked read" : "Mark read"}
          title={item.read_at ? "Marked read" : "Mark read"}
          disabled={busy || !!item.read_at}
          className={iconClass}
          onClick={() => void act(() => markArtifactRead(item.item_id))}
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
            void act(() => archiveArtifact(item.item_id, { expected_revision: item.revision }))
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
              if (
                await act(() => deleteArtifact(item.item_id, { expected_revision: item.revision }))
              )
                navigate("/");
            })()
          }
        >
          <Trash2 size={18} />
        </button>
        <span className="mx-2 h-5 border-l border-border" aria-hidden="true" />
        <a
          aria-label="Download artifact"
          title={`Download ${current?.filename ?? "artifact"}`}
          className={iconClass}
          href={artifactContentURL(item.item_id, shown, true)}
          download={current?.filename}
        >
          <Download size={18} />
        </a>
        <button
          type="button"
          aria-label="Copy artifact"
          title={copied === "Copied" ? "Copied" : "Copy artifact"}
          className={iconClass}
          onClick={() => void copyContent()}
        >
          {copied === "Copied" ? <Check size={18} /> : <Copy size={18} />}
        </button>
        <button
          type="button"
          aria-label="Copy direct link"
          title={copied === "Link copied" ? "Link copied" : "Copy direct link"}
          className={iconClass}
          onClick={() =>
            void copyText(
              artifactDirectLink(item.item_id),
              "Link copied",
              "Could not copy the link. Check your browser's clipboard permission.",
            )
          }
        >
          {copied === "Link copied" ? <Check size={18} /> : <Link2 size={18} />}
        </button>
        <button
          type="button"
          aria-label="Copy object id"
          title={copied === "Object id copied" ? "Object id copied" : "Copy object id"}
          className={iconClass}
          onClick={() =>
            void copyText(
              item.item_id,
              "Object id copied",
              "Could not copy the object id. Check your browser's clipboard permission.",
            )
          }
        >
          {copied === "Object id copied" ? <Check size={18} /> : <Hash size={18} />}
        </button>
        <span role="status" className="ml-2 text-xs text-fg-muted">
          {copied}
        </span>
      </div>
      <h2 className="text-xl font-semibold">{item.title}</h2>
      <p className="mt-2 text-xs text-fg-muted">
        {item.agent_label || item.agent_id} · {item.type} · {item.read_at ? "Read" : "Unread"}
        {item.requires_signoff ? " · Sign-off requested" : ""}
      </p>
      {/* The name above is whatever the agent declared. This is who the host bound the sender to. */}
      <p className="text-xs text-fg-muted" data-testid="artifact-provenance">
        Sent via {props.entry.interaction.caller_scope}
        {props.entry.interaction.caller_principal_ref
          ? ` · ${props.entry.interaction.caller_principal_ref}`
          : ""}
      </p>
      {item.summary ? (
        <div className="mt-4">
          <Markdown content={item.summary} />
        </div>
      ) : null}
      <div className="mt-4">{preview}</div>
      {error ? (
        <p role="alert" className="mt-3 text-danger">
          {error}
        </p>
      ) : null}
      <Thread
        item={item}
        terminal={terminal}
        busy={busy}
        draft={draft}
        setDraft={setDraft}
        onSend={(kind) =>
          void (async () => {
            const body = draft.trim();
            if (!body) return;
            if (await act(() => sendArtifactMessage(item.item_id, { body, kind }))) setDraft("");
          })()
        }
        onApprove={() =>
          void (async () => {
            if (
              await act(() =>
                signoffArtifact(item.item_id, {
                  expected_revision: item.revision,
                  note: draft.trim() || undefined,
                }),
              )
            )
              setDraft("");
          })()
        }
      />
    </article>
  );
}

function Thread({
  item,
  terminal,
  busy,
  draft,
  setDraft,
  onSend,
  onApprove,
}: {
  item: ArtifactView;
  terminal: boolean;
  busy: boolean;
  draft: string;
  setDraft: (value: string) => void;
  onSend: (kind: "message" | "changes_requested") => void;
  onApprove: () => void;
}) {
  const agent = item.agent_label || item.agent_id;
  return (
    <section className="mt-6 space-y-3 border-t border-border pt-4" aria-label="Conversation">
      <h3 className="font-semibold">Conversation</h3>
      {item.messages.length === 0 ? (
        <p className="text-sm text-fg-muted">No messages yet.</p>
      ) : (
        <ol className="space-y-3">
          {item.messages.map((message) => (
            <MessageRow key={message.seq} message={message} agent={agent} />
          ))}
        </ol>
      )}
      {item.resolution ? (
        <p className="rounded border border-border bg-surface p-3 text-sm">
          Approved{item.resolution.revision ? ` revision ${item.resolution.revision}` : ""} on{" "}
          {new Date(item.resolution.resolved_at).toLocaleString()}
          {item.resolution.note ? ` — ${item.resolution.note}` : ""}
        </p>
      ) : terminal ? (
        <p className="rounded border border-border bg-surface p-3 text-sm">
          This artifact is in history ({item.state}); it takes no more messages.
        </p>
      ) : (
        <div className="space-y-2">
          <label className="block text-sm">
            Message to {agent}
            <textarea
              className={`${inputClass} mt-2`}
              rows={3}
              value={draft}
              maxLength={8000}
              onChange={(event) => setDraft(event.target.value)}
            />
          </label>
          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              className={buttonClass}
              disabled={busy || !draft.trim()}
              onClick={() => onSend("message")}
            >
              Send
            </button>
            <button
              type="button"
              className={buttonClass}
              disabled={busy || !draft.trim()}
              title="Send your note and keep the artifact open"
              onClick={() => onSend("changes_requested")}
            >
              Request changes
            </button>
            {item.requires_signoff ? (
              <button type="button" className={buttonClass} disabled={busy} onClick={onApprove}>
                Approve
              </button>
            ) : null}
          </div>
        </div>
      )}
    </section>
  );
}

function MessageRow({ message, agent }: { message: ArtifactMessage; agent: string }) {
  const mine = message.author === "operator";
  const badge =
    message.kind === "changes_requested"
      ? "Changes requested"
      : message.kind === "approved"
        ? "Approved"
        : "";
  return (
    <li className="rounded border border-border bg-surface p-3 text-sm">
      <p className="mb-1 text-xs text-fg-muted">
        {mine ? "You" : agent}
        {badge ? ` · ${badge}` : ""}
        {message.revision ? ` · on revision ${message.revision}` : ""} ·{" "}
        <time>{new Date(message.created_at).toLocaleString()}</time>
      </p>
      <p className="whitespace-pre-wrap break-words">{message.body}</p>
    </li>
  );
}
