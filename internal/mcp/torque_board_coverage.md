<!-- Why internal/mcp/torque_board_test.go is gone (CW-20260911-0070). -->

It tested the Torque board's tools through an in-memory MCP rig with the plugin
compiled in. Neither half of that arrangement exists any more: the plugin is a
separate process, and a subprocess plugin calls back into Tangent over HTTP
`/mcp` — which an in-memory transport cannot provide, so the test could not be
made to pass by fixing it.

Its coverage did not go anywhere. Every behaviour it asserted is held, on a real
`pluginhost` with a fake Torque at the wire, by the Torque plugin's own tests
(now `torque/internal/torque` in hollis-labs/tangent-plugins):

| It asserted | Now held by |
|---|---|
| open → stage → sync round trip | `TestOpenQueriesTorqueThenCreatesARoomThenPresents`, `TestSyncPushesStagedChangesThenPullsFreshCards` |
| a Torque outage leaves the board alone | `TestOpenReportsATorqueOutageAndCreatesNothing`, `TestARefusedTransitionDoesNotAbortTheSync` |
| dispatch routes by tool name | `TestMCPDispatchRoutesByToolName` |
| the sync route runs the same sync | `TestHTTPRouteRunsTheSameSync` |

The one thing it held that those do not — that the tools reach Tangent's real
MCP surface — moved UP rather than away, to
`internal/smoke/plugin_surface_test.go`. That is a stronger assertion than the
one deleted: it builds the plugin, emits its manifest, installs it through
`tangent plugin install`, boots the shipped binary, and compares what was
installed against what the running process advertises. The old test compared two
views of one compiled artifact; the new one has discovery, the protocol check,
the spawn and the handshake inside it.
