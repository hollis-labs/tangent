// Package server is Tangent's HTTP surface: the browser APIs for the inbox,
// HITL, turns, docs, artifacts, channels, rooms, effects and plugins, and the
// /ws bridge.
//
// It builds a route table. It does not listen, mint sessions, guard hosts or
// verify callers: the Substrate host does (httphost, identity, auth). Every
// route is registered with the scope it needs, and the table is what the host
// maps onto auth.ScopeTable, so a route with no scope cannot exist.
package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	driver "github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-host"

	"github.com/hollis-labs/tangent/internal/access"
	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/effect"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/health"
	"github.com/hollis-labs/tangent/internal/participant"
	"github.com/hollis-labs/tangent/internal/pluginconfig"
	"github.com/hollis-labs/tangent/internal/pluginhost"
	"github.com/hollis-labs/tangent/internal/telemetry"
)

// Config controls route construction.
type Config struct {
	// Logger receives structured logs. Defaults to slog.Default() when nil.
	Logger *slog.Logger

	// Envelope is the loaded go-envelopes service. PR 2 wires it through
	// without consuming it; PR 3 (MCP) and PR 4 (WebSocket) attach
	// validators and dispatchers to it. New returns an error if Envelope
	// is nil so misconfigured boots fail fast rather than at first
	// envelope.
	Envelope *envelope.Service

	// WSHandler is the constructed WebSocket bridge handler (PR 4).
	// When non-nil the HTTP server mounts /ws. Optional in Config so
	// test code can spin up a server without the full WS wiring;
	// production main always passes a non-nil value.
	WSHandler http.Handler

	// HITL is the durable operator-owned inbox application service. When set,
	// the server mounts its dedicated browser API and revision event stream;
	// it remains separate from caller-facing MCP and room WebSockets.
	HITL HITLService

	// HITLEvents optionally adds typed resumable events beside legacy revision pings.
	HITLEvents *HITLEventStream

	// Turns is the durable agent turns FIFO inbox application service (CW-20260913-0019).
	// When set, the server mounts its dedicated browser and daemon APIs.
	Turns TurnsService

	// Docs is the durable Docs inbox application service (CW-20260917-0009).
	// When set, the server mounts its dedicated browser API and revision
	// event stream. Enqueue happens only through the MCP tool surface, not
	// this HTTP API — Docs has no daemon-loopback caller the way Turns does.
	Docs DocsService

	// Artifacts is the durable artifact inbox application service (ADR 0015).
	// The operator acts on an artifact through this HTTP API; agents use the
	// tangent_artifact_* MCP tools.
	Artifacts ArtifactsService

	// Rooms is the browser room API's application service. When set, the
	// server mounts /api/rooms — the participant-authenticated replacement for
	// the SPA's direct /mcp POSTs (ADR 0004 §11). Optional in Config for the
	// same reason MCP and HITL are; production main always passes it.
	Rooms RoomService

	// Inbox is the unified operator projection of canonical interactions.
	Inbox InboxService

	// Channels is the channel pane's application service (CW-20260907-0017).
	// When set, the server mounts /api/channels — the operator's send/read
	// path over channel.Store and relay.Store. Optional in Config for the
	// same reason MCP, HITL, and Rooms are.
	Channels ChannelService

	// Effects is the host-mediated effect broker (ADR 0003 §2.5,
	// CW-20260825-0077). When set together with EffectContext the server
	// mounts POST /api/effects, the single channel a renderer uses to ask the
	// host to act on the world. Optional in Config for the same reason MCP,
	// HITL, and Rooms are.
	Effects *effect.Broker

	// EffectContext resolves the pinned definition binding whose granted
	// capabilities govern an interaction's effects.
	EffectContext EffectContextResolver

	// Health answers the three operability questions liveness, readiness, and
	// per-capability health (CW-20260825-0066). Optional in Config for the
	// same reason MCP and HITL are: a transport-level test constructs a server
	// without a database. When nil, /healthz still answers — liveness reads no
	// dependency — and every other probe reports that the reporter is missing
	// rather than 404ing, so an unwired build is visible instead of silent.
	Health *health.Reporter

	// Participants resolves the signed-in user behind a browser request. It is
	// required: a route table with no gate would publish the browser APIs
	// guarded by nothing but their scope, so New refuses it.
	Participants *participant.Gate

	// PluginRoutes are the ADR 0007 §4 plugin-served browser routes
	// (CW-20260910-0030), as the plugin host recorded them at load. When
	// non-empty the server mounts each one under the reserved
	// /api/plugins/ prefix, behind the same guards every other browser API
	// route uses. Empty is the normal state; see plugin_routes.go.
	PluginRoutes []pluginhost.HTTPRoute
	PluginHost   *pluginhost.Host
	PluginConfig *pluginconfig.Store

	// PluginHostInstance is the shared plugin lifecycle epoch. Production boot
	// supplies it; standalone servers create a fresh epoch when it is absent.
	PluginHostInstance string

	// Telemetry records correlation-bearing observations for the browser
	// transports this package owns: an object-access refusal at the
	// participant gate, and a host-mediated effect the broker declined.
	//
	// Optional, and a nil recorder is a silent no-op rather than a nil
	// dereference. A build with no telemetry serves exactly the same traffic;
	// it just cannot answer what happened afterwards.
	Telemetry *telemetry.Recorder
}

// Route is one HTTP route and the scope it needs.
type Route struct {
	// Pattern is "METHOD /path" as net/http spells it.
	Pattern string
	// Scope is the scope a request must hold. It is never empty: register
	// refuses that.
	Scope string
	// UserOnly marks a route a signed-in user session alone may use, whatever
	// scopes an API key holds. Every route whose scope is access.Resolve is
	// UserOnly; routes whose pattern says "deliver" are not.
	UserOnly bool
	Handler  http.Handler
}

// Method and Path split Pattern.
func (r Route) Method() string {
	method, _, found := strings.Cut(r.Pattern, " ")
	if !found {
		return ""
	}
	return method
}

// Path is the path part of Pattern.
func (r Route) Path() string {
	_, path, found := strings.Cut(r.Pattern, " ")
	if !found {
		return r.Pattern
	}
	return path
}

// Table is the set of routes New built.
type Table struct {
	routes []Route
}

// Routes lists every route in registration order.
func (t *Table) Routes() []Route { return append([]Route(nil), t.routes...) }

// API is the handler for every route under /api/, for httphost's Routes
// option. The host's guard wraps it.
func (t *Table) API() *http.ServeMux {
	mux := http.NewServeMux()
	for _, route := range t.routes {
		if strings.HasPrefix(route.Path(), "/api/") {
			mux.Handle(route.Pattern, route.Handler)
		}
	}
	return mux
}

// Handler serves every route, API and outside, on one mux with nothing in
// front of it: no host guard, no authentication. The host builds its own from
// API and Outside; this is for tests and tools that exercise handlers.
func (t *Table) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, route := range t.routes {
		mux.Handle(route.Pattern, route.Handler)
	}
	return mux
}

// ScopePattern is Pattern's path in the form auth.ScopeTable reads: a
// trailing "{name...}" becomes the table's final "*".
func (r Route) ScopePattern() string {
	path := r.Path()
	if i := strings.LastIndex(path, "{"); i >= 0 && strings.HasSuffix(path, "...}") {
		return path[:i] + "*"
	}
	return path
}

// Outside lists the routes that are not under /api/, which the host mounts
// one by one.
func (t *Table) Outside() []Route {
	var out []Route
	for _, route := range t.routes {
		if !strings.HasPrefix(route.Path(), "/api/") {
			out = append(out, route)
		}
	}
	return out
}

// router collects routes while New builds them.
type router struct {
	cfg    Config
	routes []Route
	seen   map[string]bool
	err    error
}

// handle registers a route under scope. A route with no scope, or the same
// pattern twice, is an error New returns.
func (rt *router) handle(pattern, scope string, userOnly bool, handler http.Handler) {
	handler = sameOriginGuard(handler)
	switch {
	case rt.err != nil:
		return
	case scope == "":
		rt.err = fmt.Errorf("server: route %q has no scope", pattern)
		return
	case rt.seen[pattern]:
		rt.err = fmt.Errorf("server: route %q registered twice", pattern)
		return
	}
	rt.seen[pattern] = true
	rt.routes = append(rt.routes, Route{Pattern: pattern, Scope: scope, UserOnly: userOnly, Handler: handler})
}

// participant registers a browser route: the caller must be a signed-in user,
// holding the scope the capability maps to. The scope is checked by the host
// before the handler runs, and again here against the session's grants, so the
// ADR 0004 §7 matrix is consulted in one place.
func (rt *router) participant(pattern string, capability authz.Capability, handler http.HandlerFunc) {
	scope := access.ForCapability(capability)
	rt.handle(pattern, scope, scope == access.Resolve,
		requireParticipant(rt.cfg.Participants, rt.cfg.Telemetry, capability, handler))
}

// caller registers a route a service credential may use as well as a user: a
// producer enqueuing a turn, a consumer acknowledging one. The scope is its
// own; none of these may be access.Resolve.
func (rt *router) caller(pattern, scope string, handler http.HandlerFunc) {
	if scope == access.Resolve {
		rt.err = fmt.Errorf("server: caller route %q may not need %s: a decision is a user's", pattern, access.Resolve)
		return
	}
	rt.handle(pattern, scope, false, handler)
}

// New builds the route table.
//
// cfg.Envelope and cfg.Participants must be non-nil, so a misconfigured boot
// fails fast.
func New(cfg Config) (*Table, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Envelope == nil {
		return nil, fmt.Errorf("server: envelope service is required")
	}
	if cfg.Participants == nil {
		return nil, fmt.Errorf("server: participant gate is required")
	}
	if err := cfg.HITLEvents.validate(); err != nil {
		return nil, fmt.Errorf("server: hitl events: %w", err)
	}
	if cfg.PluginHostInstance == "" {
		epoch, err := driver.NewHostInstance()
		if err != nil {
			return nil, fmt.Errorf("server: plugin epoch: %w", err)
		}
		cfg.PluginHostInstance = epoch
	}
	rt := &router{cfg: cfg, seen: map[string]bool{}}

	registerHealthRoutes(rt, cfg.Health)

	if cfg.WSHandler != nil {
		// /ws upgrades to WebSocket. The upgrade needs a user session, checked
		// inside the handler before the upgrade so a refusal is an ordinary
		// 403 rather than a socket that closes for no stated reason.
		rt.handle("GET /ws", access.View, false, sameOriginGuard(cfg.WSHandler))
	}

	if cfg.Inbox != nil {
		rt.participant("GET /api/inbox", authz.View, inboxHandler(cfg.Inbox))
		rt.participant("GET /api/inbox/search", authz.View, inboxSearchHandler(cfg.Inbox))
		rt.participant("GET /api/inbox/items/{interactionID}", authz.View, inboxItemHandler(cfg.Inbox))
		if operator, ok := cfg.Inbox.(inboxOperator); ok {
			rt.participant("POST /api/inbox/items/{interactionID}/archive", authz.Resolve, inboxArchiveHandler(operator))
			rt.participant("DELETE /api/inbox/items/{interactionID}", authz.Resolve, inboxDeleteHandler(operator))
		}
	}

	if cfg.HITL != nil {
		// Each route names the capability it exercises, so the ADR 0004 §2
		// table is readable straight off the route table: reads need `view`,
		// acknowledging a presented projection needs `draft`, and submitting a
		// terminal response needs `resolve`.
		hitlHandler := newHITLHTTPHandler(cfg.HITL)
		hitlHandler.logger = logger
		hitlHandler.eventStream = cfg.HITLEvents
		register := func(pattern string, capability authz.Capability, handler http.HandlerFunc) {
			rt.participant(pattern, capability, handler)
		}
		register("GET /api/hitl", authz.View, hitlHandler.inbox)
		register("GET /api/hitl/events", authz.View, hitlHandler.events)
		register("GET /api/hitl/items/{itemID}", authz.View, hitlHandler.item)
		register("GET /api/hitl/items/{itemID}/evidence/{evidenceIndex}/reference", authz.View, hitlHandler.tangentEvidence)
		register("GET /api/hitl/items/{itemID}/evidence/{evidenceIndex}/preview", authz.View, hitlHandler.artifactPreview)
		// Present is gated on `resolve`, not `draft`, since CW-20261016-0174.
		// Presenting binds the participant who may resolve the item — the
		// interaction store refuses a resolution whose participant binding
		// differs from the presenter's — so an unelevated presenter would
		// pin an approval to an identity that can never decide it. That
		// makes presentation authority-bearing, and it takes the same
		// operator assurance as the decision it sets up.
		register("POST /api/hitl/items/{itemID}/present", authz.Resolve, hitlHandler.present)
		register("POST /api/hitl/items/{itemID}/resolve", authz.Resolve, hitlHandler.resolve)
	}

	if cfg.Turns != nil {
		turnsHandler := newTurnsHTTPHandler(cfg.Turns)
		register := func(pattern string, capability authz.Capability, handler http.HandlerFunc) {
			rt.participant(pattern, capability, handler)
		}
		register("GET /api/turns", authz.View, turnsHandler.inbox)
		register("GET /api/turns/events", authz.View, turnsHandler.events)
		register("GET /api/turns/items/{itemID}", authz.View, turnsHandler.item)
		register("POST /api/turns/items/{itemID}/reply", authz.Resolve, turnsHandler.reply)
		register("POST /api/turns/items/{itemID}/dismiss", authz.Resolve, turnsHandler.dismiss)
		rt.caller("POST /api/turns/enqueue", access.Draft, turnsHandler.enqueue)
		rt.caller("POST /api/turns/items/{itemID}/ack", access.Deliver, turnsHandler.ack)
		rt.caller("GET /api/turns/sessions/{sessionID}/replies", access.View, turnsHandler.sessionReplies)
	}

	if cfg.Docs != nil {
		// "read" is gated on `draft`, not `resolve` — marking a doc read is
		// not a terminal decision, it is the same authoring-weight action as
		// a channel's mark-read route above. Acknowledge and archive both
		// move a doc toward or into a terminal interaction state, so they
		// take `resolve`, matching HITL's resolve and Turns' dismiss/reply.
		docsHandler := newDocsHTTPHandler(cfg.Docs)
		register := func(pattern string, capability authz.Capability, handler http.HandlerFunc) {
			rt.participant(pattern, capability, handler)
		}
		register("GET /api/docs", authz.View, docsHandler.inbox)
		register("GET /api/docs/events", authz.View, docsHandler.events)
		register("GET /api/docs/items/{itemID}", authz.View, docsHandler.item)
		register("POST /api/docs/items/{itemID}/read", authz.Draft, docsHandler.markRead)
		register("POST /api/docs/items/{itemID}/acknowledge", authz.Resolve, docsHandler.acknowledge)
		register("POST /api/docs/items/{itemID}/archive", authz.Resolve, docsHandler.archive)
		register("DELETE /api/docs/items/{itemID}", authz.Resolve, docsHandler.delete)
	}

	if cfg.Artifacts != nil {
		// Same capability split as docs: reading and marking read are view and
		// draft; messaging, sign-off, archive and delete move an artifact
		// toward a terminal state or speak for the operator, so take resolve.
		artifactsHandler := newArtifactsHTTPHandler(cfg.Artifacts)
		register := func(pattern string, capability authz.Capability, handler http.HandlerFunc) {
			rt.participant(pattern, capability, handler)
		}
		register("GET /api/artifacts", authz.View, artifactsHandler.inbox)
		register("GET /api/artifacts/events", authz.View, artifactsHandler.events)
		register("GET /api/artifacts/items/{itemID}", authz.View, artifactsHandler.item)
		register("GET /api/artifacts/items/{itemID}/revisions/{revision}/content", authz.View, artifactsHandler.content)
		register("GET /api/artifacts/items/{itemID}/revisions/{revision}/rendered", authz.View, artifactsHandler.rendered)
		register("POST /api/artifacts/items/{itemID}/read", authz.Draft, artifactsHandler.markRead)
		register("POST /api/artifacts/items/{itemID}/messages", authz.Resolve, artifactsHandler.message)
		register("POST /api/artifacts/items/{itemID}/signoff", authz.Resolve, artifactsHandler.signoff)
		register("POST /api/artifacts/items/{itemID}/archive", authz.Resolve, artifactsHandler.archive)
		register("DELETE /api/artifacts/items/{itemID}", authz.Resolve, artifactsHandler.delete)
	}

	if cfg.Rooms != nil {
		// The browser room API.
		//
		// All three routes require `view`, which is the session proving that
		// this browser is an admitted participant. Closing is deliberately not
		// gated on a participant `close` capability, because a participant
		// never holds one (ADR 0004 §7): the tab-strip close button is the
		// SPA's own *caller application* acting, and the close is authorized
		// against that caller's scope inside the room service. The session
		// establishes which browser is asking; the caller identity decides
		// what it may destroy.
		roomHandler := newRoomHTTPHandler(cfg.Rooms)
		rt.participant("GET /api/rooms", authz.View, roomHandler.list)
		rt.participant("GET /api/rooms/{roomID}", authz.View, roomHandler.inspect)
		rt.participant("POST /api/rooms/{roomID}/close", authz.View, roomHandler.close)
	}

	if cfg.Channels != nil {
		// The channel pane's browser API (CW-20260907-0017). Listing and
		// reading need `view`. Sending a message and marking read both need
		// `draft`, not `submit` — and that is a choice, not just an
		// available slot `submit` happened not to occupy.
		//
		// The ADR 0004 §7 matrix's KindParticipant row is exactly {view,
		// draft, resolve, cancel}, and authz.Authorize consults that row
		// unconditionally regardless of what a session's own grant set
		// holds (participant.Gate.Authorize's own doc comment: "the matrix
		// is consulted even where the session's own grant set would have
		// answered"). `submit`'s own doc says it "creates an interaction on
		// a surface" — a caller-application act, not a human's — and a
		// relay exchange is not an interaction at all, so `submit` was
		// never the right shape here, independent of whether a participant
		// could hold it. `draft` is the only authoring capability the
		// participant row does hold, and composing a channel message —
		// non-terminal, immediately visible, revisable by sending again —
		// is exactly what `draft` already means elsewhere in this file. If
		// the relay's operator actions ever earn a capability of their
		// own, that is an amendment to ADR 0004's matrix, decided
		// deliberately, not drift arrived at by finding whatever slot a
		// route happens to authorize against.
		//
		// Gating the send route on `submit` instead — as this route
		// originally shipped — meant a real browser participant session
		// could never hold it, so an operator could never have sent a
		// message through this pane at all: caught live against dev while
		// seeding the phase 4 acceptance run, not by any test, since every
		// existing channels test used a fake ChannelService with no
		// Participants gate configured, making the whole class invisible
		// to them. See TestChannelSendWorksForARealParticipantSession.
		channelHandler := newChannelHTTPHandler(cfg.Channels)
		register := func(pattern string, capability authz.Capability, handler http.HandlerFunc) {
			rt.participant(pattern, capability, handler)
		}
		register("GET /api/channels", authz.View, channelHandler.list)
		register("GET /api/channels/events", authz.View, channelHandler.events)
		register("GET /api/channels/{channelID}", authz.View, channelHandler.get)
		register("POST /api/channels/{channelID}/messages", authz.Draft, channelHandler.send)
		register("POST /api/channels/{channelID}/read", authz.Draft, channelHandler.markRead)
	}

	if cfg.Effects != nil && cfg.EffectContext != nil {
		// `view` is the route's floor, not its decision. Every effect names
		// its own object-access precondition (effect.ObjectPrecondition) and
		// the broker evaluates it; requiring `view` here means a browser with
		// no admitted session never reaches the broker at all, which keeps the
		// audit table free of rows for requests that were never participant
		// acts.
		effectHandler := newEffectHTTPHandler(cfg.Effects, cfg.EffectContext, cfg.Telemetry)
		rt.participant("POST /api/effects", authz.View, effectHandler.request)
	}

	// Plugin registry endpoint (CW-20260911-0035). Serves registry.Response
	// so the browser loader can resolve plugins. Registered before plugin-served
	// routes so it cannot be claimed by a plugin.
	registerPluginRegistryRoute(rt, logger, cfg.PluginHostInstance)
	if cfg.PluginConfig != nil && cfg.PluginHost != nil {
		registerPluginConfig(rt, cfg.PluginConfig)
	}

	// Plugin-served routes (ADR 0007 §4, CW-20260910-0030). Mounted last, so
	// every route this package writes by hand has already claimed its pattern
	// and a plugin cannot take one; the reserved prefix makes that structural
	// rather than a matter of ordering, and the ordering makes it obvious.
	if err := registerPluginRoutes(rt, logger); err != nil {
		return nil, err
	}

	if rt.err != nil {
		return nil, rt.err
	}
	return &Table{routes: rt.routes}, nil
}
