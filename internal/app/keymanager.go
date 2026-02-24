// Package app implements application-level services for the Gandalf LLM gateway.
package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"time"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/storage"
	"github.com/google/uuid"
)

// KeyManager handles API key lifecycle (create, delete).
type KeyManager struct {
	store storage.APIKeyStore
}

// NewKeyManager returns a KeyManager backed by store.
func NewKeyManager(store storage.APIKeyStore) *KeyManager {
	return &KeyManager{store: store}
}

// CreateKeyOpts holds all fields for API key creation.
type CreateKeyOpts struct {
	OrgID         string
	UserID        string
	TeamID        string
	Name          string
	Role          string
	AllowedModels []string
	RPMLimit      *int64
	TPMLimit      *int64
	MaxBudget     *float64
	ExpiresAt     *time.Time
}

// CreateKey generates a new API key with the given options, stores its hash,
// and returns the plaintext (shown once) along with the persisted APIKey record.
func (km *KeyManager) CreateKey(ctx context.Context, opts CreateKeyOpts) (string, *gateway.APIKey, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}

	plaintext := gateway.APIKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	hash := gateway.HashKey(plaintext)
	prefix := plaintext
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}

	role := opts.Role
	if role == "" {
		role = "member"
	}

	key := &gateway.APIKey{
		ID:            uuid.Must(uuid.NewV7()).String(),
		KeyHash:       hash,
		KeyPrefix:     prefix,
		OrgID:         opts.OrgID,
		UserID:        opts.UserID,
		TeamID:        opts.TeamID,
		Role:          role,
		AllowedModels: opts.AllowedModels,
		RPMLimit:      opts.RPMLimit,
		TPMLimit:      opts.TPMLimit,
		MaxBudget:     opts.MaxBudget,
		ExpiresAt:     opts.ExpiresAt,
		CreatedAt:     time.Now().UTC(),
	}

	if err := km.store.CreateKey(ctx, key); err != nil {
		return "", nil, err
	}

	return plaintext, key, nil
}

// DeleteKey removes the API key with the given ID.
func (km *KeyManager) DeleteKey(ctx context.Context, id string) error {
	return km.store.DeleteKey(ctx, id)
}
