package config

import (
	"context"
	"testing"

	"github.com/eugener/gandalf/internal/storage/sqlite"
)

func newTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	path := t.TempDir() + "/test.db"
	s, err := sqlite.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestBootstrap(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()

	cfg := &Config{
		Providers: []ProviderEntry{
			{
				Name:      "openai",
				BaseURL:   "https://api.openai.com/v1",
				APIKey:    "sk-test",
				Models:    []string{"gpt-4o"},
				Priority:  1,
				Weight:    1,
				TimeoutMs: 30000,
			},
		},
		Routes: []RouteEntry{
			{
				ModelAlias: "gpt-4o",
				Targets:    []TargetEntry{{Provider: "openai", Model: "gpt-4o", Priority: 1}},
				Strategy:   "priority",
			},
		},
		Keys: []KeyEntry{
			{
				Name:  "test-key",
				Key:   "gnd_testkey123456",
				OrgID: "default",
				Role:  "admin",
			},
		},
	}

	// First call seeds everything.
	if err := Bootstrap(ctx, cfg, store); err != nil {
		t.Fatal("bootstrap:", err)
	}

	// Verify provider seeded.
	prov, err := store.GetProvider(ctx, "openai")
	if err != nil {
		t.Fatal("get provider:", err)
	}
	if prov.Name != "openai" {
		t.Errorf("provider name = %q, want %q", prov.Name, "openai")
	}

	// Verify route seeded.
	route, err := store.GetRouteByAlias(ctx, "gpt-4o")
	if err != nil {
		t.Fatal("get route:", err)
	}
	if route.Strategy != "priority" {
		t.Errorf("route strategy = %q, want %q", route.Strategy, "priority")
	}

	// Second call is idempotent -- no errors, no duplicates.
	if err := Bootstrap(ctx, cfg, store); err != nil {
		t.Fatal("idempotent bootstrap:", err)
	}

	providers, err := store.ListProviders(ctx)
	if err != nil {
		t.Fatal("list providers:", err)
	}
	if len(providers) != 1 {
		t.Errorf("provider count after second bootstrap = %d, want 1", len(providers))
	}

	routes, err := store.ListRoutes(ctx)
	if err != nil {
		t.Fatal("list routes:", err)
	}
	if len(routes) != 1 {
		t.Errorf("route count after second bootstrap = %d, want 1", len(routes))
	}
}

func TestBootstrapSkipsEmptyKeys(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()

	cfg := &Config{
		Keys: []KeyEntry{
			{Name: "empty", Key: "", OrgID: "default"},
		},
	}

	if err := Bootstrap(ctx, cfg, store); err != nil {
		t.Fatal("bootstrap:", err)
	}

	keys, err := store.ListKeys(ctx, "default", 0, 10)
	if err != nil {
		t.Fatal("list keys:", err)
	}
	if len(keys) != 0 {
		t.Errorf("key count = %d, want 0 (empty key should be skipped)", len(keys))
	}
}
