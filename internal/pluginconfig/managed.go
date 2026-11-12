package pluginconfig

import (
	"context"
	"strings"

	libsecrets "github.com/hollis-labs/libs/security/secrets"
)

// ManagedNamespace is the directory, inside the secrets module's file store, that holds plugin
// configuration secrets. An entry's reference is file://tangent-plugin-config/<account>.
const ManagedNamespace = "tangent-plugin-config"

// Managed keeps plugin secrets in the secrets module's managed file store (0600 files in a 0700
// directory, written atomically) instead of the OS keychain. Accounts are the opaque UUIDs the
// store already generates; anything that is not a plain name is refused, so an account can never
// name a path outside the namespace. The module trims surrounding whitespace and refuses an empty
// value, so a secret is stored as its trimmed text. Like OSKeychain, every failure is reported as ErrSecret and
// carries no value.
type Managed struct{ Resolver *libsecrets.Resolver }

// Ref is the secrets-module reference for an account.
func Ref(account string) string { return "file://" + ManagedNamespace + "/" + account }

func (m Managed) ref(account string) (string, bool) {
	if m.Resolver == nil || account == "" || strings.ContainsAny(account, "/\\") || strings.HasPrefix(account, ".") {
		return "", false
	}
	return Ref(account), true
}

func (m Managed) Get(ctx context.Context, account string) (string, error) {
	ref, ok := m.ref(account)
	if !ok {
		return "", ErrSecret
	}
	res, err := m.Resolver.Resolve(ctx, ref)
	if err != nil {
		return "", ErrSecret
	}
	return res.Secret.Reveal(), nil
}

func (m Managed) Set(ctx context.Context, account, value string) error {
	ref, ok := m.ref(account)
	if !ok {
		return ErrSecret
	}
	if err := m.Resolver.Set(ctx, ref, strings.NewReader(value)); err != nil {
		return ErrSecret
	}
	return nil
}

func (m Managed) Delete(ctx context.Context, account string) error {
	ref, ok := m.ref(account)
	if !ok {
		return ErrSecret
	}
	if err := m.Resolver.Delete(ctx, ref); err != nil && libsecrets.KindOf(err) != libsecrets.KindNotFound {
		return ErrSecret
	}
	return nil
}
