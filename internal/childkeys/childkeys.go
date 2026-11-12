// Package childkeys gives each plugin child process its own short-lived identity key.
//
// A subprocess plugin calls back into Tangent over /mcp (pkg/plugin/hostclient). /mcp refuses a
// request with no credential, so the host mints one per child at start: a key from the identity
// service, named for the plugin, holding what a plugin may use and never the decision or
// administration. It lives a few minutes and is renewed while the child lives; when the child
// stops it is revoked.
//
// The key reaches the child by reference: the child's environment carries the path of a file
// (mode 0600 in a 0700 directory) that holds the current key, and the host rewrites the file when it
// renews. A key literal in the environment could not be renewed, since a running process's
// environment does not change. hostclient reads the file on every request.
package childkeys

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// KeyFileEnv names the environment variable that carries the path of the file holding the
// child's current key. hostclient reads the same name.
const KeyFileEnv = "TANGENT_MCP_KEY_FILE"

// NamePrefix starts the name of every key minted here. The caller middleware keeps such a key in
// the unlabeled partition, like the in-process plugin host (see internal/mcp), so moving a
// child from a loopback caller to a key moves no data.
const NamePrefix = "plugin-"

// Issuer is the slice of the identity service the host needs.
type Issuer interface {
	// Create makes a key and returns its secret (shown once) and id.
	Create(ctx context.Context, name string, scopes []string, ttl time.Duration) (secret, id string, err error)
	// Revoke ends a key. A key already gone is not an error.
	Revoke(ctx context.Context, id string) error
}

// Leases mints and tracks the keys of plugin children.
type Leases struct {
	issuer Issuer
	scopes []string
	ttl    time.Duration
	renew  time.Duration
	log    *slog.Logger
}

// New returns Leases that mint keys holding scopes. ttl is each key's lifetime; renew is how
// often a live child's key is replaced (it must be well under ttl).
func New(issuer Issuer, scopes []string, ttl, renew time.Duration, log *slog.Logger) (*Leases, error) {
	if issuer == nil {
		return nil, errors.New("childkeys: no issuer")
	}
	if len(scopes) == 0 {
		return nil, errors.New("childkeys: a child key needs scopes")
	}
	if ttl <= 0 || renew <= 0 || renew >= ttl {
		return nil, errors.New("childkeys: renewal must be positive and shorter than the lifetime")
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Leases{issuer: issuer, scopes: append([]string(nil), scopes...), ttl: ttl, renew: renew, log: log}, nil
}

var unsafe = regexp.MustCompile(`[^a-z0-9_-]+`)

// Name is the key name for a plugin: reserved prefix plus the plugin id made into a caller name.
func Name(pluginID string) string {
	n := NamePrefix + strings.Trim(unsafe.ReplaceAllString(strings.ToLower(pluginID), "-"), "-")
	if len(n) > 64 {
		n = n[:64]
	}
	return n
}

// Lease is one child's key while the child lives.
type Lease struct {
	leases *Leases
	name   string
	path   string

	cancel context.CancelFunc
	done   chan struct{}

	mu       sync.Mutex
	current  string // id of the key in the file
	previous string // id of the one before, kept one cycle for requests in flight
}

// Open mints the child's first key, writes it to a file under dir, and keeps it renewed until
// Close. The returned environment entry names the file.
func (l *Leases) Open(ctx context.Context, pluginID, dir string) (*Lease, []string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("childkeys: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302 -- a directory: owner-only is 0700.
		return nil, nil, fmt.Errorf("childkeys: %w", err)
	}
	lease := &Lease{leases: l, name: Name(pluginID), path: filepath.Join(dir, "mcp-key"), done: make(chan struct{})}
	if err := lease.rotate(ctx); err != nil {
		return nil, nil, err
	}
	run, cancel := context.WithCancel(context.WithoutCancel(ctx))
	lease.cancel = cancel
	go lease.keepRenewed(run)
	return lease, []string{KeyFileEnv + "=" + lease.path}, nil
}

// rotate mints a new key, publishes it, and revokes the one before the previous.
func (e *Lease) rotate(ctx context.Context) error {
	secret, id, err := e.leases.issuer.Create(ctx, e.name, e.leases.scopes, e.leases.ttl)
	if err != nil {
		return fmt.Errorf("childkeys: mint a key for %s: %w", e.name, err)
	}
	tmp := e.path + ".tmp"
	if err = os.WriteFile(tmp, []byte(secret), 0o600); err != nil {
		_ = e.leases.issuer.Revoke(ctx, id)
		return fmt.Errorf("childkeys: %w", err)
	}
	if err = os.Rename(tmp, e.path); err != nil {
		_ = os.Remove(tmp)
		_ = e.leases.issuer.Revoke(ctx, id)
		return fmt.Errorf("childkeys: %w", err)
	}
	e.mu.Lock()
	stale := e.previous
	e.previous, e.current = e.current, id
	e.mu.Unlock()
	if stale != "" {
		e.revoke(ctx, stale)
	}
	return nil
}

func (e *Lease) revoke(ctx context.Context, id string) {
	if err := e.leases.issuer.Revoke(ctx, id); err != nil {
		// Never the key itself: its id and the plugin's name are enough to find it.
		e.leases.log.Warn("childkeys: could not revoke a key; it expires on its own", "plugin_key", e.name, "key_id", id, "error", err)
	}
}

func (e *Lease) keepRenewed(ctx context.Context) {
	defer close(e.done)
	tick := time.NewTicker(e.leases.renew)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := e.rotate(ctx); err != nil && ctx.Err() == nil {
				e.leases.log.Warn("childkeys: could not renew a key", "plugin_key", e.name, "error", err)
			}
		}
	}
}

// Close stops renewing, revokes the keys and removes the file. Safe to call more than once.
func (e *Lease) Close() {
	if e == nil {
		return
	}
	e.mu.Lock()
	if e.cancel == nil {
		e.mu.Unlock()
		return
	}
	cancel := e.cancel
	e.cancel = nil
	e.mu.Unlock()
	cancel()
	<-e.done
	e.mu.Lock()
	ids := []string{e.current, e.previous}
	e.current, e.previous = "", ""
	e.mu.Unlock()
	_ = os.Remove(e.path)
	for _, id := range ids {
		if id != "" {
			e.revoke(context.Background(), id)
		}
	}
}
