// Operator elevation (CW-20261016-0174).
//
// A browser session minted from loopback admission can view the queues but
// cannot decide anything: approving, denying, replying and acknowledging need
// `operator-token` assurance. When the server refuses a request with
// `assurance_insufficient`, `fetchWithElevation` asks the operator for the
// token (through `OperatorTokenPrompt`), posts it to the elevation endpoint,
// and retries the original request once.
//
// The token is held only for the duration of one POST. It is never written to
// localStorage, sessionStorage, or any module state; the prompt clears its
// input after submit. The server rotates the session cookie on success, so the
// retry carries the elevated session automatically.

export const ELEVATE_PATH = "/api/session/elevate";
export const ASSURANCE_INSUFFICIENT = "assurance_insufficient";

export interface ElevationPromptState {
  open: boolean;
  submitting: boolean;
  error: string;
}

const closedState: ElevationPromptState = { open: false, submitting: false, error: "" };

let state: ElevationPromptState = closedState;
let pending: { promise: Promise<boolean>; settle: (elevated: boolean) => void } | null = null;
const listeners = new Set<() => void>();

function setState(next: ElevationPromptState) {
  state = next;
  for (const listener of listeners) listener();
}

export function subscribeElevationPrompt(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function getElevationPromptState(): ElevationPromptState {
  return state;
}

/**
 * Opens the operator-token prompt and resolves true once the session is
 * elevated, false if the operator dismisses it. Concurrent callers share one
 * prompt.
 */
export function requestOperatorElevation(): Promise<boolean> {
  if (pending) return pending.promise;
  let settle: (elevated: boolean) => void = () => {};
  const promise = new Promise<boolean>((resolve) => {
    settle = resolve;
  });
  pending = { promise, settle };
  setState({ open: true, submitting: false, error: "" });
  return promise;
}

function finish(elevated: boolean) {
  const current = pending;
  pending = null;
  setState(closedState);
  current?.settle(elevated);
}

export function cancelOperatorElevation(): void {
  finish(false);
}

/** Posts the token. The caller must discard its copy whatever the outcome. */
export async function submitOperatorToken(token: string): Promise<boolean> {
  if (!token) {
    setState({ ...state, error: "Enter the operator token." });
    return false;
  }
  setState({ ...state, submitting: true, error: "" });
  try {
    const response = await fetch(ELEVATE_PATH, {
      method: "POST",
      headers: { Accept: "application/json", "Content-Type": "application/json" },
      body: JSON.stringify({ token }),
    });
    if (response.ok) {
      finish(true);
      return true;
    }
    const body = (await response.json().catch(() => ({}))) as { message?: string };
    setState({
      open: true,
      submitting: false,
      error:
        response.status === 429
          ? body.message || "Too many attempts. Wait a minute and try again."
          : "That token was not accepted.",
    });
    return false;
  } catch {
    setState({ open: true, submitting: false, error: "Tangent could not be reached." });
    return false;
  }
}

async function isAssuranceRefusal(response: Response): Promise<boolean> {
  if (response.status !== 403) return false;
  try {
    const body = (await response.clone().json()) as { code?: string };
    return body?.code === ASSURANCE_INSUFFICIENT;
  } catch {
    return false;
  }
}

/**
 * `fetch`, plus one elevation round-trip: a 403 `assurance_insufficient` opens
 * the operator-token prompt and, once the session is elevated, the request is
 * retried exactly once. A dismissed prompt returns the original refusal.
 */
export async function fetchWithElevation(input: string, init?: RequestInit): Promise<Response> {
  const response = await fetch(input, init);
  if (!(await isAssuranceRefusal(response))) return response;
  if (!(await requestOperatorElevation())) return response;
  return fetch(input, init);
}
