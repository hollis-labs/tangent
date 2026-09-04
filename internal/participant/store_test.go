package participant

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/authz"
	tangentdb "github.com/hollis-labs/tangent/internal/db"
)

// TestMintedSessionStoresOnlyTheHashOfItsCookie is acceptance criterion 3 at
// the storage layer: the cookie value is the only capability material in the
// system, and it must not exist in the database at all.
//
// The test sweeps every text column of every table rather than checking the
// session row, because "the value is not in the column we meant" is a weaker
// claim than "the value is nowhere".
func TestMintedSessionStoresOnlyTheHashOfItsCookie(t *testing.T) {
	t.Parallel()
	store, database := openTestStore(t)

	session, cookie, err := store.Mint(context.Background(), DefaultTemplate)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if cookie == "" {
		t.Fatal("Mint returned an empty cookie value")
	}
	assertValueAbsentFromDatabase(t, database, cookie)

	var storedHash string
	if scanErr := database.QueryRow(
		`SELECT cookie_sha256 FROM participant_sessions WHERE id = ?`, session.ID,
	).Scan(&storedHash); scanErr != nil {
		t.Fatalf("read stored hash: %v", scanErr)
	}
	if storedHash != HashCookie(cookie) {
		t.Fatal("stored hash does not match the cookie it was derived from")
	}

	resolved, err := store.Lookup(context.Background(), cookie)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if resolved.ID != session.ID || resolved.Scope != authz.ParticipantScope {
		t.Fatalf("Lookup returned %+v, want the minted session", resolved)
	}
	if !resolved.Holds(authz.Resolve) || resolved.Holds(authz.Close) {
		t.Fatalf("minted grants = %v, want the participant row without close", resolved.Grants)
	}
	if resolved.ExpiresAt.Sub(resolved.CreatedAt) != MaximumLifetime {
		t.Fatalf("session lifetime = %s, want the 30-day absolute maximum", resolved.ExpiresAt.Sub(resolved.CreatedAt))
	}
}

// TestLookupRefusesUnknownRevokedAndExpiredSessionsIdentically: three causes,
// one refusal, so a probe cannot tell them apart.
func TestLookupRefusesUnknownRevokedAndExpiredSessionsIdentically(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()

	if _, err := store.Lookup(ctx, "never-issued"); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("unknown cookie error = %v, want ErrSessionInvalid", err)
	}
	if _, err := store.Lookup(ctx, ""); !errors.Is(err, ErrNoSession) {
		t.Fatalf("empty cookie error = %v, want ErrNoSession", err)
	}

	revoked, revokedCookie, err := store.Mint(ctx, DefaultTemplate)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	ended, err := store.Revoke(ctx, revoked.ID, "test")
	if err != nil || !ended {
		t.Fatalf("Revoke = %v, %v", ended, err)
	}
	if _, lookupErr := store.Lookup(ctx, revokedCookie); !errors.Is(lookupErr, ErrSessionInvalid) {
		t.Fatalf("revoked cookie error = %v, want ErrSessionInvalid", lookupErr)
	}

	// Absolute expiry, with no idle expiry anywhere near it: the clock moves
	// past the 30-day maximum and nothing else changes.
	expired, expiredCookie, err := store.Mint(ctx, DefaultTemplate)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	store.now = func() time.Time { return expired.ExpiresAt.Add(time.Second) }
	if _, err := store.Lookup(ctx, expiredCookie); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("expired cookie error = %v, want ErrSessionInvalid", err)
	}
}

// TestRotateRevokesThePredecessorAndIssuesANewCookie covers the ADR 0004 §10.2
// composition point: an assurance change must not leave the old session id
// usable, and the audit trail has to cross the rotation.
func TestRotateRevokesThePredecessorAndIssuesANewCookie(t *testing.T) {
	t.Parallel()
	store, database := openTestStore(t)
	ctx := context.Background()

	original, originalCookie, err := store.Mint(ctx, DefaultTemplate)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	verified := DefaultTemplate
	verified.Assurance = "adapter-verified"
	verified.Ref = "opaque-principal-ref"
	successor, successorCookie, err := store.Rotate(ctx, original.ID, verified)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if successor.ID == original.ID || successorCookie == originalCookie {
		t.Fatal("rotation reused the predecessor's identity")
	}
	if _, lookupErr := store.Lookup(ctx, originalCookie); !errors.Is(lookupErr, ErrSessionInvalid) {
		t.Fatalf("predecessor still resolves after rotation: %v", lookupErr)
	}
	resolved, err := store.Lookup(ctx, successorCookie)
	if err != nil {
		t.Fatalf("Lookup successor: %v", err)
	}
	if resolved.Assurance != "adapter-verified" {
		t.Fatalf("successor assurance = %q, want the raised one", resolved.Assurance)
	}
	var rotatedTo sql.NullString
	if err := database.QueryRow(
		`SELECT rotated_to FROM participant_sessions WHERE id = ?`, original.ID,
	).Scan(&rotatedTo); err != nil {
		t.Fatalf("read rotation link: %v", err)
	}
	if !rotatedTo.Valid || rotatedTo.String != successor.ID {
		t.Fatal("the audit trail does not cross the rotation")
	}
	assertValueAbsentFromDatabase(t, database, originalCookie)
	assertValueAbsentFromDatabase(t, database, successorCookie)

	// Rotating an already-rotated session is a no-op refusal rather than a
	// second successor, so a retry cannot fork the identity.
	if _, _, err := store.Rotate(ctx, original.ID, verified); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("second rotation error = %v, want ErrSessionInvalid", err)
	}
}

// TestRevokeAllEndsEverySessionAtOnce backs the CLI flag, which is the only
// way to revoke.
func TestRevokeAllEndsEverySessionAtOnce(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()

	cookies := make([]string, 0, 3)
	for range 3 {
		_, cookie, err := store.Mint(ctx, DefaultTemplate)
		if err != nil {
			t.Fatalf("Mint: %v", err)
		}
		cookies = append(cookies, cookie)
	}
	revoked, err := store.RevokeAll(ctx, "operator-revoked")
	if err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}
	if revoked != 3 {
		t.Fatalf("RevokeAll revoked %d, want 3", revoked)
	}
	for _, cookie := range cookies {
		if _, err := store.Lookup(ctx, cookie); !errors.Is(err, ErrSessionInvalid) {
			t.Fatalf("session survived revocation: %v", err)
		}
	}
	// A second sweep finds nothing left, so revocation is idempotent.
	if again, err := store.RevokeAll(ctx, "operator-revoked"); err != nil || again != 0 {
		t.Fatalf("second RevokeAll = %d, %v, want 0", again, err)
	}
}

func openTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "participant.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrateErr := tangentdb.RunMigrations(database); migrateErr != nil {
		t.Fatalf("run migrations: %v", migrateErr)
	}
	store, err := NewStore(database)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, database
}

// assertValueAbsentFromDatabase sweeps every text column of every table for a
// literal value. It is deliberately exhaustive: the guarantee is that a cookie
// value exists nowhere in durable storage, not that it is absent from the one
// column we thought to check.
func assertValueAbsentFromDatabase(t *testing.T, database *sql.DB, value string) {
	t.Helper()
	tables, err := database.Query(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer func() { _ = tables.Close() }()
	names := make([]string, 0, 32)
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		names = append(names, name)
	}
	if err := tables.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	for _, name := range names {
		// The table name comes from sqlite_master, not from any input; there
		// is no parameter form for an identifier.
		//nolint:gosec // G202: identifier from sqlite_master, not user input
		rows, err := database.Query(`SELECT * FROM "` + name + `"`)
		if err != nil {
			t.Fatalf("scan %s: %v", name, err)
		}
		columns, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			t.Fatalf("columns of %s: %v", name, err)
		}
		for rows.Next() {
			cells := make([]any, len(columns))
			targets := make([]any, len(columns))
			for index := range cells {
				targets[index] = &cells[index]
			}
			if err := rows.Scan(targets...); err != nil {
				_ = rows.Close()
				t.Fatalf("scan row of %s: %v", name, err)
			}
			for index, cell := range cells {
				text, ok := cellText(cell)
				if ok && strings.Contains(text, value) {
					_ = rows.Close()
					t.Fatalf("capability material found in %s.%s", name, columns[index])
				}
			}
		}
		_ = rows.Close()
	}
}

func cellText(cell any) (string, bool) {
	switch typed := cell.(type) {
	case string:
		return typed, true
	case []byte:
		return string(typed), true
	default:
		return "", false
	}
}
