package server

import (
	"io"
	"net/http"
)

// RefuseBody wraps a handler for a route that takes no request body. A non-empty body is refused with 400
// before the handler runs. It reads at most one byte when the length is not declared.
func RefuseBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hasBody(r) {
			writeHITLJSON(w, http.StatusBadRequest, map[string]string{
				"code":    "invalid_request",
				"message": "this route takes no request body",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hasBody(r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return false
	}
	if r.ContentLength > 0 {
		return true
	}
	var one [1]byte
	n, _ := io.ReadFull(r.Body, one[:])
	return n > 0
}
