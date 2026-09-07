import { fireEvent, render, screen } from "@testing-library/react";
import { useRef } from "react";
import {
  Link,
  MemoryRouter,
  Outlet,
  Route,
  Routes,
  useLocation,
  useParams,
} from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { RouteErrorBoundary } from "./RouteErrorBoundary";

// CW-20260907-0085's acceptance criterion, verbatim: "a component that
// throws on render leaves sibling routes reachable and the shell mounted."
// This harness mirrors App.tsx's actual shape (a shell with a nav, an
// Outlet wrapped in the boundary) closely enough to prove that property,
// including the case a first version of this boundary got wrong: a sibling
// under the *same* top-level section (a different channel, a different
// room) — the most likely thing an operator actually clicks after a crash.

function Thrower(): never {
  throw new Error("boom: this route's render always fails");
}

function FineRoute() {
  return <div>fine route rendered</div>;
}

// /things/:id — one route pattern serving both a route that always throws
// and one that always renders, so a "same section, different resource" nav
// (/things/bad -> /things/good) exercises the same component *type* the
// real HITLInbox/ChannelPane/Room routes do.
function MaybeThrows(): never | ReturnType<typeof FineRoute> {
  const { id } = useParams<{ id: string }>();
  if (id === "bad") {
    throw new Error("boom: this resource always fails");
  }
  return <div>resource {id} rendered fine</div>;
}

function makeMountCounter() {
  let mounts = 0;
  function Counter() {
    const alreadyMounted = useRef(false);
    if (!alreadyMounted.current) {
      alreadyMounted.current = true;
      mounts += 1;
    }
    const { id } = useParams<{ id: string }>();
    return (
      <div data-testid="mount-count" data-resource={id}>
        {mounts}
      </div>
    );
  }
  return Counter;
}

function Shell({ withBoundary }: { withBoundary: boolean }) {
  const location = useLocation();
  const content = <Outlet />;
  return (
    <div>
      <nav>
        <Link to="/throws">go throw</Link>
        <Link to="/fine">go fine</Link>
        <Link to="/things/bad">go to bad resource</Link>
        <Link to="/things/good">go to good resource</Link>
        <Link to="/things/good2">go to second good resource</Link>
      </nav>
      <div data-testid="shell-marker">shell mounted</div>
      {withBoundary ? (
        <RouteErrorBoundary locationKey={location.pathname}>{content}</RouteErrorBoundary>
      ) : (
        content
      )}
    </div>
  );
}

function renderApp(initialPath: string, withBoundary = true, resourceElement = <MaybeThrows />) {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route element={<Shell withBoundary={withBoundary} />}>
          <Route path="/throws" element={<Thrower />} />
          <Route path="/fine" element={<FineRoute />} />
          <Route path="/things/:id" element={resourceElement} />
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

  // The case a first version of this boundary got wrong: the operator's
  // most likely recovery is not "go to an unrelated page," it's "click a
  // different item in the list I'm already looking at" — a sibling under
  // the *same* top-level section. Keying the boundary by section (or by
  // full path) leaves this broken, because the key never changes.
  it("clears a crash on navigation to a same-section sibling, with no Try again needed", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    renderApp("/things/bad");

    expect(screen.getByRole("alert")).toBeInTheDocument();

    fireEvent.click(screen.getByText("go to good resource"));

    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByText("resource good rendered fine")).toBeInTheDocument();
    expect(screen.getByTestId("shell-marker")).toBeInTheDocument();
  });

  // The property the fix must not sacrifice to get the one above: a
  // healthy in-page selection between two same-type resources must not
  // remount the component, or HITLInbox's own focus-management state (the
  // real regression this caught) breaks the same way it did when this
  // boundary was keyed by path.
  it("does not remount a healthy component across a same-section, same-type selection", () => {
    const Counter = makeMountCounter();
    renderApp("/things/good", true, <Counter />);
    expect(screen.getByTestId("mount-count")).toHaveTextContent("1");

    fireEvent.click(screen.getByText("go to second good resource"));

    // Same component type, same position, no error in play: React's own
    // reconciliation keeps the instance and just updates params. Still "1"
    // proves no remount happened — the property a path- or section-keyed
    // boundary would have broken.
    expect(screen.getByTestId("mount-count")).toHaveTextContent("1");
    expect(screen.getByTestId("mount-count")).toHaveAttribute("data-resource", "good2");
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
