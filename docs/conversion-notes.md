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
   two such values. Decision taken: they move to the `secrets` module by a command run on the host, never printed;
   a dry mode moves references only. Open until slice (d).
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
6. **G8: identity had no offline bootstrap and `/api/setup` was open on an empty database. Closed by the pin at `d453ac8`.**
   `tangent identity setup --username NAME` creates the first owner on the host (password from a no-echo prompt or the
   first line of stdin, never an argument or the environment) and `tangent identity key create --name N --scope S...
   (--out PATH | --stdout)` a key. `POST /api/setup` is always refused (403 `setup_offline`). The tests create the owner
   the same way before the server serves (`identity.Service.SetupOwner`, which the command calls; the smoke harness runs
   the shipped binary's own commands) and sign in with an ordinary `POST /api/login`. The commands start only the
   modules they need (`plan.go`): no listener, no tool server.

7. **Plugin children cannot call back (found in slice c; needs a decision).** A subprocess plugin reaches Tangent's tools
   through `/mcp` at `TANGENT_MCP_URL` (`pkg/plugin/hostclient`) with no credential: it was a local caller. `/mcp` now
   refuses a request without one, so the first-party `runner`, `tesseract` and `torque` plugins can no longer drive
   Tangent (the runner's turn never reaches the inbox; `TestInstalledRunnerCarriesATurnThroughTheInbox` is skipped with
   this reference, and was green on the old binary). Two designs (see `docs/client-table.md`): (A) the host mints a
   per-child identity API key, hands it over in the child's environment, and `hostclient` presents it; this follows the
   identity model but only works once the plugins repository is built against the new `hostclient`; (B) the host gives
   each child its own URL with a per-child secret, served from a mount the host owns: works with the shipped plugin
   binaries, but is a second door beside identity. Recommended: A, with B only as a bridge if the plugin release cannot
   land with the cutover. Waiting for the driver/owner.
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

Not yet covered: artifact bodies on disk (`artifacts/`), the plugin directory and its `config.sqlite`, the keychain
values (G2), the plugin binaries.

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
producers as `gateway:<name>` / `adapter-verified`; `identity-session` and `key-verified` map to the event stream's
`actor.verified: "token"` (a credential the host verified, which `operator-token` was) and are accepted in the payload's
assurance vocabulary (`internal/events/approval.go`). Existing `participant_sessions` rows (1 282 in the rehearsal copy)
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

**Known open after this slice.** The desktop shell (`cmd/tangent-app`, `internal/appshell`) still imports the removed
boot API and does not build (it needs Wails and GTK, which this host lacks; slice f, after the owner decides). The
`tangent session` and maintenance commands (`--db-check`, `--db-backup`, restore, retention) are not ported and
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
