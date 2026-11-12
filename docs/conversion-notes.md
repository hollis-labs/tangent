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
   reply where it got JSON.
6. **G8: identity has no offline bootstrap and `/api/setup` is open on an empty database.** With the identity module
   selected, the first caller of `POST /api/setup` becomes the owner (`*`). There is no config key to close it and
   no programmatic way to create the owner or a key (`Service` has `User`, `Verify` and key rotation only). The plan
   forbids first-run setup over HTTP, so the product path waits for the framework's offline bootstrap. Tests create
   their owner through `/api/setup` on a throwaway data directory only, and so do hand checks of the built binary
   on a throwaway data directory (nothing real is behind it).
7. **Pins.** `substrate pins check --app .` passes at `bac8c60`; `pins sync` moved `libs/mesh/core` (0.8.0 to 0.6.0),
   `libs/ui/ui-go` (0.4.0 to 0.1.0) and `libs/harness/core` (0.13.0 to 0.16.0) to the BOM versions and the existing
   packages still build and vet.
8. **Hygiene.** `scripts/scan-public --tree` reports 86 findings in the inherited tree (test fixtures that look like
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

Not yet covered: artifact bodies on disk (`artifacts/`), the plugin directory and its `config.sqlite`, the keychain
values (G2), the plugin binaries.

## Environment and paths (so far)

| Old | New |
|---|---|
| `TANGENT_DB_PATH` (default `~/.tangent/tangent.db`) | `database.sqlite.path`, anchored in the data directory |
| (none) | `TANGENT_DATA_DIR`, else `$XDG_DATA_HOME/tangent` |
| `TANGENT_HTTP_PORT` / `--port` (7842) | `httphost.addr` (default `127.0.0.1:7842`, must be loopback) |
| `TANGENT_OPERATOR_TOKEN`, `TANGENT_ELEVATION_TTL` | removed with the operator-elevation model (see the auth slice) |
| `TANGENT_MCP_TOKENS`, `TANGENT_MCP_TRUSTED_FORWARDERS`, `TANGENT_PROXIED_HTTP_ADDR` | removed: identity API keys replace them |
| (none) | `SUBSTRATE_CONFIG`: YAML file holding any override |

The table grows as slices land.

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
