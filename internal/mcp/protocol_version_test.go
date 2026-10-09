package mcp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gomcpclient "github.com/hollis-labs/libs/plugin-mcp/go-mcp/client"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestStreamableHTTPNegotiates20260728AndGoMCPClientPings covers the
// CW-20261001-0078 follow-up to CW-20261001-0003. Tangent advertises
// protocol 2026-07-28 again, and a go-mcp v0.14.1 client -- the version
// Tether's proxy uses -- connects, pings and lists tools against the
// stateless /mcp handler. go-sdk v1.8.0's bare ping still lacks the SEP-2575
// request _meta and the server still answers it with -32602; go-mcp v0.14.1
// counts that reply as reachable.
func TestStreamableHTTPNegotiates20260728AndGoMCPClientPings(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	srv := httptest.NewServer(rg.mcpSrv.HTTPHandler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// The official SDK client negotiates the newest version the server offers.
	sdk := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "tangent-protocol-test", Version: "v0.0.0"}, nil)
	session, err := sdk.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: http.DefaultClient}, nil)
	if err != nil {
		t.Fatalf("sdk client.Connect: %v", err)
	}
	defer func() { _ = session.Close() }()
	if got := session.InitializeResult().ProtocolVersion; got != "2026-07-28" {
		t.Fatalf("negotiated protocol = %q, want 2026-07-28", got)
	}

	// A go-mcp v0.14.1 client, as Tether's proxy dials it.
	pool := gomcpclient.NewPool(gomcpclient.WithIdentity("tangent-protocol-test", "v0.0.0"))
	defer func() { _ = pool.Close() }()
	if regErr := pool.Register("tangent", gomcpclient.ServerConfig{Transport: "http", URL: srv.URL}); regErr != nil {
		t.Fatalf("register: %v", regErr)
	}
	c, err := pool.Get("tangent")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if pingErr := c.Ping(ctx); pingErr != nil {
		t.Fatalf("go-mcp client ping: %v", pingErr)
	}
	listed, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("go-mcp client ListTools: %v", err)
	}
	if len(listed.Tools) == 0 {
		t.Fatal("ListTools returned no tools")
	}
}
