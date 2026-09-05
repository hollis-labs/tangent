package effect

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestNormalizeRefusesEveryTraversalShape is the string-level defense. Each
// case is a real shape a path has arrived in, not a synthetic permutation.
func TestNormalizeRefusesEveryTraversalShape(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		input string
		want  error
	}{
		"parent":                    {"../secrets", ErrPathEscapes},
		"bare parent":               {"..", ErrPathEscapes},
		"buried parent":             {"a/b/../../../secrets", ErrPathEscapes},
		"absolute":                  {"/etc/passwd", ErrPathEscapes},
		"absolute after clean":      {"//etc//passwd", ErrPathEscapes},
		"windows separator":         {`..\..\secrets`, ErrPathEscapes},
		"mixed separators":          {`notes\..\..\secrets`, ErrPathEscapes},
		"volume":                    {`C:\Windows`, ErrPathInvalid},
		"unc":                       {`\\host\share\file`, ErrPathEscapes},
		"nul byte":                  {"notes\x00/../../etc/passwd", ErrPathInvalid},
		"nul byte in a plain name":  {"notes\x00.txt", ErrPathInvalid},
		"empty":                     {"", ErrPathInvalid},
		"whitespace":                {"   ", ErrPathInvalid},
		"dot":                       {".", ErrPathInvalid},
		"dot slash only":            {"./", ErrPathInvalid},
		"trailing parent":           {"notes/..", ErrPathInvalid},
		"parent reached by cleanup": {"a/../../b", ErrPathEscapes},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeRelative(testCase.input)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("NormalizeRelative(%q) = %q, %v; want error %v",
					testCase.input, got, err, testCase.want)
			}
		})
	}
}

// TestNormalizeCanonicalizesWhatItAccepts. A handle stores the normalized
// form, so two spellings of one file must not mint two differently-keyed
// handles.
func TestNormalizeCanonicalizesWhatItAccepts(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{
		"notes.md":            "notes.md",
		"./notes.md":          "notes.md",
		"a//b///c.txt":        "a/b/c.txt",
		"a/./b/c.txt":         "a/b/c.txt",
		"a/b/../c.txt":        "a/c.txt",
		`docs\design\spec.md`: "docs/design/spec.md",
		"  notes.md  ":        "notes.md",
	} {
		got, err := NormalizeRelative(input)
		if err != nil || got != want {
			t.Errorf("NormalizeRelative(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
}

// TestSymlinkEscapeIsRefusedAtOpen is the case a string check cannot reach.
// `escape` is a well-formed, traversal-free, single-element relative path. Only
// resolution can tell that it leaves the root.
func TestSymlinkEscapeIsRefusedAtOpen(t *testing.T) {
	t.Parallel()
	outside := t.TempDir()
	root := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	writeFile(t, secret, "the thing outside the root")
	writeFile(t, filepath.Join(root, "inside.txt"), "the thing inside the root")

	if err := os.Symlink(secret, filepath.Join(root, "escape.txt")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	// A directory symlink is the more dangerous shape: every path under it
	// looks ordinary.
	if err := os.Symlink(outside, filepath.Join(root, "escape-dir")); err != nil {
		t.Fatalf("symlink directory: %v", err)
	}

	if _, err := NormalizeRelative("escape.txt"); err != nil {
		t.Fatalf("escape.txt is a well-formed relative path; normalization must accept it: %v", err)
	}
	if _, err := ReadInRoot(root, "escape.txt", 0); !errors.Is(err, ErrPathEscapes) {
		t.Errorf("read through a file symlink = %v, want ErrPathEscapes", err)
	}
	if _, err := ReadInRoot(root, "escape-dir/secret.txt", 0); !errors.Is(err, ErrPathEscapes) {
		t.Errorf("read through a directory symlink = %v, want ErrPathEscapes", err)
	}
	content, err := ReadInRoot(root, "inside.txt", 0)
	if err != nil || string(content) != "the thing inside the root" {
		t.Errorf("read inside the root = %q, %v; want the file's content", content, err)
	}
}

// TestAbsoluteSymlinkTargetInsideTheRootStillResolves records the boundary
// precisely: the rule is "may not leave the root", not "no symlinks". A link
// to a sibling file inside the root is ordinary filesystem hygiene and must
// keep working, or a mediated root becomes unusable for real trees.
func TestAbsoluteSymlinkTargetInsideTheRootStillResolves(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "real.txt"), "inside")
	if err := os.Symlink("real.txt", filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	content, err := ReadInRoot(root, "link.txt", 0)
	if err != nil || string(content) != "inside" {
		t.Errorf("read through an in-root symlink = %q, %v; want the target's content", content, err)
	}
}

// TestOpenIsRaceSafeAgainstASwappedDirectory is the TOCTOU case.
//
// The attack it models is the one a check-then-open cannot survive: a local
// process replaces a directory that was just verified to be inside the root
// with a symlink pointing outside it, in the window before the open. The test
// hammers that swap while reads run concurrently and asserts the invariant
// that matters — **no read ever returns content from outside the root** —
// rather than asserting a particular error, because under a race either
// outcome (the real file, or a refusal) is legitimate and only a leak is not.
func TestOpenIsRaceSafeAgainstASwappedDirectory(t *testing.T) {
	t.Parallel()
	outside := t.TempDir()
	root := t.TempDir()
	writeFile(t, filepath.Join(outside, "target.txt"), "OUTSIDE-THE-ROOT")

	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(realDir, "target.txt"), "INSIDE-THE-ROOT")

	swapped := filepath.Join(root, "swapped")
	if err := os.Symlink(realDir, swapped); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	var group sync.WaitGroup
	stop := make(chan struct{})
	group.Add(1)
	go func() {
		defer group.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// Flip `swapped` between an in-root directory and an out-of-root
			// one, as fast as the filesystem allows.
			_ = os.Remove(swapped)
			_ = os.Symlink(outside, swapped)
			_ = os.Remove(swapped)
			_ = os.Symlink(realDir, swapped)
		}
	}()

	leaked := false
	for range 2000 {
		content, err := ReadInRoot(root, "swapped/target.txt", 0)
		if err != nil {
			continue
		}
		if strings.Contains(string(content), "OUTSIDE-THE-ROOT") {
			leaked = true
			break
		}
	}
	close(stop)
	group.Wait()

	if leaked {
		t.Fatal("a read resolved outside the root under a concurrent symlink swap")
	}
}

// TestReadRefusesAtTheCeilingRatherThanTruncating. A truncated read that looks
// successful is worse than a refusal: the participant sees content that is not
// the content.
func TestReadRefusesAtTheCeilingRatherThanTruncating(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "exact.txt"), strings.Repeat("x", 16))
	writeFile(t, filepath.Join(root, "over.txt"), strings.Repeat("x", 17))

	if content, err := ReadInRoot(root, "exact.txt", 16); err != nil || len(content) != 16 {
		t.Errorf("read at exactly the ceiling = %d bytes, %v; want 16 bytes", len(content), err)
	}
	if _, err := ReadInRoot(root, "over.txt", 16); !errors.Is(err, ErrTooLarge) {
		t.Errorf("read one byte over the ceiling = %v, want ErrTooLarge", err)
	}
}

// TestNonRegularTargetsAreRefused. A directory, a device, or a fifo is not
// content, and reading one either blocks forever or returns something a
// participant will misread.
func TestNonRegularTargetsAreRefused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "dir"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := ReadInRoot(root, "dir", 0); !errors.Is(err, ErrNotRegularFile) {
		t.Errorf("read of a directory = %v, want ErrNotRegularFile", err)
	}
}

// TestWriteStaysInsideItsRoot covers the write half with the same shapes.
func TestWriteStaysInsideItsRoot(t *testing.T) {
	t.Parallel()
	outside := t.TempDir()
	root := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape-dir")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	if err := writeInRoot(root, "escape-dir/planted.txt", []byte("planted")); !errors.Is(err, ErrPathEscapes) {
		t.Errorf("write through a directory symlink = %v, want ErrPathEscapes", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "planted.txt")); !os.IsNotExist(err) {
		t.Error("a mediated write landed outside its root")
	}
	if err := writeInRoot(root, "notes.txt", []byte("kept")); err != nil {
		t.Fatalf("write inside the root: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "notes.txt")) //nolint:gosec // test fixture
	if err != nil || string(content) != "kept" {
		t.Errorf("write inside the root produced %q, %v", content, err)
	}
}

// TestARootMustBeAbsolute. A relative root resolves against whatever the
// process happens to have as a working directory, which is not a root anyone
// chose.
func TestARootMustBeAbsolute(t *testing.T) {
	t.Parallel()
	set := NewRootSet([]Root{
		{ID: "relative", Path: "some/relative/dir"},
		{ID: "empty", Path: ""},
		{ID: "", Path: "/tmp"},
		{ID: "good", Path: filepath.Join(string(filepath.Separator), "tmp")},
	})
	if _, ok := set.Lookup("relative"); ok {
		t.Error("a relative root was registered")
	}
	if _, ok := set.Lookup("empty"); ok {
		t.Error("a root with no path was registered")
	}
	if _, ok := set.Lookup(""); ok {
		t.Error("a root with no id was registered")
	}
	root, ok := set.Lookup("good")
	if !ok {
		t.Fatal("an absolute root was not registered")
	}
	if root.MaxReadBytes != DefaultMaxReadBytes {
		t.Errorf("root ceiling = %d, want the default %d", root.MaxReadBytes, DefaultMaxReadBytes)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
