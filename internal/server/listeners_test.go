package server

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProxiedHTTPAddressValidation(t *testing.T) {
	for _, addr := range []string{"", "127.0.0.1:7844", "[::1]:7844", "127.0.0.1:0"} {
		if err := validateProxiedHTTPAddr(addr); err != nil {
			t.Errorf("valid address %q: %v", addr, err)
		}
	}
	for _, addr := range []string{":7844", "0.0.0.0:7844", "100.112.71.40:7844", "localhost:7844", "[::]:7844", "127.0.0.1", "127.0.0.1:http", "127.0.0.1:-1", "127.0.0.1:65536"} {
		if _, err := New(Config{ProxiedHTTPAddr: addr}); err == nil || !strings.Contains(err.Error(), EnvProxiedHTTPAddr) {
			t.Errorf("invalid address %q: %v", addr, err)
		}
	}
}

func listenerTestServer(addr string) *Server {
	return &Server{
		cfg:    Config{ProxiedHTTPAddr: addr},
		logger: slog.New(slog.DiscardHandler),
		httpS: &http.Server{
			Addr:              "127.0.0.1:0",
			ReadHeaderTimeout: time.Second,
			ConnContext:       proxiedConnContext,
			Handler: markProxiedRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, html.EscapeString(r.Header.Get("X-Tangent-Via")))
			})),
		},
	}
}

func TestProxiedListenersLifecycle(t *testing.T) {
	for _, mode := range []string{"disabled", "shutdown", "accept failure"} {
		t.Run(mode, func(t *testing.T) {
			addr := "127.0.0.1:0"
			if mode == "disabled" {
				addr = ""
			}
			srv := listenerTestServer(addr)
			ln, err := srv.Listen()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ln.Close(); _ = srv.httpS.Close() })
			done := make(chan error, 1)
			go func() { done <- srv.Serve(ln) }()
			client := &http.Client{Timeout: 5 * time.Second}
			defer client.CloseIdleConnections()
			check := func(addr net.Addr, want string) {
				t.Helper()
				req, _ := http.NewRequest(http.MethodGet, "http://"+addr.String(), nil)
				req.Header.Set("X-Tangent-Via", "caller-value")
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if string(body) != want {
					t.Fatalf("marker = %q, want %q", body, want)
				}
			}
			check(ln.Addr(), "caller-value")
			if mode == "disabled" {
				if ln.ProxiedAddr() != nil {
					t.Fatal("optional socket unexpectedly enabled")
				}
			} else {
				check(ln.ProxiedAddr(), "proxied-listener")
			}
			if mode == "accept failure" {
				_ = ln.proxied.Close()
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := srv.Shutdown(ctx); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if mode == "accept failure" && (err == nil || errors.Is(err, http.ErrServerClosed)) {
					t.Fatalf("accept failure reported as normal shutdown: %v", err)
				}
				if mode != "accept failure" && !errors.Is(err, http.ErrServerClosed) {
					t.Fatalf("Serve = %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Serve did not stop both listeners")
			}
			for _, addr := range []net.Addr{ln.Addr(), ln.ProxiedAddr()} {
				if addr == nil {
					continue
				}
				conn, err := net.DialTimeout("tcp", addr.String(), time.Second)
				if err == nil {
					_ = conn.Close()
					t.Errorf("socket %s still accepts after Serve returned", addr)
				}
			}
		})
	}
}

func TestProxiedListenerBindFailure(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = occupied.Close() }()
	srv := listenerTestServer(occupied.Addr().String())
	if ln, err := srv.Listen(); err == nil {
		_ = ln.Close()
		t.Fatal("Listen accepted occupied proxied address")
	}
}

func TestProxiedListenersDrainBothSockets(t *testing.T) {
	srv := listenerTestServer("127.0.0.1:0")
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	srv.httpS.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	})
	ln, err := srv.Listen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close(); _ = srv.httpS.Close() })
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ln) }()
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	responses := make(chan error, 2)
	for _, addr := range []net.Addr{ln.Addr(), ln.ProxiedAddr()} {
		go func() {
			resp, err := client.Get("http://" + addr.String())
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusNoContent {
					err = fmt.Errorf("status = %d", resp.StatusCode)
				}
			}
			responses <- err
		}()
	}
	// Always release handlers before cleanup, even on a failed assertion.
	var once sync.Once
	defer once.Do(func() { close(release) })
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("request did not reach handler")
		}
	}
	shuttingDown := make(chan struct{})
	srv.httpS.RegisterOnShutdown(func() { close(shuttingDown) })
	shutdownDone := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { shutdownDone <- srv.Shutdown(ctx) }()
	<-shuttingDown
	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown returned before draining: %v", err)
	default:
	}
	once.Do(func() { close(release) })
	for range 2 {
		if err := <-responses; err != nil {
			t.Error(err)
		}
	}
	if err := <-shutdownDone; err != nil {
		t.Fatal(err)
	}
	if err := <-serveDone; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Serve = %v", err)
	}
}
