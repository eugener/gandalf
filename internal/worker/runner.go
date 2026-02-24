package worker

import (
	"context"
	"log/slog"

	"golang.org/x/sync/errgroup"
)

// Runner manages a set of workers, cancelling all on first error.
type Runner struct {
	workers []Worker
}

// NewRunner creates a Runner with the given workers.
func NewRunner(workers ...Worker) *Runner {
	return &Runner{workers: workers}
}

// Run starts all workers in parallel. It blocks until all workers finish.
// If any worker returns a non-nil error, the context is cancelled and
// the first error is returned.
func (r *Runner) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)
	for _, w := range r.workers {
		slog.Info("worker started", "type", workerName(w))
		g.Go(func() error {
			return w.Run(ctx)
		})
	}
	return g.Wait()
}

func workerName(w Worker) string {
	switch w.(type) {
	case *UsageRecorder:
		return "usage_recorder"
	case *QuotaSyncWorker:
		return "quota_sync"
	case *UsageRollupWorker:
		return "usage_rollup"
	default:
		return "unknown"
	}
}
