import { useIdentity } from "@hollis-labs/kit-identity/client";
import { useEffect } from "react";
import { onUnauthorized } from "@/lib/api";

/**
 * The identity kit moves to its "session expired" sign-in when one of its own
 * requests is answered 401. The rest of the app's requests (apiFetch) report the
 * same fact through `onUnauthorized`; this asks the kit a question only a live
 * session answers, so its 401 puts the gate on the sign-in screen with the
 * kit's own notice.
 */
export function SessionLossBridge() {
  const { client } = useIdentity();
  useEffect(
    () =>
      onUnauthorized(() => {
        void client.listSessions().catch(() => {});
      }),
    [client],
  );
  return null;
}
