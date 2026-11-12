# Cutover plan: the live Tangent moves to the Substrate build

Status: **written and rehearsed on a scratch copy; not run.** Nothing here touches the live service until the owner
approves the cutover (section 12). The rehearsal record is section 11; what it could not cover is listed there.

**Last commit of the old version on `main`: `793d386`** (the merge of #148, which carries the form-collect repair, after
#149). The Substrate port merged on top of it. A fix to the running version is cut from `793d386`, not from `main`, until
the cutover is done; so is a rollback build.

Related: `docs/conversion-notes.md` (what changed and why, the cutover notes this plan expands, the list of what is not
available afterwards), `docs/client-table.md` (every client, its credential before and after), `deploy.yaml` (the
installer descriptor), `.github/workflows/build.yml` (the release build).

## 1. What this is and what the owner decides

Tangent runs today as one user service over one SQLite database of about 18 MB, an artifacts tree, an installed-plugins
tree and two secrets in the user's keyring, and is in use. The port replaces the binary and moves **all** the data
(the clean break applies to code only) into a new data directory with a new layout. Every row is copied and proved equal,
every file is copied and compared, and the old files are never written: they are the rollback.

Decided by the owner (2026-11-12):
1. The retention, erase, purge, close-surface, expire-capability, external-report and history commands are **not ported**;
   the cutover goes ahead without them (`docs/conversion-notes.md`, "Operator commands"; follow-up CW-20261112-0122).
2. The two older `~/.tangent` directories are **not migrated and not touched** (cleanup follow-up CW-20261112-0115). The
   port migrates exactly one instance, the one the service uses.
3. The Wails desktop app, the LaunchAgent and installer, and the Caddy snippet are dropped.

Still the owner's, because it is his proxy: **which of the two proxy styles in section 9 runs in front of Tangent.** This
plan recommends the first (the proxy as it is, one port change).

The order, fixed by the driver: merge the port (done), tag Tangent, bump and release the three plugin modules against
that tag, then cut over with the plugins reinstalled.

## 2. What runs today (read from the host, names only)

| Piece | Today |
|---|---|
| The service | a user unit `tangent-pilot.service`; `ExecStart` is the bare binary at `~/.local/bin/tangent` (a regular file, not under the fleet installer); running since 2026-10-31 |
| Ports | `7843` (the app) and `7844` (the proxied MCP listener); both loopback |
| Unit environment (names) | `HOLLIS_OTEL_DISABLED`, `TANGENT_DB_PATH`, `TANGENT_HTTP_PORT=7843`, `TANGENT_REPLY_TO_OPTIONAL=1`, `TANGENT_PLUGIN_DIR` (the old tree), `XDG_RUNTIME_DIR`, `DBUS_SESSION_BUS_ADDRESS`, `PATH`, `TANGENT_ELEVATION_TTL`, `TANGENT_PROXIED_HTTP_ADDR=127.0.0.1:7844`, `TANGENT_TESSERACT_API_URL` |
| Environment files (`~/.config/tangent-pilot/`) | `tether.env` (`TANGENT_TETHER_URL`, `TANGENT_TETHER_TOKEN`, `TANGENT_TETHER_SENDER`), `mcp-tokens.env` (`TANGENT_MCP_TOKENS`), `operator.env` (`TANGENT_OPERATOR_TOKEN`), `tesseract.env` (`TANGENT_TESSERACT_TOKEN`) |
| Data | `~/.local/share/tangent-pilot`: `tangent.db` (WAL; schema at the old migrator's version 24, which equals the port's baseline), `artifacts/` (about 800 KB, 42 directories), `plugins/` (about 284 MB, with `.state/config/config.sqlite`), `backups/`, `ingress/`, `releases/`, `stderr.log` |
| Keyring | the user's Secret Service holds two entries for service `hollis-labs.tangent.plugin-config` (the plugin configuration secrets) |
| A plugin child | one running (the Tesseract plugin) |
| The proxy in front | two routes for Tangent's names: `/mcp` and `/sse` go to `7844` with `Host` rewritten to that loopback address; everything else goes to `7843`, tailnet members only, with `Host` rewritten to the loopback address and `Origin` rewritten from the public origin to the loopback origin |
| The Tether gateway | its catalog entry for Tangent is transport `http` at the public name plus `/mcp`, with one credential line (a proxy service token file); it forwards sessions with `X-Tether-*` headers |
| Producers seen in the data | the distinct `caller_scope` values and counts: `sqlite3 -readonly <db> "select caller_scope, count(*) from interactions group by 1 order by 2 desc"` (the key names in section 6, step 8, are these names without the `standalone-local:` or `gateway:` prefix) |
| An older install | `~/.local/share/tangent` exists on the host and holds an **older** Tangent (a database last written 2026-10-12, plugins, backups); no process has it open; it is not used by anything here and must stay untouched |

## 3. Principles

- **The old files are never written.** Every `migrate-*` command opens its source read-only and refuses an existing
  target. Rollback is: stop the new build, remove the drop-in, start the old build on the old files (section 10).
- **The new data directory is a new one: `~/.local/share/tangent-pilot-substrate`.** It does not exist today. The default
  `~/.local/share/tangent` is **not** used: it holds the older install, which the new binary would refuse as a legacy
  database and which `migrate-data` would refuse as an existing target. The unit's drop-in sets `TANGENT_DATA_DIR` and
  `TANGENT_PLUGIN_DIR` explicitly; no default of the new binary resolves to either `~/.tangent` or the older install.
- **No open setup window, ever.** `POST /api/setup` is always refused (403 `setup_offline`). The owner and the first keys
  are created by the offline bootstrap **before the service starts**, and the first check after start is that
  `GET /api/setup` answers `{"setup_required":false}`. If it answers true, stop the service at once.
- **Never print a secret.** Values never go in argv, a log or a command's output; the keyring step reports lengths only.
- **The installer comes last**: `fleet-install deploy` restarts the unit, and the new `serve` refuses a data directory that
  holds only an old database, so the restart is only safe after the migration.
- One kind of client at a time, with the check after each; nothing is switched for a client before its new credential has
  been tried.

## 4. Step zero: before anything else, with nothing changed

Every run of the new binary that boots the modules (`serve`, the `migrate-*` commands, `identity setup` and `key create`,
`plugin install`) **creates** its data directory. A stray run has no safe default here, so this is the check:

1. List both data homes, as the service's user, changing nothing:
   `find ~/.local/share/tangent-pilot ~/.local/share/tangent -ls`. Record the listing in the task.
2. `~/.local/share/tangent-pilot` holds the old files (the list in section 2). `~/.local/share/tangent` holds the older
   install. **`~/.local/share/tangent-pilot-substrate` must not exist.** If it does, stop and report: something has
   already booted the new build there, and what it created (a signing key, an owner, rows) must be understood first.
3. Record `sha256sum` of every file under `~/.local/share/tangent-pilot` except `*-wal` and `*-shm`, after the service is
   stopped (step 1 below), as the "before" for check 14.
4. Confirm `~/.config/tangent-pilot/tangent.yaml` does not exist yet (it is written in step 7).

## 5. Before the window (anyone, no downtime)

1. Tag Tangent and release the three plugin modules against it (`runner`, `tesseract`, `torque` in the plugins
   repository; the new host-callback client). Bump `tangent-plugins.version` in this repository to that plugin release.
   Without this the plugins start and fail loudly with `401` (section 8).
2. Publish the release from `main` (CI is green on the merge): `tangent_<version>_linux_amd64.tar.gz`, `SHA256SUMS` and
   `deploy.yaml`, built by `.github/workflows/build.yml` (frontend built first, and a check that the published binary
   serves the SPA). Nothing is tagged or installed by it. Record the commit; it is the `--commit` of the deploy.
3. Keep the old binary aside, once: copy `~/.local/bin/tangent` to a dated name next to it. The first deploy keeps a
   regular file at the target as `<name>.pre-fleet-install` (as for the other apps); the driver confirms the name with
   `fleet-install deploy tangent --dry-run` before the real deploy. The first rollback is a file swap (section 10).
4. Rehearse on a fresh online-backup copy, in a scratch directory with `HOME` and every XDG variable pointed there
   (section 11). Counts must match, `integrity_check` ok, old tree checksum unchanged.
5. Prepare, without applying: the config file (section 6, step 7), the key list from the producers query (section 2),
   the proxy edit (section 9) and the Tether catalog plan (step 12). Announce to the agents that use the Tangent tools
   that they will be unavailable for the window (section 13).
6. Read `docs/client-table.md` against the producer list: each producer gets a key, or is retired.

## 6. The window (host-run, in this order)

Every command runs as the service's user with its `HOME`. The new binary is extracted from the release tarball into a
scratch directory first (`$NEWBIN` below); it is **not installed yet**. The same environment is used for every
host-run command, so each one places the database, signing key and secrets exactly as the service will:

```sh
export SUBSTRATE_CONFIG=$HOME/.config/tangent-pilot/tangent.yaml
export TANGENT_DATA_DIR=$HOME/.local/share/tangent-pilot-substrate
export TANGENT_PLUGIN_DIR=$TANGENT_DATA_DIR/plugins
unset TANGENT_DB_PATH
OLD=$HOME/.local/share/tangent-pilot
```

1. **Stop**: `systemctl --user stop tangent-pilot`. It must return within the drain (10 s) without a kill; if the journal
   shows `State 'stop-sigterm' timed out`, note it. Confirm no `tangent` or `tangent-plugin-*` process remains
   (`pgrep -af tangent`). Then do step zero 3.
2. **Backup**, read-only on the old file: `$NEWBIN database check --db $OLD/tangent.db` (expect `integrity ok, 0
   foreign-key problems`), then
   `$NEWBIN database backup --db $OLD/tangent.db --to $HOME/.local/share/backups/tangent.pre-cutover.db` (refuses an
   existing target; mode 0600). SQLite leaves empty `-wal` and `-shm` files beside a database it opened read-only when no
   server holds it; the database file's content is unchanged (rehearsed).
3. **`migrate-data`**:
   `$NEWBIN migrate-data --from $HOME/.local/share/backups/tangent.pre-cutover.db --to $TANGENT_DATA_DIR/tangent.db`.
   Expect `46 tables, 18222 rows` (the counts of the day; the figure at the cutover is whatever the backup holds), the
   `verified:` line (counts, per-row digests, type histograms, `EXCEPT` both ways, object definitions, sequence counters,
   `integrity_check`, `foreign_key_check`) and exit 0. About three seconds. It creates the data directory.
4. **`migrate-files`**: `$NEWBIN migrate-files --from $OLD --to $TANGENT_DATA_DIR`. Expect `artifacts N files`,
   `plugins N files`, `verified: every path, size, mode and sha256 equals the source` and `verified: 52 artifact
   revisions match` (the number at the cutover: every recorded revision). It refuses a non-empty target, a link or device
   in the source, and a database that records a revision whose body is missing. A tree that is not `$OLD` (no
   `artifacts/`) is refused for exactly that reason.
5. **`migrate-secrets`**, dry run first, from a session that can reach the same Secret Service the old unit used (the
   keyring may need to be unlocked):
   `$NEWBIN migrate-secrets --to $TANGENT_DATA_DIR --dry-run` (expect the two settings as `would move`, with byte lengths),
   then `$NEWBIN migrate-secrets --to $TANGENT_DATA_DIR` (expect `moved` and `each value read back from the store matches
   the keychain by length and keyed digest`). Values are never printed. The keyring entries are left in place: they are
   the rollback. A store entry holding a different value is never overwritten.
6. **The Tether reply token** (see section 7 for why it moves): store it in the secrets store from its environment file
   without printing it or putting it on a command line, and check only its length:
   ```sh
   ( umask 077; install -d -m 700 $TANGENT_DATA_DIR/secrets/tangent
     sed -n 's/^TANGENT_TETHER_TOKEN=//p' $HOME/.config/tangent-pilot/tether.env | tr -d "\"'" \
       > $TANGENT_DATA_DIR/secrets/tangent/tether-send-only )
   wc -c < $TANGENT_DATA_DIR/secrets/tangent/tether-send-only
   ```
   (the length is the token's plus one for the newline, which the module trims). The config key in step 7 names it.
7. **The config file** `~/.config/tangent-pilot/tangent.yaml`, mode 0600, written by the operator (nothing in it is a
   secret):
   ```yaml
   httphost:
     addr: "127.0.0.1:7843"
   identity:
     secure_cookies: always          # section 9: the proxy as it is sends no X-Forwarded-Proto
   tangent:
     reply_to_optional: true
     tether_url: "<copied from tether.env, TANGENT_TETHER_URL>"
     tether_sender: "<copied from tether.env, TANGENT_TETHER_SENDER>"
     tether_token_ref: "file://tangent/tether-send-only"
     gateway_producers: ["tether-gateway"]      # plus each machine's own gateway producer, by key name
     trusted_forwarders: ["tether-gateway"]
   ```
   `tangent.allowed_hosts` and `identity.trusted_proxy` are **not** set with the proxy as it is (section 9). An
   unresolvable `tether_token_ref` stops the start with a message naming the key (rehearsed).
8. **The offline identity bootstrap**, service still stopped, same binary and environment:
   - `$NEWBIN identity setup --username <owner>`: the password comes from the no-echo prompt (or the first line of
     stdin), never an argument or the environment. It refuses if an owner exists.
   - One key per client, each into a new `0600` file in a `0700` scratch directory that is removed once the client holds
     its key: `$NEWBIN identity key create --name <name> --scope tangent.mcp --scope ... --out <dir>/<name>.key`. The
     **name** is the producer's application id without its prefix, so what it wrote before stays visible to it. Scopes are
     `docs/client-table.md`'s; **the gateway key needs the full non-resolve set** (`tangent.mcp`, `tangent.view`,
     `tangent.draft`, `tangent.submit`, `tangent.cancel`, `tangent.close`, `tangent.withdraw`, `tangent.deliver`,
     `tangent.plugin`) to expose all 73 tools; with only `mcp` and `view` it sees 25 (rehearsed). Nothing gets
     `tangent.resolve`: only the owner's session decides. An existing `--out` file is refused.
9. **Deploy**: `fleet-install deploy tangent --commit <sha12>`. Its preflight backs up the **migrated** database; its
   drop-in (`20-fleet-exec.conf`) clears and sets `ExecStart`, sets `SUBSTRATE_CONFIG`, `TANGENT_DATA_DIR`,
   `TANGENT_PLUGIN_DIR` and `TimeoutStopSec=30`; its restart starts the unit; its health poll reads
   `http://127.0.0.1:7843/healthz`. Then confirm the installed binary is the one the migration used
   (`sha256sum ~/.local/bin/tangent $NEWBIN`, same version string). The unit's other `Environment=` lines and its four
   environment files are left alone: the variables with no reader any more are ignored (section 14).
10. **First checks before anything else is switched** (section 12, checks 1 to 6): `healthz`, setup closed, 401, unknown
    `Host` 403, sign-in, a read.
11. **The proxy** (section 9): the owner changes the `/mcp` route's upstream from `7844` to `7843` and drops `/sse`.
    Until then the gateway cannot reach Tangent.
12. **The Tether gateway** (the driver; Agent Ops applies): the catalog entry's URL does **not** change; only its
    credential changes, from the proxy service token to the gateway producer's identity key. That is a catalog plan and
    takes effect at a hub reload, which restarts every gateway child. **Order: approve the plan, apply it, then reload
    the hub**; never reload to "pick up" a plan that is still waiting (a plan does not survive a hub restart). Do it
    after steps 8 and 10, with the key minted and the producer listed in the config. Afterwards refresh the catalog and
    compare the tool list with the 73 names in `internal/mcp/testdata/tool_names.txt` (the gateway does not refresh an
    http upstream on notifications).
13. **Reinstall the plugins** with the new binary, so each carries the new host-callback client (section 8): for each of
    `runner`, `tesseract`, `torque`, build the pinned plugin release, `--manifest > plugin.yaml`, and
    `tangent plugin install <dir>` (this is `make install-plugins`, run with the environment above). Install is an upgrade
    that preserves each plugin's enable intent; check with `tangent plugin list`, then restart the unit
    (`systemctl --user restart tangent-pilot`) so the host loads them.
14. **The rest of the checks** (section 12), the per-client switches (section 13), and the record in the task.

## 7. What moved out of the environment

| Was | Now |
|---|---|
| `TANGENT_HTTP_PORT=7843` | `httphost.addr` in the config file |
| `TANGENT_DB_PATH`, the old `TANGENT_PLUGIN_DIR` | the drop-in sets `TANGENT_DATA_DIR` and `TANGENT_PLUGIN_DIR` under the new directory; the old `TANGENT_DB_PATH` is ignored |
| `TANGENT_REPLY_TO_OPTIONAL=1` | `tangent.reply_to_optional: true` |
| `TANGENT_TETHER_URL`, `TANGENT_TETHER_SENDER` | `tangent.tether_url`, `tangent.tether_sender` (copied file to file by the driver) |
| `TANGENT_TETHER_TOKEN` | a secrets-store file named by `tangent.tether_token_ref` (step 6). **Why not leave it in the environment file:** the host starts each plugin child with its own environment plus extras, so a variable in the unit's environment reaches every plugin child, a third party's included (`TestEnvironmentInheritanceAndPluginPWD`). The environment variable still works when no reference is set; it should not be the way once the token can be a file. After the new service is verified, the line is removed from `tether.env` (the owner's edit). |
| `TANGENT_TESSERACT_API_URL`, `TANGENT_TESSERACT_TOKEN` | unchanged: they are the Tesseract plugin's, and reach it through the inherited environment (the same test) |
| `TANGENT_MCP_TOKENS`, `TANGENT_PROXIED_HTTP_ADDR`, `TANGENT_OPERATOR_TOKEN`, `TANGENT_ELEVATION_TTL` | **dead**: identity API keys replace the tokens; there is no second listener; the session decides. `mcp-tokens.env` and `operator.env` have no reader |
| `HOLLIS_OTEL_DISABLED`, `XDG_RUNTIME_DIR`, `DBUS_SESSION_BUS_ADDRESS`, `PATH` | unchanged (the service no longer needs the keyring or D-Bus at run time; only the one-time `migrate-secrets` does) |

## 8. Plugins

The host now admits a plugin child by an identity key it mints per child (`internal/childkeys`: ten minutes, renewed
every three, revoked on stop, never `tangent.resolve`, admin or a wildcard) and hands the child as the path of a private
file. A plugin built against the old client presents no key, and `/mcp` answers a request without a credential `401`
(`TestMCPWithoutACredentialIsRefused`), so such a plugin's calls into the host fail. **It does not look broken until it
calls back:** in the rehearsal a plugin built from the pinned (old-client) release loaded, passed the readiness check and
listed its tools; nothing in the rehearsal made it call the host, so the loud failure itself was not observed. Hence the
three first-party plugins are released against the new client before the cutover and reinstalled in step 13, any other
installed plugin must be rebuilt the same way, and check 12 requires a plugin tool that calls back into the host, not
only a green readiness line.

## 9. The proxy

The proxy is the owner's. Two styles work; this is what the scratch tests showed (section 11), with a scratch proxy that
rewrites headers as described. Browser on `https://<public name>`.

| Case | Proxy sends | Tangent config | Result |
|---|---|---|---|
| **A1** as today | `Host` and `Origin` rewritten to loopback, no `X-Forwarded-Proto` | none | sign-in 200, a write with the cookie 204, `/ws` Origin check passes, a foreign Origin is refused; the cookie is `HttpOnly; SameSite=Strict` but **not `Secure`** |
| **A2** as today, recommended | same | `identity.secure_cookies: always` | the same, and the cookie is `Secure` |
| A3 | `Host` rewritten, `Origin` **not** | none | sign-in **403 `bad_origin`**: the Origin rewrite is required in this style |
| **B1** | real `Host` and `Origin` kept, `X-Forwarded-Proto: https` added | `tangent.allowed_hosts: [<public name>]` and `identity.trusted_proxy: true` | sign-in 200, cookie `Secure`, write 204, `/ws` passes; `/mcp` under the public name relies on the `mcpLoopbackHost` workaround (framework gap G9) |
| B2 | as B1 but no `X-Forwarded-Proto` | as B1 | **403 `Untrusted HTTPS proxy`** for everything |
| B3 | as B1 | `allowed_hosts` not set | **403 `Invalid host`** for everything |

**Recommendation: A2.** It needs one change on the proxy (below), keeps the Host rewrite that makes `allowed_hosts` and
`trusted_proxy` unnecessary, leaves the G9 path unexercised on this host, and only adds one line of Tangent config. B
would also work and gives host-name checking inside Tangent, but it changes the proxy in more places in the same window
(stop rewriting `Host` and `Origin`, add `X-Forwarded-Proto`), and a mistake in any one of them turns into a total 403
(B2, B3). In style A the `Host` Tangent sees is always loopback, so `allowed_hosts` protects nothing; the proxy is the
boundary, as it is today.

**The change on the proxy (generic), in the window (step 11):**

```
before:  route "/mcp" and "/sse"  -> upstream 127.0.0.1:7844, Host rewritten to 127.0.0.1:7844
         route everything else    -> upstream 127.0.0.1:7843, tailnet members only,
                                     Host rewritten to 127.0.0.1:7843, Origin <public origin> -> http://127.0.0.1:7843
after:   route "/mcp"             -> upstream 127.0.0.1:7843, Host rewritten to 127.0.0.1:7843   (same access rule as today)
         route "/sse"             -> removed (the transport is gone; it answers 404)
         route everything else    -> unchanged
```

Port `7844` no longer exists after the cutover; a proxy still pointing there loses Tangent for every gateway client.
Whether the proxy itself checks the service token on `/mcp` (rather than only passing it on) is not known from the
repository; if it does, it must pass the identity key through instead.

## 10. Rollback

The old data directory, its database and the unit file are never written by the new build, and the keyring entries stay.
To roll back, at any point before real use:

1. `systemctl --user stop tangent-pilot`.
2. Put the old binary back at `~/.local/bin/tangent` (the first deploy kept it as `tangent.pre-fleet-install`; from the
   second deploy on, `fleet-install deploy tangent --commit <previous>`).
3. Remove the drop-in `~/.config/systemd/user/tangent-pilot.service.d/20-fleet-exec.conf` and
   `systemctl --user daemon-reload`, so the unit's own `ExecStart` and environment apply again. (Whether
   `fleet-install rollback` removes the drop-in itself is not confirmed; remove it by hand as written.)
4. `systemctl --user start tangent-pilot`: the old binary runs on the old files. `~/.local/share/tangent-pilot-substrate`
   is left in place (it is the new build's, and nothing reads it); remove it only to try again, never to roll back.
5. Restore the client changes already made: the proxy route (section 9, reverse the port change and put `/sse` back), the
   gateway's catalog credential (a catalog plan and a hub reload again), and each client's old configuration.

Anything written to the new database after the cutover is not in the old one: the stop in step 1 is the freeze that keeps
that loss at zero if the rollback happens before real use. The plugins in the old tree are the old binaries and work with
the old host. Keep the old tree, the backup and the scratch copies until the owner is satisfied.

## 11. Rehearsal

The whole plan, run with a binary built from `main` at `b55e94d` plus the `migrate-files` correction described below, on a
scratch host (`HOME` and every XDG directory under a scratch directory, loopback port 18153, no process of the live service
touched) with the owner's online-backup copy of the database (read, never written, never
copied off the host or into the repository) and a copy of the live artifacts tree. The keyring step ran against a
**synthetic** Secret Service with made-up values on a private D-Bus session, so the real keyring was never contacted;
the plugin tree was synthetic.

Step by step, with what was observed:

- **Step zero.** The old directory held the database, `artifacts/` (54 files) and a synthetic `plugins/`; the new
  directory did not exist.
- **Backup.** `database check`: `integrity ok, 0 foreign-key problems`; `database backup`: the same on the copy. The source
  file's checksum was unchanged; empty `-wal` and `-shm` files appeared beside it.
- **`migrate-data`.** 46 tables, 18,222 rows, 2.7 s, every verification line, exit 0; an independent `sqlite3` check found
  no difference.
- **`migrate-files`.** 54 artifact files and the plugin tree copied; 52 artifact revisions matched their recorded sha256.
  A first run on a copy that did not yet have the artifacts tree **passed with "0 revisions"**: a defect in the merged
  code, fixed (a database that records revisions against a tree without any body is now refused, with a test that fails
  without the fix).
- **`migrate-secrets`.** Dry run: two settings `would move` with lengths; then `moved`, read back by length and keyed
  digest; store files mode 0600 in a 0700 directory; the synthetic values appear in no log (`grep` finds none).
- **Tether token file.** The step-6 command on a synthetic environment file: a 26-byte 0600 file; the service refuses to
  start without it (`tether_token_ref does not resolve`) and starts with it (`reply mail enabled`, the value in no log).
- **Offline bootstrap.** Owner created; a second owner refused; keys created (`--out` files 0600); an existing `--out`
  refused.
- **Start and checks.** `healthz` ok; `GET /api/setup` `{"setup_over_http":false,"setup_required":false}`; `POST
  /api/setup` 403; no credential 401; unknown `Host` 403; sign-in 200; readiness 200 for the session and for a key; the
  inbox lists the migrated interactions (1,485 of 1,486: one is hidden); the artifacts listing; `/mcp` with the gateway key
  lists **73 tools equal to `internal/mcp/testdata/tool_names.txt`**, under a loopback `Host` and under the public name
  through the proxy path; a key on a deciding route is 403.
- **Stop.** SIGTERM: exit 0, port free, and the whole old tree byte-identical to before (every file but `-wal`/`-shm`).
- **Proxy.** The six cases of section 9, with a scratch proxy.
- **Plugin reinstall mechanics.** `tangent plugin install` on a plugin built from the pinned (old-client) release: installed
  disabled; `plugin enable` then a second `install` is an upgrade that keeps the intent. A plugin tree must have its
  `.state` directory private (mode 0700) or the host refuses to load any plugin (`pluginpkg: .../.state must be a private
  real directory`): check 12. With the migrated plugin configuration database (created with the real table definitions)
  the plugin loaded (`1 plugin(s) loaded, 0 contributed kind(s), 2 tool(s), 1 route(s)`, readiness `ok`), the host minted
  its key file (`.state/<id>/run/mcp-key`, mode 0600), and `/mcp` listed 75 tools: the 73 plus the plugin's two, whose names
  the plugin chooses (`tangent.tesseract_review` has a dot, which the gateway's name lint refuses: `docs/mcp-tool-names.md`).

**Not rehearsed, and why:** the real keyring (synthetic only, by instruction); `fleet-install` and its `--dry-run` (the
driver's, with the real unit); the real proxy; the Tether catalog plan and hub reload (Agent Ops); the plugin reinstall
**with the new plugin release** (it does not exist until the plugin release in section 5 step 1: the mechanics above ran
with the old one); a write through the gateway.

## 12. Checks that gate each switch

1. `curl -fsS http://127.0.0.1:7843/healthz` is 200 `{"status":"ok","checks":{"database":"ok"}}`.
2. `GET /api/setup` answers `{"setup_over_http":false,"setup_required":false}`; `POST /api/setup` is 403.
3. `GET /api/health/ready` with no credential is 401; with an unknown `Host` header anything is 403.
4. Signed in as the owner (`POST /api/login` with `X-Requested-With`): 200; `GET /api/health/ready` is 200 and every check
   passes (the plugin line is a warning until the plugins are reinstalled).
5. `GET /api/inbox` lists the migrated interactions; `GET /api/artifacts` lists the artifacts; a read of one artifact body
   works.
6. A key (not the owner) on a deciding route (an inbox archive or an answer) is 403.
7. `/mcp` with the gateway key: `initialize`, `tools/list` equals the 73 names exactly (plus whatever tools plugins
   contribute once they are loaded: compare the 73 as a subset), one read call works; with an agent key the list is the
   shorter one its scopes allow; with no key it is 401.
8. After the proxy change (step 11): the same, through the public name.
9. After the gateway change (step 12): the catalog refresh lists the 73 names (and the plugins' tools that pass the
   gateway's name lint), and one tool called through the gateway returns a result.
10. Reply mail: the start log says `reply mail enabled`; an answer to a room that carries `completion.reply_to` is
    mailed (the driver checks with a real one).
11. `systemctl --user show tangent-pilot -p ExecStart -p TimeoutStopSec` shows the drop-in's values; `readlink
    ~/.local/bin/tangent` is the installed release.
12. `stat -c '%a' $TANGENT_DATA_DIR/plugins/.state` is `700`; `tangent plugin list` shows each plugin with its enable
    intent; after the restart in step 13 each plugin child is running (`GET /api/health/ready`, the `plugins` line is
    `pass`, and `.state/<id>/run/mcp-key` exists with mode 600) and **one tool of each plugin that calls back into the host
    returns a result** (a green readiness line alone does not show that the plugin holds a working key).
13. One signing key in the data directory (`identity-signing.key` and its `.jwks.json`); the data directory holds
    `tangent.db`, `artifacts`, `plugins` and `secrets`. The old tree's `backups/`, `ingress/`, `releases/` and `stderr.log`
    are not copied (nothing in the new build reads them); they stay in the old tree.
14. The old tree is byte-identical to the listing from step zero (every file except `-wal` and `-shm`).
15. `systemctl --user stop tangent-pilot` (done once at the end, then start again) returns in under the drain and the
    journal shows no kill.

## 13. Clients and who changes what

The full table is `docs/client-table.md`; at the cutover:

| Client | Change | Who |
|---|---|---|
| Every agent that calls `/mcp` | holds an identity key by its producer name, in its own MCP configuration as an `Authorization: Bearer` header | the owner mints (step 8); each client's owner installs |
| The Tether gateway | the credential line in its catalog entry becomes the gateway producer's key (URL unchanged); catalog plan, then hub reload | the driver, Agent Ops |
| A machine's own gateway producer | its own key, listed in `tangent.gateway_producers` | the owner mints; that machine's operator installs |
| The browser | signs in with the owner's account; same origin | the owner |
| Cerberus (resource health, `cerberus resource doctor`) | `/healthz` is unchanged and open; readiness is `/api/health/ready` and needs a `tangent.view` key | whoever owns the Cerberus definition |
| `make smoke` | `TANGENT_SMOKE_KEY` | the operator running it |
| Fleet monitors that poll `/readyz` or `/healthz/capability` | repoint to `/healthz` (open) or hold a `tangent.view` key and the `/api/health/...` path | the owner knows which exist; **cannot be enumerated from the repository** |
| Plugin children | none by hand: the host mints their keys; they are reinstalled (step 13) | the driver |

**Agents lose the Tangent tools** from the stop in step 1 until step 12 completes (the gateway cannot reach Tangent through
the proxy before step 11 and carries no valid key before step 12). Agents that call Tangent from their own MCP
configuration (not through the gateway) lose it until each gets its key.

## 14. Left in place, to clean up after the cutover (the owner's)

- `mcp-tokens.env` and `operator.env`, and `TANGENT_ELEVATION_TTL`, `TANGENT_PROXIED_HTTP_ADDR`, `TANGENT_DB_PATH`,
  `TANGENT_HTTP_PORT` and `TANGENT_REPLY_TO_OPTIONAL` in the unit: no reader any more.
- `TANGENT_TETHER_TOKEN` in `tether.env`, once the secrets-store file is verified (section 7).
- The two keyring entries (rollback until the owner is satisfied) and the old tree.
- The older install, `~/.tangent` on two machines, and the old `plugins/` binaries: CW-20261112-0115.

## 15. Expected downtime

| Surface | Downtime |
|---|---|
| The service (stop to healthy) | about **two minutes**: the stop, the backup (about a second), `migrate-data` (about three seconds on the 18 MB copy), `migrate-files` and `migrate-secrets` (a second or two each), the prompt for the owner's password, the deploy and its health poll |
| The Tangent tools for agents (through the gateway) | from the stop until the hub reload in step 12: **estimate ten minutes, not measured**; preparing the proxy edit and the catalog plan beforehand (section 5.5) and applying them the moment step 10 passes brings it nearer five |
| The plugins | until step 13's restart, a minute after the above |

## 16. What is not available after the cutover

Listed with what each did in `docs/conversion-notes.md` ("Operator commands: ported, dropped, not available", and the
removed-pieces sections). In short:

- **Governance commands, not ported until someone does** (follow-up CW-20261112-0122): `--retention-plan`,
  `--retention-apply`, `--retention-history`, `--erase-interaction`, `--erase-surface`, `--erase-exchange`,
  `--erase-channel`, `--erase-drafts`, `--purge-surface`, `--close-surface`, `--expire-capabilities` and
  `--external-deletion-report`. The old binary refuses the new database, so they cannot be run at all.
- Dropped by decision: `--db-restore` (the procedure is stop, move the damaged database aside, copy a backup into place),
  `--db-drain`, `--db-repair`, `tangent session list|revoke`; `--db-compact` deferred.
- Removed: the legacy `/sse` transport (five probe requests in the log, all on one day in October, none since), the `7844` listener, MCP over stdio
  (the old binary never had it; `tangent mcp` exits non-zero and says what to use), the operator-token elevation, first-run
  setup over HTTP, the `/readyz` and `/healthz/capability` paths, the Wails desktop app, the LaunchAgent and its installer,
  the Caddy snippet.
- Behaviour that changed: the SPA is served without `ETag`/`Last-Modified` (the framework's web UI sends neither, so a
  static asset is revalidated by content-hashed name, not by a 304); the owner's session lasts 12 hours.

## 17. Approval

The owner approves this plan separately from the pull request that carries it. Until then nothing is deployed and the
live service, its data and the gateway's entry are not touched. The cutover is run by the driver on the server host; this
repository's agents do not run any `migrate-*` command against it.
