package server

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestLongLivedMCPHandlerSurvivesServerReadTimeout runs the real
// longLivedMCPHandler inside a real http.Server whose read timeout is
// compressed to a few hundred milliseconds. Before the read deadline was
// cleared, the server tore the connection down mid-stream and the MCP session
// id died with it — which is what made mux_catalog_refresh hang and the next
// call answer "session not found". The compressed timeout keeps the regression
// deterministic instead of depending on the 30s production constant.
func TestLongLivedMCPHandlerSurvivesServerReadTimeout(t *testing.T) {
	const readTimeout = 150 * time.Millisecond

	stream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("response writer is not a flusher")
			return
		}
		for event := range 4 {
			if _, err := fmt.Fprintf(w, "event: message\ndata: %d\n\n", event); err != nil {
				t.Errorf("write event %d after %s: %v", event, readTimeout, err)
				return
			}
			flusher.Flush()
			select {
			case <-time.After(readTimeout):
			case <-r.Context().Done():
				return
			}
		}
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	httpServer := &http.Server{
		Handler:           longLivedMCPHandler(stream),
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       readTimeout,
		WriteTimeout:      readTimeout,
	}
	go func() { _ = httpServer.Serve(listener) }()
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String()+"/sse", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("open sse stream: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	// Reading the fourth event proves the stream stayed alive well past the
	// server-wide read timeout that used to close it.
	reader := bufio.NewReader(response.Body)
	seen := 0
	for seen < 4 {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("sse stream closed after %d events: %v", seen, readErr)
		}
		if strings.HasPrefix(line, "data: ") {
			seen++
		}
	}
}
