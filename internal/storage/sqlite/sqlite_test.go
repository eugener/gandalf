package sqlite

import (
	"context"
	"testing"
	"time"

	gateway "github.com/eugener/gandalf/internal"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	// Use a unique file-based temp DB for each test to avoid shared :memory: races
	path := t.TempDir() + "/test.db"
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAPIKeyRoundTrip(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	key := &gateway.APIKey{
		ID:        "key-1",
		KeyHash:   "abc123hash",
		KeyPrefix: "gnd_abc1",
		OrgID:     "default",
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	}

	if err := s.CreateKey(ctx, key); err != nil {
		t.Fatal("create:", err)
	}

	got, err := s.GetKeyByHash(ctx, "abc123hash")
	if err != nil {
		t.Fatal("get:", err)
	}
	if got.ID != key.ID {
		t.Errorf("id = %q, want %q", got.ID, key.ID)
	}
	if got.KeyPrefix != key.KeyPrefix {
		t.Errorf("prefix = %q, want %q", got.KeyPrefix, key.KeyPrefix)
	}
	if got.OrgID != key.OrgID {
		t.Errorf("org = %q, want %q", got.OrgID, key.OrgID)
	}

	// List
	keys, err := s.ListKeys(ctx, "default", 0, 10)
	if err != nil {
		t.Fatal("list:", err)
	}
	if len(keys) != 1 {
		t.Fatalf("list count = %d, want 1", len(keys))
	}

	// Update
	key.Blocked = true
	if err := s.UpdateKey(ctx, key); err != nil {
		t.Fatal("update:", err)
	}
	got, _ = s.GetKeyByHash(ctx, "abc123hash")
	if !got.Blocked {
		t.Error("blocked should be true after update")
	}

	// TouchUsed
	if err := s.TouchKeyUsed(ctx, "key-1"); err != nil {
		t.Fatal("touch:", err)
	}
	got, _ = s.GetKeyByHash(ctx, "abc123hash")
	if got.LastUsedAt == nil {
		t.Error("last_used_at should be set after touch")
	}

	// Delete
	if err := s.DeleteKey(ctx, "key-1"); err != nil {
		t.Fatal("delete:", err)
	}
	_, err = s.GetKeyByHash(ctx, "abc123hash")
	if err != gateway.ErrNotFound {
		t.Errorf("after delete err = %v, want ErrNotFound", err)
	}
}

func TestProviderRoundTrip(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	p := &gateway.ProviderConfig{
		ID:        "prov-1",
		Name:      "openai",
		Type:      "openai",
		BaseURL:   "https://api.openai.com/v1",
		APIKeyEnc: "enc-key",
		Models:    []string{"gpt-4o"},
		Priority:  1,
		Weight:    1,
		Enabled:   true,
		TimeoutMs: 30000,
	}

	if err := s.CreateProvider(ctx, p); err != nil {
		t.Fatal("create:", err)
	}

	got, err := s.GetProvider(ctx, "prov-1")
	if err != nil {
		t.Fatal("get:", err)
	}
	if got.Name != "openai" {
		t.Errorf("name = %q, want %q", got.Name, "openai")
	}
	if !got.Enabled {
		t.Error("enabled should be true")
	}

	providers, err := s.ListProviders(ctx)
	if err != nil {
		t.Fatal("list:", err)
	}
	if len(providers) != 1 {
		t.Fatalf("list count = %d, want 1", len(providers))
	}

	if err := s.DeleteProvider(ctx, "prov-1"); err != nil {
		t.Fatal("delete:", err)
	}
}

func TestRouteRoundTrip(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	r := &gateway.Route{
		ID:         "route-1",
		ModelAlias: "gpt-4o",
		Targets:    []byte(`[{"provider_id":"prov-1","model":"gpt-4o","priority":1}]`),
		Strategy:   "priority",
		CacheTTLs:  0,
	}

	if err := s.CreateRoute(ctx, r); err != nil {
		t.Fatal("create:", err)
	}

	got, err := s.GetRouteByAlias(ctx, "gpt-4o")
	if err != nil {
		t.Fatal("get:", err)
	}
	if got.Strategy != "priority" {
		t.Errorf("strategy = %q, want %q", got.Strategy, "priority")
	}

	routes, err := s.ListRoutes(ctx)
	if err != nil {
		t.Fatal("list:", err)
	}
	if len(routes) != 1 {
		t.Fatalf("list count = %d, want 1", len(routes))
	}

	if err := s.DeleteRoute(ctx, "route-1"); err != nil {
		t.Fatal("delete:", err)
	}
}

func TestOrgAndTeamRoundTrip(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	org := &gateway.Organization{
		ID:        "org-1",
		Name:      "Acme",
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	}

	if err := s.CreateOrg(ctx, org); err != nil {
		t.Fatal("create org:", err)
	}

	got, err := s.GetOrg(ctx, "org-1")
	if err != nil {
		t.Fatal("get org:", err)
	}
	if got.Name != "Acme" {
		t.Errorf("org name = %q, want %q", got.Name, "Acme")
	}

	team := &gateway.Team{
		ID:    "team-1",
		OrgID: "org-1",
		Name:  "Backend",
	}
	if err := s.CreateTeam(ctx, team); err != nil {
		t.Fatal("create team:", err)
	}

	teams, err := s.ListTeams(ctx, "org-1", 0, 10)
	if err != nil {
		t.Fatal("list teams:", err)
	}
	if len(teams) != 1 {
		t.Fatalf("teams count = %d, want 1", len(teams))
	}

	if err := s.DeleteTeam(ctx, "team-1"); err != nil {
		t.Fatal("delete team:", err)
	}
	if err := s.DeleteOrg(ctx, "org-1"); err != nil {
		t.Fatal("delete org:", err)
	}
}

func TestUsageBatchInsert(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	records := []gateway.UsageRecord{
		{
			ID:               "u-1",
			KeyID:            "key-1",
			OrgID:            "default",
			Model:            "gpt-4o",
			ProviderID:       "prov-1",
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
			StatusCode:       200,
			RequestID:        "req-1",
			CreatedAt:        time.Now().UTC(),
		},
		{
			ID:               "u-2",
			KeyID:            "key-1",
			OrgID:            "default",
			Model:            "gpt-4o",
			ProviderID:       "prov-1",
			PromptTokens:     20,
			CompletionTokens: 10,
			TotalTokens:      30,
			StatusCode:       200,
			RequestID:        "req-2",
			CreatedAt:        time.Now().UTC(),
		},
	}

	if err := s.InsertUsage(ctx, records); err != nil {
		t.Fatal("insert usage:", err)
	}

	// Verify by counting
	var count int
	err := s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_records`).Scan(&count)
	if err != nil {
		t.Fatal("count:", err)
	}
	if count != 2 {
		t.Errorf("usage count = %d, want 2", count)
	}
}
