// Package pluginpkg is the installed-plugin format: what a `plugin.yaml`
// declares, where installed plugins live, and how the host finds them
// (CW-20260911-0070).
//
// # Whose declaration this is
//
// The host's. That distinction is load-bearing and it is the same ruling
// `internal/pluginhost` already applies: Tangent deliberately never calls
// `mcp/list_tools`, because Nanite built runtime self-declaration and discarded
// it, and ADR 0008 §3 records the host-manifest-authoritative result.
//
// A `plugin.yaml` does not reverse that. It is read ONCE, by the host, at
// install and at boot — before the child is running and before it could answer
// for itself. What the host reads there becomes the host's own record of what
// that plugin contributes, and the child is told what it was registered for at
// dispatch time. A child never gets to widen its own surface between two boots
// of the same host, which is exactly what runtime self-declaration allowed.
//
// If that ever needs to change, it is a reversal of ADR 0008 §3 and wants its
// own record — not a field quietly added here.
//
// # Why this is not ADR 0003's manifest
//
// `internal/definition` owns interaction definition manifests: what a KIND is,
// what may render it, what it may reach. Those are versioned, digested and
// frozen, and ADR 0003 §6 assigns each one an owning package.
//
// This is a different document with a different job — what a PROCESS is, and
// how to start it. It carries no trust claim, grants nothing, and has a verified artifact inventory. A plugin that contributes a kind still ships that kind's ADR
// 0003 manifest through the host's own package tree, and this file cannot
// substitute for it.
package pluginpkg

import (
	"fmt"
	"os"

	sdkmanifest "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/tangent/internal/definition"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/pkg/plugin"
)

const ManifestName = plugin.ManifestName

type Manifest = plugin.Manifest
type ToolDecl = plugin.ToolDecl
type RouteDecl = plugin.RouteDecl

var ErrIncompatible = fmt.Errorf("pluginpkg: incompatible plugin contract")

// CheckCompatible checks explicit public contracts, never app release versions.
func CheckCompatible(m Manifest) error {
	if err := m.CheckCompatibility(sdkmanifest.Compatibility{Hosts: map[string]string{"tangent": plugin.ContractVersion}, Engines: map[string]string{"binary": plugin.BinaryContractVersion}}); err != nil {
		return fmt.Errorf("%w: %s: %w; rebuild for this host contract", ErrIncompatible, m.ID, err)
	}
	if _, ok := m.Hosts["tangent"]; !ok {
		return fmt.Errorf("%w: %s declares no tangent host contract", ErrIncompatible, m.ID)
	}
	for _, ref := range m.Bindings.Kinds {
		raw, err := extensions.PackageManifest(ref.Kind)
		if err != nil {
			return fmt.Errorf("pluginpkg: %s unresolved kind %s: %w", m.ID, ref.Kind, err)
		}
		authored, err := definition.Parse(raw)
		if err != nil {
			return err
		}
		if authored.PackageID != ref.Package || authored.Version != ref.Version {
			return fmt.Errorf("pluginpkg: %s kind %s package/version mismatch: requested %s@%s, approved %s@%s", m.ID, ref.Kind, ref.Package, ref.Version, authored.PackageID, authored.Version)
		}
	}
	return nil
}

func ParseManifest(path string) (Manifest, error) {
	f, err := os.Open(path) // #nosec G304 -- manifest path comes from the install/discovery directory.
	if err != nil {
		return Manifest{}, fmt.Errorf("pluginpkg: read %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	m, err := plugin.DecodeManifest(f)
	if err != nil {
		return Manifest{}, fmt.Errorf("pluginpkg: parse %s: %w", path, err)
	}
	return m, nil
}
