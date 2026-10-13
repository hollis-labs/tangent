import { Info, Pin, PinOff, Send } from "lucide-react";
import { Dialog } from "radix-ui";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { PageShell } from "@/components/layout/PageShell";
import { Markdown } from "@/components/markdown";
import {
  allItems,
  buildStream,
  type ChatMessage,
  type ComposerMode,
  composerMode,
  isUnseen,
  loadPrefs,
  pendingFinal,
  rosterAgents,
  savePrefs,
  sortAgents,
  staleItemIds,
} from "@/lib/chat-model";
import {
  ChatSendError,
  describeUnavailable,
  fetchChatRoster,
  ROSTER_UNAVAILABLE,
  type Roster,
  sendChat,
} from "@/lib/chat-roster-api";
import { fetchTurnsInbox, replyTurn, TurnsAPIError, type TurnsInbox } from "@/lib/turns-api";
import { cn } from "@/lib/utils";

const PAGE_SIZES = [25, 50, 100, 200];
const ROSTER_POLL_MS = 5000;

function stamp(value?: string): string {
  if (!value) return "";
  const at = new Date(value);
  const now = new Date();
  return at.toDateString() === now.toDateString()
    ? at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
    : at.toLocaleDateString([], { month: "short", day: "numeric" });
}

// A new view over the Agent Turns data. It does not share state or markup with
// the Inbox, which stays exactly as it was.
export default function Chat() {
  const { agentID } = useParams<{ agentID?: string }>();
  const navigate = useNavigate();
  const [inbox, setInbox] = useState<TurnsInbox | null>(null);
  const [roster, setRoster] = useState<Roster | null>(null);
  const [loadError, setLoadError] = useState("");
  const [prefs, setPrefs] = useState(() => loadPrefs());
  const [visible, setVisible] = useState(prefs.pageSize);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [sendError, setSendError] = useState("");
  const [info, setInfo] = useState<ChatMessage | null>(null);
  const syncSeq = useRef(0);
  const rosterSeq = useRef(0);
  // One idempotency key per distinct message text, so a retry after a lost
  // response cannot deliver the same message twice.
  const sendKey = useRef<{ text: string; key: string } | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const stickToBottom = useRef(true);

  const updatePrefs = useCallback((change: (p: typeof prefs) => typeof prefs) => {
    setPrefs((current) => {
      const next = change(current);
      savePrefs(next);
      return next;
    });
  }, []);

  const sync = useCallback(async () => {
    const seq = ++syncSeq.current;
    try {
      const next = await fetchTurnsInbox();
      if (seq !== syncSeq.current) return;
      setInbox(next);
      setLoadError("");
    } catch (reason) {
      if (seq === syncSeq.current) setLoadError((reason as Error).message);
    }
  }, []);

  const syncRoster = useCallback(async () => {
    const seq = ++rosterSeq.current;
    const next = await fetchChatRoster();
    if (seq === rosterSeq.current) setRoster(next);
  }, []);

  useEffect(() => {
    void sync();
    void syncRoster();
    const events = new EventSource("/api/turns/events");
    const onRevision = () => {
      void sync();
      void syncRoster();
    };
    events.addEventListener("revision", onRevision);
    const poll = window.setInterval(() => void syncRoster(), ROSTER_POLL_MS);
    // A session may have started or stopped while the tab was in the background.
    const onFocus = () => void syncRoster();
    window.addEventListener("focus", onFocus);
    document.addEventListener("visibilitychange", onFocus);
    return () => {
      window.removeEventListener("focus", onFocus);
      document.removeEventListener("visibilitychange", onFocus);
      events.removeEventListener("revision", onRevision);
      events.close();
      window.clearInterval(poll);
    };
  }, [sync, syncRoster]);

  const items = useMemo(() => allItems(inbox), [inbox]);
  const rosterState = roster ?? ROSTER_UNAVAILABLE;
  const chatAgents = useMemo(
    () => sortAgents(rosterAgents(rosterState.agents, items), prefs.pinned),
    [rosterState.agents, items, prefs.pinned],
  );
  const active = chatAgents.find((a) => a.agentID === agentID);
  const stream = useMemo(() => (active ? buildStream(items, active) : []), [items, active]);
  const shown = stream.slice(Math.max(0, stream.length - visible));
  const pending = active ? pendingFinal(items, active) : undefined;
  const mode = active ? composerMode(active, pending) : ({ kind: "working" } as const);
  const stale = useMemo(
    () => (active ? staleItemIds(items, active) : new Set<string>()),
    [items, active],
  );

  // Opening an agent is what marks its messages seen.
  const newest = active?.lastAgentMessageAt;
  useEffect(() => {
    if (active && newest) {
      updatePrefs((p) =>
        p.lastSeen[active.agentID] === newest
          ? p
          : { ...p, lastSeen: { ...p.lastSeen, [active.agentID]: newest } },
      );
    }
  }, [active, newest, updatePrefs]);

  // Switching agents resets paging, draft and scroll.
  // biome-ignore lint/correctness/useExhaustiveDependencies: reset keyed on the agent only
  useEffect(() => {
    setVisible(prefs.pageSize);
    setDraft("");
    setSendError("");
    stickToBottom.current = true;
  }, [agentID]);

  const lastKey = shown[shown.length - 1]?.key;
  // biome-ignore lint/correctness/useExhaustiveDependencies: follow the newest message
  useEffect(() => {
    const el = scroller.current;
    if (el && stickToBottom.current) el.scrollTop = el.scrollHeight;
  }, [lastKey, agentID]);

  const onScroll = () => {
    const el = scroller.current;
    if (!el) return;
    stickToBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    if (el.scrollTop < 40 && visible < stream.length) {
      const before = el.scrollHeight;
      setVisible((v) => v + prefs.pageSize);
      // Keep the reader where they were after older messages are prepended.
      requestAnimationFrame(() => {
        el.scrollTop += el.scrollHeight - before;
      });
    }
  };

  const send = async () => {
    const text = draft.trim();
    if (!active || mode.kind === "working" || !text || sending) return;
    setSending(true);
    setSendError("");
    try {
      // The roster may have moved since the last poll; decide on a fresh one so
      // a reply never goes to a session that has just ended.
      const fresh = await fetchChatRoster();
      let current: ComposerMode = mode;
      if (fresh.available) {
        setRoster(fresh);
        const entry = fresh.agents.find((a) => a.launch_id === active.agentID);
        current = composerMode({ ...active, session: entry ? entry.session : null }, pending);
      }
      if (current.kind === "working") {
        setSendError(
          `${active.name} is working. Your text is kept; send again when it finishes the turn.`,
        );
        return;
      }
      if (current.kind === "reply") {
        const target = current.item;
        await replyTurn(target.item_id, {
          expected_revision: target.revision,
          action: "respond",
          response_text: text,
        });
      } else {
        if (sendKey.current?.text !== text) {
          sendKey.current = { text, key: crypto.randomUUID() };
        }
        await sendChat(active.agentID, text, sendKey.current.key);
        sendKey.current = null;
      }
      setDraft("");
      stickToBottom.current = true;
    } catch (reason) {
      setSendError(sendFailure(reason, active.name));
    } finally {
      setSending(false);
      void sync();
      void syncRoster();
    }
  };

  return (
    <PageShell as="main" className="flex">
      <aside
        aria-label="Chat agents"
        className="flex w-56 shrink-0 flex-col border-r border-border bg-surface/40"
      >
        <ul className="min-h-0 flex-1 overflow-y-auto">
          {chatAgents.map((agent) => {
            const pinned = prefs.pinned.includes(agent.agentID);
            const unseen = agent.agentID !== agentID && isUnseen(agent, prefs.lastSeen);
            return (
              <li
                key={agent.agentID}
                className={cn(
                  "group flex items-center border-b border-border-subtle",
                  agent.agentID === agentID && "bg-surface",
                )}
              >
                <button
                  type="button"
                  onClick={() => navigate(`/chat/${encodeURIComponent(agent.agentID)}`)}
                  aria-current={agent.agentID === agentID ? "page" : undefined}
                  className="flex min-w-0 flex-1 items-center gap-2 px-3 py-2 text-left"
                >
                  {unseen ? (
                    <span
                      role="img"
                      aria-label="New messages"
                      className="size-2 shrink-0 rounded-full bg-primary"
                    />
                  ) : (
                    <span aria-hidden className="size-2 shrink-0" />
                  )}
                  <span className="min-w-0 flex-1">
                    <span className={cn("block truncate text-sm", unseen && "font-semibold")}>
                      {agent.name}
                    </span>
                    <span className="block text-xs text-fg-muted">
                      {stamp(agent.lastMessageAt)}
                    </span>
                  </span>
                </button>
                <button
                  type="button"
                  aria-label={pinned ? `Unpin ${agent.name}` : `Pin ${agent.name}`}
                  onClick={() =>
                    updatePrefs((p) => ({
                      ...p,
                      pinned: pinned
                        ? p.pinned.filter((id) => id !== agent.agentID)
                        : [...p.pinned, agent.agentID],
                    }))
                  }
                  className={cn(
                    "mr-2 rounded p-1 text-fg-muted hover:text-fg",
                    !pinned && "opacity-0 focus-visible:opacity-100 group-hover:opacity-100",
                  )}
                >
                  {pinned ? <PinOff size={14} /> : <Pin size={14} />}
                </button>
              </li>
            );
          })}
          {roster && chatAgents.length === 0 ? (
            <li className="px-3 py-3 text-xs text-fg-muted">
              {rosterState.available
                ? "No chat agents are configured on Tether."
                : "Roster unavailable."}
            </li>
          ) : null}
        </ul>
        <div className="space-y-2 border-t border-border p-2 text-xs">
          <label className="flex items-center justify-between gap-2 text-fg-muted">
            Messages per page
            <select
              aria-label="Messages per page"
              value={prefs.pageSize}
              onChange={(e) => {
                const size = Number(e.target.value);
                updatePrefs((p) => ({ ...p, pageSize: size }));
                setVisible(size);
              }}
              className="rounded border border-border bg-bg px-1 py-1 text-fg"
            >
              {[...new Set([...PAGE_SIZES, prefs.pageSize])]
                .sort((a, b) => a - b)
                .map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
            </select>
          </label>
        </div>
      </aside>

      <section aria-label="Conversation" className="flex min-w-0 flex-1 flex-col">
        {loadError ? (
          <p role="alert" className="border-b border-border px-4 py-2 text-sm text-danger">
            {loadError}
          </p>
        ) : null}
        {!active ? (
          <div className="m-auto max-w-sm space-y-2 px-4 text-center text-sm text-fg-muted">
            {!roster ? (
              <p>Loading agents…</p>
            ) : !rosterState.available ? (
              <>
                <p role="status">
                  Chat roster unavailable. {describeUnavailable(rosterState.reason)}
                </p>
                <button
                  type="button"
                  onClick={() => void syncRoster()}
                  className="rounded border border-border px-3 py-1 text-fg"
                >
                  Retry
                </button>
              </>
            ) : chatAgents.length ? (
              <p>Select an agent to chat with.</p>
            ) : (
              <p>No chat agents are configured on Tether.</p>
            )}
          </div>
        ) : (
          <>
            <header className="flex items-center border-b border-border px-4 py-2">
              <h1 className="text-sm font-semibold">{active.name}</h1>
            </header>
            <div
              ref={scroller}
              onScroll={onScroll}
              role="log"
              aria-label={`Conversation with ${active.name}`}
              className="min-h-0 flex-1 space-y-3 overflow-y-auto px-4 py-4"
            >
              {visible < stream.length ? (
                <button
                  type="button"
                  onClick={() => setVisible((v) => v + prefs.pageSize)}
                  className="mx-auto block text-xs text-fg-muted hover:text-fg"
                >
                  Load older messages
                </button>
              ) : null}
              {shown.length === 0 ? (
                <p className="text-center text-sm text-fg-muted">No messages yet.</p>
              ) : null}
              {shown.map((message) => (
                <MessageRow
                  key={message.key}
                  message={message}
                  agentName={active.name}
                  stale={stale.has(message.item.item_id)}
                  onInfo={() => setInfo(message)}
                />
              ))}
            </div>
            <form
              aria-label="Message form"
              onSubmit={(e) => {
                e.preventDefault();
                void send();
              }}
              className="shrink-0 border-t border-border bg-bg p-3"
            >
              {sendError ? (
                <p role="alert" className="mb-2 text-sm text-danger">
                  {sendError}
                </p>
              ) : null}
              <div className="flex items-end gap-2">
                <textarea
                  aria-label="Message"
                  value={draft}
                  rows={2}
                  disabled={mode.kind === "working" || sending}
                  placeholder={
                    mode.kind === "working"
                      ? `${active.name} is working…`
                      : `Message ${active.name}`
                  }
                  onChange={(e) => setDraft(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                      e.preventDefault();
                      void send();
                    }
                  }}
                  className="min-h-[3rem] flex-1 resize-none rounded border border-border bg-surface px-3 py-2 text-sm disabled:opacity-60"
                />
                <button
                  type="submit"
                  aria-label="Send"
                  disabled={mode.kind === "working" || sending || !draft.trim()}
                  className="rounded bg-primary p-2 text-primary-fg disabled:opacity-50"
                >
                  <Send size={16} />
                </button>
              </div>
              {mode.kind === "working" ? (
                <p role="status" className="mt-1 text-xs text-fg-muted">
                  {active.name} is working… You can message again when it finishes the turn.
                </p>
              ) : null}
            </form>
          </>
        )}
      </section>

      <Dialog.Root open={info !== null} onOpenChange={(open) => !open && setInfo(null)}>
        <Dialog.Portal>
          <Dialog.Overlay className="fixed inset-0 bg-black/50" />
          <Dialog.Content className="fixed left-1/2 top-1/2 max-h-[80vh] w-[min(32rem,90vw)] -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded border border-border bg-bg p-4 text-sm">
            <Dialog.Title className="mb-3 font-semibold">Message details</Dialog.Title>
            <Dialog.Description className="sr-only">
              Trace and source of this message
            </Dialog.Description>
            {info ? <MessageInfo message={info} /> : null}
            <Dialog.Close className="mt-4 rounded border border-border px-3 py-1">
              Close
            </Dialog.Close>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </PageShell>
  );
}

function sendFailure(reason: unknown, name: string): string {
  if (reason instanceof ChatSendError) {
    if (reason.code === "agent_working") {
      return `${name} is working. Your text is kept; send again when it finishes the turn.`;
    }
    if (reason.code === "tether_forbidden") {
      return "Tether refused this send (the plugin's token may lack the operate scope). Your text is kept.";
    }
    if (reason.status === 404) return "Sending is not available on this Tether. Your text is kept.";
    return `Could not send (${reason.code}). Your text is kept.`;
  }
  if (reason instanceof TurnsAPIError && reason.status === 409) {
    return "The agent moved on before this was sent (it is working or the turn changed). Your text is kept; send again when it replies.";
  }
  return (reason as Error).message;
}

function MessageRow({
  message,
  agentName,
  stale,
  onInfo,
}: {
  message: ChatMessage;
  agentName: string;
  stale: boolean;
  onInfo: () => void;
}) {
  const mine = message.from === "operator";
  return (
    <div className={cn("flex", mine && "justify-end")}>
      <div
        className={cn(
          "max-w-[80%] rounded-lg px-3 py-2",
          mine ? "bg-primary/15" : "border border-border bg-surface",
        )}
      >
        <div className="mb-1 flex items-center gap-2 text-xs text-fg-muted">
          <span>{mine ? "You" : agentName}</span>
          <time dateTime={message.at}>{stamp(message.at)}</time>
          <button
            type="button"
            aria-label="Message details"
            onClick={onInfo}
            className="ml-auto rounded p-0.5 hover:text-fg"
          >
            <Info size={12} />
          </button>
        </div>
        {stale && !mine ? (
          <p className="mb-1 text-xs italic text-fg-muted">
            Session ended; this message is no longer awaiting a reply.
          </p>
        ) : null}
        {mine ? (
          <p className="whitespace-pre-wrap break-words">{message.body}</p>
        ) : (
          <Markdown content={message.body} />
        )}
      </div>
    </div>
  );
}

function MessageInfo({ message }: { message: ChatMessage }) {
  const { item } = message;
  const source = item.source_message;
  const rows: Array<[string, string | undefined]> = [
    ["Item", item.item_id],
    ["Kind", item.kind],
    ["Session", item.session_id],
    ["Turn", item.turn_id],
    ["Delivery", item.delivery_state],
    ["Sender (unverified)", source?.sender_urn],
    ["Channel", source?.channel],
    ["Publication", source ? `${source.message_id} · sequence ${source.sequence}` : undefined],
    ["Output", source?.output_id],
    ["Runtime", source?.attribution?.runtime],
  ];
  return (
    <>
      <dl className="space-y-1 break-words">
        {rows
          .filter(([, v]) => v)
          .map(([k, v]) => (
            <div key={k}>
              <dt className="text-xs text-fg-muted">{k}</dt>
              <dd>{v}</dd>
            </div>
          ))}
      </dl>
      {item.stage_trace?.length ? (
        <>
          <h3 className="mt-3 font-semibold">Stage trace</h3>
          <ul className="mt-1 space-y-1">
            {item.stage_trace.map((t) => (
              <li key={t.stage_id}>
                {t.stage_id} ({t.stage_version}): {t.outcome}, {t.duration_ms} ms
                {t.failure_code ? ` · ${t.failure_code}` : ""}
              </li>
            ))}
          </ul>
        </>
      ) : null}
    </>
  );
}
