package testutil

import (
	"context"
	"net/http"

	gateway "github.com/eugener/gandalf/internal"
)

// FakeAuth always authenticates successfully with admin permissions.
type FakeAuth struct{}

// Authenticate returns a test identity with admin permissions.
func (FakeAuth) Authenticate(_ context.Context, _ *http.Request) (*gateway.Identity, error) {
	return &gateway.Identity{
		Subject:    "test",
		OrgID:      "default",
		Role:       "admin",
		Perms:      gateway.RolePermissions["admin"],
		AuthMethod: "apikey",
	}, nil
}

// RejectAuth always rejects authentication.
type RejectAuth struct{}

// Authenticate always returns ErrUnauthorized.
func (RejectAuth) Authenticate(context.Context, *http.Request) (*gateway.Identity, error) {
	return nil, gateway.ErrUnauthorized
}
