package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeWorker struct {
	runFn func(ctx context.Context) error
}

func (f *fakeWorker) Run(ctx context.Context) error {
	if f.runFn != nil {
		return f.runFn(ctx)
	}
	<-ctx.Done()
	return nil
}

func TestRunner_StopOnCancel(t *testing.T) {
	t.Parallel()
	w := &fakeWorker{}
	r := NewRunner(w)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop after cancel")
	}
}

func TestRunner_PropagateError(t *testing.T) {
	t.Parallel()
	testErr := errors.New("worker failed")
	w := &fakeWorker{runFn: func(context.Context) error { return testErr }}
	r := NewRunner(w)

	ctx := t.Context()

	err := r.Run(ctx)
	if !errors.Is(err, testErr) {
		t.Errorf("err = %v, want %v", err, testErr)
	}
}

func TestRunner_MultipleWorkers(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	w1 := &fakeWorker{runFn: func(ctx context.Context) error { count.Add(1); <-ctx.Done(); return nil }}
	w2 := &fakeWorker{runFn: func(ctx context.Context) error { count.Add(1); <-ctx.Done(); return nil }}
	r := NewRunner(w1, w2)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if count.Load() != 2 {
			t.Errorf("count = %d, want 2", count.Load())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop")
	}
}
