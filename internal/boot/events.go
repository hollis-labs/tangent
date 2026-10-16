package boot

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hollis-labs/tangent/internal/events"
	eventlog "github.com/hollis-labs/tangent/internal/events/log"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/server"
)

// eventsRelayTick is how often the outbox relay polls. The post-commit wake
// makes delivery immediate in the normal case; the tick is the fallback, so
// correctness never depends on the wake.
const eventsRelayTick = 500 * time.Millisecond

// startEventStream wires the typed approval event stream (CW-20261016-0143):
// the durable journal, the outbox relay, the 400-day retention job, the
// post-commit wake and the server-side stream config. With cfg.EventsDisabled
// it starts nothing and returns a nil stream, which leaves the legacy
// revision-only SSE unchanged.
//
// The returned stop function cancels the background work and waits for it; it
// must run before the database closes.
func startEventStream(
	cfg Config, logger *slog.Logger, db *sql.DB, store *interaction.Store,
) (*server.HITLEventStream, func(), error) {
	if cfg.EventsDisabled {
		logger.Warn("typed event stream disabled (TANGENT_EVENTS_DISABLED); approval events still queue in the outbox")
		return nil, nil, nil
	}
	ctx := context.Background()
	journal, err := eventlog.Open(ctx, db)
	if err != nil {
		return nil, nil, fmt.Errorf("open event journal: %w", err)
	}
	epoch, err := journal.Epoch(ctx)
	if err != nil {
		_ = journal.Close()
		return nil, nil, fmt.Errorf("read event journal epoch: %w", err)
	}
	head, err := journal.Head(ctx, events.DefaultStream)
	if err != nil {
		_ = journal.Close()
		return nil, nil, fmt.Errorf("read event journal head: %w", err)
	}

	tick := cfg.eventsRelayTick
	if tick <= 0 {
		tick = eventsRelayTick
	}
	relay := eventlog.NewRelay(db, journal, tick)
	if !cfg.eventsNoCommitHook {
		store.SetCommitHook(relay.Wake)
	}

	runCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if runErr := relay.Run(runCtx); runErr != nil && runCtx.Err() == nil {
			logger.Error("event relay stopped", "error", runErr)
		}
	}()
	go func() {
		defer wg.Done()
		if runErr := journal.RunRetention(runCtx, time.Hour); runErr != nil && runCtx.Err() == nil {
			logger.Error("event retention stopped", "error", runErr)
		}
	}()
	stop := func() {
		cancel()
		wg.Wait()
		_ = journal.Close()
	}

	logger.Info("typed event stream enabled",
		"stream", events.DefaultStream, "epoch", epoch, "head_seq", uint64(head.Latest))
	return &server.HITLEventStream{Log: journal, Notify: journal, Epoch: epoch, Stream: events.DefaultStream}, stop, nil
}
