package circuitbreaker

import (
	"testing"
	"time"
)

func TestSlidingWindow_RecordAndErrorRate(t *testing.T) {
	t.Parallel()

	w := newSlidingWindow(60)
	now := time.Now()

	// 7 successes + 3 errors (weight 1.0) = 30% error rate.
	for range 7 {
		w.Record(0, now)
	}
	for range 3 {
		w.Record(1.0, now)
	}

	rate, samples := w.ErrorRate(now)
	if samples != 10 {
		t.Fatalf("samples = %d, want 10", samples)
	}
	if rate < 0.29 || rate > 0.31 {
		t.Fatalf("rate = %f, want ~0.30", rate)
	}
}

func TestSlidingWindow_Expiry(t *testing.T) {
	t.Parallel()

	w := newSlidingWindow(5) // 5-second window for fast test
	base := time.Now()

	// Record at t=0.
	w.Record(1.0, base)

	// At t=6, the old bucket should be expired.
	later := base.Add(6 * time.Second)
	rate, samples := w.ErrorRate(later)
	if samples != 0 {
		t.Fatalf("samples = %d, want 0 (expired)", samples)
	}
	if rate != 0 {
		t.Fatalf("rate = %f, want 0", rate)
	}
}

func TestSlidingWindow_Reset(t *testing.T) {
	t.Parallel()

	w := newSlidingWindow(60)
	now := time.Now()
	for range 20 {
		w.Record(1.0, now)
	}
	w.Reset()

	rate, samples := w.ErrorRate(now)
	if samples != 0 || rate != 0 {
		t.Fatalf("after reset: samples=%d rate=%f, want 0/0", samples, rate)
	}
}

func TestBreaker_ClosedAllows(t *testing.T) {
	t.Parallel()

	b := NewBreaker(DefaultConfig())
	if !b.Allow() {
		t.Fatal("closed breaker should allow")
	}
	if b.State() != StateClosed {
		t.Fatalf("state = %v, want closed", b.State())
	}
}

func TestBreaker_OpensOnThreshold(t *testing.T) {
	t.Parallel()

	cfg := Config{
		ErrorThreshold: 0.30,
		MinSamples:     10,
		WindowSeconds:  60,
		OpenTimeout:    30 * time.Second,
	}
	b := NewBreaker(cfg)

	// 7 successes + 3 errors = 30% -> should trip.
	for range 7 {
		b.RecordSuccess()
	}
	for range 3 {
		b.RecordError(1.0)
	}

	if b.State() != StateOpen {
		t.Fatalf("state = %v, want open", b.State())
	}
	if b.Allow() {
		t.Fatal("open breaker should reject")
	}
}

func TestBreaker_MinSamplesRequired(t *testing.T) {
	t.Parallel()

	cfg := Config{
		ErrorThreshold: 0.30,
		MinSamples:     10,
		WindowSeconds:  60,
		OpenTimeout:    30 * time.Second,
	}
	b := NewBreaker(cfg)

	// 9 samples at 100% error rate -> still below minSamples.
	for range 9 {
		b.RecordError(1.0)
	}

	if b.State() != StateClosed {
		t.Fatalf("state = %v, want closed (below min samples)", b.State())
	}
}

func TestBreaker_HalfOpenProbeSuccess(t *testing.T) {
	t.Parallel()

	cfg := Config{
		ErrorThreshold: 0.30,
		MinSamples:     10,
		WindowSeconds:  60,
		OpenTimeout:    1 * time.Millisecond, // tiny timeout for test
	}
	b := NewBreaker(cfg)

	// Trip the breaker.
	for range 10 {
		b.RecordError(1.0)
	}
	if b.State() != StateOpen {
		t.Fatalf("state = %v, want open", b.State())
	}

	// Wait for open timeout.
	time.Sleep(5 * time.Millisecond)

	// Allow should transition to half-open and permit probe.
	if !b.Allow() {
		t.Fatal("should allow probe in half-open")
	}
	if b.State() != StateHalfOpen {
		t.Fatalf("state = %v, want half_open", b.State())
	}

	// Second request should be rejected (probe in flight).
	if b.Allow() {
		t.Fatal("should reject during probe")
	}

	// Probe succeeds -> close.
	b.RecordSuccess()
	if b.State() != StateClosed {
		t.Fatalf("state = %v, want closed after probe success", b.State())
	}
}

func TestBreaker_HalfOpenProbeFailure(t *testing.T) {
	t.Parallel()

	cfg := Config{
		ErrorThreshold: 0.30,
		MinSamples:     10,
		WindowSeconds:  60,
		OpenTimeout:    1 * time.Millisecond,
	}
	b := NewBreaker(cfg)

	// Trip the breaker.
	for range 10 {
		b.RecordError(1.0)
	}

	time.Sleep(5 * time.Millisecond)

	// Allow probe.
	if !b.Allow() {
		t.Fatal("should allow probe")
	}

	// Probe fails -> reopen.
	b.RecordError(1.0)
	if b.State() != StateOpen {
		t.Fatalf("state = %v, want open after probe failure", b.State())
	}
}

func TestBreaker_WeightedErrors(t *testing.T) {
	t.Parallel()

	cfg := Config{
		ErrorThreshold: 0.30,
		MinSamples:     10,
		WindowSeconds:  60,
		OpenTimeout:    30 * time.Second,
	}
	b := NewBreaker(cfg)

	// 10 requests: 4 with weight 0.5 = 2.0 weighted errors / 10 total = 20% -> below threshold.
	for range 6 {
		b.RecordSuccess()
	}
	for range 4 {
		b.RecordError(0.5)
	}

	if b.State() != StateClosed {
		t.Fatalf("state = %v, want closed (20%% < 30%%)", b.State())
	}

	// Add 2 more errors with weight 1.5 = 3.0 more.
	// Now: (2.0 + 3.0) / 12 = 41.7% -> above threshold.
	for range 2 {
		b.RecordError(1.5)
	}
	if b.State() != StateOpen {
		t.Fatalf("state = %v, want open", b.State())
	}
}

func TestSlidingWindow_InvalidSize(t *testing.T) {
	t.Parallel()

	// windowSeconds <= 0 or > 60 should clamp to 60.
	w := newSlidingWindow(0)
	if w.size != 60 {
		t.Fatalf("size = %d, want 60 for zero input", w.size)
	}
	w2 := newSlidingWindow(100)
	if w2.size != 60 {
		t.Fatalf("size = %d, want 60 for oversized input", w2.size)
	}
	w3 := newSlidingWindow(-1)
	if w3.size != 60 {
		t.Fatalf("size = %d, want 60 for negative input", w3.size)
	}
}

func TestBreaker_AllProvidersFail_CBOpenForAll(t *testing.T) {
	t.Parallel()

	cfg := Config{
		ErrorThreshold: 0.50,
		MinSamples:     2,
		WindowSeconds:  60,
		OpenTimeout:    30 * time.Second,
	}
	b := NewBreaker(cfg)

	// All fail: 2 errors at 100% -> open.
	b.RecordError(1.0)
	b.RecordError(1.0)

	if b.State() != StateOpen {
		t.Fatalf("state = %v, want open", b.State())
	}
	if b.Allow() {
		t.Fatal("open breaker should reject")
	}
}

func TestBreaker_ZeroWeightDoesNotTrip(t *testing.T) {
	t.Parallel()

	cfg := Config{
		ErrorThreshold: 0.30,
		MinSamples:     10,
		WindowSeconds:  60,
		OpenTimeout:    30 * time.Second,
	}
	b := NewBreaker(cfg)

	// 10 "errors" with weight 0 (client errors) should not trip.
	for range 10 {
		b.RecordError(0)
	}
	if b.State() != StateClosed {
		t.Fatalf("state = %v, want closed (zero-weight errors)", b.State())
	}
}

func TestBreaker_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	b := NewBreaker(Config{
		ErrorThreshold: 0.50,
		MinSamples:     100,
		WindowSeconds:  60,
		OpenTimeout:    1 * time.Millisecond,
	})

	done := make(chan struct{})
	for range 10 {
		go func() {
			for range 100 {
				b.Allow()
				b.RecordSuccess()
				b.RecordError(0.5)
				_ = b.State()
				_ = b.LastUsed()
			}
			done <- struct{}{}
		}()
	}
	for range 10 {
		<-done
	}
	// No race detected = pass (test runs with -race).
}

func TestState_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state State
		want  string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half_open"},
		{State(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}
