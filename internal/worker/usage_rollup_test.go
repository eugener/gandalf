package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	gateway "github.com/eugener/gandalf/internal"
)

type fakeRollupStore struct {
	mu      sync.RWMutex
	records []gateway.UsageRecord
	rollups []gateway.UsageRollup
}

func (s *fakeRollupStore) QueryUsage(_ context.Context, f gateway.UsageFilter) ([]gateway.UsageRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []gateway.UsageRecord
	for _, r := range s.records {
		ts := r.CreatedAt.UTC().Format(time.RFC3339)
		if f.Since != "" && ts < f.Since {
			continue
		}
		if f.Until != "" && ts >= f.Until {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *fakeRollupStore) UpsertRollup(_ context.Context, rollups []gateway.UsageRollup) error {
	s.mu.Lock()
	s.rollups = append(s.rollups, rollups...)
	s.mu.Unlock()
	return nil
}

func TestUsageRollupWorker(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Hour)
	store := &fakeRollupStore{
		records: []gateway.UsageRecord{
			{
				ID: "u1", KeyID: "k1", OrgID: "org1", Model: "gpt-4o",
				PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15,
				CostUSD: 0.01, CreatedAt: now.Add(-30 * time.Minute),
			},
			{
				ID: "u2", KeyID: "k1", OrgID: "org1", Model: "gpt-4o",
				PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30,
				CostUSD: 0.02, Cached: true, CreatedAt: now.Add(-20 * time.Minute),
			},
			{
				ID: "u3", KeyID: "k2", OrgID: "org1", Model: "gpt-4o-mini",
				PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8,
				CostUSD: 0.005, CreatedAt: now.Add(-10 * time.Minute),
			},
		},
	}

	w := NewUsageRollupWorker(store)
	w.rollup(context.Background())

	store.mu.RLock()
	defer store.mu.RUnlock()

	if len(store.rollups) == 0 {
		t.Fatal("expected rollups to be created")
	}

	// Should have 2 rollup entries: (org1, k1, gpt-4o) and (org1, k2, gpt-4o-mini)
	if len(store.rollups) != 2 {
		t.Fatalf("expected 2 rollups, got %d", len(store.rollups))
	}

	// Find the k1/gpt-4o rollup.
	var k1Rollup *gateway.UsageRollup
	for i := range store.rollups {
		if store.rollups[i].KeyID == "k1" {
			k1Rollup = &store.rollups[i]
			break
		}
	}
	if k1Rollup == nil {
		t.Fatal("k1 rollup not found")
	}
	if k1Rollup.RequestCount != 2 {
		t.Errorf("request_count = %d, want 2", k1Rollup.RequestCount)
	}
	if k1Rollup.TotalTokens != 45 {
		t.Errorf("total_tokens = %d, want 45", k1Rollup.TotalTokens)
	}
	if k1Rollup.CachedCount != 1 {
		t.Errorf("cached_count = %d, want 1", k1Rollup.CachedCount)
	}
	if k1Rollup.Period != "hourly" {
		t.Errorf("period = %q, want hourly", k1Rollup.Period)
	}
}

func TestUsageRollupWorker_RunCancelledContext(t *testing.T) {
	t.Parallel()

	store := &fakeRollupStore{}
	w := NewUsageRollupWorker(store)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := w.Run(ctx)
	if err != nil {
		t.Errorf("Run should return nil on cancelled context, got %v", err)
	}
}
