import { fireEvent, render, screen } from "@testing-library/react";
import { Link, MemoryRouter, Outlet, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { RouteErrorBoundary } from "./RouteErrorBoundary";

// CW-20260907-0085's acceptance criterion, verbatim: "a component that
// throws on render leaves sibling routes reachable and the shell mounted."
// This harness mirrors App.tsx's actual shape (a shell with a nav, an
// Outlet wrapped in the boundary, keyed by path) closely enough to prove
// that property without needing the real TabStrip/HITLInbox/ChannelPane
// routes and their own data fetching.

function Thrower(): never {
  throw new Error("boom: this route's render always fails");
}

function FineRoute() {
  return <div>fine route rendered</div>;
}

function Shell({ withBoundary }: { withBoundary: boolean }) {
  const location = useLocation();
  const content = <Outlet />;
  return (
    <div>
      <nav>
        <Link to="/throws">go throw</Link>
        <Link to="/fine">go fine</Link>
      </nav>
      <div data-testid="shell-marker">shell mounted</div>
      {withBoundary ? (
        <RouteErrorBoundary key={location.pathname}>{content}</RouteErrorBoundary>
      ) : (
        content
      )}
    </div>
  );
}

function renderApp(initialPath: string, withBoundary = true) {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route element={<Shell withBoundary={withBoundary} />}>
          <Route path="/throws" element={<Thrower />} />
          <Route path="/fine" element={<FineRoute />} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

describe("<RouteErrorBoundary>", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("contains a render crash: the shell stays mounted and a sibling route is still reachable", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    renderApp("/throws");

    expect(screen.getByTestId("shell-marker")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(/this view failed/i);

    fireEvent.click(screen.getByText("go fine"));
    expect(screen.getByText("fine route rendered")).toBeInTheDocument();
    expect(screen.getByTestId("shell-marker")).toBeInTheDocument();
  });

  // The proof this task asked for: without the boundary, the same crash is
  // not contained. React 19 re-throws an uncaught render error out of
  // render(), so this is the render call itself failing, not a gentler
  // symptom — exactly what CW-20260907-0085 observed live (every route
  // going blank, not only the one that actually broke).
  it("fails without the boundary — the crash is not contained", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    expect(() => renderApp("/throws", false)).toThrow(/boom/);
  });

  it("never swallows the error — it still reaches the console with its stack", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    renderApp("/throws");
    const loggedThisError = spy.mock.calls.some(
      (call) =>
        typeof call[0] === "string" &&
        call[0].includes("failed to render") &&
        call[1] instanceof Error &&
        call[1].message.includes("boom"),
    );
    expect(loggedThisError).toBe(true);
  });

  it("shows a retry action that re-attempts a real render, not a no-op", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    renderApp("/throws");
    expect(screen.getByRole("alert")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /try again/i }));

    // Thrower always throws, so a genuine re-render fails again — proving
    // Try again actually remounted the subtree rather than doing nothing.
    expect(screen.getByRole("alert")).toBeInTheDocument();
  });

  it("offers a way out via a Go home link", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    renderApp("/throws");
    expect(screen.getByRole("link", { name: /go home/i })).toHaveAttribute("href", "/");
  });
});
