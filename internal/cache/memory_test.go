package cache

import (
	"context"
	"testing"
	"time"
)

func TestMemory_GetSetDelete(t *testing.T) {
	t.Parallel()
	m, err := NewMemory(100, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Get non-existent.
	if _, ok := m.Get(ctx, "missing"); ok {
		t.Error("should not find missing key")
	}

	// Set and get.
	m.Set(ctx, "k1", []byte("v1"), time.Minute)
	// otter processes Set asynchronously; wait briefly.
	time.Sleep(50 * time.Millisecond)

	val, ok := m.Get(ctx, "k1")
	if !ok {
		t.Fatal("should find k1")
	}
	if string(val) != "v1" {
		t.Errorf("value = %q, want %q", val, "v1")
	}

	// Delete.
	m.Delete(ctx, "k1")
	if _, ok := m.Get(ctx, "k1"); ok {
		t.Error("should not find deleted key")
	}
}

func TestMemory_TTLExpiry(t *testing.T) {
	t.Parallel()
	m, err := NewMemory(100, time.Hour) // long default TTL
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Set with very short per-entry TTL.
	m.Set(ctx, "expiring", []byte("data"), 50*time.Millisecond)
	time.Sleep(50 * time.Millisecond)

	// Get should check our per-entry expiry.
	time.Sleep(50 * time.Millisecond)
	if _, ok := m.Get(ctx, "expiring"); ok {
		t.Error("entry should be expired")
	}
}

func TestMemory_Purge(t *testing.T) {
	t.Parallel()
	m, err := NewMemory(100, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	m.Set(ctx, "a", []byte("1"), time.Minute)
	m.Set(ctx, "b", []byte("2"), time.Minute)
	time.Sleep(50 * time.Millisecond)

	m.Purge(ctx)

	if _, ok := m.Get(ctx, "a"); ok {
		t.Error("purge should remove all keys")
	}
	if _, ok := m.Get(ctx, "b"); ok {
		t.Error("purge should remove all keys")
	}
}
