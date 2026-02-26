package ratelimit

import (
	"context"
	"testing"
)

type fakeQuotaStore struct {
	costs map[string]float64
}

func (s *fakeQuotaStore) SumUsageCost(_ context.Context, keyID string) (float64, error) {
	return s.costs[keyID], nil
}

func TestQuotaTracker_WithinBudget(t *testing.T) {
	t.Parallel()
	q := NewQuotaTracker()

	if !q.Check("key1", 10.0) {
		t.Error("new key should be within budget")
	}
}

func TestQuotaTracker_OverBudget(t *testing.T) {
	t.Parallel()
	q := NewQuotaTracker()

	q.Consume("key1", 10.0)

	if q.Check("key1", 10.0) {
		t.Error("key at limit should be over budget")
	}
}

func TestQuotaTracker_Consume(t *testing.T) {
	t.Parallel()
	q := NewQuotaTracker()

	q.Consume("key1", 3.0)
	q.Consume("key1", 4.0)

	if !q.Check("key1", 10.0) {
		t.Error("key at 7/10 should be within budget")
	}

	q.Consume("key1", 4.0)

	if q.Check("key1", 10.0) {
		t.Error("key at 11/10 should be over budget")
	}
}

func TestQuotaTracker_UnlimitedBudget(t *testing.T) {
	t.Parallel()
	q := NewQuotaTracker()

	q.Consume("key1", 1000000)

	if !q.Check("key1", 0) {
		t.Error("unlimited budget (0) should always pass")
	}
}

func TestQuotaTracker_Sync(t *testing.T) {
	t.Parallel()
	q := NewQuotaTracker()
	store := &fakeQuotaStore{costs: map[string]float64{"key1": 8.5}}

	// First check creates the entry.
	q.Check("key1", 10.0)
	// Sync reloads from DB.
	if err := q.Sync(context.Background(), store, "key1"); err != nil {
		t.Fatal(err)
	}

	if !q.Check("key1", 10.0) {
		t.Error("key at 8.5/10 should be within budget")
	}

	store.costs["key1"] = 11.0
	if err := q.Sync(context.Background(), store, "key1"); err != nil {
		t.Fatal(err)
	}

	if q.Check("key1", 10.0) {
		t.Error("key at 11/10 should be over budget")
	}
}

func TestQuotaTracker_SyncAll(t *testing.T) {
	t.Parallel()
	q := NewQuotaTracker()
	store := &fakeQuotaStore{costs: map[string]float64{"k1": 5.0, "k2": 15.0}}

	q.Check("k1", 10.0) // create entries
	q.Check("k2", 10.0)

	if err := q.SyncAll(context.Background(), store); err != nil {
		t.Fatal(err)
	}

	if !q.Check("k1", 10.0) {
		t.Error("k1 at 5/10 should be within budget")
	}
	if q.Check("k2", 10.0) {
		t.Error("k2 at 15/10 should be over budget")
	}
}

func TestQuotaTracker_SyncNewKey(t *testing.T) {
	t.Parallel()
	q := NewQuotaTracker()
	store := &fakeQuotaStore{costs: map[string]float64{"new": 3.0}}

	// Sync a key that hasn't been checked yet.
	if err := q.Sync(context.Background(), store, "new"); err != nil {
		t.Fatal(err)
	}

	if !q.Check("new", 5.0) {
		t.Error("key at 3/5 should be within budget")
	}
}

func TestQuotaTracker_Preload(t *testing.T) {
	t.Parallel()
	q := NewQuotaTracker()

	// Preload seeds the entry so SyncAll will include it.
	q.Preload("preloaded", 10.0)

	store := &fakeQuotaStore{costs: map[string]float64{"preloaded": 9.0}}
	if err := q.SyncAll(context.Background(), store); err != nil {
		t.Fatal(err)
	}

	if !q.Check("preloaded", 10.0) {
		t.Error("preloaded key at 9/10 should be within budget")
	}

	store.costs["preloaded"] = 11.0
	if err := q.SyncAll(context.Background(), store); err != nil {
		t.Fatal(err)
	}

	if q.Check("preloaded", 10.0) {
		t.Error("preloaded key at 11/10 should be over budget")
	}
}

func TestQuotaTracker_PreloadIdempotent(t *testing.T) {
	t.Parallel()
	q := NewQuotaTracker()

	q.Consume("existing", 5.0)
	q.Preload("existing", 10.0)

	// Preload should not overwrite existing entry.
	if !q.Check("existing", 10.0) {
		t.Error("existing key at 5/10 should be within budget")
	}
}
