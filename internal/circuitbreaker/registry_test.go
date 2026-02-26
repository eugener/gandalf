package circuitbreaker

import (
	"testing"
	"time"
)

func TestRegistry_GetOrCreate(t *testing.T) {
	t.Parallel()

	r := NewRegistry(DefaultConfig())

	b1 := r.GetOrCreate("provider-a")
	if b1 == nil {
		t.Fatal("GetOrCreate returned nil")
	}

	// Second call returns same instance.
	b2 := r.GetOrCreate("provider-a")
	if b1 != b2 {
		t.Fatal("GetOrCreate returned different instance")
	}

	// Different provider gets different instance.
	b3 := r.GetOrCreate("provider-b")
	if b1 == b3 {
		t.Fatal("different providers should get different breakers")
	}
}

func TestRegistry_Get(t *testing.T) {
	t.Parallel()

	r := NewRegistry(DefaultConfig())

	// Get returns nil for unknown provider.
	if b := r.Get("unknown"); b != nil {
		t.Fatal("Get should return nil for unknown provider")
	}

	r.GetOrCreate("known")
	if b := r.Get("known"); b == nil {
		t.Fatal("Get should return breaker after GetOrCreate")
	}
}

func TestRegistry_EvictStale(t *testing.T) {
	t.Parallel()

	r := NewRegistry(DefaultConfig())
	r.GetOrCreate("active")
	r.GetOrCreate("stale")

	// Touch "active" to keep it fresh.
	r.Get("active").Allow()

	// Evict with cutoff in the future should evict everything.
	cutoff := time.Now().Add(1 * time.Hour)
	evicted := r.EvictStale(cutoff)
	if evicted != 2 {
		t.Fatalf("evicted = %d, want 2", evicted)
	}

	if b := r.Get("active"); b != nil {
		t.Fatal("active should be evicted (cutoff is in future)")
	}
}

func TestRegistry_EvictStale_KeepsFresh(t *testing.T) {
	t.Parallel()

	r := NewRegistry(DefaultConfig())
	r.GetOrCreate("fresh")

	// Cutoff in the past should keep everything.
	cutoff := time.Now().Add(-1 * time.Hour)
	evicted := r.EvictStale(cutoff)
	if evicted != 0 {
		t.Fatalf("evicted = %d, want 0", evicted)
	}

	if b := r.Get("fresh"); b == nil {
		t.Fatal("fresh breaker should still exist")
	}
}
