package mcp

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCallerAuthConfiguration(t *testing.T) {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte("test-only-token")))
	for _, config := range []string{"", "tether=" + digest} {
		if _, err := ParseCallerAuth(config, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, config := range []string{
		"secret-value", "gateway:tether=" + digest, "tether=secret-value", "tether=" + digest + ",",
		"tether=" + digest + ",tether=" + digest, "tether=" + digest + ",another=" + digest,
	} {
		if _, err := ParseCallerAuth(config, ""); err == nil || strings.Contains(err.Error(), config) || strings.Contains(err.Error(), "secret-value") {
			t.Fatalf("invalid configuration did not yield a redacted error: %v", err)
		}
	}
}

func TestCallerAuthAdmissionAndBoundIdentity(t *testing.T) {
	const token = "test-only-tether-credential"
	auth, err := ParseCallerAuth(fmt.Sprintf("tether=%x", sha256.Sum256([]byte(token))), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, peer, authorization, proxyHeader string
		want                                   int
	}{
		{"direct IPv4", "127.0.0.1:1234", "", "", 204},
		{"direct IPv6", "[::1]:1234", "", "", 204},
		{"remote with loopback Host", "192.0.2.1:1234", "", "", 401},
		{"unknown peer", "", "", "", 401},
		{"Caddy missing token", "127.0.0.1:1234", "", "X-Tangent-Via", 401},
		{"missing marker forwarded", "127.0.0.1:1234", "", "X-Forwarded-For", 401},
		{"forwarded header", "127.0.0.1:1234", "", "Forwarded", 401},
		{"empty spoofed marker", "127.0.0.1:1234", "", "X-Real-Ip", 401},
		{"bad local token", "127.0.0.1:1234", "Bearer incorrect", "", 401},
		{"bad scheme", "127.0.0.1:1234", "Basic " + token, "", 401},
		{"Caddy bearer", "127.0.0.1:1234", "Bearer " + token, "X-Tangent-Via", 204},
		{"remote bearer", "192.0.2.1:1234", "Bearer " + token, "", 204},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := auth.wrap(func(w http.ResponseWriter, r *http.Request, producer string) {
				if producer != "" {
					if r.Header.Get("Authorization") != "" {
						t.Error("credential reached SDK dispatch")
					}
					for _, binding := range []string{callerIdentity(r.Context()).Scope, declaredCaller(r.Context(), "gateway:victim", "forged").Scope, requesterScope(r.Context(), "operator:local")} {
						if binding != "gateway:tether" {
							t.Errorf("caller assertion changed authenticated scope: %s", binding)
						}
					}
				} else if _, ok := verifiedCaller(r.Context()); ok {
					t.Error("tokenless caller acquired verified identity")
				}
				w.WriteHeader(http.StatusNoContent)
			})
			r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", nil)
			r.RemoteAddr = test.peer
			if test.authorization != "" {
				r.Header.Set("Authorization", test.authorization)
			}
			if test.proxyHeader != "" {
				r.Header.Set(test.proxyHeader, "")
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != test.want {
				t.Fatalf("status = %d, want %d", w.Code, test.want)
			}
			if w.Code == 401 && (w.Header().Get("WWW-Authenticate") == "" || w.Body.String() != "MCP authentication required\n") {
				t.Fatalf("nonuniform refusal: %v %s", w.Header(), w.Body.String())
			}
		})
	}
	// An invalid supplied credential never falls back to local admission.
	for _, values := range [][]string{{""}, {"Bearer " + token, "Bearer " + token}} {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header["Authorization"] = values
		if _, ok := auth.authenticate(r); ok {
			t.Fatal("accepted empty or repeated authorization")
		}
	}
	if requesterScope(context.Background(), "codex") != "standalone-local:codex" {
		t.Fatal("local compatibility changed")
	}
}
