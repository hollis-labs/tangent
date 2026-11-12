package server

import (
	"net/http"
	"strings"
)

// The document security policy: the browser-side half of the renderer trust
// classes (CW-20260825-0073).
//
// Until this file Tangent served no `Content-Security-Policy` at all. That
// absence was the single reason `CW-20260904-0077` had to invent
// `effect.MediationDeclared`: with no policy on the document, a renderer could
// `fetch()` any origin on the internet, and routing `network.fetch` through the
// broker produced an audit trail rather than a barrier. Its report said so, and
// said this task was where it would stop being true.
//
// What the policy here actually buys, stated precisely so nothing is claimed
// that is not delivered:
//
//   - **`network.fetch` becomes genuinely enforced.** `connect-src` is the one
//     directive that governs `fetch`, `XMLHttpRequest`, `WebSocket`,
//     `EventSource`, and `navigator.sendBeacon` alike. Restricted to this
//     origin plus its own WebSocket schemes, no renderer in Tangent's tree can
//     reach an external origin, whatever its trust class.
//   - **Untrusted scripts inside a sandboxed preview stop executing.** A
//     `<iframe srcdoc>` inherits its parent document's policy in addition to
//     enforcing its own, so `script-src` here also governs the design-iteration
//     frame. The hash source below is the SPA's own shim and nothing else; an
//     agent-authored `<script>` in a preview matches no source in either
//     policy.
//   - **`base-uri`, `object-src`, `form-action`, and `frame-ancestors`** close
//     the four navigation and embedding holes that need no capability model to
//     be worth closing.
//
// What it does not buy is equally load-bearing: **`clipboard.write` and
// `export.download` are not CSP-governed at all.** There is no clipboard
// directive, and the only download control in the platform is the `sandbox`
// token `allow-downloads`, which is a property of a frame and cannot be applied
// to a top-level document without sandboxing the whole application. Both remain
// `MediationDeclared` for a main-origin renderer and become `MediationHost`
// only inside a sandboxed frame. See `effect.MediationFor`.
//
// # Why there is no `default-src`
//
// Every directive that matters is named explicitly and `default-src` is
// deliberately absent, which is the opposite of the usual advice. The reason is
// `frame-src`: browsers do not agree about whether an `about:srcdoc` frame is
// subject to it, and a `default-src 'self'` would supply a `frame-src` by
// fallback. Getting that wrong breaks `tangent.design-iteration` outright, in a
// way no test in this repository can catch — there is no browser in CI. Naming
// the directives, and leaving frame embedding to the `sandbox` attribute and
// the frame's own policy, trades a small amount of defense-in-depth for a
// guarantee that the shipped workflow still runs. The trade is recorded here
// rather than discovered later.

// sandboxFrameShimSHA256 is the CSP source for the SPA's sandbox bootstrap.
//
// It is the base64 SHA-256 of `SANDBOX_FRAME_SHIM` in
// `ui/src/lib/sandbox-frame-shim.ts`, and it is a constant in two languages
// because the browser needs it in a header that Go writes and in a document
// that TypeScript builds. `TestSandboxFrameShimMatchesTheSPA` recomputes it
// from the SPA source on every run and fails with the correct value, so the two
// copies cannot drift without the build saying so.
const sandboxFrameShimSHA256 = "sha256-I1swIBtbR1QnY1/IgmK00boupM7TEi+/BltRerODeSY="

// permissionsPolicy denies every powerful browser feature Tangent does not use,
// and scopes the one it does to its own origin.
//
// `clipboard-write=(self)` is the load-bearing entry and the reason this header
// exists at all. `self` is Tangent's document origin, so the allowlist admits
// the host's own React tree and denies every embedded frame — including the
// opaque-origin sandbox a `sandboxed-code` renderer runs in. That is what makes
// `clipboard.write` genuinely unavailable to untrusted code while remaining
// available to the reviewed components that use it today.
//
// Setting it to `()` would enforce the capability for every renderer, including
// the main-origin ones. It is not done here because three shipped core-trusted
// components copy to the clipboard without declaring the capability, and
// breaking them is a capability backfill rather than a trust boundary. That
// gap is named in docs/renderer-trust-classes.md rather than papered over.
func permissionsPolicy() string {
	return strings.Join([]string{
		"accelerometer=()",
		"ambient-light-sensor=()",
		"autoplay=()",
		"camera=()",
		"clipboard-read=()",
		"clipboard-write=(self)",
		"display-capture=()",
		"encrypted-media=()",
		"geolocation=()",
		"gyroscope=()",
		"idle-detection=()",
		"local-fonts=()",
		"magnetometer=()",
		"microphone=()",
		"midi=()",
		"payment=()",
		"picture-in-picture=()",
		"publickey-credentials-get=()",
		"screen-wake-lock=()",
		"serial=()",
		"usb=()",
		"xr-spatial-tracking=()",
	}, ", ")
}

// contentSecurityPolicy builds the document policy for one request.
//
// It is per-request because `connect-src` names this server's own WebSocket
// origins, and those are derived from the Host header rather than guessed:
// `'self'` is specified to cover `ws:`/`wss:` for an `http:`/`https:` document,
// but browser support for that clause has been inconsistent enough that a
// working WebSocket is not worth betting on it.
//
// There is no dev-proxy variant any more: the SPA's dev server serves its own pages,
// and this policy is for the pages the backend itself serves (the embedded build).
func contentSecurityPolicy(r *http.Request) string {
	connect := []string{"'self'"}
	if host := strings.TrimSpace(r.Host); host != "" {
		connect = append(connect, "ws://"+host, "wss://"+host)
	}

	script := "script-src 'self' '" + sandboxFrameShimSHA256 + "'"

	return strings.Join([]string{
		script,
		// Radix and React itself set inline `style` attributes, so
		// `'unsafe-inline'` here is not optional and not a placeholder. Style
		// injection is a real risk class and a much smaller one than script
		// injection; the sandboxed frame's own policy is where untrusted
		// styling is contained.
		"style-src 'self' 'unsafe-inline'",
		// No external origin is admitted: every subresource is same-origin, data:
		// or blob:. A third-party editor that needs a CDN must self-host its
		// assets or declare and be granted `network.fetch` (see
		// docs/renderer-trust-classes.md).
		"img-src 'self' data: blob:",
		"font-src 'self' data:",
		"media-src 'self' data: blob:",
		"connect-src " + strings.Join(connect, " "),
		// Nothing in Tangent embeds a plugin, and `object-src` is the one
		// directive `default-src` would not have covered adequately anyway.
		"object-src 'none'",
		// A `<base>` tag rewrites every relative URL on the page, which turns
		// one injected element into a redirect of every asset and every API
		// call. Tangent authors no `<base>`.
		"base-uri 'none'",
		// The SPA submits through the WebSocket and `/api/*`, never through a
		// form navigation. `'self'` rather than `'none'` because a browser's
		// own autofill and password-manager flows can synthesize a same-origin
		// submission, and refusing those produces a confusing failure with no
		// security gain.
		"form-action 'self'",
		// Tangent is never framed. This is the modern spelling; the
		// `X-Frame-Options` beside it covers the browsers that still read it.
		"frame-ancestors 'none'",
	}, "; ")
}

// SecurityHeaders applies the response headers that belong on every response this process
// emits: the document policy above, the permissions policy, and the cross-origin and framing
// headers beside them. It wraps the whole handler rather than each route: a header applied per
// route is a header the next route forgets.
//
// `Referrer-Policy: no-referrer` because design-iteration renders untrusted agent-authored HTML
// and evidence payloads may carry links. `X-Content-Type-Options: nosniff` because the same
// material must never be re-typed by a content sniffer. `X-Frame-Options` duplicates the
// policy's `frame-ancestors` for browsers that read only the older header (artifact responses
// set their own, later, and win). `Cross-Origin-Opener-Policy` severs `window.opener`;
// `Cross-Origin-Resource-Policy` stops another site loading these responses as subresources.
func SecurityHeaders(next http.Handler) http.Handler {
	permissions := permissionsPolicy()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Content-Security-Policy", contentSecurityPolicy(r))
		header.Set("Permissions-Policy", permissions)
		header.Set("X-Frame-Options", "DENY")
		header.Set("Cross-Origin-Opener-Policy", "same-origin")
		header.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
