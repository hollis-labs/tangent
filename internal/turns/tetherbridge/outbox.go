package tetherbridge

import (
	"sync"
	"time"
)

// PendingReply represents an operator reply queued while the target session is busy.
type PendingReply struct {
	ItemID       string    `json:"item_id"`
	SessionID    string    `json:"session_id"`
	ResponseText string    `json:"response_text"`
	Action       string    `json:"action,omitempty"`
	QueuedAt     time.Time `json:"queued_at"`
}

// SessionOutbox is a thread-safe FIFO outbox buffer keyed by sessionID.
type SessionOutbox struct {
	mu      sync.Mutex
	pending map[string][]PendingReply
}

// NewSessionOutbox initializes a new SessionOutbox.
func NewSessionOutbox() *SessionOutbox {
	return &SessionOutbox{
		pending: make(map[string][]PendingReply),
	}
}

// Enqueue adds a reply to the session's queue.
func (o *SessionOutbox) Enqueue(reply PendingReply) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pending[reply.SessionID] = append(o.pending[reply.SessionID], reply)
}

// Dequeue pops the oldest reply for the session, returning false if empty.
func (o *SessionOutbox) Dequeue(sessionID string) (PendingReply, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	q, ok := o.pending[sessionID]
	if !ok || len(q) == 0 {
		return PendingReply{}, false
	}
	first := q[0]
	if len(q) == 1 {
		delete(o.pending, sessionID)
	} else {
		o.pending[sessionID] = q[1:]
	}
	return first, true
}

// Peek returns the oldest reply for the session without removing it.
func (o *SessionOutbox) Peek(sessionID string) (PendingReply, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	q, ok := o.pending[sessionID]
	if !ok || len(q) == 0 {
		return PendingReply{}, false
	}
	return q[0], true
}

// Len returns the count of queued replies for a session.
func (o *SessionOutbox) Len(sessionID string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.pending[sessionID])
}

// Total returns the count of all queued replies across all sessions.
func (o *SessionOutbox) Total() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	count := 0
	for _, q := range o.pending {
		count += len(q)
	}
	return count
}

// Clear drops all pending replies for a session (e.g. when session dies).
func (o *SessionOutbox) Clear(sessionID string) []PendingReply {
	o.mu.Lock()
	defer o.mu.Unlock()
	q := o.pending[sessionID]
	delete(o.pending, sessionID)
	return q
}
