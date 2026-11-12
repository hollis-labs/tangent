// "A plugin route path an envelope may name."
//
// An envelope is data from an agent. Two renderers (ExternalReview, AppBoard)
// POST to a path the envelope names, from the owner's signed-in page, so the
// path must be provably a plugin route and nothing else. A prefix test is not
// enough: `/api/plugins/x/a/../../../inbox/items/<id>/archive` starts with the
// prefix and the browser resolves it to `/api/inbox/items/<id>/archive`.
//
// The rule is deliberately narrower than "what the router would accept": plain
// segments only, and the URL the browser would request must be the string given.

const PREFIX = "/api/plugins/";

export function isPluginRoutePath(path: unknown): path is string {
  if (typeof path !== "string" || !path.startsWith(PREFIX)) return false;
  if (path.includes("//") || path.includes("%") || path.includes("\\")) return false;
  if (/[\s?#]/.test(path)) return false;
  // Control characters and anything outside printable ASCII never name a route.
  if (!/^[\x21-\x7e]+$/.test(path)) return false;
  const segments = path.slice(PREFIX.length).split("/");
  if (segments.some((segment) => segment === "" || segment === "." || segment === "..")) {
    return false;
  }
  // What the browser would actually request must be what was named.
  try {
    const resolved = new URL(path, window.location.origin);
    return resolved.origin === window.location.origin && resolved.pathname === path;
  } catch {
    return false;
  }
}
