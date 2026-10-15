import { ArrowLeft, Expand, Minimize, RefreshCw } from "lucide-react";
import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";

import { EnvelopeRouter } from "@/components/envelopes/EnvelopeRouter";
import { PageShell } from "@/components/layout/PageShell";
import { ResponseSummary } from "@/components/ResponseSummary";
import { UiControl, useUiCommands, useViewDescriptor } from "@/hooks/useUiCommands";
import { useViewPresentation } from "@/hooks/useViewPresentation";
import { createCoalescer } from "@/lib/coalesce";
import { subscribeRevision, visiblePoll } from "@/lib/event-hub";
import {
  categoryOf,
  entrySource,
  entryTitle,
  fetchInbox,
  fetchInboxItem,
  type InboxCache,
  type InboxEntry,
  isTerminal,
  searchInbox,
} from "@/lib/inbox-api";
import { idCommand, type UiHandler } from "@/lib/ui-channel";
import { cn } from "@/lib/utils";
import { InboxItemBody } from "./InboxItemBody";
import Room from "./Room";

const labels = {
  approval: "Approval",
  document: "Document",
  turn: "Agent turn",
  workflow: "Interaction",
};

// SSE is the signal; these are only safety nets.
const EMPTY_IDS: ReadonlySet<string> = new Set();
const INBOX_FALLBACK_POLL_MS = 30_000;
const INBOX_DEGRADED_POLL_MS = 3_000;

export default function Inbox() {
  const { itemID, roomID } = useParams<{ itemID?: string; roomID?: string }>();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const view = params.get("view") || "pending";
  const category = params.get("type") || "all";
  const sort = params.get("sort") || "newest";
  const search = params.get("q") || "";
  const [entries, setEntries] = useState<InboxEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [expanded, setExpanded] = useState(false);
  const refreshSequence = useRef(0);
  const cache = useRef<InboxCache>({});
  const run = useCallback(async () => {
    const sequence = ++refreshSequence.current;
    try {
      // An unchanged list comes back as the same array, so this setState is a no-op.
      const next = await fetchInbox(undefined, cache.current);
      if (sequence !== refreshSequence.current) return;
      setEntries(next);
      setError("");
    } catch (reason) {
      if (sequence === refreshSequence.current) setError((reason as Error).message);
    } finally {
      if (sequence === refreshSequence.current) setLoading(false);
    }
  }, []);
  // One request at a time; a trigger mid-flight (the three event streams all announce on
  // connect) becomes a single conditional follow-up instead of a second full list.
  const sync = useMemo(() => createCoalescer(run), [run]);
  useEffect(() => {
    // The three event streams all announce on connect and on every change; coalesce a
    // burst into one conditional fetch. While every stream is up, polling is only a
    // slow safety net; if one drops it tightens until the stream is back.
    let pending: number | undefined;
    const schedule = () => {
      if (pending !== undefined) return;
      pending = window.setTimeout(() => {
        pending = undefined;
        void sync();
      }, 100);
    };
    const poll = visiblePoll(() => void sync(), INBOX_FALLBACK_POLL_MS);
    const up = new Map<string, boolean>();
    const subscriptions = ["hitl", "turns", "docs"].map((source) =>
      subscribeRevision(`/api/${source}/events`, schedule, (connected) => {
        up.set(source, connected);
        const allUp = up.size === 3 && [...up.values()].every(Boolean);
        poll.reset(allUp ? INBOX_FALLBACK_POLL_MS : INBOX_DEGRADED_POLL_MS);
      }),
    );
    void sync();
    return () => {
      window.clearTimeout(pending);
      poll.stop();
      for (const subscription of subscriptions) subscription.close();
      refreshSequence.current++;
    };
  }, [sync]);
  // `entries` are summaries; the open item is read in full on its own.
  const selectedSummary = itemID
    ? entries.find((entry) => entry.interaction.interaction_id === itemID)
    : roomID
      ? entries.filter((entry) => entry.interaction.legacy_room_id === roomID).at(-1)
      : undefined;
  const selectedID = selectedSummary?.interaction.interaction_id;
  const selectedRevision = selectedSummary?.interaction.revision;
  const [detail, setDetail] = useState<InboxEntry | null>(null);
  const [detailError, setDetailError] = useState("");
  useEffect(() => {
    void selectedRevision; // a changed revision re-reads the item
    if (!selectedID) {
      setDetail(null);
      return;
    }
    // A new selection starts clean: neither the previous item's error nor its body.
    setDetailError("");
    const controller = new AbortController();
    fetchInboxItem(selectedID, controller.signal).then(
      (full) => {
        if (controller.signal.aborted) return;
        setDetail(full);
        setDetailError("");
      },
      (reason) => {
        if (!controller.signal.aborted) setDetailError((reason as Error).message);
      },
    );
    return () => controller.abort();
  }, [selectedID, selectedRevision]);
  // The header and list use the summary; the body needs the full entry, and must never be
  // drawn from a different interaction's.
  const selected = selectedSummary
    ? detail && detail.interaction.interaction_id === selectedID
      ? detail
      : undefined
    : undefined;
  // Server-side content matches, scoped to the query they answered. A result for another
  // query (or a failed one) is never used to filter the current one.
  const [matches, setMatches] = useState<{
    query: string;
    ids: ReadonlySet<string>;
    failed: boolean;
  } | null>(null);
  const trimmedSearch = search.trim();
  useEffect(() => {
    void entries; // a changed list re-runs the search
    if (!trimmedSearch) {
      setMatches(null);
      return;
    }
    // Content search runs on the server: the list no longer carries full request text.
    // It reruns when the list changes, so a new or updated item that matches appears
    // without the query being retyped.
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      searchInbox(trimmedSearch, controller.signal).then(
        (ids) => {
          if (!controller.signal.aborted)
            setMatches({ query: trimmedSearch, ids: new Set(ids), failed: false });
        },
        () => {
          if (!controller.signal.aborted)
            setMatches({ query: trimmedSearch, ids: new Set(), failed: true });
        },
      );
    }, 250);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [trimmedSearch, entries]);
  const matchedIDs: ReadonlySet<string> =
    matches && matches.query === trimmedSearch ? matches.ids : EMPTY_IDS;
  const searchFailed = Boolean(matches && matches.query === trimmedSearch && matches.failed);
  const filtered = useMemo(() => {
    const query = search.toLowerCase().trim();
    return entries
      .filter(
        (entry) =>
          (view === "all" || (view === "history") === isTerminal(entry)) &&
          (category === "all" || categoryOf(entry) === category) &&
          (!query ||
            matchedIDs.has(entry.interaction.interaction_id) ||
            `${entryTitle(entry)} ${entrySource(entry)} ${entry.interaction.definition_binding.kind}`
              .toLowerCase()
              .includes(query)),
      )
      .sort((a, b) => (sort === "newest" ? b.sequence - a.sequence : a.sequence - b.sequence));
  }, [entries, view, category, search, sort, matchedIDs]);
  const updateFilter = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    setParams(next);
  };
  const hasSelection = Boolean(itemID || roomID);
  const listURL = `/${params.toString() ? `?${params}` : ""}`;
  const paramsText = params.toString();
  const select = (entry: InboxEntry) =>
    navigate(
      `/inbox/items/${entry.interaction.interaction_id}${paramsText ? `?${paramsText}` : ""}`,
    );
  // Rows are memoized, so what they call must keep its identity across the renders that
  // do not change the list: the latest values ride in refs.
  const filteredRef = useRef(filtered);
  filteredRef.current = filtered;
  const selectRef = useRef(select);
  selectRef.current = select;
  const selectEntry = useCallback((entry: InboxEntry) => selectRef.current(entry), []);
  const navigateRows = useCallback((entry: InboxEntry, key: string, list: HTMLElement) => {
    const rows = filteredRef.current;
    const index = rows.indexOf(entry);
    const nextIndex =
      key === "Home"
        ? 0
        : key === "End"
          ? rows.length - 1
          : Math.max(0, Math.min(rows.length - 1, index + (key === "ArrowDown" ? 1 : -1)));
    selectRef.current(rows[nextIndex]);
    (
      list.closest("ul")?.querySelectorAll("button")[nextIndex] as HTMLButtonElement | undefined
    )?.focus();
  }, []);
  const presentation = useViewPresentation(
    filtered.slice(0, 32).map((entry) => ({
      id: entry.interaction.interaction_id,
      title: entryTitle(entry),
      body: (
        <p>
          {labels[categoryOf(entry)]} · {entry.interaction.state}
        </p>
      ),
    })),
    navigate,
  );
  const filterCommand: UiHandler = {
    declaration: {
      name: "set_filter",
      scope: "url-backed",
      input_schema: {
        type: "object",
        properties: {
          name: { type: "string", enum: ["view", "type", "sort"] },
          value: { type: "string", maxLength: 32 },
        },
        required: ["name", "value"],
        additionalProperties: false,
      },
    },
    validate: (args) =>
      Object.keys(args).length === 2 &&
      typeof args.name === "string" &&
      typeof args.value === "string" &&
      (
        {
          view: ["pending", "history", "all"],
          type: ["all", "approval", "document", "turn", "workflow"],
          sort: ["oldest", "newest"],
        }[args.name] ?? []
      ).includes(args.value),
    apply: (args) => {
      updateFilter(args.name as string, args.value as string);
      return "applied";
    },
  };
  const searchCommand: UiHandler = {
    declaration: {
      name: "set_search",
      scope: "url-backed",
      input_schema: {
        type: "object",
        properties: { search: { type: "string", maxLength: 1024 } },
        required: ["search"],
        additionalProperties: false,
      },
    },
    validate: (args) =>
      Object.keys(args).length === 1 &&
      typeof args.search === "string" &&
      new TextEncoder().encode(args.search).length <= 1024,
    apply: (args) => {
      updateFilter("q", args.search as string);
      return "applied";
    },
  };
  const presentationRef = useRef(presentation);
  presentationRef.current = presentation;
  const registerTarget = useCallback(
    (id: string, element: HTMLButtonElement | null) =>
      presentationRef.current.targetRef(id)(element),
    [],
  );
  const pendingCount = useMemo(
    () => entries.filter((entry) => !isTerminal(entry)).length,
    [entries],
  );
  const commands = useUiCommands([
    ...presentation.handlers,
    filterCommand,
    searchCommand,
    idCommand("select_item", "url-backed", (args) => {
      const entry = filtered.slice(0, 32).find((row) => row.interaction.interaction_id === args.id);
      if (!entry) return "not_visible";
      select(entry);
      return "applied";
    }),
  ]);
  const control = useViewDescriptor(
    {
      active_filters: [
        { name: "modal", values: [presentation.modalID ?? "closed"] },
        { name: "search", values: [search ? "active" : "empty"] },
        { name: "queue", values: [expanded ? "hidden" : "shown"] },
        {
          name: "view",
          values: [["pending", "history", "all"].includes(view) ? view : "unsupported"],
        },
        {
          name: "type",
          values: [
            ["all", "approval", "document", "turn", "workflow"].includes(category)
              ? category
              : "unsupported",
          ],
        },
        { name: "sort", values: [["oldest", "newest"].includes(sort) ? sort : "unsupported"] },
      ],
      selected_ids: selectedID ? [selectedID] : [],
      visible_rows: filtered.slice(0, 32).map((entry) => ({
        id: entry.interaction.interaction_id,
        summary: labels[categoryOf(entry)],
      })),
    },
    commands,
    params.toString(),
  );
  useEffect(() => {
    void itemID;
    void roomID;
    setExpanded(false);
  }, [itemID, roomID]);
  return (
    <PageShell as="main" className="flex flex-col overflow-hidden">
      <header className="shrink-0 border-b border-border px-4 py-3">
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="text-lg font-semibold">Inbox</h1>
          <UiControl control={control} />
          <span className="text-xs text-fg-muted">{pendingCount} pending</span>
          <label className="sr-only" htmlFor="inbox-search">
            Search inbox
          </label>
          <input
            id="inbox-search"
            type="search"
            placeholder="Search inbox"
            value={search}
            onChange={(event) => updateFilter("q", event.target.value)}
            className="min-w-0 flex-1 rounded border border-border bg-bg-elevated px-3 py-2 text-sm"
          />
          <button
            type="button"
            aria-label="Refresh inbox"
            onClick={() => void sync()}
            className="rounded p-2 hover:bg-surface-hover"
          >
            <RefreshCw size={16} />
          </button>
        </div>
        <div className="mt-3 flex flex-wrap gap-3 text-sm">
          <label>
            View{" "}
            <select
              aria-label="Inbox view"
              value={view}
              onChange={(event) => updateFilter("view", event.target.value)}
              className="rounded border border-border bg-bg px-2 py-1"
            >
              <option value="pending">Pending</option>
              <option value="history">History</option>
              <option value="all">All</option>
            </select>
          </label>
          <label>
            Type{" "}
            <select
              aria-label="Interaction type"
              value={category}
              onChange={(event) => updateFilter("type", event.target.value)}
              className="rounded border border-border bg-bg px-2 py-1"
            >
              <option value="all">All types</option>
              <option value="approval">Approvals</option>
              <option value="document">Documents</option>
              <option value="turn">Agent turns</option>
              <option value="workflow">Structured interactions</option>
            </select>
          </label>
          <label>
            Sort{" "}
            <select
              aria-label="Inbox sort"
              value={sort}
              onChange={(event) => updateFilter("sort", event.target.value)}
              className="rounded border border-border bg-bg px-2 py-1"
            >
              <option value="newest">Newest first</option>
              <option value="oldest">Oldest first (FIFO)</option>
            </select>
          </label>
        </div>
        {error ? (
          <p role="alert" className="mt-2 text-sm text-danger">
            {error}
          </p>
        ) : null}
        {searchFailed ? (
          <p role="alert" className="mt-2 text-sm text-danger">
            Content search failed; results show title and source matches only.
          </p>
        ) : null}
      </header>
      <div className="flex min-h-0 flex-1 overflow-hidden">
        <aside
          aria-label="Inbox queue"
          className={cn(
            "w-full shrink-0 overflow-y-auto border-r border-border md:w-80 lg:w-96",
            hasSelection ? "hidden md:block" : "block",
            expanded && "md:hidden",
          )}
        >
          {loading ? (
            <p className="p-6 text-fg-muted">Loading inbox…</p>
          ) : filtered.length === 0 ? (
            <div className="p-6">
              <h2 className="font-medium">
                {view === "history" ? "No matching history" : "No matching requests"}
              </h2>
              <p className="mt-2 text-sm text-fg-muted">
                {entries.length
                  ? "Try another filter."
                  : "Requests from your agents will appear here."}
              </p>
            </div>
          ) : (
            <ul
              aria-label={
                view === "history"
                  ? "Interaction history"
                  : `Pending requests, ${sort === "newest" ? "newest" : "oldest"} first`
              }
            >
              {filtered.map((entry) => (
                <InboxRow
                  key={entry.interaction.interaction_id}
                  entry={entry}
                  selected={selectedID === entry.interaction.interaction_id}
                  onSelect={selectEntry}
                  onNavigate={navigateRows}
                  registerTarget={registerTarget}
                />
              ))}
            </ul>
          )}
        </aside>
        <section
          aria-label="Selected interaction"
          className={cn(
            "flex min-h-0 min-w-0 flex-1 flex-col",
            hasSelection ? "flex" : "hidden md:flex",
          )}
        >
          {selectedSummary ? (
            <>
              <div className="flex shrink-0 items-center gap-3 border-b border-border px-4 py-2 text-xs">
                <button
                  type="button"
                  onClick={() => navigate(listURL)}
                  className="inline-flex items-center gap-1 text-fg-muted"
                >
                  <ArrowLeft size={14} /> Back to inbox
                </button>
                <span className="ml-auto text-fg-muted">
                  {labels[categoryOf(selectedSummary)]} · {selectedSummary.interaction.state}
                </span>
                <button
                  type="button"
                  aria-label={expanded ? "Show inbox queue" : "Expand interaction"}
                  onClick={() => setExpanded(!expanded)}
                  className="hidden rounded p-2 hover:bg-surface-hover md:block"
                >
                  {expanded ? <Minimize size={16} /> : <Expand size={16} />}
                </button>
              </div>
              <div className="min-h-0 flex-1 overflow-y-auto" data-testid="inbox-body">
                {!selected ? (
                  detailError ? (
                    <p role="alert" className="p-6 text-danger">
                      {detailError}
                    </p>
                  ) : (
                    <p className="p-6 text-fg-muted">Loading interaction…</p>
                  )
                ) : categoryOf(selected) !== "workflow" ? (
                  <InboxItemBody
                    key={selected.interaction.interaction_id}
                    entry={selected}
                    onChange={sync}
                  />
                ) : isTerminal(selected) ? (
                  <WorkflowHistory entry={selected} />
                ) : selected.interaction.legacy_room_id ? (
                  <Room
                    key={selected.interaction.interaction_id}
                    roomID={selected.interaction.legacy_room_id}
                    embedded
                  />
                ) : (
                  <div className="p-6">
                    <h2 className="text-xl font-semibold">{entryTitle(selected)}</h2>
                    <p role="alert" className="mt-4 text-danger">
                      This interaction has no browser presentation. Ask the submitting agent to use
                      a supported workflow.
                    </p>
                  </div>
                )}
              </div>
            </>
          ) : (
            <div className="flex flex-1 items-center justify-center p-8 text-center text-fg-muted">
              <p>
                {hasSelection && !loading
                  ? "This interaction is unavailable."
                  : "Select a request to review and respond."}
              </p>
            </div>
          )}
        </section>
      </div>
      {presentation.dialog}
    </PageShell>
  );
}

interface InboxRowProps {
  entry: InboxEntry;
  selected: boolean;
  onSelect: (entry: InboxEntry) => void;
  onNavigate: (entry: InboxEntry, key: string, element: HTMLElement) => void;
  registerTarget: (id: string, element: HTMLButtonElement | null) => void;
}

// Memoized on the entry: the list poll hands back the same array when nothing changed,
// and a changed list keeps the identity of every unchanged entry only if the server
// payload does, so a 304 re-renders no row at all. `content-visibility` lets the browser
// skip layout and paint for rows scrolled out of view.
const InboxRow = memo(function InboxRow({
  entry,
  selected,
  onSelect,
  onNavigate,
  registerTarget,
}: InboxRowProps) {
  const record = entry.interaction;
  const id = record.interaction_id;
  const ref = useCallback(
    (element: HTMLButtonElement | null) => registerTarget(id, element),
    [id, registerTarget],
  );
  const created = useMemo(() => new Date(record.created_at).toLocaleString(), [record.created_at]);
  return (
    <li className="[contain-intrinsic-size:auto_112px] [content-visibility:auto]">
      <button
        type="button"
        ref={ref}
        aria-current={selected ? "true" : undefined}
        onClick={() => onSelect(entry)}
        onKeyDown={(event) => {
          if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
          event.preventDefault();
          onNavigate(entry, event.key, event.currentTarget);
        }}
        className={cn(
          "w-full border-b border-border px-4 py-4 text-left hover:bg-surface-hover focus-visible:outline-2 focus-visible:outline-primary",
          selected && "bg-surface",
        )}
      >
        <div className="flex items-center justify-between gap-2 text-xs text-fg-muted">
          <span>{labels[categoryOf(entry)]}</span>
          <span>#{entry.sequence}</span>
        </div>
        <div className="mt-1 break-words text-sm font-medium">{entryTitle(entry)}</div>
        <div className="mt-2 truncate text-xs text-fg-muted">
          {entrySource(entry)} · {record.state}
        </div>
        <time className="mt-1 block text-xs text-fg-faint" dateTime={record.created_at}>
          {created}
        </time>
      </button>
    </li>
  );
});

function WorkflowHistory({ entry }: { entry: InboxEntry }) {
  const record = entry.interaction;
  const envelope = {
    id: record.legacy_envelope_id,
    type: record.definition_binding.kind,
    data: record.request_snapshot,
  };
  return (
    <article className="space-y-6 p-4 sm:p-6 lg:p-8">
      <h2 className="text-xl font-semibold">{entryTitle(entry)}</h2>
      <section className="rounded border border-border bg-surface p-4">
        <h3 className="mb-3 font-semibold">
          {entry.resolution ? "Your reply" : record.state === "canceled" ? "Canceled" : "Outcome"}
        </h3>
        {entry.resolution ? (
          <>
            <ResponseSummary value={entry.resolution.response_payload} />
            <time className="mt-4 block text-xs text-fg-muted">
              Saved {new Date(entry.resolution.recorded_at).toLocaleString()}
            </time>
          </>
        ) : (
          <p>{record.terminal_reason || record.state}</p>
        )}
      </section>
      <section>
        <h3 className="mb-3 font-medium">Original request</h3>
        {/* ExternalReview honors readOnly itself; inert would also block its source link
            and text selection. Other renderers still need the interaction guard. */}
        <fieldset
          disabled
          inert={record.definition_binding.kind !== "tangent.external-review"}
          className="min-w-0"
        >
          <EnvelopeRouter
            envelope={envelope}
            onSubmit={() => {}}
            onCancel={() => {}}
            roomID={record.legacy_room_id}
            readOnly
          />
        </fieldset>
      </section>
    </article>
  );
}
