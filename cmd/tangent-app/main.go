// Command tangent-app is the Tangent desktop shell: a Wails v3 window over
// the same server cmd/tangent serves, adopting a running one or booting its
// own. See internal/appshell for the adopt-or-boot decision.
//
// This binary carries no go:embed — the server already holds ui_dist — and
// is the only place in the tree that imports Wails. It reads the same
// environment variables as cmd/tangent so a launchd daemon and this window
// agree on which port and database to look at.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/hollis-labs/tangent/internal/appshell"
	"github.com/hollis-labs/tangent/internal/boot"
)

const (
	defaultPort        = 7842
	envPort            = "TANGENT_HTTP_PORT"
	envDevFrontendURL  = "TANGENT_DEV_FRONTEND_URL"
	envDBPath          = "TANGENT_DB_PATH"
	envManagedResource = "TANGENT_MANAGED_RESOURCE"
	envOpenTelemetry   = "TANGENT_OTEL"
	ownerLabel         = "tangent-app"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	shell, err := appshell.New(appshell.Config{
		Config: boot.Config{
			DBPath:          os.Getenv(envDBPath),
			Port:            resolvePort(),
			DevFrontendURL:  os.Getenv(envDevFrontendURL),
			ManagedResource: os.Getenv(envManagedResource),
			OTel:            os.Getenv(envOpenTelemetry) != "",
			OwnerLabel:      ownerLabel,
			Logger:          logger,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "tangent-app: %v\n", err)
		os.Exit(1)
	}
	if err := shell.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tangent-app: %v\n", err)
		os.Exit(1)
	}
}

// resolvePort mirrors cmd/tangent's own resolvePort: TANGENT_HTTP_PORT when
// it parses as a positive integer, otherwise the same default port.
func resolvePort() int {
	raw := os.Getenv(envPort)
	if raw == "" {
		return defaultPort
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultPort
	}
	return n
}
