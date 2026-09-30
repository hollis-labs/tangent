package appshell

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// Default window geometry, used on first run and whenever the persisted
// values are missing or implausible. Matches the size shell.go opened the
// window at before this task existed.
const (
	defaultWindowWidth  = 1100
	defaultWindowHeight = 780

	// Guard rails for restored geometry. A window restored to 12x8 because a
	// resize was sampled mid-teardown is unrecoverable by mouse.
	minWindowWidth  = 480
	minWindowHeight = 320
	maxWindowDim    = 20000

	// geometryEpsilon is the smallest change treated as a real move or
	// resize.
	//
	// Wails' set and get are not symmetric on macOS: windowSetSize computes a
	// content size, applies it, and then setFrame:display:animate:YES, while
	// windowGetSize reads the live NSWindow frame. Reading back what was just
	// written therefore returns a value one or two points off. Persisting
	// that difference shrank the saved size on every launch — measured
	// drifting 1100 -> 1099 -> 1097 -> 1096 across three restarts (see
	// Tachyon's internal/shell/prefs.go, where this was first
	// measured; this repo lifts it verbatim).
	//
	// A person resizing a window moves it by more than this; the readback
	// asymmetry never does. The trade is deliberate and it is real: a resize
	// of four points or fewer is discarded.
	//
	// This is one of two guards, and they cover different things. This one
	// covers the asymmetry on a window that is up and being used. Shell's
	// geometryReady (see shell.go) covers the resize and move events a
	// window emits while it is being created, which are not a user's choice
	// at all.
	geometryEpsilon = 4
)

// WindowGeometry is a persisted window size and position.
//
// X and Y are ABSOLUTE screen coordinates, because that is what the window
// creation path consumes: WebviewWindowOptions.X/Y are applied through
// setPosition, which is absolute. Saving WebviewWindow.RelativePosition and
// restoring it as options.X/Y would move the window a little on every
// launch.
//
// The cost of absolute coordinates is that they do not survive a display
// going away; sane() drops an origin that has gone out of range rather than
// restoring the window onto a screen that no longer exists.
type WindowGeometry struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	X      int `json:"x"`
	Y      int `json:"y"`
	// Placed distinguishes "never positioned" from "positioned at 0,0".
	Placed bool `json:"placed"`
}

func defaultWindowGeometry() WindowGeometry {
	return WindowGeometry{Width: defaultWindowWidth, Height: defaultWindowHeight}
}

// sane reports whether g is plausible enough to restore onto the screen.
func (g WindowGeometry) sane() bool {
	if g.Width < minWindowWidth || g.Height < minWindowHeight ||
		g.Width > maxWindowDim || g.Height > maxWindowDim {
		return false
	}
	// Position is absolute screen coordinates, which do not survive a
	// display going away. A wildly out-of-range origin is dropped along with
	// the size rather than restored onto a screen that no longer exists.
	return g.X > -maxWindowDim && g.X < maxWindowDim &&
		g.Y > -maxWindowDim && g.Y < maxWindowDim
}

// near reports whether other is the same geometry as g up to readback noise
// (see geometryEpsilon).
func (g WindowGeometry) near(other WindowGeometry) bool {
	within := func(a, b int) bool {
		d := a - b
		if d < 0 {
			d = -d
		}
		return d <= geometryEpsilon
	}
	return within(g.Width, other.Width) && within(g.Height, other.Height) &&
		within(g.X, other.X) && within(g.Y, other.Y)
}

// Prefs is the app shell's persisted state, at
// os.UserConfigDir()/tangent/shell.json — NOT in tangent.db, which carries
// immutability triggers and retention policy that window chrome has no
// business fighting. A caller can override the path (LoadPrefsFrom /
// SavePrefsTo); Shell does so only from Config.PrefsPath, which is empty in
// production and set only by tests.
//
// Every guard this file carries is lifted from
// Tachyon's internal/shell/prefs.go, not re-derived.
type Prefs struct {
	// ClientID is generated once, on first run, and never changes. It is
	// deliberately not derived from anything the OS can hand out differently
	// across restarts (a PID, a boot id): the whole point is that it
	// survives them. Handed to the SPA so its WebSocket attach carries a
	// stable ClientID — that is what makes an app restart a reconnect
	// (inherits the resolver lease) rather than a second tab (attaches as
	// observer). See internal/room/connection.go and
	// AttachOptions.ClientID.
	ClientID string `json:"clientId"`

	// Window is the last-known size and position, restored on launch.
	Window WindowGeometry `json:"window"`

	// AlwaysOnTop persists the window level toggle (tray menu item and
	// Cmd+Shift+T, wired by CW-20260905-0030). Deliberately just a level,
	// not CollectionBehavior: this window does not follow the user onto a
	// fullscreen Space (see shell.go's window construction for why).
	AlwaysOnTop bool `json:"alwaysOnTop"`

	// StartAtLogin persists whether the LaunchAgent installed by
	// CW-20260905-0031 is active. The shell does not act on this field
	// itself — it is the record a launch agent toggle reads and writes so
	// the tray checkbox reflects reality after a restart.
	StartAtLogin bool `json:"startAtLogin"`
}

// defaultPrefsPath is os.UserConfigDir()/tangent/shell.json, computed once
// per call so a test can override HOME/XDG_CONFIG_HOME and see it take
// effect. Named distinctly from the local "prefsPath" variables callers
// tend to hold so the two never shadow each other.
func defaultPrefsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("appshell: locate user config dir: %w", err)
	}
	return filepath.Join(dir, "tangent", "shell.json"), nil
}

// LoadPrefs reads the persisted prefs from the default path. See
// LoadPrefsFrom for the loading rules.
func LoadPrefs() (Prefs, error) {
	path, err := defaultPrefsPath()
	if err != nil {
		return Prefs{}, err
	}
	return LoadPrefsFrom(path)
}

// LoadPrefsFrom reads the persisted prefs at path, creating a fresh set
// (with a newly minted ClientID and default window geometry) if none exist
// yet. It never returns a Prefs with an empty ClientID or insane window
// geometry: a caller that gets one back can hand it to the SPA and the
// window constructor without checking either.
//
// Tests pass their own path (via t.TempDir()) so a test run never touches
// the real os.UserConfigDir()/tangent/shell.json — see prefs_test.go.
func LoadPrefsFrom(path string) (Prefs, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is caller-controlled config, not user input
	switch {
	case os.IsNotExist(err):
		fresh := freshPrefs()
		return fresh, SavePrefsTo(path, fresh)
	case err != nil:
		return Prefs{}, fmt.Errorf("appshell: read prefs %q: %w", path, err)
	}

	var prefs Prefs
	if err := json.Unmarshal(raw, &prefs); err != nil {
		// A corrupt prefs file is not a reason to refuse to launch — the
		// whole file is one shell's worth of UI convenience, and losing it
		// costs a stale window position and a one-time non-reconnect, not
		// data. Mint a fresh set and overwrite it, the same as first run.
		fresh := freshPrefs()
		return fresh, SavePrefsTo(path, fresh)
	}

	dirty := false
	if prefs.ClientID == "" {
		prefs.ClientID = newClientID()
		dirty = true
	}
	if !prefs.Window.sane() {
		prefs.Window = defaultWindowGeometry()
		dirty = true
	}
	if dirty {
		return prefs, SavePrefsTo(path, prefs)
	}
	return prefs, nil
}

// freshPrefs is what a first run, or a corrupt/absent prefs file, resets to.
func freshPrefs() Prefs {
	return Prefs{ClientID: newClientID(), Window: defaultWindowGeometry()}
}

// SavePrefs writes prefs to the default path.
func SavePrefs(prefs Prefs) error {
	path, err := defaultPrefsPath()
	if err != nil {
		return err
	}
	return SavePrefsTo(path, prefs)
}

// SavePrefsTo writes prefs to path atomically, creating the parent
// directory if needed.
//
// The write goes through a temp file and rename: a resize's debounced
// geometry save can fire dozens of times over a session, and a crash
// between truncate and write would otherwise leave a file LoadPrefsFrom
// cannot parse, which it would then silently reset to defaults, discarding
// ClientID along with it.
func SavePrefsTo(path string, prefs Prefs) error {
	dir := filepath.Dir(path)
	if mkdirErr := os.MkdirAll(dir, 0o750); mkdirErr != nil {
		return fmt.Errorf("appshell: create prefs directory: %w", mkdirErr)
	}
	encoded, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		return fmt.Errorf("appshell: encode prefs: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".shell-*.json")
	if err != nil {
		return fmt.Errorf("appshell: create temp prefs file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, writeErr := tmp.Write(encoded); writeErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("appshell: write temp prefs file %q: %w", tmpName, writeErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return fmt.Errorf("appshell: close temp prefs file %q: %w", tmpName, closeErr)
	}
	if renameErr := os.Rename(tmpName, path); renameErr != nil {
		return fmt.Errorf("appshell: rename into %q: %w", path, renameErr)
	}
	return nil
}

func newClientID() string {
	return "desktop-" + uuid.NewString()
}
