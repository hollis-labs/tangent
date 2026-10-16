package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/participant"
)

// The `tangent session` commands (CW-20261016-0174).
//
// Browser participant sessions have no idle expiry, and an elevated session
// carries operator authority for its whole 30-day life. Revocation is
// therefore an explicit operator act, and it has to work during an incident —
// while Tangent is serving. So unlike --revoke-participant-sessions, these do
// not take the single-writer ownership lock: each is one UPDATE (or one
// SELECT) under SQLite's own locking, with no trigger suspension and no
// schema change, which is the hazard the lock exists for. A revoked session is
// refused on its very next HTTP request, since every request looks the
// session up, and on the next answer frame of any WebSocket it opened, since
// the socket re-reads its session before each authority-bearing frame.
//
// Output is counts only for revoke. list prints the first eight characters of
// each session id (the prefix revoke takes), never a cookie or its hash.

// stringList is a repeatable string flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func sessionUsage(out io.Writer) {
	_, _ = fmt.Fprint(out, `usage: tangent session <command>

commands:
  revoke --all                      revoke every live browser session
  revoke --id-prefix <8+ hex> ...   revoke live sessions whose id starts with the prefix (repeatable)
  list                              list sessions (id prefix, assurance, timestamps, state)

The database is the one tangent serves ($TANGENT_DB_PATH, or the default).
These commands are safe to run while tangent is serving.
`)
}

// runSessionCommand dispatches `tangent session <subcommand>` against dbPath.
func runSessionCommand(args []string, dbPath string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		sessionUsage(stderr)
		return 2
	}
	switch args[0] {
	case "revoke":
		return revokeSessions(args[1:], dbPath, stdout, stderr)
	case "list", "ls":
		return listSessions(dbPath, stdout, stderr)
	case "-h", "-help", "--help", "help":
		sessionUsage(stdout)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "tangent: unknown session command %q\n", args[0])
		sessionUsage(stderr)
		return 2
	}
}

func openSessionStore(dbPath string, stderr io.Writer) (*participant.Store, func(), bool) {
	sqlDB, err := tangentdb.Open(dbPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tangent: open db: %v\n", err)
		return nil, nil, false
	}
	closeDB := func() {
		if closeErr := tangentdb.Close(sqlDB); closeErr != nil {
			_, _ = fmt.Fprintf(stderr, "tangent: close db: %v\n", closeErr)
		}
	}
	store, err := participant.NewStore(sqlDB)
	if err != nil {
		closeDB()
		_, _ = fmt.Fprintf(stderr, "tangent: build participant session store: %v\n", err)
		return nil, nil, false
	}
	return store, closeDB, true
}

func revokeSessions(args []string, dbPath string, stdout, stderr io.Writer) int {
	flagSet := flag.NewFlagSet("tangent session revoke", flag.ContinueOnError)
	flagSet.SetOutput(stderr)
	all := flagSet.Bool("all", false, "revoke every live browser session")
	var prefixes stringList
	flagSet.Var(&prefixes, "id-prefix", "revoke live sessions whose id starts with this prefix (at least 8 hex digits; repeatable)")
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flagSet.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "tangent: unexpected argument %q\n", flagSet.Arg(0))
		return 2
	}
	if *all == (len(prefixes) > 0) {
		_, _ = fmt.Fprintln(stderr, "tangent: session revoke takes exactly one of --all or --id-prefix")
		return 2
	}
	for _, prefix := range prefixes {
		if !participant.ValidRevokePrefix(prefix) {
			_, _ = fmt.Fprintf(stderr, "tangent: --id-prefix must be at least %d hex digits\n",
				participant.MinRevokePrefixLength)
			return 2
		}
	}

	store, closeDB, ok := openSessionStore(dbPath, stderr)
	if !ok {
		return 1
	}
	defer closeDB()

	ctx := context.Background()
	var total int64
	if *all {
		revoked, err := store.RevokeAll(ctx, "operator-revoked")
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "tangent: revoke sessions: %v\n", err)
			return 1
		}
		total = revoked
	} else {
		for _, prefix := range prefixes {
			revoked, err := store.RevokeByIDPrefix(ctx, prefix, "operator-revoked")
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "tangent: revoke sessions: %v\n", err)
				return 1
			}
			total += revoked
		}
	}
	_, _ = fmt.Fprintf(stdout, "revoked %d session(s)\n", total)
	return 0
}

func listSessions(dbPath string, stdout, stderr io.Writer) int {
	store, closeDB, ok := openSessionStore(dbPath, stderr)
	if !ok {
		return 1
	}
	defer closeDB()
	sessions, err := store.List(context.Background())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tangent: list sessions: %v\n", err)
		return 1
	}
	now := time.Now()
	writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "ID PREFIX\tASSURANCE\tCREATED\tLAST SEEN\tSTATE")
	for _, session := range sessions {
		state := "live"
		switch {
		case session.Revoked:
			state = "revoked"
		case !now.Before(session.ExpiresAt):
			state = "expired"
		}
		id := session.ID
		if len(id) > participant.MinRevokePrefixLength {
			id = id[:participant.MinRevokePrefixLength]
		}
		_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", id, session.Assurance,
			session.CreatedAt.UTC().Format(time.RFC3339), session.LastSeenAt.UTC().Format(time.RFC3339), state)
	}
	_ = writer.Flush()
	return 0
}
