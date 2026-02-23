package ratelimit

import (
	"context"
	"sync"
)

// QuotaStore provides aggregated usage cost for quota sync.
type QuotaStore interface {
	SumUsageCost(ctx context.Context, keyID string) (float64, error)
}

// budgetEntry tracks cumulative spend for a single key.
type budgetEntry struct {
	limit    float64
	consumed float64
}

// QuotaTracker enforces cumulative spend budgets per API key.
type QuotaTracker struct {
	mu      sync.Mutex
	budgets map[string]*budgetEntry
}

// NewQuotaTracker creates a new QuotaTracker.
func NewQuotaTracker() *QuotaTracker {
	return &QuotaTracker{
		budgets: make(map[string]*budgetEntry),
	}
}

// Check returns true if the key is within its budget.
// Returns true if limit is 0 (unlimited) or if no entry exists yet.
func (q *QuotaTracker) Check(keyID string, limit float64) bool {
	if limit <= 0 {
		return true
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.budgets[keyID]
	if !ok {
		q.budgets[keyID] = &budgetEntry{limit: limit}
		return true
	}
	e.limit = limit
	return e.consumed < limit
}

// Consume adds cost to the key's accumulated spend.
func (q *QuotaTracker) Consume(keyID string, costUSD float64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.budgets[keyID]
	if !ok {
		e = &budgetEntry{}
		q.budgets[keyID] = e
	}
	e.consumed += costUSD
}

// Sync reloads a key's consumed amount from the store.
func (q *QuotaTracker) Sync(ctx context.Context, store QuotaStore, keyID string) error {
	total, err := store.SumUsageCost(ctx, keyID)
	if err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.budgets[keyID]
	if !ok {
		e = &budgetEntry{}
		q.budgets[keyID] = e
	}
	e.consumed = total
	return nil
}

// SyncAll reloads consumed amounts for all tracked keys from the store.
func (q *QuotaTracker) SyncAll(ctx context.Context, store QuotaStore) error {
	q.mu.Lock()
	keys := make([]string, 0, len(q.budgets))
	for k := range q.budgets {
		keys = append(keys, k)
	}
	q.mu.Unlock()

	for _, k := range keys {
		if err := q.Sync(ctx, store, k); err != nil {
			return err
		}
	}
	return nil
}
