package childkeys

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIssuer records what the host asked identity to do.
type fakeIssuer struct {
	mu      sync.Mutex
	n       int
	live    map[string]string // id -> secret
	revoked []string
	names   []string
	ttls    []time.Duration
}

func (f *fakeIssuer) Create(_ context.Context, name string, _ []string, ttl time.Duration) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	if f.live == nil {
		f.live = map[string]string{}
	}
	id, secret := fmt.Sprintf("k_%d", f.n), fmt.Sprintf("secret-%d", f.n)
	f.live[id] = secret
	f.names = append(f.names, name)
	f.ttls = append(f.ttls, ttl)
	return secret, id, nil
}

func (f *fakeIssuer) Revoke(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.live, id)
	f.revoked = append(f.revoked, id)
	return nil
}

func (f *fakeIssuer) liveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.live)
}

func TestAChildGetsAShortLivedKeyInAPrivateFileAndItIsRevokedWhenTheChildStops(t *testing.T) {
	issuer := &fakeIssuer{}
	leases, err := New(issuer, []string{"tangent.view"}, time.Hour, 30*time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "run")
	lease, env, err := leases.Open(context.Background(), "Tangent.Runner", dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mcp-key")
	if len(env) != 1 || env[0] != KeyFileEnv+"="+path {
		t.Fatalf("env = %v", env)
	}
	if info, statErr := os.Stat(path); statErr != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", info, statErr)
	}
	if info, statErr := os.Stat(dir); statErr != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("key dir: %v %v", info, statErr)
	}
	if got, _ := os.ReadFile(path); string(got) != "secret-1" { // #nosec G304 -- a file in this test's temp directory.
		t.Fatalf("the file holds %q", got)
	}
	if issuer.names[0] != "plugin-tangent-runner" || issuer.ttls[0] != time.Hour {
		t.Fatalf("minted %v with lifetimes %v", issuer.names, issuer.ttls)
	}

	lease.Close()
	lease.Close() // idempotent
	if issuer.liveCount() != 0 {
		t.Fatalf("keys still live after the child stopped: %v", issuer.live)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("the key file outlived the child")
	}
}

func TestAKeyIsRenewedWhileTheChildLivesAndOnlyTwoAreEverLive(t *testing.T) {
	issuer := &fakeIssuer{}
	leases, err := New(issuer, []string{"tangent.view"}, time.Second, 20*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "run")
	lease, _, err := leases.Open(context.Background(), "p", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		issuer.mu.Lock()
		made := issuer.n
		issuer.mu.Unlock()
		if made >= 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the key was never renewed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if live := issuer.liveCount(); live > 2 {
		t.Fatalf("%d keys live at once; the one before the previous must be revoked", live)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "mcp-key")) // #nosec G304 -- a file in this test's temp directory.
	issuer.mu.Lock()
	_, current := issuer.live[strings.Replace(string(got), "secret-", "k_", 1)]
	issuer.mu.Unlock()
	if !current {
		t.Fatalf("the file holds %q, which is not a live key", got)
	}
}

func TestRenewalMustBeShorterThanTheLifetime(t *testing.T) {
	if _, err := New(&fakeIssuer{}, []string{"s"}, time.Minute, time.Minute, nil); err == nil {
		t.Fatal("a renewal as long as the lifetime was accepted")
	}
	if _, err := New(&fakeIssuer{}, nil, time.Minute, time.Second, nil); err == nil {
		t.Fatal("a child key with no scopes was accepted")
	}
}
