// Package circuitbreaker implements a per-provider circuit breaker with a sliding-window
// error rate detector. It short-circuits requests to known-bad providers, reducing
// failover latency from seconds (timeout + network) to nanoseconds (state check).
package circuitbreaker

import (
	"sync"
	"time"
)

// State represents the circuit breaker state.
type State int

const (
	// StateClosed allows all requests through.
	StateClosed State = iota
	// StateOpen rejects all requests.
	StateOpen
	// StateHalfOpen allows a single probe request.
	StateHalfOpen
)

// String returns a human-readable state name.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// Config holds circuit breaker parameters.
type Config struct {
	ErrorThreshold float64       // weighted error rate to trip (e.g. 0.30)
	MinSamples     int           // minimum requests before breaker can open
	WindowSeconds  int           // sliding window duration in seconds
	OpenTimeout    time.Duration // time in OPEN before transitioning to HALF_OPEN
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		ErrorThreshold: 0.30,
		MinSamples:     10,
		WindowSeconds:  60,
		OpenTimeout:    30 * time.Second,
	}
}

// bucket holds error and request counts for a 1-second slot.
type bucket struct {
	errors float64 // weighted error sum
	total  int     // total requests
}

// SlidingWindow is a fixed-size ring buffer of 1-second buckets.
// The array is stack-allocated to avoid heap allocs.
type SlidingWindow struct {
	buckets  [60]bucket // fixed-size, no heap alloc
	size     int        // number of active buckets (== windowSeconds)
	head     int        // index of current bucket
	headTime int64      // unix seconds of head bucket
}

// newSlidingWindow creates a sliding window with the given bucket count (capped at 60).
func newSlidingWindow(windowSeconds int) SlidingWindow {
	if windowSeconds <= 0 || windowSeconds > 60 {
		windowSeconds = 60
	}
	return SlidingWindow{size: windowSeconds}
}

// advance moves the head forward to the current second, clearing stale buckets.
func (w *SlidingWindow) advance(nowSec int64) {
	if w.headTime == 0 {
		w.headTime = nowSec
		return
	}
	gap := nowSec - w.headTime
	if gap <= 0 {
		return
	}
	// Clear buckets that have expired.
	clear := min(int(gap), w.size)
	for i := range clear {
		idx := (w.head + 1 + i) % w.size
		w.buckets[idx] = bucket{}
	}
	w.head = (w.head + int(gap)) % w.size
	w.headTime = nowSec
}

// Record adds a request with the given error weight to the current bucket.
// Weight 0 means success.
func (w *SlidingWindow) Record(weight float64, now time.Time) {
	nowSec := now.Unix()
	w.advance(nowSec)
	w.buckets[w.head].total++
	w.buckets[w.head].errors += weight
}

// ErrorRate returns the weighted error rate and total sample count across the window.
func (w *SlidingWindow) ErrorRate(now time.Time) (rate float64, samples int) {
	nowSec := now.Unix()
	w.advance(nowSec)
	var totalErrors float64
	var totalRequests int
	for i := range w.size {
		b := &w.buckets[i]
		totalErrors += b.errors
		totalRequests += b.total
	}
	if totalRequests == 0 {
		return 0, 0
	}
	return totalErrors / float64(totalRequests), totalRequests
}

// Reset clears all buckets.
func (w *SlidingWindow) Reset() {
	for i := range w.size {
		w.buckets[i] = bucket{}
	}
	w.headTime = 0
	w.head = 0
}

// Breaker is a per-provider circuit breaker state machine.
type Breaker struct {
	mu          sync.Mutex
	state       State
	window      SlidingWindow
	openedAt    time.Time     // when transitioned to OPEN
	lastUsed    time.Time     // for stale eviction
	probing     bool          // true when a half-open probe is in flight
	threshold   float64       // weighted error rate to trip
	minSamples  int           // min requests before CB can open
	openTimeout time.Duration // OPEN -> HALF_OPEN transition time
}

// NewBreaker creates a breaker with the given config.
func NewBreaker(cfg Config) *Breaker {
	return &Breaker{
		state:       StateClosed,
		window:      newSlidingWindow(cfg.WindowSeconds),
		threshold:   cfg.ErrorThreshold,
		minSamples:  cfg.MinSamples,
		openTimeout: cfg.OpenTimeout,
		lastUsed:    time.Now(),
	}
}

// State returns the current breaker state.
func (b *Breaker) State() State {
	b.mu.Lock()
	s := b.state
	b.mu.Unlock()
	return s
}

// Allow checks whether a request should be allowed through.
// Returns true if the request may proceed.
func (b *Breaker) Allow() bool {
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastUsed = now

	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		if now.Sub(b.openedAt) >= b.openTimeout {
			b.state = StateHalfOpen
			b.probing = false
			// Allow this request as the probe.
			b.probing = true
			return true
		}
		return false
	case StateHalfOpen:
		if !b.probing {
			// Allow exactly one probe.
			b.probing = true
			return true
		}
		// Another probe is already in flight; reject.
		return false
	}
	return false
}

// RecordSuccess records a successful request outcome.
func (b *Breaker) RecordSuccess() {
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastUsed = now
	b.window.Record(0, now)

	if b.state == StateHalfOpen {
		// Probe succeeded: close the breaker.
		b.state = StateClosed
		b.probing = false
		b.window.Reset()
	}
}

// RecordError records a failed request with the given error weight.
func (b *Breaker) RecordError(weight float64) {
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastUsed = now
	b.window.Record(weight, now)

	switch b.state {
	case StateClosed:
		rate, samples := b.window.ErrorRate(now)
		if samples >= b.minSamples && rate >= b.threshold {
			b.state = StateOpen
			b.openedAt = now
		}
	case StateHalfOpen:
		// Probe failed: reopen.
		b.state = StateOpen
		b.openedAt = now
		b.probing = false
	}
}

// LastUsed returns the time of last activity (for stale eviction).
func (b *Breaker) LastUsed() time.Time {
	b.mu.Lock()
	t := b.lastUsed
	b.mu.Unlock()
	return t
}
