import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { PageShell } from "@/components/layout/PageShell";
import { Markdown } from "@/components/markdown";
import {
  UiControl,
  useUiCommandOrigin,
  useUiCommands,
  useViewDescriptor,
} from "@/hooks/useUiCommands";
import { useViewPresentation } from "@/hooks/useViewPresentation";
import {
  type ChannelAgentPresence,
  ChannelAPIError,
  type ChannelDetail,
  type ChannelMessage,
  type ChannelSummary,
  fetchChannel,
  fetchChannels,
  markChannelRead,
  sendChannelMessage,
} from "@/lib/channel-api";
import { cn } from "@/lib/utils";

// CW-20260907-0017: the minimal channel pane. A channel list with unread
// and needs-input counts, one addressed chat view per channel, HITL items
// raised by that channel's agent shown inline, a per-channel draft in
// localStorage, and enter to send. Deliberately out of scope: a subject
// pane, show-me viewers, search, mute/archive, rules, and any
// notification — see the task description for the full out-list.
//
// There is no "create channel" action here, on purpose: 0066 shipped only
// the agent-facing tangent.relay_open_channel; an operator's channel list
// is exactly the channels an agent has already opened. The primary flow
// runs agent-first — an agent needing input opens a channel and messages
// the operator, who replies here — not operator-first. If daily use shows
// that blocking, adding a create action is small; it is a recorded
// decision to omit it now, not an oversight.

const DETAIL_POLL_MS = 5000;
const LIST_EVENT_LIFETIME_MS = 55_000; // client-side headroom over the 50s server cap

function draftKey(channelID: string): string {
  return `tangent:channel-draft:${channelID}`;
}

function loadDraft(channelID: string): string {
  try {
    return window.localStorage.getItem(draftKey(channelID)) ?? "";
  } catch {
    return "";
  }
}

function saveDraft(channelID: string, value: string): void {
  try {
    if (value) window.localStorage.setItem(draftKey(channelID), value);
    else window.localStorage.removeItem(draftKey(channelID));
  } catch {
    // Browser storage custody limitation, same as CW-20260905-0001 already
    // accepts elsewhere: the draft simply does not survive this case.
  }
}

export default function ChannelPaneRoute() {
  const { channelID } = useParams<{ channelID?: string }>();
  const navigate = useNavigate();
  const commandOrigin = useUiCommandOrigin();
  const [channels, setChannels] = useState<ChannelSummary[] | null>(null);
  const [detail, setDetail] = useState<ChannelDetail | null>(null);
  const [listError, setListError] = useState("");
  const [detailError, setDetailError] = useState("");
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const markedReadFor = useRef("");

  const syncChannels = useCallback(async () => {
    try {
      const next = await fetchChannels();
      setChannels(next);
      setListError("");
    } catch (reason) {
      setListError(humanizeError(reason));
    }
  }, []);

  useEffect(() => {
    void syncChannels();
  }, [syncChannels]);

  // Revision-hint SSE, matching /api/hitl/events' own contract: the event
  // carries no state, only a signal to refetch. A channel list is a
  // durable ledger, like the HITL inbox, not a live collaborative
  // envelope — that is why this mirrors HITL's SSE shape rather than
  // ws-client.ts, which solves a different problem (a room's resolver
  // lease).
  useEffect(() => {
    const events = new EventSource("/api/channels/events");
    const handleRevision = () => void syncChannels();
    events.addEventListener("revision", handleRevision);
    const lifetime = window.setTimeout(() => events.close(), LIST_EVENT_LIFETIME_MS);
    return () => {
      window.clearTimeout(lifetime);
      events.removeEventListener("revision", handleRevision);
      events.close();
    };
  }, [syncChannels]);

  const syncDetail = useCallback(async (id: string) => {
    try {
      const next = await fetchChannel(id);
      setDetail(next);
      setDetailError("");
    } catch (reason) {
      setDetailError(humanizeError(reason));
    }
  }, []);

  useEffect(() => {
    if (!channelID) {
      setDetail(null);
      return;
    }
    setDraft(loadDraft(channelID));
    void syncDetail(channelID);
    // The detail view (delivery-state transitions, presence) is not part
    // of the list-level revision hint — see channelpane.Service.Revision's
    // own doc comment for why — so it refreshes on its own short interval
    // while open instead.
    const poll = window.setInterval(() => void syncDetail(channelID), DETAIL_POLL_MS);
    return () => window.clearInterval(poll);
  }, [channelID, syncDetail]);

  // Successful participant opens (including deep links/back-forward) retain read
  // behavior. Agent navigation is presentation-only and cannot record receipts.
  useEffect(() => {
    if (commandOrigin.current || !channelID || !detail || detail.channel_id !== channelID) return;
    const hasUnread = channels?.find((c) => c.channel_id === channelID)?.unread_count ?? 0;
    if (hasUnread === 0 || markedReadFor.current === channelID) return;
    markedReadFor.current = channelID;
    markChannelRead(channelID)
      .then(syncChannels)
      .catch(() => {
        markedReadFor.current = "";
      });
  }, [channelID, channels, detail, syncChannels, commandOrigin]);
  const selectChannel = (channel: ChannelSummary) => {
    commandOrigin.current = false;
    navigate(`/channels/${encodeURIComponent(channel.channel_id)}`);
  };

  const presentation = useViewPresentation(
    (channels ?? []).slice(0, 32).map((channel) => ({
      id: channel.channel_id,
      title: channel.title || shortID(channel.channel_id),
      body: <p>Channel view</p>,
    })),
    navigate,
  );
  const commands = useUiCommands(presentation.handlers);
  const control = useViewDescriptor(
    {
      active_filters: [{ name: "modal", values: [presentation.modalID ?? "closed"] }],
      selected_ids:
        channelID && channels?.some((channel) => channel.channel_id === channelID)
          ? [channelID]
          : [],
      visible_rows: (channels ?? [])
        .slice(0, 32)
        .map((channel) => ({ id: channel.channel_id, summary: "Channel" })),
    },
    commands,
  );
  const orderedMessages = useMemo(() => (detail ? [...detail.messages].reverse() : []), [detail]);

  const send = async () => {
    if (!channelID || sending) return;
    const body = draft.trim();
    if (!body) return;
    setSending(true);
    try {
      await sendChannelMessage(channelID, body);
      setDraft("");
      saveDraft(channelID, "");
      await Promise.all([syncDetail(channelID), syncChannels()]);
    } catch (reason) {
      setDetailError(humanizeError(reason));
    } finally {
      setSending(false);
    }
  };

  return (
    <PageShell as="main" className="flex overflow-y-auto">
      <section
        className={cn(
          "w-full shrink-0 border-r border-zinc-800 sm:w-80",
          channelID ? "hidden sm:block" : "block",
        )}
        aria-label="Channels"
      >
        <header className="border-b border-zinc-800 px-4 py-3">
          <h1 className="text-sm font-semibold tracking-wide text-zinc-100">Channels</h1>
          <UiControl control={control} />
          <p className="mt-1 text-xs leading-5 text-zinc-500">
            Opened by an agent, over the relay. Reading never consumes an agent's inbox.
          </p>
        </header>
        {listError ? (
          <div
            role="alert"
            className="border-b border-red-900/60 bg-red-950/40 px-4 py-2 text-xs text-red-300"
          >
            {listError}
          </div>
        ) : null}
        <ul className="m-0 list-none p-0">
          {!channels ? (
            <li className="px-4 py-6 text-xs text-zinc-500">Loading…</li>
          ) : channels.length === 0 ? (
            <li className="px-4 py-6 text-xs text-zinc-500">
              No channels yet. An agent opens one with tangent.relay_open_channel.
            </li>
          ) : (
            channels.map((channel) => (
              <li key={channel.channel_id}>
                <button
                  type="button"
                  ref={presentation.targetRef(channel.channel_id)}
                  onClick={() => selectChannel(channel)}
                  aria-current={channel.channel_id === channelID ? "page" : undefined}
                  className={cn(
                    "block w-full border-b border-zinc-900 px-4 py-3 text-left outline-none focus-visible:ring-2 focus-visible:ring-amber-400",
                    channel.channel_id === channelID ? "bg-zinc-900" : "hover:bg-zinc-900/60",
                  )}
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate text-sm font-medium text-zinc-100">
                      {channel.title || shortID(channel.channel_id)}
                    </span>
                    <span className="flex shrink-0 gap-1">
                      {channel.needs_input_count > 0 ? (
                        <CountBadge
                          count={channel.needs_input_count}
                          tone="amber"
                          label="needs input"
                        />
                      ) : null}
                      {channel.unread_count > 0 ? (
                        <CountBadge count={channel.unread_count} tone="blue" label="unread" />
                      ) : null}
                    </span>
                  </div>
                  {channel.last_message_preview ? (
                    <p className="mt-1 truncate text-xs text-zinc-500">
                      {channel.last_message_preview}
                    </p>
                  ) : null}
                </button>
              </li>
            ))
          )}
        </ul>
      </section>

      <section
        className={cn("min-w-0 flex-1", channelID ? "flex flex-col" : "hidden sm:flex sm:flex-col")}
      >
        {!channelID ? (
          <div className="flex flex-1 items-center justify-center px-8 text-center text-sm text-zinc-500">
            Choose a channel to see its history and reply.
          </div>
        ) : detailError && !detail ? (
          <div className="flex flex-1 items-center justify-center px-8 text-center">
            <div>
              <p className="text-sm text-red-300">{detailError}</p>
              <button
                type="button"
                onClick={() => void syncDetail(channelID)}
                className="mt-3 border border-zinc-700 px-3 py-1.5 text-xs text-zinc-300 hover:border-zinc-500 hover:text-white"
              >
                Retry
              </button>
            </div>
          </div>
        ) : !detail ? (
          <div className="flex flex-1 items-center justify-center text-sm text-zinc-500">
            Loading…
          </div>
        ) : (
          <>
            <header className="flex items-center justify-between gap-3 border-b border-zinc-800 px-4 py-3">
              <div className="min-w-0">
                <h2 className="truncate text-sm font-semibold text-zinc-100">
                  {detail.title || shortID(detail.channel_id)}
                </h2>
                <AgentPresenceLine presence={detail.agent_presence} />
              </div>
              <button
                type="button"
                onClick={() => navigate("/channels")}
                className="shrink-0 text-xs text-zinc-500 hover:text-zinc-200 sm:hidden"
              >
                Back
              </button>
            </header>

            {detail.hitl_items.length > 0 ? (
              <div className="border-b border-zinc-800 bg-zinc-900/50 px-4 py-2">
                <p className="text-[10px] font-semibold uppercase tracking-wide text-zinc-500">
                  Needs input
                </p>
                <ul className="mt-1 space-y-1">
                  {detail.hitl_items.map((item) => (
                    <li key={item.item_id}>
                      <a
                        href={item.item_url}
                        className="text-xs text-amber-300 underline decoration-amber-700 underline-offset-2 hover:text-amber-200"
                      >
                        {item.state.replace(/_/g, " ")} · open in Approvals
                      </a>
                    </li>
                  ))}
                </ul>
              </div>
            ) : null}

            {detailError ? (
              <div
                role="alert"
                className="border-b border-red-900/60 bg-red-950/40 px-4 py-2 text-xs text-red-300"
              >
                {detailError}
              </div>
            ) : null}

            <ul className="m-0 flex-1 list-none space-y-3 overflow-y-auto p-4">
              {orderedMessages.length === 0 ? (
                <li className="text-sm text-zinc-500">No messages yet.</li>
              ) : (
                orderedMessages.map((message) => (
                  <MessageRow key={message.exchange_id} message={message} />
                ))
              )}
            </ul>

            <form
              className="border-t border-zinc-800 p-3"
              onSubmit={(event) => {
                event.preventDefault();
                void send();
              }}
            >
              <textarea
                value={draft}
                onChange={(event) => {
                  setDraft(event.target.value);
                  saveDraft(channelID, event.target.value);
                }}
                onKeyDown={(event) => {
                  if (event.key === "Enter" && !event.shiftKey) {
                    event.preventDefault();
                    void send();
                  }
                }}
                disabled={sending}
                rows={2}
                placeholder="Message this channel's agent. Enter to send, Shift+Enter for a new line."
                className="w-full resize-none border border-zinc-700 bg-zinc-900 px-3 py-2 text-sm text-zinc-100 outline-none placeholder:text-zinc-600 focus:border-amber-500"
              />
              <div className="mt-2 flex justify-end">
                <button
                  type="submit"
                  disabled={sending || !draft.trim()}
                  className="border border-zinc-600 bg-zinc-100 px-3 py-1.5 text-xs font-semibold text-zinc-900 outline-none hover:bg-white disabled:cursor-not-allowed disabled:opacity-40"
                >
                  {sending ? "Sending…" : "Send"}
                </button>
              </div>
            </form>
          </>
        )}
      </section>
      {presentation.dialog}
    </PageShell>
  );
}

function CountBadge({
  count,
  tone,
  label,
}: {
  count: number;
  tone: "amber" | "blue";
  label: string;
}) {
  return (
    <span
      title={`${count} ${label}`}
      className={cn(
        "rounded-full px-1.5 py-0.5 text-[10px] font-semibold leading-none",
        tone === "amber" ? "bg-amber-500/20 text-amber-300" : "bg-blue-500/20 text-blue-300",
      )}
    >
      {count}
    </span>
  );
}

function AgentPresenceLine({ presence }: { presence?: ChannelAgentPresence }) {
  if (!presence) return null;
  // A third party's view only — this pane never calls relay_receive, so
  // there is no self-report artifact to guard against here, unlike a
  // participant reading its own presence.
  if (presence.open) {
    return <p className="text-xs text-emerald-400">Agent is checking in right now</p>;
  }
  if (presence.last_seen_at) {
    return (
      <p className="text-xs text-zinc-500">Agent last seen {relativeAge(presence.last_seen_at)}</p>
    );
  }
  return <p className="text-xs text-zinc-500">Agent has not checked in yet</p>;
}

function MessageRow({ message }: { message: ChannelMessage }) {
  const isOperator = message.direction === "operator";
  return (
    <li className={cn("flex", isOperator ? "justify-end" : "justify-start")}>
      <div
        className={cn(
          "max-w-[85%] border px-3 py-2 text-sm leading-6",
          isOperator
            ? "border-zinc-700 bg-zinc-800 text-zinc-100"
            : "border-zinc-800 bg-zinc-900 text-zinc-200",
        )}
      >
        {/*
          Both directions route through the shared renderer. People type
          markdown too, and splitting by direction would print the operator's
          literal ** beside the agent's rendered bold in one thread. The
          renderer is inert either way — no HTML is constructed from the text,
          and no link here can navigate.
        */}
        <Markdown
          data-testid="channel-message-body"
          content={message.body}
          className={cn("space-y-2 leading-6", isOperator ? "text-zinc-100" : "text-zinc-200")}
        />
        <p className="mt-1 flex items-center gap-2 text-[10px] uppercase tracking-wide text-zinc-500">
          <span>
            {new Date(message.created_at).toLocaleTimeString([], {
              hour: "numeric",
              minute: "2-digit",
            })}
          </span>
          {isOperator && message.delivery_state ? (
            <DeliveryBadge state={message.delivery_state} />
          ) : null}
        </p>
      </div>
    </li>
  );
}

function DeliveryBadge({ state }: { state: NonNullable<ChannelMessage["delivery_state"]> }) {
  const label =
    state === "queued" ? "queued" : state === "awaiting-peer" ? "awaiting peer" : "accepted";
  const tone =
    state === "accepted-by-peer"
      ? "text-emerald-400"
      : state === "queued"
        ? "text-blue-400"
        : "text-zinc-500";
  return <span className={tone}>{label}</span>;
}

function shortID(id: string): string {
  return id.slice(0, 8);
}

function relativeAge(value: string): string {
  const milliseconds = Date.now() - new Date(value).getTime();
  const minutes = Math.max(0, Math.floor(milliseconds / 60_000));
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

function humanizeError(reason: unknown): string {
  if (reason instanceof ChannelAPIError) return reason.detail.message || reason.message;
  return reason instanceof Error ? reason.message : "An unexpected channel error occurred.";
}
