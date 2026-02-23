package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestLimiter_AllowRPM(t *testing.T) {
	t.Parallel()
	l := newLimiter(Limits{RPM: 3})

	for i := range 3 {
		r := l.AllowRPM()
		if !r.Allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	r := l.AllowRPM()
	if r.Allowed {
		t.Error("4th request should be denied")
	}
	if r.RetryAfterSeconds <= 0 {
		t.Error("RetryAfterSeconds should be positive")
	}
}

func TestLimiter_RefillAfterTime(t *testing.T) {
	t.Parallel()
	l := newLimiter(Limits{RPM: 1})

	r := l.AllowRPM()
	if !r.Allowed {
		t.Fatal("first request should be allowed")
	}

	r = l.AllowRPM()
	if r.Allowed {
		t.Fatal("second request should be denied")
	}

	// Manually advance the bucket's last fill time.
	l.mu.Lock()
	l.rpm.lastFill = time.Now().Add(-61 * time.Second)
	l.mu.Unlock()

	r = l.AllowRPM()
	if !r.Allowed {
		t.Error("request should be allowed after refill")
	}
}

func TestLimiter_DualBucketIndependence(t *testing.T) {
	t.Parallel()
	l := newLimiter(Limits{RPM: 100, TPM: 10})

	// Exhaust TPM.
	r := l.ConsumeTPM(10)
	if !r.Allowed {
		t.Fatal("first TPM consume should be allowed")
	}

	r = l.ConsumeTPM(1)
	if r.Allowed {
		t.Error("TPM should be exhausted")
	}

	// RPM should still work.
	rpm := l.AllowRPM()
	if !rpm.Allowed {
		t.Error("RPM should be independent of TPM")
	}
}

func TestLimiter_AdjustTPM(t *testing.T) {
	t.Parallel()
	l := newLimiter(Limits{TPM: 100})

	// Consume 80, then adjust +30 (overestimated).
	l.ConsumeTPM(80)
	l.AdjustTPM(30) // refund 30

	r := l.ConsumeTPM(45)
	if !r.Allowed {
		t.Error("should be allowed after adjustment (had 50 remaining)")
	}

	r = l.ConsumeTPM(10)
	if r.Allowed {
		t.Error("should be denied after consuming more than remaining")
	}
}

func TestLimiter_UnlimitedRPM(t *testing.T) {
	t.Parallel()
	l := newLimiter(Limits{RPM: 0, TPM: 100})

	r := l.AllowRPM()
	if !r.Allowed {
		t.Error("unlimited RPM should always allow")
	}
	if r.Limit != 0 {
		t.Error("limit should be 0 for unlimited")
	}
}

func TestLimiter_UnlimitedTPM(t *testing.T) {
	t.Parallel()
	l := newLimiter(Limits{RPM: 100, TPM: 0})

	r := l.ConsumeTPM(1000000)
	if !r.Allowed {
		t.Error("unlimited TPM should always allow")
	}
}

func TestLimiter_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	l := newLimiter(Limits{RPM: 1000, TPM: 100000})

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			l.AllowRPM()
			l.ConsumeTPM(10)
			l.AdjustTPM(5)
		})
	}
	wg.Wait()
}

func TestRegistry_GetOrCreate(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	l1 := r.GetOrCreate("key1", Limits{RPM: 10})
	l2 := r.GetOrCreate("key1", Limits{RPM: 10})
	if l1 != l2 {
		t.Error("same key+limits should return same limiter")
	}

	l3 := r.GetOrCreate("key1", Limits{RPM: 20})
	if l1 == l3 {
		t.Error("changed limits should create new limiter")
	}
}

func TestRegistry_EvictStale(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	r.GetOrCreate("fresh", Limits{RPM: 10})
	r.GetOrCreate("stale", Limits{RPM: 10})

	// Manually make "stale" entry old.
	r.mu.Lock()
	r.limiters["stale"].mu.Lock()
	r.limiters["stale"].lastUsed = time.Now().Add(-2 * time.Hour)
	r.limiters["stale"].mu.Unlock()
	r.mu.Unlock()

	evicted := r.EvictStale(time.Now().Add(-1 * time.Hour))
	if evicted != 1 {
		t.Errorf("evicted = %d, want 1", evicted)
	}

	r.mu.RLock()
	_, hasFresh := r.limiters["fresh"]
	_, hasStale := r.limiters["stale"]
	r.mu.RUnlock()

	if !hasFresh {
		t.Error("fresh limiter should not be evicted")
	}
	if hasStale {
		t.Error("stale limiter should be evicted")
	}
}

func BenchmarkAllowRPM(b *testing.B) {
	l := newLimiter(Limits{RPM: 1_000_000}) // high limit so it never denies
	for b.Loop() {
		l.AllowRPM()
	}
}

func TestLimiter_RPMResult(t *testing.T) {
	t.Parallel()
	l := newLimiter(Limits{RPM: 10})
	l.AllowRPM()

	r := l.RPMResult()
	if !r.Allowed {
		t.Error("RPMResult should show allowed")
	}
	if r.Limit != 10 {
		t.Errorf("limit = %d, want 10", r.Limit)
	}
	if r.Remaining < 8 || r.Remaining > 9 {
		t.Errorf("remaining = %d, want ~9", r.Remaining)
	}
}

func TestLimiter_RPMResult_Unlimited(t *testing.T) {
	t.Parallel()
	l := newLimiter(Limits{RPM: 0, TPM: 0})
	r := l.RPMResult()
	if !r.Allowed {
		t.Error("unlimited RPMResult should be allowed")
	}
}

func TestBucket_RefillNegativeElapsed(t *testing.T) {
	t.Parallel()
	// Bucket with token = 5/10.
	l := newLimiter(Limits{RPM: 10})
	l.mu.Lock()
	l.rpm.tokens = 5
	old := l.rpm.lastFill
	l.rpm.lastFill = time.Now().Add(time.Hour) // future
	l.mu.Unlock()

	r := l.AllowRPM()
	if !r.Allowed {
		t.Error("should be allowed (refill skipped for negative elapsed)")
	}

	// Restore for cleanup.
	l.mu.Lock()
	l.rpm.lastFill = old
	l.mu.Unlock()
}

func TestBucket_RetryAfterAvailable(t *testing.T) {
	t.Parallel()
	l := newLimiter(Limits{RPM: 60}) // 1 token/sec
	// Exhaust all tokens.
	for range 60 {
		l.AllowRPM()
	}
	r := l.AllowRPM()
	if r.Allowed {
		t.Fatal("should be denied")
	}
	if r.RetryAfterSeconds <= 0 {
		t.Error("retry after should be positive")
	}
}

func BenchmarkConsumeTPM(b *testing.B) {
	l := newLimiter(Limits{TPM: 1_000_000_000})
	for b.Loop() {
		l.ConsumeTPM(100)
	}
}
