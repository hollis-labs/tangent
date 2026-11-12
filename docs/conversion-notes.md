# Conversion notes: Tangent onto Substrate

Work in progress on `feat/substrate-conversion`. This records what changed, what Substrate did not cover (Gaps,
for the owner to route) and what an operator must do. Code is a clean break; data is not: every row of the
pre-Substrate database is migrated.

## Gaps

Each says what was seen, and what was done meanwhile.

1. **G1: Node range.** `ui/package.json` requires `node >=22.12 <23` (and `mise.toml` pins 22.12.0); the migration
   hosts have Node 24. Open: the port moves the UI onto the BOM-pinned kits, which may lift the range. Not blocking
   the backend.
2. **G2: plugin configuration secrets.** The plugin host keeps declared secret values only in the OS keychain
   (`internal/pluginconfig/keychain.go`, service `hollis-labs.tangent.plugin-config`, no file fallback; Secret Service
   over DBus on Linux) and their references in `<plugin root>/.state/config/config.sqlite`. The live instance holds
   two such values. **Closed in slice (d):** plugin secrets now live in the `secrets` module's managed file store
   (`pluginconfig.Managed`, `file://tangent-plugin-config/<account>`; 0600 files in a 0700 directory, no keychain at
   run time). `tangent migrate-secrets --to <data dir> [--dry-run]` moves the two existing values (below).
3. **G5: WebSocket and streams through httphost: no gap found.** `cmd/tangent/g5_test.go` drives a real socket and a
   real stream through the whole chain (host guard, identity middleware, auth guard, `apierr.Normalize`, handler):
   cookie and bearer upgrades work, no credential and a foreign Origin and an unknown Host are refused, a stream is
   held open past two minutes (`go test -run G5StreamOutlivesTimeouts` without `-short`: 142 s). One caveat for the
   handlers: `httphost.Serve` calls `http.Server.Shutdown`, which does not end hijacked or streaming connections, so
   each streaming handler must watch the application context itself (the test does), or shutdown waits out the
   10 s grace period and returns an error.
4. **G6a: `/sse`: dropped.** The module's `/mcp` mount is Streamable HTTP only; Tangent also served the legacy SSE
   transport at `/sse`. The live instance's log (from 2026-10-24) shows `/sse` requested five times in total, all
   on 2026-10-24 between 18:53 and 19:13 UTC, each under two seconds (probes, not sessions), never since, while
   `/mcp` has thousands of requests. Removed.
5. **G6b: JSON responses.** The old server runs the MCP SDK `Stateless` and `JSONResponse`; libs go-mcp's handler
   (used by the `mcp` module) sets `Stateless` only, so a client sending `Accept: text/event-stream` gets an SSE-framed
   reply where it got JSON. Taken to the Framework Agent by the driver. Recorded against the module as it is today
   (`TestG6bWhatTheModuleAnswersToTheMainClientsPost`, real host, real identity): a POST with `Accept: application/json,
   text/event-stream` (what Claude Code, Codex and the SDK clients send) is answered `200 text/event-stream`, one
   event, `event: message` then `data: {json-rpc}`; `Accept: application/json` alone, or no `Accept`, is `400 Accept
   must contain both 'application/json' and 'text/event-stream'`. The SDK's own client reads the framed reply (the
   host tests use it); the in-tree probes (`internal/smoke`) and `docs/mcp-smoketest.md`'s curl recipes now unwrap it.
   **Which client library the Tether gateway uses for Tangent's `/mcp`, and whether it accepts an SSE-framed reply to
   a POST, cannot be told from Tangent's side.** Tangent's old gateway smoke tests (`-tags tether_smoke`) used an
   `sse` catalog entry, so they say nothing about the Streamable HTTP client; they are deleted with `/sse`. The gateway
   owner must confirm before cutover, and `internal/smoke`'s catalog classification still expects `transport: sse` and
   `url: <base>/sse` for Tangent's entry: it must change with the catalog entry (a correctly migrated entry would read
   `CATALOG_STALE` today).
   **Settled for the main client.** The live catalog entry is already `http` at the public name plus `/mcp`, and Tether
   builds http upstreams with libs `go-mcp/client` over go-sdk v1.8.0's `StreamableClientTransport`.
   `TestGoMCPClientOverStreamableHTTP` drives the real host with that client (identity `tether-proxy`, a key): initialize,
   tools/list and a tool call all work against the module as it answers today. The JSON-response option is not needed
   for it.
7b. **G9: the MCP SDK's DNS-rebinding guard refuses the gateway's public Host.** Found by
   `TestProxiedGatewayKeepsItsForwardedSessionHeaders`: behind the local TLS proxy the gateway arrives with the public
   name in `Host`; the Host check accepts it (listed in `allowed_hosts`, loopback peer, `X-Forwarded-Proto: https`), but
   go-sdk's `StreamableHTTPHandler` (libs go-mcp, `DisableLocalhostProtection` unset) answers `403 Forbidden: invalid
   Host header` for any non-loopback Host on a loopback listener. Worked around in Tangent: `mcpLoopbackHost`
   (`cmd/tangent/serve.go`) presents the loopback name to `/mcp` only for a request that came through the Host check's
   proxy validation (which keeps `X-Forwarded-Proto` for no other request). A framework fix would be an option on the
   `mcp` module to disable the SDK guard when `allowed_hosts` is set; for the Framework Agent.
6. **G8: identity had no offline bootstrap and `/api/setup` was open on an empty database. Closed by the pin at `d453ac8`.**
   `tangent identity setup --username NAME` creates the first owner on the host (password from a no-echo prompt or the
   first line of stdin, never an argument or the environment) and `tangent identity key create --name N --scope S...
   (--out PATH | --stdout)` a key. `POST /api/setup` is always refused (403 `setup_offline`). The tests create the owner
   the same way before the server serves (`identity.Service.SetupOwner`, which the command calls; the smoke harness runs
   the shipped binary's own commands) and sign in with an ordinary `POST /api/login`. The commands start only the
   modules they need (`plan.go`): no listener, no tool server.

7. **Plugin children cannot call back: decided (driver, option A) and done.** A subprocess plugin reaches Tangent's tools through
   `/mcp` at `TANGENT_MCP_URL` (`pkg/plugin/hostclient`). `/mcp` refuses a request without a credential, so the host now
   mints one key per child through the identity service (`internal/childkeys`): named `plugin-<id>`, scopes = what the
   plugin host holds (view, draft, submit, cancel, close, withdraw, deliver, plugin) plus `tangent.mcp`, never resolve,
   admin or a wildcard (`access.PluginChildScopes`, `TestPluginChildScopesNeverDecide`). It lives ten minutes and is
   renewed every three while the child lives; the one before the previous is revoked at each renewal (two are live at
   most), and both are revoked, and the file removed, when the incarnation ends (`pluginhost.ChildSpec.Credential`: the
   attempt's key is also ended when the next attempt starts, so a child that failed to start leaves none).
   *Delivery:* the child's environment carries `TANGENT_MCP_KEY_FILE`, the path of a 0600 file in a 0700 directory
   (`<plugin root>/.state/<id>/run/mcp-key`) that the host rewrites on every renewal, not the key itself: a running
   process's environment cannot change, so a key literal could not be renewed. `hostclient` reads the file on every
   request (`TANGENT_MCP_KEY` carries a literal for a host that does not renew). Also fixed: `TANGENT_MCP_URL` is now the
   address the host really listens on (it used to read `TANGENT_HTTP_PORT`, which nothing sets any more).
   *Partition:* a `plugin-` key stays in the unlabeled caller partition, as the in-process plugin host does, so a child
   moving from a loopback caller to a key moves no data (and a gateway producer cannot take that prefix).
   *Revocation, a Gap for Substrate:* `identity.Service` has `CreateKey` but no exported revoke; the host calls the
   module's own published `DELETE /api/identity/keys/` handler in-process (`cmd/tangent/childkeys.go`). An exported
   `RevokeKey(ctx, id)` would replace that. If a revoke fails the key still expires on its own within ten minutes.
   *Verified:* `TestAPluginChildCallsBackWithItsOwnKeyAndLosesItWhenItStops` (real stack: a view tool succeeds, every deciding
   route and `GET /api/identity/keys` refuse the key, a renewal reaches the next call, both keys are 401 once the child
   stops, the file is gone; break-checked by removing the close) and the runner end-to-end test is back
   (`TestInstalledRunnerCarriesATurnThroughTheInbox`, no longer skipped): the shipped binary starts the real runner plugin,
   built from the pinned plugins commit against this tree's `hostclient` (the smoke harness replaces the `tangent` module
   with the working tree for the fixture), and a turn goes through the inbox.
   *What the plugins repository must change (do not push there; the driver was told):* nothing in the plugin sources. In
   `tangent-runner`, `tangent-tesseract` and `tangent-torque` (the three whose `main.go` imports `pkg/plugin/hostclient`)
   bump the `github.com/hollis-labs/tangent` requirement from `v0.20.0` to the first Tangent release that carries this
   `hostclient`, refresh `go.sum`, and cut a release of the three plugins; then move `tangent-plugins.version` and
   `tangent-plugins.smoke-ref` here to it. At the cutover the installed plugin binaries must be reinstalled
   (`make install-plugins`): a binary built with the old `hostclient` presents no key and is refused.
8. **Pins.** `substrate pins check --app .` passes at `bac8c60`; `pins sync` moved `libs/mesh/core` (0.8.0 to 0.6.0),
   `libs/ui/ui-go` (0.4.0 to 0.1.0) and `libs/harness/core` (0.13.0 to 0.16.0) to the BOM versions and the existing
   packages still build and vet.
9. **Hygiene.** `scan-public --tree` (now the framework's: `go run github.com/hollis-labs/substrate/cmd/substrate scan-public --tree`, the copy in scripts/ is gone; CI step and `make scan-public`) reported 86 findings in the inherited tree (test fixtures that look like
   secrets, redaction test data, macOS home paths, two private-network addresses and two internal host names in
   `docs/mcp-integration.md`, a personal home path in `tangent.cerberus.yaml`). Most sit in code the port deletes; the
   rest are fixed or, where provably not a secret, proposed to the driver for the allowlist. None has been
   allowlisted.

## Data: the one-way copy

`tangent migrate-data --from <old tangent.db> --to <new file>` (both required; it prints both absolute paths first) builds the new database under a temporary name and
renames it into place only after every check passes; it refuses an existing target.

- The old file is only ever read (`mode=ro`; `immutable=1` when there is no WAL content so no `-shm` is created). It
  is fingerprinted (size, mtime, sha256 of the file, `-wal`, `-journal`) before and after; any change aborts.
- Run it on an online-backup copy, not on the live file.
- Order: create tables and indexes (migration 00001), copy every table with foreign keys off, create the view and
  the 48 triggers (00002), then verify. The triggers are created after the rows because several write other rows or
  make rows immutable: `interaction_inbox_arrival` would write `inbox_order` a second time.
- Proof: row counts; a sha256 over every row of every table as `quote()`d text plus `typeof()`; the `typeof()`
  histogram of every column; `EXCEPT` in both directions on the attached files; the SQL text of every table, index,
  view and trigger equal to the old file's; `integrity_check`; `foreign_key_check` equal to the old file's.
  `AUTOINCREMENT` counters (`sqlite_sequence`) are copied and compared (a counter above `max(id)` is kept).
- `schema_migrations` (the old migrator's bookkeeping) and its index are not copied; the new file has goose's own
  table. Nothing else is left behind: a table or index in the old file that the new schema lacks, a virtual table,
  or a view or trigger whose definition differs, stops the migration (before install).
- Only one instance is migrated: the one the live service uses. The two old `~/.tangent` directories (one on the
  server, one on a laptop) are neither read nor written; no default of the ported binary resolves there (the data
  directory is `TANGENT_DATA_DIR`, else `$XDG_DATA_HOME/tangent`; `TestDefaultsNeverResolveToTheOldDirectory`).
- The schema is, character for character, the one the old binary ended at (its migrations 0001 to 0024):
  `internal/persistence/testdata/legacy_schema.txt`, compared by `TestSchemaEqualsLegacy`. The old migrator's
  `schema_migrations` bookkeeping table is the only object not carried.
- A database with the old layout is refused at start (`RefuseLegacy`, before the database module migrates).
- Rollback: the old file is never touched, so rollback is starting the previous binary on it.
- Rehearsal on the owner's online-backup copy of the live database (18.3 MB, 46 data tables, 18 222 rows): migrated
  and verified in about one second; an independent check with `sqlite3`/Python (per-table counts and `EXCEPT` both
  ways) found no difference; the copy was byte-identical afterwards and gained no `-shm`.

A defect found and fixed in slice (c): the old binary's migration 0021 *seeded* the one row the event outbox counts on
(`event_outbox_state`, id 1), and the baseline schema carried only the table. `TestSchemaEqualsLegacy` compares schema
text and cannot see a seed row, so a new database could not enqueue an approval (`events: next sequence: sql: no rows`).
`00002_guards.sql` now seeds it with `INSERT OR IGNORE` (a copied database keeps its own generation and sequence, proved
by `TestMigrateLegacy`; a new one gets a random generation, `TestANewDatabaseSeedsTheEventOutboxCounter`). The other
inserts in the old migrations were backfills of rows that are copied as data.

## Files and secrets: the one-way copy (slice d)

Run in this order, on the host, with the old Tangent stopped. The old data directory is only ever read.

1. `tangent migrate-data --from <old tangent.db copy> --to <data dir>/tangent.db`
2. `tangent migrate-files --from <old data dir> --to <data dir>` copies `artifacts/` and `plugins/` (the installed
   plugins, `.state/` and `config.sqlite`). Each tree is built under `<name>.migrating`, compared with its source
   (every path, size, mode and sha256), and every `artifact_revisions` row's sha256 is checked against the copied body;
   only then is it renamed into place. A non-empty target, a symlink or device in the source, or any mismatch stops the
   run with nothing installed. Without a database at the target the artifact check is reported as NOT CHECKED.
3. `tangent migrate-secrets --to <data dir> --dry-run`, then without `--dry-run`. References come from
   `plugin_config_override.secret_ref`; each value is read from the keychain (service
   `hollis-labs.tangent.plugin-config`), written to `<data dir>/secrets/tangent-plugin-config/<account>`, read back and
   compared by length and a keyed digest (a random per-run key). Values are never printed or put on a command line; only
   plugin, setting name, byte length and status are. A store entry with a different value is never overwritten. The
   keychain entries stay as the rollback. The secrets module trims surrounding whitespace, so a value is stored trimmed.
4. Optional: `tangent.tether_token_ref: file://tangent/tether-send-only` in the config block names the reply-mail token in
   the same store (`TANGENT_TETHER_TOKEN` still works when no reference is set; an unresolvable reference stops the
   start).

The plugin directory moved: it is `<data dir>/plugins` unless `TANGENT_PLUGIN_DIR` is set (was `~/.tangent/plugins`).
Plugin binaries are copied as files; reinstall them (`make install-plugins`) if a plugin pin has moved.

Not yet covered: nothing on the data side. The old `~/.tangent` directories are neither read nor written unless named
by `--from`.

## Environment and paths (so far)

| Old | New |
|---|---|
| `TANGENT_DB_PATH` (default `~/.tangent/tangent.db`) | `database.sqlite.path`, anchored in the data directory |
| (none) | `TANGENT_DATA_DIR`, else `$XDG_DATA_HOME/tangent` |
| `TANGENT_HTTP_PORT` / `--port` (7842) | `httphost.addr` (default `127.0.0.1:7842`, must be loopback) |
| `TANGENT_OPERATOR_TOKEN`, `TANGENT_ELEVATION_TTL` | removed with the operator-elevation model (see the auth slice) |
| `TANGENT_MCP_TOKENS`, `TANGENT_MCP_TRUSTED_FORWARDERS`, `TANGENT_PROXIED_HTTP_ADDR` | removed: identity API keys replace them; the producers that count as gateways are `tangent.gateway_producers` and `tangent.trusted_forwarders` in the config |
| `TANGENT_DEV_FRONTEND_URL`, the `/r/{roomID}` prevalidation, CSP headers, SPA serving | removed from the backend; the SPA is the host's `Assets` (slice e) |
| `TANGENT_ARTIFACT_DIR`, `TANGENT_D2_BIN`, `TANGENT_MANAGED_RESOURCE`, `TANGENT_OTEL`, `TANGENT_PLUGIN_HEALTH_GATE_DISABLE`, `TANGENT_MCP_LEGACY_TOOL_NAMES`, `TANGENT_EVENTS_DISABLED`, `TANGENT_HITL_BACKEND`, `TANGENT_PLUGIN_DIR` | unchanged (read by `cmd/tangent/compose.go`; artifacts default to `<data dir>/artifacts`) |
| `/readyz`, `/healthz/capability`, `/healthz/capability/{kind}` | `/api/health/ready`, `/api/health/capability`, `/api/health/capability/{kind}` (need `tangent.view`); liveness is the host's `/healthz` |
| `/sse`, the 7844 listener | removed (G6a; one listener) |
| (none) | `SUBSTRATE_CONFIG`: YAML file holding any override |

The table grows as slices land.

## Slice (c): routes, scopes, who may decide, the tools

**Routes.** Every route is registered with a scope in one table (`internal/server/server.go`: `Route{Pattern, Scope,
UserOnly, Handler}`); registering one without a scope, or twice, is an error at start (`TestEveryRouteHasAScope`, and
`TestARouteWithoutAScopeCannotBeRegistered` which fails when the check is removed). `cmd/tangent` hands the host the
`/api/` routes (httphost `Routes`) and the rest as mounts (`/ws`), and registers each route's scope on the auth scope
table, so the guard (enforce mode) refuses a request that lacks it before the handler runs.

**G7: deciding is a signed-in person's.** A route that resolves or elevates an approval is `UserOnly`: it needs a user
session (`Kind "user"`, a session id) *and* `tangent.resolve`, which only the owner holds
(`internal/access.MayResolve`). libauth treats `*` and `tangent.*` as holding every scope, so the scope alone would let a
key with a wildcard decide; the session test is Tangent's own. Shown against the real host with the real identity
module and real minted keys: no credential gets 401; a key holding `tangent.resolve` by name, `tangent.*`, `*`, or only
view/draft/submit gets 403 on every deciding route (`TestEveryAgentCredentialIsRefusedOnEveryDecidingRoute`, which fails
when the user-session test is weakened: checked), while the owner's session reaches them
(`TestTheOwnerSessionReachesTheDecidingRoutes`). `TANGENT_OPERATOR_TOKEN` and `TANGENT_ELEVATION_TTL` are dropped, and
with them the elevation endpoints and the participant cookie. **Legitimate agent resolve paths: none.** What agents may
do instead: acknowledge delivery (`tangent.deliver`: `tangent_turns_ack`, `tangent_interaction_ack`,
`tangent_relay_ack` and the turns ack route), and take back their own request (`tangent.withdraw`: `tangent_hitl_withdraw`,
`tangent_artifact_withdraw`). Each agent class holds an API key now; letting a class use local trust later is a change to
how the host admits it, not to Tangent's scopes.

**The browser participant** is the signed-in user: `Ref user:<id>`, authority `tangent-identity`, assurance
`identity-session` (rank 3 in `interaction.AssuranceRank`, so it supersedes the presenter a lower assurance opened, as
the operator session did). API-key callers are recorded as `standalone-local:<key name>` / `key-verified`, gateway
producers as `gateway:<name>` / `adapter-verified`; `identity-session` maps to the event stream's `actor.verified: "token"` (a person proved themselves, which `operator-token`
was) and `key-verified` to `"claimed"`: an agent's key is not a person, so the two are not collapsed (ruling 2, decided:
the closed set `token`, `claimed`, `none` is an architect ruling and the port does not change it; `adapter-verified` keeps
reporting `other`; `operator-token` and `loopback-unverified` stay valid in history with no new producer, because old rows
carry them). A possible follow-up, for after the migration and for the libs vocabulary's owner, not opened here: a fourth
value for a verified machine credential, so `key-verified` need not be reported as `claimed`. Both are accepted, distinct, in the payload's assurance vocabulary
(`internal/events/approval.go`). Existing `participant_sessions` rows (1 282 in the rehearsal copy)
are migrated history the new code never reads. Cross-origin protection: `Sec-Fetch-Site`/`Origin` on every route
(`sameOriginGuard`), and identity requires the SPA to send `X-Requested-With` on every unsafe request (for gui-tangent).

**Tools.** They register on the `mcp` module's server (`mcp.WithServer`), each with exactly one scope
(`internal/mcp/tool_scopes.go`), authorized per call by `access.AuthorizeTool` against the caller's principal, listed to
a caller only if it holds the scope (`UseToolFilter`), and refused with a fixed `forbidden` result that names neither the
scope nor whether the tool exists. A tool with no scope is unreachable, not open; a server built with no authorizer
refuses everything. A plugin's tool gets `tangent.plugin` when it registers and loses it when withdrawn. Names are
unchanged (73 tools, 67 deprecated aliases; recorded files, compared byte for byte against the real host). Caller
identity comes from the credential only: `caller.scope`, `requester_scope` and `owner_scope` arguments are still
accepted and ignored when there is a credential. The in-process loopback caller acts as `tangent:plugin-host` (no
name, so the unlabeled caller partition it always had; scopes view/draft/submit/cancel/close/withdraw/deliver/plugin,
never resolve).

**Other changes in this slice.** Composition: `internal/boot` now takes the host's database handle, MCP server,
authorizer and verifier and returns the route table (no flock, no golang-migrate, no listeners); `cmd/tangent/compose.go`
wires it. `/mcp` needs the scope `tangent.mcp` (the mcp module's own). The health migration check reads goose's table
(`persistence.Inspect`; its "dirty" case no longer exists) and tells the operator to restart rather than run a command
that no longer exists. `tangent plugin install|enable|disable|remove|list|dir` are back (they touch no database).
`internal/smoke` boots the built binary on a throwaway data directory with a first-run owner and a probe key.

**Known open after this slice.** The `tangent session` and maintenance commands (`--db-check`, `--db-backup`, restore, retention) are not ported and
`internal/db` still carries the old golang-migrate helpers that ~36 test files use to build a test database
(`internal/testdb` is the replacement; moving them is mechanical and not done). `ui/` is untouched (slice e).

## Read-only commands

Only serving (the default command) starts the modules that open files and creates the data directory. `version`,
`config`, `health`, `help` and `--help` start config, logging, health and cli only and create nothing
(`TestReadOnlyCommandsCreateNothing`, empty HOME). `plan` (cmd/tangent/plan.go) decides; `TestRootHasNoValueFlags`
keeps its word-splitting honest.

## Testing rule

Tangent is in use on the machines these tests run on. `internal/testenv` isolates every test process and every binary
a test starts (HOME and every XDG variable in a temporary tree, a clean environment); `TestNoUnisolatedBinaryRuns`
fails when a test file starts a process any other way, with a short list of pre-Substrate test files exempt until the
code they test is ported or removed. `scripts/test` runs `go test` under the same isolation.

## Pin d453ac8

One bump of Substrate (`bac8c60` to `d453ac8`), nothing pinned between. What it changed here:
- The offline owner and key commands (gap 6 above).
- `httphost.Stopping(r)`: every long-lived handler (the five event streams and `/ws`) takes its context from
  `internal/stopping.Context(r)`, which ends with the request or when the server begins to stop. `untilShutdown` is gone;
  the SIGTERM timing tests (`TestG5ShutdownWithOpenConnections`) are unchanged and pass.
- Identity refuses to create a signing key when the database already holds users or keys and no key file exists
  (`identity.allow_new_signing_key` overrides). For this app: a restored database without its key file will not start.
- `scan-public` is the framework's command (`go run github.com/hollis-labs/substrate/cmd/substrate scan-public --tree`),
  in CI and as `make scan-public`; the copied script is deleted.
- Not in the pin, so still ours: the Host check (`internal/hostguard`), the cross-process open-and-migrate lock,
  `queue.reservation_secs`, the MCP JSON-response option.

## Pin da9f59b

`d453ac8` to `da9f59b` (the driver's note on the first read): BOM `security/auth` v0.3.1 and a Go floor of 1.26.11
(GO-2026-6859). `go.mod` says `go 1.26.11`; hosts with 1.26.10 fetch the toolchain on first build. The full gate was run
again after the bump.

## Document security policy (ruling 4)

Slice (c) deleted `internal/server/csp.go` with the static handler, so for a while the SPA's pages carried no policy.
It is back (`internal/server/csp.go`, `server.SecurityHeaders`), applied outermost in `cmd/tangent/serve.go` so it is on every
response, a refused Host included. What it does, unchanged from before except for the dev proxy:
- `Content-Security-Policy` per request: `script-src 'self'` plus the hash of the SPA's sandbox shim (an `srcdoc` preview inherits
  it), `connect-src 'self'` plus this Host's own `ws://`/`wss://` (so `network.fetch` is a barrier for every renderer),
  `object-src`/`base-uri` none, `form-action 'self'`, `frame-ancestors 'none'`; no `default-src` or `frame-src` on purpose
  (they would risk blocking `about:srcdoc` frames).
- `Permissions-Policy` (everything denied, `clipboard-write=(self)`), `Referrer-Policy: no-referrer`, `X-Content-Type-Options`,
  `X-Frame-Options: DENY` (artifact responses set their own), `Cross-Origin-Opener-Policy`, `Cross-Origin-Resource-Policy`.
- There is no report endpoint, and there never was one.
- Dropped: the dev-proxy variant (`'unsafe-inline'` for Vite) and `mintParticipantSessions` (the identity module's sign-in is the
  session now). The SPA's dev server serves its own pages.
- Tests: `TestTheDocumentPolicyIsOnTheSPAResponses` (index, `/index.html`, two deep links, an asset, an API path, `/healthz`, through the
  real chain), `TestAHostRefusalCarriesTheSecurityHeaders`, and the old suite in `internal/server/csp_test.go` (the hash recomputed from the SPA
  source, the directives, the embedded bundle carrying the shim verbatim).

## Removed: the desktop app, the LaunchAgent and installer, the Caddy snippet (owner's decision, slice f)

Removed in their own commit; each can come back as its own task.
- **Wails desktop app:** `cmd/tangent-app`, `internal/appshell`, `packaging/macos` (Info.plist, icon), `scripts/build-macos-app.sh`,
  `cmd/tangent-packagecheck` and `internal/packagecheck` (they only verified `Tangent.app`), the `build-app` target and the
  Wails requirement in `go.mod`.
- **LaunchAgent and macOS installer:** `cmd/tangent-launchagent`, `cmd/tangent-install`, `internal/launchagent`,
  `internal/installer`, `scripts/install-macos.sh`, the `launch-agent-*`, `install-macos*` and `uninstall-macos` targets,
  `docs/launch-at-login.md`, `docs/installing.md`.
- **Caddy snippet for remote MCP:** `deploy/caddy/tangent-mcp-auth.caddy`, and the section of `docs/mcp-integration.md` that
  described the proxy marker, `TANGENT_MCP_TOKENS` and the proxied listener (replaced by a short "Caller authentication"
  section; `docs/architecture.md` points at it).
- **CI and hooks:** the macOS `go-desktop` job is gone; the `go` job no longer filters two packages out of `go list` (nothing needs
  gtk/webkit or `pkg-config` any more, and `go build ./...` and `go vet ./...` now work on a Linux host without them); `lefthook.yml` and
  `scripts/pre-push-go-test.sh` lost their pkg-config exclusions.
- **Checked:** `grep` finds no remaining importer of any removed package; `go build ./...` and `go vet ./...` pass over the whole module.
  The remaining mentions are history (`CHANGELOG.md`, `docs/adr/`, `docs/manual-tests/`) and the removal notes themselves.
  Stale prose elsewhere in `docs/architecture.md` and `docs/mcp-integration.md` about loopback admission and the old listeners reconciles
  at release, not here.

## MCP over stdio (Substrate local trust, PR #20)

Tangent does not serve MCP over stdio. **Dropped, and nothing used it:** the old binary had no stdio MCP code either
(`cmd/tangent/main.go` and `maintenance.go` in `../tangent-previous` have no such command or transport), no client in
`docs/client-table.md` uses it, and the only `stdio` mentions in the docs are about agent adapters (Claude/Codex
streaming) and other tools' stdio servers. A stdio client should use the running server's HTTP endpoint, `POST <base>/mcp`
(Streamable HTTP) with an identity API key as `Authorization: Bearer`. `tangent mcp` exists only to say so and exits
non-zero (`TestTheStdioCommandSaysWhatToUseInstead`), so a client configured for it is told instead of getting "unknown
command". It is not wired in this port: a second process on the
same database would start the delivery and reply-mail workers and the recovery pass twice, and the cross-process
open-and-migrate lock is not in the framework yet. It would need a client-only boot as well. If it is wired later:
`StdioOptions{LocalTrust: true, LocalScopes: <explicit list>}` in code, never from config, never the default `*` (which holds
`tangent.resolve`), with the list leaving out the decision and administration scopes (`stdioScopes` in
`cmd/tangent/stdio_test.go` is the list that holds today: what a plugin may use and `tangent.mcp`).

What is held now (`cmd/tangent/stdio_test.go`, through the module's own `ServeStdio` over pipes on the real stack):
`TestStdioScopesNeverHoldTheDecision`, and `TestStdioUnderLocalTrustRefusesEveryToolOutsideItsScopes`, which derives from the
published tool names and their scopes every tool needing `tangent.resolve` or `tangent.admin` and requires each to be
refused under local trust. **No resolve action is reachable as an MCP tool**: that set is empty (`TestNoToolNeedsTheDecisionScope`
too); deciding is the browser's, at a user session, on the user-only routes. (`tangent_interaction_definition_resolve`
resolves a definition reference, not an item awaiting a decision.) Because the set is empty the refusal check is also run with a
narrowed local list ({view}) and requires every other tool to be refused, which shows the per-tool scope check holds under local
trust; it was break-checked by widening the list.


## Deleted tests: what covers each behaviour

The branch deletes these test files outside the removed desktop packages (`cmd/tangent-app`, `internal/appshell`,
`launchagent`, `installer`, `packagecheck`). The suite goes from 1151 to 1094 tests; the deleted ones belonged to
participant sessions, the operator token, the 7844 listener and the old `internal/db`-coupled harness. "Gone" means the
behaviour no longer exists, with the reason.

| Deleted file | Now |
|---|---|
| `cmd/tangent/help_test.go` | `TestReadOnlyCommandsCreateNothing` (help and version touch nothing); `TestAnUnknownSubcommandFailsWithoutStartingAnythingOrCreatingFiles` (cmd/tangent/datadir_test.go). |
| `cmd/tangent/session_test.go` | Gone: `tangent session` revoked participant sessions, which no longer exist (operator commands section). |
| `cmd/tangent/shutdown_test.go` | `TestAFailedShutdownStillExitsNonZero` (cmd/tangent/shutdown_test.go, real binary); `TestG5ShutdownWithOpenConnections`. |
| `internal/boot/caller_auth_test.go` | Caller configuration is the `tangent` config block: `TestParse`, `TestParseReplyMailKeys` (internal/settings) and `TestCallerConfigurationIsValidatedWhenTheServerIsBuilt` (a forwarder that is not a producer, duplicates, bad names, the plugin-child prefix). The elevation TTL test is gone with elevation. |
| `internal/boot/listeners_e2e_test.go` | `TestProxiedGatewayKeepsItsForwardedSessionHeaders`, `TestProxyBoundary` (internal/hostguard). The 7844 listener is gone; proxied admission is the Host check on the one listener. |
| `internal/mcp/caller_auth_test.go` | `TestCallersProducerIsolationOverBothTransports`, `TestCallersRefusesRelayAndPluginOverBothTransports`, `TestCallersRoomInteractionAndInboxOwnership`, `TestAKeySeesAndCallsOnlyWhatItsScopesAllow`. Token-hash admission (`TANGENT_MCP_TOKENS`) is gone: a producer is an identity key. |
| `internal/mcp/forwarded_caller_integration_test.go`, `forwarded_caller_test.go` | `TestProxiedGatewayKeepsItsForwardedSessionHeaders` (attestation read through the proxy path, an inconsistent one refused), `TestVerifiedGatewayCallerScopeIsIdenticalOnBothBackends`, `TestGatewayMetadata*`. The SSE transport variants are gone with `/sse`. |
| `internal/mcp/tether_hitl_gateway_smoke_test.go`, `tether_room_workflow_smoke_test.go` | These were build-tagged (`tether_smoke`) and drove the old `sse` catalog entry. Gone with `/sse`; replaced by `TestGoMCPClientOverStreamableHTTP` (the client Tether uses), `TestRoomWorkflowCompletionOverStreamableHTTP`, `TestAnAgentKeyAsksAndTheOwnerAnswers`, and `internal/smoke`'s catalog check, which now expects `transport: http` at `<base>/mcp` (`TestAnSSECatalogEntryIsCatalogStale`). |
| `internal/participant/*` (`elevation_policy_test`, `elevation_ttl_test`, `operator_test`, `store_test`) | Gone: participant sessions, the operator token and elevation are replaced by the identity module's sign-in, which has its own tests in the framework. What decides is covered by `TestOnlyASignedInUserWithTheScopeMayResolve` (internal/access), `TestEveryAgentCredentialIsRefusedOnEveryDecidingRoute`, `TestTheOwnerSessionReachesTheDecidingRoutes`. |
| `internal/server/channels_participant_test.go` | `TestChannelSendWorksForTheSignedInOwner` (cmd/tangent/channels_participant_test.go). |
| `internal/server/hitl_inbox_e2e_test.go` | `TestHITLInboxJoinsPublicMCPBrowserProjectionAndRestart` (cmd/tangent/hitl_inbox_e2e_test.go: /mcp with a key, the owner's browser projection, stop and restart on the same data directory). |
| `internal/server/listeners_test.go` | Gone with the 7844 listener. |
| `internal/server/mcp_deadline_test.go` | `TestAnMCPCallThatWaitsLongIsNotCutByAServerDeadline`, `TestAnOrdinaryRouteKeepsTheHeaderReadDeadline` (cmd/tangent/mcp_deadline_test.go), `TestG5StreamOutlivesTimeouts`. httphost sets only a header-read and an idle timeout, so the old per-route deadline clearing is gone. |
| `internal/server/operator_deelevate_test.go`, `operator_elevation_limiter_test.go`, `operator_token_test.go`, `operator_ws_test.go` | Gone with elevation and the operator token. Their live claims are covered by: a deciding action needs the owner's session (`TestEveryResolveRouteIsUserOnly`, `TestNoAgentCredentialMayResolve`, `TestTheOwnerSessionGetsPastTheResolveGate`), a requester cannot answer its own approval and the recorded assurance (`TestVerifiedFollowsRecordedAssurance`, internal/events), and sockets (`TestG5WebSocketThroughTheChain`, `TestHandler_ParticipantResolverGatesTheUpgrade`). |
| `internal/server/participant_capability_test.go` | `TestEveryRoutesScopeIsOneTheOwnerSessionHolds`, `TestAKeyHoldingOnlyARoutesScopeIsAdmittedByTheScopeGuard` (cmd/tangent/participant_capability_test.go), `TestEveryRouteHasAScope`. |
| `internal/server/participant_session_test.go` | Gone: minting a session by opening a URL. Kept in new form: security headers on every response (`TestAHostRefusalCarriesTheSecurityHeaders`, `TestTheDocumentPolicyIsOnTheSPAResponses`), cross-site requests refused (`TestACrossOriginEffectRequestIsRefused`, `TestPluginRouteIsOriginGuarded`, `TestHITLBrowserRoutesRequireJSONAndSameOrigin`). |
| `internal/server/plugin_config_http_test.go` | `TestPluginConfigHTTPGuardsCASSecretPresenceAndApply` (internal/server; guards, stale-revision 409, secret presence only, apply). |
| `internal/server/plugin_lifecycle_test.go` | `TestRuntimePluginRoutesAndManagementKeepTheirGuards` (internal/server). |
| `internal/server/plugin_routes_test.go` | `TestPluginRouteWorksForTheSignedInOwner`, `TestPluginRouteIsRefusedWithoutASessionAndForAnAgentKey`, `TestPluginRouteIsOriginGuarded`, `TestPluginRouteAppearsInTheRouteTableWithItsScope`, `TestPluginRouteFailureRendersARefusal`, `TestPluginRouteCannotSetACookie`, `TestPluginRouteBodyIsCapped`, `TestServerRefusesAPluginRouteNoCallerCouldReach`, `TestServerRefusesPluginRoutesWithNoUserGate` (internal/server), and `TestPluginRoutesAndManagementAreGuardedByTheRealHost` (cmd/tangent, the real 401 and CSRF half). |
| `internal/server/sse_longevity_test.go` | `TestG5StreamOutlivesTimeouts`, `TestAnMCPCallThatWaitsLongIsNotCutByAServerDeadline`. |
| `internal/server/static_cache_test.go` | `TestTheSPAIsCachedAsTheFrameworkDecidesThroughTheRealChain` (cmd/tangent/static_cache_test.go: hashed assets immutable, documents `no-cache`, a missing asset 404 and not immutable). **ETag and 304 revalidation are gone**: the framework's webui sends no `ETag` or `Last-Modified` (probed on the real host), so `TestStaticETagRevalidates` and `TestStaticETagIsPerHandler` have nothing left to test. A behaviour change for the SPA, not a loss of data. |

The one `t.Skip` the branch adds: `no SPA build embedded; make build-ui populates internal/webui/dist`, in `internal/server/csp_test.go` (the sandbox-frame shim must match the SPA), which needs a built SPA and skips when the embedded directory holds only its placeholder (CI runs `npm run build`
first, so it runs there).

Found while writing these, not changed: an owner `POST /api/rooms/<unknown id>/close` answers 500 (`insert legacy room
surface: constraint failed: FOREIGN KEY`) where 404 would be expected. `internal/mcp/browser_rooms.go` and
`internal/interaction/legacy_room.go` are unchanged from main, so it predates the branch.

## Operator commands: ported, dropped, not available

The old binary's maintenance flags and `tangent session` (from `../tangent-previous/cmd/tangent/maintenance.go`,
`session.go`):

- **Ported:** `tangent database check [--db PATH]` (integrity and foreign-key check, opened read-only, creates nothing) and
  `tangent database backup --to PATH [--db PATH]` (`VACUUM INTO` from a read transaction, so consistent beside a running
  server; refuses an existing target, never writes the source, verifies the copy and removes a damaged one, mode 0600).
  Both run before any module starts and take the data directory's database by default. They replace `--db-check` and
  `--db-backup`.
- **Dropped:** `--db-restore` (stop Tangent, move the damaged `tangent.db` (and its `-wal`/`-shm`) aside, copy the backup
  into place as `tangent.db`, start), `--db-drain` and `--db-repair` (they served the old trigger and single-writer scheme;
  goose owns the schema now), `tangent session list|revoke` (participant sessions no longer exist; the owner's session is
  identity's). `--db-compact` is deferred (low value).
- **Not available after cutover until ported** (content-removing governance operations built around the old single-writer
  lock, which the framework does not have across processes; the old binary refuses the new database, so they are gone until
  someone ports them; the owner uses none of them and decided to cut over without them; none showed in any timer, schedule, unit or
  shell history on the server host. Follow-up task CW-20261112-0122 covers reviewing and porting any that are wanted later):
  - `--retention-plan`: report which interactions and surfaces are past their retention window.
  - `--retention-apply`: redact every record past its window (needed `--confirm`).
  - `--retention-history`: print the retention operation log.
  - `--erase-interaction`: redact one interaction's caller payload, drafts, resolution and adapter text.
  - `--erase-surface`: redact every payload on one surface, including the legacy room copy.
  - `--erase-exchange`: redact one relay exchange's body and its receipts' and outbox row's free text.
  - `--erase-channel`: the same for every exchange in one channel, both directions.
  - `--erase-drafts`: delete one interaction's draft revisions and write their tombstones.
  - `--purge-surface`: delete a surface and everything that cascades from it.
  - `--close-surface`: close a surface and cancel its outstanding interactions (removed no content).
  - `--expire-capabilities`: delete every expired or revoked host-mediated effect handle.
  - `--external-deletion-report`: list the external references one interaction holds that Tangent cannot delete.
  Supporting flags `--actor`, `--policy-ref`, `--backup-dir`, `--dry-run` and `--confirm` go with them. `internal/retention`
  and its documentation stay in the tree.

## For the GUI agent (gui-tangent)

What the backend now requires of the SPA, in one place; keep it current.

- **Sign-in.** The first owner is created offline on the host (`tangent identity setup`); `POST /api/setup` is always
  refused (403 `setup_offline`), so the SPA has no first-run screen that creates an owner. Sign in with
  `POST /api/login` (username, password); the session lasts 12 h. Deciding (answering an inbox item, approving, the
  inbox archive and delete routes) needs the signed-in user session and `tangent.resolve`, which only the owner holds; an
  API key or plugin key is refused on every deciding route whatever its scopes.
- **`X-Requested-With`** on every unsafe request (`POST`, `PUT`, `PATCH`, `DELETE`): identity's CSRF rule. Same-origin only
  (`Sec-Fetch-Site`/`Origin` are checked on every route).
- **Health paths.** `/api/health/ready` and `/api/health/capability` (session or key with `tangent.view`);
  `/readyz` and `/healthz/capability` are gone; `/healthz` stays open.
- **Form-collect repair stays in the SPA.** `repairFormSchema` is browser-side and the new GUI must keep it.
- **What a browser session holds.** The owner's session carries the scopes of the owner; the tool and route scope table is
  `internal/mcp/tool_scopes.go` and `internal/server` (`access`), and each route's scope is in the route table
  (`TestEveryAgentCredentialIsRefusedOnEveryDecidingRoute` lists the deciding ones).
- **Security headers.** CSP is set on every response (`internal/server/csp.go`): `script-src 'self'` plus the shim's
  sha256, `connect-src` self plus ws/wss, no inline script beyond that shim. A new bundle must keep to it.
- **No operator-token elevation.** The old `TANGENT_OPERATOR_TOKEN` elevation is gone; the sign-in session is what
  decides, so `OperatorTokenPrompt` has no backend to prompt for.

## Cutover notes (host-run, in this order)

1. Merge the port, tag Tangent, then bump the three plugin modules (`runner`, `tesseract`, `torque`) to that tag and
   release them in `hollis-labs/plugins`; only then cut over, with the plugins reinstalled (`make install-plugins`).
2. Stop the old Tangent. Take an online backup of its database.
3. `tangent migrate-data`, `tangent migrate-files`, `tangent migrate-secrets --dry-run`, then `tangent migrate-secrets`
   (section above). Create the owner (`tangent identity setup`) and one key per client class and producer
   (`tangent identity key create`); put the keys in the clients' own configuration.
4. Config block: `tangent.allowed_hosts` (the public name), `gateway_producers` and `trusted_forwarders` (the Tether
   gateway's key name), `reply_to_optional`, `tether_url`, `tether_sender`, `tether_token_ref`.
5. The Tether catalog entry for Tangent: read it first (the driver's two reads disagree: `http` at the public name plus
   `/mcp`, or `sse` at `<base>/sse`). Either way it must end as transport `http` at `<base>/mcp` with an identity key for
   the producer as its credential (instead of the proxy service token). **A gateway entry change takes effect only at a
   Tether hub reload, which restarts every gateway child**; so make the entry change, then reload the hub, in the cutover
   window and after Tangent is up with the key minted and the producer listed in `tangent.gateway_producers` and
   `trusted_forwarders`. `internal/smoke`'s catalog check reports `CATALOG_STALE` for an `sse` entry or a URL not ending in
   `/mcp`. After cutover refresh the catalog and compare the
   tool list with the 73 names (the gateway does not refresh http upstreams on notifications, so plugin tools registered
   after start appear on a refresh).
6. **An old plugin binary must fail loudly.** A plugin not reinstalled still presents no key: `/mcp` answers it `401`
   (`TestMCPWithoutACredentialIsRefused`), and the child's calls into the host fail. That the plugin host's health shows it is NOT tested here: check the
   plugin host's health after start, and treat a plugin whose calls get 401 as not reinstalled.
7. Cerberus' doctor reads `/readyz`; that is a Cerberus change (`/api/health/ready` with a key holding `tangent.view`, or
   `/healthz`), routed by the driver.
8. Rollback: the old data directory and database are untouched, the keychain entries are kept; start the previous binary.
