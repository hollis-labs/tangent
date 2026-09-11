package extensions

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/hollis-labs/tangent/internal/definition"
	"github.com/hollis-labs/tangent/internal/envelope"
)

// packagesFS is the shipped package tree: one directory per package id, one
// directory per kind inside it, and inside that the authored `manifest.yaml`
// plus the schema files it references.
//
// ADR 0003 §6 requires a bundled kind's manifest to be a standalone file
// rather than an inline Go []byte. Two reasons, and the second is the one that
// matters: the §2 field list does not survive as a Go string literal, and a
// file has a digest-able identity that an inline literal does not. Every
// digest in this stack — manifest_digest, contract_digest, binding_digest, the
// @definition-source stamp on generated artifacts — is taken over these bytes.
//
// One embed of the whole tree rather than eighteen per-kind directives, so the
// drift walker and the registrar necessarily read the same files. A kind whose
// directory exists but which nothing registers, or a registration with no
// directory, is caught by TestPackageTreeMatchesRegistrations rather than
// discovered in production.
//
// # A request schema is contract bytes, not documentation (CW-20260911-0004)
//
// It follows from the paragraph above, and it decides a question that looked
// like a style preference: **which fields render as markdown is documented on
// the MCP tool schemas in internal/mcp, and deliberately not here.**
//
// `internal/mcp/*_schema.go` says, per field, "Renders as markdown." — and says
// where it does NOT, which is the half that matters for a diff hunk or a
// reviewer's exact wording. A package request schema says nothing about it.
// That asymmetry is the decision rather than an oversight, and
// TestPackageRequestSchemasDocumentNoRendering holds it so a later reader does
// not "fix" it.
//
// The reason is cost, and it is not small. A field `description` added here
// changes the request schema bytes, so it changes `contract_digest` and
// `binding_digest` (internal/interaction/catalog.go hashes the schema bytes
// into both). Under ADR 0003 §8 C1 that makes every pending interaction of the
// kind `unavailable` for new submissions, and under §3 a `contract_digest`
// change may not ride a `revision` bump at all — it is a new `version`, which
// §8 C3 freezes for the shipped kinds. So the wrong place for a rendering fact
// is the one place where writing it down can take a live surface out of
// service. A manifest `description` is the one §3 does contemplate correcting,
// because it moves `manifest_digest` and not the contract.
//
// The corollary for a plugin author copying one of these as a model: copy the
// silence. Say which fields render as markdown in the tool schema your plugin
// registers, where a correction costs a rebuild and nothing else.
//
//go:embed all:packages
var packagesFS embed.FS

// packagesRoot is the embedded path prefix. Kept as a constant because both
// the loader and the tree walker index into it.
const packagesRoot = "packages"

// Manifest and schema file names inside one kind's package directory. They are
// fixed rather than free-form: the manifest names its schemas by relative
// path, and constraining that path to these three keeps a package from
// referencing something outside its own directory.
const (
	manifestFileName       = "manifest.yaml"
	requestSchemaFileName  = "request.schema.json"
	responseSchemaFileName = "response.schema.json"
	errorSchemaFileName    = "error.schema.json"
)

// packageMaterial is one kind's shipped bytes, read out of packagesFS.
type packageMaterial struct {
	PackageID string
	Kind      string
	Manifest  []byte
	Request   []byte
	Response  []byte
	Error     []byte
	// Locator is the embedded path the material was read from, recorded as
	// trust.source_locator (ADR 0003 §2.7).
	Locator string
}

// loadPackageMaterial reads one kind's directory. The manifest is authoritative
// about which schema files exist: a file present on disk but unreferenced is
// not loaded, and a referenced file that is missing is an error rather than a
// silently absent schema.
func loadPackageMaterial(packageID, kind string) (packageMaterial, error) {
	dir := path.Join(packagesRoot, packageID, kindSlug(kind))
	manifestPath := path.Join(dir, manifestFileName)
	manifestBytes, err := packagesFS.ReadFile(manifestPath)
	if err != nil {
		return packageMaterial{}, fmt.Errorf(
			"%w: %s: %s: %w", definition.ErrMaterialMissing, kind, manifestPath, err)
	}
	manifest, err := definition.Parse(manifestBytes)
	if err != nil {
		return packageMaterial{}, err
	}
	if manifest.Kind != kind || manifest.PackageID != packageID {
		return packageMaterial{}, fmt.Errorf(
			"%w: %s declares kind %q in package %q but ships at %s",
			definition.ErrInvalidManifest, manifestPath, manifest.Kind, manifest.PackageID, dir)
	}

	material := packageMaterial{
		PackageID: packageID, Kind: kind,
		Manifest: manifestBytes, Locator: "embedded://" + manifestPath,
	}
	for _, reference := range []struct {
		ref    string
		target *[]byte
	}{
		{manifest.RequestSchemaRef, &material.Request},
		{manifest.ResponseSchemaRef, &material.Response},
		{manifest.ErrorSchemaRef, &material.Error},
	} {
		if reference.ref == "" {
			continue
		}
		if err := validateSchemaRef(kind, reference.ref); err != nil {
			return packageMaterial{}, err
		}
		body, readErr := packagesFS.ReadFile(path.Join(dir, reference.ref))
		if readErr != nil {
			return packageMaterial{}, fmt.Errorf(
				"%w: %s references %s: %w", definition.ErrMaterialMissing, kind, reference.ref, readErr)
		}
		*reference.target = body
	}
	return material, nil
}

// validateSchemaRef confines a manifest's schema references to the three known
// file names inside its own directory. A package that could name an arbitrary
// path could reach into a sibling package's material, and the ownership split
// ADR 0003 §5 draws would stop being enforceable by inspection.
func validateSchemaRef(kind, ref string) error {
	switch ref {
	case requestSchemaFileName, responseSchemaFileName, errorSchemaFileName:
		return nil
	}
	return fmt.Errorf(
		"%w: %s: schema reference %q must be one of %s, %s, %s",
		definition.ErrInvalidManifest, kind, ref,
		requestSchemaFileName, responseSchemaFileName, errorSchemaFileName)
}

// kindSlug is the directory name for a kind: the wire name without its
// publisher namespace. `tangent.diff-review` ships at
// packages/<package-id>/diff-review/.
func kindSlug(kind string) string {
	if _, slug, found := strings.Cut(kind, "."); found {
		return slug
	}
	return kind
}

// registerPackagedDefinition installs one shipped kind through the manifest
// path. It is the C3 compatibility adapter in one function: the manifest is
// authored to reproduce the exact wire name, version, description, response
// kind, request schema bytes, and `ui.component` slug the previous inline
// registration produced, so nothing on the wire moves — only the amount the
// host knows about the definition changes.
func registerPackagedDefinition(svc *envelope.Service, packageID, kind string) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	material, err := loadPackageMaterial(packageID, kind)
	if err != nil {
		return fmt.Errorf("extensions: load %s: %w", kind, err)
	}
	if err := svc.RegisterDefinition(
		material.Manifest, material.Request, material.Response, PluginID,
		envelope.WithErrorSchema(material.Error),
		envelope.WithSourceLocator(material.Locator),
	); err != nil {
		return fmt.Errorf("extensions: register %s: %w", kind, err)
	}
	return nil
}

// PackageManifest returns the authored manifest bytes for one shipped kind,
// without registering it. Used by the drift walker and by tooling that wants
// to read a manifest without booting a registry.
func PackageManifest(kind string) ([]byte, error) {
	packageID, ok := packageIDFor(kind)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not a shipped kind", definition.ErrMaterialMissing, kind)
	}
	material, err := loadPackageMaterial(packageID, kind)
	if err != nil {
		return nil, err
	}
	return bytes.Clone(material.Manifest), nil
}

// ShippedPackagePaths walks the embedded tree and returns every kind directory
// it finds, as "<package-id>/<kind-slug>", sorted.
//
// This is deliberately a walk of the shipped assets rather than a projection
// of the registration table: comparing the two is what detects a manifest
// authored but never registered, and a registration whose package was deleted.
func ShippedPackagePaths() ([]string, error) {
	var found []string
	err := fs.WalkDir(packagesFS, packagesRoot, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != manifestFileName {
			return nil
		}
		found = append(found, path.Dir(strings.TrimPrefix(current, packagesRoot+"/")))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("extensions: walk package tree: %w", err)
	}
	sort.Strings(found)
	return found, nil
}
