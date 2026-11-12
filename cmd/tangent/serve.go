package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/identity"

	"github.com/hollis-labs/tangent/internal/hostguard"
	"github.com/hollis-labs/tangent/internal/server"
	"github.com/hollis-labs/tangent/internal/settings"
	"github.com/hollis-labs/tangent/internal/webui"
)

// tangent is what the commands share: the started framework app. ready, when set, learns the
// bound address (tests listen on port 0).
type tangent struct {
	app    *module.App
	ready  func(net.Addr)
	getenv func(string) string // os.Getenv when nil

	// table is the route table of the running server (set once composed).
	table atomic.Pointer[server.Table]
}

func (t *tangent) serveCmd(ctx context.Context) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Serve Tangent: the SPA, its API and the MCP tools",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return t.serve(ctx) },
	}
}

// mountSpec is an extra mount with the scope that guards it (tests; the app's own mounts come
// from the packages that own them).
type mountSpec struct {
	httphost.Mount
	Method, Scope string
}

// serve runs the HTTP host until ctx ends.
func (t *tangent) serve(ctx context.Context) error { return t.serveWith(ctx, nil) }

func (t *tangent) serveWith(ctx context.Context, extra []mountSpec) error {
	app := t.app
	cfg, err := settings.Parse(app.Config[settings.Key])
	if err != nil {
		return err
	}
	if err = requireLoopback(app); err != nil {
		return err
	}
	_, table, closer, err := t.compose(cfg)
	if err != nil {
		return err
	}
	t.table.Store(table)
	// Stopped before the database module closes the handle (the caller's Stop).
	defer func() {
		if cerr := closer.Close(); cerr != nil {
			slog.Warn("stopping the services", "error", cerr)
		}
	}()
	// Every route has a scope, and the guard reads it from the scope table.
	scopes := auth.ScopeTable(app)
	for _, route := range table.Routes() {
		scopes.Route(route.Method(), route.ScopePattern(), route.Scope)
	}
	mounts := make([]httphost.Mount, 0, len(extra))
	for _, route := range table.Outside() {
		mounts = append(mounts, httphost.Mount{Pattern: route.Pattern, Handler: route.Handler})
	}
	for _, m := range extra {
		if !m.Open {
			scopes.Route(m.Method, scopePath(m.Pattern), m.Scope)
		}
		mounts = append(mounts, m.Mount)
	}
	handler, err := t.handler(cfg, table.API(), mounts)
	if err != nil {
		return err
	}
	return httphost.Serve(ctx, app, handler, httphost.ServeOptions{
		Ready: func(addr net.Addr) {
			slog.Info("tangent listening", "addr", addr.String())
			if t.ready != nil {
				t.ready(addr)
			}
		},
	})
}

// scopePath is the path part of a mount pattern ("GET /ws" -> "/ws").
func scopePath(pattern string) string {
	if _, p, ok := strings.Cut(pattern, " "); ok {
		return strings.TrimSpace(p)
	}
	return pattern
}

// handler assembles the chain every request passes. Outermost first: the Host check, then the
// session cookie becomes the bearer the guard verifies, then the host (guard, routes, SPA).
func (t *tangent) handler(cfg settings.Settings, api http.Handler, mounts []httphost.Mount) (http.Handler, error) {
	id := identity.From(t.app)
	if id == nil {
		return nil, errors.New("the identity module must be started")
	}
	assets, err := webui.Assets()
	if err != nil {
		return nil, err
	}
	mux, _ := api.(*http.ServeMux)
	if mux == nil {
		mux = http.NewServeMux()
		mux.Handle("/", api)
	}
	h, err := httphost.Handler(t.app, httphost.Options{Assets: assets, Routes: mux, Mounts: mounts})
	if err != nil {
		return nil, err
	}
	return hostguard.Handler(cfg.AllowedHosts, id.Middleware(h)), nil
}

// requireLoopback keeps the backend on the loopback interface: the Host check assumes it, and a
// remote client reaches it through an authenticated tunnel or a local TLS proxy.
func requireLoopback(app *module.App) error {
	cfg, _ := app.Config["httphost"].(map[string]any)
	addr, _ := cfg["addr"].(string)
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("tangent requires a loopback httphost.addr (for example 127.0.0.1:7842), got %q", addr)
}
