package boot

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/replymail"
)

// Environment for reply mail. The token is a Tether service credential that may
// send mail and nothing else; it lives in a root-only env file, never in logs.
const (
	envTetherURL    = "TANGENT_TETHER_URL"
	envTetherToken  = "TANGENT_TETHER_TOKEN" //nolint:gosec // the variable name, not a credential
	envTetherSender = "TANGENT_TETHER_SENDER"
)

// startReplyMail starts the worker that mails operator answers to the agents
// that asked (reply_to). Unconfigured, it starts nothing: deliveries stay
// queued in the interaction store and flush once the credential is installed.
func startReplyMail(cfg Config, logger *slog.Logger, store *interaction.Store) func() {
	sender := cfg.ReplyMailSender
	if sender == nil {
		url, token, from := os.Getenv(envTetherURL), os.Getenv(envTetherToken), os.Getenv(envTetherSender)
		if url == "" || token == "" || from == "" {
			logger.Warn("reply mail not configured; answers to agents stay queued",
				"need", strings.Join([]string{envTetherURL, envTetherToken, envTetherSender}, ", "))
			return nil
		}
		sender = &replymail.TetherSender{URL: url, Token: token, Sender: from}
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
