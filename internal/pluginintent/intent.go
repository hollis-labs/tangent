// Package pluginintent holds operator enable decisions, separate from plugin
// configuration and artifact declarations. An absent decision is disabled.
package pluginintent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

var ErrBusy = errors.New("plugin enable intent is being updated; retry")
var ErrUnsupported = errors.New("plugin enable intent locking is unavailable on this platform")

func Path(root string) string { return filepath.Join(root, ".state", "enabled.json") }

// Read is a pure snapshot. Previously recorded true and false decisions remain
// authoritative; neither discovery nor an upgrade fills absent decisions.
func Read(path string) (map[string]bool, error) {
	f, err := os.Open(path) // #nosec G304 -- host/operator-selected boolean intent path.
	if errors.Is(err, os.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	err = errors.Join(err, f.Close())
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, errors.New("plugin enable intent exceeds limit")
	}
	var intent map[string]bool
	if err = json.Unmarshal(data, &intent); err != nil || intent == nil {
		return nil, errors.New("invalid plugin enable intent")
	}
	return intent, nil
}

// Set atomically changes one decision. The shared, nonblocking file lock and
// fresh read prevent an offline CLI and a serving host from losing sibling
// decisions. An offline change takes effect when the host next starts.
func Set(ctx context.Context, path, id string, enabled bool) (map[string]bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, errors.New("plugin enable intent requires an installed ID")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600) // #nosec G304 -- fixed sidecar of the host-selected intent path.
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	if err = tryLock(lock); err != nil {
		return nil, err
	}
	defer func() { _ = unlock(lock) }()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	intent, err := Read(path)
	if err != nil {
		return nil, err
	}
	intent[id] = enabled
	data, err := json.Marshal(intent)
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, errors.New("plugin enable intent exceeds limit")
	}
	f, err := os.CreateTemp(dir, ".enabled-*")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = os.Rename(name, path); err != nil {
		return nil, err
	}
	directory, err := os.Open(dir) // #nosec G304 -- sync the host-selected intent parent after atomic rename.
	if err != nil {
		return nil, err
	}
	if err = errors.Join(directory.Sync(), directory.Close()); err != nil {
		return nil, err
	}
	return intent, nil
}
