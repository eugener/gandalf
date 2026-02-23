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
