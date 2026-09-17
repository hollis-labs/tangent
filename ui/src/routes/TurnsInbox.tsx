import { Button, Callout, EmptyState, LiveDot } from "@hollis-labs/design-components";
import {
  Bot,
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  Clock,
  CornerDownLeft,
  Layers,
  MessageSquare,
  RefreshCw,
  Terminal,
  Zap,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";

import { PageShell } from "@/components/layout/PageShell";
import { Markdown } from "@/components/markdown";
import {
  dismissTurn,
  fetchSessionReplies,
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
type DetailTab = "conversation" | "technical";

const KIND_COLORS: Record<TurnKind, { bg: string; text: string; border: string }> = {
  question: { bg: "bg-sky-950/60", text: "text-sky-300", border: "border-sky-800/80" },
  approval: { bg: "bg-amber-950/60", text: "text-amber-300", border: "border-amber-800/80" },
  checkpoint: { bg: "bg-violet-950/60", text: "text-violet-300", border: "border-violet-800/80" },
  failure: { bg: "bg-rose-950/60", text: "text-rose-300", border: "border-rose-800/80" },
  terminal: { bg: "bg-emerald-950/60", text: "text-emerald-300", border: "border-emerald-800/80" },
};

const QUICK_RESPONSES = ["Proceed", "Looks good", "Yes", "No", "Please explain further"];

export default function TurnsInboxRoute() {
  const { itemID } = useParams<{ itemID?: string }>();
  const navigate = useNavigate();

  const [inbox, setInbox] = useState<TurnsInbox | null>(null);
  const [detachedItem, setDetachedItem] = useState<TurnItemView | null>(null);
  const [view, setView] = useState<ViewMode>("pending");
  const [kindFilter, setKindFilter] = useState<KindFilter>("all");
  const [sessionFilter, setSessionFilter] = useState<string>("all");
  const [agentFilter, setAgentFilter] = useState<string>("all");
  const [detailTab, setDetailTab] = useState<DetailTab>("conversation");
  const [showSessionHistory, setShowSessionHistory] = useState(true);
  const [sessionReplies, setSessionReplies] = useState<TurnItemView[]>([]);
  const [syncStatus, setSyncStatus] = useState("Connecting to live turns stream…");
  const [error, setError] = useState("");
  const [actionError, setActionError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [bannerNotice, setBannerNotice] = useState<string | null>(null);

  // Composer fields
  const [responseText, setResponseText] = useState("");
  const [selectedOption, setSelectedOption] = useState("");
  const [note, setNote] = useState("");

  const syncSequence = useRef(0);
  const activeItemID = useRef(itemID);
  activeItemID.current = itemID;
  const textareaRef = useRef<HTMLTextAreaElement>(null);

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

  const agents = useMemo(() => {
    const map = new Map<string, { id: string; label: string; count: number }>();
    for (const it of allItems) {
      const key = it.agent_id || "unknown";
      const existing = map.get(key);
      if (existing) {
        existing.count++;
      } else {
        map.set(key, { id: key, label: it.agent_label || key, count: 1 });
      }
    }
    return Array.from(map.values());
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
      if (agentFilter !== "all" && item.agent_id !== agentFilter) return false;
      return true;
    });
  }, [inbox, allItems, view, kindFilter, sessionFilter, agentFilter]);

  const activeItem = useMemo(() => {
    if (itemID) {
      const found = allItems.find((it) => it.item_id === itemID);
      if (found) return found;
    }
    return filteredItems[0] ?? null;
  }, [itemID, allItems, filteredItems]);

  const activeIndex = useMemo(() => {
    if (!activeItem) return -1;
    return filteredItems.findIndex((it) => it.item_id === activeItem.item_id);
  }, [activeItem, filteredItems]);

  // Fetch session history for dialogue context
  useEffect(() => {
    if (!activeItem?.session_id) {
      setSessionReplies([]);
      return;
    }
    let cancelled = false;
    fetchSessionReplies(activeItem.session_id)
      .then((res) => {
        if (!cancelled) {
          setSessionReplies(res.replies || []);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setSessionReplies([]);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [activeItem?.session_id]);

  // Separate previous turns in this session
  const earlierSessionTurns = useMemo(() => {
    if (!activeItem) return [];
    return sessionReplies.filter(
      (it) => it.item_id !== activeItem.item_id && it.queue_sequence < activeItem.queue_sequence,
    );
  }, [sessionReplies, activeItem]);

  // Reset composer on item change
  useEffect(() => {
    if (activeItem?.item_id) {
      setResponseText("");
      setSelectedOption("");
      setNote("");
      setActionError("");
      setBannerNotice(null);
    }
  }, [activeItem?.item_id]);

  const handleSelect = (targetID: string) => {
    navigate(`/turns/items/${encodeURIComponent(targetID)}`);
  };

  const handleNavigateStep = (direction: "prev" | "next") => {
    if (activeIndex === -1 || filteredItems.length <= 1) return;
    const targetIdx = direction === "next" ? activeIndex + 1 : activeIndex - 1;
    if (targetIdx >= 0 && targetIdx < filteredItems.length) {
      navigate(`/turns/items/${encodeURIComponent(filteredItems[targetIdx].item_id)}`);
    }
  };

  // Keyboard navigation shortcuts
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      // Avoid intercepting when user is typing in inputs or textarea
      const tag = (e.target as HTMLElement)?.tagName?.toLowerCase();
      const isInput = tag === "input" || tag === "textarea" || tag === "select";

      if (!isInput) {
        if (e.key === "[" || (e.altKey && e.key === "ArrowUp")) {
          e.preventDefault();
          handleNavigateStep("prev");
        } else if (e.key === "]" || (e.altKey && e.key === "ArrowDown")) {
          e.preventDefault();
          handleNavigateStep("next");
        } else if (activeItem?.options && e.key >= "1" && e.key <= "9") {
          const idx = Number.parseInt(e.key, 10) - 1;
          if (idx < activeItem.options.length) {
            e.preventDefault();
            setSelectedOption(activeItem.options[idx].value);
          }
        }
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  });

  const advanceToNextPending = () => {
    if (!activeItem) return;
    const remaining = (inbox?.pending ?? []).filter((it) => it.item_id !== activeItem.item_id);
    if (remaining.length > 0) {
      navigate(`/turns/items/${encodeURIComponent(remaining[0].item_id)}`);
    } else {
      navigate("/turns");
    }
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
      setBannerNotice(`Reply delivered for turn #${activeItem.queue_sequence}`);
      await syncInbox();
      advanceToNextPending();
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
      setBannerNotice(`Turn #${activeItem.queue_sequence} dismissed`);
      await syncInbox();
      advanceToNextPending();
    } catch (err) {
      setActionError((err as Error).message);
    } finally {
      setSubmitting(false);
    }
  };

  const handleComposerKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      if (!submitting) {
        if (activeItem?.kind === "approval") {
          void handleReply("approve");
        } else if (responseText.trim() || selectedOption) {
          void handleReply("respond");
        }
      }
    }
  };

  const handleCannedInsert = (phrase: string) => {
    setResponseText((prev) => (prev ? `${prev.trim()} ${phrase}` : phrase));
    textareaRef.current?.focus();
  };

  return (
    <PageShell className="flex flex-col overflow-hidden">
      {/* Top Navigation & Status Bar */}
      <header className="flex shrink-0 items-center justify-between border-b border-zinc-800 bg-zinc-950/80 px-6 py-2.5 backdrop-blur">
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2">
            <Bot className="h-4 w-4 text-amber-400" />
            <h1 className="text-sm font-semibold tracking-tight text-zinc-100">
              Agent turns FIFO inbox
            </h1>
          </div>
          <span className="rounded border border-zinc-800 bg-zinc-900 px-2 py-0.5 font-mono text-[11px] text-zinc-400">
            surface_turns_default
          </span>
          <div className="flex items-center gap-1.5 text-xs text-zinc-400">
            <LiveDot tone={error ? "danger" : "success"} pulsing={!error} />
            <span className="text-[11px]">{syncStatus}</span>
          </div>
        </div>

        <div className="flex items-center gap-3 text-xs text-zinc-400">
          <span className="hidden text-[11px] text-zinc-500 sm:inline">
            ⌘+Enter submit · [ / ] navigate
          </span>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => void syncInbox()}
            className="h-7 px-2 text-zinc-400 hover:text-zinc-100"
            title="Refresh inbox"
          >
            <RefreshCw className="h-3.5 w-3.5" />
          </Button>
        </div>
      </header>

      {bannerNotice && (
        <div className="flex items-center justify-between border-b border-emerald-900/50 bg-emerald-950/40 px-6 py-1.5 text-xs text-emerald-300">
          <div className="flex items-center gap-2">
            <Check className="h-3.5 w-3.5" />
            <span>{bannerNotice}</span>
          </div>
          <button
            type="button"
            onClick={() => setBannerNotice(null)}
            className="text-emerald-400 hover:text-emerald-200"
          >
            Dismiss
          </button>
        </div>
      )}

      {/* Main Workspace Area */}
      <div className="flex min-h-0 flex-1">
        {/* Left Column: Arrival Queue & Filters */}
        <div className="flex w-96 shrink-0 flex-col border-r border-zinc-800 bg-zinc-950">
          {/* View Mode Switcher */}
          <div className="border-b border-zinc-800 p-2.5">
            <div className="flex w-full rounded-lg bg-zinc-900 p-1 text-xs">
              <button
                type="button"
                onClick={() => setView("pending")}
                className={cn(
                  "flex-1 rounded-md py-1.5 font-medium transition-colors",
                  view === "pending"
                    ? "bg-zinc-800 text-zinc-100 shadow-sm"
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
                    ? "bg-zinc-800 text-zinc-100 shadow-sm"
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
                    ? "bg-zinc-800 text-zinc-100 shadow-sm"
                    : "text-zinc-400 hover:text-zinc-200",
                )}
              >
                By Session
              </button>
            </div>
          </div>

          {/* Kind Filter Chips */}
          <div className="flex flex-wrap gap-1 border-b border-zinc-800/80 px-3 py-2 text-xs">
            {(["all", "question", "approval", "checkpoint", "failure", "terminal"] as const).map(
              (k) => (
                <button
                  key={k}
                  type="button"
                  onClick={() => setKindFilter(k)}
                  className={cn(
                    "rounded-full px-2.5 py-0.5 text-[11px] font-medium transition-colors",
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

          {/* Agent Filter Selector (when multiple agents exist) */}
          {agents.length > 1 && (
            <div className="flex items-center gap-2 border-b border-zinc-800/80 px-3 py-1.5 text-xs">
              <span className="text-[11px] text-zinc-500 font-medium shrink-0">Agent:</span>
              <div className="flex flex-1 gap-1 overflow-x-auto py-0.5">
                <button
                  type="button"
                  onClick={() => setAgentFilter("all")}
                  className={cn(
                    "rounded px-2 py-0.5 text-[11px] whitespace-nowrap",
                    agentFilter === "all"
                      ? "bg-amber-400/20 text-amber-300 font-medium"
                      : "text-zinc-400 hover:text-zinc-200",
                  )}
                >
                  All ({allItems.length})
                </button>
                {agents.map((ag) => (
                  <button
                    key={ag.id}
                    type="button"
                    onClick={() => setAgentFilter(ag.id)}
                    className={cn(
                      "rounded px-2 py-0.5 text-[11px] whitespace-nowrap",
                      agentFilter === ag.id
                        ? "bg-amber-400/20 text-amber-300 font-medium"
                        : "text-zinc-400 hover:text-zinc-200",
                    )}
                  >
                    {ag.label} ({ag.count})
                  </button>
                ))}
              </div>
            </div>
          )}

          {/* Session Selector (when by-session mode is active) */}
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
                  variant={
                    kindFilter !== "all" || sessionFilter !== "all" || agentFilter !== "all"
                      ? "no-results"
                      : "empty"
                  }
                  title={view === "pending" ? "No pending turns" : "No turns found"}
                  description={
                    view === "pending"
                      ? "All live agent questions, approvals, and checkpoints have been addressed."
                      : "No turns match the current filter criteria."
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
                        isSelected
                          ? "bg-zinc-900 border-l-2 border-amber-400 pl-[12px]"
                          : "hover:bg-zinc-900/40",
                      )}
                    >
                      <div className="flex items-center justify-between gap-2">
                        <div className="flex items-center gap-1.5 min-w-0">
                          <span className="font-mono text-xs font-semibold text-zinc-500">
                            #{item.queue_sequence}
                          </span>
                          <span
                            className={cn(
                              "rounded px-1.5 py-0.2 text-[10px] font-medium border uppercase tracking-wider",
                              color.bg,
                              color.text,
                              color.border,
                            )}
                          >
                            {item.kind}
                          </span>
                          <span className="truncate text-xs font-medium text-zinc-300">
                            {item.agent_label || item.agent_id}
                          </span>
                        </div>
                        <span className="shrink-0 text-[11px] text-zinc-500">
                          {new Date(item.created_at).toLocaleTimeString([], {
                            hour: "2-digit",
                            minute: "2-digit",
                          })}
                        </span>
                      </div>

                      <div className="text-xs font-medium text-zinc-100 line-clamp-1">
                        {item.title}
                      </div>
                      <div className="text-xs text-zinc-400 line-clamp-2 leading-relaxed">
                        {item.content}
                      </div>

                      <div className="flex items-center justify-between pt-1 text-[11px] text-zinc-500">
                        <span className="truncate max-w-[170px] font-mono">{item.session_id}</span>
                        {isTerminal && (
                          <span
                            className={cn(
                              "flex items-center gap-1 text-[11px] font-medium",
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

        {/* Right Column: Turn Inspection, Dialogue Stream & Operator Actions */}
        <div className="flex min-w-0 flex-1 flex-col overflow-y-auto bg-zinc-950 p-6">
          {activeItem ? (
            <div className="mx-auto flex w-full max-w-3xl flex-col gap-5">
              {/* Item Header & Navigation Bar */}
              <div className="flex flex-col gap-3 border-b border-zinc-800 pb-4">
                <div className="flex items-center justify-between gap-3">
                  <div className="flex items-center gap-2.5">
                    <span className="font-mono text-sm font-bold text-amber-300">
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

                  {/* Queue step navigation buttons */}
                  <div className="flex items-center gap-1.5">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => handleNavigateStep("prev")}
                      disabled={activeIndex <= 0}
                      className="h-7 px-2 text-zinc-400 hover:text-zinc-100"
                      title="Previous turn ([ or Alt+Up)"
                    >
                      <ChevronUp className="h-4 w-4" />
                    </Button>
                    <span className="text-xs text-zinc-500 font-mono">
                      {activeIndex + 1} / {filteredItems.length}
                    </span>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => handleNavigateStep("next")}
                      disabled={activeIndex === -1 || activeIndex >= filteredItems.length - 1}
                      className="h-7 px-2 text-zinc-400 hover:text-zinc-100"
                      title="Next turn (] or Alt+Down)"
                    >
                      <ChevronDown className="h-4 w-4" />
                    </Button>
                  </div>
                </div>

                <h2 className="text-xl font-bold text-zinc-100 tracking-tight">
                  {activeItem.title}
                </h2>

                {activeItem.summary && (
                  <div className="text-sm text-zinc-400">
                    <Markdown content={activeItem.summary} />
                  </div>
                )}

                {/* Metadata Badges */}
                <div className="flex flex-wrap items-center gap-3 pt-1 text-xs text-zinc-400">
                  <div className="flex items-center gap-1.5 rounded bg-zinc-900 px-2 py-1 border border-zinc-800">
                    <Bot className="h-3.5 w-3.5 text-amber-400" />
                    <span>Agent:</span>
                    <strong className="text-zinc-200">
                      {activeItem.agent_label || activeItem.agent_id}
                    </strong>
                  </div>

                  <div className="flex items-center gap-1.5 rounded bg-zinc-900 px-2 py-1 border border-zinc-800">
                    <Terminal className="h-3.5 w-3.5 text-sky-400" />
                    <span>Session:</span>
                    <span className="font-mono text-zinc-300">{activeItem.session_id}</span>
                  </div>

                  <div className="flex items-center gap-1.5 rounded bg-zinc-900 px-2 py-1 border border-zinc-800">
                    <Clock className="h-3.5 w-3.5 text-zinc-400" />
                    <span>Turn:</span>
                    <span className="font-mono text-zinc-300">{activeItem.turn_id}</span>
                  </div>

                  {Boolean(activeItem.correlations?.task_id) && (
                    <div className="flex items-center gap-1.5 rounded bg-amber-950/30 px-2 py-1 border border-amber-800/60">
                      <Layers className="h-3.5 w-3.5 text-amber-400" />
                      <span>Task:</span>
                      <span className="font-mono font-medium text-amber-300">
                        {String(activeItem.correlations?.task_id)}
                      </span>
                    </div>
                  )}
                </div>

                {/* Sub-tab view toggle: Turn Conversation vs Technical Telemetry */}
                <div className="flex items-center gap-4 pt-2 border-t border-zinc-800/80 text-xs">
                  <button
                    type="button"
                    onClick={() => setDetailTab("conversation")}
                    className={cn(
                      "pb-1 border-b-2 font-medium transition-colors",
                      detailTab === "conversation"
                        ? "border-amber-400 text-amber-300"
                        : "border-transparent text-zinc-400 hover:text-zinc-200",
                    )}
                  >
                    Interactive Turn & Context
                  </button>
                  <button
                    type="button"
                    onClick={() => setDetailTab("technical")}
                    className={cn(
                      "pb-1 border-b-2 font-medium transition-colors",
                      detailTab === "technical"
                        ? "border-amber-400 text-amber-300"
                        : "border-transparent text-zinc-400 hover:text-zinc-200",
                    )}
                  >
                    Technical Context & Raw Telemetry
                  </button>
                </div>
              </div>

              {/* Error Callout if any */}
              {actionError && <Callout tone="danger">{actionError}</Callout>}

              {detailTab === "conversation" ? (
                <>
                  {/* Session Context: Earlier Turns in this Session */}
                  {earlierSessionTurns.length > 0 && (
                    <div className="rounded-xl border border-zinc-800 bg-zinc-900/40 p-4">
                      <button
                        type="button"
                        onClick={() => setShowSessionHistory((prev) => !prev)}
                        className="flex w-full items-center justify-between text-xs font-semibold uppercase tracking-wider text-zinc-400 hover:text-zinc-200"
                      >
                        <div className="flex items-center gap-2">
                          <MessageSquare className="h-3.5 w-3.5 text-amber-400" />
                          <span>Session Context ({earlierSessionTurns.length} earlier turns)</span>
                        </div>
                        {showSessionHistory ? (
                          <ChevronUp className="h-4 w-4" />
                        ) : (
                          <ChevronDown className="h-4 w-4" />
                        )}
                      </button>

                      {showSessionHistory && (
                        <div className="mt-3 divide-y divide-zinc-800/60 pt-2 text-xs">
                          {earlierSessionTurns.map((prevTurn) => (
                            <div key={prevTurn.item_id} className="py-2.5 flex flex-col gap-1.5">
                              <div className="flex items-center justify-between text-[11px] text-zinc-500">
                                <span className="font-mono font-semibold">
                                  #{prevTurn.queue_sequence} {prevTurn.title}
                                </span>
                                <span>{new Date(prevTurn.created_at).toLocaleTimeString()}</span>
                              </div>
                              <div className="text-zinc-300 line-clamp-2">
                                <Markdown content={prevTurn.content} />
                              </div>
                              {prevTurn.resolution && (
                                <div className="mt-1 rounded bg-zinc-950/80 p-2 border border-zinc-800/80 text-zinc-200">
                                  <div className="flex items-center gap-1.5 text-[11px] text-emerald-400 font-medium">
                                    <Check className="h-3 w-3" />
                                    <span>
                                      Operator: {prevTurn.resolution.action.toUpperCase()}
                                    </span>
                                  </div>
                                  {prevTurn.resolution.response_text && (
                                    <p className="mt-1 text-xs text-zinc-300">
                                      {prevTurn.resolution.response_text}
                                    </p>
                                  )}
                                </div>
                              )}
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  )}

                  {/* Active Message Content Box */}
                  <div className="rounded-xl border border-zinc-800/80 bg-zinc-900/50 p-5 shadow-sm">
                    <div className="mb-2.5 flex items-center justify-between text-xs font-semibold uppercase tracking-wider text-zinc-400">
                      <span>Agent Message</span>
                      <span className="text-[11px] text-zinc-500">
                        {new Date(activeItem.created_at).toLocaleTimeString()}
                      </span>
                    </div>
                    <div className="prose prose-invert max-w-none text-sm leading-relaxed text-zinc-200">
                      <Markdown content={activeItem.content} />
                    </div>
                  </div>

                  {/* Selectable Options List (if provided) */}
                  {activeItem.options && activeItem.options.length > 0 && (
                    <div className="flex flex-col gap-2">
                      <div className="text-xs font-semibold uppercase tracking-wider text-zinc-400">
                        Selectable Options (Press 1–{activeItem.options.length} to choose)
                      </div>
                      <div className="grid gap-2">
                        {activeItem.options.map((opt, idx) => (
                          <button
                            key={opt.value}
                            type="button"
                            onClick={() => setSelectedOption(opt.value)}
                            disabled={
                              activeItem.state !== "presented" && activeItem.state !== "staged"
                            }
                            className={cn(
                              "flex items-start justify-between rounded-lg border p-3.5 text-left transition-colors",
                              selectedOption === opt.value
                                ? "border-amber-400 bg-amber-950/20 text-zinc-100 shadow-sm"
                                : "border-zinc-800 bg-zinc-900/40 text-zinc-300 hover:border-zinc-700",
                            )}
                          >
                            <div className="flex items-start gap-3">
                              <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded bg-zinc-800 font-mono text-xs text-zinc-400">
                                {idx + 1}
                              </span>
                              <div>
                                <div className="flex items-center gap-2 text-sm font-medium">
                                  <span>{opt.label}</span>
                                  {opt.recommended && (
                                    <span className="rounded bg-amber-400/10 px-1.5 py-0.5 text-xs font-normal text-amber-300 border border-amber-400/20">
                                      Recommended
                                    </span>
                                  )}
                                </div>
                                {opt.description && (
                                  <div className="mt-1 text-xs text-zinc-400">
                                    <Markdown content={opt.description} />
                                  </div>
                                )}
                              </div>
                            </div>
                            <span className="font-mono text-xs text-zinc-500">{opt.value}</span>
                          </button>
                        ))}
                      </div>
                    </div>
                  )}

                  {/* Resolution Record Display (if terminal) */}
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

                      <div className="mt-3 flex flex-col gap-2.5 text-xs">
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
                            <p className="mt-1 rounded bg-zinc-900/80 p-3 text-sm text-zinc-100 whitespace-pre-wrap leading-relaxed border border-zinc-800">
                              {activeItem.resolution.response_text}
                            </p>
                          </div>
                        )}
                        {activeItem.resolution.note && (
                          <div>
                            <span className="text-zinc-400">Internal Note: </span>
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

                  {/* Response Composer (when item is presented/staged) */}
                  {(activeItem.state === "presented" || activeItem.state === "staged") && (
                    <div className="flex flex-col gap-4 rounded-xl border border-zinc-800 bg-zinc-900/60 p-5 shadow-sm">
                      <div className="flex items-center justify-between">
                        <div className="text-xs font-semibold uppercase tracking-wider text-zinc-300">
                          Operator Guidance & Response
                        </div>
                        <div className="flex items-center gap-1.5 text-[11px] text-amber-400/90">
                          <Zap className="h-3 w-3" />
                          <span>Delivers immediately if idle, or next stop if busy</span>
                        </div>
                      </div>

                      {/* Quick Canned Responses */}
                      <div className="flex flex-wrap items-center gap-1.5">
                        <span className="text-[11px] text-zinc-500 font-medium">Quick chips:</span>
                        {QUICK_RESPONSES.map((phrase) => (
                          <button
                            key={phrase}
                            type="button"
                            onClick={() => handleCannedInsert(phrase)}
                            className="rounded-full bg-zinc-800 px-2.5 py-1 text-[11px] font-medium text-zinc-300 hover:bg-zinc-700 hover:text-zinc-100 transition-colors"
                          >
                            {phrase}
                          </button>
                        ))}
                      </div>

                      <div className="flex flex-col gap-2">
                        <label
                          htmlFor="turn-response"
                          className="text-xs font-medium text-zinc-400 flex items-center justify-between"
                        >
                          <span>Response Message</span>
                          <span className="text-[11px] text-zinc-500">⌘+Enter to submit</span>
                        </label>
                        <textarea
                          ref={textareaRef}
                          id="turn-response"
                          rows={4}
                          value={responseText}
                          onChange={(e) => setResponseText(e.target.value)}
                          onKeyDown={handleComposerKeyDown}
                          placeholder="Type your guidance or decision for the agent..."
                          className="w-full rounded-lg border border-zinc-800 bg-zinc-950 p-3 text-sm text-zinc-100 placeholder-zinc-500 outline-none focus:border-amber-400 transition-colors"
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
                          className="w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-xs text-zinc-100 placeholder-zinc-500 outline-none focus:border-amber-400 transition-colors"
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
                              className="bg-amber-400 text-zinc-950 hover:bg-amber-300 font-medium flex items-center gap-1.5"
                            >
                              <span>Submit Response</span>
                              <CornerDownLeft className="h-3 w-3 text-zinc-900" />
                            </Button>
                          )}
                        </div>
                      </div>
                    </div>
                  )}
                </>
              ) : (
                /* Technical Telemetry & Correlations Tab */
                <div className="flex flex-col gap-4 rounded-xl border border-zinc-800 bg-zinc-900/40 p-5">
                  <div className="text-xs font-semibold uppercase tracking-wider text-zinc-300">
                    Raw Telemetry & Interaction Substrate
                  </div>

                  <div className="grid grid-cols-2 gap-3 text-xs">
                    <div className="rounded bg-zinc-950 p-3 border border-zinc-800">
                      <span className="text-zinc-500 block mb-1">Durable Interaction ID</span>
                      <span className="font-mono text-zinc-200">{activeItem.item_id}</span>
                    </div>

                    <div className="rounded bg-zinc-950 p-3 border border-zinc-800">
                      <span className="text-zinc-500 block mb-1">Originating Turn ID</span>
                      <span className="font-mono text-zinc-200">{activeItem.turn_id}</span>
                    </div>

                    <div className="rounded bg-zinc-950 p-3 border border-zinc-800">
                      <span className="text-zinc-500 block mb-1">Tether Session ID</span>
                      <span className="font-mono text-zinc-200">{activeItem.session_id}</span>
                    </div>

                    <div className="rounded bg-zinc-950 p-3 border border-zinc-800">
                      <span className="text-zinc-500 block mb-1">Queue Sequence</span>
                      <span className="font-mono text-zinc-200">
                        #{activeItem.queue_sequence} (rev: {activeItem.revision})
                      </span>
                    </div>
                  </div>

                  {activeItem.correlations && (
                    <div className="flex flex-col gap-2 pt-2">
                      <span className="text-xs font-medium text-zinc-400">
                        Correlations Dictionary:
                      </span>
                      <pre className="rounded-lg bg-zinc-950 p-3 font-mono text-xs text-zinc-300 overflow-x-auto border border-zinc-800">
                        {JSON.stringify(activeItem.correlations, null, 2)}
                      </pre>
                    </div>
                  )}

                  <div className="flex flex-col gap-2 pt-2">
                    <span className="text-xs font-medium text-zinc-400">
                      Complete Item Payload:
                    </span>
                    <pre className="rounded-lg bg-zinc-950 p-3 font-mono text-[11px] text-zinc-400 overflow-x-auto max-h-72 border border-zinc-800">
                      {JSON.stringify(activeItem, null, 2)}
                    </pre>
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
    </PageShell>
  );
}
