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

func TestGetKey(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	key := &gateway.APIKey{
		ID:        "key-get",
		KeyHash:   "hash-get",
		KeyPrefix: "gnd_get1",
		OrgID:     "default",
		Role:      "admin",
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := s.CreateKey(ctx, key); err != nil {
		t.Fatal("create:", err)
	}

	// GetKey by ID (vs GetKeyByHash).
	got, err := s.GetKey(ctx, "key-get")
	if err != nil {
		t.Fatal("GetKey:", err)
	}
	if got.ID != "key-get" {
		t.Errorf("id = %q, want key-get", got.ID)
	}
	if got.Role != "admin" {
		t.Errorf("role = %q, want admin", got.Role)
	}

	// Not found.
	_, err = s.GetKey(ctx, "nonexistent")
	if err != gateway.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestCountKeys(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	// Create org first (FK constraint).
	if err := s.CreateOrg(ctx, &gateway.Organization{
		ID: "org-count", Name: "CountOrg", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	// Initially zero.
	n, err := s.CountKeys(ctx, "org-count")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}

	// Insert two keys.
	for i, id := range []string{"k1", "k2"} {
		if err := s.CreateKey(ctx, &gateway.APIKey{
			ID:        id,
			KeyHash:   "hash-" + id,
			KeyPrefix: "gnd_" + id,
			OrgID:     "org-count",
			CreatedAt: time.Now().UTC().Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal("create:", err)
		}
	}

	n, err = s.CountKeys(ctx, "org-count")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("count = %d, want 2", n)
	}
}

func TestProviderUpdate(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	p := &gateway.ProviderConfig{
		ID: "prov-upd", Name: "openai", Type: "openai",
		BaseURL: "https://api.openai.com/v1", Models: []string{"gpt-4o"},
		Priority: 1, Weight: 1, Enabled: true, TimeoutMs: 30000,
	}
	if err := s.CreateProvider(ctx, p); err != nil {
		t.Fatal("create:", err)
	}

	p.Name = "openai-updated"
	p.Enabled = false
	p.Models = []string{"gpt-4o", "gpt-4o-mini"}
	if err := s.UpdateProvider(ctx, p); err != nil {
		t.Fatal("update:", err)
	}

	got, err := s.GetProvider(ctx, "prov-upd")
	if err != nil {
		t.Fatal("get:", err)
	}
	if got.Name != "openai-updated" {
		t.Errorf("name = %q, want openai-updated", got.Name)
	}
	if got.Enabled {
		t.Error("enabled should be false")
	}
	if len(got.Models) != 2 {
		t.Errorf("models len = %d, want 2", len(got.Models))
	}
}

func TestProviderCountAndRouteCount(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	// Initially zero.
	pc, err := s.CountProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pc != 0 {
		t.Errorf("providers = %d, want 0", pc)
	}
	rc, err := s.CountRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rc != 0 {
		t.Errorf("routes = %d, want 0", rc)
	}

	// Add one of each.
	if err := s.CreateProvider(ctx, &gateway.ProviderConfig{
		ID: "p1", Name: "openai", Type: "openai", BaseURL: "https://api.openai.com/v1",
		Priority: 1, Weight: 1, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRoute(ctx, &gateway.Route{
		ID: "r1", ModelAlias: "gpt-4o", Targets: []byte(`[]`), Strategy: "priority",
	}); err != nil {
		t.Fatal(err)
	}

	pc, _ = s.CountProviders(ctx)
	if pc != 1 {
		t.Errorf("providers = %d, want 1", pc)
	}
	rc, _ = s.CountRoutes(ctx)
	if rc != 1 {
		t.Errorf("routes = %d, want 1", rc)
	}
}

func TestOrgUpdate(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	org := &gateway.Organization{
		ID: "org-upd", Name: "OrigName", CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := s.CreateOrg(ctx, org); err != nil {
		t.Fatal(err)
	}

	rpmLimit := int64(100)
	org.Name = "UpdatedName"
	org.RPMLimit = &rpmLimit
	org.AllowedModels = []string{"gpt-4o"}
	if err := s.UpdateOrg(ctx, org); err != nil {
		t.Fatal("update:", err)
	}

	got, err := s.GetOrg(ctx, "org-upd")
	if err != nil {
		t.Fatal("get:", err)
	}
	if got.Name != "UpdatedName" {
		t.Errorf("name = %q, want UpdatedName", got.Name)
	}
	if got.RPMLimit == nil || *got.RPMLimit != 100 {
		t.Errorf("rpm_limit = %v, want 100", got.RPMLimit)
	}
	if len(got.AllowedModels) != 1 || got.AllowedModels[0] != "gpt-4o" {
		t.Errorf("allowed_models = %v", got.AllowedModels)
	}
}

func TestTeamUpdate(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.CreateOrg(ctx, &gateway.Organization{
		ID: "org-team-upd", Name: "Org", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	team := &gateway.Team{ID: "team-upd", OrgID: "org-team-upd", Name: "OrigTeam"}
	if err := s.CreateTeam(ctx, team); err != nil {
		t.Fatal(err)
	}

	tpmLimit := int64(5000)
	team.Name = "UpdatedTeam"
	team.TPMLimit = &tpmLimit
	if err := s.UpdateTeam(ctx, team); err != nil {
		t.Fatal("update:", err)
	}

	got, err := s.GetTeam(ctx, "team-upd")
	if err != nil {
		t.Fatal("get:", err)
	}
	if got.Name != "UpdatedTeam" {
		t.Errorf("name = %q, want UpdatedTeam", got.Name)
	}
	if got.TPMLimit == nil || *got.TPMLimit != 5000 {
		t.Errorf("tpm_limit = %v, want 5000", got.TPMLimit)
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

func TestUsageQueryAndCount(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	records := []gateway.UsageRecord{
		{ID: "uq-1", KeyID: "k1", OrgID: "org1", Model: "gpt-4o", ProviderID: "p1",
			PromptTokens: 10, TotalTokens: 15, StatusCode: 200, RequestID: "r1",
			CreatedAt: now.Add(-2 * time.Hour)},
		{ID: "uq-2", KeyID: "k2", OrgID: "org1", Model: "gpt-3.5", ProviderID: "p1",
			PromptTokens: 5, TotalTokens: 8, StatusCode: 200, RequestID: "r2",
			CreatedAt: now.Add(-1 * time.Hour)},
		{ID: "uq-3", KeyID: "k1", OrgID: "org2", Model: "gpt-4o", ProviderID: "p1",
			PromptTokens: 20, TotalTokens: 30, StatusCode: 200, RequestID: "r3",
			CreatedAt: now},
	}
	if err := s.InsertUsage(ctx, records); err != nil {
		t.Fatal(err)
	}

	// Filter by OrgID.
	recs, err := s.QueryUsage(ctx, gateway.UsageFilter{OrgID: "org1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Errorf("org1 records = %d, want 2", len(recs))
	}

	// Filter by KeyID.
	recs, err = s.QueryUsage(ctx, gateway.UsageFilter{KeyID: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Errorf("k1 records = %d, want 2", len(recs))
	}

	// Filter by Model.
	recs, err = s.QueryUsage(ctx, gateway.UsageFilter{Model: "gpt-3.5"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Errorf("gpt-3.5 records = %d, want 1", len(recs))
	}

	// Filter by time range.
	since := now.Add(-90 * time.Minute).Format(time.RFC3339)
	recs, err = s.QueryUsage(ctx, gateway.UsageFilter{Since: since})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Errorf("since records = %d, want 2", len(recs))
	}

	// CountUsage.
	n, err := s.CountUsage(ctx, gateway.UsageFilter{OrgID: "org1"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("count = %d, want 2", n)
	}
}

func TestUsageSumCost(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	records := []gateway.UsageRecord{
		{ID: "uc-1", KeyID: "k-cost", OrgID: "org1", Model: "gpt-4o", ProviderID: "p1",
			CostUSD: 0.05, StatusCode: 200, RequestID: "r1", CreatedAt: time.Now().UTC()},
		{ID: "uc-2", KeyID: "k-cost", OrgID: "org1", Model: "gpt-4o", ProviderID: "p1",
			CostUSD: 0.10, StatusCode: 200, RequestID: "r2", CreatedAt: time.Now().UTC()},
		{ID: "uc-3", KeyID: "k-other", OrgID: "org1", Model: "gpt-4o", ProviderID: "p1",
			CostUSD: 1.00, StatusCode: 200, RequestID: "r3", CreatedAt: time.Now().UTC()},
	}
	if err := s.InsertUsage(ctx, records); err != nil {
		t.Fatal(err)
	}

	total, err := s.SumUsageCost(ctx, "k-cost")
	if err != nil {
		t.Fatal(err)
	}
	// Allow floating-point tolerance.
	if total < 0.14 || total > 0.16 {
		t.Errorf("sum cost = %f, want ~0.15", total)
	}
}

func TestUsageRollupUpsert(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	rollups := []gateway.UsageRollup{
		{OrgID: "org1", KeyID: "k1", Model: "gpt-4o", Period: "hourly",
			Bucket: "2024-01-01T00:00:00Z", RequestCount: 10, TotalTokens: 100, CostUSD: 0.50},
	}
	if err := s.UpsertRollup(ctx, rollups); err != nil {
		t.Fatal("first upsert:", err)
	}

	// Upsert again -- should accumulate.
	rollups[0].RequestCount = 5
	rollups[0].TotalTokens = 50
	rollups[0].CostUSD = 0.25
	if err := s.UpsertRollup(ctx, rollups); err != nil {
		t.Fatal("second upsert:", err)
	}

	got, err := s.QueryRollups(ctx, gateway.RollupFilter{OrgID: "org1", Period: "hourly"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("rollups = %d, want 1", len(got))
	}
	if got[0].RequestCount != 15 {
		t.Errorf("request_count = %d, want 15", got[0].RequestCount)
	}
	if got[0].TotalTokens != 150 {
		t.Errorf("total_tokens = %d, want 150", got[0].TotalTokens)
	}
	if got[0].CostUSD < 0.74 || got[0].CostUSD > 0.76 {
		t.Errorf("cost = %f, want ~0.75", got[0].CostUSD)
	}

	// Query with filters that don't match.
	got, err = s.QueryRollups(ctx, gateway.RollupFilter{OrgID: "nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("nonexistent rollups = %d, want 0", len(got))
	}
}
