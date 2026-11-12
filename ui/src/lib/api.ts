// The one way the SPA talks to Tangent's own HTTP API.
//
// Tangent signs the owner in through the identity module: a session cookie, sent
// by the browser on every same-origin request (`fetch` does so by default).
// Identity's CSRF rule asks the page to say, on every unsafe request, that it is a page: `X-Requested-With`.
// `apiFetch` adds it, and only for a same-origin path, so a caller-supplied
// absolute URL never receives a header meant for Tangent. A 401 that is not a
// wrong password means the session is gone: listeners (`onUnauthorized`) hear it.
//
// Errors from Tangent are flat: `{ "code", "message", "details"? }`. `readApiError`
// reads that shape and nothing older. A body that is not that shape (a proxy's
// page, an empty body) yields a code from the status and a plain message.

const UNSAFE_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"]);

/** The value sent as `X-Requested-With`. Identity checks only that the header is present. */
export const REQUESTED_WITH = "tangent";

/**
 * 401 codes that mean "those credentials were wrong", not "your session ended".
 * Every other 401 from Tangent is the signed-out signal.
 */
const CREDENTIAL_CODES = new Set(["invalid_login"]);

const unauthorizedListeners = new Set<() => void>();

/** Calls `listener` whenever a request is answered 401 because the session is gone. */
export function onUnauthorized(listener: () => void): () => void {
  unauthorizedListeners.add(listener);
  return () => unauthorizedListeners.delete(listener);
}

async function signalIfSignedOut(response: Response): Promise<void> {
  if (response.status !== 401) return;
  const { code } = await readApiError(response);
  if (CREDENTIAL_CODES.has(code)) return;
  for (const listener of [...unauthorizedListeners]) listener();
}

export interface ApiError {
  status: number;
  code: string;
  message: string;
  details?: unknown;
}

function isSameOriginPath(input: string): boolean {
  return input.startsWith("/") && !input.startsWith("//");
}

/**
 * `fetch` for Tangent's API: `X-Requested-With` on POST, PUT, PATCH and DELETE
 * to a same-origin path. Everything else about the request is the caller's and
 * passes through untouched (`fetch` already sends the session cookie to the
 * same origin).
 */
export async function apiFetch(input: string, init?: RequestInit): Promise<Response> {
  const response = await send(input, init);
  if (isSameOriginPath(input)) await signalIfSignedOut(response);
  return response;
}

function send(input: string, init?: RequestInit): Promise<Response> {
  if (!init) return fetch(input);
  const method = (init.method ?? "GET").toUpperCase();
  if (!isSameOriginPath(input) || !UNSAFE_METHODS.has(method)) return fetch(input, init);
  const headers = new Headers(init.headers);
  if (headers.has("X-Requested-With")) return fetch(input, init);
  return fetch(input, { ...init, headers: withRequestedWith(init.headers) });
}

// A plain-object header bag stays a plain object, so callers and tests that read
// it by the names they wrote keep working.
function withRequestedWith(headers: HeadersInit | undefined): HeadersInit {
  if (headers instanceof Headers || Array.isArray(headers)) {
    const copy = new Headers(headers);
    copy.set("X-Requested-With", REQUESTED_WITH);
    return copy;
  }
  return { ...headers, "X-Requested-With": REQUESTED_WITH };
}

/** Reads the flat error body of a non-OK response. Never throws. */
export async function readApiError(response: Response): Promise<ApiError> {
  let body: unknown;
  try {
    body = await response.clone().json();
  } catch {
    body = undefined;
  }
  const record = body && typeof body === "object" ? (body as Record<string, unknown>) : {};
  const code = typeof record.code === "string" && record.code ? record.code : "http_error";
  const message =
    typeof record.message === "string" && record.message
      ? record.message
      : `Request failed (HTTP ${response.status}).`;
  return { status: response.status, code, message, details: record.details };
}
