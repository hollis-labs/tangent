package pluginpkg

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/plugin-sdk/subprocess"
)

// Three tests, and deliberately only three.
//
// `internal/smoke` proves the happy path against the real binary: build a
// plugin, emit its manifest, install it, boot, and see its tools advertised.
// What it cannot prove is the FAILURE paths, and those are the novel claims
// this package makes — the two behaviors inverted from the reference
// implementation, and the compatibility policy. Without these they would be
// assertions in a commit body rather than properties a build enforces.
//
// Anything wider — table-driven coverage of manifest validation, path edge
// cases, install idempotency — is deliberately not here.

// writePlugin lays down a plugin directory with a working entrypoint.
func writePlugin(t *testing.T, root, id string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	binary := filepath.Join(dir, "runme")
	// 0o700 rather than 0o600: discovery refuses a non-executable entrypoint,
	// which is the point of the bit. #nosec G306 -- inside t.TempDir().
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306
		t.Fatalf("write entrypoint: %v", err)
	}
	manifest := "id: " + id + "\n" +
		"name: " + id + "\n" +
		"version: 1.0.0\n" +
		"protocol: " + itoa(subprocess.ProtocolVersion) + "\n" +
		"entrypoint: runme\n"
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// TestOneBadManifestCostsOnePlugin is the first Nanite defect, inverted.
//
// Its DiscoverPlugins returns `nil, err` on the first manifest that fails to
// parse, so one malformed file means ZERO plugins discovered rather than one
// plugin missing — the same defect class as a read timeout that kills a whole
// connection. A host that booted with no plugins because of one bad directory
// would be reporting a problem that is real and describing it as the wrong size.
func TestOneBadManifestCostsOnePlugin(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writePlugin(t, root, "good.one")
	writePlugin(t, root, "good.two")

	broken := filepath.Join(root, "broken.one")
	if err := os.MkdirAll(broken, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(broken, ManifestName),
		[]byte("id: [this is not\n  valid yaml\n"), 0o600); err != nil {
		t.Fatalf("write broken manifest: %v", err)
	}

	// A directory that is not a plugin at all is ignored rather than reported:
	// a leftover or an editor's backup under the install root is not something
	// an operator needs told about.
	if err := os.MkdirAll(filepath.Join(root, "not-a-plugin"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	installed, rejected, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan returned a fatal error for one bad directory: %v", err)
	}
	if len(installed) != 2 {
		t.Errorf("installed = %d, want 2: one unparseable manifest took out the scan", len(installed))
	}
	if len(rejected) != 1 {
		t.Fatalf("rejected = %d, want 1", len(rejected))
	}
	if rejected[0].Dir != broken {
		t.Errorf("rejected %s, want %s", rejected[0].Dir, broken)
	}
	// Attributable, not just counted. An operator needs to know WHICH plugin
	// and why, which is the whole reason these come back rather than being
	// logged and dropped.
	if rejected[0].Reason == nil || !strings.Contains(rejected[0].Reason.Error(), broken) {
		t.Errorf("the rejection does not name the directory: %v", rejected[0].Reason)
	}
}

// TestAnIncompletePluginIsRefusedBeforeTheExistingOneIsTouched is Nanite's
// install defect, inverted.
//
// Theirs strips `ui/dist` without rebuilding and leaves an install that looks
// complete and does not work (CW-20260423-0005). The general shape is an
// install that reports success for something that will not load.
//
// Here the staged copy is run through the same evaluation discovery uses,
// before anything existing is disturbed. The second half of that sentence is
// the part worth testing: an upgrade that fails must leave the working plugin
// working, or a bad build becomes an outage.
func TestAnIncompletePluginIsRefusedBeforeTheExistingOneIsTouched(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installRoot := filepath.Join(root, "installed")
	if err := os.MkdirAll(installRoot, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	good := writePlugin(t, filepath.Join(root, "src-good"), "acme.plugin")
	if _, err := Install(good, installRoot); err != nil {
		t.Fatalf("install the working version: %v", err)
	}

	// The same plugin, rebuilt badly: the manifest is fine and the entrypoint
	// it names is missing. This is what a build that half-succeeded produces.
	incomplete := filepath.Join(root, "src-broken", "acme.plugin")
	if err := os.MkdirAll(incomplete, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifest, err := os.ReadFile(filepath.Join(good, ManifestName)) // #nosec G304 G703 -- t.TempDir() path.
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if writeErr := os.WriteFile(filepath.Join(incomplete, ManifestName), manifest, 0o600); writeErr != nil { // #nosec G703 -- t.TempDir() path.
		t.Fatalf("write manifest: %v", writeErr)
	}

	if _, installErr := Install(incomplete, installRoot); installErr == nil {
		t.Fatal("installing a plugin whose entrypoint is missing succeeded")
	}

	// The working plugin is still there and still loads. That is the property:
	// a failed upgrade is a no-op, not an outage.
	installed, rejected, err := Scan(installRoot)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(rejected) != 0 {
		t.Errorf("a refused install left %d unusable directories behind: %+v", len(rejected), rejected)
	}
	if len(installed) != 1 || installed[0].Manifest.ID != "acme.plugin" {
		t.Fatalf("the working plugin did not survive a refused upgrade: %+v", installed)
	}
	if _, statErr := os.Stat(installed[0].Entrypoint); statErr != nil {
		t.Errorf("the surviving plugin has no entrypoint: %v", statErr)
	}
	// And no staging directory is left lying around for discovery to trip over.
	entries, err := os.ReadDir(installRoot)
	if err != nil {
		t.Fatalf("read install root: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("install root holds %d entries after a refused install, want 1", len(entries))
	}
}

// TestAnIncompatibleProtocolIsRefused holds the compatibility policy.
//
// The wire version must match exactly — not a range, not a floor. The protocol
// carries no capability negotiation, so two ends that nearly agree have no way
// to discover what they disagree about, and the failure would surface as a
// wrong answer at one method rather than a refusal at boot. A permissive
// fallback here is the defect class ADR 0003 §2.7 forbids in its own domain.
//
// Asserted at BOTH doors, because an operator meets whichever comes first and
// only one of them is cheap: install costs a command, boot costs a debugging
// session with the install already forgotten.
func TestAnIncompatibleProtocolIsRefused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := writePlugin(t, filepath.Join(root, "src"), "acme.future")

	manifestPath := filepath.Join(dir, ManifestName)
	raw, err := os.ReadFile(manifestPath) // #nosec G304 G703 -- t.TempDir() path.
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	future := strings.Replace(string(raw),
		"protocol: "+itoa(subprocess.ProtocolVersion),
		"protocol: "+itoa(subprocess.ProtocolVersion+1), 1)
	if writeErr := os.WriteFile(manifestPath, []byte(future), 0o600); writeErr != nil { // #nosec G703 -- t.TempDir() path.
		t.Fatalf("write manifest: %v", writeErr)
	}

	installRoot := filepath.Join(root, "installed")
	if mkErr := os.MkdirAll(installRoot, 0o750); mkErr != nil {
		t.Fatalf("mkdir: %v", mkErr)
	}
	_, installErr := Install(dir, installRoot)
	if !errors.Is(installErr, ErrIncompatible) {
		t.Fatalf("install err = %v, want ErrIncompatible", installErr)
	}

	// And at boot, for a plugin installed by some other route — copied in by
	// hand, or installed by a host that spoke its version.
	copied := filepath.Join(installRoot, "acme.future")
	if mkErr := os.MkdirAll(copied, 0o750); mkErr != nil {
		t.Fatalf("mkdir: %v", mkErr)
	}
	for _, name := range []string{ManifestName, "runme"} {
		body, readErr := os.ReadFile(filepath.Join(dir, name)) // #nosec G304 G703 -- t.TempDir() path.
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		if writeErr := os.WriteFile(filepath.Join(copied, name), body, 0o700); writeErr != nil { // #nosec G306 G703 -- t.TempDir() path.
			t.Fatalf("write %s: %v", name, writeErr)
		}
	}

	installed, rejected, err := Scan(installRoot)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(installed) != 0 {
		t.Errorf("a plugin speaking protocol %d loaded on a host speaking %d",
			subprocess.ProtocolVersion+1, subprocess.ProtocolVersion)
	}
	if len(rejected) != 1 || !errors.Is(rejected[0].Reason, ErrIncompatible) {
		t.Fatalf("rejected = %+v, want one ErrIncompatible", rejected)
	}
	// The refusal says what to do about it. A version mismatch an operator
	// cannot act on is a version mismatch reported badly.
	if !strings.Contains(rejected[0].Reason.Error(), "rebuild") {
		t.Errorf("the refusal does not say how to resolve it: %v", rejected[0].Reason)
	}
}
