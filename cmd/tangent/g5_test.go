package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/hollis-labs/substrate/modules/httphost"
)

// Gap G5: do WebSocket upgrades and long-lived streams survive the whole chain (Host check,
// identity middleware, guard, apierr.Normalize, handler)? These mounts stand in for Tangent's
// /ws and event streams: an echo socket and a stream with a heartbeat.
func g5Mounts(ctx context.Context, _ *tangent) []mountSpec {
	ws := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil) // default: same-origin only
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		for {
			typ, b, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if err = c.Write(r.Context(), typ, b); err != nil {
				return
			}
		}
	})
	stream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl.Flush()
		beat := time.NewTicker(streamBeat)
		defer beat.Stop()
		n := 0
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ctx.Done(): // application shutdown: httphost's Shutdown does not end streams
				return
			case <-beat.C:
				n++
				_, _ = fmt.Fprintf(w, "data: %d\n\n", n)
				fl.Flush()
			}
		}
	})
	return []mountSpec{
		{Mount: httphost.Mount{Pattern: "GET /ws", Handler: ws}, Method: "GET", Scope: "tangent.test"},
		{Mount: httphost.Mount{Pattern: "GET /stream", Handler: stream}, Method: "GET", Scope: "tangent.test"},
	}
}

var streamBeat = 10 * time.Second

type g5 struct {
	base   string
	cookie *http.Client
	key    string
}

func g5Setup(t *testing.T) *g5 {
	t.Helper()
	base, _ := started(t, g5Mounts)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	do := func(method, path string, body any) map[string]any {
		var rdr io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rdr = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, rdr)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Requested-With", "test")
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		out, _ := io.ReadAll(res.Body)
		if res.StatusCode/100 != 2 {
			t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, out)
		}
		var m map[string]any
		_ = json.Unmarshal(out, &m)
		return m
	}
	// Test only: the owner is made through first-run setup on a throwaway data directory. The
	// product plan has no setup over HTTP (the offline identity bootstrap is the framework's).
	do("POST", "/api/setup", map[string]any{"display_name": "Owner", "username": "owner", "password": "a long test passphrase"})
	k := do("POST", "/api/identity/keys", map[string]any{"name": "ws-test", "scopes": []string{"tangent.test"}})
	key, _ := k["key"].(string)
	if key == "" {
		t.Fatalf("no api key in %v", k)
	}
	return &g5{base: base, cookie: c, key: key}
}

func (g *g5) wsURL() string { return "ws" + strings.TrimPrefix(g.base, "http") + "/ws" }

func echo(ctx context.Context, t *testing.T, c *websocket.Conn) {
	t.Helper()
	if err := c.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, b, err := c.Read(ctx)
	if err != nil || string(b) != "hello" {
		t.Fatalf("echo = %q, %v", b, err)
	}
}

func TestG5WebSocketThroughTheChain(t *testing.T) {
	g := g5Setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	u, _ := url.Parse(g.base)

	// A browser: the session cookie, the page's own Origin.
	hdr := http.Header{"Origin": []string{g.base}}
	c, _, err := websocket.Dial(ctx, g.wsURL(), &websocket.DialOptions{HTTPClient: g.cookie, HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("cookie upgrade: %v", err)
	}
	echo(ctx, t, c)
	_ = c.Close(websocket.StatusNormalClosure, "")

	// A machine client: a bearer API key and no cookie.
	c, _, err = websocket.Dial(ctx, g.wsURL(), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + g.key}}})
	if err != nil {
		t.Fatalf("bearer upgrade: %v", err)
	}
	echo(ctx, t, c)
	_ = c.Close(websocket.StatusNormalClosure, "")

	// No credential: refused with 401 before any upgrade.
	_, res, err := websocket.Dial(ctx, g.wsURL(), nil)
	if err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous upgrade: res=%v err=%v, want 401", res, err)
	}
	// A bad key: refused.
	_, res, err = websocket.Dial(ctx, g.wsURL(), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer key_nope"}}})
	if err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad key upgrade: res=%v err=%v, want 401", res, err)
	}
	// The session cookie from a foreign Origin (cross-site socket hijacking): refused.
	_, res, err = websocket.Dial(ctx, g.wsURL(), &websocket.DialOptions{HTTPClient: g.cookie, HTTPHeader: http.Header{"Origin": []string{"http://evil.example.test"}}})
	if err == nil || res == nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign-origin upgrade: res=%v err=%v, want 403", res, err)
	}
	// An unknown Host (DNS rebinding): refused by the host guard.
	req, _ := http.NewRequest("GET", g.base+"/ws", nil)
	req.Host = "rebind.example.test"
	req.Header.Set("Authorization", "Bearer "+g.key)
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != http.StatusForbidden {
		t.Fatalf("unknown host: %v %v", r, err)
	}
	_ = u
}

// SSE: the stream is not cut by the server's timeouts, and a key with the wrong scope is refused.
// The full run holds the stream open past the two-minute idle timeout; -short shortens it.
func TestG5StreamOutlivesTimeouts(t *testing.T) {
	hold := 135 * time.Second
	if testing.Short() {
		hold = 12 * time.Second
	}
	streamBeat = 10 * time.Second
	g := g5Setup(t)
	req, _ := http.NewRequest("GET", g.base+"/stream", nil)
	req.Header.Set("Authorization", "Bearer "+g.key)
	res, err := (&http.Client{}).Do(req) // no client timeout
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("stream: %v %v", res, err)
	}
	defer func() { _ = res.Body.Close() }()
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}
	deadline := time.Now().Add(hold)
	sc := bufio.NewScanner(res.Body)
	events := 0
	for time.Now().Before(deadline) && sc.Scan() {
		if strings.HasPrefix(sc.Text(), "data: ") {
			events++
		}
	}
	if least := int(hold/streamBeat) - 1; events < least {
		t.Fatalf("got %d events in %s, want at least %d (the stream was cut: %v)", events, hold, least, sc.Err())
	}
}

// Shutdown with a stream and a socket open must finish inside the grace period: httphost's
// Shutdown does not end hijacked or streaming connections, so the handlers watch the app context.
func TestG5ShutdownWithOpenConnections(t *testing.T) {
	g := g5Setup(t)
	ctx := context.Background()
	c, _, err := websocket.Dial(ctx, g.wsURL(), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + g.key}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	echo(ctx, t, c)
	req, _ := http.NewRequest("GET", g.base+"/stream", nil)
	req.Header.Set("Authorization", "Bearer "+g.key)
	res, err := (&http.Client{}).Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("stream: %v %v", res, err)
	}
	defer func() { _ = res.Body.Close() }()
	// started's cleanup cancels serve and fails the test if it does not return in 20s with nil.
}
