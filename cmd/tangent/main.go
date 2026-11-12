// Command tangent is the Tangent server, built on Substrate. The shell (config, logging, health,
// HTTP, auth, identity, secrets, database, CLI skeleton) comes from the manifest; this file and
// internal/ are the app: its data, its routes and its tools.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/cli"
	"github.com/hollis-labs/substrate/modules/database"

	"github.com/hollis-labs/tangent/internal/persistence"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "tangent:", err)
		stop()
		os.Exit(1)
	}
}

func run(ctx context.Context, getenv func(string) string) error {
	if args := os.Args[1:]; len(args) > 0 && args[0] == "migrate-data" {
		// Before the modules start: see migrate.go.
		return migrateData(ctx, args[1:], os.Stdout)
	}
	fsys, root := persistence.Migrations()
	database.UseMigrationFS(fsys, root)

	modules, prepare := plan(os.Args[1:])
	started, err := boot.StartWith(ctx, substrateApp, modules, substrateConfigJSON, bootOptions(getenv, prepare, modules))
	if err != nil {
		return err
	}
	defer func() { _ = started.Stop(context.Background()) }()

	t := &tangent{app: started.App}
	rootCmd := cli.Root(started.App)
	serve := t.serveCmd(ctx)
	// `tangent` with no command serves, like `tangent serve`.
	rootCmd.RunE = func(c *cobra.Command, args []string) error { return serve.RunE(c, args) }
	rootCmd.Args = cobra.NoArgs
	rootCmd.AddCommand(serve)
	return rootCmd.ExecuteContext(ctx)
}
