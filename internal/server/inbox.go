package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tangent/internal/interaction"
)

// InboxService projects the canonical records without owning their lifecycle.
type InboxService interface {
	BrowserInbox(context.Context) ([]interaction.InboxEntry, error)
}

// inboxHandler serves the whole Inbox list with a strong ETag over the encoded body.
// A client that already holds the current list sends If-None-Match and gets a 304
// with no body, so an unchanged Inbox costs one read and no transfer, parse or render.
// Cache-Control stays no-store so the browser never answers a conditional itself:
// the page owns revalidation and must see the real 304.
func inboxHandler(service InboxService) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		entries, err := service.BrowserInbox(r.Context())
		if err != nil {
			writeRoomError(w, err)
			return
		}
		var body bytes.Buffer
		if err := json.NewEncoder(&body).Encode(map[string]any{"items": entries}); err != nil {
			writeRoomError(w, err)
			return
		}
		sum := sha256.Sum256(body.Bytes())
		etag := `"` + hex.EncodeToString(sum[:16]) + `"`
		w.Header().Set("Server-Timing", "inbox;dur="+strconv.FormatFloat(float64(time.Since(start).Microseconds())/1000, 'f', 1, 64))
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "no-store")
		if etagMatches(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body.Bytes())
	})
}

// etagMatches reports whether an If-None-Match header names etag. Compression
// proxies rewrite a strong tag to a weak one (W/"…"), which still matches here:
// the comparison is for "same list", not for byte-range safety.
func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == etag || candidate == "*" {
			return true
		}
	}
	return false
}
