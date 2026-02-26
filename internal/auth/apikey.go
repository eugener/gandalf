// Package auth implements API key authentication for the Gandalf gateway.
// Keys are validated against the store and cached in a W-TinyLFU cache.
package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/storage"
	"github.com/maypok86/otter/v2"
)

const (
	cacheTTL    = 30 * time.Second // short enough to pick up key revocations promptly
	cacheMaxLen = 10_000           // max concurrent active keys expected per deployment
)

// APIKeyAuth authenticates requests using API keys with "gnd_" prefix.
// It caches resolved API keys in an otter W-TinyLFU cache for fast lookups.
type APIKeyAuth struct {
	store       storage.APIKeyStore
	cache       *otter.Cache[string, *gateway.APIKey]
	keyIDToHash sync.Map // keyID -> hash for cache invalidation by key ID
}

// NewAPIKeyAuth returns a new APIKeyAuth backed by store.
func NewAPIKeyAuth(store storage.APIKeyStore) (*APIKeyAuth, error) {
	c, err := otter.New(&otter.Options[string, *gateway.APIKey]{
		MaximumSize:      cacheMaxLen,
		ExpiryCalculator: otter.ExpiryWriting[string, *gateway.APIKey](cacheTTL),
	})
	if err != nil {
		return nil, fmt.Errorf("create auth cache: %w", err)
	}
	return &APIKeyAuth{store: store, cache: c}, nil
}

// Authenticate extracts a Bearer token from the Authorization header,
// validates it against the store, and returns the caller's Identity.
// Only keys with the "gnd_" prefix are handled; all others return ErrUnauthorized.
func (a *APIKeyAuth) Authenticate(ctx context.Context, r *http.Request) (*gateway.Identity, error) {
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if raw == "" || raw == r.Header.Get("Authorization") {
		return nil, gateway.ErrUnauthorized
	}

	if !strings.HasPrefix(raw, gateway.APIKeyPrefix) {
		return nil, gateway.ErrUnauthorized
	}

	hash := gateway.HashKey(raw)

	// Check cache first.
	if key, ok := a.cache.GetIfPresent(hash); ok {
		if key.Blocked {
			return nil, gateway.ErrKeyBlocked
		}
		if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
			a.cache.Invalidate(hash)
			return nil, gateway.ErrKeyExpired
		}
		return buildIdentity(key), nil
	}

	key, err := a.store.GetKeyByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, gateway.ErrNotFound) {
			return nil, gateway.ErrUnauthorized
		}
		return nil, err
	}

	// Belt-and-suspenders: constant-time comparison of the stored hash against
	// the computed hash. The DB lookup already matched, but this guards against
	// hypothetical SQL collation or encoding surprises.
	if subtle.ConstantTimeCompare([]byte(key.KeyHash), []byte(hash)) != 1 {
		return nil, gateway.ErrUnauthorized
	}

	if key.Blocked {
		return nil, gateway.ErrKeyBlocked
	}
	if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
		return nil, gateway.ErrKeyExpired
	}

	a.cache.Set(hash, key)
	a.keyIDToHash.Store(key.ID, hash)

	// Touch last-used timestamp asynchronously.
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		a.store.TouchKeyUsed(ctx, key.ID) //nolint:errcheck
	}()

	return buildIdentity(key), nil
}

// InvalidateByKeyID removes a cached API key by its key ID.
// Used when admin operations (block, update, delete) modify a key.
func (a *APIKeyAuth) InvalidateByKeyID(keyID string) {
	if hash, ok := a.keyIDToHash.LoadAndDelete(keyID); ok {
		a.cache.Invalidate(hash.(string))
	}
}

// buildIdentity constructs an Identity from a validated API key.
func buildIdentity(key *gateway.APIKey) *gateway.Identity {
	role := key.Role
	if role == "" {
		role = "member"
	}
	perms := gateway.RolePermissions[role]
	id := &gateway.Identity{
		Subject:    key.KeyPrefix,
		KeyID:      key.ID,
		OrgID:      key.OrgID,
		TeamID:     key.TeamID,
		UserID:     key.UserID,
		Role:       role,
		Perms:      perms,
		AuthMethod: "apikey",
	}
	if key.RPMLimit != nil {
		id.RPMLimit = *key.RPMLimit
	}
	if key.TPMLimit != nil {
		id.TPMLimit = *key.TPMLimit
	}
	if key.MaxBudget != nil {
		id.MaxBudget = *key.MaxBudget
	}
	if len(key.AllowedModels) > 0 {
		id.AllowedModels = key.AllowedModels
	}
	return id
}
