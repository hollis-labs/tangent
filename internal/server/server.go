package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/effect"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/health"
	"github.com/hollis-labs/tangent/internal/participant"
	"github.com/hollis-labs/tangent/internal/pluginhost"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/telemetry"
)

// Config controls Server construction.
type Config struct {
	// Port is the TCP port to listen on (e.g. 7842).
	Port int

	// DevFrontendURL, when non-empty, makes the server reverse-proxy all
	// non-API requests to the given URL (typically a Vite dev server at
	// http://localhost:5173). When empty, the embedded SPA is served.
	DevFrontendURL string

	// Logger receives structured logs. Defaults to slog.Default() when nil.
	Logger *slog.Logger

	// Envelope is the loaded go-envelopes service. PR 2 wires it through
	// without consuming it; PR 3 (MCP) and PR 4 (WebSocket) attach
	// validators and dispatchers to it. New returns an error if Envelope
	// is nil so misconfigured boots fail fast rather than at first
	// envelope.
	Envelope *envelope.Service

	// MCP is the constructed MCP server (PR 3). When non-nil the HTTP
	// server mounts /mcp (streamable HTTP) and /sse (legacy fallback).
	// It's optional in Config so test code can spin up a server without
	// the full MCP wiring; production main always passes a non-nil value.
	MCP MCPServer

	// WSHandler is the constructed WebSocket bridge handler (PR 4).
	// When non-nil the HTTP server mounts /ws. Optional in Config so
	// test code can spin up a server without the full WS wiring;
	// production main always passes a non-nil value.
	WSHandler http.Handler

	// RoomManager is the in-memory multi-room session model (PR 4).
	// When non-nil the HTTP server uses it to validate /r/<roomID>
	// links — unknown ids return 404 immediately, before the SPA
	// loads (which would otherwise show a confusing blank room).
	// Optional in Config; when nil all /r/ requests fall through to
	// the SPA which renders its own "room not found" view client-side.
	RoomManager *room.Manager

	// HITL is the durable operator-owned inbox application service. When set,
	// the server mounts its dedicated browser API and revision event stream;
	// it remains separate from caller-facing MCP and room WebSockets.
	HITL HITLService

	// Turns is the durable agent turns FIFO inbox application service (CW-20260913-0019).
	// When set, the server mounts its dedicated browser and daemon APIs.
	Turns TurnsService

	// Docs is the durable Docs inbox application service (CW-20260917-0009).
	// When set, the server mounts its dedicated browser API and revision
	// event stream. Enqueue happens only through the MCP tool surface, not
	// this HTTP API — Docs has no daemon-loopback caller the way Turns does.
	Docs DocsService

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

	// Participants is the authenticated browser participant session gate.
	//
	// When set, document navigations mint a session, the browser APIs and the
	// /ws upgrade require one, and a room UUID alone grants nothing. Optional
	// in Config so an embedder or a transport-level test can construct a
	// server without a database — production main always passes it, and
	// leaving it nil is what the pre-ADR-0004 behavior was.
	Participants *participant.Gate

	// PluginRoutes are the ADR 0007 §4 plugin-served browser routes
	// (CW-20260910-0030), as the plugin host recorded them at load. When
	// non-empty the server mounts each one under the reserved
	// /api/plugins/ prefix, behind the same guards every other browser API
	// route uses. Empty is the normal state; see plugin_routes.go.
	PluginRoutes []pluginhost.HTTPRoute

	// Telemetry records correlation-bearing observations for the browser
	// transports this package owns: an object-access refusal at the
	// participant gate, and a host-mediated effect the broker declined.
	//
	// Optional, and a nil recorder is a silent no-op rather than a nil
	// dereference. A build with no telemetry serves exactly the same traffic;
	// it just cannot answer what happened afterwards.
	Telemetry *telemetry.Recorder
}

// MCPServer is the minimal contract internal/mcp satisfies. Declared as
// an interface here (rather than importing the package directly) so
// tests can inject a stub and to keep the import graph one-way: mcp
// depends on envelope; server depends on mcp via this interface only.
type MCPServer interface {
	HTTPHandler() http.Handler
	SSEHandler() http.Handler
}

// Server is the Tangent HTTP server. It wraps a *http.Server and the
// route registration; in v0.1 the only handler is the SPA (or its dev
// reverse-proxy).
type Server struct {
	cfg    Config
	logger *slog.Logger
	mux    *http.ServeMux
	httpS  *http.Server

	// envelope holds the registry-backed validation/dispatch service
	// shared with future MCP and WebSocket subsystems.
	envelope *envelope.Service

	// participantRoutes is CW-20260907-0084's enumeration point: every
	// route registerParticipantRoute wired up during New, in the order it
	// was registered. It exists so a check can read what the server
	// actually did rather than a hand-maintained list that drifts the way
	// #37's route did — see ParticipantRoutes.
	participantRoutes []ParticipantRoute
}

// ParticipantRoute names one HTTP route requireParticipant gates, and the
// capability it was registered with.
type ParticipantRoute struct {
	Pattern    string
	Capability authz.Capability
}

// ParticipantRoutes returns every participant-guarded route this Server
// registered, in registration order.
//
// It is CW-20260907-0084's answer to a real bug: PR #37 shipped
// POST /api/channels/{id}/messages gated on authz.Submit, a capability
// ADR 0004 §7's KindParticipant row never holds, so no browser participant
// session could ever have sent a message through it — and every test for
// that route used a fake service with no participant gate configured, so
// nothing could have caught it. A check that reads this list back and
// asserts every capability is one KindParticipant actually holds closes
// that class at test time, for every route registerParticipantRoute wires
// up, present and future, with no per-route test and no hand-maintained
// list to drift.
func (s *Server) ParticipantRoutes() []ParticipantRoute {
	return s.participantRoutes
}

// registerParticipantRoute wires pattern to handler behind the same
// same-origin and participant-capability guards every browser API route
// uses, and records the (pattern, capability) pair on the Server being
// built. This is the one place a route's capability is written down, so it
// and Server.ParticipantRoutes can never drift apart — a route registered
// any other way is, by construction, not a route ParticipantRoutes can see.
func registerParticipantRoute(
	mux *http.ServeMux,
	routes *[]ParticipantRoute,
	cfg Config,
	pattern string,
	capability authz.Capability,
	handler http.HandlerFunc,
) {
	*routes = append(*routes, ParticipantRoute{Pattern: pattern, Capability: capability})
	mux.Handle(pattern, hitlSameOrigin(requireParticipant(cfg.Participants, cfg.Telemetry, capability, handler)))
}

const (
	httpServerWriteTimeout = 60 * time.Second
	httpServerReadTimeout  = 30 * time.Second
)

// New constructs a Server with the embedded-SPA or dev-proxy handler.
//
// cfg.Envelope must be non-nil. The envelope service is constructed at
// startup in cmd/tangent/main.go; passing it through Config keeps the
// server agnostic to manifest-load lifecycle.
func New(cfg Config) (*Server, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Envelope == nil {
		return nil, fmt.Errorf("server: envelope service is required")
	}

	mux := http.NewServeMux()
	registerHealthRoutes(mux, cfg.Health)
	var participantRoutes []ParticipantRoute

	if cfg.MCP != nil {
		// Streamable-HTTP transport (modern MCP clients). The SDK's
		// handler accepts POST + GET on the same URL — clients use POST
		// to send JSON-RPC requests and may GET to subscribe to a
		// session-scoped event stream. We mount it at the path level
		// rather than per-method so both routes resolve to the same
		// handler.
		//
		// The origin guard now covers it. It always permitted header-less
		// non-browser clients, so MCP clients are unaffected; what it stops is
		// a drive-by POST from any page the operator happens to have open,
		// which until now could reach every session_* tool.
		mux.Handle("/mcp", sameOriginGuard(longLivedMCPHandler(cfg.MCP.HTTPHandler())))
		// SSE fallback for legacy Claude Code / older MCP clients. GET
		// opens the long-lived event stream; POST is used for outbound
		// JSON-RPC frames keyed against the streamed session id.
		mux.Handle("/sse", sameOriginGuard(longLivedMCPHandler(cfg.MCP.SSEHandler())))
		// Logged URLs use 127.0.0.1 (matching the actual bind) rather
		// than the human-friendly "localhost" alias — on systems where
		// localhost resolves to ::1 first without an IPv4 fallback, a
		// pasted link would dead-end against an IPv4-only listener.
		logger.Info("MCP server ready",
			"http_url", fmt.Sprintf("http://127.0.0.1:%d/mcp", cfg.Port),
			"sse_url", fmt.Sprintf("http://127.0.0.1:%d/sse", cfg.Port),
		)
	}

	if cfg.WSHandler != nil {
		// /ws upgrades to WebSocket. Mounted before the catch-all so
		// the SPA (or dev proxy) never sees the upgrade request.
		// The upgrade is same-origin guarded here and participant-session
		// guarded inside the handler, before the upgrade, so a refusal is an
		// ordinary 403 rather than a socket that closes for no stated reason.
		mux.Handle("GET /ws", sameOriginGuard(cfg.WSHandler))
		logger.Info("WebSocket bridge ready",
			"ws_url", fmt.Sprintf("ws://127.0.0.1:%d/ws", cfg.Port),
		)
	}

	if cfg.Inbox != nil {
		registerParticipantRoute(mux, &participantRoutes, cfg, "GET /api/inbox", authz.View, inboxHandler(cfg.Inbox))
	}

	if cfg.HITL != nil {
		// Each route names the capability it exercises, so the ADR 0004 §2
		// table is readable straight off the route table: reads need `view`,
		// acknowledging a presented projection needs `draft`, and submitting a
		// terminal response needs `resolve`.
		hitlHandler := newHITLHTTPHandler(cfg.HITL)
		register := func(pattern string, capability authz.Capability, handler http.HandlerFunc) {
			registerParticipantRoute(mux, &participantRoutes, cfg, pattern, capability, handler)
		}
		register("GET /api/hitl", authz.View, hitlHandler.inbox)
		register("GET /api/hitl/events", authz.View, hitlHandler.events)
		register("GET /api/hitl/items/{itemID}", authz.View, hitlHandler.item)
		register("GET /api/hitl/items/{itemID}/evidence/{evidenceIndex}/reference", authz.View, hitlHandler.tangentEvidence)
		register("GET /api/hitl/items/{itemID}/evidence/{evidenceIndex}/preview", authz.View, hitlHandler.artifactPreview)
		register("POST /api/hitl/items/{itemID}/present", authz.Draft, hitlHandler.present)
		register("POST /api/hitl/items/{itemID}/resolve", authz.Resolve, hitlHandler.resolve)
	}

	if cfg.Turns != nil {
		turnsHandler := newTurnsHTTPHandler(cfg.Turns)
		register := func(pattern string, capability authz.Capability, handler http.HandlerFunc) {
			registerParticipantRoute(mux, &participantRoutes, cfg, pattern, capability, handler)
		}
		register("GET /api/turns", authz.View, turnsHandler.inbox)
		register("GET /api/turns/events", authz.View, turnsHandler.events)
		register("GET /api/turns/items/{itemID}", authz.View, turnsHandler.item)
		register("POST /api/turns/items/{itemID}/reply", authz.Resolve, turnsHandler.reply)
		register("POST /api/turns/items/{itemID}/dismiss", authz.Resolve, turnsHandler.dismiss)
		turnsLoopbackOrParticipantRoute(mux, &participantRoutes, cfg, "POST /api/turns/enqueue", authz.Draft, turnsHandler.enqueue)
		turnsLoopbackOrParticipantRoute(mux, &participantRoutes, cfg, "POST /api/turns/items/{itemID}/ack", authz.Resolve, turnsHandler.ack)
		turnsLoopbackOrParticipantRoute(mux, &participantRoutes, cfg, "GET /api/turns/sessions/{sessionID}/replies", authz.View, turnsHandler.sessionReplies)
	}

	if cfg.Docs != nil {
		// "read" is gated on `draft`, not `resolve` — marking a doc read is
		// not a terminal decision, it is the same authoring-weight action as
		// a channel's mark-read route above. Acknowledge and archive both
		// move a doc toward or into a terminal interaction state, so they
		// take `resolve`, matching HITL's resolve and Turns' dismiss/reply.
		docsHandler := newDocsHTTPHandler(cfg.Docs)
		register := func(pattern string, capability authz.Capability, handler http.HandlerFunc) {
			registerParticipantRoute(mux, &participantRoutes, cfg, pattern, capability, handler)
		}
		register("GET /api/docs", authz.View, docsHandler.inbox)
		register("GET /api/docs/events", authz.View, docsHandler.events)
		register("GET /api/docs/items/{itemID}", authz.View, docsHandler.item)
		register("POST /api/docs/items/{itemID}/read", authz.Draft, docsHandler.markRead)
		register("POST /api/docs/items/{itemID}/acknowledge", authz.Resolve, docsHandler.acknowledge)
		register("POST /api/docs/items/{itemID}/archive", authz.Resolve, docsHandler.archive)
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
		registerParticipantRoute(mux, &participantRoutes, cfg, "GET /api/rooms", authz.View, roomHandler.list)
		registerParticipantRoute(mux, &participantRoutes, cfg, "GET /api/rooms/{roomID}", authz.View, roomHandler.inspect)
		registerParticipantRoute(mux, &participantRoutes, cfg, "POST /api/rooms/{roomID}/close", authz.View, roomHandler.close)
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
			registerParticipantRoute(mux, &participantRoutes, cfg, pattern, capability, handler)
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
		registerParticipantRoute(mux, &participantRoutes, cfg, "POST /api/effects", authz.View, effectHandler.request)
	}

	// Plugin registry endpoint (CW-20260911-0035). Serves registry.Response
	// so the browser loader can resolve plugins. Registered before plugin-served
	// routes so it cannot be claimed by a plugin.
	registerPluginRegistryRoute(mux, logger)

	// Plugin-served routes (ADR 0007 §4, CW-20260910-0030). Mounted last, so
	// every route this package writes by hand has already claimed its pattern
	// and a plugin cannot take one; the reserved prefix makes that structural
	// rather than a matter of ordering, and the ordering makes it obvious.
	if err := registerPluginRoutes(mux, &participantRoutes, cfg, logger); err != nil {
		return nil, err
	}

	rootHandler, err := buildRootHandler(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("build root handler: %w", err)
	}

	// /r/{roomID} → SPA. The SPA's React Router picks up the roomID
	// from the path. When a RoomManager is configured we pre-validate
	// the id so unknown rooms get a 404 before the SPA bundle loads;
	// keeps the dev experience honest about ephemeral state.
	if cfg.RoomManager != nil {
		mux.Handle("GET /r/{roomID}", roomFallbackHandler(cfg.RoomManager, rootHandler))
	}
	mux.Handle("/", rootHandler)

	// v0.x is localhost-only by design (concept doc: single-user, no
	// auth, no cloud). Bind explicitly to 127.0.0.1 so a developer
	// running `./tangent` on a coffee-shop wifi isn't inadvertently
	// exposing MCP + WS to the LAN. A future release may add an
	// opt-in BindAll flag; until then the constraint is enforced
	// here rather than relying on docs/firewall hygiene.
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	httpS := &http.Server{
		Addr: addr,
		// Order matters and is the decision, not a detail. Security headers are
		// outermost so nothing below can forget them; minting sits inside the
		// headers and outside the routes so a document navigation has a
		// session before any route reads one; logging stays innermost so it
		// measures the handler rather than the middleware.
		Handler:           securityHeaders(cfg.DevFrontendURL, mintParticipantSessions(cfg.Participants, loggingMiddleware(logger, mux))),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       httpServerReadTimeout,
		WriteTimeout:      httpServerWriteTimeout,
		IdleTimeout:       120 * time.Second,
	}

	return &Server{
		cfg:               cfg,
		logger:            logger,
		mux:               mux,
		httpS:             httpS,
		envelope:          cfg.Envelope,
		participantRoutes: participantRoutes,
	}, nil
}

// longLivedMCPHandler removes both server-wide deadlines for MCP transports.
// Room-backed tools deliberately wait for a human response, and SSE
// subscriptions are likewise long-lived; both may validly outlive the ordinary
// HTTP timeouts. Request-context cancellation remains authoritative.
//
// Clearing the write deadline alone is not enough. A legacy `/sse` session is a
// GET whose request body is never closed by the client, so the server-wide
// ReadTimeout keeps counting against the connection and tears the stream down
// mid-session — the MCP session id then disappears and the next POST answers
// "session not found". Both deadlines must go.
func longLivedMCPHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		controller := http.NewResponseController(w)
		_ = controller.SetWriteDeadline(time.Time{})
		_ = controller.SetReadDeadline(time.Time{})
		next.ServeHTTP(w, r)
	})
}

// Envelope returns the wired envelope service. Exposed so future PRs
// (MCP, WebSocket) can grab it after construction without revisiting
// New's signature.
func (s *Server) Envelope() *envelope.Service { return s.envelope }

// Listen binds the server's TCP socket and returns the listener.
// Callers can fail-fast on bind errors (e.g. EADDRINUSE) before logging
// readiness. Pass the returned listener to Serve.
func (s *Server) Listen() (net.Listener, error) {
	return net.Listen("tcp", s.httpS.Addr)
}

// Serve runs the HTTP server on the given listener. Blocks until the
// listener errors out; callers should treat http.ErrServerClosed as a
// clean shutdown.
func (s *Server) Serve(ln net.Listener) error {
	s.logger.Info("tangent listening",
		"addr", s.httpS.Addr,
		"dev_frontend_url", s.cfg.DevFrontendURL,
	)
	return s.httpS.Serve(ln)
}

// ListenAndServe is a convenience for tests / non-production callers
// that don't need to log readiness only after a successful bind.
// Production main() uses Listen + Serve so bind failures surface before
// the "tangent ready" log line.
func (s *Server) ListenAndServe() error {
	ln, err := s.Listen()
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

// Shutdown gracefully drains in-flight requests with the given timeout.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpS.Shutdown(ctx)
}

// buildRootHandler returns the root handler — either the embedded SPA
// or a reverse proxy to a Vite dev server.
func buildRootHandler(cfg Config, logger *slog.Logger) (http.Handler, error) {
	if cfg.DevFrontendURL == "" {
		return spaHandler(), nil
	}

	target, err := url.Parse(cfg.DevFrontendURL)
	if err != nil {
		return nil, fmt.Errorf("parse dev frontend url: %w", err)
	}
	// url.Parse is lenient: "localhost:5173" parses with scheme="" and
	// host="" (it lands in Path), which would silently produce a broken
	// reverse proxy. Reject anything that isn't an absolute http(s) URL
	// so misconfigured TANGENT_DEV_FRONTEND_URL fails fast.
	if scheme := strings.ToLower(target.Scheme); scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("dev frontend url %q must use http or https scheme", cfg.DevFrontendURL)
	}
	if target.Host == "" {
		return nil, fmt.Errorf("dev frontend url %q must include a host (e.g. http://localhost:5173)", cfg.DevFrontendURL)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		// Connection refused / timeout → frontend dev server isn't up.
		// Surface a clear message rather than the default 502.
		logger.Warn("dev frontend proxy error",
			"url", cfg.DevFrontendURL,
			"path", r.URL.Path,
			"err", err,
		)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "tangent dev: frontend unreachable at "+cfg.DevFrontendURL+"\n")
	}
	return proxy, nil
}

// roomFallbackHandler validates the {roomID} path variable against the
// live room manager and either delegates to the SPA handler (allowing
// the React Router to pick up the route) or returns a 404. We don't
// rewrite the request path — the SPA receives /r/<id> and chooses what
// to render.
func roomFallbackHandler(mgr *room.Manager, fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("roomID")
		if id == "" {
			http.Error(w, "missing roomID", http.StatusBadRequest)
			return
		}
		if _, ok := mgr.Get(id); !ok {
			http.Error(w, "room not found", http.StatusNotFound)
			return
		}
		fallback.ServeHTTP(w, r)
	})
}

// loggingMiddleware emits a single structured log entry per request.
//
// It logs method, path, and duration. It must never be extended to log
// r.URL.RawQuery, the Cookie or Authorization headers, or request bodies. That
// is a maintenance rule, not an observation: the participant session id is the
// only capability material in the system, and this is the one place in the
// process with an obvious temptation to log it (ADR 0004 §6.2).
func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", time.Since(start).String(),
		)
	})
}

// IsClosedErr reports whether err is the sentinel returned by a clean
// http.Server.Shutdown call. Convenience wrapper so cmd/tangent doesn't
// need to import net/http just for the comparison.
func IsClosedErr(err error) bool {
	return errors.Is(err, http.ErrServerClosed)
}
