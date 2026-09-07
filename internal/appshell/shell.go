// Package appshell is the Tangent desktop window: adopt-or-boot against a
// running or absent server, and the Wails v3 webview pointed at it. See
// docs/adr/0006 (once CW-20260905-0051 lands) and
// ~/dev/agent-os/workspaces/drafts/tangent/desktop-shell-architecture.md for
// the design this implements.
//
// Wails is a dependency of this package and cmd/tangent-app only. Nothing in
// internal/server, internal/mcp, or the service layer learns it exists.
package appshell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/hollis-labs/tangent/internal/boot"
	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/server"
)

// healthProbeTimeout matches cmd/tangent/maintenance.go's own probeServing:
// the same question (is something already answering on this loopback port)
// deserves the same patience.
const healthProbeTimeout = 750 * time.Millisecond

// bootWaitTimeout bounds how long the window waits for a just-booted server
// to answer healthy before opening anyway. boot.Boot's own construction
// (migrations, the service graph, room hydration) is normally well under
// this; a build that regresses past it should still open a window that
// shows the operator a connection error, not hang indefinitely.
const bootWaitTimeout = 5 * time.Second

// Config is everything the shell needs to probe, adopt-or-boot, and open the
// window. It embeds boot.Config verbatim: the boot half of adopt-or-boot is
// exactly the CLI's own Boot call, same Config shape, same Port/DBPath the
// probe and the window URL are built from.
type Config struct {
	boot.Config

	// PrefsPath overrides where shell.json is read and written. Empty means
	// the default (os.UserConfigDir()/tangent/shell.json). Tests set it;
	// cmd/tangent-app does not.
	PrefsPath string
}

// Shell owns the adopt-or-boot decision, the constructed Wails application,
// and — only when this process booted rather than adopted — the server and
// its closer.
type Shell struct {
	app    *application.App
	window *application.WebviewWindow
	logger *slog.Logger

	// booted is true when this process called boot.Boot and therefore owns
	// the server's lifecycle. An adopted daemon must outlive the window, so
	// OnShutdown does nothing when this is false.
	booted bool
	srv    *server.Server
	closer io.Closer

	// prefsPath and prefs back window geometry and always-on-top
	// persistence. prefs is guarded by prefsMu because SetAlwaysOnTop (the
	// tray toggle CW-20260905-0030 wires) and the geometry-save goroutine
	// both mutate it.
	prefsPath string
	prefsMu   sync.Mutex
	prefs     Prefs

	// geometryReady gates geometry persistence against the resize/move
	// events a window emits while it is being created and laid out;
	// persisting one of those would write a frame the user never chose over
	// the frame they did. This is a second, different guard from
	// WindowGeometry's geometryEpsilon: that one covers readback asymmetry
	// on a window that is already up and being used, this one covers
	// creation-time events that are not a user's choice at all. It is set
	// once, the first time the window actually shows — see
	// wireGeometryPersistence, which sets it in the same step it measures
	// heightChrome.
	geometryReady atomic.Bool

	// chromeMeasured guards heightChrome against being (re-)measured more
	// than once. It is a separate flag from geometryReady, set first, so
	// heightChrome is always valid by the time anything observes
	// geometryReady true.
	chromeMeasured atomic.Bool

	// heightChrome is the gap between the FRAME height WebviewWindow.Size
	// reports and the CONTENT height the window was actually created with.
	//
	// WebviewWindowOptions.Height is a content height — macOS creates the
	// window via initWithContentRect — but Size() always reads back the
	// live NSWindow frame, which is taller by the native title bar's
	// height. WebviewWindow.GetBorderSizes exists to answer exactly this
	// but is a stub returning zero on the darwin backend (see
	// webview_window_darwin.go), so there is no query for it; this measures
	// it empirically instead, once, from the delta between what this
	// session requested at creation and what Size() reports the first time
	// the window shows.
	//
	// Left uncorrected this is a measured, unbounded, compounding bug, not
	// mere readback noise: create with content height H, Size() reports
	// H+27 (macOS' default title bar, measured on this build), persist
	// H+27 as the next launch's requested content height, which then
	// reports H+54, then H+81, growing by a title bar's worth on every
	// single restart until sane() eventually rejects it. geometryEpsilon
	// does not catch this because 27 points is nowhere near noise — the
	// anchor-vs-reading comparison in saveGeometry just accepts each larger
	// value as a real resize and keeps re-anchoring higher.
	//
	// Subtracting heightChrome from every Size() reading before it is
	// compared or persisted converts it back into the same content-height
	// units WebviewWindowOptions.Height consumes, which is what makes
	// save-then-restore idempotent. Measuring it fresh each session (rather
	// than deriving or persisting it) also means a title bar height that
	// changes with the OS, accessibility text size, or window style is
	// absorbed automatically rather than baked in.
	//
	// Width carries no equivalent: macOS' title bar occupies vertical
	// space only, and Position/SetPosition operate on the frame origin
	// both ways, so there is nothing analogous to compensate for there.
	heightChrome atomic.Int64

	// geometryDirty coalesces a drag's burst of resize/move events into one
	// pending save. Capacity 1.
	geometryDirty chan struct{}
}

// New decides adopt-or-boot, opens the window, and returns a Shell ready to
// Run. It does not block.
//
// Three outcomes:
//   - The port answers healthy: adopt. No flock, no boot.Boot call at all.
//   - The port answers nothing: boot.Boot in-process, serve, wait for
//     healthy, then open the window on top of what this process just
//     started.
//   - The port answers nothing AND boot.Boot's AcquireOwnership refuses
//     (*tangentdb.OwnershipConflict): something else holds the database
//     without serving here — a maintenance one-shot, or a server on a
//     different port. That is reported distinctly and New returns an error;
//     it is not a case to loop or guess through.
func New(cfg Config) (*Shell, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	shell := &Shell{logger: logger, geometryDirty: make(chan struct{}, 1)}

	if probeHealthy(cfg.Port) {
		logger.Info("tangent-app: adopting a healthy server", "port", cfg.Port)
	} else if err := shell.bootInProcess(cfg.Config); err != nil {
		var conflict *tangentdb.OwnershipConflict
		if errors.As(err, &conflict) {
			return nil, fmt.Errorf(
				"nothing answered http://127.0.0.1:%d/healthz, and the database is already held (not by a server on this port): %w",
				cfg.Port, conflict)
		}
		return nil, fmt.Errorf("boot: %w", err)
	}

	prefsPath := cfg.PrefsPath
	if prefsPath == "" {
		var pathErr error
		prefsPath, pathErr = defaultPrefsPath()
		if pathErr != nil {
			shell.stopIfBooted()
			return nil, fmt.Errorf("locate prefs path: %w", pathErr)
		}
	}
	prefs, err := LoadPrefsFrom(prefsPath)
	if err != nil {
		shell.stopIfBooted()
		return nil, fmt.Errorf("load prefs: %w", err)
	}
	shell.prefsPath = prefsPath
	shell.prefs = prefs

	app := application.New(application.Options{
		Name: "Tangent",
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyRegular,
		},
	})
	shell.app = app

	// clientId and clientKind reach the SPA as a query param on the window
	// URL rather than a Wails runtime bridge call: CW-20260905-0025 found
	// that an external-URL window gets no runtime injection, so there is no
	// bridge to call. ws-client.ts's getTabClientID reads clientId and
	// persists it into the same sessionStorage slot a plain tab uses, which
	// is what makes a relaunch a reconnect (inherits the resolver lease)
	// rather than a second tab.
	windowURL := fmt.Sprintf("http://127.0.0.1:%d/?clientId=%s&clientKind=desktop", cfg.Port, prefs.ClientID)
	windowOpts := application.WebviewWindowOptions{
		Name:        "main",
		Title:       "Tangent",
		Width:       prefs.Window.Width,
		Height:      prefs.Window.Height,
		URL:         windowURL,
		AlwaysOnTop: prefs.AlwaysOnTop,
		// Deliberately no Mac.CollectionBehavior: this is an ordinary tool
		// window a person switches to via Cmd+Tab or the Dock, not a
		// summoned utility. It follows the same Space rules as any other
		// window and does not carry over onto a fullscreen app's Space. If
		// that judgment call is wrong for how Tangent gets used day to day,
		// revisit alongside AlwaysOnTop — that is a window *level*, whereas
		// following a fullscreen Space needs CanJoinAllSpaces |
		// FullScreenAuxiliary in CollectionBehavior, a different mechanism.
	}
	if prefs.Window.Placed {
		// WindowXY, not the zero value: WindowCentered IS 0, so a literal 0
		// here would silently center the window and throw the saved
		// position away.
		windowOpts.InitialPosition = application.WindowXY
		windowOpts.X, windowOpts.Y = prefs.Window.X, prefs.Window.Y
	}
	window := app.Window.NewWithOptions(windowOpts)
	shell.window = window
	shell.wireGeometryPersistence(window, windowOpts.Height)

	app.OnShutdown(func() {
		// A resize or move that happens seconds before quit would otherwise
		// be lost with the pending debounce in persistGeometryLoop.
		shell.saveGeometry()
		shell.stopIfBooted()
	})

	return shell, nil
}

// wireGeometryPersistence marks the geometry dirty on every resize/move and
// starts the goroutine that debounces those into single writes. It also
// measures heightChrome and flips geometryReady the first time the window
// actually shows: together they exclude the resize/move events macOS emits
// while laying the window out (geometryReady) and convert every later
// Size() reading back into the content-height units the window was created
// with (heightChrome) — see the Shell field docs for why both exist.
// requestedContentHeight is the Height this session actually asked
// WebviewWindowOptions for.
func (s *Shell) wireGeometryPersistence(window *application.WebviewWindow, requestedContentHeight int) {
	window.OnWindowEvent(events.Common.WindowShow, func(*application.WindowEvent) {
		if !s.chromeMeasured.CompareAndSwap(false, true) {
			return
		}
		_, frameHeight := window.Size()
		s.heightChrome.Store(int64(frameHeight - requestedContentHeight))
		s.geometryReady.Store(true)
	})
	mark := func(*application.WindowEvent) {
		select {
		case s.geometryDirty <- struct{}{}:
		default:
		}
	}
	window.OnWindowEvent(events.Common.WindowDidResize, mark)
	window.OnWindowEvent(events.Common.WindowDidMove, mark)
	go s.persistGeometryLoop()
}

// persistGeometryLoop debounces a burst of resize/move events (a drag fires
// many) into one write, 500ms after the burst settles.
func (s *Shell) persistGeometryLoop() {
	const settle = 500 * time.Millisecond
	for range s.geometryDirty {
		time.Sleep(settle)
		select {
		case <-s.geometryDirty:
		default:
		}
		s.saveGeometry()
	}
}

// saveGeometry reads the window's current size and position and persists
// them, subject to the creation-time gate, the sanity check, and the
// readback-noise epsilon. Safe to call from any goroutine: Size and
// Position dispatch onto the main thread internally.
func (s *Shell) saveGeometry() {
	if !s.geometryReady.Load() {
		return
	}
	w, h := s.window.Size()
	h -= int(s.heightChrome.Load())
	x, y := s.window.Position()
	g := WindowGeometry{Width: w, Height: h, X: x, Y: y, Placed: true}
	if !g.sane() {
		return
	}

	s.prefsMu.Lock()
	if s.prefs.Window.Placed && s.prefs.Window.near(g) {
		s.prefsMu.Unlock()
		return
	}
	s.prefs.Window = g
	next := s.prefs
	s.prefsMu.Unlock()

	if err := SavePrefsTo(s.prefsPath, next); err != nil {
		s.logger.Error("tangent-app: save window geometry", "error", err)
	}
}

// AlwaysOnTop reports the persisted always-on-top state.
func (s *Shell) AlwaysOnTop() bool {
	s.prefsMu.Lock()
	defer s.prefsMu.Unlock()
	return s.prefs.AlwaysOnTop
}

// SetAlwaysOnTop updates the window's level and persists the choice.
// CW-20260905-0030 calls this from the tray menu item and Cmd+Shift+T.
func (s *Shell) SetAlwaysOnTop(v bool) error {
	s.window.SetAlwaysOnTop(v)

	s.prefsMu.Lock()
	s.prefs.AlwaysOnTop = v
	next := s.prefs
	s.prefsMu.Unlock()

	return SavePrefsTo(s.prefsPath, next)
}

// Run blocks until the app quits.
func (s *Shell) Run() error {
	return s.app.Run()
}

// bootInProcess is the boot half of adopt-or-boot: construct and serve, then
// wait for the listener to actually answer before returning, so the window
// this caller is about to open does not race a server that has not started
// accepting connections yet.
func (s *Shell) bootInProcess(cfg boot.Config) error {
	_, srv, closer, err := boot.Boot(cfg)
	if err != nil {
		return err
	}
	ln, err := srv.Listen()
	if err != nil {
		_ = closer.Close()
		return fmt.Errorf("listen: %w", err)
	}
	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			s.logger.Error("tangent-app: serve", "error", serveErr)
		}
	}()
	if !waitHealthy(cfg.Port, bootWaitTimeout) {
		s.logger.Warn("tangent-app: booted server did not answer healthy within the wait window; opening the window anyway")
	}
	s.booted = true
	s.srv = srv
	s.closer = closer
	s.logger.Info("tangent-app: booted a new server", "port", cfg.Port)
	return nil
}

// stopIfBooted shuts the server down and releases ownership when this
// process booted it. It is a no-op for an adopted daemon, which must outlive
// the window, and safe to call more than once.
func (s *Shell) stopIfBooted() {
	if !s.booted {
		return
	}
	s.booted = false
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		s.logger.Error("tangent-app: shutdown", "error", err)
	}
	if err := s.closer.Close(); err != nil {
		s.logger.Error("tangent-app: release database ownership", "error", err)
	}
}

// probeHealthy asks /healthz — liveness, not readiness. Adopt-or-boot only
// needs to know a Tangent is already serving this port; a process that is
// alive but not yet ready is still a process a second boot would collide
// with via the flock, so it is still a reason to adopt rather than boot.
func probeHealthy(port int) bool {
	client := &http.Client{Timeout: healthProbeTimeout}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port)) //nolint:noctx // bounded by the client timeout
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

func waitHealthy(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if probeHealthy(port) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
