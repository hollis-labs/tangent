package artifacts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Renderer turns an artifact type's source into a picture on the server. A type with no renderer is
// still accepted and shown as source; a type with one is rendered when it is created, so a syntax
// error reaches the agent at once instead of the operator as a blank preview.
type Renderer interface {
	// Render returns the image bytes and their media type, or ErrInvalidRequest wrapping the reason
	// the source could not be drawn.
	Render(ctx context.Context, source []byte) ([]byte, string, error)
}

var renderTimeout = 15 * time.Second

// renderTimeoutForTest swaps the render timeout and returns the old one.
func renderTimeoutForTest(d time.Duration) time.Duration {
	old := renderTimeout
	renderTimeout = d
	return old
}

const (
	renderMaxOutput = 8 << 20
	renderMaxStderr = 4 << 10

	// renderedFilename is the picture stored beside a revision's source.
	renderedFilename = "rendered.svg"
)

// D2Renderer draws D2 with the `d2` command line tool. D2 has no safe in-browser build (its WASM
// needs `wasm-unsafe-eval` and blob workers for the whole app), so the server runs the binary.
type D2Renderer struct {
	bin string
}

// NewD2Renderer returns a renderer for the d2 binary at bin, or found on PATH when bin is empty. It
// returns nil when there is none, and D2 artifacts then show their source.
func NewD2Renderer(bin string) *D2Renderer {
	if bin == "" {
		bin = "d2"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil
	}
	return &D2Renderer{bin: path}
}

// Bin is the resolved path of the binary, for logs.
func (r *D2Renderer) Bin() string { return r.bin }

// Render runs d2 on source from stdin to stdout, in an empty directory with no inherited
// environment, under a timeout and an output cap.
func (r *D2Renderer) Render(ctx context.Context, source []byte) ([]byte, string, error) {
	dir, err := os.MkdirTemp("", "tangent-d2-")
	if err != nil {
		return nil, "", fmt.Errorf("artifacts: d2 scratch directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	ctx, cancel := context.WithTimeout(ctx, renderTimeout)
	defer cancel()
	// "-" reads stdin and writes stdout. No file path means nothing for an import to resolve against.
	cmd := exec.CommandContext(ctx, r.bin, "--pad", "20", "-", "-") // #nosec G204 -- bin is the operator-configured d2 path, arguments are fixed
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "TMPDIR=" + dir}
	confine(cmd)
	cmd.Stdin = bytes.NewReader(source)
	var out limitedBuffer
	out.limit = renderMaxOutput
	var stderr limitedBuffer
	stderr.limit = renderMaxStderr
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err = cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, "", fmt.Errorf("%w: d2 took longer than %s to draw this diagram", ErrInvalidRequest, renderTimeout)
		}
		if out.exceeded {
			return nil, "", fmt.Errorf("%w: the drawn diagram is larger than %d bytes", ErrTooLarge, renderMaxOutput)
		}
		reason := strings.TrimSpace(stderr.buf.String())
		if reason == "" {
			reason = err.Error()
		}
		return nil, "", fmt.Errorf("%w: d2 could not draw this diagram: %s", ErrInvalidRequest, firstLines(reason, 4))
	}
	if out.exceeded {
		return nil, "", fmt.Errorf("%w: the drawn diagram is larger than %d bytes", ErrTooLarge, renderMaxOutput)
	}
	if !bytes.Contains(out.buf.Bytes(), []byte("<svg")) {
		return nil, "", fmt.Errorf("%w: d2 produced no picture", ErrInvalidRequest)
	}
	return out.buf.Bytes(), "image/svg+xml", nil
}

// limitedBuffer keeps at most limit bytes and records that it was asked for more.
type limitedBuffer struct {
	buf      bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := b.limit - b.buf.Len()
	if room <= 0 {
		b.exceeded = true
		return len(p), nil
	}
	if len(p) > room {
		b.buf.Write(p[:room])
		b.exceeded = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

func firstLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "; ")
}

// RenderedAvailable reports whether this service draws the given type on the server.
func (s *Service) RenderedAvailable(artifactType string) bool {
	return s.renderers[artifactType] != nil
}
