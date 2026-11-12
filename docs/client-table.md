# Clients: what each holds today, what it holds after the port, who changes what

Every caller of Tangent after the port holds a Substrate identity credential: an API key (agents, producers, machine
clients) or a signed-in user session (the person in a browser). Tangent no longer admits anyone by being on loopback,
by a digest in an environment variable, or by a cookie it minted for a navigation. The key's **name** is the caller's
application id (`standalone-local:<name>`, or `gateway:<name>` for a configured gateway producer), so an item written
under a partition before the port stays visible to the same client under the same name.

A key is minted by the owner (identity API, signed in; shown once). Its scopes are exactly the tools and routes the
client uses. Nothing here grants `tangent.resolve` to a key; and a key that holds `*` or `tangent.*` still cannot
decide (`internal/access.MayResolve` needs a signed-in user session as well), which `cmd/tangent/host_test.go` shows
against the real host for every deciding route.

Scopes: `tangent.mcp` (reach `/mcp` at all), `tangent.view`, `tangent.draft`, `tangent.submit`, `tangent.cancel`,
`tangent.close`, `tangent.withdraw`, `tangent.deliver`, `tangent.plugin`, `tangent.admin`, `tangent.resolve`
(user sessions only, in practice the owner's).

| Client | Today: credential and where it is stored | After: credential and scopes | Who changes configuration at cutover |
|---|---|---|---|
| **Local agent sessions** (Claude Code, Codex, `tachyon`, `parallax`, `cerberus`, `portfolio-manager`, `fragments-engine`, `tangent-deploy-smoke`, ...) over `/mcp` | None. Admitted because the request came from loopback; the application id is whatever the client wrote in the request (`caller.scope`, `requester_scope`), recorded as `standalone-local:<id>` with assurance `loopback-unverified`. | One API key per client class, named for it (`codex`, `claude-code`, ...), scopes `tangent.mcp` plus the tools the class uses: typically `tangent.view`, `tangent.submit`, `tangent.deliver`, `tangent.withdraw`; a class that cancels or closes sessions adds those. Stored in the client's own MCP configuration as an `Authorization: Bearer` header (never in argv). Assurance `key-verified`. | The owner mints each key; whoever owns each client's MCP configuration (the agent launcher's profiles, each agent's own MCP config) adds the header. The old `caller.scope` argument is still accepted and ignored: the key's name decides. |
| **(b) Proxied producers**: the Tether gateway and each machine's own gateway (`gateway:tether-gateway`, `gateway:macbook-1`, ...) | A producer token whose SHA-256 is in `TANGENT_MCP_TOKENS` on the Tangent service, on the always-proxied listener (`TANGENT_PROXIED_HTTP_ADDR`, 7844); the token itself in the gateway's catalog entry. Producers named in `TANGENT_MCP_TRUSTED_FORWARDERS` may attest the session they forward for (`X-Forwarded-User-Id`, `X-Tether-Session-Id`, ...). Assurance `adapter-verified`. | One API key per producer, named exactly as the producer is today (`tether-gateway`, `macbook-1`), scopes `tangent.mcp` plus what its agents may do. The producer's name is listed in the `tangent.gateway_producers` setting (and in `tangent.trusted_forwarders` for those that attest sessions); that is what makes it `gateway:<name>` rather than a local caller. The forwarded-session headers are read exactly as before. The 7844 listener is gone: producers use `/mcp` on the one listener. | The owner mints the keys and sets the two lists in the Tangent config override. The gateway's operator changes the catalog entry for Tangent from `transport: sse`, `url: <base>/sse` to Streamable HTTP at `<base>/mcp` with the key as a header. **Which MCP client library the Tether gateway uses for Tangent, and whether it accepts an SSE-framed reply to a POST, cannot be told from Tangent's side** (see G6b in `docs/conversion-notes.md`); the gateway owner has to confirm it before cutover. |
| **The browser** (the SPA) | A participant session cookie Tangent minted on a same-origin document navigation, elevated to operator assurance with `TANGENT_OPERATOR_TOKEN` to decide. | A signed-in identity user session (the owner's). Same-origin; every unsafe request also carries `X-Requested-With` (identity's CSRF rule). Deciding needs the session and `tangent.resolve`, which only the owner holds. | gui-tangent: the SPA must send `X-Requested-With` on every `POST`/`PUT`/`DELETE`/`PATCH`, use `/api/health/ready` and `/api/health/capability` for health (`/readyz` and `/healthz/capability` are gone), and sign in through identity. The owner signs in once per session (12 h). |
| **(g) Plugin children** (the first-party `runner`, `tesseract`, `torque` plugins; any installed plugin) | None. The host starts the child with `TANGENT_MCP_URL` and the child (`pkg/plugin/hostclient`) POSTs to `/mcp` as a local caller. | One short-lived key per child, minted by the host through the identity service when the child starts: named `plugin-<id>`, scopes `tangent.mcp` plus view, draft, submit, cancel, close, withdraw, deliver and plugin (never resolve, admin or a wildcard); ten minutes, renewed every three while the child lives, revoked when it stops. It reaches the child as the path of a private file (`TANGENT_MCP_KEY_FILE`) that the host keeps current; `hostclient` presents it as a bearer credential on every request. The in-process loopback caller (`tangent:plugin-host`) holds the same scopes without `tangent.mcp`. | The plugins repository: bump the `tangent` requirement of the three plugin modules to the release carrying the new `hostclient` and release them; at the cutover reinstall the plugins (`make install-plugins`). No owner action: the keys are the host's. |
| **(h) Docs and reply producers** (`tangent_docs_enqueue`, `tangent_artifact_*`, `tangent_turns_*`, `tangent_relay_*`: Fragments Engine's `write_doc`, the Tangent skill, agents that collect replies) | None, as for local agents (loopback). | The producing agent's own key. Enqueue and create are `tangent.submit` (turns enqueue too); reading and awaiting a reply are `tangent.view`; acknowledging that a reply arrived is `tangent.deliver`; taking back one's own request is `tangent.withdraw`. The producer collects a reply with `tangent_turns_await`, `tangent_artifact_await` or `tangent_relay_receive` on its own key. Room workflows that carry `completion.reply_to` are the exception (#149): Tangent itself mails the operator's answer to that Tether URN, as an outbound caller, see the Tether row below. | The owner mints; the producer's owner adds the header. Fragments Engine and the Tangent skill are the likely first two to need it. |
| **Cerberus / supervisors** probing health | None (`/healthz`, `/readyz`, `/healthz/capability` on loopback). | `/healthz` stays open. The readiness and capability documents are at `/api/health/ready` and `/api/health/capability` and need `tangent.view`: give the probe a key with it. | Whoever owns the Cerberus resource definition (`tangent.cerberus.yaml`) and `TANGENT_SMOKE_KEY` for `make smoke`. |

## Outbound: Tangent calling Tether (reply mail, #149)

| Caller | Today | After | Who changes configuration at cutover |
|---|---|---|---|
| **Tangent's reply-mail worker** mailing an operator's answer to the agent in `completion.reply_to` | `TANGENT_TETHER_URL`, `TANGENT_TETHER_SENDER` and a send-only Tether service token `TANGENT_TETHER_TOKEN` in a root-only env file. Unconfigured, answers stay queued. `TANGENT_REPLY_TO_OPTIONAL=1` is the rollout switch. | `tangent.tether_url`, `tangent.tether_sender` and `tangent.reply_to_optional` in the config block (no secret there). The token is a secret, never config: `tangent.tether_token_ref` names it in the secrets store (`file://tangent/tether-send-only`); `TANGENT_TETHER_TOKEN` in the environment still works when no reference is set. | Whoever holds Tangent's service environment: move the three values; store the token with the secrets module and set the reference. |

## Health probes: who calls what

Every caller found in this repository today, and what each calls after. `/healthz` (liveness) stays open and keeps its path.
Readiness and capability moved under `/api/health/` and need `tangent.view`; every caller below that reads them needs a key
that holds it (`tangent.mcp` is not needed for these, they are not `/mcp`).

| Caller | Calls today | Calls after | Note |
|---|---|---|---|
| Cerberus resource health check (`tangent.cerberus.yaml`, `health:`) | `GET /healthz` | `GET /healthz` (unchanged) | Liveness only, open: no key needed. The supervisor restarts on it, so it must not touch a dependency. |
| `cerberus resource doctor tangent-dev` (as documented in `docs/mcp-integration.md`) | `/readyz` | `GET /api/health/ready` with a `tangent.view` key | The doctor is Cerberus's code, not this repository's: whoever owns it must give it a key. Not verifiable from here. |
| `make smoke` / `internal/smoke` (the MCP smoke test and the deployment smoke) | `/readyz`, `/healthz/capability`, `/healthz` | `/healthz`, `/api/health/ready`, `/api/health/capability`; key from `TANGENT_SMOKE_KEY` (the harness mints its own with `tangent identity key create`) | Done in slice (c). |
| `docs/manual-tests/workflow-smoke-tests.md` and `docs/mcp-integration.md` (operator `curl` lines) | `/readyz`, `/healthz/capability[/{kind}]` | the `/api/health/...` paths with `-H "Authorization: Bearer <key>"` | Documentation only; updated here. |
| `tangent --db-check`/maintenance commands (old `cmd/tangent/maintenance.go` probed `/readyz` to say that something answers the port) | `/readyz` | `/healthz` (open): the question is only "does something answer", so no key is needed | The commands are not ported yet (see below); when they are, they ask `/healthz`. |
| `cmd/tangent-install` / `internal/installer` (waits for readiness after loading the LaunchAgent) | `/readyz` (then `/api/health/ready` after slice c) | removed with the installer (owner's decision) | |
| The desktop shell (`internal/appshell`, adopts a running server) | `/healthz` | removed with the shell (owner's decision) | |
| The SPA | `/readyz`, `/healthz/capability` | `/api/health/ready`, `/api/health/capability` (a signed-in session, `tangent.view`) | gui-tangent. |
| Fleet tooling outside this repository (anything else polling a Tangent port) | unknown from here | `/healthz` needs nothing; any other probe needs a `tangent.view` key and the new path | **Cannot be enumerated from this repository.** The owner knows which fleet monitors exist; each needs either `/healthz` or a key. |

## Not in this table yet

- Desktop shell (`cmd/tangent-app`), launch-at-login and the Caddy snippet for remote MCP: removed by the owner's decision
  (see `docs/conversion-notes.md`); they can return as their own task.
- `tangent session` and the maintenance commands (`--db-check`, `--db-backup`, restore, retention): not ported; they
  talked to the database or the server directly and need a decision about whether they become commands on the new CLI.
