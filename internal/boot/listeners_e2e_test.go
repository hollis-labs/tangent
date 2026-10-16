package boot

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Exercise the production Boot -> Listen -> Serve path on real sockets, with
// the actual MCP admission and participant/WebSocket handlers on both ports.
func TestProxiedListenerAdmissionAndBrowser(t *testing.T) {
	const token = "synthetic-listener-test-credential"
	root := t.TempDir()
	svc, srv, closer, err := Boot(Config{
		DBPath: filepath.Join(root, "tangent.db"), PluginDir: filepath.Join(root, "plugins"),
		OperatorToken: e2eOperatorToken, Logger: slog.New(slog.DiscardHandler),
		ProxiedHTTPAddr: "127.0.0.1:0", MCPCallerTokens: fmt.Sprintf("gateway=%x", sha256.Sum256([]byte(token))),
	})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := srv.Listen()
	if err != nil {
		_ = closer.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		<-done
		_ = closer.Close()
	})
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	for _, port := range []struct {
		name, base string
		proxied    bool
	}{
		{"main", "http://" + ln.Addr().String(), false},
		{"proxied", "http://" + ln.ProxiedAddr().String(), true},
	} {
		t.Run(port.name, func(t *testing.T) {
			for _, route := range []struct{ method, path string }{
				{http.MethodPost, "/mcp"}, {http.MethodGet, "/sse"}, {http.MethodPost, "/sse"},
			} {
				path := route.path
				for _, credential := range []string{"", "invalid", token} {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					method := route.method
					req, _ := http.NewRequestWithContext(ctx, method, port.base+path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Accept", "application/json, text/event-stream")
					if credential != "" {
						req.Header.Set("Authorization", "Bearer "+credential)
					}
					// No proxy headers: the auxiliary socket must enforce auth itself.
					resp, err := client.Do(req)
					if err != nil {
						cancel()
						t.Fatal(err)
					}
					want := http.StatusOK
					if credential == "invalid" || (credential == "" && port.proxied) {
						want = http.StatusUnauthorized
					} else if path == "/sse" && method == http.MethodPost {
						want = http.StatusBadRequest
					} // admitted, but no session ID
					if resp.StatusCode != want {
						t.Errorf("%s credential=%t: status %d, want %d", path, credential != "", resp.StatusCode, want)
					}
					if want == http.StatusUnauthorized && resp.Header.Get("WWW-Authenticate") == "" {
						t.Error("no bearer challenge")
					}
					cancel()
					_ = resp.Body.Close()
				}
			}
			app := &e2eApp{baseURL: port.base, svc: svc}
			cookie := app.elevatedSession(t)
			req, _ := http.NewRequest(http.MethodGet, port.base+"/api/hitl", nil)
			req.AddCookie(cookie)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("browser API = %d", resp.StatusCode)
			}
			rm, err := svc.RoomManager.CreateWithError(nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(port.base, "http")+"/ws?roomID="+rm.ID, &websocket.DialOptions{
				HTTPHeader: http.Header{"Cookie": {cookie.String()}, "Origin": {port.base}},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.CloseNow() }()
			if _, _, err := conn.Read(ctx); err != nil {
				t.Fatalf("WebSocket first frame: %v", err)
			}
		})
	}
}
