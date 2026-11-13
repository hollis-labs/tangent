import { apiFetch } from "@/lib/api";

/**
 * Asks who is signed in and discards the answer. A 401 reaches `onUnauthorized`
 * (see api.ts), which is the point: the server also says `not_authorized` on a
 * room socket once a session is revoked, and this settles whether that is a
 * session that ended or a person whose scope does not allow answering.
 */
export async function probeSession(): Promise<void> {
  await apiFetch("/api/identity/profile", { headers: { Accept: "application/json" } });
}
