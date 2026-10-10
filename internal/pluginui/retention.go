package pluginui

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Retention is an append-only, private route-identity reservation journal. Every
// reservation remains after disposal or an uncertain provision. No compaction
// or expiry is provided: deleting history invalidates this serving boundary.
type Retention struct {
	mu       sync.Mutex
	root     *os.Root
	identity string
}

const retentionMarker = "retention.identity"

// InitializeRetention is only for an explicitly new, nonexistent private store.
// The caller must retain the returned identity independently and pass it on
// reopen. OpenRetention never creates/reinitializes a missing store or marker.
func InitializeRetention(directory string) (identity string, err error) {
	if err = os.Mkdir(directory, 0700); err != nil {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", err
	}
	identity = hex.EncodeToString(raw)
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	if err = writeExclusive(root, retentionMarker, []byte(identity)); err != nil {
		return "", err
	}
	// Persist the directory entry in its parent as well as the store contents.
	parent, err := os.Open(filepath.Dir(directory))
	if err != nil {
		return "", err
	}
	err = errors.Join(parent.Sync(), parent.Close())
	return identity, err
}

func OpenRetention(directory, expectedIdentity string) (*Retention, error) {
	if !validDigest(expectedIdentity) {
		return nil, errors.New("pluginui: missing retention identity")
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, errors.New("pluginui: retention directory must be private")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	r := &Retention{root: root, identity: expectedIdentity}
	if err = r.verify(); err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return r, nil
}
func (r *Retention) verify() error {
	if r.root == nil {
		return errors.New("pluginui: retention closed")
	}
	info, err := r.root.Lstat(retentionMarker)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return errors.New("pluginui: invalid retention marker")
	}
	f, err := r.root.Open(retentionMarker)
	if err != nil {
		return err
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, 65))
	if err = errors.Join(readErr, f.Close()); err != nil {
		return err
	}
	if string(raw) != r.identity {
		return errors.New("pluginui: retention identity mismatch")
	}
	return nil
}
func writeExclusive(root *os.Root, name string, value []byte) error {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	n, writeErr := f.Write(value)
	if writeErr == nil && n != len(value) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = f.Sync()
	}
	err = errors.Join(writeErr, f.Close())
	if err != nil {
		return err
	} // Leave uncertain reservations intact, never reuse.
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
func (r *Retention) reserve(scope string, record []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.verify(); err != nil {
		return err
	}
	// This filename depends only on the route scope, never the replacement owner.
	if err := writeExclusive(r.root, digestBytes([]byte(scope))+".reserved", record); err != nil {
		return fmt.Errorf("pluginui: route scope unavailable: %w", err)
	}
	return nil
}
func (r *Retention) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.root == nil {
		return nil
	}
	err := r.root.Close()
	r.root = nil
	return err
}
