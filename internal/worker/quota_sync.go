package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/eugener/gandalf/internal/ratelimit"
)

const quotaSyncInterval = 60 * time.Second

// KeyBudgetStore provides the list of keys with configured budgets.
type KeyBudgetStore interface {
	// ListBudgetedKeyIDs returns a map of key ID to max_budget for keys with budgets > 0.
	ListBudgetedKeyIDs(ctx context.Context) (map[string]float64, error)
}

// QuotaSyncWorker periodically syncs in-memory quota counters from the DB.
type QuotaSyncWorker struct {
	tracker    *ratelimit.QuotaTracker
	store      ratelimit.QuotaStore
	budgetStore KeyBudgetStore
}

// NewQuotaSyncWorker creates a QuotaSyncWorker.
func NewQuotaSyncWorker(tracker *ratelimit.QuotaTracker, store ratelimit.QuotaStore) *QuotaSyncWorker {
	return &QuotaSyncWorker{tracker: tracker, store: store}
}

// NewQuotaSyncWorkerWithBudgets creates a QuotaSyncWorker that preloads budgeted keys on startup.
func NewQuotaSyncWorkerWithBudgets(tracker *ratelimit.QuotaTracker, store ratelimit.QuotaStore, budgetStore KeyBudgetStore) *QuotaSyncWorker {
	return &QuotaSyncWorker{tracker: tracker, store: store, budgetStore: budgetStore}
}

// Name returns the worker identifier.
func (w *QuotaSyncWorker) Name() string { return "quota_sync" }

// Run preloads budgeted keys, performs an initial sync, then periodically
// syncs quota counters until ctx is cancelled.
func (w *QuotaSyncWorker) Run(ctx context.Context) error {
	// Pre-populate tracker with all keys that have budgets configured,
	// so quota checks work immediately after restart (not only after first request).
	if w.budgetStore != nil {
		if budgets, err := w.budgetStore.ListBudgetedKeyIDs(ctx); err != nil {
			slog.LogAttrs(ctx, slog.LevelError, "failed to preload budgeted keys",
				slog.String("error", err.Error()),
			)
		} else {
			for keyID, budget := range budgets {
				w.tracker.Preload(keyID, budget)
			}
		}
	}

	if err := w.tracker.SyncAll(ctx, w.store); err != nil {
		slog.LogAttrs(ctx, slog.LevelError, "initial quota sync failed",
			slog.String("error", err.Error()),
		)
	}

	ticker := time.NewTicker(quotaSyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := w.tracker.SyncAll(ctx, w.store); err != nil {
				slog.LogAttrs(ctx, slog.LevelError, "quota sync failed",
					slog.String("error", err.Error()),
				)
			}
		case <-ctx.Done():
			return nil
		}
	}
}
