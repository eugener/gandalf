package app

import (
	"context"
	"encoding/json"
	"fmt"

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

// ResolveModel maps a model alias to a provider name and actual model ID.
// If no route is found, the alias is treated as a direct OpenAI model name.
func (rs *RouterService) ResolveModel(ctx context.Context, model string) (providerName, actualModel string, err error) {
	route, err := rs.routeStore.GetRouteByAlias(ctx, model)
	if err != nil {
		// No route -- treat as direct pass-through.
		return "openai", model, nil //nolint:nilerr
	}

	var targets []gateway.RouteTarget
	if err := json.Unmarshal(route.Targets, &targets); err != nil {
		return "", "", fmt.Errorf("parse route targets: %w", err)
	}
	if len(targets) == 0 {
		return "", "", fmt.Errorf("route %q has no targets", model)
	}

	return targets[0].ProviderID, targets[0].Model, nil
}
