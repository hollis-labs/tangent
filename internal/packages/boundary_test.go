package packages_test

import (
	"go/build"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/interactionpkg"
	"github.com/hollis-labs/tangent/internal/packages"
)

func packagesNewRegistry() (*interactionpkg.Registry, error) { return packages.NewRegistry() }

// coreImportPaths are the Tangent-core packages that hosted
// tangent.form-collect's business state and response interpretation before
// CW-20260825-0074.
var coreImportPaths = []string{
	"github.com/hollis-labs/tangent/internal/room",
	"github.com/hollis-labs/tangent/internal/mcp",
	"github.com/hollis-labs/tangent/internal/envelope",
	"github.com/hollis-labs/tangent/internal/envelope/extensions",
	"github.com/hollis-labs/tangent/internal/interaction",
	"github.com/hollis-labs/tangent/internal/roomflow",
}

// TestCoreDoesNotImportAnyInteractionPackage is the removal claim stated as a
// compile-time fact rather than as a behavior.
//
// Deleting internal/packages/formcollect and its line in packages.go must
// leave every package above compiling. Test files are excluded deliberately:
// internal/mcp's tests install the shipped packages the way production does,
// which is how the end-to-end behavior gets covered, and a test dependency
// does not make core depend on a package at build time.
func TestCoreDoesNotImportAnyInteractionPackage(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	for _, importPath := range coreImportPaths {
		pkg, err := build.ImportDir(filepath.Join(root, strings.TrimPrefix(
			importPath, "github.com/hollis-labs/tangent/")), 0)
		if err != nil {
			t.Fatalf("import %s: %v", importPath, err)
		}
		for _, imported := range pkg.Imports {
			if strings.HasPrefix(imported, "github.com/hollis-labs/tangent/internal/packages") {
				t.Errorf("%s imports %s; core must not name an interaction package, "+
					"or removing one stops being a directory deletion", importPath, imported)
			}
		}
	}
}

// formCollectVocabulary is the business vocabulary that lived in
// internal/room/form_state.go and internal/mcp/form_collect_handler.go. None of
// it may reappear in a core source file.
//
// It is a source scan rather than a type check because the leak this guards
// against is a reader's convenience — a helper "just for form-collect" added to
// a core file — and that never shows up as an import.
var formCollectVocabulary = regexp.MustCompile(
	`FormSnapshot|FormStateView|ProjectFormState|SaveFormSnapshot|` +
		`FormSavedDraft|FormTemplate|FormAttachmentRef|FormSubmissionSummary|` +
		`normalizeFormCollect|formCollectBlob|buildFormCollectExport|countFormAnswers`)

func TestCoreCarriesNoFormCollectBusinessVocabulary(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	for _, importPath := range coreImportPaths {
		dir := filepath.Join(root, strings.TrimPrefix(importPath, "github.com/hollis-labs/tangent/"))
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			// #nosec G304 -- the path is this repository's own source tree,
			// walked from the module root; there is no external input.
			body, readErr := os.ReadFile(filepath.Join(dir, name))
			if readErr != nil {
				t.Fatalf("read %s: %v", name, readErr)
			}
			if match := formCollectVocabulary.Find(body); match != nil {
				t.Errorf("%s/%s names %q; tangent.form-collect's business vocabulary "+
					"belongs to its package", importPath, name, match)
			}
		}
	}
}

// TestEveryShippedPackageIsRegisteredExactlyOnce keeps the shipped set and the
// directory tree from drifting apart, the way extensions' package tree and
// registration table are already held together.
func TestEveryShippedPackageIsRegisteredExactlyOnce(t *testing.T) {
	t.Parallel()
	registry, err := packagesNewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	kinds := registry.Kinds()

	root := repoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "internal", "packages"))
	if err != nil {
		t.Fatalf("read internal/packages: %v", err)
	}
	var directories []string
	for _, entry := range entries {
		if entry.IsDir() {
			directories = append(directories, entry.Name())
		}
	}
	if len(directories) != len(kinds) {
		t.Fatalf("internal/packages holds %v but %v are registered; a package "+
			"directory with no registration, or the reverse", directories, kinds)
	}
	for _, kind := range kinds {
		_, slug, _ := strings.Cut(kind, ".")
		slug = strings.ReplaceAll(slug, "-", "")
		if !slices.Contains(directories, slug) {
			t.Errorf("%s is registered but has no internal/packages/%s directory", kind, slug)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for range 6 {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the repository root")
	return ""
}
