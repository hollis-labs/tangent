import { Settings } from "lucide-react";
import { NavLink, useLocation } from "react-router-dom";
import { ThemeToggle } from "@/components/ThemeToggle";
import { cn } from "@/lib/utils";

// Navigation describes destinations, not every presentation container an
// agent creates. Interaction discovery and history belong to the Inbox.
export function TabStrip() {
  const { pathname } = useLocation();
  const inboxActive =
    pathname === "/" || pathname.startsWith("/inbox") || pathname.startsWith("/r/");
  const linkClass = ({ isActive }: { isActive: boolean }) =>
    cn(
      "rounded px-3 py-2 text-sm focus-visible:outline-2 focus-visible:outline-primary",
      isActive ? "bg-surface text-fg" : "text-fg-muted hover:text-fg",
    );
  return (
    <nav
      aria-label="Main navigation"
      className="flex shrink-0 items-center gap-2 border-b border-border bg-bg px-4 py-2"
    >
      <NavLink to="/" className="mr-3 text-sm font-semibold">
        Tangent
      </NavLink>
      <NavLink
        to="/"
        end
        aria-current={inboxActive ? "page" : undefined}
        className={() => linkClass({ isActive: inboxActive })}
      >
        Inbox
      </NavLink>
      <NavLink to="/channels" className={linkClass}>
        Channels
      </NavLink>
      <div className="ml-auto flex items-center gap-2">
        <ThemeToggle />
        <NavLink to="/settings" aria-label="Settings" className={linkClass}>
          <Settings size={18} />
        </NavLink>
      </div>
    </nav>
  );
}
