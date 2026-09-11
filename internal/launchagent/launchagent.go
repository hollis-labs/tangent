// Package launchagent writes, validates, installs, and removes the macOS
// LaunchAgent that starts the headless Tangent daemon at login
// (CW-20260905-0031).
//
// Shape, and the decisions behind it (personal MVP plan CW-20260907-0014,
// decisions 1 and 3):
//
//   - The agent runs the headless `tangent` daemon, never the app. A window at
//     login is unwanted; the app adopts the running daemon when the user opens
//     it. launchd is the stable launch authority; Cerberus keeps only the dev
//     instance.
//   - RunAtLoad true, KeepAlive false. Quit means quit; launchd does not fight
//     a deliberate stop. Revisit if crashes turn out to matter.
//   - `launchctl bootstrap` / `bootout` (the modern verbs), never `load` /
//     `unload`.
//   - The plist is written and removed by this package's Installer, driven by
//     `cmd/tangent-launchagent` and the `make launch-agent-*` targets. There is
//     no tray toggle in v1; that arrives with CW-20260905-0030 in stable 1.1.
//   - The plist hardcodes the binary path. Install refuses when the binary is
//     not where the plist would say, and Status reports a plist whose binary
//     has since moved, so the failure is a message rather than a silent
//     no-start at next login.
//
// Chosen over SMAppService deliberately: no ObjC or Swift shim, and the same
// shape ports to a systemd unit when the Linux server work lands.
//
// Nothing in this package runs launchctl or touches ~/Library unless a caller
// hands it a real Launchctl and a real home directory; tests use a fake and a
// temporary directory.
package launchagent

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Label is the LaunchAgent label and the plist file's base name. It matches
// the bundle identifier the desktop shell's Info.plist carries.
const Label = "com.hollislabs.tangent"

// Config is everything the plist needs.
type Config struct {
	// Binary is the absolute path to the headless tangent daemon. Required.
	Binary string
	// Port, when non-zero, is exported to the daemon as TANGENT_HTTP_PORT.
	// Zero leaves the daemon's own default in force.
	Port int
	// DBPath, when set, is exported as TANGENT_DB_PATH. Empty leaves the
	// daemon's default (~/.tangent/tangent.db) in force.
	DBPath string
	// PluginDir, when set, is exported as TANGENT_PLUGIN_DIR. Empty leaves the
	// daemon's default (~/.tangent/plugins) in force.
	//
	// It travels beside DBPath deliberately. A packaged install that relocated
	// one and not the other would split a plugin from the database it was
	// installed alongside, and the symptom — a plugin that loads but finds none
	// of its own state — reads as a plugin bug rather than a path one.
	PluginDir string
	// LogDir receives tangent.log (stdout and stderr). Empty means
	// DefaultLogDir(home).
	LogDir string
}

// PlistPath is where the agent lives for the given home directory.
func PlistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
}

// DefaultLogDir is where the daemon's output goes unless Config.LogDir says
// otherwise. ~/Library/Logs is where Console.app and a person expect to look.
func DefaultLogDir(home string) string {
	return filepath.Join(home, "Library", "Logs", "Tangent")
}

// ValidateBinary is the path check Install performs and Status repeats: the
// path must be absolute, exist, be a regular file, and be executable. The
// error text is meant to be shown to the person as-is.
func ValidateBinary(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("launch agent: no daemon binary path given")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("launch agent: daemon binary path must be absolute, got %q (launchd resolves nothing relative to a shell)", path)
	}
	info, err := os.Stat(path) //nolint:gosec // the path is the operator's chosen daemon binary; checking it is the point
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("launch agent: daemon binary %q does not exist; the plist would point at nothing and the daemon would silently not start at login", path)
	case err != nil:
		return fmt.Errorf("launch agent: daemon binary %q: %w", path, err)
	case info.IsDir():
		return fmt.Errorf("launch agent: daemon binary %q is a directory, not the tangent executable", path)
	case !info.Mode().IsRegular():
		return fmt.Errorf("launch agent: daemon binary %q is not a regular file", path)
	case info.Mode()&0o111 == 0:
		return fmt.Errorf("launch agent: daemon binary %q is not executable (mode %s)", path, info.Mode())
	}
	return nil
}

// Render produces the plist bytes for cfg. It validates the binary path's
// shape (absolute, non-empty) but does not stat it; Install does that, so a
// plist can be rendered for inspection on a machine where the binary is not
// installed.
func Render(cfg Config, home string) ([]byte, error) {
	if strings.TrimSpace(cfg.Binary) == "" {
		return nil, errors.New("launch agent: no daemon binary path given")
	}
	if !filepath.IsAbs(cfg.Binary) {
		return nil, fmt.Errorf("launch agent: daemon binary path must be absolute, got %q", cfg.Binary)
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		return nil, fmt.Errorf("launch agent: port %d is out of range", cfg.Port)
	}
	logDir := cfg.LogDir
	if logDir == "" {
		logDir = DefaultLogDir(home)
	}
	logPath := filepath.Join(logDir, "tangent.log")

	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n<dict>\n")
	writeKey(&b, "Label", Label)
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	b.WriteString("\t\t<string>" + html.EscapeString(cfg.Binary) + "</string>\n")
	b.WriteString("\t</array>\n")
	if cfg.Port != 0 || cfg.DBPath != "" || cfg.PluginDir != "" {
		b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		if cfg.Port != 0 {
			b.WriteString("\t\t<key>TANGENT_HTTP_PORT</key>\n\t\t<string>" + strconv.Itoa(cfg.Port) + "</string>\n")
		}
		if cfg.DBPath != "" {
			b.WriteString("\t\t<key>TANGENT_DB_PATH</key>\n\t\t<string>" + html.EscapeString(cfg.DBPath) + "</string>\n")
		}
		if cfg.PluginDir != "" {
			b.WriteString("\t\t<key>TANGENT_PLUGIN_DIR</key>\n\t\t<string>" + html.EscapeString(cfg.PluginDir) + "</string>\n")
		}
		b.WriteString("\t</dict>\n")
	}
	// RunAtLoad true: start at login. KeepAlive false: a deliberate stop
	// stays stopped. Both are decisions, not defaults; see the package doc.
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>KeepAlive</key>\n\t<false/>\n")
	// Background so launchd schedules it like a service, not a UI app.
	writeKey(&b, "ProcessType", "Background")
	writeKey(&b, "StandardOutPath", logPath)
	writeKey(&b, "StandardErrorPath", logPath)
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes(), nil
}

func writeKey(b *bytes.Buffer, key, value string) {
	b.WriteString("\t<key>" + key + "</key>\n\t<string>" + html.EscapeString(value) + "</string>\n")
}

// Launchctl is the slice of launchctl this package needs. The real one shells
// out; tests substitute a recorder.
type Launchctl interface {
	// Bootstrap loads the plist at path into the domain (gui/<uid>).
	Bootstrap(ctx context.Context, domain, plistPath string) error
	// Bootout unloads the service target (gui/<uid>/<label>). Implementations
	// return ErrNotLoaded when the service was not loaded, which callers treat
	// as already done.
	Bootout(ctx context.Context, serviceTarget string) error
	// Loaded reports whether the service target is currently loaded.
	Loaded(ctx context.Context, serviceTarget string) (bool, error)
}

// ErrNotLoaded is returned by Bootout when there was nothing to unload.
var ErrNotLoaded = errors.New("launch agent: service is not loaded")

// Installer performs the idempotent install, uninstall, and status operations
// against one user's LaunchAgents directory.
type Installer struct {
	// Home is the user's home directory; the plist goes under
	// Home/Library/LaunchAgents and logs under Home/Library/Logs/Tangent.
	Home string
	// UID selects the launchd domain, gui/<UID>.
	UID int
	// Launchctl performs the launchd side. Required.
	Launchctl Launchctl
}

// Result reports what an install or uninstall did.
type Result struct {
	PlistPath string
	// Changed is true when the plist content on disk changed (install) or the
	// file was removed (uninstall). A second identical install reports false
	// and still re-bootstraps, so the loaded agent always matches the file.
	Changed bool
	// WasLoaded is true when a previously loaded agent was booted out first.
	WasLoaded bool
	// Loaded is true when the agent is loaded after the operation.
	Loaded bool
}

func (i Installer) domain() string        { return "gui/" + strconv.Itoa(i.UID) }
func (i Installer) serviceTarget() string { return i.domain() + "/" + Label }

// Install validates the binary, writes the plist atomically, and loads it.
// Re-running it is safe: an already loaded agent is booted out and
// bootstrapped again so the loaded definition matches the file.
func (i Installer) Install(ctx context.Context, cfg Config) (Result, error) {
	if i.Launchctl == nil {
		return Result{}, errors.New("launch agent: no launchctl implementation")
	}
	if err := ValidateBinary(cfg.Binary); err != nil {
		return Result{}, err
	}
	rendered, err := Render(cfg, i.Home)
	if err != nil {
		return Result{}, err
	}
	path := PlistPath(i.Home)
	result := Result{PlistPath: path}

	existing, readErr := os.ReadFile(path) //nolint:gosec // the path is derived from Home and a constant label
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return result, fmt.Errorf("launch agent: read existing plist: %w", readErr)
	}
	result.Changed = !bytes.Equal(existing, rendered)

	logDir := cfg.LogDir
	if logDir == "" {
		logDir = DefaultLogDir(i.Home)
	}
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		return result, fmt.Errorf("launch agent: create log directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // ~/Library/LaunchAgents is conventionally 0755
		return result, fmt.Errorf("launch agent: create LaunchAgents directory: %w", err)
	}

	// Boot out first so launchd never holds a definition the file no longer
	// matches. ErrNotLoaded is the common first-install case.
	switch err := i.Launchctl.Bootout(ctx, i.serviceTarget()); {
	case err == nil:
		result.WasLoaded = true
	case errors.Is(err, ErrNotLoaded):
	default:
		return result, fmt.Errorf("launch agent: boot out existing agent: %w", err)
	}

	if err := writeAtomic(path, rendered); err != nil {
		return result, err
	}
	if err := i.Launchctl.Bootstrap(ctx, i.domain(), path); err != nil {
		return result, fmt.Errorf("launch agent: bootstrap %s: %w", path, err)
	}
	result.Loaded = true
	return result, nil
}

// Uninstall boots the agent out and removes the plist. A missing plist and an
// unloaded agent are both "already done", not errors.
func (i Installer) Uninstall(ctx context.Context) (Result, error) {
	if i.Launchctl == nil {
		return Result{}, errors.New("launch agent: no launchctl implementation")
	}
	path := PlistPath(i.Home)
	result := Result{PlistPath: path}
	switch err := i.Launchctl.Bootout(ctx, i.serviceTarget()); {
	case err == nil:
		result.WasLoaded = true
	case errors.Is(err, ErrNotLoaded):
	default:
		return result, fmt.Errorf("launch agent: boot out: %w", err)
	}
	switch err := os.Remove(path); {
	case err == nil:
		result.Changed = true
	case errors.Is(err, os.ErrNotExist):
	default:
		return result, fmt.Errorf("launch agent: remove plist: %w", err)
	}
	return result, nil
}

// Status describes the installed agent, if any.
type Status struct {
	PlistPath string
	// Installed is true when the plist file exists.
	Installed bool
	// Loaded is true when launchd currently has the agent loaded.
	Loaded bool
	// Binary is the daemon path the plist names; empty when not installed.
	Binary string
	// BinaryOK is true when Binary passes ValidateBinary right now.
	BinaryOK bool
	// Problem is the person-facing explanation when something is wrong: the
	// binary has moved, the plist is unreadable, or it names no program.
	Problem string
}

// Status reads the plist back and re-validates the path it hardcodes, so a
// Tangent.app or tangent binary that has moved since install is reported
// rather than discovered as a login that started nothing.
func (i Installer) Status(ctx context.Context) (Status, error) {
	path := PlistPath(i.Home)
	status := Status{PlistPath: path}
	raw, err := os.ReadFile(path) //nolint:gosec // the path is derived from Home and a constant label
	if errors.Is(err, os.ErrNotExist) {
		return status, nil
	}
	if err != nil {
		return status, fmt.Errorf("launch agent: read plist: %w", err)
	}
	status.Installed = true
	if i.Launchctl != nil {
		loaded, loadedErr := i.Launchctl.Loaded(ctx, i.serviceTarget())
		if loadedErr != nil {
			return status, fmt.Errorf("launch agent: query launchd: %w", loadedErr)
		}
		status.Loaded = loaded
	}
	args, parseErr := ProgramArguments(raw)
	switch {
	case parseErr != nil:
		status.Problem = "the plist could not be parsed: " + parseErr.Error()
	case len(args) == 0:
		status.Problem = "the plist names no program"
	default:
		status.Binary = args[0]
		if validateErr := ValidateBinary(args[0]); validateErr != nil {
			status.Problem = validateErr.Error() + "; run install again with the binary's current path, or uninstall"
		} else {
			status.BinaryOK = true
		}
	}
	return status, nil
}

// ProgramArguments extracts the ProgramArguments array from plist XML. It
// understands exactly the subset this package writes plus enough of the
// format (nested dicts and arrays) to skip over anything else without
// misreading it.
func ProgramArguments(raw []byte) ([]string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	decoder.Strict = false
	// Walk to the top-level dict.
	if _, err := seekStart(decoder, "plist"); err != nil {
		return nil, err
	}
	if _, err := seekStart(decoder, "dict"); err != nil {
		return nil, err
	}
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, errors.New("no ProgramArguments key")
			}
			return nil, err
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			if end, isEnd := token.(xml.EndElement); isEnd && end.Name.Local == "dict" {
				return nil, errors.New("no ProgramArguments key")
			}
			continue
		}
		if start.Name.Local != "key" {
			// A value we are not looking at; skip it whole.
			if skipErr := decoder.Skip(); skipErr != nil {
				return nil, skipErr
			}
			continue
		}
		var key string
		if decodeErr := decoder.DecodeElement(&key, &start); decodeErr != nil {
			return nil, decodeErr
		}
		if strings.TrimSpace(key) != "ProgramArguments" {
			continue
		}
		var array struct {
			Strings []string `xml:"string"`
		}
		arrayStart, err := seekStart(decoder, "array")
		if err != nil {
			return nil, err
		}
		if err := decoder.DecodeElement(&array, &arrayStart); err != nil {
			return nil, err
		}
		return array.Strings, nil
	}
}

// seekStart advances the decoder to the next start element named local and
// returns it, consumed. It returns an error at EOF.
func seekStart(decoder *xml.Decoder, local string) (xml.StartElement, error) {
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return xml.StartElement{}, fmt.Errorf("no <%s> element", local)
			}
			return xml.StartElement{}, err
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Local == local {
			return start, nil
		}
	}
}

// writeAtomic writes data to path through a temp file and rename, so a
// crash mid-write cannot leave launchd a half plist.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+Label+"-*.plist")
	if err != nil {
		return fmt.Errorf("launch agent: create temp plist: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("launch agent: write plist: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil { //nolint:gosec // launchd reads the plist; it holds no secret
		_ = tmp.Close()
		return fmt.Errorf("launch agent: chmod plist: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("launch agent: close plist: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("launch agent: rename plist into place: %w", err)
	}
	return nil
}
