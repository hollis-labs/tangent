// Package log is a durable streamhub.Log over Tangent's SQLite database
// (migration 0022). It is the journal behind the SSE event stream: per-stream
// dense seq, an epoch that changes whenever the journal is created or reset,
// pruned_through plus durable gap records, and dedupe on a unique event_id so a
// retried append (an outbox relay replaying after a crash) stores one row.
//
// The shape follows Nanite's host_runtime_feed store. Differences:
//
//   - The cursor a client holds is "<epoch>:<seq>" (FormatCursor/ParseCursor);
//     the Log interface itself only sees Seq, so a foreign-epoch cursor is
//     resolved by Resolve before After is called.
//   - recorded_at and seq are assigned here, never by the producer.
//   - The envelope is stored verbatim; kind, subject and event_id are lifted
//     from it (or passed explicitly via AppendMeta) into indexed columns.
package log

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"math"
	"strings"
	"sync"
	"time"

	streamhub "github.com/hollis-labs/libs/ui/ui-go/streamhub"
)

const (
	defaultPageSize = 128
	epochKey        = "epoch"
	// recordedLayout is fixed width so recorded_at compares lexically.
	recordedLayout = "2006-01-02T15:04:05.000000000Z"
	eventIDPrefix  = "evt_"
)

// Option configures a Log.
type Option func(*Log)

// WithPageSize sets how many records After reads per page (default 128). No
// database handle is held between pages, so a slow consumer cannot stall
// Append on the single SQLite connection.
func WithPageSize(n int) Option {
	return func(l *Log) {
		if n > 0 {
			l.pageSize = n
		}
	}
}

// WithClock sets the clock used for recorded_at and MaxAge trimming.
func WithClock(now func() time.Time) Option {
	return func(l *Log) {
		if now != nil {
			l.now = now
		}
	}
}

// Meta is the indexed projection of an event besides its name (stored as
// kind). Zero fields are lifted from the envelope JSON, and a missing EventID
// is minted.
type Meta struct {
	EventID     string
	SubjectType string
	SubjectID   string
}

// Log is a SQLite-backed streamhub.Log.
type Log struct {
	db       *sql.DB
	pageSize int
	now      func() time.Time

	mu     sync.Mutex
	closed bool
	subs   map[int]chan struct{}
	nextID int
}

var _ streamhub.Log = (*Log)(nil)

// Open returns a Log over a database that has migration 0022 applied. The
// epoch row is minted if the journal has none (first open).
func Open(ctx context.Context, db *sql.DB, opts ...Option) (*Log, error) {
	l := &Log{db: db, pageSize: defaultPageSize, now: time.Now, subs: make(map[int]chan struct{})}
	for _, o := range opts {
		o(l)
	}
	epoch, err := newEpoch()
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO stream_log_meta (key, value) VALUES (?, ?)`, epochKey, epoch); err != nil {
		return nil, fmt.Errorf("event log: mint epoch: %w", err)
	}
	return l, nil
}

func newEpoch() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("event log: epoch entropy: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// NewEventID mints a producer-style evt_ id (evt_ + 32 hex).
func NewEventID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("event log: event id entropy: %w", err)
	}
	return eventIDPrefix + hex.EncodeToString(b[:]), nil
}

// Epoch returns the current journal epoch.
func (l *Log) Epoch(ctx context.Context) (string, error) {
	if l.isClosed() {
		return "", streamhub.ErrClosed
	}
	var e string
	if err := l.db.QueryRowContext(ctx, `SELECT value FROM stream_log_meta WHERE key = ?`, epochKey).Scan(&e); err != nil {
		return "", fmt.Errorf("event log: read epoch: %w", err)
	}
	return e, nil
}

// Reset empties the journal and mints a new epoch. Run it after restoring a
// database from a backup so cursors issued before the restore are recognized
// as foreign instead of silently pointing at different events.
func (l *Log) Reset(ctx context.Context) (string, error) {
	if l.isClosed() {
		return "", streamhub.ErrClosed
	}
	epoch, err := newEpoch()
	if err != nil {
		return "", err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("event log: begin reset: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM stream_log_events`,
		`DELETE FROM stream_log_gaps`,
		`DELETE FROM stream_log_streams`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return "", fmt.Errorf("event log: reset: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO stream_log_meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, epochKey, epoch); err != nil {
		return "", fmt.Errorf("event log: reset epoch: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("event log: commit reset: %w", err)
	}
	l.notify()
	return epoch, nil
}

// Append implements streamhub.Log. The event_id, kind and subject come from the
// envelope JSON in e.Data when it carries them (id, kind, subject{type,id});
// otherwise an id is minted. A repeated event_id returns the stored record and
// writes nothing.
func (l *Log) Append(ctx context.Context, stream string, e streamhub.Event) (streamhub.Record, error) {
	return l.AppendMeta(ctx, stream, Meta{}, e)
}

// AppendMeta is Append with explicit indexed fields. Non-zero Meta fields win
// over what the envelope says.
func (l *Log) AppendMeta(ctx context.Context, stream string, m Meta, e streamhub.Event) (streamhub.Record, error) {
	if err := ctx.Err(); err != nil {
		return streamhub.Record{}, err
	}
	if l.isClosed() {
		return streamhub.Record{}, streamhub.ErrClosed
	}
	if stream == "" {
		return streamhub.Record{}, streamhub.ErrUnknownStream
	}
	m = fillMeta(m, e)
	if m.EventID == "" {
		id, err := NewEventID()
		if err != nil {
			return streamhub.Record{}, err
		}
		m.EventID = id
	}
	data := append([]byte(nil), e.Data...)
	name := e.Name

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return streamhub.Record{}, fmt.Errorf("event log: begin append: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		seq        uint64
		recordedAt string
		oldStream  string
		oldName    string
	)
	row := tx.QueryRowContext(ctx, `SELECT stream, seq, recorded_at, kind FROM stream_log_events WHERE event_id = ?`, m.EventID)
	switch err := row.Scan(&oldStream, &seq, &recordedAt, &oldName); {
	case err == nil:
		// Retried append: hand back the stored record.
		if oldStream != stream {
			return streamhub.Record{}, fmt.Errorf("event log: event %s already stored in stream %q", m.EventID, oldStream)
		}
		return l.loadOne(ctx, tx, stream, seq)
	case !errors.Is(err, sql.ErrNoRows):
		return streamhub.Record{}, fmt.Errorf("event log: dedupe lookup: %w", err)
	}

	at := l.now().UTC()
	if err := tx.QueryRowContext(ctx, `INSERT INTO stream_log_streams (stream, latest_seq) VALUES (?, 1)
		ON CONFLICT(stream) DO UPDATE SET latest_seq = latest_seq + 1
		RETURNING latest_seq`, stream).Scan(&seq); err != nil {
		return streamhub.Record{}, fmt.Errorf("event log: allocate seq: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO stream_log_events
		(stream, seq, event_id, kind, subject_type, subject_id, recorded_at, size_bytes, envelope)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		stream, seq, m.EventID, name, m.SubjectType, m.SubjectID, at.Format(recordedLayout),
		len(name)+len(data), data); err != nil {
		return streamhub.Record{}, fmt.Errorf("event log: insert event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return streamhub.Record{}, fmt.Errorf("event log: commit append: %w", err)
	}
	l.notify()
	return streamhub.Record{Seq: streamhub.Seq(seq), Name: name, Data: data, At: at}, nil
}

func (l *Log) loadOne(ctx context.Context, tx *sql.Tx, stream string, seq uint64) (streamhub.Record, error) {
	var (
		name, at string
		data     []byte
	)
	if err := tx.QueryRowContext(ctx, `SELECT kind, recorded_at, envelope FROM stream_log_events WHERE stream = ? AND seq = ?`,
		stream, seq).Scan(&name, &at, &data); err != nil {
		return streamhub.Record{}, fmt.Errorf("event log: load stored record: %w", err)
	}
	t, err := time.Parse(recordedLayout, at)
	if err != nil {
		return streamhub.Record{}, fmt.Errorf("event log: parse recorded_at %q: %w", at, err)
	}
	return streamhub.Record{Seq: streamhub.Seq(seq), Name: name, Data: data, At: t}, nil
}

// fillMeta lifts id, kind and subject from the envelope when m leaves them
// blank. A payload that is not a JSON object simply contributes nothing.
func fillMeta(m Meta, e streamhub.Event) Meta {
	if m.EventID != "" && m.SubjectType != "" && m.SubjectID != "" {
		return m
	}
	var env struct {
		ID      string `json:"id"`
		Subject struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"subject"`
	}
	if len(e.Data) == 0 || e.Data[0] != '{' || json.Unmarshal(e.Data, &env) != nil {
		return m
	}
	if m.EventID == "" {
		m.EventID = env.ID
	}
	if m.SubjectType == "" {
		m.SubjectType = env.Subject.Type
	}
	if m.SubjectID == "" {
		m.SubjectID = env.Subject.ID
	}
	return m
}

// After implements streamhub.Log.
func (l *Log) After(ctx context.Context, stream string, after streamhub.Seq) iter.Seq2[streamhub.Record, error] {
	return func(yield func(streamhub.Record, error) bool) {
		cursor := after
		for {
			page, err := l.page(ctx, stream, cursor)
			if err != nil {
				yield(streamhub.Record{}, err)
				return
			}
			if len(page) == 0 {
				return
			}
			for _, r := range page {
				if !yield(r, nil) {
					return
				}
			}
			cursor = page[len(page)-1].Seq
		}
	}
}

func (l *Log) page(ctx context.Context, stream string, cursor streamhub.Seq) ([]streamhub.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.isClosed() {
		return nil, streamhub.ErrClosed
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("event log: begin read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var latest, pruned uint64
	switch qerr := tx.QueryRowContext(ctx, `SELECT latest_seq, pruned_through FROM stream_log_streams WHERE stream = ?`,
		stream).Scan(&latest, &pruned); {
	case errors.Is(qerr, sql.ErrNoRows):
	case qerr != nil:
		return nil, fmt.Errorf("event log: read head: %w", qerr)
	}
	switch {
	case uint64(cursor) > latest:
		return nil, &streamhub.GapError{Gap: streamhub.Gap{
			Reason: streamhub.GapCursorAhead, Requested: cursor,
			OldestAvailable: streamhub.Seq(pruned + 1), Latest: streamhub.Seq(latest),
			Missed: uint64(cursor) - latest,
		}}
	case uint64(cursor) < pruned:
		return nil, &streamhub.GapError{Gap: streamhub.Gap{
			Reason: streamhub.GapRetention, Requested: cursor,
			OldestAvailable: streamhub.Seq(pruned + 1), Latest: streamhub.Seq(latest),
			Missed: pruned - uint64(cursor),
		}}
	}
	rows, err := tx.QueryContext(ctx, `SELECT seq, kind, recorded_at, envelope FROM stream_log_events
		WHERE stream = ? AND seq > ? ORDER BY seq LIMIT ?`, stream, uint64(cursor), l.pageSize)
	if err != nil {
		return nil, fmt.Errorf("event log: read page: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []streamhub.Record
	for rows.Next() {
		var (
			seq      uint64
			name, at string
			data     []byte
		)
		if err := rows.Scan(&seq, &name, &at, &data); err != nil {
			return nil, fmt.Errorf("event log: scan record: %w", err)
		}
		t, err := time.Parse(recordedLayout, at)
		if err != nil {
			return nil, fmt.Errorf("event log: parse recorded_at %q: %w", at, err)
		}
		out = append(out, streamhub.Record{Seq: streamhub.Seq(seq), Name: name, Data: data, At: t})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("event log: iterate page: %w", err)
	}
	return out, nil
}

// Head implements streamhub.Log.
func (l *Log) Head(ctx context.Context, stream string) (streamhub.Head, error) {
	if err := ctx.Err(); err != nil {
		return streamhub.Head{}, err
	}
	if l.isClosed() {
		return streamhub.Head{}, streamhub.ErrClosed
	}
	var h streamhub.Head
	var latest, pruned, dropped uint64
	err := l.db.QueryRowContext(ctx, `SELECT latest_seq, pruned_through, dropped FROM stream_log_streams WHERE stream = ?`,
		stream).Scan(&latest, &pruned, &dropped)
	if errors.Is(err, sql.ErrNoRows) {
		return h, nil
	}
	if err != nil {
		return h, fmt.Errorf("event log: read head: %w", err)
	}
	h.Latest, h.PrunedThrough, h.Dropped = streamhub.Seq(latest), streamhub.Seq(pruned), dropped
	return h, nil
}

// Streams lists the streams the journal has seen.
func (l *Log) Streams(ctx context.Context) ([]string, error) {
	if l.isClosed() {
		return nil, streamhub.ErrClosed
	}
	rows, err := l.db.QueryContext(ctx, `SELECT stream FROM stream_log_streams ORDER BY stream`)
	if err != nil {
		return nil, fmt.Errorf("event log: list streams: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("event log: scan stream: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Trim implements streamhub.Log. Records leave oldest first; a durable gap
// record is written when anything was removed.
func (l *Log) Trim(ctx context.Context, stream string, r streamhub.Retention) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.isClosed() {
		return streamhub.ErrClosed
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("event log: begin trim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var latest, pruned uint64
	switch qerr := tx.QueryRowContext(ctx, `SELECT latest_seq, pruned_through FROM stream_log_streams WHERE stream = ?`,
		stream).Scan(&latest, &pruned); {
	case errors.Is(qerr, sql.ErrNoRows):
		return nil
	case qerr != nil:
		return fmt.Errorf("event log: trim head: %w", qerr)
	}
	through := pruned
	if r.MaxRecords > 0 && latest > uint64(r.MaxRecords) {
		through = max(through, latest-uint64(r.MaxRecords))
	}
	if r.MaxBytes > 0 {
		// Newest-first running total; the first record that overflows the
		// budget and everything older goes.
		var seq sql.NullInt64
		if qerr := tx.QueryRowContext(ctx, `SELECT MAX(seq) FROM (
				SELECT seq, SUM(size_bytes) OVER (ORDER BY seq DESC) AS cum
				FROM stream_log_events WHERE stream = ?) WHERE cum > ?`, stream, r.MaxBytes).Scan(&seq); qerr != nil {
			return fmt.Errorf("event log: trim by bytes: %w", qerr)
		}
		if seq.Valid {
			through = max(through, uint64(seq.Int64)) //nolint:gosec // seq is a stored 1-based sequence
		}
	}
	if r.MaxAge > 0 {
		cutoff := l.now().UTC().Add(-r.MaxAge).Format(recordedLayout)
		// Only the contiguous old prefix goes, matching the in-memory log.
		var seq sql.NullInt64
		if qerr := tx.QueryRowContext(ctx, `SELECT MIN(seq) - 1 FROM stream_log_events
			WHERE stream = ? AND recorded_at >= ?`, stream, cutoff).Scan(&seq); qerr != nil {
			return fmt.Errorf("event log: trim by age: %w", qerr)
		}
		switch {
		case seq.Valid:
			through = max(through, uint64(seq.Int64)) //nolint:gosec // MIN(seq)-1 of stored sequences is >= 0
		default:
			// Nothing is newer than the cutoff: everything is old.
			through = max(through, latest)
		}
	}
	if through <= pruned {
		return nil
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM stream_log_events WHERE stream = ? AND seq <= ?`, stream, through)
	if err != nil {
		return fmt.Errorf("event log: delete trimmed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("event log: count trimmed: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE stream_log_streams SET pruned_through = ?, dropped = dropped + ? WHERE stream = ?`,
		through, n, stream); err != nil {
		return fmt.Errorf("event log: advance pruned_through: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO stream_log_gaps (stream, reason, from_seq, through_seq, dropped, recorded_at)
		VALUES (?, 'retention', ?, ?, ?, ?)`, stream, pruned+1, through, n, l.now().UTC().Format(recordedLayout)); err != nil {
		return fmt.Errorf("event log: record gap: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("event log: commit trim: %w", err)
	}
	return nil
}

// GapRecord is one durable retention gap.
type GapRecord struct {
	Stream     string
	Reason     string
	FromSeq    uint64
	ThroughSeq uint64
	Dropped    uint64
	RecordedAt time.Time
}

// Gaps returns the durable gap records of a stream, oldest first.
func (l *Log) Gaps(ctx context.Context, stream string) ([]GapRecord, error) {
	if l.isClosed() {
		return nil, streamhub.ErrClosed
	}
	rows, err := l.db.QueryContext(ctx, `SELECT stream, reason, from_seq, through_seq, dropped, recorded_at
		FROM stream_log_gaps WHERE stream = ? ORDER BY id`, stream)
	if err != nil {
		return nil, fmt.Errorf("event log: list gaps: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []GapRecord
	for rows.Next() {
		var g GapRecord
		var at string
		if err := rows.Scan(&g.Stream, &g.Reason, &g.FromSeq, &g.ThroughSeq, &g.Dropped, &at); err != nil {
			return nil, fmt.Errorf("event log: scan gap: %w", err)
		}
		t, err := time.Parse(recordedLayout, at)
		if err != nil {
			return nil, fmt.Errorf("event log: parse gap time %q: %w", at, err)
		}
		g.RecordedAt = t
		out = append(out, g)
	}
	return out, rows.Err()
}

// Subscribe returns a channel that receives a value after a commit that made
// new records visible (an Append that stored a row, or a Reset). Wakes are
// coalesced: a burst leaves at most one pending value, so a consumer reads
// After(lastSeq) when woken and never relies on the count. Call cancel to
// release the subscription. Subscribe before replaying After so nothing
// committed in between is missed; dedupe by seq.
func (l *Log) Subscribe() (wake <-chan struct{}, cancel func()) {
	ch := make(chan struct{}, 1)
	l.mu.Lock()
	id := l.nextID
	l.nextID++
	if l.closed {
		close(ch)
	} else {
		l.subs[id] = ch
	}
	l.mu.Unlock()
	return ch, func() {
		l.mu.Lock()
		delete(l.subs, id)
		l.mu.Unlock()
	}
}

func (l *Log) notify() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, ch := range l.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (l *Log) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

// Close implements streamhub.Log. It does not close the shared *sql.DB.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	for id, ch := range l.subs {
		close(ch)
		delete(l.subs, id)
	}
	return nil
}

// FormatCursor renders the client-facing cursor "<epoch>:<seq>".
func FormatCursor(epoch string, seq streamhub.Seq) string {
	return fmt.Sprintf("%s:%d", epoch, uint64(seq))
}

// ParseCursor splits a cursor produced by FormatCursor.
func ParseCursor(c string) (epoch string, seq streamhub.Seq, err error) {
	i := strings.LastIndexByte(c, ':')
	if i <= 0 || i == len(c)-1 {
		return "", 0, fmt.Errorf("event log: malformed cursor %q", c)
	}
	var n uint64
	for _, ch := range c[i+1:] {
		if ch < '0' || ch > '9' || n > (math.MaxUint64-9)/10 {
			return "", 0, fmt.Errorf("event log: malformed cursor %q", c)
		}
		n = n*10 + uint64(ch-'0')
	}
	return c[:i], streamhub.Seq(n), nil
}
