package testutil

import (
	"context"
	"sync"

	gateway "github.com/eugener/gandalf/internal"
)

// FakeStore is an in-memory implementation of storage.Store for testing.
type FakeStore struct {
	mu     sync.RWMutex
	routes map[string]*gateway.Route
}

// NewFakeStore returns a FakeStore with empty collections.
func NewFakeStore() *FakeStore {
	return &FakeStore{routes: make(map[string]*gateway.Route)}
}

// AddRoute inserts a route into the fake store.
func (s *FakeStore) AddRoute(r *gateway.Route) {
	s.mu.Lock()
	s.routes[r.ModelAlias] = r
	s.mu.Unlock()
}

// --- RouteStore ---

// CreateRoute stores a route.
func (s *FakeStore) CreateRoute(_ context.Context, r *gateway.Route) error {
	s.AddRoute(r)
	return nil
}

// GetRouteByAlias looks up a route by model alias.
func (s *FakeStore) GetRouteByAlias(_ context.Context, alias string) (*gateway.Route, error) {
	s.mu.RLock()
	r, ok := s.routes[alias]
	s.mu.RUnlock()
	if !ok {
		return nil, gateway.ErrNotFound
	}
	return r, nil
}

// ListRoutes returns all stored routes.
func (s *FakeStore) ListRoutes(context.Context) ([]*gateway.Route, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*gateway.Route, 0, len(s.routes))
	for _, r := range s.routes {
		out = append(out, r)
	}
	return out, nil
}

// UpdateRoute updates a stored route.
func (s *FakeStore) UpdateRoute(_ context.Context, r *gateway.Route) error {
	s.mu.Lock()
	s.routes[r.ModelAlias] = r
	s.mu.Unlock()
	return nil
}

// DeleteRoute removes a route by ID.
func (s *FakeStore) DeleteRoute(_ context.Context, id string) error {
	s.mu.Lock()
	for alias, r := range s.routes {
		if r.ID == id {
			delete(s.routes, alias)
			break
		}
	}
	s.mu.Unlock()
	return nil
}

// --- Stubs for other Store interfaces ---

func (s *FakeStore) CreateKey(context.Context, *gateway.APIKey) error                         { return nil }
func (s *FakeStore) GetKeyByHash(context.Context, string) (*gateway.APIKey, error)            { return nil, gateway.ErrNotFound }
func (s *FakeStore) ListKeys(context.Context, string, int, int) ([]*gateway.APIKey, error)    { return nil, nil }
func (s *FakeStore) UpdateKey(context.Context, *gateway.APIKey) error                         { return nil }
func (s *FakeStore) DeleteKey(context.Context, string) error                                  { return nil }
func (s *FakeStore) TouchKeyUsed(context.Context, string) error                               { return nil }
func (s *FakeStore) CreateProvider(context.Context, *gateway.ProviderConfig) error            { return nil }
func (s *FakeStore) GetProvider(context.Context, string) (*gateway.ProviderConfig, error)     { return nil, gateway.ErrNotFound }
func (s *FakeStore) ListProviders(context.Context) ([]*gateway.ProviderConfig, error)         { return nil, nil }
func (s *FakeStore) UpdateProvider(context.Context, *gateway.ProviderConfig) error            { return nil }
func (s *FakeStore) DeleteProvider(context.Context, string) error                             { return nil }
func (s *FakeStore) InsertUsage(context.Context, []gateway.UsageRecord) error                 { return nil }
func (s *FakeStore) CreateOrg(context.Context, *gateway.Organization) error                   { return nil }
func (s *FakeStore) GetOrg(context.Context, string) (*gateway.Organization, error)            { return nil, gateway.ErrNotFound }
func (s *FakeStore) ListOrgs(context.Context, int, int) ([]*gateway.Organization, error)      { return nil, nil }
func (s *FakeStore) UpdateOrg(context.Context, *gateway.Organization) error                   { return nil }
func (s *FakeStore) DeleteOrg(context.Context, string) error                                  { return nil }
func (s *FakeStore) CreateTeam(context.Context, *gateway.Team) error                          { return nil }
func (s *FakeStore) GetTeam(context.Context, string) (*gateway.Team, error)                   { return nil, gateway.ErrNotFound }
func (s *FakeStore) ListTeams(context.Context, string, int, int) ([]*gateway.Team, error)     { return nil, nil }
func (s *FakeStore) UpdateTeam(context.Context, *gateway.Team) error                          { return nil }
func (s *FakeStore) DeleteTeam(context.Context, string) error                                 { return nil }
func (s *FakeStore) Close() error                                                             { return nil }
