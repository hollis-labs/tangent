package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/hollis-labs/libs/ui/ui-go/streamhub"
)

// HITLEventNotifier supplies per-subscriber wakeups after durable appends.
// Wakeups may coalesce: the Log, not the notification channel, owns the records.
// Subscribe must return a buffered channel; publishers must send without blocking.
// The returned function releases the subscription when the request ends.
type HITLEventNotifier interface {
	Subscribe() (<-chan struct{}, func())
}

// HITLEventStream connects the optional typed inbox stream to durable storage.
// Epoch identifies the log's sequence namespace and must remain stable for the
// log's lifetime. A nil Log leaves the legacy revision-only stream unchanged.
// A nil Notify uses the existing inbox poll to discover appended records.
type HITLEventStream struct {
	Log    streamhub.Log
	Notify HITLEventNotifier
	Epoch  string
	Stream string
}

type hitlEventGap struct {
	Reason            string `json:"reason"`
	EarliestAvailable string `json:"earliest_available"`
	HighWater         string `json:"high_water"`
	Epoch             string `json:"epoch"`
}

type hitlEventSession struct {
	source      *HITLEventStream
	after       streamhub.Seq
	notify      <-chan struct{}
	unsubscribe func()
	initialGap  *hitlEventGap
	checkpoint  string
}

var errHITLEventCursor = errors.New("invalid event cursor: expected <epoch>:<seq>")

func parseHITLEventCursor(raw string) (string, streamhub.Seq, error) {
	epoch, sequence, ok := strings.Cut(raw, ":")
	if !ok || epoch == "" || strings.ContainsAny(epoch, "\x00\r\n") || sequence == "" || strings.Trim(sequence, "0123456789") != "" {
		return "", 0, errHITLEventCursor
	}
	n, err := strconv.ParseUint(sequence, 10, 64)
	if err != nil {
		return "", 0, errHITLEventCursor
	}
	return epoch, streamhub.Seq(n), nil
}

func hitlEventID(epoch string, sequence streamhub.Seq) string {
	return fmt.Sprintf("%s:%d", epoch, sequence)
}

func (s *HITLEventStream) validate() error {
	if s == nil || s.Log == nil {
		return nil
	}
	if s.Epoch == "" || strings.ContainsAny(s.Epoch, ":\x00\r\n") || s.Stream == "" {
		return errors.New("incomplete event stream configuration")
	}
	return nil
}

func (s *HITLEventStream) open(ctx context.Context, r *http.Request) (*hitlEventSession, error) {
	if s == nil || s.Log == nil {
		return nil, nil
	}
	raw := r.Header.Get("Last-Event-ID")
	fromHeader := raw != ""
	hasCursor := fromHeader || r.URL.Query().Has("after")
	if !fromHeader {
		raw = r.URL.Query().Get("after")
	}
	var epoch string
	var after streamhub.Seq
	var cursorErr error
	if hasCursor {
		epoch, after, cursorErr = parseHITLEventCursor(raw)
		if cursorErr != nil && !fromHeader {
			return nil, cursorErr
		}
	}
	session := &hitlEventSession{source: s, after: after}
	// Subscribe before Head/After so an append at the replay/live boundary
	// cannot fall between reading history and registering for notifications.
	if s.Notify != nil {
		session.notify, session.unsubscribe = s.Notify.Subscribe()
	}
	head, err := s.Log.Head(ctx, s.Stream)
	if err != nil {
		session.close()
		// Keep an existing cursor across a revision-only fallback. Otherwise
		// its first revision frame would clear EventSource's reconnect id.
		fallback := &hitlEventSession{}
		if hasCursor && cursorErr == nil {
			fallback.checkpoint = raw
		}
		return fallback, err
	}
	switch {
	case cursorErr != nil:
		session.after = head.Latest
		session.initialGap = session.gap("invalid_cursor", head.PrunedThrough+1, head.Latest)
	case !hasCursor:
		session.after = head.Latest // A fresh reader follows live events only.
	case epoch != s.Epoch:
		session.after = head.Latest
		session.initialGap = session.gap("foreign_epoch", head.PrunedThrough+1, head.Latest)
	}
	// Establish the initial id on every connection, before any revision hint.
	// The SSE parser starts each response with an empty last-event-id buffer.
	session.checkpoint = hitlEventID(s.Epoch, session.after)
	return session, nil
}

func (s *hitlEventSession) close() {
	if s != nil && s.unsubscribe != nil {
		s.unsubscribe()
	}
}

func (s *hitlEventSession) gap(reason string, earliest, latest streamhub.Seq) *hitlEventGap {
	return &hitlEventGap{
		Reason: reason, Epoch: s.source.Epoch,
		EarliestAvailable: hitlEventID(s.source.Epoch, earliest),
		HighWater:         hitlEventID(s.source.Epoch, latest),
	}
}

func writeHITLEventGap(w io.Writer, gap *hitlEventGap) error {
	data, err := json.Marshal(gap)
	if err != nil {
		return err
	}
	// Reset EventSource's reconnect cursor even if no later record arrives.
	_, err = fmt.Fprintf(w, "id: %s\nevent: gap\ndata: %s\n\n", gap.HighWater, data)
	return err
}

func writeHITLTypedEvent(w io.Writer, epoch string, record streamhub.Record) error {
	if record.Name == "" || strings.ContainsAny(record.Name, "\r\n") {
		return errors.New("invalid event kind")
	}
	// Pretty-printed JSON must not turn into extra SSE lines or frames. The
	// envelope's own id is preserved; only the SSE id carries epoch:sequence.
	var data bytes.Buffer
	if err := json.Compact(&data, record.Data); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", hitlEventID(epoch, record.Seq), record.Name, data.Bytes())
	return err
}

func (s *hitlEventSession) writeCheckpoint(w io.Writer) error {
	if s == nil || s.checkpoint == "" {
		return nil
	}
	_, err := fmt.Fprintf(w, "id: %s\n\n", s.checkpoint)
	return err
}

func (s *hitlEventSession) replay(ctx context.Context, w io.Writer) error {
	if s == nil || s.source == nil {
		return nil
	}
	if s.initialGap != nil {
		if err := writeHITLEventGap(w, s.initialGap); err != nil {
			return err
		}
		s.initialGap = nil
	}
	for record, err := range s.source.Log.After(ctx, s.source.Stream, s.after) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			var gap *streamhub.GapError
			if !errors.As(err, &gap) {
				return err
			}
			reason := gap.Gap.Reason.String()
			if gap.Gap.Reason == streamhub.GapRetention {
				reason = "pruned"
			}
			if err := writeHITLEventGap(w, s.gap(reason, gap.Gap.OldestAvailable, gap.Gap.Latest)); err != nil {
				return err
			}
			s.after = gap.Gap.Latest
			return nil
		}
		if record.Seq <= s.after {
			continue
		}
		if err := writeHITLTypedEvent(w, s.source.Epoch, record); err != nil {
			return err
		}
		s.after = record.Seq
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	return nil
}
