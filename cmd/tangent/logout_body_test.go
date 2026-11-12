package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRefuseLogoutBody(t *testing.T) {
	var reached int
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached++; w.WriteHeader(http.StatusNoContent) })
	handler := refuseLogoutBody(next)

	serve := func(method, path, body string) int {
		var reader *strings.Reader
		request := httptest.NewRequest(method, path, nil)
		if body != "" {
			reader = strings.NewReader(body)
			request = httptest.NewRequest(method, path, reader)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code
	}
	if code := serve(http.MethodPost, "/api/logout", `{"room_id":"r","envelope_id":"e"}`); code != http.StatusBadRequest || reached != 0 {
		t.Fatalf("logout with a body = %d, reached %d", code, reached)
	}
	if code := serve(http.MethodPost, "/api/logout", ""); code != http.StatusNoContent {
		t.Fatalf("logout with no body = %d", code)
	}
	// Other routes keep their bodies: sign-in carries credentials.
	if code := serve(http.MethodPost, "/api/login", `{"username":"u"}`); code != http.StatusNoContent {
		t.Fatalf("login with a body = %d", code)
	}
}
