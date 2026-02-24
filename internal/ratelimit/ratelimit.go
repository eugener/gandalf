// Package ratelimit implements per-key RPM and TPM rate limiting with lazy-refill token buckets.
package ratelimit

import (
	"sync"
	"time"
)

// Limits holds the effective RPM and TPM limits for a key.
// A value of 0 means unlimited.
type Limits struct {
	RPM int64
	TPM int64
}

// Result is the outcome of a rate limit check.
type Result struct {
	Allowed           bool
	Limit             int64
	Remaining         int64
	RetryAfterSeconds float64
}

// Bucket is a token bucket with lazy refill (no background goroutine).
type Bucket struct {
	tokens   float64
	max      float64
	rate     float64 // tokens per second
	lastFill time.Time
}

func newBucket(limit int64) *Bucket {
	return &Bucket{
		tokens:   float64(limit),
		max:      float64(limit),
		rate:     float64(limit) / 60.0, // per-minute limit -> per-second rate
		lastFill: time.Now(),
	}
}

// refill adds tokens based on elapsed time since last refill.
func (b *Bucket) refill(now time.Time) {
	elapsed := now.Sub(b.lastFill).Seconds()
	if elapsed <= 0 {
		return
	}
	b.tokens = min(b.max, b.tokens+elapsed*b.rate)
	b.lastFill = now
}

// tryConsume attempts to consume n tokens. Returns remaining and whether allowed.
func (b *Bucket) tryConsume(n float64, now time.Time) (remaining int64, allowed bool) {
	b.refill(now)
	if b.tokens >= n {
		b.tokens -= n
		return int64(b.tokens), true
	}
	return 0, false
}

// retryAfter returns seconds until n tokens are available.
func (b *Bucket) retryAfter(n float64) float64 {
	if b.tokens >= n {
		return 0
	}
	deficit := n - b.tokens
	return deficit / b.rate
}

// remaining returns current token count.
func (b *Bucket) remaining() int64 {
	return int64(b.tokens)
}

// adjust adds or removes tokens (for post-response correction).
func (b *Bucket) adjust(delta float64) {
	b.tokens = min(b.max, max(0, b.tokens+delta))
}

// Limiter holds dual RPM + TPM buckets for a single key.
type Limiter struct {
	mu     sync.Mutex
	rpm    *Bucket // nil if RPM unlimited
	tpm    *Bucket // nil if TPM unlimited
	limits Limits
	lastUsed time.Time
}

// newLimiter creates a Limiter with the given limits.
func newLimiter(limits Limits) *Limiter {
	l := &Limiter{limits: limits, lastUsed: time.Now()}
	if limits.RPM > 0 {
		l.rpm = newBucket(limits.RPM)
	}
	if limits.TPM > 0 {
		l.tpm = newBucket(limits.TPM)
	}
	return l
}

// AllowRPM consumes 1 RPM token.
func (l *Limiter) AllowRPM() Result {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.lastUsed = now

	if l.rpm == nil {
		return Result{Allowed: true}
	}

	remaining, ok := l.rpm.tryConsume(1, now)
	if ok {
		return Result{
			Allowed:   true,
			Limit:     l.limits.RPM,
			Remaining: remaining,
		}
	}
	return Result{
		Allowed:           false,
		Limit:             l.limits.RPM,
		Remaining:         0,
		RetryAfterSeconds: l.rpm.retryAfter(1),
	}
}

// ConsumeTPM consumes estimated TPM tokens.
func (l *Limiter) ConsumeTPM(estimated int64) Result {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.lastUsed = now

	if l.tpm == nil {
		return Result{Allowed: true}
	}

	remaining, ok := l.tpm.tryConsume(float64(estimated), now)
	if ok {
		return Result{
			Allowed:   true,
			Limit:     l.limits.TPM,
			Remaining: remaining,
		}
	}
	return Result{
		Allowed:           false,
		Limit:             l.limits.TPM,
		Remaining:         0,
		RetryAfterSeconds: l.tpm.retryAfter(float64(estimated)),
	}
}

// AdjustTPM corrects the TPM bucket by delta (estimated - actual).
// Positive delta refunds tokens; negative consumes more.
func (l *Limiter) AdjustTPM(delta int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.tpm != nil {
		l.tpm.adjust(float64(delta))
	}
}

// RPMResult returns current RPM state without consuming.
func (l *Limiter) RPMResult() Result {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rpm == nil {
		return Result{Allowed: true}
	}
	l.rpm.refill(time.Now())
	return Result{
		Allowed:   true,
		Limit:     l.limits.RPM,
		Remaining: l.rpm.remaining(),
	}
}

// Registry manages per-key Limiters.
type Registry struct {
	mu       sync.RWMutex
	limiters map[string]*Limiter
}

// NewRegistry creates a new rate limiter registry.
func NewRegistry() *Registry {
	return &Registry{
		limiters: make(map[string]*Limiter),
	}
}

// GetOrCreate returns the limiter for keyID, creating one if needed.
// If the key's limits have changed, a new limiter is created.
func (r *Registry) GetOrCreate(keyID string, limits Limits) *Limiter {
	r.mu.RLock()
	l, ok := r.limiters[keyID]
	r.mu.RUnlock()
	if ok && l.limits == limits {
		return l
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check after acquiring write lock.
	if l, ok := r.limiters[keyID]; ok && l.limits == limits {
		return l
	}
	l = newLimiter(limits)
	r.limiters[keyID] = l
	return l
}

// EvictStale removes limiters not used since cutoff.
func (r *Registry) EvictStale(cutoff time.Time) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	evicted := 0
	for k, l := range r.limiters {
		l.mu.Lock()
		stale := l.lastUsed.Before(cutoff)
		l.mu.Unlock()
		if stale {
			delete(r.limiters, k)
			evicted++
		}
	}
	return evicted
}
