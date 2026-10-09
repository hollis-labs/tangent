// Package plugin is the public surface a Tangent plugin is written against
// (CW-20260930-0102).
//
// A plugin is its own program, speaking the plugin-sdk subprocess wire
// (github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess) to the Tangent that spawned
// it. The SDK is host-neutral and stays that way; this package carries only the
// Tangent-specific half — the shapes a plugin registers, the capability a
// browser route is gated on, the plugin.yaml a plugin emits, and the one-method
// caller it drives Tangent's tool surface through. pkg/plugin/hostclient is
// that caller over MCP.
//
// # A leaf, deliberately
//
// Nothing here imports github.com/hollis-labs/tangent/internal/... . Go lets a
// package inside this module import internal/, and a plugin in another module
// importing this one would then silently compile Tangent's host internals and
// pin them through the tangent version it requires.
// internal/smoke's TestPublicPluginSurfaceIsALeaf holds that.
//
// The definitions live here and the host aliases them (internal/pluginhost,
// internal/authz, internal/pluginpkg), so a host type and the plugin type are
// the same type, not two that are kept in step.
//
// # Scope
//
// Exactly what the first-party plugins use. The host's own machinery — the
// Host, discovery, install, lifecycle and isolation — stays internal. A generic
// "a plugin calls its host's tools" abstraction may move down into plugin-sdk
// when a second host needs one; until then it lives here.
package plugin
