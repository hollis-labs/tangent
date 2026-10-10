import { Button, Callout, EmptyState, LiveDot } from "@hollis-labs/design-components";
import { Archive, BookOpen, Check, RefreshCw, Search } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";

import { PageShell } from "@/components/layout/PageShell";
import { Markdown } from "@/components/markdown";
import { UiControl, useUiCommands, useViewDescriptor } from "@/hooks/useUiCommands";
import { useViewPresentation } from "@/hooks/useViewPresentation";
import {
  acknowledgeDoc,
  archiveDoc,
  type DocItemView,
  type DocsInbox,
  fetchDocItem,
  fetchDocsInbox,
  markDocRead,
} from "@/lib/docs-api";
import { idCommand } from "@/lib/ui-channel";
import { cn } from "@/lib/utils";

type ViewMode = "pending" | "archived";
type ReadFilter = "all" | "unread" | "read";
type SortOrder = "newest" | "oldest";

export default function DocsInboxRoute() {
  const { itemID } = useParams<{ itemID?: string }>();
  const navigate = useNavigate();

  const [inbox, setInbox] = useState<DocsInbox | null>(null);
  const [detachedItem, setDetachedItem] = useState<DocItemView | null>(null);
  const [view, setView] = useState<ViewMode>("pending");
  const [readFilter, setReadFilter] = useState<ReadFilter>("all");
  const [sortOrder, setSortOrder] = useState<SortOrder>("newest");
  const [search, setSearch] = useState("");
  const [syncStatus, setSyncStatus] = useState("Connecting to live docs stream…");
  const [error, setError] = useState("");
  const [actionError, setActionError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [bannerNotice, setBannerNotice] = useState<string | null>(null);
  const [ackNote, setAckNote] = useState("");

  const syncInbox = useCallback(async () => {
    try {
      const next = await fetchDocsInbox();
      let directItem: DocItemView | null = null;
      if (
        itemID &&
        !next.pending.some((it) => it.item_id === itemID) &&
        !next.history.some((it) => it.item_id === itemID)
      ) {
        try {
          directItem = await fetchDocItem(itemID);
        } catch {
          // direct item unavailable; keep null
        }
      }
      setInbox(next);
      setDetachedItem(directItem);
      setError("");
      setSyncStatus(`Synced at ${new Date(next.synced_at).toLocaleTimeString()}`);
    } catch (err) {
      setError((err as Error).message);
      setSyncStatus("Durable queue unavailable — retrying");
    }
  }, [itemID]);

  useEffect(() => {
    void syncInbox();
  }, [syncInbox]);

  useEffect(() => {
    const events = new EventSource("/api/docs/events");
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

  const filteredItems = useMemo(() => {
    let source = view === "pending" ? (inbox?.pending ?? []) : (inbox?.history ?? []);

    if (readFilter !== "all") {
      source = source.filter((item) =>
        readFilter === "unread" ? !item.read_at : Boolean(item.read_at),
      );
    }

    const query = search.trim().toLowerCase();
    if (query) {
      source = source.filter(
        (item) =>
          item.title.toLowerCase().includes(query) ||
          (item.summary ?? "").toLowerCase().includes(query) ||
          item.content_markdown.toLowerCase().includes(query) ||
          (item.agent_label ?? item.agent_id).toLowerCase().includes(query) ||
          (item.tags ?? []).some((tag) => tag.toLowerCase().includes(query)),
      );
    }

    const sorted = [...source].sort((a, b) => {
      const delta = new Date(a.created_at).getTime() - new Date(b.created_at).getTime();
      return sortOrder === "newest" ? -delta : delta;
    });
    return sorted;
  }, [inbox, view, readFilter, search, sortOrder]);

  const activeItem = useMemo(() => {
    if (itemID) {
      const found = allItems.find((it) => it.item_id === itemID);
      if (found) return found;
    }
    return filteredItems[0] ?? null;
  }, [itemID, allItems, filteredItems]);

  useEffect(() => {
    if (activeItem?.item_id) {
      setActionError("");
      setBannerNotice(null);
      setAckNote("");
    }
  }, [activeItem?.item_id]);

  const presentation = useViewPresentation(
    filteredItems.slice(0, 32).map((item) => ({
      id: item.item_id,
      title: item.title,
      body: <Markdown content={item.content_markdown} />,
    })),
    navigate,
  );
  const commands = useUiCommands([
    ...presentation.handlers,
    idCommand("open_doc", "ephemeral", (args) => presentation.open(args.id as string)),
  ]);
  const control = useViewDescriptor(
    {
      active_filters: [
        { name: "modal", values: [presentation.modalID ?? "closed"] },
        { name: "search", values: [search ? "active" : "empty"] },
        { name: "view", values: [view] },
        { name: "read", values: [readFilter] },
        { name: "sort", values: [sortOrder] },
      ],
      selected_ids: activeItem ? [activeItem.item_id] : [],
      visible_rows: filteredItems
        .slice(0, 32)
        .map((item) => ({ id: item.item_id, summary: "Document" })),
    },
    commands,
    search,
  );
  const handleSelect = (targetID: string) => {
    navigate(`/docs/items/${encodeURIComponent(targetID)}`);
  };

  const isTerminal = (item: DocItemView) =>
    item.state !== "presented" && item.state !== "staged" && item.state !== "in_progress";

  const handleMarkRead = async () => {
    if (!activeItem) return;
    setSubmitting(true);
    setActionError("");
    try {
      await markDocRead(activeItem.item_id);
      await syncInbox();
    } catch (err) {
      setActionError((err as Error).message);
    } finally {
      setSubmitting(false);
    }
  };

  const handleAcknowledge = async () => {
    if (!activeItem) return;
    setSubmitting(true);
    setActionError("");
    try {
      await acknowledgeDoc(activeItem.item_id, {
        expected_revision: activeItem.revision,
        note: ackNote.trim() || undefined,
      });
      setBannerNotice("Acknowledged");
      await syncInbox();
    } catch (err) {
      setActionError((err as Error).message);
    } finally {
      setSubmitting(false);
    }
  };

  const handleArchive = async () => {
    if (!activeItem) return;
    setSubmitting(true);
    setActionError("");
    try {
      await archiveDoc(activeItem.item_id, { expected_revision: activeItem.revision });
      setBannerNotice("Archived");
      await syncInbox();
      navigate("/docs");
    } catch (err) {
      setActionError((err as Error).message);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <PageShell className="flex flex-col overflow-hidden">
      <header className="flex shrink-0 items-center justify-between border-b border-zinc-800 bg-zinc-950/80 px-6 py-2.5 backdrop-blur">
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2">
            <BookOpen className="h-4 w-4 text-amber-400" />
            <h1 className="text-sm font-semibold tracking-tight text-zinc-100">Docs inbox</h1>
            <UiControl control={control} />
          </div>
          <div className="flex items-center gap-1.5 text-xs text-zinc-400">
            <LiveDot tone={error ? "danger" : "success"} pulsing={!error} />
            <span className="text-[11px]">{syncStatus}</span>
          </div>
        </div>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => void syncInbox()}
          className="h-7 px-2 text-zinc-400 hover:text-zinc-100"
          title="Refresh inbox"
        >
          <RefreshCw className="h-3.5 w-3.5" />
        </Button>
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

      <div className="flex min-h-0 flex-1">
        <div className="flex w-96 shrink-0 flex-col border-r border-zinc-800 bg-zinc-950">
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
                Inbox ({inbox?.total_pending ?? 0})
              </button>
              <button
                type="button"
                onClick={() => setView("archived")}
                className={cn(
                  "flex-1 rounded-md py-1.5 font-medium transition-colors",
                  view === "archived"
                    ? "bg-zinc-800 text-zinc-100 shadow-sm"
                    : "text-zinc-400 hover:text-zinc-200",
                )}
              >
                Archived ({inbox?.total_terminal ?? 0})
              </button>
            </div>
          </div>

          <div className="border-b border-zinc-800/80 px-3 py-2">
            <div className="relative">
              <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-zinc-500" />
              <input
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search title, content, agent, tags…"
                className="w-full rounded-md border border-zinc-800 bg-zinc-900 py-1.5 pl-7 pr-2 text-xs text-zinc-100 outline-none placeholder:text-zinc-600 focus:border-zinc-600"
              />
            </div>
          </div>

          <div className="flex items-center justify-between gap-2 border-b border-zinc-800/80 px-3 py-2 text-xs">
            <div className="flex gap-1">
              {(["all", "unread", "read"] as const).map((f) => (
                <button
                  key={f}
                  type="button"
                  onClick={() => setReadFilter(f)}
                  className={cn(
                    "rounded-full px-2.5 py-0.5 text-[11px] font-medium capitalize transition-colors",
                    readFilter === f
                      ? "bg-zinc-100 text-zinc-900"
                      : "bg-zinc-900 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200",
                  )}
                >
                  {f}
                </button>
              ))}
            </div>
            <select
              value={sortOrder}
              onChange={(e) => setSortOrder(e.target.value as SortOrder)}
              className="rounded-md border border-zinc-800 bg-zinc-900 px-1.5 py-1 text-[11px] text-zinc-300 outline-none focus:border-zinc-600"
              aria-label="Sort order"
            >
              <option value="newest">Newest first</option>
              <option value="oldest">Oldest first</option>
            </select>
          </div>

          <div className="flex-1 overflow-y-auto">
            {filteredItems.length === 0 ? (
              <div className="p-8">
                <EmptyState
                  variant={search || readFilter !== "all" ? "no-results" : "empty"}
                  title={view === "pending" ? "No documents" : "Nothing archived"}
                  description={
                    view === "pending"
                      ? "Documents agents send you show up here."
                      : "No archived documents match the current filter."
                  }
                />
              </div>
            ) : (
              <div className="divide-y divide-zinc-800/60">
                {filteredItems.map((item) => {
                  const isSelected = activeItem?.item_id === item.item_id;
                  const unread = !item.read_at;
                  return (
                    <button
                      key={item.item_id}
                      type="button"
                      ref={presentation.targetRef(item.item_id)}
                      onClick={() => handleSelect(item.item_id)}
                      className={cn(
                        "flex w-full flex-col gap-1.5 p-3.5 text-left transition-colors",
                        isSelected
                          ? "border-l-2 border-amber-400 bg-zinc-900 pl-[12px]"
                          : "hover:bg-zinc-900/40",
                      )}
                    >
                      <div className="flex items-center justify-between gap-2">
                        <div className="flex min-w-0 items-center gap-1.5">
                          {unread && (
                            <span
                              className="size-1.5 shrink-0 rounded-full bg-amber-400"
                              title="Unread"
                              aria-hidden="true"
                            />
                          )}
                          <span
                            className={cn(
                              "truncate text-xs",
                              unread ? "font-semibold text-zinc-100" : "font-medium text-zinc-300",
                            )}
                          >
                            {item.title}
                          </span>
                          {item.requires_ack && (
                            <span className="shrink-0 rounded border border-amber-800/80 bg-amber-950/60 px-1.5 py-0.2 text-[10px] font-medium uppercase tracking-wider text-amber-300">
                              Ack
                            </span>
                          )}
                        </div>
                        <span className="shrink-0 text-[11px] text-zinc-500">
                          {new Date(item.created_at).toLocaleTimeString([], {
                            hour: "2-digit",
                            minute: "2-digit",
                          })}
                        </span>
                      </div>
                      {item.summary && (
                        <div className="line-clamp-2 text-xs leading-relaxed text-zinc-400">
                          {item.summary}
                        </div>
                      )}
                      <div className="flex items-center justify-between pt-1 text-[11px] text-zinc-500">
                        <span className="truncate">{item.agent_label || item.agent_id}</span>
                        {item.resolution && (
                          <span className="flex items-center gap-1 font-medium text-emerald-400">
                            <Check className="h-3 w-3" />
                            acknowledged
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

        <div className="flex min-w-0 flex-1 flex-col overflow-y-auto bg-zinc-950 p-6">
          {activeItem ? (
            <div className="mx-auto flex w-full max-w-3xl flex-col gap-5">
              <div className="flex flex-col gap-3 border-b border-zinc-800 pb-4">
                <div className="flex flex-wrap items-center gap-2">
                  {activeItem.read_at ? (
                    <span className="rounded border border-zinc-700 bg-zinc-900 px-2 py-0.5 text-[11px] font-medium uppercase tracking-wider text-zinc-400">
                      Read
                    </span>
                  ) : (
                    <span className="rounded border border-amber-800/80 bg-amber-950/60 px-2 py-0.5 text-[11px] font-medium uppercase tracking-wider text-amber-300">
                      Unread
                    </span>
                  )}
                  {activeItem.requires_ack && (
                    <span className="rounded border border-zinc-700 bg-zinc-900 px-2 py-0.5 text-[11px] font-medium uppercase tracking-wider text-zinc-400">
                      {activeItem.resolution ? "Acknowledged" : "Needs acknowledgment"}
                    </span>
                  )}
                  {(activeItem.tags ?? []).map((tag) => (
                    <span
                      key={tag}
                      className="rounded bg-zinc-900 px-1.5 py-0.5 text-[11px] text-zinc-400"
                    >
                      #{tag}
                    </span>
                  ))}
                </div>
                <h2 className="text-xl font-bold tracking-tight text-zinc-100">
                  {activeItem.title}
                </h2>
                <div className="flex flex-wrap items-center gap-3 text-xs text-zinc-400">
                  <span>
                    From{" "}
                    <strong className="text-zinc-200">
                      {activeItem.agent_label || activeItem.agent_id}
                    </strong>
                  </span>
                  <span>{new Date(activeItem.created_at).toLocaleString()}</span>
                </div>
                <div className="flex items-center gap-2 pt-1">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => void handleMarkRead()}
                    disabled={submitting || Boolean(activeItem.read_at)}
                  >
                    {activeItem.read_at ? "Marked read" : "Mark read"}
                  </Button>
                  {activeItem.requires_ack && !activeItem.resolution && (
                    <Button
                      variant="default"
                      size="sm"
                      onClick={() => void handleAcknowledge()}
                      disabled={submitting}
                    >
                      Acknowledge
                    </Button>
                  )}
                  {!isTerminal(activeItem) && (
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => void handleArchive()}
                      disabled={submitting}
                      className="text-zinc-400 hover:text-rose-300"
                    >
                      <Archive className="mr-1.5 h-3.5 w-3.5" />
                      Delete
                    </Button>
                  )}
                </div>
              </div>

              {actionError && <Callout tone="danger">{actionError}</Callout>}

              <div className="rounded-xl border border-zinc-800/80 bg-zinc-900/50 p-5 shadow-sm">
                <div className="prose prose-invert max-w-none text-sm leading-relaxed text-zinc-200">
                  <Markdown content={activeItem.content_markdown} />
                </div>
              </div>

              {activeItem.resolution?.note && (
                <div className="rounded-lg border border-zinc-800/80 bg-zinc-950/80 p-3 text-xs text-zinc-300">
                  <div className="mb-1 font-semibold uppercase tracking-wider text-zinc-500">
                    Your note
                  </div>
                  {activeItem.resolution.note}
                </div>
              )}
            </div>
          ) : (
            <div className="flex h-full items-center justify-center">
              <EmptyState
                variant="empty"
                title="No document selected"
                description="Select a document from the inbox to read it."
              />
            </div>
          )}
        </div>
      </div>
      {presentation.dialog}
    </PageShell>
  );
}
