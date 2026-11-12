/**
 * The artifact body is text an agent wrote. Only an https figma.com link, with
 * no credentials in it, may become an href or an embed source; anything else
 * (javascript:, data:, another host) is not shown as a link.
 */
export function figmaURL(raw: string): string | null {
  try {
    const url = new URL(raw.trim());
    const host = url.hostname.toLowerCase();
    if (url.protocol !== "https:" || url.username || url.password) return null;
    if (host !== "figma.com" && !host.endsWith(".figma.com")) return null;
    return url.href;
  } catch {
    return null;
  }
}
