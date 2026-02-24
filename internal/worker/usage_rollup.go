package worker

import (
	"context"
	"log/slog"
	"time"

	gateway "github.com/eugener/gandalf/internal"
)

const (
	rollupInterval = 5 * time.Minute
)

// RollupStore is the persistence interface consumed by UsageRollupWorker.
type RollupStore interface {
	QueryUsage(ctx context.Context, filter gateway.UsageFilter) ([]gateway.UsageRecord, error)
	UpsertRollup(ctx context.Context, rollups []gateway.UsageRollup) error
}

// UsageRollupWorker periodically aggregates raw usage records into hourly rollups.
type UsageRollupWorker struct {
	store RollupStore
}

// NewUsageRollupWorker creates a new rollup worker.
func NewUsageRollupWorker(store RollupStore) *UsageRollupWorker {
	return &UsageRollupWorker{store: store}
}

// Name returns the worker identifier.
func (w *UsageRollupWorker) Name() string { return "usage_rollup" }

// Run aggregates usage records into hourly rollups on a periodic schedule.
func (w *UsageRollupWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(rollupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.rollup(ctx)
		}
	}
}

func (w *UsageRollupWorker) rollup(ctx context.Context) {
	// Aggregate the last 2 hours to cover any late-arriving records.
	now := time.Now().UTC()
	since := now.Add(-2 * time.Hour).Truncate(time.Hour).Format(time.RFC3339)
	until := now.Truncate(time.Hour).Format(time.RFC3339)

	records, err := w.store.QueryUsage(ctx, gateway.UsageFilter{
		Since: since,
		Until: until,
		Limit: 10_000,
	})
	if err != nil {
		slog.LogAttrs(ctx, slog.LevelError, "rollup query failed",
			slog.String("error", err.Error()),
		)
		return
	}
	if len(records) == 0 {
		return
	}

	// Aggregate by (org_id, key_id, model, hour).
	type key struct {
		OrgID  string
		KeyID  string
		Model  string
		Bucket string
	}
	agg := make(map[key]*gateway.UsageRollup)
	for _, r := range records {
		bucket := r.CreatedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
		k := key{OrgID: r.OrgID, KeyID: r.KeyID, Model: r.Model, Bucket: bucket}
		if _, ok := agg[k]; !ok {
			agg[k] = &gateway.UsageRollup{
				OrgID:  r.OrgID,
				KeyID:  r.KeyID,
				Model:  r.Model,
				Period: "hourly",
				Bucket: bucket,
			}
		}
		ru := agg[k]
		ru.RequestCount++
		ru.PromptTokens += r.PromptTokens
		ru.CompletionTokens += r.CompletionTokens
		ru.TotalTokens += r.TotalTokens
		ru.CostUSD += r.CostUSD
		if r.Cached {
			ru.CachedCount++
		}
	}

	rollups := make([]gateway.UsageRollup, 0, len(agg))
	for _, r := range agg {
		rollups = append(rollups, *r)
	}

	if err := w.store.UpsertRollup(ctx, rollups); err != nil {
		slog.LogAttrs(ctx, slog.LevelError, "rollup upsert failed",
			slog.String("error", err.Error()),
		)
		return
	}
	slog.Info("usage rollup completed", "rollups", len(rollups), "records", len(records))
}
