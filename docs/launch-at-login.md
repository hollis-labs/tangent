# Launch at login (macOS LaunchAgent)

The piece that makes "always running" survive a reboot. A user LaunchAgent at
`~/Library/LaunchAgents/com.hollislabs.tangent.plist` starts the **headless
`tangent` daemon** at login. The desktop app is not started at login: a window
appearing at every login is unwanted, and the app adopts a running daemon when
the user opens it (see the adopt-or-boot design in `internal/appshell`).

This document describes the facility. `make install-macos` writes and loads
the LaunchAgent as part of the stable install; see
[`installing.md`](./installing.md). A dev instance run with `make dev` is not
managed by it.

## Decisions

| Decision | Choice | Why |
|---|---|---|
| What starts | the headless daemon | no window at login; the app adopts on demand |
| `RunAtLoad` | `true` | start at login |
| `KeepAlive` | `false` | quit means quit; launchd does not fight a deliberate stop. Revisit if crashes matter |
| Verbs | `launchctl bootstrap` / `bootout` | the modern verbs; `load` / `unload` are deprecated |
| Who writes the plist | `tangent-launchagent` (and `make launch-agent-*`) | idempotent; no tray toggle in v1 (that is `CW-20260905-0030`, stable 1.1) |
| Mechanism | LaunchAgent plist, not `SMAppService` | no ObjC/Swift shim, and the same shape ports to a systemd unit for the Linux server work |

## The path problem

The plist hardcodes the daemon's absolute path. Moving or renaming the binary
(or the `Tangent.app` bundle it lives in) breaks it silently: at the next login
launchd finds nothing to run and says so only in its own log. The facility
therefore:

- **refuses to install** when the given binary is not an absolute path to an
  existing, regular, executable file, and says which of those failed;
- **re-validates on `status`**, so a plist whose binary has since moved is
  reported as a problem with the fix spelled out (install again with the new
  path, or uninstall).

## Commands

```bash
# Print the plist, touching nothing. Works on any OS.
go run ./cmd/tangent-launchagent render --binary /usr/local/bin/tangent --port 7842

# Install or update and (re)load. macOS only. Idempotent: re-running with the
# same settings boots the agent out and bootstraps it again so launchd always
# matches the file.
go run ./cmd/tangent-launchagent install --binary /usr/local/bin/tangent --port 7842 [--db PATH] [--plugin-dir DIR] [--log-dir DIR] [--env KEY=VALUE ...] [--replace-env]

# Report the plist, whether launchd has it loaded, and whether the binary it
# names is still there. Non-zero exit when the agent would not start the daemon.
go run ./cmd/tangent-launchagent status

# Boot out and remove. Idempotent.
go run ./cmd/tangent-launchagent uninstall
```

The `make` targets `launch-agent-render`, `launch-agent-install`,
`launch-agent-uninstall`, and `launch-agent-status` wrap these. They take
`LAUNCH_AGENT_BINARY` (default: the workspace build, which is the right default
only for `render`), `LAUNCH_AGENT_PORT` (default 7842), and optionally
`LAUNCH_AGENT_DB`.

### Plugin settings live in the daemon's environment

The host holds no plugin configuration: a plugin reads its own environment,
which it inherits from the daemon. Under launchd that means the plist's
`EnvironmentVariables`. `--env KEY=VALUE` (repeatable, on both
`tangent-launchagent install` and `tangent-install install`) writes a variable
there, for example:

```bash
go run ./cmd/tangent-launchagent install --binary ~/.local/bin/tangent \
  --env TANGENT_TESSERACT_NAMESPACES=user/<name>/memory
```

**An install keeps every extra variable the installed plist already carries**,
whether an earlier `--env` put it there or a person added it by hand, and
`--env` adds to or overrides them. An upgrade therefore cannot silently drop a
plugin's configuration. `--replace-env` writes exactly the `--env` given.
`TANGENT_HTTP_PORT`, `TANGENT_DB_PATH` and `TANGENT_PLUGIN_DIR` are not extra
variables: they come only from `--port`, `--db` and `--plugin-dir`.

## What the plist contains

- `Label`: `com.hollislabs.tangent`, the same identifier as the desktop
  shell's bundle.
- `ProgramArguments`: the daemon binary, alone. Configuration travels as
  environment, not flags.
- `EnvironmentVariables`: `TANGENT_HTTP_PORT` and, when given, `TANGENT_DB_PATH`.
  Omitted entirely when neither is set, so the daemon's own defaults apply.
- `RunAtLoad` true, `KeepAlive` false, `ProcessType` `Background`.
- `StandardOutPath` and `StandardErrorPath`: `~/Library/Logs/Tangent/tangent.log`
  (or `--log-dir`). The directory is created on install.

## Where the code is

`internal/launchagent` renders and validates the plist and performs install,
uninstall, and status through a `Launchctl` interface. `ExecLaunchctl` is the
only thing in the repository that runs `launchctl`, and no test constructs it:
the package's tests use a recording fake and a temporary home directory, so
`make test` never touches a real launchd domain or `~/Library`.
`cmd/tangent-launchagent` is the thin command over the package.

## Out of scope

A general `tangent service install` covering launchd and systemd belongs with
the Linux/server work. Starting the app (rather than the daemon) at login, and
the tray toggle for start-at-login, are `CW-20260905-0030`.
