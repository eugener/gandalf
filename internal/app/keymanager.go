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

// CreateKey generates a new API key for the given org, stores its hash,
// and returns the plaintext (shown once) along with the persisted APIKey record.
func (km *KeyManager) CreateKey(ctx context.Context, orgID, name, role string) (string, *gateway.APIKey, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}

	plaintext := gateway.APIKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	hash := gateway.HashKey(plaintext)

	key := &gateway.APIKey{
		ID:        uuid.New().String(),
		KeyHash:   hash,
		KeyPrefix: plaintext[:8],
		OrgID:     orgID,
		CreatedAt: time.Now().UTC(),
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
