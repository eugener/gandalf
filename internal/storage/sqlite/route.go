package sqlite

import (
	"context"

	gateway "github.com/eugener/gandalf/internal"
)

// CreateRoute inserts a new route.
func (s *Store) CreateRoute(ctx context.Context, r *gateway.Route) error {
	_, err := s.write.ExecContext(ctx,
		`INSERT INTO routes (id, model_alias, targets, strategy, cache_ttl_s)
		 VALUES (?, ?, ?, ?, ?)`,
		r.ID, r.ModelAlias, string(r.Targets), r.Strategy, r.CacheTTLs,
	)
	return err
}

// GetRoute retrieves a route by its ID.
func (s *Store) GetRoute(ctx context.Context, id string) (*gateway.Route, error) {
	row := s.read.QueryRowContext(ctx,
		`SELECT id, model_alias, targets, strategy, cache_ttl_s
		 FROM routes WHERE id=?`, id,
	)
	return scanRoute(row)
}

// CountRoutes returns the total number of routes.
func (s *Store) CountRoutes(ctx context.Context) (int, error) {
	var n int
	err := s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM routes`).Scan(&n)
	return n, err
}

// CountProviders returns the total number of providers.
func (s *Store) CountProviders(ctx context.Context) (int, error) {
	var n int
	err := s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM providers`).Scan(&n)
	return n, err
}

// GetRouteByAlias retrieves a route by model alias.
func (s *Store) GetRouteByAlias(ctx context.Context, alias string) (*gateway.Route, error) {
	row := s.read.QueryRowContext(ctx,
		`SELECT id, model_alias, targets, strategy, cache_ttl_s
		 FROM routes WHERE model_alias=?`, alias,
	)
	return scanRoute(row)
}

// ListRoutes returns all routes.
func (s *Store) ListRoutes(ctx context.Context) ([]*gateway.Route, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT id, model_alias, targets, strategy, cache_ttl_s FROM routes ORDER BY model_alias`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var routes []*gateway.Route
	for rows.Next() {
		r, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		routes = append(routes, r)
	}
	return routes, rows.Err()
}

// UpdateRoute updates an existing route.
func (s *Store) UpdateRoute(ctx context.Context, r *gateway.Route) error {
	result, err := s.write.ExecContext(ctx,
		`UPDATE routes SET model_alias=?, targets=?, strategy=?, cache_ttl_s=? WHERE id=?`,
		r.ModelAlias, string(r.Targets), r.Strategy, r.CacheTTLs, r.ID,
	)
	if err != nil {
		return err
	}
	return checkRowsAffected(result, "route")
}

// DeleteRoute removes a route.
func (s *Store) DeleteRoute(ctx context.Context, id string) error {
	result, err := s.write.ExecContext(ctx, `DELETE FROM routes WHERE id=?`, id)
	if err != nil {
		return err
	}
	return checkRowsAffected(result, "route")
}

func scanRoute(s scanner) (*gateway.Route, error) {
	var r gateway.Route
	var targets string
	err := s.Scan(&r.ID, &r.ModelAlias, &targets, &r.Strategy, &r.CacheTTLs)
	if err != nil {
		return nil, notFoundErr(err)
	}
	r.Targets = []byte(targets)
	return &r, nil
}
