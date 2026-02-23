package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/maypok86/otter/v2"
)

// entry wraps a cached value with its expiration time.
type entry struct {
	data      []byte
	expiresAt time.Time
}

// Memory is an in-memory W-TinyLFU cache backed by otter.
type Memory struct {
	cache *otter.Cache[string, entry]
}

// NewMemory creates an in-memory cache with the given max entry count and default TTL.
func NewMemory(maxSize int, defaultTTL time.Duration) (*Memory, error) {
	c, err := otter.New[string, entry](&otter.Options[string, entry]{
		MaximumSize:      maxSize,
		ExpiryCalculator: otter.ExpiryWriting[string, entry](defaultTTL),
	})
	if err != nil {
		return nil, fmt.Errorf("create cache: %w", err)
	}
	return &Memory{cache: c}, nil
}

// Get retrieves a value from the cache if present and not expired.
func (m *Memory) Get(_ context.Context, key string) ([]byte, bool) {
	e, ok := m.cache.GetIfPresent(key)
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expiresAt) {
		m.cache.Invalidate(key)
		return nil, false
	}
	return e.data, true
}

// Set stores a value with per-entry TTL.
func (m *Memory) Set(_ context.Context, key string, val []byte, ttl time.Duration) {
	m.cache.Set(key, entry{
		data:      val,
		expiresAt: time.Now().Add(ttl),
	})
}

// Delete removes a value from the cache.
func (m *Memory) Delete(_ context.Context, key string) {
	m.cache.Invalidate(key)
}

// Purge removes all values from the cache.
func (m *Memory) Purge(_ context.Context) {
	m.cache.InvalidateAll()
}
