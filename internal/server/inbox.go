package server

import (
	"context"
	"net/http"

	"github.com/hollis-labs/tangent/internal/interaction"
)

// InboxService projects the canonical records without owning their lifecycle.
type InboxService interface {
	BrowserInbox(context.Context) ([]interaction.InboxEntry, error)
}

func inboxHandler(service InboxService) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entries, err := service.BrowserInbox(r.Context())
		if err != nil {
			writeRoomError(w, err)
			return
		}
		writeHITLJSON(w, http.StatusOK, map[string]any{"items": entries})
	})
}
