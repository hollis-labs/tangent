package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hollis-labs/tangent/internal/interaction"
)

// InboxService projects the canonical records without owning their lifecycle.
type InboxService interface {
	BrowserInbox(context.Context) ([]interaction.InboxEntry, error)
}

// inboxHandler serves the Inbox list as a conditional read; see writeConditionalJSON.
//
// `?view=summary` returns the fields a list row needs and drops the heavy ones: on a
// real database the request snapshots are three quarters of the full payload (the largest
// is 57 KB), and the row only shows a title and a source. The page fetches the full entry
// for the one interaction the operator opens (inboxItemHandler) and asks the server which
// interactions match a search (inboxSearchHandler), so nothing it could do before is lost.
func inboxHandler(service InboxService) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		entries, err := service.BrowserInbox(r.Context())
		if err != nil {
			writeRoomError(w, err)
			return
		}
		if r.URL.Query().Get("view") == "summary" {
			summary := make([]interaction.InboxEntry, len(entries))
			for i, entry := range entries {
				summary[i] = summarizeInboxEntry(entry)
			}
			entries = summary
		}
		if err := writeConditionalJSON(w, r, "inbox", start, map[string]any{"items": entries}, nil); err != nil {
			writeRoomError(w, err)
		}
	})
}

// inboxItemReader is the keyed read a service may offer; without it the handler lists and
// filters, which gives the same answer more slowly.
type inboxItemReader interface {
	BrowserInboxItem(context.Context, string) (interaction.InboxEntry, error)
}

// inboxItemHandler serves one full entry by interaction id.
func inboxItemHandler(service InboxService) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("interactionID")
		notFound := func() {
			writeHITLJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": "interaction not found"})
		}
		if keyed, ok := service.(inboxItemReader); ok {
			entry, err := keyed.BrowserInboxItem(r.Context(), id)
			switch {
			case err == nil:
				writeHITLJSON(w, http.StatusOK, entry)
			case errors.Is(err, interaction.ErrNotFound):
				notFound()
			default:
				writeRoomError(w, err)
			}
			return
		}
		entries, err := service.BrowserInbox(r.Context())
		if err != nil {
			writeRoomError(w, err)
			return
		}
		for _, entry := range entries {
			if entry.Interaction.ID == id {
				writeHITLJSON(w, http.StatusOK, entry)
				return
			}
		}
		notFound()
	})
}

// inboxOperator is what the Inbox offers the operator on any open or closed
// item, whatever its kind: end it (archive) or remove it (delete).
type inboxOperator interface {
	ArchiveBrowserInboxItem(context.Context, string) error
	DeleteBrowserInboxItem(context.Context, string) error
}

func inboxArchiveHandler(service inboxOperator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := service.ArchiveBrowserInboxItem(r.Context(), r.PathValue("interactionID")); err != nil {
			writeRoomError(w, err)
			return
		}
		writeHITLJSON(w, http.StatusOK, map[string]string{"status": "archived"})
	}
}

func inboxDeleteHandler(service inboxOperator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := service.DeleteBrowserInboxItem(r.Context(), r.PathValue("interactionID")); err != nil {
			writeRoomError(w, err)
			return
		}
		writeHITLJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// inboxSearchHandler returns the ids of interactions whose request, source or resolution
// text contains `q` (case-insensitive), so the page can search content it no longer holds.
func inboxSearchHandler(service InboxService) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
		entries, err := service.BrowserInbox(r.Context())
		if err != nil {
			writeRoomError(w, err)
			return
		}
		ids := make([]string, 0)
		if query != "" {
			for _, entry := range entries {
				haystack := strings.ToLower(string(entry.Interaction.RequestSnapshot) + " " + entry.Interaction.CallerPrincipalRef + " " + entry.Interaction.CallerScope)
				if entry.Resolution != nil {
					haystack += " " + strings.ToLower(string(entry.Resolution.ResponsePayload))
				}
				if strings.Contains(haystack, query) {
					ids = append(ids, entry.Interaction.ID)
				}
			}
		}
		writeHITLJSON(w, http.StatusOK, map[string]any{"ids": ids})
	})
}

var (
	summaryTextKeys   = []string{"title", "queue_title", "prompt", "question", "name"}
	summarySourceKeys = []string{"agent_label", "agent_id", "application_label", "application_id"}
)

const summaryTextLimit = 300

// summarizeInboxEntry keeps what a list row renders: identity, state, times, and the few
// request fields the row's title and source are read from, each bounded. The resolution
// stays (a row can tell it is resolved) without its payload.
func summarizeInboxEntry(entry interaction.InboxEntry) interaction.InboxEntry {
	record := entry.Interaction
	record.RequestSnapshot = summarizeSnapshot(record.RequestSnapshot, record.ExternalRefs)
	record.ExternalRefs = nil
	record.Policy = nil
	// Binding, caller and participant bookkeeping is not shown in a row; only the kind
	// (which picks the category) and the scope the row's group filters on are kept.
	record.Definition = interaction.DefinitionBinding{Kind: record.Definition.Kind}
	// CallerPrincipalRef stays: entrySource falls back to it (before caller_scope) when the
	// snapshot names no source, so clearing it would change what the row shows.
	record.CallerAuthority, record.CallerAssurance, record.IdempotencyKey = "", "", ""
	record.ParticipantScope, record.ParticipantRef, record.ParticipantAuthority, record.ParticipantAssurance = "", "", "", ""
	record.ConnectionID, record.TerminalPolicyRef = "", ""
	entry.Interaction = record
	if entry.Resolution != nil {
		resolution := *entry.Resolution
		resolution.ResponsePayload = nil
		entry.Resolution = &resolution
	}
	return entry
}

func summarizeSnapshot(raw json.RawMessage, externalRefs json.RawMessage) json.RawMessage {
	var snapshot map[string]json.RawMessage
	if json.Unmarshal(raw, &snapshot) != nil {
		return json.RawMessage(`{}`)
	}
	kept := map[string]any{}
	for _, key := range summaryTextKeys {
		var text string
		if json.Unmarshal(snapshot[key], &text) == nil && text != "" {
			kept[key] = truncateRunes(text, summaryTextLimit)
		}
	}
	if _, titled := kept["title"]; !titled {
		if title := workflowRowTitle(snapshot, externalRefs); title != "" {
			kept["title"] = truncateRunes(title, summaryTextLimit)
		}
	}
	var source map[string]json.RawMessage
	if json.Unmarshal(snapshot["source"], &source) == nil {
		trimmed := map[string]string{}
		for _, key := range summarySourceKeys {
			var text string
			if json.Unmarshal(source[key], &text) == nil && text != "" {
				trimmed[key] = truncateRunes(text, summaryTextLimit)
			}
		}
		kept["source"] = trimmed
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return out
}

// workflowRowTitle names a room-workflow row. The request payload of a form or
// review has no title field (the envelope's title lives in the presented
// envelope kept in external_refs), so without this the row reads "form collect"
// for every one of them. Falls back to the form's intent, then its id.
func workflowRowTitle(snapshot map[string]json.RawMessage, externalRefs json.RawMessage) string {
	var refs struct {
		Envelope struct {
			Title string `json:"title"`
		} `json:"legacy_envelope"`
	}
	if json.Unmarshal(externalRefs, &refs) == nil && refs.Envelope.Title != "" {
		return refs.Envelope.Title
	}
	for _, key := range []string{"intent", "form_id", "queue_id", "review_id"} {
		var text string
		if json.Unmarshal(snapshot[key], &text) == nil && text != "" {
			return text
		}
	}
	return ""
}

func truncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit])
}
