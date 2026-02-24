package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	gateway "github.com/eugener/gandalf/internal"
)

const (
	usageChanSize   = 1000
	usageBatchSize  = 100
	usageFlushEvery = 5 * time.Second
	usageDrainTime  = 30 * time.Second
)

// UsageStore is the persistence interface consumed by UsageRecorder.
type UsageStore interface {
	InsertUsage(ctx context.Context, records []gateway.UsageRecord) error
}

// UsageRecorder buffers usage records and batch-flushes them to the store.
// Records are dropped if the channel is full (back-pressure on slow DB).
type UsageRecorder struct {
	ch    chan gateway.UsageRecord
	store UsageStore
}

// NewUsageRecorder creates a UsageRecorder backed by store.
func NewUsageRecorder(store UsageStore) *UsageRecorder {
	return &UsageRecorder{
		ch:    make(chan gateway.UsageRecord, usageChanSize),
		store: store,
	}
}

// Name returns the worker identifier.
func (u *UsageRecorder) Name() string { return "usage_recorder" }

// Record enqueues a usage record. It never blocks; drops on full channel.
func (u *UsageRecorder) Record(r gateway.UsageRecord) {
	select {
	case u.ch <- r:
	default:
		slog.Warn("usage record dropped, channel full")
	}
}

// Run processes records until ctx is cancelled, then drains remaining records.
func (u *UsageRecorder) Run(ctx context.Context) error {
	ticker := time.NewTicker(usageFlushEvery)
	defer ticker.Stop()

	buf := make([]gateway.UsageRecord, 0, usageBatchSize)

	for {
		select {
		case r := <-u.ch:
			buf = append(buf, r)
			if len(buf) >= usageBatchSize {
				u.flush(ctx, buf)
				buf = buf[:0]
			}

		case <-ticker.C:
			if len(buf) > 0 {
				u.flush(ctx, buf)
				buf = buf[:0]
			}

		case <-ctx.Done():
			// Drain remaining records with a timeout.
			u.drain(buf)
			return nil
		}
	}
}

func (u *UsageRecorder) drain(buf []gateway.UsageRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), usageDrainTime)
	defer cancel()

	for {
		select {
		case r := <-u.ch:
			buf = append(buf, r)
			if len(buf) >= usageBatchSize {
				u.flush(ctx, buf)
				buf = buf[:0]
			}
		default:
			// Channel empty, flush remaining.
			if len(buf) > 0 {
				u.flush(ctx, buf)
			}
			return
		}
	}
}

func (u *UsageRecorder) flush(ctx context.Context, buf []gateway.UsageRecord) {
	// Copy to avoid aliasing the caller's slice.
	batch := make([]gateway.UsageRecord, len(buf))
	copy(batch, buf)

	// Assign IDs off the hot path; callers leave ID empty.
	for i := range batch {
		if batch[i].ID == "" {
			batch[i].ID = uuid.Must(uuid.NewV7()).String()
		}
	}

	if err := u.store.InsertUsage(ctx, batch); err != nil {
		slog.LogAttrs(ctx, slog.LevelError, "usage flush failed",
			slog.Int("count", len(batch)),
			slog.String("error", err.Error()),
		)
	}
}
