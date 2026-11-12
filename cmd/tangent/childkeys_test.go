package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/modules/identity"

	"github.com/hollis-labs/tangent/internal/access"
	"github.com/hollis-labs/tangent/internal/childkeys"
	"github.com/hollis-labs/tangent/pkg/plugin/hostclient"
)

// A plugin child's key, end to end on the real stack: the host mints it through the identity
// service, the child's client presents it from the file, /mcp serves what a plugin may use and
// nothing that decides, a renewal reaches the next call, and stopping the child revokes the key.
func TestAPluginChildCallsBackWithItsOwnKeyAndLosesItWhenItStops(t *testing.T) {
	h := newHost(t)
	app := h.tg.app
	issuer, err := newKeyIssuer(app, identity.From(app))
	if err != nil {
		t.Fatal(err)
	}
	leases, err := childkeys.New(issuer, access.PluginChildScopes(), 10*time.Minute, 150*time.Millisecond, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	lease, env, err := leases.Open(context.Background(), "tangent.runner", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var keyFile string
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, hostclient.KeyFileEnv+"="); ok {
			keyFile = value
		}
	}
	if keyFile == "" {
		t.Fatalf("no key file in the child's environment: %v", env)
	}
	t.Setenv(hostclient.URLEnv, h.base+"/mcp")
	t.Setenv(hostclient.KeyFileEnv, keyFile)
	t.Setenv(hostclient.KeyEnv, "")

	child, err := hostclient.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A view tool, which a plugin may use.
	result, err := child.CallTool(ctx, "tangent_workflow_list", nil)
	if err != nil || result.IsError {
		t.Fatalf("the child was refused a view tool: %v %+v", err, result)
	}
	// The key is a service key, not a person: every deciding route refuses it.
	first, _ := os.ReadFile(keyFile) // #nosec G304 -- a file in this test's temp directory.
	key := strings.TrimSpace(string(first))
	for _, route := range h.tg.table.Load().Routes() {
		if route.UserOnly {
			if got := h.call(key, route.Method(), concrete(route.Path())); got != http.StatusForbidden {
				t.Errorf("%s with a plugin child's key = %d, want 403", route.Pattern, got)
			}
		}
	}
	// A key listing it holds no scope for: it is not an administrator.
	if got := h.call(key, http.MethodGet, "/api/identity/keys"); got != http.StatusForbidden {
		t.Errorf("GET /api/identity/keys with a plugin child's key = %d, want 403", got)
	}

	// Renewal: the file changes while the child lives and the new key works.
	deadline := time.Now().Add(10 * time.Second)
	for {
		now, _ := os.ReadFile(keyFile) // #nosec G304 -- a file in this test's temp directory.
		if string(now) != string(first) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the key was never renewed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if result, err = child.CallTool(ctx, "tangent_workflow_list", nil); err != nil || result.IsError {
		t.Fatalf("the child was refused after a renewal: %v %+v", err, result)
	}

	// Stopping the child revokes its keys (including the one an older request still holds).
	last, _ := os.ReadFile(keyFile) // #nosec G304 -- a file in this test's temp directory.
	lease.Close()
	for name, secret := range map[string]string{"first": key, "last": strings.TrimSpace(string(last))} {
		if got := h.call(secret, http.MethodGet, "/api/health/ready"); got != http.StatusUnauthorized {
			t.Errorf("the %s key after the child stopped = %d, want 401", name, got)
		}
	}
	if _, err = os.Stat(keyFile); !os.IsNotExist(err) {
		t.Error("the key file outlived the child")
	}
}

// The host revokes a plugin child's key through the identity module's own DELETE handler, found by
// exact method and path. If the module moves or renames it, this fails instead of the first
// revocation failing at run time.
func TestTheKeyRevocationRouteIsStillPublished(t *testing.T) {
	_, tg := started(t, nil)
	if _, err := newKeyIssuer(tg.app, identity.From(tg.app)); err != nil {
		t.Fatalf("%v: the identity module's revoke route changed (%s %s)", err, http.MethodDelete, revokeRoute)
	}
}
