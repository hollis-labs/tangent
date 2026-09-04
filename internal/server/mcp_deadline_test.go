package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/envelope"
)

type deadlineTestMCP struct {
	httpHandler http.Handler
	sseHandler  http.Handler
}

func (m deadlineTestMCP) HTTPHandler() http.Handler { return m.httpHandler }
func (m deadlineTestMCP) SSEHandler() http.Handler  { return m.sseHandler }

type deadlineProbeResponseWriter struct {
	header   http.Header
	body     strings.Builder
	now      time.Time
	deadline time.Time
	calls    int
}

func newDeadlineProbeResponseWriter() *deadlineProbeResponseWriter {
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	return &deadlineProbeResponseWriter{
		header:   make(http.Header),
		now:      now,
		deadline: now.Add(httpServerWriteTimeout),
	}
}

func (w *deadlineProbeResponseWriter) Header() http.Header { return w.header }
func (w *deadlineProbeResponseWriter) WriteHeader(int)     {}

func (w *deadlineProbeResponseWriter) Write(p []byte) (int, error) {
	if !w.deadline.IsZero() && w.now.After(w.deadline) {
		return 0, errors.New("simulated response write deadline exceeded")
	}
	return w.body.Write(p)
}

func (w *deadlineProbeResponseWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	w.calls++
	return nil
}

func TestMCPRoutesClearWriteDeadlineBeforeLongRunningHandler(t *testing.T) {
	for _, test := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "streamable HTTP", method: http.MethodPost, path: "/mcp"},
		{name: "legacy SSE", method: http.MethodGet, path: "/sse"},
	} {
		t.Run(test.name, func(t *testing.T) {
			probe := newDeadlineProbeResponseWriter()
			longResponse := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				probe.now = probe.now.Add(httpServerWriteTimeout + time.Second)
				if _, err := w.Write([]byte("completed after operator response")); err != nil {
					t.Errorf("write after original deadline: %v", err)
				}
			})
			srv := newDeadlineTestServer(t, deadlineTestMCP{
				httpHandler: longResponse,
				sseHandler:  longResponse,
			})

			srv.httpS.Handler.ServeHTTP(probe, requestForDeadlineTest(test.method, test.path))

			if probe.calls != 1 || !probe.deadline.IsZero() {
				t.Fatalf("write deadline calls/deadline = %d/%v, want one clear", probe.calls, probe.deadline)
			}
			if got := probe.body.String(); got != "completed after operator response" {
				t.Fatalf("response body = %q", got)
			}
		})
	}
}

func TestOrdinaryHTTPRoutesRetainServerWriteDeadline(t *testing.T) {
	probe := newDeadlineProbeResponseWriter()
	initialDeadline := probe.deadline
	srv := newDeadlineTestServer(t, deadlineTestMCP{
		httpHandler: http.NotFoundHandler(),
		sseHandler:  http.NotFoundHandler(),
	})

	srv.httpS.Handler.ServeHTTP(probe, requestForDeadlineTest(http.MethodGet, "/healthz"))

	if probe.calls != 0 || !probe.deadline.Equal(initialDeadline) {
		t.Fatalf("ordinary route changed write deadline: calls=%d deadline=%v", probe.calls, probe.deadline)
	}
}

func TestLongLivedMCPHandlerReturnsOnRequestCancellation(t *testing.T) {
	started := make(chan struct{})
	handlerReturned := make(chan struct{})
	requestReturned := make(chan struct{})
	longResponse := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(handlerReturned)
	})
	srv := newDeadlineTestServer(t, deadlineTestMCP{
		httpHandler: longResponse,
		sseHandler:  http.NotFoundHandler(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	request := requestForDeadlineTest(http.MethodPost, "/mcp").WithContext(ctx)

	go func() {
		defer close(requestReturned)
		srv.httpS.Handler.ServeHTTP(newDeadlineProbeResponseWriter(), request)
	}()
	<-started
	cancel()

	assertDeadlineTestChannelClosed(t, handlerReturned, "MCP handler did not observe cancellation")
	assertDeadlineTestChannelClosed(t, requestReturned, "MCP request goroutine did not return")
}

func newDeadlineTestServer(t *testing.T, mcp deadlineTestMCP) *Server {
	t.Helper()
	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := New(Config{Envelope: envelopeService, MCP: mcp, Logger: logger})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	return srv
}

func requestForDeadlineTest(method, path string) *http.Request {
	request, err := http.NewRequest(method, "http://127.0.0.1:7842"+path, nil)
	if err != nil {
		panic(err)
	}
	return request
}

func assertDeadlineTestChannelClosed(t *testing.T, channel <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(2 * time.Second):
		t.Fatal(message)
	}
}
