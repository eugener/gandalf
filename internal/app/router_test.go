package app

import (
	"context"
	"testing"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/testutil"
)

func TestResolveModel_MultiTarget(t *testing.T) {
	t.Parallel()

	store := testutil.NewFakeStore()
	store.AddRoute(&gateway.Route{
		ID:         "r-1",
		ModelAlias: "gpt-4o",
		Targets:    []byte(`[{"provider_id":"anthropic","model":"claude-sonnet-4-6","priority":2},{"provider_id":"openai","model":"gpt-4o","priority":1}]`),
		Strategy:   "priority",
	})

	rs := NewRouterService(store)
	targets, err := rs.ResolveModel(context.Background(), "gpt-4o")
	if err != nil {
		t.Fatalf("ResolveModel: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("got %d targets, want 2", len(targets))
	}
	// Sorted by priority: openai (1) before anthropic (2).
	if targets[0].ProviderID != "openai" {
		t.Errorf("targets[0].ProviderID = %q, want openai", targets[0].ProviderID)
	}
	if targets[1].ProviderID != "anthropic" {
		t.Errorf("targets[1].ProviderID = %q, want anthropic", targets[1].ProviderID)
	}
}

func TestResolveModel_NoRoute(t *testing.T) {
	t.Parallel()

	store := testutil.NewFakeStore()
	rs := NewRouterService(store)

	_, err := rs.ResolveModel(context.Background(), "unknown-model")
	if err == nil {
		t.Fatal("expected error for unrouted model")
	}
}

func TestResolveModel_EmptyTargets(t *testing.T) {
	t.Parallel()

	store := testutil.NewFakeStore()
	store.AddRoute(&gateway.Route{
		ID:         "r-2",
		ModelAlias: "empty",
		Targets:    []byte(`[]`),
		Strategy:   "priority",
	})

	rs := NewRouterService(store)
	_, err := rs.ResolveModel(context.Background(), "empty")
	if err == nil {
		t.Fatal("expected error for empty targets")
	}
}
