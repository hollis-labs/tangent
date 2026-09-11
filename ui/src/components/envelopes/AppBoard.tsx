// AppBoard renders a `tangent.app-board` envelope: a board of caller-supplied
// cards arranged in columns, with a filter bar and an optional detail pane.
//
// # What this component is not
//
// It is not a task board. It has no notion of a project, a status transition,
// an assignee or a sprint, and adding one would make ADR 0005's "Tangent is not
// a task tracker" false while a Torque board happened to be the thing rendering
// (ADR 0007 §6). Everything domain-shaped arrives as data: a badge is a label
// and a tone, a field is a key and a value, an action is an id and a label.
//
// It also does not query anything. **Filters are a view over the cards the
// caller supplied.** Selecting a filter narrows what is on screen; it cannot
// reach a record the caller did not send. This is said in the manifest
// description as well, because it is the sentence a future reader is most
// likely to assume the other way, and assuming the other way turns an optional
// refresh into a mandatory round trip (Chrispian's decision, 2026-09-09).
//
// # The two ways state leaves this component
//
//   - `onDraft` — filters, selection, whether the detail pane is open. Not a
//     decision, does not settle the interaction, does not take the resolver
//     lease. The caller reads it by pulling `tangent.surface_get`.
//   - `onSubmit` — the participant pressed one of the caller's actions. That
//     settles the interaction, and the caller applies whatever the action means
//     using its own tools.
//
// # Staged changes, and why they are not decisions
//
// When the caller supplies a `sync` block, the detail pane offers a control
// that moves a card into another column. That move is **staged**: it is
// recorded in the draft and the card renders in its new column with a marker,
// and nothing has happened to the caller's records. Pressing Sync is what
// applies it — the caller's own plugin route reads the draft, applies the
// changes with the owning application's API, and replaces the board.
//
// This preserves ADR 0007 §5's rule rather than bending it. A draft is still
// what the participant is looking at, and a board closed with staged changes
// and never synced changes nothing. The press is the decision; the draft is
// only where the intent was accumulated.
//
// A `note_label` adds a free-text box per card on the same terms. The note goes
// into the draft beside the staged column and is read by the caller at the same
// moment, for whatever the caller means by it — this component does not know.
// It exists because a board could say where a card should GO but had no way for
// the participant to say anything ABOUT it, and some callers need a sentence
// rather than a column.
//
// The board never fetches its own content. `sync.endpoint` is a same-origin
// path under `/api/plugins/`, checked below, so this is not a general fetch
// surface a caller could point anywhere — that is what the host-mediated
// effect broker exists for, and this is deliberately not a second one.
//
// The detail pane is a pane in THIS envelope, never a second one. Tangent
// allows one pending envelope per room, and composing the detail inside the
// board respects that limitation instead of contesting it.

import { useMemo, useState } from "react";

import { Markdown } from "@/components/markdown";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export type BoardBadgeTone = "neutral" | "info" | "success" | "warning" | "danger";
export type BoardActionTone = "neutral" | "primary" | "danger";
export type BoardFilterKind = "single" | "multi" | "text";

export interface BoardBadge {
  id?: string;
  label: string;
  tone?: BoardBadgeTone;
}

export interface BoardCardData {
  id: string;
  title: string;
  subtitle?: string;
  badges?: BoardBadge[];
  /** Markdown. Rendered through the shared <Markdown>, never a second path. */
  body?: string;
  /**
   * A note already on record for this card. The note control seeds from it, so
   * the participant sees what was recorded rather than an empty box.
   *
   * The renderer does not know what a note means or who acts on it — only that
   * one is on record and that editing it replaces it.
   */
  note?: string;
  fields?: Record<string, unknown>;
}

export interface BoardColumn {
  id: string;
  label: string;
  card_ids?: string[];
}

export interface BoardFilterOption {
  value: string;
  label?: string;
  count?: number;
}

export interface BoardFilter {
  id: string;
  label: string;
  kind: BoardFilterKind;
  /** Which card field this filter reads. Defaults to the filter's own id. */
  field?: string;
  options?: BoardFilterOption[];
  selected?: string[];
}

export interface BoardDetailSection {
  label: string;
  markdown?: string;
}

export interface BoardDetailAction {
  id: string;
  label: string;
  tone?: BoardActionTone;
}

export interface BoardDetail {
  card_id?: string;
  open?: boolean;
  /** Who raised this pane. Either party may; the renderer treats both alike. */
  raised_by?: "agent" | "user";
  sections?: BoardDetailSection[];
  actions?: BoardDetailAction[];
}

/**
 * What the caller offers for talking back to it without an agent turn.
 *
 * `endpoint` is a same-origin path under `/api/plugins/`; see isPluginRoutePath
 * for why that is checked here rather than trusted.
 */
export interface BoardSync {
  enabled?: boolean;
  endpoint?: string;
  label?: string;
  /** Names the staging control. Empty means staging is not offered. */
  stage_label?: string;
  /** Names the per-card note control. Empty means no note is offered. */
  note_label?: string;
  /** What the caller sent, in its own words, so a filter that finds nothing reads as a scope rather than a bug. */
  scope?: string;
}

export interface AppBoardEnvelope {
  v: number;
  id: string;
  type: "tangent.app-board";
  typeVersion?: string;
  title?: string;
  context?: string;
  presentation?: "inline" | "modal" | "drawer" | "sidecar" | "fullscreen";
  data?: {
    board_id?: string;
    title?: string;
    source?: { app?: string; label?: string };
    columns?: BoardColumn[];
    cards?: BoardCardData[];
    filters?: BoardFilter[];
    detail?: BoardDetail;
    sync?: BoardSync;
    updated_at?: string;
  };
  meta?: Record<string, unknown>;
}

/**
 * What a draft revision carries. This is view state and only view state: it
 * says what the participant is looking at, and a caller must not read it as a
 * decision (ADR 0007 §5).
 */
export interface AppBoardDraft {
  board_id: string;
  filters: Record<string, string[]>;
  selected_card_id: string | null;
  detail_open: boolean;
  /**
   * True when the participant asked for fresh data. Refresh is explicit and
   * pull-based: this flag sits in the draft until the caller looks. Nothing is
   * pushed, and nothing here re-queries anything.
   */
  refresh_requested: boolean;
  /**
   * Cards the participant moved into another column but has not synced.
   *
   * Staged intent, not a decision: the caller reads it when the participant
   * presses Sync and not before, and a board abandoned with staged changes
   * changes nothing. Absent on a board whose caller offers no sync.
   */
  staged_changes?: Record<string, { column_id: string }>;
  /**
   * What the participant wrote about a card, keyed by card id.
   *
   * View state, exactly as a staged change is: it says what they typed, not
   * what they decided. The caller reads it when the participant presses Sync
   * and not before, and a board abandoned with a note written changes nothing.
   * Absent on a board whose caller offers no note control.
   *
   * This component neither interprets a note nor sends it anywhere. What it
   * means is the caller's business, which is what keeps the kind domain-free.
   */
  staged_notes?: Record<string, string>;
}

export interface AppBoardResponse {
  v: 1;
  envelopeId: string;
  kind: "data";
  status: "submitted";
  payload: {
    board_id: string;
    action_id: string;
    card_id: string | null;
    filters: Record<string, string[]>;
  };
  completedAt?: string;
}

export type AppBoardProps = {
  envelope: AppBoardEnvelope;
  onSubmit: (response: AppBoardResponse) => void;
  onCancel: () => void;
  onDraft?: (draft: unknown) => void;
  roomID?: string;
};

// A caller names an action's weight; Tangent decides what that looks like. The
// caller cannot hand over a class name, which is what keeps a board's styling
// inside the reviewed component rather than in agent-authored data.
const ACTION_VARIANTS: Record<BoardActionTone, "default" | "secondary" | "destructive"> = {
  neutral: "secondary",
  primary: "default",
  danger: "destructive",
};

const BADGE_TONES: Record<BoardBadgeTone, string> = {
  neutral: "bg-zinc-800 text-zinc-300",
  info: "bg-sky-950 text-sky-300",
  success: "bg-emerald-950 text-emerald-300",
  warning: "bg-amber-950 text-amber-300",
  danger: "bg-red-950 text-red-300",
};

/** Reads one card field as the strings a filter compares against. */
export function cardFieldValues(card: BoardCardData, field: string): string[] {
  const raw = card.fields?.[field];
  if (raw === undefined || raw === null) return [];
  if (Array.isArray(raw)) {
    return raw.filter((v) => typeof v === "string" || typeof v === "number").map(String);
  }
  if (typeof raw === "string" || typeof raw === "number" || typeof raw === "boolean") {
    return [String(raw)];
  }
  return [];
}

/**
 * Narrows the supplied cards by the current selection.
 *
 * A filter with nothing selected narrows nothing. A `text` filter matches the
 * title, subtitle and body substring-wise; the others match the card's field
 * values. Selections across different filters are ANDed, values within one
 * filter are ORed — the behavior a reader expects from a filter bar.
 *
 * Exported for its tests, because "the filter is a view, not a query" is a
 * claim about this function and is worth being able to check directly.
 */
export function applyFilters(
  cards: BoardCardData[],
  filters: BoardFilter[],
  selection: Record<string, string[]>,
): BoardCardData[] {
  return cards.filter((card) =>
    filters.every((filter) => {
      const chosen = selection[filter.id] ?? [];
      if (chosen.length === 0) return true;
      if (filter.kind === "text") {
        const haystack = [card.title, card.subtitle ?? "", card.body ?? ""]
          .join("\n")
          .toLowerCase();
        return chosen.every((needle) => haystack.includes(needle.toLowerCase()));
      }
      const values = cardFieldValues(card, filter.field ?? filter.id);
      return chosen.some((value) => values.includes(value));
    }),
  );
}

/**
 * Whether a caller-supplied sync endpoint is one this board may POST to.
 *
 * A same-origin path under the host's reserved plugin prefix, and nothing else:
 * no scheme, no host, no protocol-relative `//`, no `..`. A board is not a
 * general fetch surface — the effect broker is what mediates a renderer acting
 * on the world, and letting an envelope name an arbitrary URL here would be a
 * second, unreviewed one. Whether the named route will *accept* the request is
 * still the server's decision; this only stops the request being aimed
 * somewhere else entirely.
 *
 * Exported for its tests, because "a caller cannot point the board at an
 * arbitrary URL" is a claim about this function.
 */
export function isPluginRoutePath(endpoint: string | undefined): endpoint is string {
  if (!endpoint) return false;
  if (!endpoint.startsWith("/api/plugins/")) return false;
  if (endpoint.startsWith("//")) return false;
  if (endpoint.includes("..")) return false;
  return !/[\s?#]/.test(endpoint);
}

/**
 * Rewrites the columns so a staged card renders where the participant put it.
 *
 * The caller's own column assignment is left alone in the data — staging is the
 * participant's, not the caller's, and the two must not be conflated. This is
 * the projection the participant sees until they sync.
 *
 * Exported for its tests.
 */
export function applyStagedColumns(
  columns: BoardColumn[],
  staged: Record<string, string>,
): BoardColumn[] {
  if (Object.keys(staged).length === 0) return columns;
  const known = new Set(columns.map((column) => column.id));
  return columns.map((column) => ({
    ...column,
    card_ids: (column.card_ids ?? [])
      // Out of this column if it was staged elsewhere...
      .filter((id) => {
        const target = staged[id];
        // A staged column the board does not have is ignored rather than
        // vanishing the card: the participant would otherwise lose a card to a
        // control that looked like it worked.
        return target === undefined || !known.has(target) || target === column.id;
      })
      // ...and into this one if it was staged here.
      .concat(
        Object.entries(staged)
          .filter(([id, target]) => target === column.id && !(column.card_ids ?? []).includes(id))
          .map(([id]) => id),
      ),
  }));
}

export function AppBoard({ envelope, onSubmit, onCancel, onDraft, roomID }: AppBoardProps) {
  const data = envelope.data ?? {};
  const boardID = data.board_id ?? "";
  const cards = useMemo(() => data.cards ?? [], [data.cards]);
  const filters = useMemo(() => data.filters ?? [], [data.filters]);
  const columns = useMemo(() => data.columns ?? [], [data.columns]);
  const detail = data.detail;

  // Seeded from the envelope once. Room keys the router on the envelope id, so
  // a different envelope is a different component and this cannot leak across
  // presentations; a supersede of the SAME envelope deliberately leaves the
  // participant's filters alone rather than resetting what they were doing.
  const [selection, setSelection] = useState<Record<string, string[]>>(() => {
    const seeded: Record<string, string[]> = {};
    for (const filter of filters) {
      seeded[filter.id] = filter.selected ?? [];
    }
    return seeded;
  });
  const [selectedCardID, setSelectedCardID] = useState<string | null>(detail?.card_id ?? null);
  const [detailOpen, setDetailOpen] = useState<boolean>(detail?.open ?? false);
  // Staged, not applied. Cleared by the next envelope rather than by this
  // component: a sync replaces the board, and the replacement is a different
  // envelope with a fresh draft sequence, so there is nothing to reset.
  const [staged, setStaged] = useState<Record<string, string>>({});
  // Notes, seeded from whatever the caller says is already on record.
  //
  // Seeding is what makes a note survive a sync in the participant's EYES. The
  // caller replies to a sync with a fresh board, so without this the box comes
  // back empty and the words they typed are gone from the screen even though
  // the caller has them. First use found that within a minute.
  //
  // Seeded from the envelope once, like `selection`: Room keys the router on
  // the envelope id, so a replacement board is a different component with a
  // different seed, and a supersede of the SAME envelope deliberately leaves
  // what the participant is typing alone.
  const [notes, setNotes] = useState<Record<string, string>>(() => {
    const seeded: Record<string, string> = {};
    for (const card of cards) {
      if (card.note !== undefined) seeded[card.id] = card.note;
    }
    return seeded;
  });
  // What was on record when this board arrived, so "sent" and "not sent yet"
  // are distinguishable. It is not state: it is a property of the envelope.
  const onRecord = useMemo(() => {
    const recorded: Record<string, string> = {};
    for (const card of cards) {
      if (card.note !== undefined) recorded[card.id] = card.note;
    }
    return recorded;
  }, [cards]);
  const [syncing, setSyncing] = useState(false);
  const [syncError, setSyncError] = useState<string | null>(null);

  const sync = data.sync;
  const syncEndpoint = sync?.enabled && isPluginRoutePath(sync.endpoint) ? sync.endpoint : null;
  const canStage = syncEndpoint !== null && (sync?.stage_label ?? "") !== "" && columns.length > 0;
  const canNote = syncEndpoint !== null && (sync?.note_label ?? "") !== "";

  const visible = useMemo(
    () => applyFilters(cards, filters, selection),
    [cards, filters, selection],
  );
  const stagedColumns = useMemo(() => applyStagedColumns(columns, staged), [columns, staged]);

  const publishDraft = (next: Partial<AppBoardDraft>, stagedNext = staged, notesNext = notes) => {
    if (!onDraft) return;
    onDraft({
      board_id: boardID,
      filters: selection,
      selected_card_id: selectedCardID,
      detail_open: detailOpen,
      refresh_requested: false,
      staged_changes: Object.fromEntries(
        Object.entries(stagedNext).map(([id, column]) => [id, { column_id: column }]),
      ),
      staged_notes: notesNext,
      ...next,
    } satisfies AppBoardDraft);
  };

  const toggleFilterValue = (filter: BoardFilter, value: string) => {
    const current = selection[filter.id] ?? [];
    const next =
      filter.kind === "single"
        ? current.includes(value)
          ? []
          : [value]
        : current.includes(value)
          ? current.filter((v) => v !== value)
          : [...current, value];
    const nextSelection = { ...selection, [filter.id]: next };
    setSelection(nextSelection);
    publishDraft({ filters: nextSelection });
  };

  const selectCard = (cardID: string) => {
    const sameCard = selectedCardID === cardID;
    const nextOpen = sameCard ? !detailOpen : true;
    setSelectedCardID(cardID);
    setDetailOpen(nextOpen);
    publishDraft({ selected_card_id: cardID, detail_open: nextOpen });
  };

  const requestRefresh = () => {
    // Explicit and pull-based. The flag rides in the draft until the caller
    // looks; nothing here re-queries anything and nothing is pushed.
    publishDraft({ refresh_requested: true });
  };

  const stageCard = (cardID: string, columnID: string) => {
    const next = { ...staged };
    // Staging a card back into the column it came from is unstaging it, not a
    // no-op change to push: a participant undoing a move should leave nothing
    // behind for the sync to apply.
    const home = columns.find((column) => (column.card_ids ?? []).includes(cardID));
    if (home?.id === columnID) {
      delete next[cardID];
    } else {
      next[cardID] = columnID;
    }
    setStaged(next);
    publishDraft({}, next);
  };

  // Typing updates local state only. A draft revision per keystroke would be a
  // durable write per keystroke — the store computes MAX(revision) + 1 on every
  // save — so the draft is published when the box loses focus instead. Pressing
  // Sync blurs it first, and runSync publishes from this render's state anyway,
  // so a note typed and immediately synced is not lost either way.
  const editNote = (cardID: string, note: string) => {
    setNotes((current) => {
      const next = { ...current };
      // The value is kept verbatim, INCLUDING an empty one, and this is the
      // difference between "nothing was ever written here" and "what was here
      // has been cleared". A card the participant never touched and that
      // carries no note on record has no key at all; a card whose box they
      // emptied has an empty one. The caller needs both to tell a new note from
      // a withdrawn one, and deleting the key would collapse them.
      next[cardID] = note;
      return next;
    });
  };

  const commitNote = () => {
    publishDraft({});
  };

  const runSync = async () => {
    if (!syncEndpoint || syncing) return;
    setSyncing(true);
    setSyncError(null);
    try {
      // The draft is what the server reads, so it has to be written before the
      // request is sent. publishDraft is synchronous into ws-client's send
      // path; a sync issued from a board with nothing staged is a plain
      // refresh, which is the other half of "both ways".
      publishDraft({});
      const response = await fetch(syncEndpoint, {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify({ board_id: boardID, room_id: roomID ?? "" }),
      });
      if (!response.ok) {
        const body = (await response.json().catch(() => ({}))) as { message?: string };
        setSyncError(body.message ?? `Sync failed (${response.status}).`);
        return;
      }
      // Nothing is applied to this component's state on success. The server
      // replaces the board, and the replacement arrives over the WebSocket as
      // the next envelope — reconciling here would mean two sources for what
      // the participant is looking at.
    } catch (error) {
      setSyncError(error instanceof Error ? error.message : "Sync failed.");
    } finally {
      setSyncing(false);
    }
  };

  const submitAction = (actionID: string) => {
    onSubmit({
      v: 1,
      envelopeId: envelope.id,
      kind: "data",
      status: "submitted",
      payload: {
        board_id: boardID,
        action_id: actionID,
        card_id: selectedCardID,
        filters: selection,
      },
      completedAt: new Date().toISOString(),
    });
  };

  // Notes that differ from what the caller has on record — the ones a sync
  // would actually change. A note that came back unchanged from the caller is
  // not pending anything, so marking it would tell the participant they have
  // unsaved work forever.
  const unsentNotes = useMemo(() => {
    const pending: Record<string, string> = {};
    for (const [cardID, note] of Object.entries(notes)) {
      if (note !== (onRecord[cardID] ?? "")) pending[cardID] = note;
    }
    return pending;
  }, [notes, onRecord]);

  const selectedCard = cards.find((card) => card.id === selectedCardID) ?? null;
  const showDetail = detailOpen && selectedCard !== null;

  return (
    <div className="space-y-4" data-testid="app-board">
      <header className="space-y-1">
        <div className="flex items-baseline justify-between gap-3">
          <h2 className="text-base font-medium text-zinc-100">
            {data.title ?? envelope.title ?? "Board"}
          </h2>
          {data.source?.label ? (
            <span className="text-xs text-zinc-500" data-testid="app-board-source">
              {data.source.label}
            </span>
          ) : null}
        </div>
        {envelope.context ? (
          <Markdown content={envelope.context} className="text-xs text-zinc-400" />
        ) : null}
      </header>

      <BoardFilterBar
        filters={filters}
        selection={selection}
        onToggle={toggleFilterValue}
        onRefresh={requestRefresh}
        onSync={syncEndpoint ? runSync : undefined}
        syncLabel={sync?.label ?? "Sync"}
        syncing={syncing}
        stagedCount={new Set([...Object.keys(staged), ...Object.keys(unsentNotes)]).size}
        scope={sync?.scope}
        showing={visible.length}
        supplied={cards.length}
      />

      {syncError ? (
        <p
          className="rounded border border-red-900 bg-red-950/40 px-3 py-2 text-xs text-red-300"
          data-testid="app-board-sync-error"
        >
          {syncError}
        </p>
      ) : null}

      <BoardColumns
        columns={stagedColumns}
        cards={visible}
        selectedCardID={selectedCardID}
        staged={staged}
        noted={unsentNotes}
        onSelect={selectCard}
        onStage={canStage ? stageCard : undefined}
      />

      {showDetail ? (
        <BoardDetailPane
          card={selectedCard}
          detail={detail}
          columns={canStage ? columns : []}
          stageLabel={sync?.stage_label ?? ""}
          stagedColumnID={staged[selectedCard.id] ?? null}
          onStage={stageCard}
          noteLabel={canNote ? (sync?.note_label ?? "") : ""}
          note={notes[selectedCard.id] ?? ""}
          noteOnRecord={onRecord[selectedCard.id]}
          onNoteChange={editNote}
          onNoteCommit={commitNote}
          onAction={submitAction}
          onClose={() => {
            setDetailOpen(false);
            publishDraft({ detail_open: false });
          }}
        />
      ) : null}

      <div className="flex justify-end">
        <Button variant="ghost" size="sm" onClick={onCancel} data-testid="app-board-cancel">
          Close board
        </Button>
      </div>
    </div>
  );
}

function BoardFilterBar({
  filters,
  selection,
  onToggle,
  onRefresh,
  onSync,
  syncLabel,
  syncing,
  stagedCount,
  scope,
  showing,
  supplied,
}: {
  filters: BoardFilter[];
  selection: Record<string, string[]>;
  onToggle: (filter: BoardFilter, value: string) => void;
  onRefresh: () => void;
  /** Absent when the caller offers no sync route; the board is then read-only. */
  onSync?: () => void;
  syncLabel: string;
  syncing: boolean;
  stagedCount: number;
  scope?: string;
  showing: number;
  supplied: number;
}) {
  if (filters.length === 0 && supplied === 0) return null;
  return (
    <div
      className="space-y-2 rounded border border-zinc-800 bg-zinc-900/40 p-3"
      data-testid="app-board-filters"
    >
      {filters.map((filter) => (
        <div key={filter.id} className="flex flex-wrap items-center gap-2">
          <span className="text-xs uppercase tracking-wide text-zinc-500">{filter.label}</span>
          {(filter.options ?? []).map((option) => {
            const active = (selection[filter.id] ?? []).includes(option.value);
            return (
              <button
                key={option.value}
                type="button"
                aria-pressed={active}
                onClick={() => onToggle(filter, option.value)}
                className={cn(
                  "rounded px-2 py-0.5 text-xs",
                  active
                    ? "bg-zinc-200 text-zinc-900"
                    : "bg-zinc-800 text-zinc-300 hover:bg-zinc-700",
                )}
              >
                {option.label ?? option.value}
                {typeof option.count === "number" ? (
                  <span className="ml-1 text-[10px] opacity-70">{option.count}</span>
                ) : null}
              </button>
            );
          })}
        </div>
      ))}
      <div className="flex items-center justify-between gap-3 pt-1">
        {/* Says out loud what the filter bar is. A reader who thinks this is a
            query will expect rows the caller never sent. The caller may add its
            own sentence about what it sent; both are shown, because "what was
            sent" and "what this bar does" are different facts. */}
        <p className="text-[11px] text-zinc-500" data-testid="app-board-scope">
          Showing {showing} of {supplied} supplied. Filters narrow what the caller sent
          {onSync ? " — press " : " — ask for a refresh"}
          {onSync ? <span className="text-zinc-400">{syncLabel}</span> : null}
          {onSync ? " to re-query." : " to see more."}
          {scope ? <span className="block pt-0.5 text-zinc-600">{scope}</span> : null}
        </p>
        <div className="flex shrink-0 items-center gap-2">
          {stagedCount > 0 ? (
            <span className="text-[11px] text-amber-400" data-testid="app-board-staged-count">
              {stagedCount} staged
            </span>
          ) : null}
          {onSync ? (
            <button
              type="button"
              onClick={onSync}
              disabled={syncing}
              className="rounded bg-zinc-800 px-2 py-0.5 text-xs text-zinc-300 hover:bg-zinc-700 disabled:opacity-50"
              data-testid="app-board-sync"
            >
              {syncing ? "Syncing…" : syncLabel}
            </button>
          ) : (
            <button
              type="button"
              onClick={onRefresh}
              className="rounded bg-zinc-800 px-2 py-0.5 text-xs text-zinc-300 hover:bg-zinc-700"
              data-testid="app-board-refresh"
            >
              Ask for fresh data
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

/**
 * The MIME type a dragged card is carried as.
 *
 * A private type rather than `text/plain`: a board must not accept a drop of
 * arbitrary text that happens to look like a card id, and dragging a card out
 * to another application should not paste an opaque identifier into it. The
 * drop handler also checks the id against the cards actually on the board, so
 * this is the outer of two gates rather than the only one.
 */
export const CARD_DRAG_TYPE = "application/x-tangent-board-card";

export function BoardColumns({
  columns,
  cards,
  selectedCardID,
  staged,
  noted,
  onSelect,
  onStage,
}: {
  columns: BoardColumn[];
  cards: BoardCardData[];
  selectedCardID: string | null;
  /** Card id → the column the participant staged it into, for the marker. */
  staged?: Record<string, string>;
  /** Card id → what the participant wrote about it, for the marker. */
  noted?: Record<string, string>;
  onSelect: (cardID: string) => void;
  /**
   * Stages a card into a column. Absent means the caller offers no staging, and
   * the cards are then not draggable and the columns are not drop targets.
   *
   * Dragging is a SECOND gesture onto the same view state, never a second
   * meaning: a drop writes `staged_changes` exactly as the detail pane's button
   * does, nothing reaches the owning application until Sync, and a board
   * abandoned mid-drag has changed nothing. The button path stays because it is
   * the keyboard and assistive-technology route — HTML5 drag is neither.
   */
  onStage?: (cardID: string, columnID: string) => void;
}) {
  const byID = new Map(cards.map((card) => [card.id, card]));
  const [dragOverColumn, setDragOverColumn] = useState<string | null>(null);
  // No columns is a legitimate board: one implicit column of everything.
  const effective: BoardColumn[] =
    columns.length > 0
      ? columns
      : [{ id: "__all", label: "All", card_ids: cards.map((c) => c.id) }];

  return (
    <div
      className="grid gap-3"
      style={{ gridTemplateColumns: `repeat(${Math.min(effective.length, 4)}, minmax(0, 1fr))` }}
      data-testid="app-board-columns"
    >
      {effective.map((column) => {
        // A card_id naming a card the caller did not supply is dropped rather
        // than rendered as an empty tile: the column is a projection of the
        // card set, and inventing a placeholder would show the participant a
        // record that does not exist.
        const columnCards = (column.card_ids ?? [])
          .map((id) => byID.get(id))
          .filter((card): card is BoardCardData => card !== undefined);
        const droppable = onStage !== undefined;
        return (
          // biome-ignore lint/a11y/noStaticElementInteractions: the drop target is a pointer affordance layered over the button path, which is what keyboard and AT use; a role here would advertise an interaction those users cannot perform
          <section
            key={column.id}
            className={cn(
              "space-y-2 rounded",
              droppable && dragOverColumn === column.id
                ? "outline outline-1 outline-zinc-500 bg-zinc-900/40"
                : "outline-none",
            )}
            data-testid={`app-board-column-${column.id}`}
            onDragOver={
              droppable
                ? (event) => {
                    // preventDefault is what makes an element a drop target at
                    // all; without it the browser refuses every drop and the
                    // card silently springs back.
                    if (!event.dataTransfer.types.includes(CARD_DRAG_TYPE)) return;
                    event.preventDefault();
                    event.dataTransfer.dropEffect = "move";
                    setDragOverColumn(column.id);
                  }
                : undefined
            }
            onDragLeave={
              droppable
                ? (event) => {
                    // Only when the pointer actually left this column. Moving
                    // between a column's own children fires dragleave on the
                    // section, and clearing on that makes the highlight strobe.
                    if (event.currentTarget.contains(event.relatedTarget as Node | null)) return;
                    setDragOverColumn((current) => (current === column.id ? null : current));
                  }
                : undefined
            }
            onDrop={
              droppable
                ? (event) => {
                    event.preventDefault();
                    setDragOverColumn(null);
                    const cardID = event.dataTransfer.getData(CARD_DRAG_TYPE);
                    // A card the board does not hold is not staged. The drag
                    // type already narrows this to our own drags; this is the
                    // second gate, and it is the one that matters if a stale
                    // drag survives a board replacement.
                    if (!cardID || !byID.has(cardID)) return;
                    onStage?.(cardID, column.id);
                  }
                : undefined
            }
          >
            <h3 className="text-xs uppercase tracking-wide text-zinc-500">
              {column.label}
              <span className="ml-1 text-zinc-600">{columnCards.length}</span>
            </h3>
            {columnCards.map((card) => (
              <BoardCard
                key={card.id}
                card={card}
                selected={card.id === selectedCardID}
                staged={staged?.[card.id] !== undefined}
                noted={noted?.[card.id] !== undefined}
                draggable={droppable}
                onSelect={onSelect}
              />
            ))}
          </section>
        );
      })}
    </div>
  );
}

export function BoardCard({
  card,
  selected,
  staged,
  noted,
  draggable,
  onSelect,
}: {
  card: BoardCardData;
  selected: boolean;
  /** Moved by the participant and not yet synced. */
  staged?: boolean;
  /** Written about by the participant and not yet synced. */
  noted?: boolean;
  /** Whether this card may be dragged into another column. */
  draggable?: boolean;
  onSelect: (cardID: string) => void;
}) {
  return (
    <button
      type="button"
      onClick={() => onSelect(card.id)}
      aria-pressed={selected}
      draggable={draggable}
      onDragStart={
        draggable
          ? (event) => {
              event.dataTransfer.setData(CARD_DRAG_TYPE, card.id);
              event.dataTransfer.effectAllowed = "move";
            }
          : undefined
      }
      data-testid={`app-board-card-${card.id}`}
      className={cn(
        "w-full space-y-1 rounded border p-2 text-left",
        draggable && "cursor-grab active:cursor-grabbing",
        selected
          ? "border-zinc-400 bg-zinc-800"
          : "border-zinc-800 bg-zinc-900 hover:border-zinc-700",
      )}
    >
      <div className="flex items-start gap-1.5 text-sm text-zinc-100">
        {staged ? (
          // A card that moved but has not been pushed anywhere. Without this
          // the board would claim a change the owning application has not seen.
          <span
            className="pt-0.5 text-amber-400"
            title="Staged — press Sync to apply"
            data-testid={`app-board-staged-${card.id}`}
          >
            •
          </span>
        ) : null}
        {noted ? (
          // Written about but not yet sent, for the same reason: an unsent note
          // is invisible from the column otherwise, and a participant who wrote
          // one and moved on has no way to find it again.
          <span
            className="pt-0.5 text-sky-400"
            title="Note written — press Sync to send it"
            data-testid={`app-board-noted-${card.id}`}
          >
            ✎
          </span>
        ) : null}
        <span>{card.title}</span>
      </div>
      {card.subtitle ? <div className="text-xs text-zinc-400">{card.subtitle}</div> : null}
      {card.badges && card.badges.length > 0 ? (
        <div className="flex flex-wrap gap-1 pt-0.5">
          {card.badges.map((badge, index) => (
            <span
              key={badge.id ?? `${badge.label}-${index}`}
              className={cn(
                "rounded px-1.5 py-0.5 text-[10px]",
                BADGE_TONES[badge.tone ?? "neutral"],
              )}
            >
              {badge.label}
            </span>
          ))}
        </div>
      ) : null}
      {card.body ? (
        // The shared renderer, the only markdown path in Tangent. tone="default"
        // is the envelope-surface tone; linkPolicy defaults to "reveal" because
        // the reader asked to see the caller's own output.
        <Markdown content={card.body} tone="default" className="pt-1 text-xs leading-6" />
      ) : null}
    </button>
  );
}

/**
 * What to say under the note box.
 *
 * Three states and they are genuinely different: nothing written, something
 * written that the caller has not seen, and something the caller already has.
 * Collapsing the last two is what made a participant think their note had been
 * thrown away when it had in fact been delivered.
 *
 * Exported for its tests.
 */
export function noteHint(note: string | undefined, onRecord: string | undefined): string {
  const current = note ?? "";
  const recorded = onRecord ?? "";
  if (current === recorded) {
    return recorded === "" ? "" : "On record with the caller. Edit to replace it.";
  }
  if (current.trim() === "" && recorded !== "") {
    return "Cleared. Press Sync to withdraw the note on record.";
  }
  return "Written down. Nothing has been sent until you press Sync.";
}

export function BoardDetailPane({
  card,
  detail,
  columns,
  stageLabel,
  stagedColumnID,
  onStage,
  noteLabel,
  note,
  noteOnRecord,
  onNoteChange,
  onNoteCommit,
  onAction,
  onClose,
}: {
  card: BoardCardData;
  detail?: BoardDetail;
  /** The columns this card may be staged into. Empty means staging is off. */
  columns?: BoardColumn[];
  stageLabel?: string;
  stagedColumnID?: string | null;
  onStage?: (cardID: string, columnID: string) => void;
  /** Names the note control. Empty means no note is offered. */
  noteLabel?: string;
  note?: string;
  /** What the caller has on record, so sent and unsent read differently. */
  noteOnRecord?: string;
  onNoteChange?: (cardID: string, note: string) => void;
  /** Called when the box loses focus; the draft is published there. */
  onNoteCommit?: () => void;
  onAction: (actionID: string) => void;
  onClose: () => void;
}) {
  const sections = detail?.card_id === card.id ? (detail?.sections ?? []) : [];
  const actions = detail?.actions ?? [];
  const stageColumns = columns ?? [];
  // The column the card is in right now, staged or not, so the control shows
  // the participant's own last answer rather than resetting to the caller's.
  const currentColumnID =
    stagedColumnID ??
    stageColumns.find((column) => (column.card_ids ?? []).includes(card.id))?.id ??
    null;
  return (
    <section
      className="space-y-3 rounded border border-zinc-700 bg-zinc-900 p-3"
      data-testid="app-board-detail"
    >
      <div className="flex items-start justify-between gap-3">
        <div>
          <h3 className="text-sm font-medium text-zinc-100">{card.title}</h3>
          {card.subtitle ? <p className="text-xs text-zinc-400">{card.subtitle}</p> : null}
          {detail?.raised_by ? (
            <p className="pt-0.5 text-[11px] text-zinc-500" data-testid="app-board-detail-origin">
              Opened by {detail.raised_by === "agent" ? "the agent" : "you"}
            </p>
          ) : null}
        </div>
        <button
          type="button"
          onClick={onClose}
          className="rounded bg-zinc-800 px-2 py-0.5 text-xs text-zinc-300 hover:bg-zinc-700"
          data-testid="app-board-detail-close"
        >
          Close
        </button>
      </div>

      {sections.map((section, index) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: a caller's sections are an ordered list replaced wholesale by the next revision; labels are not unique and nothing reorders in place
        <div key={`${section.label}-${index}`} className="space-y-1">
          <h4 className="text-xs uppercase tracking-wide text-zinc-500">{section.label}</h4>
          {section.markdown ? <Markdown content={section.markdown} tone="default" /> : null}
        </div>
      ))}

      {stageColumns.length > 0 && onStage ? (
        <div className="space-y-1" data-testid="app-board-stage">
          <h4 className="text-xs uppercase tracking-wide text-zinc-500">
            {stageLabel || "Move to"}
          </h4>
          <div className="flex flex-wrap gap-1.5">
            {stageColumns.map((column) => {
              const active = currentColumnID === column.id;
              return (
                <button
                  key={column.id}
                  type="button"
                  aria-pressed={active}
                  onClick={() => onStage(card.id, column.id)}
                  className={cn(
                    "rounded px-2 py-0.5 text-xs",
                    active
                      ? "bg-zinc-200 text-zinc-900"
                      : "bg-zinc-800 text-zinc-300 hover:bg-zinc-700",
                  )}
                  data-testid={`app-board-stage-${column.id}`}
                >
                  {column.label}
                </button>
              );
            })}
          </div>
          {stagedColumnID ? (
            // Says what has and has not happened. A control that looked like it
            // saved would be worse than no control.
            <p className="text-[11px] text-amber-400" data-testid="app-board-stage-note">
              Staged. Nothing has changed in the source until you press Sync.
            </p>
          ) : null}
        </div>
      ) : null}

      {noteLabel && onNoteChange ? (
        <div className="space-y-1" data-testid="app-board-note">
          <h4 className="text-xs uppercase tracking-wide text-zinc-500">{noteLabel}</h4>
          <textarea
            value={note ?? ""}
            onChange={(event) => onNoteChange(card.id, event.target.value)}
            onBlur={onNoteCommit}
            rows={3}
            className="w-full rounded border border-zinc-700 bg-zinc-950 px-2 py-1 text-xs text-zinc-200 placeholder:text-zinc-600 focus:border-zinc-500 focus:outline-none"
            placeholder="What should change?"
            data-testid={`app-board-note-${card.id}`}
          />
          {noteHint(note, noteOnRecord) ? (
            // Says what has and has not happened. A box that looked like it
            // saved would be worse than no box — and so would one that looked
            // unsaved forever after it was sent, which is why "on record" and
            // "changed since" are different sentences.
            <p
              className={cn(
                "text-[11px]",
                note !== (noteOnRecord ?? "") ? "text-sky-400" : "text-zinc-500",
              )}
              data-testid="app-board-note-hint"
            >
              {noteHint(note, noteOnRecord)}
            </p>
          ) : null}
        </div>
      ) : null}

      {actions.length > 0 ? (
        <div className="flex flex-wrap justify-end gap-2 pt-1">
          {actions.map((action) => (
            <Button
              key={action.id}
              size="sm"
              variant={ACTION_VARIANTS[action.tone ?? "neutral"]}
              onClick={() => onAction(action.id)}
              data-testid={`app-board-action-${action.id}`}
            >
              {action.label}
            </Button>
          ))}
        </div>
      ) : null}
    </section>
  );
}
