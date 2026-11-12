package main

import (
	"context"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"
	substratemcp "github.com/hollis-labs/substrate/modules/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/access"
)

// Tangent does not serve MCP over stdio: no command does, because a second process on the same
// database would run the delivery and reply-mail workers twice and the cross-process
// open-and-migrate lock is not in the framework yet. These tests hold the policy for the day one
// does: the Substrate module's local trust is chosen in code with an explicit scope list that
// leaves the decision scope (and administration) out, never the default "*" (which holds every
// scope, tangent.resolve included), and the module enforces it per tool.
//
// stdioScopes is what a locally launched caller would be given: what a plugin may use and the
// right to reach /mcp.
func stdioScopes() []string { return access.PluginChildScopes() }

// serveStdio runs the module's own stdio server over pipes under local trust with scopes, and
// returns a connected client.
func serveStdio(t *testing.T, tg *tangent, scopes []string) *mcpsdk.ClientSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	serverIn, clientWrites := io.Pipe()
	clientReads, serverOut := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- substratemcp.ServeStdio(ctx, tg.app, substratemcp.StdioOptions{
			In: serverIn, Out: serverOut, LocalTrust: true, LocalScopes: scopes, Notice: io.Discard,
		})
	}()
	t.Cleanup(func() {
		cancel()
		_ = clientWrites.Close()
		_ = serverOut.Close()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the stdio server did not stop")
		}
	})
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "stdio-test", Version: "0"}, nil)
	connectCtx, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	session, err := client.Connect(connectCtx, &mcpsdk.IOTransport{Reader: clientReads, Writer: clientWrites}, nil)
	if err != nil {
		t.Fatalf("connect over stdio: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// refused reports whether a call came back as the fixed refusal the authorizer gives.
func refused(res *mcpsdk.CallToolResult, err error) bool {
	if err != nil {
		return strings.Contains(err.Error(), "forbidden") || strings.Contains(err.Error(), "unknown tool")
	}
	if res == nil || !res.IsError {
		return false
	}
	for _, c := range res.Content {
		if text, ok := c.(*mcpsdk.TextContent); ok && strings.Contains(text.Text, "forbidden") {
			return true
		}
	}
	return false
}

func TestStdioScopesNeverHoldTheDecision(t *testing.T) {
	local := libauth.Principal{Verified: true, Kind: "service", ID: substratemcp.LocalPrincipalID, Scopes: stdioScopes()}
	for _, scope := range []string{access.Resolve, access.Admin} {
		if local.HasScope(scope) {
			t.Errorf("the local stdio principal holds %s", scope)
		}
	}
	// A principal holding "*" (what LocalTrust gives with no LocalScopes) does hold it, which is
	// why LocalScopes is always given explicitly.
	if !(libauth.Principal{Verified: true, Scopes: []string{"*"}}).HasScope(access.Resolve) {
		t.Fatal("the premise changed: a wildcard no longer holds the decision scope")
	}
}

// Under local trust, a call to a tool is refused exactly when its scope is not one of the local
// scopes. Every tool is tried; the ones needing the decision (none today: deciding is the
// browser's, at a user session) would have to be refused, and the check is derived from the scope
// table so a tool added later is covered without anyone remembering it.
func TestStdioUnderLocalTrustRefusesEveryToolOutsideItsScopes(t *testing.T) {
	_, tg := started(t, nil)
	scopes := stdioScopes()
	table := map[string]string{}
	for _, name := range golden(t, "tool_names.txt") {
		scope, ok := tg.toolScope(name)
		if !ok || scope == "" {
			t.Fatalf("tool %s has no scope on the running server", name)
		}
		table[name] = scope
	}

	var decide []string
	for name, scope := range table {
		if scope == access.Resolve || scope == access.Admin {
			decide = append(decide, name)
		}
	}
	sort.Strings(decide)
	t.Logf("tools that need the decision or administration scope: %v", decide)

	session := serveStdio(t, tg, scopes)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, name := range decide {
		res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: map[string]any{}})
		if !refused(res, err) {
			t.Errorf("tool %s, which needs a scope the local stdio caller lacks, was not refused: %v %+v", name, err, res)
		}
	}
	// The control: local trust works, so the refusals above are the scope check and not a dead
	// connection.
	if res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "tangent_workflow_list"}); err != nil || res.IsError {
		t.Fatalf("a view tool under local trust: %v %+v", err, res)
	}

	// The mechanism, with a narrower list: the same server, local scopes {view}. Every tool whose
	// scope is not view is refused and every view tool is not.
	narrow := serveStdio(t, tg, []string{access.View, access.MCP})
	for name, scope := range table {
		if scope == access.View {
			continue
		}
		res, err := narrow.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: map[string]any{}})
		if !refused(res, err) {
			t.Errorf("tool %s (scope %s) was not refused to a local caller holding only view: %v %+v", name, scope, err, res)
		}
	}
}
