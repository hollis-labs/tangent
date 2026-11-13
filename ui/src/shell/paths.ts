// The shell derives its page from a surface route, one per destination. Tangent's
// destinations also have deep links (an inbox item, a room, one agent's chat) that
// the pages resolve themselves with react-router, so the shell is told the
// destination's own path rather than the URL.

/** The surface route a browser path belongs to. */
export function surfacePath(pathname: string): string {
  if (pathname === "/" || pathname === "/inbox" || pathname.startsWith("/inbox/")) return "/";
  if (pathname.startsWith("/r/")) return "/";
  for (const section of ["/chat", "/channels", "/settings"]) {
    if (pathname === section || pathname.startsWith(`${section}/`)) return section;
  }
  return pathname;
}
