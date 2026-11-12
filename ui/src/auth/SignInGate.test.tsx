import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SignInGate, useSession } from "./SignInGate";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const profile = {
  id: "u1",
  username: "owner",
  display_name: "The Owner",
  email: "",
  scopes: ["*"],
};

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status });
}

type Handler = (init: RequestInit | undefined) => Response;

/** Routes fetch by "METHOD path"; an unlisted call fails the test. */
function stubServer(routes: Record<string, Handler>) {
  const calls: string[] = [];
  const fetchMock = vi.fn(async (input: string, init?: RequestInit) => {
    const key = `${(init?.method ?? "GET").toUpperCase()} ${input}`;
    calls.push(key);
    const handler = routes[key];
    if (!handler) throw new Error(`unexpected request: ${key}`);
    return handler(init);
  });
  vi.stubGlobal("fetch", fetchMock);
  return { calls, fetchMock };
}

function Probe() {
  const { profile: who, signOut } = useSession();
  return (
    <button type="button" onClick={() => void signOut()}>
      app for {who.username}
    </button>
  );
}

// Not a real credential; built so no literal looks like one.
const typedValue = "x".repeat(14);

const noSession = () => json(401, { code: "unauthorized", message: "sign in" });

it("renders the app only once a session exists", async () => {
  stubServer({ "GET /api/identity/profile": () => json(200, profile) });
  render(
    <SignInGate>
      <Probe />
    </SignInGate>,
  );
  expect(await screen.findByText("app for owner")).toBeInTheDocument();
});

it("shows a sign-in form when there is an owner and no session, and the app after sign-in", async () => {
  const { fetchMock } = stubServer({
    "GET /api/identity/profile": noSession,
    "GET /api/setup": () => json(200, { setup_required: false, setup_over_http: false }),
    "POST /api/login": () => json(200, profile),
  });
  render(
    <SignInGate>
      <Probe />
    </SignInGate>,
  );
  fireEvent.change(await screen.findByLabelText("Username"), { target: { value: "owner" } });
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: typedValue } });
  fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
  expect(await screen.findByText("app for owner")).toBeInTheDocument();
  const login = fetchMock.mock.calls.find(([url]) => url === "/api/login")?.[1] as RequestInit;
  expect(JSON.parse(String(login.body))).toEqual({
    username: "owner",
    password: typedValue,
  });
  expect(new Headers(login.headers).get("X-Requested-With")).toBe("tangent");
});

it("keeps the form and shows the server's message on a wrong password", async () => {
  stubServer({
    "GET /api/identity/profile": noSession,
    "GET /api/setup": () => json(200, { setup_required: false, setup_over_http: false }),
    "POST /api/login": () =>
      json(401, { code: "invalid_login", message: "invalid username or password" }),
  });
  render(
    <SignInGate>
      <Probe />
    </SignInGate>,
  );
  fireEvent.change(await screen.findByLabelText("Username"), { target: { value: "owner" } });
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: "wrong" } });
  fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("invalid username or password");
  expect(screen.queryByText("app for owner")).not.toBeInTheDocument();
});

for (const overHTTP of [false, true]) {
  it(`shows the offline-owner screen, never a form, when setup is required (setup_over_http ${overHTTP})`, async () => {
    const { calls } = stubServer({
      "GET /api/identity/profile": noSession,
      "GET /api/setup": () => json(200, { setup_required: true, setup_over_http: overHTTP }),
    });
    render(
      <SignInGate>
        <Probe />
      </SignInGate>,
    );
    expect(await screen.findByText("Tangent has no owner yet")).toBeInTheDocument();
    expect(screen.getByText("tangent identity setup --username NAME")).toBeInTheDocument();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Password")).not.toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(calls.some((c) => c.startsWith("POST"))).toBe(false);
  });
}

it("treats 403 setup_offline from login as the same offline-owner screen", async () => {
  stubServer({
    "GET /api/identity/profile": noSession,
    "GET /api/setup": () => json(200, { setup_required: false, setup_over_http: false }),
    "POST /api/login": () =>
      json(403, { code: "setup_offline", message: "create the owner on the host" }),
  });
  render(
    <SignInGate>
      <Probe />
    </SignInGate>,
  );
  fireEvent.change(await screen.findByLabelText("Username"), { target: { value: "owner" } });
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: "x" } });
  fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
  expect(await screen.findByText("Tangent has no owner yet")).toBeInTheDocument();
});

it("drops to sign-in with a notice when the session ends mid-use", async () => {
  stubServer({
    "GET /api/identity/profile": () => json(200, profile),
    "GET /api/inbox": noSession,
  });
  const { apiFetch } = await import("@/lib/api");
  render(
    <SignInGate>
      <Probe />
    </SignInGate>,
  );
  await screen.findByText("app for owner");
  await apiFetch("/api/inbox");
  expect(await screen.findByRole("status")).toHaveTextContent("Your session ended. Sign in again.");
  expect(screen.getByLabelText("Username")).toBeInTheDocument();
});

it("signs out on the server and returns to sign-in", async () => {
  const { calls } = stubServer({
    "GET /api/identity/profile": () => json(200, profile),
    "POST /api/logout": () => new Response(null, { status: 204 }),
    "GET /api/setup": () => json(200, { setup_required: false, setup_over_http: false }),
  });
  render(
    <SignInGate>
      <Probe />
    </SignInGate>,
  );
  fireEvent.click(await screen.findByText("app for owner"));
  await waitFor(() => expect(screen.getByLabelText("Username")).toBeInTheDocument());
  expect(calls).toContain("POST /api/logout");
  expect(screen.queryByRole("status")).not.toBeInTheDocument();
});
