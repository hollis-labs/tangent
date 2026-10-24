package artifacts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeD2 writes an executable shell script standing in for the d2 binary.
func fakeD2(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "d2")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil { //nolint:gosec // a test stand-in for the d2 binary has to be executable
		t.Fatal(err)
	}
	return path
}

func TestNewD2RendererIsNilWithoutABinary(t *testing.T) {
	if r := NewD2Renderer(filepath.Join(t.TempDir(), "missing")); r != nil {
		t.Fatalf("renderer = %v, want nil so D2 falls back to source", r)
	}
}

func TestD2RendererReturnsTheDrawnPicture(t *testing.T) {
	r := NewD2Renderer(fakeD2(t, `cat >/dev/null; echo '<svg xmlns="http://www.w3.org/2000/svg"></svg>'`))
	picture, media, err := r.Render(context.Background(), []byte("a -> b"))
	if err != nil || media != "image/svg+xml" || !strings.Contains(string(picture), "<svg") {
		t.Fatalf("Render = %q, %q, %v", picture, media, err)
	}
}

func TestD2RendererRunsInAnEmptyDirectoryWithNoInheritedEnvironment(t *testing.T) {
	t.Setenv("TANGENT_SECRET_PROBE", "leak")
	// The stand-in reports where it runs and what it can see, inside an svg comment.
	r := NewD2Renderer(fakeD2(t, `cat >/dev/null; echo "<svg><!-- files=$(ls -A | wc -l | tr -d " ") probe=${TANGENT_SECRET_PROBE:-none} --></svg>"`))
	picture, _, err := r.Render(context.Background(), []byte("a -> b"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(picture), "files=0") || !strings.Contains(string(picture), "probe=none") {
		t.Fatalf("d2 ran with a dirty directory or environment: %s", picture)
	}
}

func TestD2RendererReportsWhyADiagramCouldNotBeDrawn(t *testing.T) {
	r := NewD2Renderer(fakeD2(t, `cat >/dev/null; echo "err: failed to compile -: -:1:1: bad syntax" >&2; exit 1`))
	_, _, err := r.Render(context.Background(), []byte("a ->"))
	if !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), "bad syntax") {
		t.Fatalf("err = %v, want an invalid request carrying d2's reason", err)
	}
}

func TestD2RendererRefusesOutputWithoutAPicture(t *testing.T) {
	r := NewD2Renderer(fakeD2(t, `cat >/dev/null; echo hello`))
	if _, _, err := r.Render(context.Background(), []byte("a -> b")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestD2RendererStopsARunawayRender(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the render timeout")
	}
	old := renderTimeoutForTest(300 * time.Millisecond)
	defer renderTimeoutForTest(old)
	r := NewD2Renderer(fakeD2(t, `cat >/dev/null; sleep 5`))
	start := time.Now()
	_, _, err := r.Render(context.Background(), []byte("a -> b"))
	if !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), "longer than") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("render was not stopped promptly: %s", time.Since(start))
	}
}

func TestD2ImportsAreRefusedBeforeRendering(t *testing.T) {
	for _, source := range []string{"...@other", "x: @/etc/shadow", "a -> b\n  ...@../up"} {
		if _, err := BuildFile("d2", source, "", "", "", ""); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%q: err = %v, want an import refusal", source, err)
		}
	}
	for _, source := range []string{"a -> b: mail me at bob@example.com", `a: "@handle"`, "a -> b"} {
		if _, err := BuildFile("d2", source, "", "", "", ""); err != nil {
			t.Errorf("%q: %v", source, err)
		}
	}
}

// stubRenderer returns a fixed picture, or fails.
type stubRenderer struct {
	err   error
	calls int
}

func (s *stubRenderer) Render(context.Context, []byte) ([]byte, string, error) {
	s.calls++
	if s.err != nil {
		return nil, "", s.err
	}
	return []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), "image/svg+xml", nil
}

func TestRenderedPictureIsStoredWithEachRevisionAndServed(t *testing.T) {
	svc, _, _ := newTestService(t, WithRenderer("d2", &stubRenderer{}))
	ctx := context.Background()
	h, err := svc.Create(ctx, CreateInput{Request: createJSON("d2-1", "d2", map[string]any{"content": "a -> b"}), Caller: caller("designer")})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err = svc.Update(ctx, UpdateInput{ItemID: h.ItemID, Caller: caller("designer"), Content: ContentFields{Content: "a -> b -> c"}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	view, _ := svc.Inspect(ctx, h.ItemID)
	if len(view.Revisions) != 2 || !view.Revisions[0].Rendered || !view.Revisions[1].Rendered {
		t.Fatalf("revisions = %+v, want both rendered", view.Revisions)
	}
	rev, file, err := svc.OpenRendered(ctx, h.ItemID, 2)
	if err != nil {
		t.Fatalf("OpenRendered: %v", err)
	}
	defer func() { _ = file.Close() }()
	if rev.MediaType != "image/svg+xml" || !svc.RenderedAvailable("d2") || svc.RenderedAvailable("mermaid") {
		t.Fatalf("revision = %+v", rev)
	}
}

func TestADiagramThatCannotBeDrawnIsRefusedAndNothingIsStored(t *testing.T) {
	bad := &stubRenderer{err: errors.Join(ErrInvalidRequest, errors.New("d2 could not draw this diagram: bad syntax"))}
	svc, _, _ := newTestService(t, WithRenderer("d2", bad))
	ctx := context.Background()
	if _, err := svc.Create(ctx, CreateInput{Request: createJSON("d2-bad", "d2", map[string]any{"content": "a ->"}), Caller: caller("designer")}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Create err = %v", err)
	}
	inbox, _ := svc.Inbox(ctx)
	if inbox.TotalPending != 0 {
		t.Fatalf("a refused diagram left %d items in the inbox", inbox.TotalPending)
	}
}

func TestWithoutARendererTheSourceIsStillAccepted(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	h, err := svc.Create(ctx, CreateInput{Request: createJSON("d2-plain", "d2", map[string]any{"content": "a -> b"}), Caller: caller("designer")})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	view, _ := svc.Inspect(ctx, h.ItemID)
	if view.Revisions[0].Rendered {
		t.Fatal("claims a picture that was never drawn")
	}
	if _, _, err = svc.OpenRendered(ctx, h.ItemID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("OpenRendered err = %v, want not found", err)
	}
}
