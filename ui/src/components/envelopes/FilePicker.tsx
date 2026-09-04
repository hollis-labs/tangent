import { useEffect, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { FieldMessage, RequiredMark } from "@/components/ui/field";
import { SubmitGateNotice } from "@/components/ui/submit-gate-notice";
import {
  clearFilePickerDraft,
  FILE_PICKER_AUTOSAVE_DEBOUNCE_MS,
  loadFilePickerDraft,
  saveFilePickerDraft,
} from "@/lib/file-picker-draft-storage";
import { buildSubmitGate, useRevealRequirement } from "@/lib/submit-gate";
import { cn } from "@/lib/utils";

// Control ids. The file checkboxes are the only repeated control, so they
// derive their id from the same ref key the selection set is keyed by — which
// is what lets the submit gate point at a specific checkbox.
const SEARCH_ID = "file-picker-search";
const SORT_ID = "file-picker-sort";
const FILES_HINT_ID = "file-picker-files-hint";

function fileControlID(key: string): string {
  return `file-picker-file-checkbox-${key}`;
}

type BrowseRoot = {
  root_id: string;
  label?: string;
  path: string;
  kind?: string;
};

type FilePickerRef = {
  artifact_id?: string;
  name?: string;
  uri?: string;
  mime_type?: string;
  kind?: string;
  size_bytes?: number;
  root_id: string;
  relative_path: string;
};

type QueryState = {
  current_root_id?: string;
  current_dir?: string;
  search?: string;
  sort?: string;
  [key: string]: unknown;
};

export interface FilePickerEnvelope {
  v: number;
  id: string;
  type: "tangent.file-picker";
  title?: string;
  context?: string;
  data?: {
    picker_id: string;
    browse_roots?: BrowseRoot[];
    selected_refs?: FilePickerRef[];
    query_state?: QueryState;
    files?: FilePickerRef[];
    updated_at?: string;
  };
}

export interface FilePickerResponse {
  v: 1;
  envelopeId: string;
  kind: "data";
  status: "submitted";
  payload: {
    picker_id: string;
    selected_refs: FilePickerRef[];
    query_state: QueryState;
  };
  completedAt?: string;
}

type FilePickerProps = {
  envelope: FilePickerEnvelope;
  onSubmit: (response: FilePickerResponse) => void;
  onCancel: () => void;
  roomID?: string;
};

export function FilePicker({ envelope, onSubmit, onCancel, roomID }: FilePickerProps) {
  const pickerID = envelope.data?.picker_id ?? "";
  const [draft] = useState(() =>
    roomID && pickerID ? loadFilePickerDraft(roomID, pickerID) : null,
  );
  const browseRoots = useMemo(
    () => normalizeBrowseRoots(envelope.data?.browse_roots),
    [envelope.data],
  );
  const initialSelected = useMemo(
    () => normalizeRefs(envelope.data?.selected_refs),
    [envelope.data?.selected_refs],
  );
  const availableFiles = useMemo(
    () => buildAvailableFiles(envelope.data?.files, initialSelected),
    [envelope.data?.files, initialSelected],
  );
  const [activeRootID, setActiveRootID] = useState(
    () =>
      (draft?.activeRootID || normalizeQueryState(envelope.data?.query_state).current_root_id) ??
      browseRoots[0]?.root_id ??
      "",
  );
  const [currentDir, setCurrentDir] = useState(
    () => (draft?.currentDir || normalizeQueryState(envelope.data?.query_state).current_dir) ?? "",
  );
  const [search, setSearch] = useState(
    () => (draft?.search || normalizeQueryState(envelope.data?.query_state).search) ?? "",
  );
  const [sort, setSort] = useState(
    () => (draft?.sort || normalizeQueryState(envelope.data?.query_state).sort) ?? "name:asc",
  );
  const [selectedKeys, setSelectedKeys] = useState(
    () => new Set(draft?.selectedKeys?.length ? draft.selectedKeys : initialSelected.map(refKey)),
  );
  const [previewKey, setPreviewKey] = useState(
    () => draft?.previewKey || refKey(initialSelected[0] ?? { root_id: "", relative_path: "" }),
  );
  const [message, setMessage] = useState<string | null>(
    draft ? "Recovered unsent file-picker state from this browser." : null,
  );
  const lastSavedDraftRef = useRef<string | null>(draft ? JSON.stringify(draft) : null);

  const directoryOptions = useMemo(
    () => listDirectories(availableFiles, activeRootID, currentDir),
    [activeRootID, availableFiles, currentDir],
  );
  const visibleFiles = useMemo(
    () =>
      applyFilters(availableFiles, {
        activeRootID,
        currentDir,
        search,
        sort,
      }),
    [activeRootID, availableFiles, currentDir, search, sort],
  );

  const selectedRefs = useMemo(
    () => availableFiles.filter((item) => selectedKeys.has(refKey(item))),
    [availableFiles, selectedKeys],
  );
  const previewItem = useMemo(
    () => availableFiles.find((item) => refKey(item) === previewKey) ?? visibleFiles[0] ?? null,
    [availableFiles, previewKey, visibleFiles],
  );

  useEffect(() => {
    if (!roomID || !pickerID) {
      return;
    }
    const nextDraft = {
      activeRootID,
      currentDir,
      search,
      sort,
      selectedKeys: [...selectedKeys],
      previewKey,
    };
    const serialized = JSON.stringify(nextDraft);
    if (serialized === lastSavedDraftRef.current) {
      return;
    }
    const handle = window.setTimeout(() => {
      saveFilePickerDraft(roomID, pickerID, nextDraft);
      lastSavedDraftRef.current = serialized;
    }, FILE_PICKER_AUTOSAVE_DEBOUNCE_MS);
    return () => window.clearTimeout(handle);
  }, [activeRootID, currentDir, pickerID, previewKey, roomID, search, selectedKeys, sort]);

  const revealRequirement = useRevealRequirement();

  // Submit stays live and validates on click — that is this picker's shape, and
  // it is unchanged. What changes is where the answer lands: the failure used
  // to render inside the file-browser card, above the list, while the button
  // sits below the whole preview card, so an operator scrolled to Submit saw
  // nothing happen at all. The gate notice sits in the button's own row, and
  // the click takes focus to the first checkbox that can clear it.
  const gate = buildSubmitGate([
    selectedRefs.length === 0 && {
      controlID: visibleFiles.length > 0 ? fileControlID(refKey(visibleFiles[0])) : "",
      label: "the file list",
      message:
        visibleFiles.length > 0
          ? "no files are selected — tick at least one file in the list."
          : "no files are selected, and this root has no file candidates to tick.",
    },
  ]);

  const handleSubmit = () => {
    if (gate.blocked) {
      revealRequirement(gate.first);
      return;
    }
    setMessage(null);
    if (roomID && pickerID) {
      clearFilePickerDraft(roomID, pickerID);
    }
    onSubmit({
      v: 1,
      envelopeId: envelope.id,
      kind: "data",
      status: "submitted",
      payload: {
        picker_id: pickerID,
        selected_refs: selectedRefs,
        query_state: {
          ...normalizeQueryState(envelope.data?.query_state),
          current_root_id: activeRootID || undefined,
          current_dir: currentDir || undefined,
          search: search || undefined,
          sort,
        },
      },
    });
  };

  return (
    <Card data-testid="file-picker-root">
      <CardHeader className="space-y-2">
        <CardTitle>{envelope.title ?? "File picker"}</CardTitle>
        <p className="text-sm text-zinc-400">
          Select durable artifact-backed files from the allowed roots, then submit one canonical
          selection.
        </p>
      </CardHeader>
      <CardContent className="grid gap-4 lg:grid-cols-[260px_minmax(0,1fr)]">
        <div className="space-y-3">
          <div className="space-y-2">
            <fieldset
              className="m-0 min-w-0 space-y-2 border-0 p-0"
              data-testid="file-picker-roots"
            >
              <legend className="text-xs font-medium uppercase tracking-[0.18em] text-zinc-500">
                Roots
              </legend>
              {browseRoots.map((root) => (
                <button
                  key={root.root_id}
                  type="button"
                  data-testid={`file-picker-root-${root.root_id}`}
                  // One choice, not several actions: the active root was
                  // distinguishable only by an emerald border.
                  aria-pressed={activeRootID === root.root_id}
                  onClick={() => {
                    setActiveRootID(root.root_id);
                    setCurrentDir("");
                  }}
                  className={cn(
                    "w-full rounded-xl border px-3 py-3 text-left transition",
                    activeRootID === root.root_id
                      ? "border-emerald-500 bg-emerald-500/10 text-zinc-50"
                      : "border-zinc-800 bg-zinc-950/60 text-zinc-300 hover:border-zinc-700",
                  )}
                >
                  <div className="font-medium">{root.label ?? root.root_id}</div>
                  <div className="text-xs text-zinc-500">{root.path}</div>
                </button>
              ))}
            </fieldset>
          </div>
          <div className="rounded-xl border border-zinc-800 bg-zinc-950/50 p-3">
            <p className="text-xs font-medium uppercase tracking-[0.18em] text-zinc-500">
              Selected
            </p>
            <p
              className="mt-2 text-2xl font-semibold text-zinc-50"
              data-testid="file-picker-selected-count"
            >
              {selectedRefs.length}
            </p>
            <p className="text-xs text-zinc-500">artifact-backed files queued for submit</p>
          </div>
        </div>

        <div className="space-y-3">
          <div className="rounded-2xl border border-zinc-800 bg-zinc-950/50 p-3">
            <div className="flex items-center justify-between gap-3">
              <div>
                <p className="text-sm font-medium text-zinc-100">
                  {readRootLabel(browseRoots, activeRootID) ?? "Available files"}
                </p>
                <p className="text-xs text-zinc-500">
                  {visibleFiles.length > 0
                    ? "Selections are submitted as durable artifact refs."
                    : "No file candidates are loaded for this root yet."}
                </p>
              </div>
            </div>
            {/*
              The root crumb's entire accessible name was the literal "/", and
              nothing said which crumb was the directory currently shown.
            */}
            <nav
              className="mt-3 flex flex-wrap gap-2"
              data-testid="file-picker-breadcrumbs"
              aria-label="Current directory"
            >
              <button
                type="button"
                className="rounded-full border border-zinc-700 px-2 py-1 text-xs text-zinc-300"
                aria-label="Root of this browse root"
                aria-current={currentDir === "" ? "location" : undefined}
                onClick={() => setCurrentDir("")}
              >
                /
              </button>
              {breadcrumbSegments(currentDir).map((segment, index, all) => {
                const nextDir = all.slice(0, index + 1).join("/");
                return (
                  <button
                    key={nextDir}
                    type="button"
                    className="rounded-full border border-zinc-700 px-2 py-1 text-xs text-zinc-300"
                    aria-current={nextDir === currentDir ? "location" : undefined}
                    onClick={() => setCurrentDir(nextDir)}
                  >
                    {segment}
                  </button>
                );
              })}
            </nav>
            <div className="mt-3 grid gap-2 md:grid-cols-[minmax(0,1fr)_180px]">
              <div className="space-y-1">
                <label className="block text-xs text-zinc-400" htmlFor={SEARCH_ID}>
                  Search files
                </label>
                <input
                  id={SEARCH_ID}
                  type="search"
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                  placeholder="Search file names or paths"
                  data-testid="file-picker-search"
                  className="w-full rounded-xl border border-zinc-800 bg-zinc-950/70 px-3 py-2 text-sm text-zinc-100"
                />
              </div>
              <div className="space-y-1">
                <label className="block text-xs text-zinc-400" htmlFor={SORT_ID}>
                  Sort files
                </label>
                <select
                  id={SORT_ID}
                  value={sort}
                  onChange={(event) => setSort(event.target.value)}
                  data-testid="file-picker-sort"
                  className="w-full rounded-xl border border-zinc-800 bg-zinc-950/70 px-3 py-2 text-sm text-zinc-100"
                >
                  <option value="name:asc">Name asc</option>
                  <option value="name:desc">Name desc</option>
                  <option value="path:asc">Path asc</option>
                  <option value="path:desc">Path desc</option>
                </select>
              </div>
            </div>
            {directoryOptions.length > 0 ? (
              <div className="mt-3 flex flex-wrap gap-2" data-testid="file-picker-directories">
                {directoryOptions.map((dir) => (
                  <button
                    key={dir}
                    type="button"
                    className="rounded-full border border-zinc-800 px-2 py-1 text-xs text-zinc-300 hover:border-zinc-700"
                    onClick={() => setCurrentDir(dir)}
                  >
                    {dir.split("/").pop()}
                  </button>
                ))}
              </div>
            ) : null}
            {/*
              Draft-recovery notice, not validation: it stays with the browser
              state it describes. The "nothing selected" refusal moved to the
              Submit row, where the operator who triggered it is looking.
            */}
            {message ? (
              <p
                className="mt-3 text-sm text-emerald-300"
                data-testid="file-picker-message"
                role="status"
                aria-live="polite"
              >
                {message}
              </p>
            ) : null}
            {/*
              The checkboxes had no id, no name and no grouping, so "at least
              one file" — the only rule this workflow has — had no programmatic
              expression at all. The fieldset carries the rule; each checkbox
              carries an id the gate can focus.
            */}
            <fieldset
              className="mt-3 min-w-0 space-y-2 border-0 p-0"
              data-testid="file-picker-files"
              aria-describedby={FILES_HINT_ID}
            >
              <legend className="text-xs font-medium uppercase tracking-[0.18em] text-zinc-500">
                Files
                <RequiredMark
                  active={selectedRefs.length === 0}
                  testID="file-picker-files-required"
                />
              </legend>
              {visibleFiles.map((item) => {
                const key = refKey(item);
                const checked = selectedKeys.has(key);
                const controlID = fileControlID(key);
                return (
                  <label
                    key={key}
                    htmlFor={controlID}
                    data-testid={`file-picker-file-${key}`}
                    className={cn(
                      "flex cursor-pointer items-start gap-3 rounded-xl border px-3 py-3 transition",
                      checked
                        ? "border-emerald-500 bg-emerald-500/10"
                        : "border-zinc-800 bg-zinc-950/40 hover:border-zinc-700",
                    )}
                  >
                    <input
                      id={controlID}
                      name="file-picker-selection"
                      type="checkbox"
                      checked={checked}
                      data-testid={`file-picker-file-checkbox-${key}`}
                      aria-label={`${item.name ?? basename(item.relative_path)} (${item.relative_path})`}
                      onClick={() => setPreviewKey(key)}
                      onChange={() => {
                        setSelectedKeys((current) => {
                          const next = new Set(current);
                          if (next.has(key)) {
                            next.delete(key);
                          } else {
                            next.add(key);
                          }
                          return next;
                        });
                      }}
                    />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium text-zinc-100">
                        {item.name ?? basename(item.relative_path)}
                      </p>
                      <p className="truncate text-xs text-zinc-500">{item.relative_path}</p>
                      {item.uri ? (
                        <p className="truncate text-xs text-zinc-600">{item.uri}</p>
                      ) : null}
                    </div>
                  </label>
                );
              })}
              <FieldMessage id={FILES_HINT_ID}>
                Select at least one file. Every ticked file is submitted as a durable artifact ref.
              </FieldMessage>
            </fieldset>
          </div>

          <div
            className="rounded-2xl border border-zinc-800 bg-zinc-950/50 p-4"
            data-testid="file-picker-preview"
          >
            <p className="text-xs font-medium uppercase tracking-[0.18em] text-zinc-500">Preview</p>
            {previewItem ? (
              <div className="mt-3 space-y-2">
                <p className="text-sm font-medium text-zinc-100">
                  {previewItem.name ?? basename(previewItem.relative_path)}
                </p>
                <p className="text-xs text-zinc-500">{previewItem.relative_path}</p>
                <p className="text-xs text-zinc-600">{previewItem.uri ?? "No durable URI"}</p>
                <p className="text-xs text-zinc-500">
                  {previewItem.mime_type ?? "unknown type"}
                  {typeof previewItem.size_bytes === "number"
                    ? ` • ${previewItem.size_bytes} bytes`
                    : ""}
                </p>
              </div>
            ) : (
              <p className="mt-3 text-sm text-zinc-500">
                Select a file to inspect its persisted metadata.
              </p>
            )}
          </div>

          <div className="flex flex-wrap items-center justify-end gap-3">
            <SubmitGateNotice
              gate={gate}
              testID="file-picker-submit-gate"
              action="Submit"
              mode="attempt"
              onReveal={revealRequirement}
            />
            <Button type="button" variant="outline" onClick={onCancel}>
              Cancel
            </Button>
            <Button
              type="button"
              data-testid="file-picker-submit"
              onClick={handleSubmit}
              aria-describedby={gate.blocked ? "file-picker-submit-gate" : undefined}
            >
              Submit selection
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

function normalizeBrowseRoots(input: BrowseRoot[] | undefined): BrowseRoot[] {
  return (input ?? [])
    .map((item) => ({
      root_id: readString(item.root_id),
      label: readString(item.label) || undefined,
      path: readString(item.path),
      kind: readString(item.kind) || undefined,
    }))
    .filter((item) => item.root_id && item.path);
}

function normalizeRefs(input: FilePickerRef[] | undefined): FilePickerRef[] {
  return (input ?? [])
    .map((item) => ({
      artifact_id: readString(item.artifact_id) || undefined,
      name: readString(item.name) || undefined,
      uri: readString(item.uri) || undefined,
      mime_type: readString(item.mime_type) || undefined,
      kind: readString(item.kind) || undefined,
      size_bytes: typeof item.size_bytes === "number" ? item.size_bytes : undefined,
      root_id: readString(item.root_id),
      relative_path: readString(item.relative_path),
    }))
    .filter((item) => item.root_id && item.relative_path);
}

function normalizeQueryState(input: QueryState | undefined): QueryState {
  return input && typeof input === "object" ? { ...input } : {};
}

function buildAvailableFiles(
  input: FilePickerRef[] | undefined,
  selected: FilePickerRef[],
): FilePickerRef[] {
  const seen = new Set<string>();
  const out: FilePickerRef[] = [];
  for (const item of [...normalizeRefs(input), ...selected]) {
    const key = refKey(item);
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    out.push(item);
  }
  return out;
}

function applyFilters(
  items: FilePickerRef[],
  options: {
    activeRootID: string;
    currentDir: string;
    search: string;
    sort: string;
  },
): FilePickerRef[] {
  const needle = options.search.trim().toLowerCase();
  const dirPrefix = options.currentDir ? `${options.currentDir}/` : "";
  const visible = items.filter((item) => {
    if (options.activeRootID && item.root_id !== options.activeRootID) {
      return false;
    }
    if (options.currentDir) {
      if (
        !(item.relative_path === options.currentDir || item.relative_path.startsWith(dirPrefix))
      ) {
        return false;
      }
    }
    if (!needle) {
      return true;
    }
    return [item.name, item.relative_path, item.uri]
      .filter((value): value is string => Boolean(value))
      .some((value) => value.toLowerCase().includes(needle));
  });
  return [...visible].sort((left, right) => compareRefs(left, right, options.sort));
}

function compareRefs(left: FilePickerRef, right: FilePickerRef, sort: string): number {
  const [field, direction] = sort.split(":");
  const leftValue =
    field === "path" ? left.relative_path : (left.name ?? basename(left.relative_path));
  const rightValue =
    field === "path" ? right.relative_path : (right.name ?? basename(right.relative_path));
  const result = leftValue.localeCompare(rightValue);
  return direction === "desc" ? result * -1 : result;
}

function listDirectories(items: FilePickerRef[], rootID: string, currentDir: string): string[] {
  const seen = new Set<string>();
  const prefix = currentDir ? `${currentDir}/` : "";
  for (const item of items) {
    if (rootID && item.root_id !== rootID) {
      continue;
    }
    if (currentDir && !item.relative_path.startsWith(prefix)) {
      continue;
    }
    const rest = currentDir ? item.relative_path.slice(prefix.length) : item.relative_path;
    const [first] = rest.split("/");
    if (!first || !rest.includes("/")) {
      continue;
    }
    const next = currentDir ? `${currentDir}/${first}` : first;
    seen.add(next);
  }
  return [...seen].sort((left, right) => left.localeCompare(right));
}

function breadcrumbSegments(value: string): string[] {
  return value ? value.split("/").filter(Boolean) : [];
}

function refKey(item: Pick<FilePickerRef, "root_id" | "relative_path">): string {
  if (!item.root_id && !item.relative_path) {
    return "";
  }
  return `${item.root_id}:${item.relative_path}`;
}

function readRootLabel(roots: BrowseRoot[], rootID: string): string | null {
  return roots.find((item) => item.root_id === rootID)?.label ?? null;
}

function basename(value: string): string {
  const parts = value.split("/");
  return parts[parts.length - 1] ?? value;
}

function readString(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}
