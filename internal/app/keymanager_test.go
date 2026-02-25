package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	gateway "github.com/eugener/gandalf/internal"
)

// fakeKeyStore is a minimal inline fake for testing KeyManager.
type fakeKeyStore struct {
	created  *gateway.APIKey
	deleted  string
	createFn func(context.Context, *gateway.APIKey) error
	deleteFn func(context.Context, string) error
}

func (s *fakeKeyStore) CreateKey(ctx context.Context, key *gateway.APIKey) error {
	if s.createFn != nil {
		return s.createFn(ctx, key)
	}
	s.created = key
	return nil
}
func (s *fakeKeyStore) GetKey(context.Context, string) (*gateway.APIKey, error) {
	return nil, gateway.ErrNotFound
}
func (s *fakeKeyStore) GetKeyByHash(context.Context, string) (*gateway.APIKey, error) {
	return nil, gateway.ErrNotFound
}
func (s *fakeKeyStore) ListKeys(context.Context, string, int, int) ([]*gateway.APIKey, error) {
	return nil, nil
}
func (s *fakeKeyStore) CountKeys(context.Context, string) (int, error) { return 0, nil }
func (s *fakeKeyStore) UpdateKey(context.Context, *gateway.APIKey) error {
	return nil
}
func (s *fakeKeyStore) DeleteKey(ctx context.Context, id string) error {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, id)
	}
	s.deleted = id
	return nil
}
func (s *fakeKeyStore) TouchKeyUsed(context.Context, string) error { return nil }

func TestCreateKey(t *testing.T) {
	t.Parallel()

	store := &fakeKeyStore{}
	km := NewKeyManager(store)

	plaintext, key, err := km.CreateKey(context.Background(), CreateKeyOpts{
		OrgID: "org-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plaintext, gateway.APIKeyPrefix) {
		t.Errorf("plaintext should have %s prefix, got %q", gateway.APIKeyPrefix, plaintext)
	}
	if key.KeyHash == "" {
		t.Error("key hash should be set")
	}
	if key.KeyHash != gateway.HashKey(plaintext) {
		t.Error("key hash should match HashKey(plaintext)")
	}
	if key.Role != "member" {
		t.Errorf("default role = %q, want member", key.Role)
	}
	if key.OrgID != "org-1" {
		t.Errorf("org_id = %q, want org-1", key.OrgID)
	}
	if store.created == nil {
		t.Error("store.CreateKey should have been called")
	}
}

func TestCreateKey_WithOpts(t *testing.T) {
	t.Parallel()

	store := &fakeKeyStore{}
	km := NewKeyManager(store)

	expiry := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	rpm := int64(100)
	tpm := int64(50000)
	budget := 10.0

	_, key, err := km.CreateKey(context.Background(), CreateKeyOpts{
		OrgID:         "org-2",
		UserID:        "user-1",
		TeamID:        "team-1",
		Role:          "admin",
		AllowedModels: []string{"gpt-4o"},
		RPMLimit:      &rpm,
		TPMLimit:      &tpm,
		MaxBudget:     &budget,
		ExpiresAt:     &expiry,
	})
	if err != nil {
		t.Fatal(err)
	}
	if key.Role != "admin" {
		t.Errorf("role = %q, want admin", key.Role)
	}
	if key.ExpiresAt == nil || !key.ExpiresAt.Equal(expiry) {
		t.Errorf("expires_at = %v, want %v", key.ExpiresAt, expiry)
	}
	if key.RPMLimit == nil || *key.RPMLimit != 100 {
		t.Errorf("rpm_limit = %v, want 100", key.RPMLimit)
	}
	if key.TPMLimit == nil || *key.TPMLimit != 50000 {
		t.Errorf("tpm_limit = %v, want 50000", key.TPMLimit)
	}
	if key.MaxBudget == nil || *key.MaxBudget != 10.0 {
		t.Errorf("max_budget = %v, want 10.0", key.MaxBudget)
	}
	if len(key.AllowedModels) != 1 || key.AllowedModels[0] != "gpt-4o" {
		t.Errorf("allowed_models = %v", key.AllowedModels)
	}
}

func TestCreateKey_StoreError(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("db failure")
	store := &fakeKeyStore{
		createFn: func(context.Context, *gateway.APIKey) error { return storeErr },
	}
	km := NewKeyManager(store)

	_, _, err := km.CreateKey(context.Background(), CreateKeyOpts{OrgID: "org-1"})
	if !errors.Is(err, storeErr) {
		t.Errorf("err = %v, want %v", err, storeErr)
	}
}

func TestDeleteKey(t *testing.T) {
	t.Parallel()

	store := &fakeKeyStore{}
	km := NewKeyManager(store)

	if err := km.DeleteKey(context.Background(), "key-123"); err != nil {
		t.Fatal(err)
	}
	if store.deleted != "key-123" {
		t.Errorf("deleted = %q, want key-123", store.deleted)
	}
}

func TestDeleteKey_StoreError(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("delete failed")
	store := &fakeKeyStore{
		deleteFn: func(context.Context, string) error { return storeErr },
	}
	km := NewKeyManager(store)

	err := km.DeleteKey(context.Background(), "key-123")
	if !errors.Is(err, storeErr) {
		t.Errorf("err = %v, want %v", err, storeErr)
	}
}
