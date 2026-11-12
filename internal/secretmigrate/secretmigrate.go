// Package secretmigrate moves plugin configuration secrets from the OS keychain, where the
// pre-Substrate Tangent kept them, into the secrets module's store. The references to move come
// from the plugin configuration database (plugin_config_override.secret_ref); a value is read,
// written, read back and compared by length and a keyed digest, and is never printed, logged or
// passed on a command line. The keychain entries stay where they are, as the way back.
package secretmigrate

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	libsecrets "github.com/hollis-labs/libs/security/secrets"
	"github.com/zalando/go-keyring"
	_ "modernc.org/sqlite" // the driver the reference list is read through

	"github.com/hollis-labs/tangent/internal/pluginconfig"
)

// KeychainService is the service name the old store used.
const KeychainService = "hollis-labs.tangent.plugin-config"

// Source reads one old value. The real one is the OS keychain.
type Source interface {
	Get(account string) (string, error)
}

// OSKeychain reads the old keychain entries.
type OSKeychain struct{}

// Get returns the stored value for an account.
func (OSKeychain) Get(account string) (string, error) { return keyring.Get(KeychainService, account) }

// Ref is one secret to move: which plugin setting it belongs to, and the keychain account.
type Ref struct{ Plugin, Key, Account string }

// Item is the outcome for one reference. Nothing here is secret.
type Item struct {
	Ref
	Length int
	Status string // "would move", "moved", "already present", "missing from keychain", or "failed: ..."
	OK     bool
}

// References lists the secrets the configuration database points at.
func References(ctx context.Context, configDB string) ([]Ref, error) {
	if _, err := os.Stat(configDB); err != nil {
		return nil, fmt.Errorf("plugin configuration database: %w", err)
	}
	dsn := (&url.URL{Scheme: "file", Path: configDB, RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close() //nolint:errcheck // read-only
	rows, err := db.QueryContext(ctx, "SELECT DISTINCT plugin, key, secret_ref FROM plugin_config_override WHERE secret_ref <> '' ORDER BY plugin, key, secret_ref")
	if err != nil {
		return nil, fmt.Errorf("read plugin_config_override: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only
	var out []Ref
	for rows.Next() {
		var r Ref
		if err := rows.Scan(&r.Plugin, &r.Key, &r.Account); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Move copies each reference's value from src into the secrets store at dest. With dryRun it only
// reads the source to report what would move: nothing is written. An entry already in the store
// with the same value is accepted; one with a different value is a failure, never overwritten.
func Move(ctx context.Context, refs []Ref, src Source, dest *libsecrets.Resolver, dryRun bool) []Item {
	key := make([]byte, 32)
	_, _ = rand.Read(key) // an unguessable per-run key: the digests below mean nothing outside this process
	digest := func(s string) string {
		m := hmac.New(sha256.New, key)
		m.Write([]byte(s))
		return string(m.Sum(nil))
	}
	items := make([]Item, 0, len(refs))
	for _, r := range refs {
		it := Item{Ref: r}
		old, err := src.Get(r.Account)
		switch {
		case errors.Is(err, keyring.ErrNotFound):
			it.Status = "missing from keychain"
		case err != nil:
			it.Status = "failed: the keychain could not be read"
		default:
			want := strings.TrimSpace(old) // the secrets store keeps the trimmed text
			it.Length = len(want)
			it.Status, it.OK = moveOne(ctx, pluginconfig.Ref(r.Account), want, dest, dryRun, digest)
		}
		items = append(items, it)
	}
	return items
}

func moveOne(ctx context.Context, ref, want string, dest *libsecrets.Resolver, dryRun bool, digest func(string) string) (string, bool) {
	if want == "" {
		return "failed: the keychain value is empty", false
	}
	existing, err := dest.Resolve(ctx, ref)
	if err == nil {
		got := existing.Secret.Reveal()
		if len(got) == len(want) && digest(got) == digest(want) {
			return "already present", true
		}
		return "failed: the store already holds a different value; not overwritten", false
	}
	if libsecrets.KindOf(err) != libsecrets.KindNotFound {
		return "failed: the secrets store could not be read", false
	}
	if dryRun {
		return "would move", true
	}
	if err = dest.Set(ctx, ref, strings.NewReader(want)); err != nil {
		return "failed: the secrets store could not be written", false
	}
	back, err := dest.Resolve(ctx, ref)
	if err != nil {
		return "failed: the written value could not be read back", false
	}
	got := back.Secret.Reveal()
	if len(got) != len(want) || digest(got) != digest(want) {
		_ = dest.Delete(ctx, ref) // never leave a value that does not match
		return "failed: the written value differs (removed)", false
	}
	return "moved", true
}
