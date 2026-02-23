// Package storage defines persistence interfaces for the gateway.
package storage

import (
	"context"

	gateway "github.com/eugener/gandalf/internal"
)

// APIKeyStore manages API key persistence.
type APIKeyStore interface {
	CreateKey(ctx context.Context, key *gateway.APIKey) error
	GetKeyByHash(ctx context.Context, hash string) (*gateway.APIKey, error)
	ListKeys(ctx context.Context, orgID string, offset, limit int) ([]*gateway.APIKey, error)
	UpdateKey(ctx context.Context, key *gateway.APIKey) error
	DeleteKey(ctx context.Context, id string) error
	TouchKeyUsed(ctx context.Context, id string) error
}

// ProviderStore manages provider configuration persistence.
type ProviderStore interface {
	CreateProvider(ctx context.Context, p *gateway.ProviderConfig) error
	GetProvider(ctx context.Context, id string) (*gateway.ProviderConfig, error)
	ListProviders(ctx context.Context) ([]*gateway.ProviderConfig, error)
	UpdateProvider(ctx context.Context, p *gateway.ProviderConfig) error
	DeleteProvider(ctx context.Context, id string) error
}

// RouteStore manages route persistence.
type RouteStore interface {
	CreateRoute(ctx context.Context, r *gateway.Route) error
	GetRouteByAlias(ctx context.Context, alias string) (*gateway.Route, error)
	ListRoutes(ctx context.Context) ([]*gateway.Route, error)
	UpdateRoute(ctx context.Context, r *gateway.Route) error
	DeleteRoute(ctx context.Context, id string) error
}

// UsageStore manages usage record persistence.
type UsageStore interface {
	InsertUsage(ctx context.Context, records []gateway.UsageRecord) error
	SumUsageCost(ctx context.Context, keyID string) (float64, error)
}

// OrgStore manages organization and team persistence.
type OrgStore interface {
	CreateOrg(ctx context.Context, org *gateway.Organization) error
	GetOrg(ctx context.Context, id string) (*gateway.Organization, error)
	ListOrgs(ctx context.Context, offset, limit int) ([]*gateway.Organization, error)
	UpdateOrg(ctx context.Context, org *gateway.Organization) error
	DeleteOrg(ctx context.Context, id string) error
	CreateTeam(ctx context.Context, team *gateway.Team) error
	GetTeam(ctx context.Context, id string) (*gateway.Team, error)
	ListTeams(ctx context.Context, orgID string, offset, limit int) ([]*gateway.Team, error)
	UpdateTeam(ctx context.Context, team *gateway.Team) error
	DeleteTeam(ctx context.Context, id string) error
}

// Store combines all storage interfaces.
type Store interface {
	APIKeyStore
	ProviderStore
	RouteStore
	UsageStore
	OrgStore
	Close() error
}
