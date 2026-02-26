package worker

import (
	"context"
	"testing"
	"time"

	"github.com/eugener/gandalf/internal/ratelimit"
)

type fakeQuotaStore struct {
	costs map[string]float64
}

func (s *fakeQuotaStore) SumUsageCost(_ context.Context, keyID string) (float64, error) {
	return s.costs[keyID], nil
}

type fakeBudgetStore struct {
	budgets map[string]float64
}

func (s *fakeBudgetStore) ListBudgetedKeyIDs(_ context.Context) (map[string]float64, error) {
	return s.budgets, nil
}

func TestQuotaSyncWorker_Run(t *testing.T) {
	t.Parallel()
	tracker := ratelimit.NewQuotaTracker()
	store := &fakeQuotaStore{costs: map[string]float64{"k1": 5.0}}

	// Pre-populate tracker with an entry.
	tracker.Check("k1", 10.0)

	w := NewQuotaSyncWorker(tracker, store)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	// Wait briefly, then cancel.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestQuotaSyncWorker_PreloadBudgets(t *testing.T) {
	t.Parallel()
	tracker := ratelimit.NewQuotaTracker()
	quotaStore := &fakeQuotaStore{costs: map[string]float64{"budgeted-key": 8.0}}
	budgetStore := &fakeBudgetStore{budgets: map[string]float64{"budgeted-key": 10.0}}

	w := NewQuotaSyncWorkerWithBudgets(tracker, quotaStore, budgetStore)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	// Wait for initial sync.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}

	// After preload + sync, key should be within budget (8/10).
	if !tracker.Check("budgeted-key", 10.0) {
		t.Error("preloaded+synced key at 8/10 should be within budget")
	}

	// Consume additional cost to push over budget.
	tracker.Consume("budgeted-key", 3.0)
	if tracker.Check("budgeted-key", 10.0) {
		t.Error("preloaded+synced key at 11/10 should be over budget")
	}
}
