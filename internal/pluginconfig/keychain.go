package pluginconfig

import (
	"context"
	"errors"

	"github.com/zalando/go-keyring"
)

const keychainService = "hollis-labs.tangent.plugin-config"

// OSKeychain uses macOS Keychain, Windows Credential Manager or Linux Secret
// Service. It has no file/environment fallback. Constructing it touches nothing.
// The underlying native API is synchronous; context is checked before and after
// each call, rather than abandoning a credential operation in a goroutine.
type OSKeychain struct{}

func (OSKeychain) Get(ctx context.Context, account string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	value, err := keyring.Get(keychainService, account)
	if err != nil {
		return "", ErrSecret
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	return value, nil
}
func (OSKeychain) Set(ctx context.Context, account, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := keyring.Set(keychainService, account, value); err != nil {
		return ErrSecret
	}
	return ctx.Err()
}
func (OSKeychain) Delete(ctx context.Context, account string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := keyring.Delete(keychainService, account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return ErrSecret
	}
	return ctx.Err()
}
