// Package cache provides response caching for the gateway.
package cache

import (
	"context"
	"time"
)

// Cache is the interface for response caching.
type Cache interface {
	// Get retrieves a cached value by key.
	Get(ctx context.Context, key string) ([]byte, bool)
	// Set stores a value with the given TTL.
	Set(ctx context.Context, key string, val []byte, ttl time.Duration)
	// Delete removes a cached value.
	Delete(ctx context.Context, key string)
	// Purge removes all cached values.
	Purge(ctx context.Context)
}
