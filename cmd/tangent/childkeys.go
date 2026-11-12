package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/identity"
)

// revokeRoute is the identity module's published key-revocation path.
const revokeRoute = "/api/identity/keys/"

// keyIssuer is the identity module as the host's plugin-child key issuer (internal/childkeys).
type keyIssuer struct {
	svc    *identity.Service
	revoke http.Handler
}

// newKeyIssuer reads the revoke handler from the routes the identity module published. The
// service has no exported revoke, so the host calls the module's own DELETE /api/identity/keys/
// handler in-process; the guard is not in that path because the host is the caller, not a client.
//
// FRAMEWORK GAP: identity.Service.RevokeKey has been asked of the Framework Agent. When it lands,
// replace this lookup with the call and delete revokeRoute. The lookup is by exact method and
// path, so TestTheKeyRevocationRouteIsStillPublished fails if the module moves or renames it.
func newKeyIssuer(app *module.App, svc *identity.Service) (*keyIssuer, error) {
	for _, route := range module.Routes(app) {
		if route.Method == http.MethodDelete && route.Path == revokeRoute && route.Handler != nil {
			return &keyIssuer{svc: svc, revoke: route.Handler}, nil
		}
	}
	return nil, fmt.Errorf("the identity module published no key-revocation route")
}

func (k *keyIssuer) Create(ctx context.Context, name string, scopes []string, ttl time.Duration) (string, string, error) {
	secret, credential, err := k.svc.CreateKey(ctx, identity.KeyInput{Name: name, Scopes: scopes, TTL: ttl, CreatedBy: "tangent:plugin-host"})
	if err != nil {
		return "", "", err
	}
	return secret, credential.ID, nil
}

func (k *keyIssuer) Revoke(ctx context.Context, id string) error {
	request := httptest.NewRequest(http.MethodDelete, revokeRoute+id, nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	k.revoke.ServeHTTP(recorder, request)
	switch recorder.Code {
	case http.StatusNoContent, http.StatusNotFound:
		return nil // revoked, or already gone
	}
	return fmt.Errorf("revoke key %s: status %d", id, recorder.Code)
}
