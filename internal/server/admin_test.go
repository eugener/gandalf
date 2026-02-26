package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/app"
	"github.com/eugener/gandalf/internal/provider"
)

// --- Admin-specific auth fakes ---

type adminAuth struct{}

func (adminAuth) Authenticate(_ context.Context, _ *http.Request) (*gateway.Identity, error) {
	return &gateway.Identity{
		Subject:    "admin",
		KeyID:      "key-admin-1",
		OrgID:      "default",
		Role:       "admin",
		Perms:      gateway.RolePermissions["admin"],
		AuthMethod: "apikey",
	}, nil
}

type memberAuth struct{}

func (memberAuth) Authenticate(_ context.Context, _ *http.Request) (*gateway.Identity, error) {
	return &gateway.Identity{
		Subject:    "member",
		KeyID:      "key-member-1",
		OrgID:      "default",
		Role:       "member",
		Perms:      gateway.RolePermissions["member"],
		AuthMethod: "apikey",
	}, nil
}

// --- In-memory admin store ---

type adminFakeStore struct {
	mu        sync.RWMutex
	providers map[string]*gateway.ProviderConfig
	keys      map[string]*gateway.APIKey
	routes    map[string]*gateway.Route
	usage     []gateway.UsageRecord
	rollups   []gateway.UsageRollup
}

func newAdminFakeStore() *adminFakeStore {
	return &adminFakeStore{
		providers: make(map[string]*gateway.ProviderConfig),
		keys:      make(map[string]*gateway.APIKey),
		routes:    make(map[string]*gateway.Route),
	}
}

func (s *adminFakeStore) CreateProvider(_ context.Context, p *gateway.ProviderConfig) error {
	s.mu.Lock()
	s.providers[p.ID] = p
	s.mu.Unlock()
	return nil
}
func (s *adminFakeStore) GetProvider(_ context.Context, id string) (*gateway.ProviderConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.providers[id]
	if !ok {
		return nil, gateway.ErrNotFound
	}
	return p, nil
}
func (s *adminFakeStore) ListProviders(context.Context) ([]*gateway.ProviderConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*gateway.ProviderConfig, 0, len(s.providers))
	for _, p := range s.providers {
		out = append(out, p)
	}
	return out, nil
}
func (s *adminFakeStore) CountProviders(context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.providers), nil
}
func (s *adminFakeStore) UpdateProvider(_ context.Context, p *gateway.ProviderConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.providers[p.ID]; !ok {
		return gateway.ErrNotFound
	}
	s.providers[p.ID] = p
	return nil
}
func (s *adminFakeStore) DeleteProvider(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.providers[id]; !ok {
		return gateway.ErrNotFound
	}
	delete(s.providers, id)
	return nil
}

func (s *adminFakeStore) CreateKey(_ context.Context, k *gateway.APIKey) error {
	s.mu.Lock()
	s.keys[k.ID] = k
	s.mu.Unlock()
	return nil
}
func (s *adminFakeStore) GetKey(_ context.Context, id string) (*gateway.APIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k, ok := s.keys[id]
	if !ok {
		return nil, gateway.ErrNotFound
	}
	return k, nil
}
func (s *adminFakeStore) GetKeyByHash(_ context.Context, hash string) (*gateway.APIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.keys {
		if k.KeyHash == hash {
			return k, nil
		}
	}
	return nil, gateway.ErrNotFound
}
func (s *adminFakeStore) ListKeys(_ context.Context, orgID string, offset, limit int) ([]*gateway.APIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*gateway.APIKey
	for _, k := range s.keys {
		if k.OrgID == orgID {
			out = append(out, k)
		}
	}
	if offset > len(out) {
		return nil, nil
	}
	end := min(offset+limit, len(out))
	return out[offset:end], nil
}
func (s *adminFakeStore) CountKeys(_ context.Context, orgID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, k := range s.keys {
		if k.OrgID == orgID {
			n++
		}
	}
	return n, nil
}
func (s *adminFakeStore) UpdateKey(_ context.Context, k *gateway.APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.keys[k.ID]; !ok {
		return gateway.ErrNotFound
	}
	s.keys[k.ID] = k
	return nil
}
func (s *adminFakeStore) DeleteKey(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.keys[id]; !ok {
		return gateway.ErrNotFound
	}
	delete(s.keys, id)
	return nil
}
func (s *adminFakeStore) TouchKeyUsed(context.Context, string) error                   { return nil }
func (s *adminFakeStore) ListBudgetedKeyIDs(context.Context) (map[string]float64, error) { return nil, nil }

func (s *adminFakeStore) CreateRoute(_ context.Context, r *gateway.Route) error {
	s.mu.Lock()
	s.routes[r.ID] = r
	s.mu.Unlock()
	return nil
}
func (s *adminFakeStore) GetRoute(_ context.Context, id string) (*gateway.Route, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.routes[id]
	if !ok {
		return nil, gateway.ErrNotFound
	}
	return r, nil
}
func (s *adminFakeStore) GetRouteByAlias(_ context.Context, alias string) (*gateway.Route, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.routes {
		if r.ModelAlias == alias {
			return r, nil
		}
	}
	return nil, gateway.ErrNotFound
}
func (s *adminFakeStore) ListRoutes(context.Context) ([]*gateway.Route, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*gateway.Route, 0, len(s.routes))
	for _, r := range s.routes {
		out = append(out, r)
	}
	return out, nil
}
func (s *adminFakeStore) CountRoutes(context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.routes), nil
}
func (s *adminFakeStore) UpdateRoute(_ context.Context, r *gateway.Route) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.routes[r.ID]; !ok {
		return gateway.ErrNotFound
	}
	s.routes[r.ID] = r
	return nil
}
func (s *adminFakeStore) DeleteRoute(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.routes[id]; !ok {
		return gateway.ErrNotFound
	}
	delete(s.routes, id)
	return nil
}

func (s *adminFakeStore) InsertUsage(_ context.Context, records []gateway.UsageRecord) error {
	s.mu.Lock()
	s.usage = append(s.usage, records...)
	s.mu.Unlock()
	return nil
}
func (s *adminFakeStore) SumUsageCost(context.Context, string) (float64, error) { return 0, nil }
func (s *adminFakeStore) QueryUsage(_ context.Context, f gateway.UsageFilter) ([]gateway.UsageRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []gateway.UsageRecord
	for _, r := range s.usage {
		if f.OrgID != "" && r.OrgID != f.OrgID {
			continue
		}
		if f.KeyID != "" && r.KeyID != f.KeyID {
			continue
		}
		if f.Model != "" && r.Model != f.Model {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}
func (s *adminFakeStore) CountUsage(_ context.Context, f gateway.UsageFilter) (int, error) {
	records, _ := s.QueryUsage(context.Background(), f)
	return len(records), nil
}
func (s *adminFakeStore) UpsertRollup(_ context.Context, rollups []gateway.UsageRollup) error {
	s.mu.Lock()
	s.rollups = append(s.rollups, rollups...)
	s.mu.Unlock()
	return nil
}
func (s *adminFakeStore) QueryRollups(context.Context, gateway.RollupFilter) ([]gateway.UsageRollup, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rollups, nil
}

func (s *adminFakeStore) CreateOrg(context.Context, *gateway.Organization) error { return nil }
func (s *adminFakeStore) GetOrg(context.Context, string) (*gateway.Organization, error) {
	return nil, gateway.ErrNotFound
}
func (s *adminFakeStore) ListOrgs(context.Context, int, int) ([]*gateway.Organization, error) {
	return nil, nil
}
func (s *adminFakeStore) UpdateOrg(context.Context, *gateway.Organization) error { return nil }
func (s *adminFakeStore) DeleteOrg(context.Context, string) error                { return nil }
func (s *adminFakeStore) CreateTeam(context.Context, *gateway.Team) error        { return nil }
func (s *adminFakeStore) GetTeam(context.Context, string) (*gateway.Team, error) {
	return nil, gateway.ErrNotFound
}
func (s *adminFakeStore) ListTeams(context.Context, string, int, int) ([]*gateway.Team, error) {
	return nil, nil
}
func (s *adminFakeStore) UpdateTeam(context.Context, *gateway.Team) error { return nil }
func (s *adminFakeStore) DeleteTeam(context.Context, string) error        { return nil }
func (s *adminFakeStore) Close() error                                    { return nil }

// --- Helpers ---

func newAdminTestHandler(authProvider gateway.Authenticator) (http.Handler, *adminFakeStore) {
	store := newAdminFakeStore()
	reg := provider.NewRegistry()
	reg.Register("fake", fakeProvider{})
	routerSvc := app.NewRouterService(store)
	return New(Deps{
		Auth:      authProvider,
		Proxy:     app.NewProxyService(reg, routerSvc, nil, nil),
		Providers: reg,
		Router:    routerSvc,
		Keys:      app.NewKeyManager(store),
		Store:     store,
	}), store
}

// --- Tests ---

func TestAdminProviderCRUD(t *testing.T) {
	t.Parallel()
	h, _ := newAdminTestHandler(adminAuth{})

	// Create
	body := `{"name":"openai","base_url":"https://api.openai.com/v1","models":["gpt-4o"],"enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/providers", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Location") == "" {
		t.Error("Location header should be set on create")
	}

	// List
	req = httptest.NewRequest(http.MethodGet, "/admin/v1/providers", nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "openai") {
		t.Error("list should contain created provider")
	}

	// Get
	req = httptest.NewRequest(http.MethodGet, "/admin/v1/providers/openai", nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	// Update
	body = `{"name":"openai","base_url":"https://api.openai.com/v2","enabled":true}`
	req = httptest.NewRequest(http.MethodPut, "/admin/v1/providers/openai", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	// Delete
	req = httptest.NewRequest(http.MethodDelete, "/admin/v1/providers/openai", nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	// Get after delete -> 404
	req = httptest.NewRequest(http.MethodGet, "/admin/v1/providers/openai", nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("get deleted: status = %d, want 404", rec.Code)
	}
}

func TestAdminKeyCRUD(t *testing.T) {
	t.Parallel()
	h, _ := newAdminTestHandler(adminAuth{})

	// Create
	body := `{"org_id":"default","role":"member"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/keys", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}

	var created struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	json.NewDecoder(rec.Body).Decode(&created)
	if created.Key == "" {
		t.Error("plaintext key should be returned on create")
	}
	if !strings.HasPrefix(created.Key, "gnd_") {
		t.Error("key should have gnd_ prefix")
	}

	// Get
	req = httptest.NewRequest(http.MethodGet, "/admin/v1/keys/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	// Update - block the key
	body = `{"blocked":true}`
	req = httptest.NewRequest(http.MethodPut, "/admin/v1/keys/"+created.ID, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"blocked":true`) {
		t.Error("key should be blocked after update")
	}

	// List
	req = httptest.NewRequest(http.MethodGet, "/admin/v1/keys?org_id=default", nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	// Delete
	req = httptest.NewRequest(http.MethodDelete, "/admin/v1/keys/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d; body = %s", rec.Code, rec.Body.String())
	}
}

func TestAdminRouteCRUD(t *testing.T) {
	t.Parallel()
	h, _ := newAdminTestHandler(adminAuth{})

	// Create
	body := `{"model_alias":"gpt-4o","targets":[{"provider_id":"fake","model":"gpt-4o","priority":1}]}`
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/routes", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}

	var created struct {
		ID string `json:"id"`
	}
	json.NewDecoder(rec.Body).Decode(&created)

	// Get
	req = httptest.NewRequest(http.MethodGet, "/admin/v1/routes/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	// List
	req = httptest.NewRequest(http.MethodGet, "/admin/v1/routes", nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	// Delete
	req = httptest.NewRequest(http.MethodDelete, "/admin/v1/routes/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d; body = %s", rec.Code, rec.Body.String())
	}
}

func TestAdminCachePurge(t *testing.T) {
	t.Parallel()
	h, _ := newAdminTestHandler(adminAuth{})

	req := httptest.NewRequest(http.MethodPost, "/admin/v1/cache/purge", nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("cache purge: status = %d, want 204", rec.Code)
	}
}

func TestAdminRBACEnforcement_MemberDenied(t *testing.T) {
	t.Parallel()
	h, _ := newAdminTestHandler(memberAuth{})

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/admin/v1/providers"},
		{http.MethodPost, "/admin/v1/providers"},
		{http.MethodGet, "/admin/v1/keys"},
		{http.MethodPost, "/admin/v1/keys"},
		{http.MethodGet, "/admin/v1/routes"},
		{http.MethodPost, "/admin/v1/routes"},
		{http.MethodPost, "/admin/v1/cache/purge"},
		{http.MethodGet, "/admin/v1/usage"},
		{http.MethodGet, "/admin/v1/usage/summary"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			t.Parallel()
			var body *strings.Reader
			if ep.method == http.MethodPost {
				body = strings.NewReader("{}")
			} else {
				body = strings.NewReader("")
			}
			req := httptest.NewRequest(ep.method, ep.path, body)
			req.Header.Set("Authorization", "Bearer gnd_member")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403 for %s %s", rec.Code, ep.method, ep.path)
			}
		})
	}
}

func TestAdminProviderNotFound(t *testing.T) {
	t.Parallel()
	h, _ := newAdminTestHandler(adminAuth{})

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/providers/nonexistent", nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestAdminKeyNotFound(t *testing.T) {
	t.Parallel()
	h, _ := newAdminTestHandler(adminAuth{})

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/keys/nonexistent", nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestAdminRouteNotFound(t *testing.T) {
	t.Parallel()
	h, _ := newAdminTestHandler(adminAuth{})

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/routes/nonexistent", nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestAdminRouteUpdate(t *testing.T) {
	t.Parallel()
	h, store := newAdminTestHandler(adminAuth{})

	// Create route first.
	store.mu.Lock()
	store.routes["route-1"] = &gateway.Route{
		ID: "route-1", ModelAlias: "gpt-4o",
		Targets: []byte(`[{"provider_id":"fake","model":"gpt-4o","priority":1}]`),
		Strategy: "priority",
	}
	store.mu.Unlock()

	// Update success.
	body := `{"model_alias":"gpt-4o-updated","strategy":"weighted"}`
	req := httptest.NewRequest(http.MethodPut, "/admin/v1/routes/route-1", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "gpt-4o-updated") {
		t.Error("response should contain updated alias")
	}

	// Update not found.
	req = httptest.NewRequest(http.MethodPut, "/admin/v1/routes/nonexistent", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("update not found: status = %d, want 404", rec.Code)
	}
}

func TestAdminQueryUsage(t *testing.T) {
	t.Parallel()
	h, store := newAdminTestHandler(adminAuth{})

	// Insert test usage records (admin identity has OrgID "default").
	store.mu.Lock()
	store.usage = []gateway.UsageRecord{
		{ID: "u1", KeyID: "k1", OrgID: "default", Model: "gpt-4o", PromptTokens: 10},
		{ID: "u2", KeyID: "k2", OrgID: "default", Model: "gpt-3.5", PromptTokens: 5},
	}
	store.mu.Unlock()

	// Query own org (default to caller's org).
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/usage", nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("usage query: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "u1") {
		t.Error("response should contain u1")
	}

	// Query cross-org -> 403.
	req = httptest.NewRequest(http.MethodGet, "/admin/v1/usage?org_id=other-org", nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-org usage query: status = %d, want 403", rec.Code)
	}
}

func TestAdminUsageSummary(t *testing.T) {
	t.Parallel()
	h, store := newAdminTestHandler(adminAuth{})

	// Insert test rollups (admin identity has OrgID "default").
	store.mu.Lock()
	store.rollups = []gateway.UsageRollup{
		{OrgID: "default", KeyID: "k1", Model: "gpt-4o", Period: "hourly",
			Bucket: "2024-01-01T00:00:00Z", RequestCount: 10},
	}
	store.mu.Unlock()

	// Query own org.
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/usage/summary?period=hourly", nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("summary: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "gpt-4o") {
		t.Error("response should contain rollup data")
	}

	// Cross-org summary -> 403.
	req = httptest.NewRequest(http.MethodGet, "/admin/v1/usage/summary?org_id=other-org", nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-org summary: status = %d, want 403", rec.Code)
	}
}

func TestAdminCreateKey_InvalidExpiry(t *testing.T) {
	t.Parallel()
	h, _ := newAdminTestHandler(adminAuth{})

	body := `{"org_id":"default","expires_at":"not-a-date"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/keys", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestAdminUpdateKey_InvalidExpiry(t *testing.T) {
	t.Parallel()
	h, store := newAdminTestHandler(adminAuth{})

	// Seed a key.
	store.mu.Lock()
	store.keys["key-exp"] = &gateway.APIKey{
		ID: "key-exp", OrgID: "default", Role: "member",
	}
	store.mu.Unlock()

	body := `{"expires_at":"bad-format"}`
	req := httptest.NewRequest(http.MethodPut, "/admin/v1/keys/key-exp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestAdminConflictError(t *testing.T) {
	t.Parallel()

	// Use a store that returns ErrConflict on CreateProvider.
	store := newAdminFakeStore()
	store.providers["dup"] = &gateway.ProviderConfig{ID: "dup", Name: "dup"}

	// Create a conflicting adminFakeStore that returns conflict.
	conflictStore := &conflictOnCreateStore{adminFakeStore: store}

	reg := provider.NewRegistry()
	reg.Register("fake", fakeProvider{})
	routerSvc := app.NewRouterService(store)
	h := New(Deps{
		Auth:      adminAuth{},
		Proxy:     app.NewProxyService(reg, routerSvc, nil, nil),
		Providers: reg,
		Router:    routerSvc,
		Keys:      app.NewKeyManager(conflictStore),
		Store:     conflictStore,
	})

	body := `{"name":"dup","base_url":"https://example.com","enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/providers", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

// conflictOnCreateStore wraps adminFakeStore and returns ErrConflict on CreateProvider.
type conflictOnCreateStore struct {
	*adminFakeStore
}

func (s *conflictOnCreateStore) CreateProvider(_ context.Context, _ *gateway.ProviderConfig) error {
	return gateway.ErrConflict
}

func TestAdminCrossOrgKeyAccess(t *testing.T) {
	t.Parallel()
	h, store := newAdminTestHandler(adminAuth{})

	// Seed a key in a different org.
	store.mu.Lock()
	store.keys["cross-org-key"] = &gateway.APIKey{
		ID: "cross-org-key", OrgID: "other-org", Role: "member",
	}
	store.mu.Unlock()

	// GET cross-org key -> 404.
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/keys/cross-org-key", nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("get cross-org key: status = %d, want 404", rec.Code)
	}

	// PUT cross-org key -> 404.
	body := `{"blocked":true}`
	req = httptest.NewRequest(http.MethodPut, "/admin/v1/keys/cross-org-key", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("update cross-org key: status = %d, want 404", rec.Code)
	}

	// DELETE cross-org key -> 404.
	req = httptest.NewRequest(http.MethodDelete, "/admin/v1/keys/cross-org-key", nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("delete cross-org key: status = %d, want 404", rec.Code)
	}

	// LIST with cross-org filter -> 403.
	req = httptest.NewRequest(http.MethodGet, "/admin/v1/keys?org_id=other-org", nil)
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("list cross-org keys: status = %d, want 403", rec.Code)
	}

	// CREATE with cross-org -> 403.
	body = `{"org_id":"other-org","role":"member"}`
	req = httptest.NewRequest(http.MethodPost, "/admin/v1/keys", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer gnd_admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("create cross-org key: status = %d, want 403", rec.Code)
	}
}
