package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/maypok86/otter/v2"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/storage"
)

// RouterService resolves model aliases to concrete provider/model pairs
// using the route store. Resolved targets are cached to avoid repeated
// JSON unmarshalling on the hot path.
type RouterService struct {
	routeStore storage.RouteStore
	cache      *otter.Cache[string, []ResolvedTarget]
}

// NewRouterService returns a RouterService backed by the given route store.
func NewRouterService(routes storage.RouteStore) *RouterService {
	cache := otter.Must(&otter.Options[string, []ResolvedTarget]{
		MaximumSize:      256,
		ExpiryCalculator: otter.ExpiryWriting[string, []ResolvedTarget](routeCacheTTL),
	})
	return &RouterService{routeStore: routes, cache: cache}
}

// routeCacheTTL is how long resolved targets stay cached before re-reading
// from the store. Short enough to pick up config changes quickly, long enough
// to eliminate per-request JSON parsing.
const routeCacheTTL = 10 * time.Second

// ResolvedTarget is a provider/model pair with a priority for failover ordering.
type ResolvedTarget struct {
	ProviderID string
	Model      string
	Priority   int
}

// ResolveModel maps a model alias to an ordered list of targets sorted by
// priority (ascending). If no route is found, a single target defaulting to
// "openai" with the original model name is returned. Results are cached to
// avoid per-request JSON parsing.
func (rs *RouterService) ResolveModel(ctx context.Context, model string) ([]ResolvedTarget, error) {
	if cached, ok := rs.cache.GetIfPresent(model); ok {
		return cached, nil
	}

	route, err := rs.routeStore.GetRouteByAlias(ctx, model)
	if err != nil {
		// Wrap with %w to preserve original error (e.g. ErrNotFound) for callers.
		return nil, fmt.Errorf("resolve model %q: %w", model, err)
	}

	var targets []gateway.RouteTarget
	if err := json.Unmarshal(route.Targets, &targets); err != nil {
		return nil, fmt.Errorf("parse route targets: %w", err)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("route %q has no targets", model)
	}

	resolved := make([]ResolvedTarget, len(targets))
	for i, t := range targets {
		resolved[i] = ResolvedTarget{
			ProviderID: t.ProviderID,
			Model:      t.Model,
			Priority:   t.Priority,
		}
	}

	// Sort by priority ascending (lower priority number = higher precedence).
	slices.SortStableFunc(resolved, func(a, b ResolvedTarget) int {
		return a.Priority - b.Priority
	})

	rs.cache.Set(model, resolved)
	return resolved, nil
}

// CacheTTL returns the route-configured cache TTL for a model alias,
// or 0 if no route or no TTL is configured.
func (rs *RouterService) CacheTTL(ctx context.Context, model string) time.Duration {
	route, err := rs.routeStore.GetRouteByAlias(ctx, model)
	if err != nil || route.CacheTTLs <= 0 {
		return 0
	}
	return time.Duration(route.CacheTTLs) * time.Second
}
