import { Button, Callout, EmptyState, LiveDot } from "@hollis-labs/design-components";
import { Check, CheckCircle2, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";

import { Markdown } from "@/components/markdown";
import {
  dismissTurn,
  fetchTurnItem,
  fetchTurnsInbox,
  replyTurn,
  type TurnItemView,
  type TurnKind,
  type TurnsInbox,
} from "@/lib/turns-api";
import { cn } from "@/lib/utils";

type ViewMode = "pending" | "history" | "by-session";
type KindFilter = "all" | TurnKind;

const KIND_COLORS: Record<TurnKind, { bg: string; text: string; border: string }> = {
  question: { bg: "bg-sky-950/60", text: "text-sky-300", border: "border-sky-800/80" },
  approval: { bg: "bg-amber-950/60", text: "text-amber-300", border: "border-amber-800/80" },
  checkpoint: { bg: "bg-violet-950/60", text: "text-violet-300", border: "border-violet-800/80" },
  failure: { bg: "bg-rose-950/60", text: "text-rose-300", border: "border-rose-800/80" },
  terminal: { bg: "bg-emerald-950/60", text: "text-emerald-300", border: "border-emerald-800/80" },
};

export default function TurnsInboxRoute() {
  const { itemID } = useParams<{ itemID?: string }>();
  const navigate = useNavigate();

  const [inbox, setInbox] = useState<TurnsInbox | null>(null);
  const [detachedItem, setDetachedItem] = useState<TurnItemView | null>(null);
  const [view, setView] = useState<ViewMode>("pending");
  const [kindFilter, setKindFilter] = useState<KindFilter>("all");
  const [sessionFilter, setSessionFilter] = useState<string>("all");
  const [syncStatus, setSyncStatus] = useState("Connecting to live turns stream…");
  const [error, setError] = useState("");
  const [actionError, setActionError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  // Composer fields
  const [responseText, setResponseText] = useState("");
  const [selectedOption, setSelectedOption] = useState("");
  const [note, setNote] = useState("");

  const syncSequence = useRef(0);
  const activeItemID = useRef(itemID);
  activeItemID.current = itemID;

  const syncInbox = useCallback(async () => {
    const seq = ++syncSequence.current;
    const requestedID = activeItemID.current;
    try {
      const next = await fetchTurnsInbox();
      let directItem: TurnItemView | null = null;
      if (
        requestedID &&
        !next.pending.some((it) => it.item_id === requestedID) &&
        !next.history.some((it) => it.item_id === requestedID)
      ) {
        try {
          directItem = await fetchTurnItem(requestedID);
        } catch {
          // direct item unavailable; keep null
        }
      }
      if (seq !== syncSequence.current) return;
      setInbox(next);
      setDetachedItem(directItem);
      setError("");
      setSyncStatus(`Synced at ${new Date(next.synced_at).toLocaleTimeString()}`);
    } catch (err) {
      if (seq !== syncSequence.current) return;
      setError((err as Error).message);
      setSyncStatus("Durable queue unavailable — retrying");
    }
  }, []);

  useEffect(() => {
    void syncInbox();
  }, [syncInbox]);

  // SSE revision stream
  useEffect(() => {
    const events = new EventSource("/api/turns/events");
    const onRevision = () => void syncInbox();
    events.addEventListener("revision", onRevision);
    events.onopen = () => setSyncStatus("Live revision sync active");
    events.onerror = () => setSyncStatus("Reconnecting to live queue…");
    return () => {
      events.removeEventListener("revision", onRevision);
      events.close();
    };
  }, [syncInbox]);

  const allItems = useMemo(() => {
    const list = [...(inbox?.pending ?? []), ...(inbox?.history ?? [])];
    if (detachedItem && !list.some((it) => it.item_id === detachedItem.item_id)) {
      list.push(detachedItem);
    }
    return list;
  }, [inbox, detachedItem]);

  const sessions = useMemo(() => {
    const map = new Map<string, number>();
    for (const it of allItems) {
      map.set(it.session_id, (map.get(it.session_id) ?? 0) + 1);
    }
    return Array.from(map.entries()).map(([id, count]) => ({ id, count }));
  }, [allItems]);

  const filteredItems = useMemo(() => {
    let source: TurnItemView[] = [];
    if (view === "pending") {
      source = inbox?.pending ?? [];
    } else if (view === "history") {
      source = inbox?.history ?? [];
    } else {
      source = allItems;
    }

    return source.filter((item) => {
      if (kindFilter !== "all" && item.kind !== kindFilter) return false;
      if (sessionFilter !== "all" && item.session_id !== sessionFilter) return false;
      return true;
    });
  }, [inbox, allItems, view, kindFilter, sessionFilter]);

  const activeItem = useMemo(() => {
    if (itemID) {
      const found = allItems.find((it) => it.item_id === itemID);
      if (found) return found;
    }
    return filteredItems[0] ?? null;
  }, [itemID, allItems, filteredItems]);

  // Reset composer on item change
  useEffect(() => {
    if (activeItem?.item_id) {
      setResponseText("");
      setSelectedOption("");
      setNote("");
      setActionError("");
    }
  }, [activeItem?.item_id]);

  const handleSelect = (targetID: string) => {
    navigate(`/turns/items/${encodeURIComponent(targetID)}`);
  };

  const handleReply = async (action: string) => {
    if (!activeItem) return;
    setSubmitting(true);
    setActionError("");
    try {
      await replyTurn(activeItem.item_id, {
        expected_revision: activeItem.revision,
        action,
        response_text: responseText.trim(),
        selected_option: selectedOption,
        note: note.trim() || undefined,
      });
      await syncInbox();
    } catch (err) {
      setActionError((err as Error).message);
    } finally {
      setSubmitting(false);
    }
  };

  const handleDismiss = async () => {
    if (!activeItem) return;
    setSubmitting(true);
    setActionError("");
    try {
      await dismissTurn(activeItem.item_id, {
        expected_revision: activeItem.revision,
        reason: note.trim() || "Dismissed by operator",
      });
      await syncInbox();
    } catch (err) {
      setActionError((err as Error).message);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="flex h-[calc(100vh-53px)] flex-col bg-zinc-950 text-zinc-100">
      {/* Top Bar */}
      <header className="flex shrink-0 items-center justify-between border-b border-zinc-800 px-6 py-3">
        <div className="flex items-center gap-3">
          <h1 className="text-base font-semibold tracking-tight">Agent turns FIFO inbox</h1>
          <span className="rounded-md border border-zinc-800 bg-zinc-900 px-2 py-0.5 text-xs text-zinc-400">
            surface_turns_default
          </span>
          <div className="flex items-center gap-2 text-xs text-zinc-400">
            <LiveDot tone={error ? "danger" : "success"} pulsing={!error} />
            <span>{syncStatus}</span>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => void syncInbox()}
            className="text-zinc-400 hover:text-zinc-100"
            title="Refresh inbox"
          >
            <RefreshCw className="h-3.5 w-3.5" />
          </Button>
        </div>
      </header>

      {/* Main View Area */}
      <div className="flex min-h-0 flex-1">
        {/* Left Column: Queue List & Filters */}
        <div className="flex w-96 shrink-0 flex-col border-r border-zinc-800 bg-zinc-950">
          {/* View Mode Tabs */}
          <div className="flex border-b border-zinc-800 px-3 py-2">
            <div className="flex w-full rounded-lg bg-zinc-900 p-1 text-xs">
              <button
                type="button"
                onClick={() => setView("pending")}
                className={cn(
                  "flex-1 rounded-md py-1.5 font-medium transition-colors",
                  view === "pending"
                    ? "bg-zinc-800 text-zinc-100"
                    : "text-zinc-400 hover:text-zinc-200",
                )}
              >
                Needs Attention ({inbox?.total_pending ?? 0})
              </button>
              <button
                type="button"
                onClick={() => setView("history")}
                className={cn(
                  "flex-1 rounded-md py-1.5 font-medium transition-colors",
                  view === "history"
                    ? "bg-zinc-800 text-zinc-100"
                    : "text-zinc-400 hover:text-zinc-200",
                )}
              >
                History ({inbox?.total_terminal ?? 0})
              </button>
              <button
                type="button"
                onClick={() => setView("by-session")}
                className={cn(
                  "flex-1 rounded-md py-1.5 font-medium transition-colors",
                  view === "by-session"
                    ? "bg-zinc-800 text-zinc-100"
                    : "text-zinc-400 hover:text-zinc-200",
                )}
              >
                By Session
              </button>
            </div>
          </div>

          {/* Filters Bar */}
          <div className="flex flex-wrap gap-1 border-b border-zinc-800 px-3 py-2 text-xs">
            {(["all", "question", "approval", "checkpoint", "failure", "terminal"] as const).map(
              (k) => (
                <button
                  key={k}
                  type="button"
                  onClick={() => setKindFilter(k)}
                  className={cn(
                    "rounded-full px-2.5 py-1 text-xs font-medium transition-colors",
                    kindFilter === k
                      ? "bg-zinc-100 text-zinc-900"
                      : "bg-zinc-900 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200",
                  )}
                >
                  {k === "all" ? "All" : k.charAt(0).toUpperCase() + k.slice(1)}
                </button>
              ),
            )}
          </div>

          {/* Session Selector (when by-session or multiple sessions) */}
          {view === "by-session" && sessions.length > 0 && (
            <div className="border-b border-zinc-800 px-3 py-2">
              <label
                htmlFor="session-select"
                className="mb-1 block text-xs font-medium text-zinc-400"
              >
                Filter by Session
              </label>
              <select
                id="session-select"
                value={sessionFilter}
                onChange={(e) => setSessionFilter(e.target.value)}
                className="w-full rounded-md border border-zinc-800 bg-zinc-900 px-2 py-1 text-xs text-zinc-100 outline-none focus:border-zinc-600"
              >
                <option value="all">All Sessions ({allItems.length})</option>
                {sessions.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.id} ({s.count})
                  </option>
                ))}
              </select>
            </div>
          )}

          {/* Items Queue (Strict FIFO Monotonic Arrival Order) */}
          <div className="flex-1 overflow-y-auto">
            {filteredItems.length === 0 ? (
              <div className="p-8">
                <EmptyState
                  variant={kindFilter !== "all" || sessionFilter !== "all" ? "no-results" : "empty"}
                  title={view === "pending" ? "No pending turns" : "No turns found"}
                  description={
                    view === "pending"
                      ? "All live agent questions and checkpoints have been answered."
                      : "No turns match the selected filter."
                  }
                />
              </div>
            ) : (
              <div className="divide-y divide-zinc-800/60">
                {filteredItems.map((item) => {
                  const isSelected = activeItem?.item_id === item.item_id;
                  const color = KIND_COLORS[item.kind] ?? KIND_COLORS.question;
                  const isTerminal = item.state !== "presented" && item.state !== "staged";

                  return (
                    <button
                      key={item.item_id}
                      type="button"
                      onClick={() => handleSelect(item.item_id)}
                      className={cn(
                        "w-full text-left p-3.5 transition-colors flex flex-col gap-1.5",
                        isSelected ? "bg-zinc-900/90" : "hover:bg-zinc-900/40",
                      )}
                    >
                      <div className="flex items-center justify-between gap-2">
                        <div className="flex items-center gap-2 min-w-0">
                          <span className="font-mono text-xs text-zinc-500 font-semibold">
                            #{item.queue_sequence}
                          </span>
                          <span
                            className={cn(
                              "rounded px-1.5 py-0.5 text-xs font-medium border uppercase tracking-wider",
                              color.bg,
                              color.text,
                              color.border,
                            )}
                          >
                            {item.kind}
                          </span>
                          <span className="truncate text-xs font-semibold text-zinc-200">
                            {item.agent_label || item.agent_id}
                          </span>
                        </div>
                        <span className="shrink-0 text-xs text-zinc-500">
                          {new Date(item.created_at).toLocaleTimeString([], {
                            hour: "2-digit",
                            minute: "2-digit",
                          })}
                        </span>
                      </div>

                      <div className="text-xs font-medium text-zinc-100 line-clamp-1">
                        {item.title}
                      </div>
                      <div className="text-xs text-zinc-400 line-clamp-2">{item.content}</div>

                      <div className="flex items-center justify-between pt-1 text-xs text-zinc-500">
                        <span className="truncate max-w-[180px]">Session: {item.session_id}</span>
                        {isTerminal && (
                          <span
                            className={cn(
                              "flex items-center gap-1 text-xs font-medium",
                              item.delivery_state === "acknowledged"
                                ? "text-emerald-400"
                                : item.delivery_state === "terminal_failure"
                                  ? "text-rose-400"
                                  : "text-amber-400",
                            )}
                          >
                            {item.delivery_state === "acknowledged" && (
                              <Check className="h-3 w-3" />
                            )}
                            {item.delivery_state}
                          </span>
                        )}
                      </div>
                    </button>
                  );
                })}
              </div>
            )}
          </div>
        </div>

        {/* Right Column: Turn Inspection & Operator Actions */}
        <div className="flex min-w-0 flex-1 flex-col overflow-y-auto bg-zinc-950 p-6">
          {activeItem ? (
            <div className="mx-auto flex w-full max-w-3xl flex-col gap-6">
              {/* Item Header */}
              <div className="flex flex-col gap-2 border-b border-zinc-800 pb-4">
                <div className="flex items-center justify-between gap-3">
                  <div className="flex items-center gap-2.5">
                    <span className="font-mono text-sm font-bold text-zinc-400">
                      Sequence #{activeItem.queue_sequence}
                    </span>
                    <span
                      className={cn(
                        "rounded px-2 py-0.5 text-xs font-medium border uppercase tracking-wider",
                        KIND_COLORS[activeItem.kind]?.bg,
                        KIND_COLORS[activeItem.kind]?.text,
                        KIND_COLORS[activeItem.kind]?.border,
                      )}
                    >
                      {activeItem.kind}
                    </span>
                    <span className="text-xs text-zinc-400">
                      State: <strong className="text-zinc-200">{activeItem.state}</strong>
                    </span>
                  </div>
                  <span className="text-xs text-zinc-500">
                    Received {new Date(activeItem.created_at).toLocaleString()}
                  </span>
                </div>

                <h2 className="text-xl font-bold text-zinc-100">{activeItem.title}</h2>

                {activeItem.summary && (
                  <p className="text-sm text-zinc-400">{activeItem.summary}</p>
                )}

                {/* Metadata & Correlations */}
                <div className="flex flex-wrap items-center gap-4 text-xs text-zinc-400 pt-1">
                  <div>
                    Agent:{" "}
                    <span className="font-medium text-zinc-200">
                      {activeItem.agent_label || activeItem.agent_id}
                    </span>
                  </div>
                  <div>
                    Session:{" "}
                    <span className="font-mono text-zinc-300">{activeItem.session_id}</span>
                  </div>
                  <div>
                    Turn: <span className="font-mono text-zinc-300">{activeItem.turn_id}</span>
                  </div>
                  {Boolean(activeItem.correlations?.task_id) && (
                    <div className="flex items-center gap-1 rounded bg-zinc-900 px-2 py-0.5 border border-zinc-800">
                      <span>Task:</span>
                      <span className="font-medium text-amber-300">
                        {String(activeItem.correlations?.task_id)}
                      </span>
                    </div>
                  )}
                </div>
              </div>

              {/* Error Callout if any */}
              {actionError && (
                <Callout tone="danger">
                  {actionError}
                </Callout>
              )}

              {/* Message Content */}
              <div className="rounded-xl border border-zinc-800/80 bg-zinc-900/50 p-5">
                <div className="mb-2 text-xs font-semibold text-zinc-400 uppercase tracking-wider">
                  Agent Turn Message
                </div>
                <div className="prose prose-invert max-w-none text-sm leading-relaxed text-zinc-200">
                  <Markdown content={activeItem.content} />
                </div>
              </div>

              {/* Options List (if provided) */}
              {activeItem.options && activeItem.options.length > 0 && (
                <div className="flex flex-col gap-2">
                  <div className="text-xs font-semibold text-zinc-400 uppercase tracking-wider">
                    Selectable Options
                  </div>
                  <div className="grid gap-2">
                    {activeItem.options.map((opt) => (
                      <button
                        key={opt.value}
                        type="button"
                        onClick={() => setSelectedOption(opt.value)}
                        disabled={activeItem.state !== "presented" && activeItem.state !== "staged"}
                        className={cn(
                          "flex items-start justify-between rounded-lg border p-3 text-left transition-colors",
                          selectedOption === opt.value
                            ? "border-amber-400 bg-amber-950/20 text-zinc-100"
                            : "border-zinc-800 bg-zinc-900/40 text-zinc-300 hover:border-zinc-700",
                        )}
                      >
                        <div>
                          <div className="flex items-center gap-2 text-sm font-medium">
                            <span>{opt.label}</span>
                            {opt.recommended && (
                              <span className="rounded bg-amber-400/10 px-1.5 py-0.5 text-xs text-amber-300 font-normal">
                                Recommended
                              </span>
                            )}
                          </div>
                          {opt.description && (
                            <p className="mt-1 text-xs text-zinc-400">{opt.description}</p>
                          )}
                        </div>
                        <span className="font-mono text-xs text-zinc-500">{opt.value}</span>
                      </button>
                    ))}
                  </div>
                </div>
              )}

              {/* Resolution Display (if terminal) */}
              {activeItem.resolution && (
                <div className="rounded-xl border border-emerald-900/50 bg-emerald-950/20 p-5">
                  <div className="flex items-center justify-between pb-3 border-b border-emerald-900/40">
                    <div className="flex items-center gap-2 text-emerald-300 font-semibold text-sm">
                      <CheckCircle2 className="h-4 w-4" />
                      <span>Resolution: {activeItem.resolution.action.toUpperCase()}</span>
                    </div>
                    <span className="text-xs text-emerald-400/80">
                      Resolved {new Date(activeItem.resolution.resolved_at).toLocaleString()} by{" "}
                      {activeItem.resolution.resolved_by}
                    </span>
                  </div>

                  <div className="mt-3 flex flex-col gap-2 text-xs">
                    {activeItem.resolution.selected_option && (
                      <div>
                        <span className="text-zinc-400">Selected Option: </span>
                        <span className="font-semibold text-zinc-200">
                          {activeItem.resolution.selected_option}
                        </span>
                      </div>
                    )}
                    {activeItem.resolution.response_text && (
                      <div>
                        <span className="text-zinc-400">Operator Response: </span>
                        <p className="mt-1 rounded bg-zinc-900/80 p-2.5 text-sm text-zinc-100 whitespace-pre-wrap">
                          {activeItem.resolution.response_text}
                        </p>
                      </div>
                    )}
                    {activeItem.resolution.note && (
                      <div>
                        <span className="text-zinc-400">Note: </span>
                        <span className="text-zinc-300">{activeItem.resolution.note}</span>
                      </div>
                    )}

                    <div className="mt-2 flex items-center justify-between pt-2 border-t border-emerald-900/30 text-xs">
                      <span className="text-zinc-400">Tether Delivery State:</span>
                      <span
                        className={cn(
                          "font-medium uppercase tracking-wider",
                          activeItem.delivery_state === "acknowledged"
                            ? "text-emerald-400"
                            : "text-amber-400",
                        )}
                      >
                        {activeItem.delivery_state}
                      </span>
                    </div>
                  </div>
                </div>
              )}

              {/* Response Composer (if item is respondable) */}
              {(activeItem.state === "presented" || activeItem.state === "staged") && (
                <div className="flex flex-col gap-4 rounded-xl border border-zinc-800 bg-zinc-900/60 p-5">
                  <div className="text-xs font-semibold text-zinc-300 uppercase tracking-wider">
                    Operator Guidance & Response
                  </div>

                  <div className="flex flex-col gap-2">
                    <label htmlFor="turn-response" className="text-xs font-medium text-zinc-400">
                      Response Message
                    </label>
                    <textarea
                      id="turn-response"
                      rows={4}
                      value={responseText}
                      onChange={(e) => setResponseText(e.target.value)}
                      placeholder="Type your guidance or decision for the agent..."
                      className="w-full rounded-lg border border-zinc-800 bg-zinc-950 p-3 text-sm text-zinc-100 placeholder-zinc-500 outline-none focus:border-amber-400"
                    />
                  </div>

                  <div className="flex flex-col gap-2">
                    <label htmlFor="turn-note" className="text-xs font-medium text-zinc-400">
                      Internal Note (optional)
                    </label>
                    <input
                      id="turn-note"
                      type="text"
                      value={note}
                      onChange={(e) => setNote(e.target.value)}
                      placeholder="Context for review history..."
                      className="w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-xs text-zinc-100 placeholder-zinc-500 outline-none focus:border-amber-400"
                    />
                  </div>

                  {/* Actions Bar */}
                  <div className="flex items-center justify-between pt-2 border-t border-zinc-800/80">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => void handleDismiss()}
                      disabled={submitting}
                      className="text-zinc-400 hover:text-rose-400"
                    >
                      Dismiss Turn
                    </Button>

                    <div className="flex items-center gap-2">
                      {activeItem.kind === "approval" ? (
                        <>
                          <Button
                            variant="secondary"
                            size="sm"
                            onClick={() => void handleReply("reject")}
                            disabled={submitting}
                            className="bg-rose-950/60 text-rose-300 hover:bg-rose-900 border border-rose-800"
                          >
                            Reject
                          </Button>
                          <Button
                            variant="default"
                            size="sm"
                            onClick={() => void handleReply("approve")}
                            disabled={submitting}
                            className="bg-emerald-600 hover:bg-emerald-500 text-white font-medium"
                          >
                            Approve
                          </Button>
                        </>
                      ) : (
                        <Button
                          variant="default"
                          size="sm"
                          onClick={() => void handleReply("respond")}
                          disabled={submitting || (!responseText.trim() && !selectedOption)}
                          className="bg-amber-400 text-zinc-950 hover:bg-amber-300 font-medium"
                        >
                          Submit Response
                        </Button>
                      )}
                    </div>
                  </div>
                </div>
              )}
            </div>
          ) : (
            <div className="flex h-full items-center justify-center">
              <EmptyState
                variant="empty"
                title="No turn selected"
                description="Select an agent turn from the queue to review or respond."
              />
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
