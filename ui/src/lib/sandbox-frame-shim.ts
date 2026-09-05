// The script Tangent injects into every sandboxed renderer frame.
//
// It lives alone in this file, as one uninterpolated template literal, because
// its **bytes are load-bearing**. The Tangent document's own
// "Content-Security-Policy" carries a "'sha256-…'" source for exactly this text
// ("internal/server/security.go"), and an "<iframe srcdoc>" inherits its
// parent document's policy in addition to enforcing its own. So the shim runs
// only if the byte sequence here matches the byte sequence Go hashed —
// "TestSandboxFrameShimMatchesTheSPA" reads this file and fails the build the
// moment they diverge.
//
// That coupling buys the thing the previous "script-src 'unsafe-inline'"
// sandbox could not have: **agent-authored scripts inside a design preview no
// longer execute at all**, in either policy, because they have no hash. Only
// Tangent's own shim does. A hash-based policy is what turns "untrusted markup
// runs with no ambient authority" into "untrusted markup does not run".
//
// Two consequences of the byte-exactness, both deliberate:
//
//   - Nothing may be interpolated into this string. Everything the shim needs
//     arrives through a "<script type="application/json">" data block, which
//     CSP does not govern because it is never executed.
//   - Reformatting this literal is a functional change. Biome does not
//     reformat template-literal contents, and the drift test catches it if
//     anything else does.

/**
 * The sandboxed-frame bootstrap, verbatim.
 *
 * What it does, in order:
 *
 *  1. Reads its payload from the non-executable JSON data block.
 *  2. Binds each declared click region to a node, giving it the keyboard and
 *     screen-reader affordances a mouse-only "click" listener never had:
 *     "tabindex", "role="button"", an "aria-label", a "keydown" handler for
 *     Enter and Space, and a visible focus ring. A sandboxed renderer whose
 *     only affordance is a mouse click does not preserve the interaction's
 *     declared semantics for a keyboard or screen-reader operator, so this is
 *     not a nicety — it is the interaction.
 *  3. Posts activations to the parent at the parent's *real* origin, never
 *     ""*"", carrying the per-mount nonce the parent generated.
 *
 * It never reads or writes anything outside its own document. It cannot: the
 * frame is opaque-origin and carries no "allow-same-origin".
 */
export const SANDBOX_FRAME_SHIM = `(() => {
  const block = document.getElementById("tangent-sandbox-payload");
  if (!block) return;
  let payload;
  try { payload = JSON.parse(block.textContent || "{}"); } catch (err) { return; }
  if (!payload || typeof payload.parentOrigin !== "string" || payload.parentOrigin === "") return;
  const regions = Array.isArray(payload.regions) ? payload.regions : [];
  const describe = (node, region) => {
    const text = node && typeof node.textContent === "string" ? node.textContent.trim() : "";
    return text || region.label || region.selector || region.id;
  };
  const post = (region, node) => {
    window.parent.postMessage({
      type: payload.messageType,
      nonce: payload.nonce,
      variant_id: payload.variantId,
      action_id: region.id,
      action_kind: "click-region",
      value: describe(node, region)
    }, payload.parentOrigin);
  };
  const bind = () => {
    const style = document.createElement("style");
    style.textContent = "[data-tangent-region]{cursor:pointer}[data-tangent-region]:focus{outline:3px solid #38bdf8;outline-offset:2px}";
    (document.head || document.documentElement).appendChild(style);
    for (const region of regions) {
      if (!region || typeof region.id !== "string" || typeof region.selector !== "string") continue;
      if (region.selector === "") continue;
      let nodes;
      try { nodes = document.querySelectorAll(region.selector); } catch (err) { continue; }
      for (const node of nodes) {
        if (node.hasAttribute("data-tangent-region")) continue;
        node.setAttribute("data-tangent-region", region.id);
        if (!node.hasAttribute("tabindex")) node.setAttribute("tabindex", "0");
        if (!node.hasAttribute("role")) node.setAttribute("role", "button");
        if (!node.hasAttribute("aria-label")) node.setAttribute("aria-label", region.label || describe(node, region));
        node.addEventListener("click", (event) => {
          event.preventDefault();
          post(region, node);
        });
        node.addEventListener("keydown", (event) => {
          if (event.key !== "Enter" && event.key !== " " && event.key !== "Spacebar") return;
          event.preventDefault();
          post(region, node);
        });
      }
    }
  };
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", bind, { once: true });
  } else {
    bind();
  }
})();`;
