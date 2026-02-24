package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/eugener/gandalf/internal/ratelimit"
)

const quotaSyncInterval = 60 * time.Second

// QuotaSyncWorker periodically syncs in-memory quota counters from the DB.
type QuotaSyncWorker struct {
	tracker *ratelimit.QuotaTracker
	store   ratelimit.QuotaStore
}

// NewQuotaSyncWorker creates a QuotaSyncWorker.
func NewQuotaSyncWorker(tracker *ratelimit.QuotaTracker, store ratelimit.QuotaStore) *QuotaSyncWorker {
	return &QuotaSyncWorker{tracker: tracker, store: store}
}

// Name returns the worker identifier.
func (w *QuotaSyncWorker) Name() string { return "quota_sync" }

// Run performs an initial sync, then periodically syncs quota counters until ctx is cancelled.
func (w *QuotaSyncWorker) Run(ctx context.Context) error {
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
