package room_test

import (
	"errors"
	"strings"
	"testing"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/room"
)

// TestNormalizeArtifactURIRefusesEveryUnmediatedScheme is the compatibility
// adapter for `tangent.diff-review`, stated as a rule rather than as a
// workflow's private habit.
//
// `tangent.file-picker` and `tangent.whiteboard` already held their reference
// URIs to `artifact://`; diff-review carried structurally identical fields with
// no rule at all. The refusal is about the class of reference, not the
// specific scheme: resolving any of these is an effect the definition did not
// declare and this host did not grant.
func TestNormalizeArtifactURIRefusesEveryUnmediatedScheme(t *testing.T) {
	t.Parallel()
	for _, uri := range []string{
		"https://files.example.test/before.txt",
		"http://127.0.0.1:9/before.txt",
		"file:///etc/passwd",
		"data:text/plain;base64,AAA",
		"blob:http://localhost/abc",
		"/etc/passwd",
		"../../etc/passwd",
		"artifact://",
	} {
		if _, err := room.NormalizeArtifactURI(uri); !errors.Is(err, room.ErrUnmediatedReference) {
			t.Errorf("NormalizeArtifactURI(%q) = %v, want ErrUnmediatedReference", uri, err)
		}
	}
	for input, want := range map[string]string{
		"":                       "",
		"   ":                    "",
		"artifact://artifact-1":  "artifact://artifact-1",
		" artifact://artifact-1": "artifact://artifact-1",
	} {
		got, err := room.NormalizeArtifactURI(input)
		if err != nil || got != want {
			t.Errorf("NormalizeArtifactURI(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
}

// TestSafeExportFilenameStripsWhatACallerShouldNotChoose is the compatibility
// adapter for `tangent.output_render`, whose `filename` reaches
// `link.download` in the SPA and was carried through with TrimSpace alone — so
// a caller chose the name a file landed under in the operator's Downloads
// folder, separators included.
func TestSafeExportFilenameStripsWhatACallerShouldNotChoose(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{
		"report.md":                   "report.md",
		"  report.md  ":               "report.md",
		"../../../etc/cron.d/payload": "payload",
		`..\..\Startup\payload.bat`:   "payload.bat",
		"/absolute/report.md":         "report.md",
		".bashrc":                     "bashrc",
		"..":                          "",
		".":                           "",
		"...":                         "",
		"":                            "",
		"report\x00.md":               "report.md",
		"report\n.md":                 "report.md",
		"dir/report.md":               "report.md",
	} {
		if got := room.SafeExportFilename(input); got != want {
			t.Errorf("SafeExportFilename(%q) = %q, want %q", input, got, want)
		}
	}
	// A very long name is bounded rather than mangled somewhere with a worse
	// error.
	long := room.SafeExportFilename(strings.Repeat("a", 500) + ".md")
	if len(long) != 128 {
		t.Errorf("SafeExportFilename(long) = %d bytes, want the 128-byte bound", len(long))
	}
}

// TestDiffReviewRejectsAnUnmediatedArtifactReference wires the rule to the
// workflow it was missing from.
func TestDiffReviewRejectsAnUnmediatedArtifactReference(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer func() { _ = tangentdb.Close(db) }()

	rm := newAnonRoom(t, db)
	err := rm.SaveDiffReviewSnapshot(room.DiffReviewSnapshot{
		ReviewID: "review-1",
		Files:    []map[string]any{{"id": "file-1", "path": "src/main.go"}},
		BeforeRef: &room.DiffReviewArtifactRef{
			ArtifactID: "artifact-1",
			URI:        "https://files.example.test/before.go",
		},
	})
	if !errors.Is(err, room.ErrInvalidDiffReviewArtifactRef) {
		t.Fatalf("SaveDiffReviewSnapshot with a remote before_ref = %v, want ErrInvalidDiffReviewArtifactRef", err)
	}
}

// TestOutputRenderSanitizesTheDownloadName wires the filename rule to the
// workflow that carries it into the browser.
func TestOutputRenderSanitizesTheDownloadName(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	defer func() { _ = tangentdb.Close(db) }()

	rm := newAnonRoom(t, db)
	if err := rm.SetFinalOutput(room.FinalOutputView{
		Title:    "Report",
		Markdown: "# Report",
		Filename: "../../../etc/cron.d/payload",
	}); err != nil {
		t.Fatalf("SaveFinalOutput: %v", err)
	}
	view := room.ProjectFinalOutput(rm.PhaseState())
	if view == nil || view.Filename != "payload" {
		t.Fatalf("persisted filename = %+v, want the sanitized basename", view)
	}
}
