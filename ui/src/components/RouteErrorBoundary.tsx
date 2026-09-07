import { Component, type ErrorInfo, Fragment, type ReactNode } from "react";
import { Link } from "react-router-dom";

// CW-20260907-0085: containment, not defensive coding. A server sending the
// wrong shape (as #39 did) is a server defect and gets fixed there — this
// exists for whatever gets through anyway, so one route's render error costs
// that route, not the shell, the nav, or every other route. Render this once,
// around <Outlet/> (see App.tsx), passing the current location as
// locationKey.
//
// Deliberately not keyed by location: an earlier version of this component
// was, so that navigating anywhere reset a crash — but that conflates "should
// the boundary's error clear" with "should the children remount," and those
// want different answers. HITLInbox, ChannelPane, and Room are each one
// long-lived instance reacting to their own dynamic param (itemID /
// channelID / roomID) via useParams; a key forces a remount on every in-page
// selection even when nothing crashed, destroying state (confirmed the hard
// way: it broke HITLInbox's own focus-management continuity between items).
// componentDidUpdate below only clears the error when locationKey actually
// changes, and only reacts at all when an error is present — a healthy
// in-page selection never touches this boundary's state, and React's own
// type-and-position reconciliation decides whether the next children are a
// fresh instance or a continuing one, which is exactly right either way:
// the crash's own unmount (render() shows RouteCrashed instead of children
// while state.error is set) already means whatever children resolves to
// next — the same route or a different one, same top-level section or
// not — gets a genuine fresh render attempt the moment the error clears.

interface Props {
  children: ReactNode;
  locationKey: string;
}

interface State {
  error: Error | null;
  resetKey: number;
}

export class RouteErrorBoundary extends Component<Props, State> {
  state: State = { error: null, resetKey: 0 };

  static getDerivedStateFromError(error: Error): Pick<State, "error"> {
    return { error };
  }

  // Never swallowed: the error and its component stack still reach the
  // console exactly as an uncaught error would. That console line is what
  // made the CW-20260907-0085 incident diagnosable in minutes rather than an
  // hour — a boundary that hid it would have cost exactly that.
  componentDidCatch(error: Error, info: ErrorInfo): void {
    console.error("Tangent: a route failed to render", error, info.componentStack);
  }

  componentDidUpdate(prevProps: Props): void {
    if (this.state.error && prevProps.locationKey !== this.props.locationKey) {
      // The operator navigated away from a crashed view — to a genuinely
      // different resource, same top-level section or not (a sibling
      // channel, a different room). Clear the error so it gets a real
      // render attempt instead of showing yesterday's failure for a
      // healthy destination.
      this.setState({ error: null });
    }
  }

  private handleRetry = (): void => {
    // Clearing the error alone would re-render the same component instance
    // with whatever internal state it already held — often the same state
    // that crashed a moment ago, for the same route the operator has not
    // left. Advancing resetKey changes the child Fragment's key, so React
    // tears that subtree down and mounts it fresh, the same clean start a
    // full reload would give, scoped to this route only.
    this.setState((state) => ({ error: null, resetKey: state.resetKey + 1 }));
  };

  render(): ReactNode {
    if (this.state.error) {
      return <RouteCrashed error={this.state.error} onRetry={this.handleRetry} />;
    }
    return <Fragment key={this.state.resetKey}>{this.props.children}</Fragment>;
  }
}

function RouteCrashed({ error, onRetry }: { error: Error; onRetry: () => void }) {
  return (
    <div role="alert" className="mx-auto max-w-lg px-6 py-16 text-center">
      <p className="font-mono text-[10px] uppercase tracking-[0.18em] text-red-400">
        This view failed
      </p>
      <h1 className="mt-3 text-xl font-semibold text-zinc-100">
        Something went wrong loading this page.
      </h1>
      <p className="mt-2 text-sm leading-6 text-zinc-400">
        The rest of Tangent is unaffected — the navigation above still works, and other pages are
        not broken by this one.
      </p>
      <p className="mt-3 break-words font-mono text-xs text-zinc-500">{error.message}</p>
      <div className="mt-6 flex justify-center gap-3">
        <button
          type="button"
          onClick={onRetry}
          className="border border-zinc-600 px-4 py-2 text-sm text-zinc-100 outline-none hover:border-zinc-400 focus-visible:ring-2 focus-visible:ring-amber-400"
        >
          Try again
        </button>
        <Link
          to="/"
          className="border border-zinc-700 px-4 py-2 text-sm text-zinc-300 outline-none hover:border-zinc-500 hover:text-white focus-visible:ring-2 focus-visible:ring-amber-400"
        >
          Go home
        </Link>
      </div>
    </div>
  );
}
