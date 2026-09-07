package appshell

import (
	"os"
	"path/filepath"
	"testing"
)

// Every test in this file uses LoadPrefsFrom/SavePrefsTo with a path under
// t.TempDir(), never LoadPrefs/SavePrefs, so a test run never touches the
// real os.UserConfigDir()/tangent/shell.json.

func TestLoadPrefsFromCreatesFreshWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "shell.json")
	prefs, err := LoadPrefsFrom(path)
	if err != nil {
		t.Fatalf("LoadPrefsFrom: %v", err)
	}
	if prefs.ClientID == "" {
		t.Error("ClientID is empty; LoadPrefsFrom must never return one")
	}
	if prefs.Window.Width != defaultWindowWidth || prefs.Window.Height != defaultWindowHeight {
		t.Errorf("Window = %+v, want the default geometry", prefs.Window)
	}
	if prefs.Window.Placed {
		t.Error("a freshly minted Prefs must not claim to be Placed")
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("LoadPrefsFrom did not persist the fresh prefs: %v", statErr)
	}
}

// The whole point of ClientID being persisted is that an app restart is a
// reconnect, not a second tab. A restart is a fresh load over the same path.
func TestClientIDSurvivesReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shell.json")
	first, err := LoadPrefsFrom(path)
	if err != nil {
		t.Fatalf("LoadPrefsFrom: %v", err)
	}

	second, err := LoadPrefsFrom(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if second.ClientID != first.ClientID {
		t.Errorf("ClientID changed across reload: %q -> %q", first.ClientID, second.ClientID)
	}
}

func TestWindowGeometrySurvivesReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shell.json")
	first, err := LoadPrefsFrom(path)
	if err != nil {
		t.Fatalf("LoadPrefsFrom: %v", err)
	}
	first.Window = WindowGeometry{Width: 1234, Height: 567, X: 40, Y: 60, Placed: true}
	if err = SavePrefsTo(path, first); err != nil {
		t.Fatalf("SavePrefsTo: %v", err)
	}

	second, err := LoadPrefsFrom(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if second.Window != first.Window {
		t.Errorf("after reload Window = %+v, want %+v", second.Window, first.Window)
	}
}

func TestAlwaysOnTopAndStartAtLoginSurviveReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shell.json")
	first, err := LoadPrefsFrom(path)
	if err != nil {
		t.Fatalf("LoadPrefsFrom: %v", err)
	}
	first.AlwaysOnTop = true
	first.StartAtLogin = true
	if err = SavePrefsTo(path, first); err != nil {
		t.Fatalf("SavePrefsTo: %v", err)
	}

	second, err := LoadPrefsFrom(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !second.AlwaysOnTop {
		t.Error("AlwaysOnTop did not survive reload")
	}
	if !second.StartAtLogin {
		t.Error("StartAtLogin did not survive reload")
	}
}

// A window restored to 12x8, or one sampled mid-teardown at 0x0, is
// unrecoverable by mouse. LoadPrefsFrom must reset it to the default rather
// than hand it to the window constructor.
func TestImplausibleGeometryIsNormalizedOnLoad(t *testing.T) {
	for name, bad := range map[string]WindowGeometry{
		"zero":          {Width: 0, Height: 0, Placed: true},
		"too small":     {Width: 12, Height: 8, Placed: true},
		"too large":     {Width: 99999, Height: 99999, Placed: true},
		"origin absurd": {Width: 900, Height: 600, X: 1000000, Y: 0, Placed: true},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "shell.json")
			seed, err := LoadPrefsFrom(path)
			if err != nil {
				t.Fatalf("LoadPrefsFrom: %v", err)
			}
			seed.Window = bad
			if err = SavePrefsTo(path, seed); err != nil {
				t.Fatalf("SavePrefsTo: %v", err)
			}

			got, err := LoadPrefsFrom(path)
			if err != nil {
				t.Fatalf("reload: %v", err)
			}
			if got.Window.Width != defaultWindowWidth || got.Window.Height != defaultWindowHeight {
				t.Errorf("implausible geometry %+v was not normalized, got %+v", bad, got.Window)
			}
		})
	}
}

func TestCorruptFileResetsToFreshDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shell.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	prefs, err := LoadPrefsFrom(path)
	if err != nil {
		t.Fatalf("LoadPrefsFrom: %v", err)
	}
	if prefs.ClientID == "" {
		t.Error("a corrupt file must still yield a usable ClientID")
	}
	if prefs.Window.Width != defaultWindowWidth {
		t.Errorf("Window.Width = %d, want the default", prefs.Window.Width)
	}
}

func TestSavePrefsToIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shell.json")
	prefs, err := LoadPrefsFrom(path)
	if err != nil {
		t.Fatalf("LoadPrefsFrom: %v", err)
	}
	if err = SavePrefsTo(path, prefs); err != nil {
		t.Fatalf("SavePrefsTo: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "shell.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory holds %v, want only shell.json (temp file left behind)", names)
	}
}

// Wails' set/get asymmetry means the geometry read back after a restore
// differs from what was written by a point or two. Shell.saveGeometry
// checks near() against the last PERSISTED value (the anchor) each launch,
// not against the previous reading — that is what stops the noise from
// compounding across restarts (measured 1100 -> 1099 -> 1097 -> 1096 before
// this gate existed). This simulates ten such launches directly against
// LoadPrefsFrom/SavePrefsTo, the same round trip saveGeometry drives.
func TestReadbackNoiseDoesNotShrinkTheWindowAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shell.json")
	start := WindowGeometry{Width: 1100, Height: 720, X: 200, Y: 130, Placed: true}
	seed, err := LoadPrefsFrom(path)
	if err != nil {
		t.Fatalf("LoadPrefsFrom: %v", err)
	}
	seed.Window = start
	if err = SavePrefsTo(path, seed); err != nil {
		t.Fatalf("SavePrefsTo: %v", err)
	}

	for i := 0; i < 10; i++ {
		anchor, loadErr := LoadPrefsFrom(path)
		if loadErr != nil {
			t.Fatalf("iteration %d: LoadPrefsFrom: %v", i, loadErr)
		}
		reading := anchor.Window
		reading.Width--
		reading.Height--
		if anchor.Window.Placed && anchor.Window.near(reading) {
			continue // mirrors saveGeometry: readback noise, not persisted
		}
		anchor.Window = reading
		if saveErr := SavePrefsTo(path, anchor); saveErr != nil {
			t.Fatalf("iteration %d: SavePrefsTo: %v", i, saveErr)
		}
	}

	final, err := LoadPrefsFrom(path)
	if err != nil {
		t.Fatalf("final LoadPrefsFrom: %v", err)
	}
	if final.Window != start {
		t.Errorf("after ten noisy launches Window = %+v, want %+v", final.Window, start)
	}
}

// A single readback difference of geometryEpsilon or fewer is noise, not a
// resize a person made.
func TestNearAbsorbsASingleReadbackStep(t *testing.T) {
	start := WindowGeometry{Width: 1100, Height: 720, X: 200, Y: 130, Placed: true}
	noisy := start
	noisy.Width -= geometryEpsilon
	noisy.Height -= geometryEpsilon
	if !start.near(noisy) {
		t.Errorf("%+v should be near %+v (epsilon %d)", noisy, start, geometryEpsilon)
	}
}

func TestNearRejectsARealResize(t *testing.T) {
	start := WindowGeometry{Width: 1100, Height: 720, X: 200, Y: 130, Placed: true}
	resized := WindowGeometry{Width: 900, Height: 600, X: 30, Y: 40, Placed: true}
	if start.near(resized) {
		t.Errorf("%+v should not be near %+v — that is a real resize", start, resized)
	}
}

func TestSaneRejectsOutOfRangeDimensions(t *testing.T) {
	for name, g := range map[string]WindowGeometry{
		"too small":  {Width: 100, Height: 100},
		"too large":  {Width: 99999, Height: 600},
		"zero":       {},
		"x overflow": {Width: 900, Height: 600, X: maxWindowDim + 1},
	} {
		t.Run(name, func(t *testing.T) {
			if g.sane() {
				t.Errorf("%+v should not be sane", g)
			}
		})
	}
}

func TestSaneAcceptsOrdinaryGeometry(t *testing.T) {
	g := WindowGeometry{Width: 1100, Height: 780, X: 100, Y: 100, Placed: true}
	if !g.sane() {
		t.Errorf("%+v should be sane", g)
	}
}
