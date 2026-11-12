package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// httphost sets no read or write deadline on the connection (only a header-read and an idle
// timeout), so nothing cuts /mcp while a tool waits. A long-poll for a reply is the longest wait
// the surface has; it outlasts the 30 and 60 seconds the pre-port server cut ordinary connections
// at. The streams and shutdown half of this is in g5_test.go (TestG5StreamOutlivesTimeouts,
// TestG5ShutdownWithOpenConnections).
func TestAnMCPCallThatWaitsLongIsNotCutByAServerDeadline(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out a 35-second tool call")
	}
	h := newHost(t)
	session := h.mcpSession(h.key("poller", "*"))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	started := time.Now()
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "tangent_turns_await",
		Arguments: map[string]any{"session_id": "sess-deadline", "wait_ms": 35000},
	})
	if err != nil || res.IsError {
		t.Fatalf("a call that waited %s failed: %v %s", time.Since(started).Round(time.Second), err, textOf(res))
	}
	if waited := time.Since(started); waited < 30*time.Second {
		t.Fatalf("the call returned after %s; it was meant to wait out the old read deadline", waited.Round(time.Second))
	}
}

// An ordinary route keeps the host's header deadline: a client that starts a request and never
// finishes its headers is dropped rather than held.
func TestAnOrdinaryRouteKeepsTheHeaderReadDeadline(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the ten-second header deadline")
	}
	h := newHost(t)
	conn, err := net.Dial("tcp", strings.TrimPrefix(h.base, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err = fmt.Fprintf(conn, "GET /healthz HTTP/1.1\r\nHost: %s\r\n", strings.TrimPrefix(h.base, "http://")); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_ = conn.SetReadDeadline(started.Add(30 * time.Second))
	buf := make([]byte, 512)
	for {
		if _, err = conn.Read(buf); err != nil {
			break
		}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatal("a request that never finished its headers was held for 30s")
	}
	if held := time.Since(started); held < 5*time.Second || held > 20*time.Second {
		t.Fatalf("the connection was dropped after %s, want about the ten-second header deadline", held.Round(time.Second))
	}
}
