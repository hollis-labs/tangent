package mcp

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testForwardedSession = "01234567-89ab-4cde-8fab-0123456789ab"

func TestTrustedForwarderConfiguration(t *testing.T) {
	tokens := fmt.Sprintf("alpha=%x,beta=%x", sha256.Sum256([]byte("alpha")), sha256.Sum256([]byte("beta")))
	for _, config := range []string{"", "alpha", "alpha,beta"} {
		if _, err := ParseCallerAuth(tokens, config); err != nil {
			t.Fatal(err)
		}
	}
	for _, config := range []string{"unknown", "alpha,", "alpha,alpha", "gateway:alpha", "secret-label"} {
		if _, err := ParseCallerAuth(tokens, config); err == nil || strings.Contains(err.Error(), config) {
			t.Fatalf("non-redacted configuration error: %v", err)
		}
	}
	if _, err := ParseCallerAuth("", "alpha"); err == nil {
		t.Fatal("trusted a producer without credentials")
	}
}

func TestTrustedForwarderAdmission(t *testing.T) {
	tokens := fmt.Sprintf("alpha=%x,beta=%x", sha256.Sum256([]byte("alpha")), sha256.Sum256([]byte("beta")))
	auth, err := ParseCallerAuth(tokens, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	valid := func() http.Header {
		return http.Header{
			"X-Forwarded-User-Id": {"session:" + testForwardedSession}, "X-Tether-Session-Id": {testForwardedSession},
		}
	}
	for _, tc := range []struct {
		name, token, scope string
		headers            http.Header
		status             int
	}{
		{"trusted", "alpha", "gateway:alpha/session:" + testForwardedSession, valid(), 204},
		{"parent", "alpha", "gateway:alpha", nil, 204},
		{"untrusted cannot impersonate", "beta", "gateway:beta", valid(), 204},
		{"untrusted malformed ignored", "beta", "gateway:beta", http.Header{"X-Tether-Session-Id": {"secret-label"}}, 204},
		{"unauthenticated", "", "", valid(), 401},
		{"bad credential first", "bad", "", http.Header{"X-Tether-Session-Id": {"secret-label"}}, 401},
		{"only session", "alpha", "", http.Header{"X-Tether-Session-Id": {testForwardedSession}}, 400},
		{"only user", "alpha", "", http.Header{"X-Forwarded-User-Id": {"session:" + testForwardedSession}}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := auth.wrap(func(w http.ResponseWriter, r *http.Request, scope string) {
				if scope != tc.scope || requesterScope(r.Context(), "gateway:victim") != tc.scope {
					t.Errorf("effective scope=%q want %q", scope, tc.scope)
				}
				for name := range r.Header {
					if strings.EqualFold(name, "Authorization") || strings.HasPrefix(strings.ToLower(name), "x-tether-") || strings.EqualFold(name, forwardedUserHeader) {
						t.Errorf("raw identity reached SDK: %s", name)
					}
				}
				w.WriteHeader(http.StatusNoContent)
			})
			r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", nil)
			r.RemoteAddr = "127.0.0.1:1234"
			if tc.headers != nil {
				r.Header = tc.headers.Clone()
			}
			if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d", w.Code, tc.status)
			}
			if w.Code == 400 && w.Body.String() != "Invalid forwarded caller context\n" {
				t.Fatalf("nonuniform failure: %s", w.Body.String())
			}
		})
	}
	// Every malformed trusted assertion fails closed, without echoed values.
	for _, mutate := range []func(http.Header){
		func(h http.Header) { h.Add(forwardedSessionHeader, testForwardedSession) },
		func(h http.Header) { h.Add(forwardedUserHeader, "session:"+testForwardedSession) },
		func(h http.Header) { h.Set(forwardedSessionHeader, strings.ToUpper(testForwardedSession)) },
		func(h http.Header) { h.Set(forwardedSessionHeader, strings.Repeat("a", 513)) },
		func(h http.Header) { h.Set(forwardedSessionHeader, "") },
		func(h http.Header) { h.Set(forwardedSessionHeader, "11234567-89ab-4cde-8fab-0123456789ab") },
		func(h http.Header) { h.Set(forwardedUserHeader, "operator:"+testForwardedSession) },
		func(h http.Header) { h.Set(forwardedUserHeader, " session:"+testForwardedSession) },
		func(h http.Header) { h.Set("X-Tether-Agent-Urn", "contains space") },
		func(h http.Header) { h.Set("X-Tether-Project-Id", strings.Repeat("a", 513)) },
		func(h http.Header) { h.Set("X-Tether-Launch-Id", "\x7f") },
		func(h http.Header) { h.Add("X-Tether-Logical-Agent-Id", "a"); h.Add("X-Tether-Logical-Agent-Id", "b") },
	} {
		h := valid()
		mutate(h)
		handler := auth.wrap(func(http.ResponseWriter, *http.Request, string) { t.Error("invalid identity dispatched") })
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", nil)
		r.Header = h
		r.Header.Set("Authorization", "Bearer alpha")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 400 || w.Body.String() != "Invalid forwarded caller context\n" {
			t.Fatalf("malformed headers: %d %s", w.Code, w.Body.String())
		}
	}
}
