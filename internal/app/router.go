package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/storage"
)

// RouterService resolves model aliases to concrete provider/model pairs
// using the route store.
type RouterService struct {
	routeStore storage.RouteStore
}

// NewRouterService returns a RouterService backed by the given route store.
func NewRouterService(routes storage.RouteStore) *RouterService {
	return &RouterService{routeStore: routes}
}

// ResolvedTarget is a provider/model pair with a priority for failover ordering.
type ResolvedTarget struct {
	ProviderID string
	Model      string
	Priority   int
}

// ResolveModel maps a model alias to an ordered list of targets sorted by
// priority (ascending). If no route is found, a single target defaulting to
// "openai" with the original model name is returned.
func (rs *RouterService) ResolveModel(ctx context.Context, model string) ([]ResolvedTarget, error) {
	route, err := rs.routeStore.GetRouteByAlias(ctx, model)
	if err != nil {
		// No configured route -- fall through to direct pass-through.
		return []ResolvedTarget{{ProviderID: "openai", Model: model, Priority: 1}}, nil //nolint:nilerr
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

	return resolved, nil
}
