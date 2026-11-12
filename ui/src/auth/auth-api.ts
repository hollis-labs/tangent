// The identity module's browser routes (the same under every Substrate app).
// There is no setup call here on purpose: the first owner is created on the host
// with `tangent identity setup`, and POST /api/setup is always refused.

import { apiFetch, readApiError } from "@/lib/api";

export interface Profile {
  id: string;
  username: string;
  display_name: string;
  email: string;
  scopes: string[];
}

export interface SetupStatus {
  setup_required: boolean;
  setup_over_http: boolean;
}

/** A sign-in refusal the person can read: the server's own message. */
export class SignInError extends Error {
  readonly code: string;
  readonly status: number;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "SignInError";
    this.status = status;
    this.code = code;
  }
}

async function fail(response: Response): Promise<never> {
  const { status, code, message } = await readApiError(response);
  throw new SignInError(status, code, message);
}

/** The signed-in person, or null when there is no session. */
export async function fetchProfile(signal?: AbortSignal): Promise<Profile | null> {
  const response = await apiFetch("/api/identity/profile", {
    signal,
    headers: { Accept: "application/json" },
  });
  if (response.status === 401) return null;
  if (!response.ok) return fail(response);
  return (await response.json()) as Profile;
}

export async function fetchSetupStatus(signal?: AbortSignal): Promise<SetupStatus> {
  const response = await apiFetch("/api/setup", {
    signal,
    headers: { Accept: "application/json" },
  });
  if (!response.ok) return fail(response);
  return (await response.json()) as SetupStatus;
}

export async function signIn(username: string, password: string): Promise<Profile> {
  const response = await apiFetch("/api/login", {
    method: "POST",
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  });
  if (!response.ok) return fail(response);
  return (await response.json()) as Profile;
}

export async function signOut(): Promise<void> {
  const response = await apiFetch("/api/logout", {
    method: "POST",
    headers: { Accept: "application/json" },
  });
  // 401 means the session was already gone, which is what was asked for.
  if (!response.ok && response.status !== 401) return fail(response);
}
