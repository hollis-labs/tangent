package mcp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestStreamableHTTPNegotiatesBelow20260728AndPings is the CW-20261001-0003
// regression: a go-sdk v1.8.0 client negotiated 2026-07-28 against the
// stateless /mcp handler, then sent ping without the per-request _meta the
// server requires, got 400, and Tether's proxy marked Tangent down. With
// 2026-07-28 not advertised, the client settles on 2025-11-25, where ping
// needs no _meta.
func TestStreamableHTTPNegotiatesBelow20260728AndPings(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	srv := httptest.NewServer(rg.mcpSrv.HTTPHandler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "tangent-protocol-test", Version: "v0.0.0"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint: srv.URL, HTTPClient: http.DefaultClient,
	}, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	if got := session.InitializeResult().ProtocolVersion; got != "2025-11-25" {
		t.Fatalf("negotiated protocol = %q, want 2025-11-25", got)
	}
	if pingErr := session.Ping(ctx, &mcpsdk.PingParams{}); pingErr != nil {
		t.Fatalf("ping: %v", pingErr)
	}
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(listed.Tools) == 0 {
		t.Fatal("ListTools returned no tools")
	}
}
