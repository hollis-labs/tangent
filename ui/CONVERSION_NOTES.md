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
4. **Sign-in** (`src/auth`). `SignInGate` wraps the app: profile probe, sign-in form, an offline-owner screen when
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
frontend precondition for the cutover. Two things seen there and not caused by the port, tracked as follow-ups: the
host answers the SPA's `view.active` frame with `ui_command_rejected` and the header shows "host refused channel"; and
a room keeps saying "Sending your response…" after a successful structured submit until it is reloaded.

## Waits on kit work (not started)

- **App shell and BOM-pinned kits.** The SPA still uses its own Tailwind components and navigation (`TabStrip`), not
  `app-shell` and the `design-components`/`design-tokens` the BOM pins. Waiting on responsive navigation in the shell.
- **kit-identity.** The sign-in and offline-owner screens here are interim (the Basalisk pattern, Tangent's own
  components). kit-identity is merged in `frontend-support` (#30) but not published, and its offline-owner behaviour is
  CW-20261112-0118. Replace `src/auth` by it when it is in the BOM.
- **User menu.** Who-is-signed-in and Sign out sit in the nav bar until the shell's user menu (CW-20261112-0107).

## Known, not changed here

- Static assets have no ETag revalidation (framework request filed).
- A non-owner's refused decision shows the server's text ("required scope: tangent.resolve"), not friendlier copy.
- `POST /api/rooms/<unknown>/close` answers 500 (Torque CW-20261112-0124, backend); the SPA does not call it.
- The built Excalidraw chunk trips a gcp-api-key rule (a false positive in a vendored string); #154 runs the CI scan
  before the build.
