package stopping_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/stopping"
)

// A request that never gets a stop channel (a test calling a handler directly) still ends with
// its own context.
func TestContextWithoutAStopChannelEndsWithTheRequest(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, cancel := stopping.Context(r)
	select {
	case <-ctx.Done():
		t.Fatal("ended before anything stopped")
	default:
	}
	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("cancel did not end the context")
	}
}
