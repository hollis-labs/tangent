// Package stopping gives a long-lived handler (an event stream, a socket) a context that ends
// when the request does and when the server begins to stop. httphost's graceful stop leaves
// r.Context() live so a request still working finishes; a stream never would, so each one
// selects on this as well.
package stopping

import (
	"context"
	"net/http"

	"github.com/hollis-labs/substrate/modules/httphost"
)

// Context is r.Context(), cancelled also when the server begins to stop. Call the returned
// function when the handler returns.
func Context(r *http.Request) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(r.Context())
	if stop := httphost.Stopping(r); stop != nil {
		go func() {
			select {
			case <-stop:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	return ctx, cancel
}
