# Installing the stable Tangent (macOS)

This is the install and upgrade recipe for the **stable** Tangent a person
uses every day: the headless `tangent` daemon under a user LaunchAgent, and
`Tangent.app` in the Applications folder, both built from one tagged tree. It
is the code half of `CW-20260907-0020`; the first cutover on the reference
machine is a sequenced operator action recorded on that task and summarised
at the end of this page.

The version the install places is whatever the tagged tree says: one source,
`ui/package.json`, asserted against everything else by
`internal/smoke/version_test.go`. This page names no version on purpose.

## The two instances

| | Stable (this page) | Dev |
|---|---|---|
| Port | `7842` (the daemon default) | `7843` (`DEV_PORT`) |
| Database | `~/.tangent/tangent.db` (the daemon default) | `<repo>/.tangent/dev.db` |
| Launch authority | LaunchAgent `com.hollislabs.tangent`, headless daemon | Cerberus resource `tangent-dev` |
| Logs | `~/.tangent/logs/tangent.log` | Cerberus |
| Binary and app | `~/.local/bin/tangent`, `~/Applications/Tangent.app` | the workspace build |
| Tether catalog entry | `tangent` (what agents reach by default) | `tangent-dev` (present, **disabled**; reach dev via a scratch catalog or `--only tangent-dev`, CW-20260907-0037) |

Different ports and database paths mean different `.owner` lock files, so the
two never collide on the single-writer flock. See
[`developing.md`](./developing.md#two-instances-stable-and-dev) for the dev
side and [`launch-at-login.md`](./launch-at-login.md) for the LaunchAgent.

## Build the artifacts

From a checkout of the tag being installed:

```bash
make build        # ./tangent, the headless daemon
make build-app    # ./Tangent.app, verified by packagecheck
```

Both must come from the same tree: the installer refuses a daemon and an app
that report different versions.

## Install, and upgrade

Same command, same path. See the plan first:

```bash
make install-macos-dry-run            # probes and prints every action; changes nothing
make install-macos                    # does it
```

or, with the artifacts somewhere else:

```bash
scripts/install-macos.sh install --artifacts /path/to/release --dry-run
scripts/install-macos.sh install --artifacts /path/to/release
```

What `install` does, in order, and where it stops:

1. **Validates the artifact pair.** `tangent --version` and the app's
   `Info.plist` must name the same release; the app must be a real bundle with
   an executable and a substituted version.
2. **Reads what is here now.** The installed daemon's version (if any) and what
   answers the port (`/readyz`, and the release the MCP server advertises).
3. **Refuses** if the port answers but the daemon is not ready (mid-migration
   or a dirty schema); if the port answers with anything other than the
   installed stable daemon (a dev instance still on the stable port has to move
   first); or if the artifact is older than what is installed (a downgrade).
4. **Runs the artifact's `--db-check` against the existing database** and
   refuses if it fails. This is what stops an older binary opening a newer
   database: it refuses, it does not repair.
5. **Places the daemon** at `~/.local/bin/tangent` (atomic rename) and the app
   at `~/Applications/Tangent.app` (staged copy, previous bundle set aside and
   removed only after the new one is in place).
6. **Writes and (re)loads the LaunchAgent** with `bootout` then `bootstrap`,
   so the running daemon is always the one on disk. The plist names the
   binary, port, database, and log file explicitly.
7. **Waits for `/readyz`** and confirms the serving release is the artifact's.
   If that never happens within the wait, the command exits non-zero and says
   where the log is.
8. **Prints the versions before and after**: installed daemon and serving
   release, so an upgrade's record is one line.

Nothing is changed before step 5, and a refusal in steps 1 to 4 leaves the
machine exactly as it was.

Options: `--bin-dir`, `--apps-dir`, `--port`, `--db`, `--log-dir`, `--wait`
(seconds for `/readyz`), `--dry-run`. Off macOS only `--dry-run` runs.

## Uninstall

```bash
make uninstall-macos
```

Boots the agent out, removes the plist, the daemon, and the app. **The
database and its directory are never removed**; delete `~/.tangent` yourself if
that is what you mean.

## After install

- `Tangent.app` opened from Applications finds the daemon on the stable port
  and adopts it; it does not boot a second one, so there is no flock refusal.
- `tangent --version` prints the installed release; `curl -fsS
  http://127.0.0.1:7842/readyz` shows the checks.
- Agents reach the stable daemon through the `tangent` Tether catalog entry,
  unchanged by the install.

## The first cutover on the reference machine

Recorded on `CW-20260907-0020` and run on 2026-09-07. The order keeps the
stable port from ever being dead for a session that loads mux, and the notes
are what the run taught:

1. `tangent --db-backup` of the existing database; keep the sidecar manifest.
2. Stop the Cerberus `tangent-dev` resource, which until then served the stable
   port from the workspace build. **Then wait for its pid to exit**, not just
   for the port to close: the process still holds the database flock while it
   drains, and the stable daemon's first start fails with "another process holds
   the Tangent database" if it starts inside that window (`CW-20260907-0035`
   makes the installer wait for the lock itself).
3. `make install-macos` from the tagged tree; confirm `/readyz`.
4. Bring dev up on the dev port against the workspace database. The prepared
   Cerberus file is the repo-root `tangent.cerberus.yaml`, but **do not run
   any Cerberus lifecycle verb against `tangent-dev` until the Cerberus daemon
   has re-read that file**: its in-memory spec keeps the old port and it finds
   "the running process" by `lsof` on it, so `stop`, `reload`, `apply`, and
   `deploy` would signal the stable daemon (`CW-20260907-0036`). Reconcile with
   `cerberus daemon restart` while the operator watches (other dev sessions are
   that daemon's children), confirm `cerberus resource status tangent-dev` says
   stopped and `inspect` shows the dev port, then `apply`. Until then, run dev by
   hand with `make dev-go` or the equivalent environment. Cerberus's `deploy`
   also needs the pinned Node on its PATH (`CW-20260905-0017`).
5. Leave the `tangent-dev` Tether catalog entry **disabled**. Enabling it beside
   `tangent` makes mux rename one side's identical tool names and route the
   bare `tangent.*` names to the other (`CW-20260907-0037`). Reach dev through a
   scratch catalog copy or `mux mcp --proxy --only tangent-dev`.
6. Collision test: both instances up, neither refuses on the flock or the
   port, `--db-check` under each instance's environment reports its own
   database, a room opened on each.
7. The HITL end-to-end pass from a Claude Code session through the default
   mux configuration against stable
   ([`manual-tests/hitl-inbox-e2e.md`](./manual-tests/hitl-inbox-e2e.md)).

## Where the code is

`internal/installer` performs every step behind a `Launchctl`, a binary
`Runner`, and an HTTP client; its tests use fakes, a temporary home, and an
`httptest` daemon, so `make test` never touches a real installation.
`cmd/tangent-install` is the command; `scripts/install-macos.sh` and the
`make` targets wrap it.
