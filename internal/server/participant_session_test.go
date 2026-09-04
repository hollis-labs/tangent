package server_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/participant"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/server"
	tangentws "github.com/hollis-labs/tangent/internal/ws"
)

// This file is the transport-level proof of ADR 0004's central claim: a room
// UUID is a locator, not a credential.
//
// Every assertion here failed before CW-20260825-0075. `/ws?roomID=` plus a
// known id was sufficient to attach, view, answer, and cancel; `/api/hitl`
// required only that the request look same-origin; `/mcp` had no origin guard
// at all; and no response carried a security header.

// TestKnowingARoomUUIDGrantsNothing is acceptance criterion 1.
//
// The room id used here is not guessed — the test creates the room and knows
// its exact id, which is the strongest form of the attacker's position and
// exactly the position an agent transcript, a log line, or a screen share puts
// someone in.
func TestKnowingARoomUUIDGrantsNothing(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	roomID := app.manager.Create(map[string]string{"title": "Known room"}).ID

	// No cookie: the WebSocket upgrade is refused before it becomes a socket,
	// as an ordinary 403 rather than a connection that closes without saying
	// why.
	if _, _, err := websocket.Dial(app.ctx(t), app.wsURL(roomID), nil); err == nil {
		t.Fatal("the /ws upgrade succeeded without a participant session")
	}
	if app.manager.Len() == 0 {
		t.Fatal("the room disappeared")
	}
	if rm, _ := app.manager.Get(roomID); rm.ConnectionCount() != 0 {
		t.Fatal("a refused upgrade attached a connection")
	}

	// No cookie: the browser room API refuses every verb.
	for _, probe := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/rooms"},
		{http.MethodGet, "/api/rooms/" + roomID},
		{http.MethodPost, "/api/rooms/" + roomID + "/close"},
		{http.MethodGet, "/api/hitl"},
	} {
		response := app.do(t, probe.method, probe.path, nil)
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("%s %s without a session = %d, want 403",
				probe.method, probe.path, response.StatusCode)
		}
		_ = response.Body.Close()
	}
}

// TestOpeningAnyTangentURLMintsASessionAndGrantsTheLocalOperatorSet is
// acceptance criterion 4: the user still just opens the URL and answers, and
// nothing new appears on any tool argument.
func TestOpeningAnyTangentURLMintsASessionAndGrantsTheLocalOperatorSet(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	roomID := app.manager.Create(map[string]string{"title": "Ergonomic room"}).ID

	cookie := app.openDocument(t, "/r/"+roomID)
	if cookie == nil {
		t.Fatal("opening a room URL did not mint a participant session")
	}
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatalf("session cookie = %+v, want HttpOnly SameSite=Lax Path=/", cookie)
	}
	if cookie.Secure {
		t.Fatal("a loopback HTTP cookie cannot be Secure")
	}
	if cookie.Domain != "" {
		t.Fatal("the session cookie must not carry a Domain attribute")
	}

	// The same cookie now carries the whole default participant set: attach,
	// read the room, read the inbox.
	conn, _, err := websocket.Dial(app.ctx(t), app.wsURL(roomID),
		&websocket.DialOptions{HTTPHeader: http.Header{"Cookie": []string{cookie.String()}}})
	if err != nil {
		t.Fatalf("a minted session could not attach: %v", err)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "done")

	for _, path := range []string{"/api/rooms", "/api/rooms/" + roomID, "/api/hitl"} {
		response := app.do(t, http.MethodGet, path, cookie)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s with a minted session = %d, want 200", path, response.StatusCode)
		}
		_ = response.Body.Close()
	}

	// A second browser gets its own session rather than inheriting the first
	// one's, which is what makes the audit trail per-browser.
	second := app.openDocument(t, "/")
	if second == nil || second.Value == cookie.Value {
		t.Fatal("a second browser did not receive its own session")
	}

	// Revocation is an explicit operator act, and it takes effect immediately.
	if _, err := app.sessions.RevokeAll(context.Background(), "operator-revoked"); err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}
	response := app.do(t, http.MethodGet, "/api/rooms", cookie)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("a revoked session still worked: %d", response.StatusCode)
	}
	_ = response.Body.Close()
	// ...and reloading the page is the whole recovery: the stale cookie is
	// cleared and a fresh session issued.
	if reminted := app.openDocumentWithCookie(t, "/", cookie); reminted == nil || reminted.Value == cookie.Value {
		t.Fatal("reloading after revocation did not mint a fresh session")
	}
}

// TestCapabilityMaterialNeverReachesALogLineOrADurableRow is acceptance
// criterion 3.
//
// The session id is the only capability material in the system. This asserts
// it against the two channels ADR 0004 §6 names as the tempting ones — the
// request log, which already logs a path for every request, and durable
// storage.
func TestCapabilityMaterialNeverReachesALogLineOrADurableRow(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	roomID := app.manager.Create(map[string]string{"title": "Logged room"}).ID

	cookie := app.openDocument(t, "/r/"+roomID)
	if cookie == nil {
		t.Fatal("no session was minted")
	}
	// Exercise every route that sees the cookie, so the log has something to
	// leak if it were going to.
	for _, path := range []string{"/api/rooms", "/api/rooms/" + roomID, "/api/hitl", "/healthz"} {
		_ = app.do(t, http.MethodGet, path, cookie).Body.Close()
	}
	conn, _, err := websocket.Dial(app.ctx(t), app.wsURL(roomID),
		&websocket.DialOptions{HTTPHeader: http.Header{"Cookie": []string{cookie.String()}}})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "done")

	if logged := app.logs.String(); strings.Contains(logged, cookie.Value) {
		t.Fatal("the session cookie reached a log line")
	}
	assertNoDatabaseCell(t, app.database, cookie.Value)
}

// TestEveryResponseCarriesTheProcessWideSecurityHeaders covers ADR 0004 §6.3.
// The headers are applied in the middleware that wraps the mux, so a route
// added later cannot forget them — which is the property being asserted.
func TestEveryResponseCarriesTheProcessWideSecurityHeaders(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	roomID := app.manager.Create(nil).ID
	cookie := app.openDocument(t, "/")

	for _, path := range []string{
		"/healthz", "/", "/r/" + roomID, "/api/rooms", "/api/hitl", "/api/rooms/" + roomID,
	} {
		response := app.do(t, http.MethodGet, path, cookie)
		if got := response.Header.Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("%s Referrer-Policy = %q, want no-referrer", path, got)
		}
		if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s X-Content-Type-Options = %q, want nosniff", path, got)
		}
		_ = response.Body.Close()
	}
}

// TestCrossSiteRequestsAreRefusedOnEveryPrivilegedRoute covers extending the
// same-origin guard to /mcp, /sse, and /ws (ADR 0004 §4.5).
//
// The guard permits header-less non-browser clients, which is what keeps MCP
// clients working; what it refuses is a browser that explicitly says it is
// cross-site.
func TestCrossSiteRequestsAreRefusedOnEveryPrivilegedRoute(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	cookie := app.openDocument(t, "/")

	crossSite := http.Header{
		"Sec-Fetch-Site": []string{"cross-site"},
		"Origin":         []string{"http://evil.example"},
	}
	for _, probe := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/mcp"},
		{http.MethodGet, "/sse"},
		{http.MethodGet, "/api/rooms"},
		{http.MethodGet, "/api/hitl"},
	} {
		response := app.doWithHeaders(t, probe.method, probe.path, cookie, crossSite)
		if response.StatusCode != http.StatusForbidden {
			t.Errorf("cross-site %s %s = %d, want 403", probe.method, probe.path, response.StatusCode)
		}
		_ = response.Body.Close()
	}

	// A header-less local client — every MCP client — is unaffected.
	response := app.do(t, http.MethodPost, "/mcp", nil)
	if response.StatusCode == http.StatusForbidden {
		t.Error("the origin guard refused a header-less local MCP client")
	}
	_ = response.Body.Close()
}

// guardedApp is a fully wired Tangent process: real database, real MCP server,
// real room manager, real participant sessions.
type guardedApp struct {
	database *sql.DB
	sessions *participant.Store
	manager  *room.Manager
	server   *server.Server
	listener net.Listener
	baseURL  string
	logs     *lockedBuffer
	done     chan error
}

func startGuardedApp(t *testing.T) *guardedApp {
	t.Helper()
	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "guarded.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrateErr := tangentdb.RunMigrations(database); migrateErr != nil {
		t.Fatalf("run migrations: %v", migrateErr)
	}

	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if registerErr := extensions.RegisterAll(envelopeService); registerErr != nil {
		t.Fatalf("extensions.RegisterAll: %v", registerErr)
	}
	interactions, err := interaction.NewService(
		interaction.NewStore(database),
		interaction.NewEnvelopeDefinitionCatalog(envelopeService, tangentmcp.HostVersion),
		interaction.WithAwaitPollInterval(time.Millisecond),
		interaction.WithSurfaceAccessPolicy(hitl.SurfaceAccessPolicy{}),
	)
	if err != nil {
		t.Fatalf("interaction.NewService: %v", err)
	}
	hitlService, err := hitl.NewService(interactions)
	if err != nil {
		t.Fatalf("hitl.NewService: %v", err)
	}
	manager := room.NewManager(database)
	if hydrateErr := manager.Hydrate(context.Background()); hydrateErr != nil {
		t.Fatalf("room.Hydrate: %v", hydrateErr)
	}
	mcpServer, err := tangentmcp.New(
		envelopeService, envelope.NewDispatcher(envelopeService), manager, "",
		tangentmcp.WithInteractionService(interactions), tangentmcp.WithHITLService(hitlService),
	)
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}
	sessions, err := participant.NewStore(database)
	if err != nil {
		t.Fatalf("participant.NewStore: %v", err)
	}
	gate, err := participant.NewGate(sessions)
	if err != nil {
		t.Fatalf("participant.NewGate: %v", err)
	}
	wsHandler := tangentws.New(manager, slog.New(slog.NewTextHandler(io.Discard, nil)))
	wsHandler.SetParticipantResolver(gate.ResolveBinding)

	logs := &lockedBuffer{}
	httpServer, err := server.New(server.Config{
		Port: 0, Logger: slog.New(slog.NewTextHandler(logs, nil)),
		Envelope: envelopeService, MCP: mcpServer, WSHandler: wsHandler,
		RoomManager: manager, HITL: hitlService, Rooms: mcpServer, Participants: gate,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	listener, err := httpServer.Listen()
	if err != nil {
		t.Fatalf("server.Listen: %v", err)
	}
	app := &guardedApp{
		database: database, sessions: sessions, manager: manager, server: httpServer,
		listener: listener, baseURL: "http://" + listener.Addr().String(),
		logs: logs, done: make(chan error, 1),
	}
	go func() { app.done <- httpServer.Serve(listener) }()
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		<-app.done
	})
	return app
}

func (app *guardedApp) ctx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func (app *guardedApp) wsURL(roomID string) string {
	return "ws://" + app.listener.Addr().String() + "/ws?roomID=" + url.QueryEscape(roomID)
}

// openDocument performs the navigation a browser makes when a user opens a
// Tangent link, and returns the session cookie the server set, if any.
func (app *guardedApp) openDocument(t *testing.T, path string) *http.Cookie {
	t.Helper()
	return app.openDocumentWithCookie(t, path, nil)
}

func (app *guardedApp) openDocumentWithCookie(t *testing.T, path string, existing *http.Cookie) *http.Cookie {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, app.baseURL+path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	request.Header.Set("Sec-Fetch-Dest", "document")
	request.Header.Set("Sec-Fetch-Site", "none")
	if existing != nil {
		request.AddCookie(existing)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("navigate to %s: %v", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	for _, cookie := range response.Cookies() {
		if cookie.Name == participant.CookieName && cookie.Value != "" {
			return cookie
		}
	}
	return nil
}

func (app *guardedApp) do(t *testing.T, method, path string, cookie *http.Cookie) *http.Response {
	t.Helper()
	return app.doWithHeaders(t, method, path, cookie, nil)
}

func (app *guardedApp) doWithHeaders(
	t *testing.T,
	method, path string,
	cookie *http.Cookie,
	headers http.Header,
) *http.Response {
	t.Helper()
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader("{}")
	}
	request, err := http.NewRequest(method, app.baseURL+path, body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return response
}

// assertNoDatabaseCell sweeps every table for a literal value.
func assertNoDatabaseCell(t *testing.T, database *sql.DB, value string) {
	t.Helper()
	rows, err := database.Query(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	names := make([]string, 0, 32)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			t.Fatalf("scan table name: %v", err)
		}
		names = append(names, name)
	}
	_ = rows.Close()
	for _, name := range names {
		// The table name comes from sqlite_master, not from any input; there
		// is no parameter form for an identifier.
		//nolint:gosec // G202: identifier from sqlite_master, not user input
		tableRows, err := database.Query(`SELECT * FROM "` + name + `"`)
		if err != nil {
			t.Fatalf("scan %s: %v", name, err)
		}
		columns, err := tableRows.Columns()
		if err != nil {
			_ = tableRows.Close()
			t.Fatalf("columns of %s: %v", name, err)
		}
		for tableRows.Next() {
			cells := make([]any, len(columns))
			targets := make([]any, len(columns))
			for index := range cells {
				targets[index] = &cells[index]
			}
			if err := tableRows.Scan(targets...); err != nil {
				_ = tableRows.Close()
				t.Fatalf("scan row of %s: %v", name, err)
			}
			for index, cell := range cells {
				var text string
				switch typed := cell.(type) {
				case string:
					text = typed
				case []byte:
					text = string(typed)
				default:
					continue
				}
				if strings.Contains(text, value) {
					_ = tableRows.Close()
					t.Fatalf("capability material found in %s.%s", name, columns[index])
				}
			}
		}
		_ = tableRows.Close()
	}
}
