# ui/ conversion notes: Tangent's SPA on Substrate

What the move to the Substrate backend changed in `ui/`, what is verified, and what waits. Backend facts are in
`docs/conversion-notes.md` ("For the GUI agent").

## Done (draft pull requests, in order)

1. **Node 24 and the dev proxy.** `engines` is `>=24 <25` (G1 closed) in `ui/package.json`, `ui/tools/design-lint`,
   `check-node`, `mise.toml`, CI and the docs. Vite proxies `/api`, `/mcp` and `/ws` to the Go dev server
   (`TANGENT_DEV_BACKEND`, `TANGENT_DEV_UI_PORT`) and presents the backend's own `Origin`; without that the identity
   module refuses a browser write with `403 bad_origin`.
2. **One request layer** (`src/lib/api.ts`). `apiFetch` adds `X-Requested-With` on `POST`/`PUT`/`PATCH`/`DELETE` to a
   same-origin path only; a 401 that is not `invalid_login` is the signed-out signal (`onUnauthorized`). Every direct
   `fetch` in `src` goes through it. `readApiError` reads the flat `{code,message,details?}` body.
3. **Elevation and the desktop shell removed.** `operator-elevation`, `OperatorTokenPrompt`, and the Wails shell's
   `?clientId`/`?clientKind` handling are gone (the stable id across an app relaunch went with the shell).
4. **Sign-in** (interim, replaced by kit-identity in the app-shell port below). `SignInGate` wrapped the app: profile probe, sign-in form, an offline-owner screen when
   `setup_required` (no form, no request, whatever `setup_over_http` says; `403 setup_offline` is the same screen),
   "Your session ended" on a mid-use 401, who-is-signed-in and Sign out in the nav bar. A room socket refused
   `not_authorized` re-checks the session, so a revoked session returns to sign-in instead of showing a scope message.
   The plugin registry sync runs after sign-in.
5. **The SPA builds where the binary embeds it** (sub-tangent's #154). Vite wrote to `internal/server/ui_dist`, the port embeds
   `internal/webui/dist`: `make build` made a binary with no SPA and the shim-hash test skipped silently.

## Verified in a real browser

Chrome (playwright-core, channel chrome) against a built binary on a scratch data directory and its own port, both
through the Vite proxy and against the embedded production build: wrong password shows the server's message; sign-in;
reload keeps the session; sign-out clears the cookie; a cleared cookie mid-use shows the notice; an empty data
directory shows the offline-owner screen with no input and no button; the owner answers an agent turn (`POST
/api/turns/items/<id>/reply` 200 with `X-Requested-With`) and the agent's `tangent_turns_await` receives it; a user
holding only `tangent.view` signs in and is refused `403 scope_required` ("required scope: tangent.resolve", shown as
the server wrote it); a session revoked while a room socket is open: the socket stays open, the next answer gets
`not_authorized`, the app returns to sign-in and the socket closes. The sandbox-frame shim hash tests pass against a
built bundle (`TestSandboxFrameShimMatchesTheSPA`, `TestTheShippedBundleCarriesTheHashedShimVerbatim`).

Then against the published release artifact `tangent-b48b81894b7c` (run 43291537913) on migrated rehearsal data, by hand in
real Chrome: the owner signs in (`POST /api/login` 200 with `X-Requested-With`), reload keeps the session, sign-out
returns 204 and the sign-in form; a form interaction and a structured interaction are decided over the room socket and
read "resolved" in History afterwards (pending 183 to 181); the dotted external-review room shows "The review plugin
route is unavailable." and sends no plugin or archive request (the throwaway interaction stays staged). This is the
frontend precondition for the cutover. Two things seen there were not caused by the port (the old build did the same,
measured on the old binary) and are fixed since: the page no longer sends `view.active` while the host has no
UI-command channel (#166; `UI_COMMAND_CHANNEL_ENABLED` in `ui/src/lib/ui-command-channel.ts`, one line to restore for
CW-20261112-0128), and a room shows "Response sent." / "Cancelled." on the host's `response.accepted` frame (host #167,
page #168; keyed on the envelope id and revision the tab sent). The clean review room for a plugin that is not
installed now says "The review plugin is unavailable." (#165).

The same checks were repeated by hand on the build of main `c01fbf0` (workflow `build` run 43300354697, artifact
`tangent-c01fbf00b05d`): sign-in, a decision shown as sent without a reload, no refused-channel notice, the
review-plugin-unavailable message, inbox archive and sign-out. Accepted by the driver as the frontend precondition for
that build.

## App-shell and the BOM-pinned kits (this port)

`ui/` is a `ManifestShell` (`@hollis-labs/app-shell` 0.11.0) over a manifest `substrate frontend` generates.

- **Manifest.** `cmd/tangent/app.ui.json` (named by `frontend.contributions` in `cmd/tangent/substrate.yaml`) declares
  Inbox, Chat, Channels and Settings as primary nav, and Account security, Access keys, Active sessions and Sign out as
  footer nav. `npm run manifest` writes `ui/src/generated/frontend.manifest.json`; regenerate it when the file changes.
- **Pages** are the existing routes. The shell is given the destination's path (`src/shell/paths.ts`), and each page sits
  under the route table its component reads params from, so `/inbox/items/:id`, `/r/:roomID` and `/chat/:agentID` work
  as before. Page titles are `h2` under the shell's `h1`; pages are `div`s inside the shell's single `main`.
- **Sign-in** is `@hollis-labs/kit-identity` 0.1.0 (`IdentityGate`): boot, sign-in, the offline-owner screen, password
  change, access keys, sessions, sign out. A 401 on any request the kit did not make (`apiFetch`) asks the kit
  `listSessions()`, whose 401 puts the gate on its "session ended" sign-in (`src/shell/SessionLossBridge.tsx`). A room
  socket refused `not_authorized` still re-checks the session (`src/lib/session-probe.ts`). The interim gate, `auth-api`
  and `TabStrip` are removed.
- **Versions (exact, no ranges), equal to the `kits:` block of the BOM at Substrate `add408c4`:** app-shell 0.11.0,
  design-app-runtime 0.6.0, design-bindings 0.3.0, design-components 0.5.1, design-tokens 0.5.0, kit-account 0.3.0,
  kit-identity 0.1.0, kit-settings 0.2.0, plugin-host-ui 0.3.0, plugin-registry 0.2.0. The design-tokens and
  design-components `overrides` stay. kit-chat and kit-cards are out of the BOM until they release against
  design-bindings 0.3 (CW-20261113-0024); Tangent uses neither. Bump the Substrate pin and re-run `npm run manifest` when the BOM moves.
- **Theme.** The preference is applied at start by `ThemeBoot` (the tab strip used to do it). Tangent's theme file has
  dark values only, so "light" sets the mode and changes nothing visible; that was already so.

### Not done, and why

- **User menu.** `app-shell` 0.11.0 has no `user.menu` region (CW-20261112-0107 is not in it): Sign out and the identity
  pages are footer nav items, and the signed-in name is not shown in the chrome. Move them to the menu when the shell has it.
- **Who-am-I after a reload.** kit-identity 0.1.0 reads the profile only after setup or sign-in, so after a reload the
  Account security page's badge says "Identity unavailable". Tangent serves `GET /api/identity/profile`; the kit should read it.
- **Pairing codes.** Not in the old app; the page is not contributed.
- **The two-header look.** The shell's page frame draws the surface title above the page's own header (by design in
  the shell; not worked around).

## Known, not changed here

- Static assets have no ETag revalidation (framework request filed).
- A non-owner's refused decision shows the server's text ("required scope: tangent.resolve"), not friendlier copy.
- `POST /api/rooms/<unknown>/close` answers 500 (Torque CW-20261112-0124, backend); the SPA does not call it.
- The built Excalidraw chunk trips a gcp-api-key rule (a false positive in a vendored string); #154 runs the CI scan
  before the build.
