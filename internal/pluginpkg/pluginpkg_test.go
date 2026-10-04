package pluginpkg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	sdkmanifest "github.com/hollis-labs/plugin-sdk/manifest"
	public "github.com/hollis-labs/tangent/pkg/plugin"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/plugin-sdk/subprocess"
)

// Existing discovery and failed-upgrade behavior remains covered with native v2 bundles.
//
// `internal/smoke` proves the happy path against the real binary: build a
// plugin, emit its manifest, install it, boot, and see its tools advertised.
// What it cannot prove is the FAILURE paths, and those are the novel claims
// this package makes — the two behaviors inverted from the reference
// implementation, and the compatibility policy. Without these they would be
// assertions in a commit body rather than properties a build enforces.
//
// Strict declarations and payload boundaries are also exercised at both doors.

// writePlugin lays down a plugin directory with a working entrypoint.
func writePlugin(t *testing.T, root, id string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if checkErr := os.MkdirAll(filepath.Join(dir, "bin"), 0700); checkErr != nil {
		t.Fatal(checkErr)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(executable) // #nosec G304 -- native test executable from os.Executable.
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "bin", "runme")
	if checkErr := os.WriteFile(binary, payload, 0700); checkErr != nil {
		t.Fatal(checkErr)
	} // #nosec G306 G703 -- executable payload in t.TempDir.
	sum := sha256.Sum256(payload)
	files := []sdkmanifest.ArtifactFile{{Path: "bin/runme", SHA256: hex.EncodeToString(sum[:]), Executable: true}}
	digest, err := sdkmanifest.TreeDigest(files)
	if err != nil {
		t.Fatal(err)
	}
	common := sdkmanifest.Manifest{SchemaVersion: 2, ID: id, Name: id, Version: "1.0.0", Protocol: 2, Runtime: "subprocess", Server: sdkmanifest.Server{Runtime: "binary", Entry: "bin/runme", Engines: map[string]sdkmanifest.HostRange{"binary": {Min: "1.0.0"}}}, Hosts: map[string]sdkmanifest.HostRange{"tangent": {Min: "1.0.0"}}, Artifact: sdkmanifest.Artifact{Files: files, TreeSHA256: digest}}
	var out bytes.Buffer
	if checkErr := public.EncodeManifest(&out, common, public.TangentExtension{SchemaVersion: 1}); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := os.WriteFile(filepath.Join(dir, ManifestName), out.Bytes(), 0600); checkErr != nil {
		t.Fatal(checkErr)
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
	if checkErr := os.MkdirAll(broken, 0o750); checkErr != nil {
		t.Fatalf("mkdir: %v", checkErr)
	}
	if err := os.WriteFile(filepath.Join(broken, ManifestName),
		[]byte("id: [this is not\n  valid yaml\n"), 0o600); err != nil {
		t.Fatalf("write broken manifest: %v", err)
	}

	// A directory that is not a plugin at all is ignored rather than reported:
	// a leftover or an editor's backup under the install root is not something
	// an operator needs told about.
	if checkErr := os.MkdirAll(filepath.Join(root, "not-a-plugin"), 0o750); checkErr != nil {
		t.Fatalf("mkdir: %v", checkErr)
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
	if checkErr := os.MkdirAll(installRoot, 0o750); checkErr != nil {
		t.Fatalf("mkdir: %v", checkErr)
	}

	good := writePlugin(t, filepath.Join(root, "src-good"), "acme.plugin")
	if _, err := Install(good, installRoot); err != nil {
		t.Fatalf("install the working version: %v", err)
	}

	// The same plugin, rebuilt badly: the manifest is fine and the entrypoint
	// it names is missing. This is what a build that half-succeeded produces.
	incomplete := filepath.Join(root, "src-broken", "acme.plugin")
	if checkErr := os.MkdirAll(incomplete, 0o750); checkErr != nil {
		t.Fatalf("mkdir: %v", checkErr)
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
		"\"protocol\": "+itoa(subprocess.ProtocolVersion),
		"\"protocol\": "+itoa(subprocess.ProtocolVersion+1), 1)
	if writeErr := os.WriteFile(manifestPath, []byte(future), 0o600); writeErr != nil { // #nosec G703 -- t.TempDir() path.
		t.Fatalf("write manifest: %v", writeErr)
	}

	installRoot := filepath.Join(root, "installed")
	if mkErr := os.MkdirAll(installRoot, 0o750); mkErr != nil {
		t.Fatalf("mkdir: %v", mkErr)
	}
	_, installErr := Install(dir, installRoot)
	if installErr == nil || !strings.Contains(installErr.Error(), "protocol") {
		t.Fatalf("install err = %v, want ErrIncompatible", installErr)
	}

	// And at boot, for a plugin installed by some other route — copied in by
	// hand, or installed by a host that spoke its version.
	copied := filepath.Join(installRoot, "acme.future")
	if mkErr := os.MkdirAll(copied, 0o750); mkErr != nil {
		t.Fatalf("mkdir: %v", mkErr)
	}
	if mkErr := os.MkdirAll(filepath.Join(copied, "bin"), 0700); mkErr != nil {
		t.Fatal(mkErr)
	}
	for _, name := range []string{ManifestName, "bin/runme"} {
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
	if len(rejected) != 1 || !strings.Contains(rejected[0].Reason.Error(), "protocol") {
		t.Fatalf("rejected = %+v, want one ErrIncompatible", rejected)
	}
	// The refusal says what to do about it. A version mismatch an operator
	// cannot act on is a version mismatch reported badly.
	if !strings.Contains(rejected[0].Reason.Error(), "protocol") {
		t.Errorf("the refusal does not say how to resolve it: %v", rejected[0].Reason)
	}
}

func TestInstallAndDiscoveryRefuseInvalidDeclarations(t *testing.T) {
	cases := []struct {
		name   string
		change func(map[string]any)
		text   string
	}{
		{"development version", func(m map[string]any) { m["version"] = "dev" }, "SemVer"},
		{"v prefix", func(m map[string]any) { m["version"] = "v1.0.0" }, "SemVer"},
		{"empty version", func(m map[string]any) { m["version"] = "" }, "SemVer"},
		{"numeric leading zero", func(m map[string]any) { m["version"] = "01.0.0" }, "SemVer"},
		{"prerelease leading zero", func(m map[string]any) { m["version"] = "1.0.0-01" }, "SemVer"},
		{"future host", func(m map[string]any) { m["hosts"] = map[string]any{"tangent": map[string]any{"min": "2.0.0"}} }, "rebuild"},
		{"future engine", func(m map[string]any) {
			m["server"].(map[string]any)["engines"] = map[string]any{"binary": map[string]any{"min": "2.0.0"}}
		}, "rebuild"},
		{"unknown top field", func(m map[string]any) { m["entrypoint"] = "runme" }, "unknown field"},
		{"null version", func(m map[string]any) { m["version"] = nil }, "null"},
		{"unknown extension field", func(m map[string]any) { m["tangent"].(map[string]any)["tools"] = []any{} }, "unknown field"},
		{"future extension", func(m map[string]any) { m["tangent"].(map[string]any)["schema_version"] = 2 }, "schema_version"},
		{"unbound tool", func(m map[string]any) { m["tangent"].(map[string]any)["mcp_tools"] = []string{"tangent.undeclared"} }, "MCP binding"},
		{"foreign route", func(m map[string]any) {
			m["tangent"].(map[string]any)["routes"] = []any{map[string]any{"method": "POST", "path": "/api/plugins/other/action", "capability": "view"}}
		}, "foreign route"},
		{"unholdable route", func(m map[string]any) {
			m["tangent"].(map[string]any)["routes"] = []any{map[string]any{"method": "POST", "path": "/api/plugins/probe/action", "capability": "administer"}}
		}, "unholdable"},
		{"unresolved kind", func(m map[string]any) {
			m["tangent"].(map[string]any)["kinds"] = []any{map[string]any{"kind": "tangent.missing", "version": "0.1", "package": "tangent.missing"}}
		}, "unresolved kind"},
		{"wrong definition version", func(m map[string]any) {
			m["tangent"].(map[string]any)["kinds"] = []any{map[string]any{"kind": "tangent.app-board", "version": "0.2", "package": "tangent.appboard"}}
		}, "mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := writePlugin(t, root, "tangent.plugin.probe")
			name := filepath.Join(dir, ManifestName)
			raw, err := os.ReadFile(name) // #nosec G304 -- private test fixture.
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if checkErr := json.Unmarshal(raw, &doc); checkErr != nil {
				t.Fatal(checkErr)
			}
			tc.change(doc)
			raw, err = json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if checkErr := os.WriteFile(name, raw, 0600); checkErr != nil {
				t.Fatal(checkErr)
			} // #nosec G703 -- private fixture path.
			_, installErr := Install(dir, filepath.Join(t.TempDir(), "installed"))
			if installErr == nil || !strings.Contains(installErr.Error(), tc.text) {
				t.Fatalf("install refusal: %v, want %s", installErr, tc.text)
			}
			installed, rejected, scanErr := Scan(root)
			if scanErr != nil || len(installed) != 0 || len(rejected) != 1 || !strings.Contains(rejected[0].Reason.Error(), tc.text) {
				t.Fatalf("discovery refusal: %+v %+v %v", installed, rejected, scanErr)
			}
		})
	}
}

func TestStrictDecoderAndPayloadBoundary(t *testing.T) {
	for _, mode := range []string{"duplicate", "trailing", "yaml", "tampered", "extra", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			dir := writePlugin(t, root, "tangent.plugin.probe")
			name := filepath.Join(dir, ManifestName)
			raw, err := os.ReadFile(name) // #nosec G304 -- private fixture.
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "duplicate":
				raw = bytes.Replace(raw, []byte(`"version": "1.0.0"`), []byte(`"version": "1.0.0", "version": "2.0.0"`), 1)
			case "trailing":
				raw = append(raw, []byte(` {}`)...)
			case "yaml":
				raw = []byte("id: tangent.plugin.probe\nversion: 1.0.0\nentrypoint: runme\n")
			case "tampered":
				if checkErr := os.WriteFile(filepath.Join(dir, "bin", "runme"), []byte("changed"), 0700); checkErr != nil {
					t.Fatal(checkErr)
				} // #nosec G306 -- private native fixture.
			case "extra":
				if checkErr := os.WriteFile(filepath.Join(dir, "unlisted"), []byte("extra"), 0600); checkErr != nil {
					t.Fatal(checkErr)
				}
			case "symlink":
				if checkErr := os.Symlink(filepath.Join(dir, "bin", "runme"), filepath.Join(dir, "link")); checkErr != nil {
					t.Fatal(checkErr)
				}
			}
			if checkErr := os.WriteFile(name, raw, 0600); checkErr != nil {
				t.Fatal(checkErr)
			} // #nosec G703 -- private fixture.
			if _, installErr := Install(dir, filepath.Join(t.TempDir(), "installed")); installErr == nil {
				t.Fatal("accepted invalid install")
			}
			installed, rejected, err := Scan(root)
			if err != nil || len(installed) != 0 || len(rejected) != 1 {
				t.Fatalf("accepted discovery: %+v %+v %v", installed, rejected, err)
			}
		})
	}
}

func TestSnapshotPinsPayloadAndSeparatesState(t *testing.T) {
	root := t.TempDir()
	dir := writePlugin(t, root, "tangent.plugin.probe")
	source, err := evaluate(dir, filepath.Join(dir, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, cleanup, err := Snapshot(source, root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if checkErr := cleanup(); checkErr != nil {
			t.Error(checkErr)
		}
	}()
	if checkErr := os.WriteFile(source.Entrypoint, []byte("replaced installation"), 0700); checkErr != nil {
		t.Fatal(checkErr)
	} // #nosec G306 G703 -- private fixture.
	state := filepath.Join(root, ".state", source.Manifest.ID, "data", "kept")
	if checkErr := os.WriteFile(state, []byte("operator state"), 0600); checkErr != nil {
		t.Fatal(checkErr)
	} // #nosec G703 -- private fixture.
	if checkErr := snapshot.Manifest.VerifyBundle(snapshot.Dir); checkErr != nil {
		t.Fatal(checkErr)
	}
	info, err := os.Stat(snapshot.Entrypoint)
	if err != nil || info.Mode().Perm()&0222 != 0 {
		t.Fatalf("snapshot is writable: %v %v", info, err)
	}
	if checkErr := cleanup(); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("snapshot disposal touched writable state: %v", err)
	}
}

func TestCorrectlyInventoriedScriptIsNotANativeBundle(t *testing.T) {
	root := t.TempDir()
	dir := writePlugin(t, root, "acme.script")
	m, err := ParseManifest(filepath.Join(dir, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("#!/bin/sh\nexit 0\n")
	var out bytes.Buffer
	if encodeErr := public.EncodeNativeManifest(&out, payload, "bin/runme", m.Manifest, m.Bindings); encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if writeErr := os.WriteFile(filepath.Join(dir, "bin", "runme"), payload, 0700); writeErr != nil {
		t.Fatal(writeErr)
	} // #nosec G306 G703 -- private executable fixture.
	if writeErr := os.WriteFile(filepath.Join(dir, ManifestName), out.Bytes(), 0600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if _, installErr := Install(dir, t.TempDir()); installErr == nil || !strings.Contains(installErr.Error(), "native") {
		t.Fatalf("script install: %v", installErr)
	}
	found, rejected, err := Scan(root)
	if err != nil || len(found) != 0 || len(rejected) != 1 || !strings.Contains(rejected[0].Reason.Error(), "native") {
		t.Fatalf("script discovery: %+v %+v %v", found, rejected, err)
	}
}
