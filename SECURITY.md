# Security policy

## Supported versions

Tangent is pre-1.0 software. Security fixes are made on `main` and the newest
tagged release. Older releases may not receive backports.

## Report a vulnerability

Do not include an exploit, token, database, room content, or other sensitive
material in a public issue.

Use GitHub's private vulnerability-reporting flow when the repository's Security
tab offers it. If it is unavailable, contact a repository maintainer privately
through a contact channel published on the Hollis Labs organization or
maintainer profile. Include:

- the affected commit or version and operating system
- the listen address and whether the port was reachable beyond loopback
- reproduction steps and the security impact
- whether credentials or user data may have been exposed
- a safe way to contact you about coordination

Maintainers will acknowledge a private report, investigate it, and coordinate
disclosure; response times are best effort during the pre-release period.

## Deployment boundary

Tangent is a **localhost, single-user** application. The server listens on
`127.0.0.1:7842` and serves the MCP endpoints (`/mcp`, `/sse`), the browser
workflows and the `/hitl` operator inbox from that one port.

- Loopback admission is **not authentication**. Any local process running as
  the same user can reach the MCP surface and can mint a participant session.
  Tangent moves authority off the URL; it does not defend against a hostile
  local process.
- The browser API rejects non-loopback `Host` headers and cross-origin browser
  requests, including same-origin DNS-rebinding attempts, and requires JSON
  command bodies. Participant routes carry a participant session and a
  capability check.
- A `standalone-local` partition is advisory, not a security boundary: any
  local caller can assert any partition.
- There is no built-in TLS and no remote-access mode. Do not expose the port
  on a non-loopback interface.
- Plugins are separate programs spawned as subprocesses with the user's
  authority. The host holds no plugin configuration or secrets; a plugin reads
  its own environment. Renderer trust classes isolate presentation but are not
  a plugin sandbox (see `docs/renderer-trust-classes.md`). Install only plugins
  you trust.

## Data at rest

There is no built-in at-rest encryption. Rooms, interactions, drafts and
captured responses are stored in a SQLite database (default
`~/.tangent/tangent.db`) and may contain sensitive content. Protect it with
normal account and disk-encryption controls. Backup, restore and retention
operations are described in [`docs/database-operations.md`](docs/database-operations.md);
backups contain the full store and must be protected like the live data.

## External data processors

Tangent makes no outbound model calls of its own. Content flows to and from the
agents and plugins you connect; first-party plugins talk to the Torque and
Tesseract services you configure, and their data handling is governed by those
services.

## Current security limitations

The canonical list is **Current limitations** in
[`docs/architecture.md`](docs/architecture.md). The security-relevant ones:

- no authentication beyond loopback admission and participant sessions
- no remote access and no built-in TLS
- no at-rest encryption
- no plugin sandbox
- pre-1.0 contracts and migration guarantees

These are deployment constraints, not hidden roadmap promises. Operate within
them or place Tangent behind controls that provide the missing boundary.
