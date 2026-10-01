import { ArrowLeft, Expand, Minimize, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";

import { EnvelopeRouter } from "@/components/envelopes/EnvelopeRouter";
import { PageShell } from "@/components/layout/PageShell";
import { ResponseSummary } from "@/components/ResponseSummary";
import {
  categoryOf,
  entrySource,
  entryTitle,
  fetchInbox,
  type InboxEntry,
  isTerminal,
} from "@/lib/inbox-api";
import { cn } from "@/lib/utils";
import { InboxItemBody } from "./InboxItemBody";
import Room from "./Room";

const labels = {
  approval: "Approval",
  document: "Document",
  turn: "Agent turn",
  workflow: "Interaction",
};

export default function Inbox() {
  const { itemID, roomID } = useParams<{ itemID?: string; roomID?: string }>();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const view = params.get("view") || "pending";
  const category = params.get("type") || "all";
  const sort = params.get("sort") || "oldest";
  const search = params.get("q") || "";
  const [entries, setEntries] = useState<InboxEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [expanded, setExpanded] = useState(false);
  const refreshSequence = useRef(0);
  const sync = useCallback(async () => {
    const sequence = ++refreshSequence.current;
    try {
      const next = await fetchInbox();
      if (sequence !== refreshSequence.current) return;
      setEntries(next);
      setError("");
    } catch (reason) {
      if (sequence === refreshSequence.current) setError((reason as Error).message);
    } finally {
      if (sequence === refreshSequence.current) setLoading(false);
    }
  }, []);
  useEffect(() => {
    void sync();
    const timer = window.setInterval(() => void sync(), 2000);
    const streams = ["hitl", "turns", "docs"].map((source) => {
      const stream = new EventSource(`/api/${source}/events`);
      stream.addEventListener("revision", () => void sync());
      return stream;
    });
    return () => {
      window.clearInterval(timer);
      for (const stream of streams) stream.close();
      refreshSequence.current++;
    };
  }, [sync]);
  const selected = itemID
    ? entries.find((entry) => entry.interaction.interaction_id === itemID)
    : roomID
      ? entries.filter((entry) => entry.interaction.legacy_room_id === roomID).at(-1)
      : undefined;
  const filtered = useMemo(() => {
    const query = search.toLowerCase().trim();
    return entries
      .filter(
        (entry) =>
          (view === "all" || (view === "history") === isTerminal(entry)) &&
          (category === "all" || categoryOf(entry) === category) &&
          (!query ||
            `${entryTitle(entry)} ${entrySource(entry)} ${entry.interaction.definition_binding.kind} ${JSON.stringify(entry.interaction.request_snapshot)}`
              .toLowerCase()
              .includes(query)),
      )
      .sort((a, b) => (sort === "newest" ? b.sequence - a.sequence : a.sequence - b.sequence));
  }, [entries, view, category, search, sort]);
  const updateFilter = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    setParams(next);
  };
  const hasSelection = Boolean(itemID || roomID);
  const listURL = `/${params.toString() ? `?${params}` : ""}`;
  const select = (entry: InboxEntry) =>
    navigate(
      `/inbox/items/${entry.interaction.interaction_id}${params.toString() ? `?${params}` : ""}`,
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
          <span className="text-xs text-fg-muted">
            {entries.filter((entry) => !isTerminal(entry)).length} pending
          </span>
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
              <option value="oldest">Oldest first (FIFO)</option>
              <option value="newest">Newest first</option>
            </select>
          </label>
        </div>
        {error ? (
          <p role="alert" className="mt-2 text-sm text-danger">
            {error}
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
              {filtered.map((entry) => {
                const record = entry.interaction;
                return (
                  <li key={record.interaction_id}>
                    <button
                      type="button"
                      aria-current={
                        selected?.interaction.interaction_id === record.interaction_id
                          ? "true"
                          : undefined
                      }
                      onClick={() => select(entry)}
                      onKeyDown={(event) => {
                        if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
                        event.preventDefault();
                        const index = filtered.indexOf(entry);
                        const nextIndex =
                          event.key === "Home"
                            ? 0
                            : event.key === "End"
                              ? filtered.length - 1
                              : Math.max(
                                  0,
                                  Math.min(
                                    filtered.length - 1,
                                    index + (event.key === "ArrowDown" ? 1 : -1),
                                  ),
                                );
                        select(filtered[nextIndex]);
                        const buttons = event.currentTarget
                          .closest("ul")
                          ?.querySelectorAll("button");
                        (buttons?.[nextIndex] as HTMLButtonElement | undefined)?.focus();
                      }}
                      className={cn(
                        "w-full border-b border-border px-4 py-4 text-left hover:bg-surface-hover focus-visible:outline-2 focus-visible:outline-primary",
                        selected?.interaction.interaction_id === record.interaction_id &&
                          "bg-surface",
                      )}
                    >
                      <div className="flex items-center justify-between gap-2 text-xs text-fg-muted">
                        <span>{labels[categoryOf(entry)]}</span>
                        <span>#{entry.sequence}</span>
                      </div>
                      <div className="mt-1 break-words text-sm font-medium">
                        {entryTitle(entry)}
                      </div>
                      <div className="mt-2 truncate text-xs text-fg-muted">
                        {entrySource(entry)} · {record.state}
                      </div>
                      <time
                        className="mt-1 block text-xs text-fg-faint"
                        dateTime={record.created_at}
                      >
                        {new Date(record.created_at).toLocaleString()}
                      </time>
                    </button>
                  </li>
                );
              })}
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
          {selected ? (
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
                  {labels[categoryOf(selected)]} · {selected.interaction.state}
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
                {categoryOf(selected) !== "workflow" ? (
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
    </PageShell>
  );
}

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
