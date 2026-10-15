package server

import (
	"context"
	"net/http"
	"time"

	"github.com/hollis-labs/tangent/internal/interaction"
)

// InboxService projects the canonical records without owning their lifecycle.
type InboxService interface {
	BrowserInbox(context.Context) ([]interaction.InboxEntry, error)
}

// inboxHandler serves the whole Inbox list as a conditional read; see writeConditionalJSON.
func inboxHandler(service InboxService) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		entries, err := service.BrowserInbox(r.Context())
		if err != nil {
			writeRoomError(w, err)
			return
		}
		if err := writeConditionalJSON(w, r, "inbox", start, map[string]any{"items": entries}, nil); err != nil {
			writeRoomError(w, err)
		}
	})
}
