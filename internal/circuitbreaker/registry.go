package circuitbreaker

import (
	"sync"
	"time"
)

// Registry manages per-provider Breaker instances.
type Registry struct {
	mu       sync.RWMutex
	breakers map[string]*Breaker
	config   Config
}

// NewRegistry creates a new circuit breaker registry with the given config.
func NewRegistry(cfg Config) *Registry {
	return &Registry{
		breakers: make(map[string]*Breaker),
		config:   cfg,
	}
}

// Get returns the breaker for the given provider ID, or nil if none exists.
func (r *Registry) Get(providerID string) *Breaker {
	r.mu.RLock()
	b := r.breakers[providerID]
	r.mu.RUnlock()
	return b
}

// GetOrCreate returns the breaker for providerID, creating one if needed.
// Uses double-check locking to minimize write-lock contention.
func (r *Registry) GetOrCreate(providerID string) *Breaker {
	r.mu.RLock()
	b, ok := r.breakers[providerID]
	r.mu.RUnlock()
	if ok {
		return b
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check after acquiring write lock.
	if b, ok := r.breakers[providerID]; ok {
		return b
	}
	b = NewBreaker(r.config)
	r.breakers[providerID] = b
	return b
}

// EvictStale removes breakers not used since cutoff.
// Phase 1: RLock to snapshot stale keys. Phase 2: Lock to delete them.
func (r *Registry) EvictStale(cutoff time.Time) int {
	// Phase 1: read-lock to identify stale keys.
	r.mu.RLock()
	var staleKeys []string
	for k, b := range r.breakers {
		if b.LastUsed().Before(cutoff) {
			staleKeys = append(staleKeys, k)
		}
	}
	r.mu.RUnlock()

	if len(staleKeys) == 0 {
		return 0
	}

	// Phase 2: write-lock only for deletions.
	r.mu.Lock()
	defer r.mu.Unlock()
	evicted := 0
	for _, k := range staleKeys {
		if b, ok := r.breakers[k]; ok {
			if b.LastUsed().Before(cutoff) {
				delete(r.breakers, k)
				evicted++
			}
		}
	}
	return evicted
}
