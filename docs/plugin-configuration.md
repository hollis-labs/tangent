# Plugin configuration

Tangent owns configuration for reviewed manifest-v2 `config.fields` and
`config.secrets`. It does not interpret a plugin's domain JSON, discover ambient
credentials, add grants, or enable a plugin by saving settings. The settings UI
uses the published `@hollis-labs/kit-settings@0.2.0` controlled form and a
host-owned projection of the reviewed manifest.

## Storage and scopes

Only host composition registers schemas and the active scope chain. Overrides
resolve from manifest defaults through client, environment and project, in that
order. A missing narrower override inherits the broader value. Scope IDs are
explicit host inputs, not browser or plugin identity claims. Saving at a scope
changes only that scope, and resetting removes selected overrides rather than
replacing history or other scopes.

Non-secret values and opaque secret references are kept in the private SQLite
`<plugin-root>/.state/config/config.sqlite` settings database. Normal boot selects
client scope `local`; embedders can provide an explicit `boot.Config.ConfigScopes`
chain. Environment/project scopes are not guessed from a plugin or browser input. Declared secret values are kept in the OS keychain: macOS
Keychain, Windows Credential Manager or Linux Secret Service. There is no
plaintext file or environment fallback. The browser receives presence only,
never a saved secret. A replacement secret exists temporarily in the submitted
browser draft and host request; neither side writes it to logs or browser
storage. Drafts belong to a keyed plugin/scope view and are discarded when that
view is disposed. A failed or stale save preserves the draft for an explicit
retry; it does not silently overwrite the latest revision.

Keychain entries are staged under new opaque references before the SQLite
transaction commits. A failure rolls back the transaction and retires staged
entries; previous references remain usable. A completed save advances one
plugin revision across its scopes. CAS prevents a stale tab or another store
instance from overwriting a newer save. Superseded keychain-entry cleanup is
best effort after commit; a cleanup failure does not reverse an earned commit.
These operations are not a distributed transaction across the OS keychain and
SQLite, and interruption can leave an unreachable keychain entry. Ordinary
filesystem ownership and private permissions do not sandbox processes running
as the same OS user.

## Validation, activation and authority

The host supports the manifest's flat string, boolean, integer, number and select
fields. SDK defaults are string encoded; the projection parses them to typed UI
scalars. Integers outside JavaScript's exact range are refused rather than
rounded. The host checks declared keys, scalar types, select options and required
values again on every save. Environment declarations are metadata, not permission
to inherit an environment variable. Conditional domain requirements, such as
messaging's file/settings mode, remain the plugin's responsibility.

Saving settings for a disabled plugin does not enable it. Settings exposes
explicit lifecycle enable/disable controls and shows desired intent separately
from runtime state. Apply refuses a disabled plugin; enable is a separate
participant-authorized choice.

Save persists a revision. Apply explicitly reloads the plugin with a detached
resolved `Init.Config`, including its declared secrets. The successful full load
and registration point marks that incarnation's revision applied. Planning or
spawning alone does not. Reload failure leaves saved settings pending; an older
incarnation cannot mark a newer save applied. Each new supervision attempt
resolves the current revision again. Existing incarnations retain their owned
snapshot until teardown. Known resolved secrets are also passed to the public
driver's literal diagnostic scrubber; status and reports contain no config map.

Global SDK config methods remain refused. A scoped load-owner handle can access
only its own reviewed schema and configuration. A stale or canceled handle
cannot read, register or update another incarnation. Runtime schema registration
cannot create undeclared fields or grant authority. Browser reads require View;
validation/save/reset/apply require Resolve and the same-origin participant
checks used by the host's existing management routes. There is no new room,
plugin or capability grant implied by a config field.

The platform keychain API is synchronous and not interruptible mid-call.
Cancellation is checked before and after a call; the host does not abandon a
credential operation in a detached goroutine. This is a platform shutdown limit,
not a claim of a bounded native keychain operation.

## Messaging first consumer

The messaging manifest declares `configuration_mode=file|settings`, the file
path, explicit endpoint/caller identities, serialized channel and stage lists,
finite transport bounds, the summarizer instruction path and a separate
`tether_token` secret. Tangent renders these scalar declarations generically.
Only messaging parses the serialized lists into its existing typed configuration.

File mode accepts only its absolute configuration path and optional token.
Settings mode requires explicit source/caller identity, lists and finite bounds;
it refuses a simultaneous file path. Supplying an instruction path both as a
field and inside the stage list refuses ambiguity. The existing one-stage
restriction and channel validation remain. An omitted host token means empty,
never ambient fallback. Only an entirely empty `Init.Config` keeps documented
standalone environment mode. Configuration validation occurs before ledger
custody or network calls; successful synthetic fixtures do not install the
plugin or demonstrate live endpoint/provider availability.
