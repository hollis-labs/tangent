// The browser room API client.
//
// The SPA used to POST JSON-RPC directly at `/mcp` for `session_list`,
// `session_get`, and `session_close`. That made the browser an unauthenticated
// MCP caller on a route with no origin, same-site, or CSRF middleware, and it
// is the coupling ADR 0004 §11 removes: `/api/rooms` is participant-session
// authenticated and origin guarded, exactly like `/api/hitl`.
//
// The payload shapes are unchanged — the server returns the same objects the
// `session_*` tools always did — so this is a transport change and not a
// contract change.

/** One room as the tab strip renders it. */
export interface RoomSummary {
  id: string;
  title?: string;
  current_envelope_type?: string;
  created_at: string;
  updated_at: string;
  /**
   * Connection lifecycle, reported next to the interaction summary rather
   * than folded into it: a room can be busy with nobody looking, and watched
   * with nothing to answer.
   */
  connection_count?: number;
  resolver_lease?: { connection_id: string; label?: string } | null;
}

/** The room projection the envelope components enrich themselves from. */
export interface RoomStatePayload {
  envelopes_history?: unknown[];
  wizard?: unknown;
  dashboard?: unknown;
  progress_panel?: unknown;
  file_picker?: unknown;
  diff_review?: unknown;
  approval_queue?: unknown;
  form_collect?: unknown;
  spreadsheet_review?: unknown;
  whiteboard?: unknown;
  synthesis_notes?: unknown;
  current_draft?: unknown;
  prose_revision_outcomes?: unknown[];
  final_output?: unknown;
}

/** List the rooms this browser may see. */
export async function fetchRooms(activeOnly = true): Promise<RoomSummary[]> {
  const payload = await requestRooms<{ rooms?: RoomSummary[] }>(
    `/api/rooms?active_only=${activeOnly ? "true" : "false"}`,
    { method: "GET" },
  );
  return payload.rooms ?? [];
}

/** Read one room's projection. */
export async function fetchRoomState(roomID: string): Promise<RoomStatePayload> {
  return requestRooms<RoomStatePayload>(`/api/rooms/${encodeURIComponent(roomID)}`, {
    method: "GET",
  });
}

/** Close one room. */
export async function closeRoom(roomID: string): Promise<void> {
  await requestRooms<{ ok?: boolean }>(`/api/rooms/${encodeURIComponent(roomID)}/close`, {
    method: "POST",
  });
}

async function requestRooms<T>(url: string, init: RequestInit): Promise<T> {
  const response = await fetch(url, {
    ...init,
    headers: { Accept: "application/json", ...(init.headers ?? {}) },
    // The session cookie is HttpOnly and same-origin; sending it is the whole
    // point of the migration, and omitting credentials would silently put the
    // browser back to being an unauthenticated caller.
    credentials: "same-origin",
  });
  if (!response.ok) {
    throw new Error(await describeRoomFailure(response));
  }
  return (await response.json()) as T;
}

/**
 * Turn a refused or failed room request into one operator-facing sentence.
 *
 * The two refusal shapes are the server's contract and stay distinguishable:
 * 403 is a refusal inside this machine's own authority, 404 is either an
 * unknown room or one belonging to an authority this browser cannot see. The
 * server deliberately never says which of those a 404 was, so neither does
 * this.
 */
async function describeRoomFailure(response: Response): Promise<string> {
  const code = await readFailureCode(response);
  if (response.status === 403 || code === "forbidden") {
    return "Not authorized for that room. Reload Tangent to get a fresh session, then try again.";
  }
  if (response.status === 404 || code === "not_found") {
    return "That room is no longer available.";
  }
  return `Room request failed (HTTP ${response.status}).`;
}

async function readFailureCode(response: Response): Promise<string> {
  try {
    const body = (await response.json()) as { code?: unknown };
    return typeof body.code === "string" ? body.code : "";
  } catch {
    return "";
  }
}
