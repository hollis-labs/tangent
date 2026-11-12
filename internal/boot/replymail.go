package boot

import (
	"context"
	"log/slog"
	"sync"

	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/replymail"
)

// startReplyMail starts the worker that mails operator answers to the agents
// that asked (reply_to). Unconfigured, it starts nothing: deliveries stay
// queued in the interaction store and flush once the credential is installed.
func startReplyMail(cfg Config, logger *slog.Logger, store *interaction.Store) func() {
	sender := cfg.ReplyMailSender
	if sender == nil {
		if cfg.ReplyMailURL == "" || cfg.ReplyMailToken == "" || cfg.ReplyMailFrom == "" {
			logger.Warn("reply mail not configured; answers to agents stay queued",
				"need", "tangent.tether_url, tangent.tether_sender and the send-only token")
			return nil
		}
		sender = &replymail.TetherSender{URL: cfg.ReplyMailURL, Token: cfg.ReplyMailToken, Sender: cfg.ReplyMailFrom}
	}
	worker := replymail.New(store, sender, logger)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		worker.Run(ctx)
	}()
	logger.Info("reply mail enabled")
	return func() {
		cancel()
		wg.Wait()
	}
}
