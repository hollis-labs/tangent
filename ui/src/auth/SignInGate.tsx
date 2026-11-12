// Nothing of Tangent renders, and nothing connects, until a person is signed in.
//
// States: checking (no flash of a form), signed out (the sign-in form, with a
// notice when a session ended), no owner (the owner is created on the host, never
// here: no form, no retry loop), signed in (the app).

import {
  createContext,
  type FormEvent,
  type ReactNode,
  useContext,
  useEffect,
  useState,
} from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { onUnauthorized } from "@/lib/api";
import {
  fetchProfile,
  fetchSetupStatus,
  type Profile,
  SignInError,
  signIn,
  signOut,
} from "./auth-api";

interface Session {
  profile: Profile;
  /** Ends the session on the server and returns to the sign-in screen. */
  signOut: () => Promise<void>;
}

const SessionContext = createContext<Session | null>(null);

/** The session when rendered under the gate, otherwise null (a component under test). */
export function useOptionalSession(): Session | null {
  return useContext(SessionContext);
}

export function useSession(): Session {
  const session = useContext(SessionContext);
  if (!session) throw new Error("useSession outside SignInGate");
  return session;
}

type Phase =
  | { kind: "checking" }
  | { kind: "signed-out"; notice: string }
  | { kind: "no-owner" }
  | { kind: "signed-in"; profile: Profile }
  | { kind: "unavailable"; message: string };

export function SignInGate({ children }: { children: ReactNode }) {
  const [phase, setPhase] = useState<Phase>({ kind: "checking" });

  useEffect(() => {
    const controller = new AbortController();
    (async () => {
      try {
        const profile = await fetchProfile(controller.signal);
        if (profile) return setPhase({ kind: "signed-in", profile });
        const status = await fetchSetupStatus(controller.signal);
        // Whatever setup_over_http says, Tangent offers no create-owner form.
        setPhase(status.setup_required ? { kind: "no-owner" } : { kind: "signed-out", notice: "" });
      } catch (error) {
        if (controller.signal.aborted) return;
        setPhase({
          kind: "unavailable",
          message: error instanceof Error ? error.message : "Tangent could not be reached.",
        });
      }
    })();
    return () => controller.abort();
  }, []);

  useEffect(
    () =>
      onUnauthorized(() =>
        setPhase((current) =>
          current.kind === "signed-in"
            ? { kind: "signed-out", notice: "Your session ended. Sign in again." }
            : current,
        ),
      ),
    [],
  );

  if (phase.kind === "checking") {
    return (
      <Screen>
        <p aria-busy="true" className="text-sm text-fg-muted">
          Loading…
        </p>
      </Screen>
    );
  }
  if (phase.kind === "unavailable") {
    return (
      <Screen>
        <h1 className="text-lg font-semibold">Tangent is not reachable</h1>
        <p role="alert" className="text-sm text-fg-muted">
          {phase.message}
        </p>
        <Button onClick={() => window.location.reload()}>Try again</Button>
      </Screen>
    );
  }
  if (phase.kind === "no-owner") return <NoOwner />;
  if (phase.kind === "signed-out") {
    return (
      <SignInForm
        notice={phase.notice}
        onSignedIn={(profile) => setPhase({ kind: "signed-in", profile })}
        onNoOwner={() => setPhase({ kind: "no-owner" })}
      />
    );
  }
  const { profile } = phase;
  const session: Session = {
    profile,
    signOut: async () => {
      await signOut();
      setPhase({ kind: "signed-out", notice: "" });
    },
  };
  return <SessionContext.Provider value={session}>{children}</SessionContext.Provider>;
}

function Screen({ children }: { children: ReactNode }) {
  return (
    <main className="flex h-screen items-center justify-center bg-bg px-4 text-fg">
      <div className="flex w-full max-w-sm flex-col gap-4">{children}</div>
    </main>
  );
}

/** No owner exists yet. Creating one is a host action, so there is nothing to submit. */
function NoOwner() {
  return (
    <Screen>
      <h1 className="text-lg font-semibold">Tangent has no owner yet</h1>
      <p className="text-sm text-fg-muted">
        The owner is created on the machine that runs Tangent, not in the browser. Run
      </p>
      <pre className="overflow-x-auto rounded border border-border bg-surface px-3 py-2 text-sm">
        tangent identity setup --username NAME
      </pre>
      <p className="text-sm text-fg-muted">there, then reload this page and sign in.</p>
    </Screen>
  );
}

function SignInForm({
  notice,
  onSignedIn,
  onNoOwner,
}: {
  notice: string;
  onSignedIn: (profile: Profile) => void;
  onNoOwner: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    setBusy(true);
    setError("");
    try {
      const profile = await signIn(
        String(data.get("username") ?? ""),
        String(data.get("password") ?? ""),
      );
      form.reset();
      onSignedIn(profile);
    } catch (caught) {
      if (caught instanceof SignInError && caught.code === "setup_offline") {
        onNoOwner();
        return;
      }
      setError(caught instanceof Error ? caught.message : "Could not sign in.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Screen>
      <h1 className="text-lg font-semibold">Sign in to Tangent</h1>
      {notice ? (
        <p role="status" className="text-sm text-fg-muted">
          {notice}
        </p>
      ) : null}
      <form onSubmit={submit} aria-busy={busy}>
        <fieldset disabled={busy} className="flex flex-col gap-3">
          <div className="flex flex-col gap-1 text-sm">
            <label htmlFor="signin-username">Username</label>
            <Input
              id="signin-username"
              name="username"
              autoComplete="username"
              required
              maxLength={64}
              autoFocus
            />
          </div>
          <div className="flex flex-col gap-1 text-sm">
            <label htmlFor="signin-password">Password</label>
            <Input
              id="signin-password"
              name="password"
              type="password"
              autoComplete="current-password"
              required
              maxLength={72}
            />
          </div>
          {error ? (
            <p role="alert" className="text-sm text-danger">
              {error}
            </p>
          ) : null}
          <Button type="submit">{busy ? "Please wait…" : "Sign in"}</Button>
        </fieldset>
      </form>
    </Screen>
  );
}
