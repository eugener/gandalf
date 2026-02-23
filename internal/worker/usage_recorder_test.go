package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	gateway "github.com/eugener/gandalf/internal"
)

type fakeUsageStore struct {
	mu      sync.Mutex
	batches [][]gateway.UsageRecord
}

func (s *fakeUsageStore) InsertUsage(_ context.Context, records []gateway.UsageRecord) error {
	s.mu.Lock()
	s.batches = append(s.batches, records)
	s.mu.Unlock()
	return nil
}

func (s *fakeUsageStore) totalRecords() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, b := range s.batches {
		n += len(b)
	}
	return n
}

func TestUsageRecorder_BatchOnSize(t *testing.T) {
	t.Parallel()
	store := &fakeUsageStore{}
	rec := NewUsageRecorder(store)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		rec.Run(ctx)
		close(done)
	}()

	// Send exactly usageBatchSize records.
	for i := range usageBatchSize {
		rec.Record(gateway.UsageRecord{ID: string(rune('a' + i%26))})
	}

	// Wait for batch to be flushed.
	deadline := time.After(2 * time.Second)
	for {
		if store.totalRecords() >= usageBatchSize {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("batch not flushed; got %d records", store.totalRecords())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	cancel()
	<-done
}

func TestUsageRecorder_FlushOnTimeout(t *testing.T) {
	t.Parallel()
	store := &fakeUsageStore{}
	rec := &UsageRecorder{
		ch:    make(chan gateway.UsageRecord, usageChanSize),
		store: store,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		rec.Run(ctx)
		close(done)
	}()

	// Send fewer than batch size.
	rec.Record(gateway.UsageRecord{ID: "test-1"})
	rec.Record(gateway.UsageRecord{ID: "test-2"})

	// Wait for ticker-based flush (usageFlushEvery = 5s, but test should pass).
	deadline := time.After(10 * time.Second)
	for {
		if store.totalRecords() >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timeout flush not triggered; got %d records", store.totalRecords())
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	cancel()
	<-done
}

func TestUsageRecorder_DropOnFull(t *testing.T) {
	t.Parallel()
	store := &fakeUsageStore{}
	rec := &UsageRecorder{
		ch:    make(chan gateway.UsageRecord, 2), // tiny buffer
		store: store,
	}

	// Fill the channel.
	rec.Record(gateway.UsageRecord{ID: "1"})
	rec.Record(gateway.UsageRecord{ID: "2"})
	// This should be dropped silently.
	rec.Record(gateway.UsageRecord{ID: "3"})

	if len(rec.ch) != 2 {
		t.Errorf("channel len = %d, want 2", len(rec.ch))
	}
}

func TestUsageRecorder_DrainOnShutdown(t *testing.T) {
	t.Parallel()
	store := &fakeUsageStore{}
	rec := NewUsageRecorder(store)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		rec.Run(ctx)
		close(done)
	}()

	// Send some records.
	rec.Record(gateway.UsageRecord{ID: "drain-1"})
	rec.Record(gateway.UsageRecord{ID: "drain-2"})

	// Cancel immediately -- should drain.
	time.Sleep(50 * time.Millisecond) // let the goroutine start
	cancel()
	<-done

	if store.totalRecords() < 2 {
		t.Errorf("expected at least 2 drained records, got %d", store.totalRecords())
	}
}
