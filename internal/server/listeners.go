package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
)

// EnvProxiedHTTPAddr opts into the always-proxied loopback listener.
const EnvProxiedHTTPAddr = "TANGENT_PROXIED_HTTP_ADDR"

func validateProxiedHTTPAddr(addr string) error {
	if addr == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("%s must be a loopback IP:port", EnvProxiedHTTPAddr)
	}
	if _, portErr := strconv.ParseUint(port, 10, 16); portErr != nil {
		return fmt.Errorf("%s requires a numeric TCP port", EnvProxiedHTTPAddr)
	}
	return nil
}

// ListenerSet owns every socket bound before readiness. Listener is the main
// socket; ProxiedAddr is nil when the optional socket is disabled.
type ListenerSet struct {
	net.Listener
	proxied net.Listener
}

func (l *ListenerSet) ProxiedAddr() net.Addr {
	if l.proxied == nil {
		return nil
	}
	return l.proxied.Addr()
}

// Close also releases the auxiliary socket if Serve has not started yet.
func (l *ListenerSet) Close() error {
	err := l.Listener.Close()
	if l.proxied != nil {
		err = errors.Join(err, l.proxied.Close())
	}
	return err
}

// Listen binds all configured sockets synchronously. Any bind failure releases
// the sockets already acquired, so callers never announce partial readiness.
func (s *Server) Listen() (*ListenerSet, error) {
	main, err := net.Listen("tcp", s.httpS.Addr)
	if err != nil {
		return nil, err
	}
	listeners := &ListenerSet{Listener: main}
	if s.cfg.ProxiedHTTPAddr != "" {
		listeners.proxied, err = net.Listen("tcp", s.cfg.ProxiedHTTPAddr)
		if err != nil {
			_ = main.Close()
			return nil, fmt.Errorf("listen on proxied HTTP address: %w", err)
		}
	}
	return listeners, nil
}

// Serve uses one HTTP server for both sockets, sharing its handler, deadlines
// and graceful shutdown. Custom listeners remain supported when the optional
// proxied address is unset; otherwise pass the complete set returned by Listen.
func (s *Server) Serve(ln net.Listener) error {
	main := ln
	var proxied net.Listener
	if listeners, ok := ln.(*ListenerSet); ok {
		main, proxied = listeners.Listener, listeners.proxied
	}
	if s.cfg.ProxiedHTTPAddr != "" && proxied == nil {
		_ = ln.Close()
		return errors.New("proxied HTTP is configured: Serve requires the set returned by Listen")
	}
	s.logger.Info("tangent listening", "addr", main.Addr().String(), "dev_frontend_url", s.cfg.DevFrontendURL)
	if proxied == nil {
		return s.httpS.Serve(main)
	}
	s.logger.Info("tangent proxied listener", "addr", proxied.Addr().String())
	done := make(chan error, 2)
	go func() { done <- s.httpS.Serve(main) }()
	go func() { done <- s.httpS.Serve(proxiedListener{proxied}) }()
	first := <-done
	// An unexpected accept failure must stop the whole server, not silently
	// leave either listener serving alone. Shutdown already closes both sockets.
	if !errors.Is(first, http.ErrServerClosed) {
		_ = s.httpS.Close()
	}
	second := <-done
	// Do not join ErrServerClosed into an unexpected failure: callers use
	// errors.Is to distinguish normal shutdown from a fatal listener error.
	if !errors.Is(first, http.ErrServerClosed) {
		return first
	}
	return second
}

type proxiedListener struct{ net.Listener }
type proxiedConn struct{ net.Conn }
type proxiedRequestKey struct{}

func (l proxiedListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return proxiedConn{conn}, nil
}

func proxiedConnContext(ctx context.Context, conn net.Conn) context.Context {
	if _, ok := conn.(proxiedConn); ok {
		return context.WithValue(ctx, proxiedRequestKey{}, true)
	}
	return ctx
}

func markProxiedRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if proxied, _ := r.Context().Value(proxiedRequestKey{}).(bool); proxied {
			r = r.Clone(r.Context())
			// This is a listener admission fact. Overwrite caller-supplied values;
			// the existing MCP gate enforces auth while browser/API/WS guards remain.
			r.Header.Set("X-Tangent-Via", "proxied-listener")
		}
		next.ServeHTTP(w, r)
	})
}
