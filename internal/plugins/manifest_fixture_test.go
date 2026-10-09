package plugins

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	sdkmanifest "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/tangent/pkg/plugin"
)

func writeEchoManifest(t *testing.T, dir, executable string) {
	t.Helper()
	payload, err := os.ReadFile(executable) // #nosec G304 -- test-owned native fixture binary.
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	files := []sdkmanifest.ArtifactFile{{Path: "bin/runme", SHA256: hex.EncodeToString(sum[:]), Executable: true}}
	digest, err := sdkmanifest.TreeDigest(files)
	if err != nil {
		t.Fatal(err)
	}
	common := sdkmanifest.Manifest{SchemaVersion: 2, ID: "tangent.plugin.echo", Name: "Echo", Version: "0.1.0", Protocol: 2, Runtime: "subprocess", Server: sdkmanifest.Server{Runtime: "binary", Entry: "bin/runme", Engines: map[string]sdkmanifest.HostRange{"binary": {Min: "1.0.0"}}}, Hosts: map[string]sdkmanifest.HostRange{"tangent": {Min: "1.0.0"}}, Artifact: sdkmanifest.Artifact{Files: files, TreeSHA256: digest}, Tools: []sdkmanifest.Tool{{Name: "tangent.echo", Description: "Echo fixture", InputSchema: json.RawMessage(`{"type":"object"}`), Effect: "read"}}}
	var out bytes.Buffer
	if err := plugin.EncodeManifest(&out, common, plugin.TangentExtension{SchemaVersion: 1, MCPTools: []string{"tangent.echo"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, plugin.ManifestName), out.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}
