# Tangent plugin capability boundary

CW-20261003-0067 implements a Tangent-only native read adapter using the
capability package in published `libs/plugin-mcp v0.1.1`. It does not complete
the historical multi-host task or make an operator grant issuer available.

A native plugin receives an exact owner-scoped handle at load. Its `Tools()`
handle captures that load, including a host-issued epoch and monotonic native
registration generation. Reload cannot lend a new owner's authority to an
old handle. This registration tuple is distinct from the subprocess driver's
incarnation tuple; it must not be used to authorize a child or UI lease.

## Reviewed host input

The composition root may configure `ToolCapabilityConfig` before any plugin
load. Both a `ToolCapabilityProvider` and an atomic shared `Budget` are
mandatory. A plugin-scoped handle cannot configure the provider or replace
the tool backend. The shipped composition has no approved provider, so native
plugin tool calls refuse with `capability_denied` before reaching the backend.

The provider authenticates the actual native handle, validates the concrete
tool and its resource arguments as read-only, and returns current grant,
policy revision, transport scope and cancellation lease. An initiating caller
and caller policy are required unless the host explicitly approved background
execution. Request JSON, manifest capability requests, tool labels, claimed
callers and unsafe-install flags are never authority. No unsafe-install bypass
is added here. A provider must return promptly, honor cancellation, return
immutable snapshots and support concurrent calls.

`host.tangent.tools.read`, schema 1, is a closed host extension. Its only
operation is `tangent/tools/read`; it requires exact operations, targets and
effects allowlists and request-byte, response-byte, deadline and concurrency
ceilings. The adapter supports only `read` effects. Requests are detached JSON
bounded to 32 KiB; a reviewed response ceiling cannot exceed 1 MiB. Calls have
the existing host dispatch deadline and must fit every effective ceiling.
Missing/unknown scopes, absent budgets, expired grants, wrong audience,
noncurrent generation, policy revision changes and withdrawn leases refuse.

The shared enforcer validates current authority before the read, reserves and
releases its budget, and validates again before returning content. Cancellation,
unload, revocation or a policy change during a read discards the result. There
is no retry on refusal. Writes and destructive operations remain unsupported:
the existing `ToolCaller` cannot couple a permit's `CheckCommit` to the
backend's actual effect transaction. A provider must not describe a mutating
tool as a read.

Denial counters use the shared closed code/reason vocabulary. Default logging
contains only that vocabulary and the fixed capability name, never arguments,
results, caller/tool/grant labels or raw policy errors. A host-supplied audit
sink receives the shared sanitized metadata DTO; it must honor cancellation
and provide bounded buffering/retention. Telemetry failure cannot grant access.

## Separate existing surfaces and remaining work

Child Init still receives **explicit empty grants**, and the base-profile
driver offers no reverse host services. `pkg/plugin/hostclient` uses ordinary
local HTTP MCP; its labels are advisory and are not a connection-authenticated
plugin binding. Installed processes run as the local user without an OS
sandbox. This adapter cannot prevent such a process independently reaching
localhost, its own application client or the filesystem.

Plugin browser routes retain the existing same-origin, real participant-session
and per-route capability gates. Participant cookies do not cross into the
plugin. Those gates do not issue plugin grants. The composition root's own
`Host.Tools()` remains a separate host-owned handle, not a plugin grant.

Private synthetic conformance tests exercise the actual native load/callback
adapter with explicit fixture-owned reviewed policy and budget inputs. They
cover reached reads, current generation, refusal, withdrawal before disclosure,
budget release and sanitized telemetry. This proves the adapter, not a deployed
issuer, subprocess reverse profile, OS isolation or another host's enforcement.
Production positive execution requires a settled reviewed provider and genuine
caller/runtime binding. Shared `mcp.reach` reverse-service admission and atomic
write backends remain unsupported here.
