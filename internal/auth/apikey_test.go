package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	gateway "github.com/eugener/gandalf/internal"
)

// fakeKeyStore is a minimal in-memory APIKeyStore for auth tests.
type fakeKeyStore struct {
	mu      sync.RWMutex
	keys    map[string]*gateway.APIKey // hash -> key
	touched map[string]int            // id -> touch count
}

func newFakeKeyStore() *fakeKeyStore {
	return &fakeKeyStore{
		keys:    make(map[string]*gateway.APIKey),
		touched: make(map[string]int),
	}
}

func (s *fakeKeyStore) addKey(raw string, key *gateway.APIKey) {
	key.KeyHash = gateway.HashKey(raw)
	s.mu.Lock()
	s.keys[key.KeyHash] = key
	s.mu.Unlock()
}

func (s *fakeKeyStore) CreateKey(_ context.Context, key *gateway.APIKey) error {
	s.mu.Lock()
	s.keys[key.KeyHash] = key
	s.mu.Unlock()
	return nil
}

func (s *fakeKeyStore) GetKeyByHash(_ context.Context, hash string) (*gateway.APIKey, error) {
	s.mu.RLock()
	k, ok := s.keys[hash]
	s.mu.RUnlock()
	if !ok {
		return nil, gateway.ErrNotFound
	}
	return k, nil
}

func (s *fakeKeyStore) GetKey(context.Context, string) (*gateway.APIKey, error) { return nil, gateway.ErrNotFound }
func (s *fakeKeyStore) ListKeys(context.Context, string, int, int) ([]*gateway.APIKey, error) {
	return nil, nil
}
func (s *fakeKeyStore) CountKeys(context.Context, string) (int, error) { return 0, nil }
func (s *fakeKeyStore) UpdateKey(context.Context, *gateway.APIKey) error { return nil }
func (s *fakeKeyStore) DeleteKey(context.Context, string) error          { return nil }

func (s *fakeKeyStore) TouchKeyUsed(_ context.Context, id string) error {
	s.mu.Lock()
	s.touched[id]++
	s.mu.Unlock()
	return nil
}

func (s *fakeKeyStore) touchCount(id string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.touched[id]
}

const testKey = "gnd_test_key_12345678901234567890"

func newTestAuth(t *testing.T) (*APIKeyAuth, *fakeKeyStore) {
	t.Helper()
	store := newFakeKeyStore()
	auth, err := NewAPIKeyAuth(store)
	if err != nil {
		t.Fatal(err)
	}
	return auth, store
}

func makeRequest(key string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	return r
}

func TestAuthenticate_ValidKey(t *testing.T) {
	t.Parallel()
	auth, store := newTestAuth(t)

	store.addKey(testKey, &gateway.APIKey{
		ID:        "key-1",
		KeyPrefix: "gnd_test_key",
		OrgID:     "org-1",
		TeamID:    "team-1",
		UserID:    "user-1",
	})

	id, err := auth.Authenticate(context.Background(), makeRequest(testKey))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.OrgID != "org-1" {
		t.Errorf("OrgID = %q, want org-1", id.OrgID)
	}
	if id.TeamID != "team-1" {
		t.Errorf("TeamID = %q, want team-1", id.TeamID)
	}
	if id.UserID != "user-1" {
		t.Errorf("UserID = %q, want user-1", id.UserID)
	}
	if id.Subject != "gnd_test_key" {
		t.Errorf("Subject = %q, want gnd_test_key", id.Subject)
	}
	if id.Role != "member" {
		t.Errorf("Role = %q, want member", id.Role)
	}
	if id.AuthMethod != "apikey" {
		t.Errorf("AuthMethod = %q, want apikey", id.AuthMethod)
	}
	if !id.Can(gateway.PermUseModels) {
		t.Error("member should have PermUseModels")
	}
}

func TestAuthenticate_CacheHit(t *testing.T) {
	t.Parallel()
	auth, store := newTestAuth(t)

	store.addKey(testKey, &gateway.APIKey{
		ID:        "key-1",
		KeyPrefix: "gnd_test_key",
		OrgID:     "org-1",
	})

	// First call populates cache.
	_, err := auth.Authenticate(context.Background(), makeRequest(testKey))
	if err != nil {
		t.Fatal(err)
	}

	// Remove from store -- second call should hit cache.
	store.mu.Lock()
	delete(store.keys, gateway.HashKey(testKey))
	store.mu.Unlock()

	id, err := auth.Authenticate(context.Background(), makeRequest(testKey))
	if err != nil {
		t.Fatalf("cache miss: %v", err)
	}
	if id.OrgID != "org-1" {
		t.Errorf("OrgID = %q, want org-1", id.OrgID)
	}
}

func TestAuthenticate_NoAuthHeader(t *testing.T) {
	t.Parallel()
	auth, _ := newTestAuth(t)

	_, err := auth.Authenticate(context.Background(), makeRequest(""))
	if err != gateway.ErrUnauthorized {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestAuthenticate_NonBearerToken(t *testing.T) {
	t.Parallel()
	auth, _ := newTestAuth(t)

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	_, err := auth.Authenticate(context.Background(), r)
	if err != gateway.ErrUnauthorized {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestAuthenticate_NonGndPrefix(t *testing.T) {
	t.Parallel()
	auth, _ := newTestAuth(t)

	_, err := auth.Authenticate(context.Background(), makeRequest("sk-not-a-gandalf-key"))
	if err != gateway.ErrUnauthorized {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestAuthenticate_KeyNotFound(t *testing.T) {
	t.Parallel()
	auth, _ := newTestAuth(t)

	_, err := auth.Authenticate(context.Background(), makeRequest("gnd_unknown_key_does_not_exist"))
	if err != gateway.ErrUnauthorized {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestAuthenticate_BlockedKey(t *testing.T) {
	t.Parallel()
	auth, store := newTestAuth(t)

	store.addKey(testKey, &gateway.APIKey{
		ID:        "key-blocked",
		KeyPrefix: "gnd_test_key",
		OrgID:     "org-1",
		Blocked:   true,
	})

	_, err := auth.Authenticate(context.Background(), makeRequest(testKey))
	if err != gateway.ErrKeyBlocked {
		t.Errorf("err = %v, want ErrKeyBlocked", err)
	}
}

func TestAuthenticate_BlockedKeyCached(t *testing.T) {
	t.Parallel()
	auth, store := newTestAuth(t)

	store.addKey(testKey, &gateway.APIKey{
		ID:        "key-blocked-cache",
		KeyPrefix: "gnd_test_key",
		OrgID:     "org-1",
		Blocked:   true,
	})

	// First call caches the blocked key.
	auth.Authenticate(context.Background(), makeRequest(testKey))

	// Second call should still return blocked from cache.
	_, err := auth.Authenticate(context.Background(), makeRequest(testKey))
	if err != gateway.ErrKeyBlocked {
		t.Errorf("err = %v, want ErrKeyBlocked", err)
	}
}

func TestAuthenticate_ExpiredKey(t *testing.T) {
	t.Parallel()
	auth, store := newTestAuth(t)

	expired := time.Now().Add(-1 * time.Hour)
	store.addKey(testKey, &gateway.APIKey{
		ID:        "key-expired",
		KeyPrefix: "gnd_test_key",
		OrgID:     "org-1",
		ExpiresAt: &expired,
	})

	_, err := auth.Authenticate(context.Background(), makeRequest(testKey))
	if err != gateway.ErrKeyExpired {
		t.Errorf("err = %v, want ErrKeyExpired", err)
	}
}

func TestAuthenticate_ExpiredKeyCacheInvalidation(t *testing.T) {
	t.Parallel()
	auth, store := newTestAuth(t)

	future := time.Now().Add(1 * time.Hour)
	store.addKey(testKey, &gateway.APIKey{
		ID:        "key-will-expire",
		KeyPrefix: "gnd_test_key",
		OrgID:     "org-1",
		ExpiresAt: &future,
	})

	// First call succeeds and caches.
	_, err := auth.Authenticate(context.Background(), makeRequest(testKey))
	if err != nil {
		t.Fatal(err)
	}

	// Mutate the cached key's expiry to the past (simulates time passing).
	hash := gateway.HashKey(testKey)
	if cached, ok := auth.cache.GetIfPresent(hash); ok {
		past := time.Now().Add(-1 * time.Hour)
		cached.ExpiresAt = &past
	}

	// Next call should detect expiry from cache and invalidate.
	_, err = auth.Authenticate(context.Background(), makeRequest(testKey))
	if err != gateway.ErrKeyExpired {
		t.Errorf("err = %v, want ErrKeyExpired", err)
	}

	// Cache should be invalidated.
	if _, ok := auth.cache.GetIfPresent(hash); ok {
		t.Error("expired key should be evicted from cache")
	}
}

func TestAuthenticate_TouchKeyUsed(t *testing.T) {
	t.Parallel()
	auth, store := newTestAuth(t)

	store.addKey(testKey, &gateway.APIKey{
		ID:        "key-touch",
		KeyPrefix: "gnd_test_key",
		OrgID:     "org-1",
	})

	_, err := auth.Authenticate(context.Background(), makeRequest(testKey))
	if err != nil {
		t.Fatal(err)
	}

	// TouchKeyUsed runs in a goroutine; give it a moment.
	time.Sleep(50 * time.Millisecond)
	if n := store.touchCount("key-touch"); n != 1 {
		t.Errorf("touch count = %d, want 1", n)
	}
}

func TestBuildIdentity(t *testing.T) {
	t.Parallel()

	key := &gateway.APIKey{
		KeyPrefix: "gnd_abcd1234",
		OrgID:     "org-x",
		TeamID:    "team-y",
		UserID:    "user-z",
	}
	id := buildIdentity(key)

	if id.Subject != "gnd_abcd1234" {
		t.Errorf("Subject = %q", id.Subject)
	}
	if id.Role != "member" {
		t.Errorf("Role = %q, want member", id.Role)
	}
	if id.Perms != gateway.RolePermissions["member"] {
		t.Errorf("Perms = %v, want member perms", id.Perms)
	}
	if id.AuthMethod != "apikey" {
		t.Errorf("AuthMethod = %q, want apikey", id.AuthMethod)
	}
}

func TestBuildIdentity_AdminRole(t *testing.T) {
	t.Parallel()

	key := &gateway.APIKey{
		KeyPrefix: "gnd_admin_key",
		OrgID:     "org-x",
		Role:      "admin",
	}
	id := buildIdentity(key)

	if id.Role != "admin" {
		t.Errorf("Role = %q, want admin", id.Role)
	}
	if id.Perms != gateway.RolePermissions["admin"] {
		t.Errorf("Perms = %v, want admin perms", id.Perms)
	}
	if !id.Can(gateway.PermManageProviders) {
		t.Error("admin should have PermManageProviders")
	}
	if !id.Can(gateway.PermManageAllKeys) {
		t.Error("admin should have PermManageAllKeys")
	}
}

func TestBuildIdentity_EmptyRoleDefaultsMember(t *testing.T) {
	t.Parallel()

	key := &gateway.APIKey{
		KeyPrefix: "gnd_empty_role",
		OrgID:     "org-x",
		Role:      "",
	}
	id := buildIdentity(key)

	if id.Role != "member" {
		t.Errorf("Role = %q, want member", id.Role)
	}
}
