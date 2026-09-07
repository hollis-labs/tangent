# Launch at login (macOS LaunchAgent)

The piece that makes "always running" survive a reboot. A user LaunchAgent at
`~/Library/LaunchAgents/com.hollislabs.tangent.plist` starts the **headless
`tangent` daemon** at login. The desktop app is not started at login: a window
appearing at every login is unwanted, and the app adopts a running daemon when
the user opens it (see the adopt-or-boot design in `internal/appshell`).

This document describes the facility (`CW-20260905-0031`). It is **not
installed on the reference development machine**: there, Cerberus supervises
the dev instance (`.agent-ops/project.yaml`, `deployment.type: dev_session`).
The stable install that performs the real install is `CW-20260907-0020`, after
`CW-20260907-0018` moves dev to its own port and database.

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
go run ./cmd/tangent-launchagent install --binary /usr/local/bin/tangent --port 7842 [--db PATH] [--log-dir DIR]

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
