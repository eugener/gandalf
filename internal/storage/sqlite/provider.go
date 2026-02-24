package sqlite

import (
	"context"
	"database/sql"

	gateway "github.com/eugener/gandalf/internal"
)

// CreateProvider inserts a new provider configuration.
func (s *Store) CreateProvider(ctx context.Context, p *gateway.ProviderConfig) error {
	models, err := marshalJSON(p.Models)
	if err != nil {
		return err
	}
	_, err = s.write.ExecContext(ctx,
		`INSERT INTO providers (id, name, type, base_url, api_key_enc, models, priority, weight, enabled, max_rps, timeout_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Type, p.BaseURL, p.APIKeyEnc, models,
		p.Priority, p.Weight, boolToInt(p.Enabled), p.MaxRPS, p.TimeoutMs,
	)
	return err
}

// GetProvider retrieves a provider by ID.
func (s *Store) GetProvider(ctx context.Context, id string) (*gateway.ProviderConfig, error) {
	row := s.read.QueryRowContext(ctx,
		`SELECT id, name, type, base_url, api_key_enc, models, priority, weight, enabled, max_rps, timeout_ms
		 FROM providers WHERE id=?`, id,
	)
	return scanProvider(row)
}

// ListProviders returns all provider configurations.
func (s *Store) ListProviders(ctx context.Context) ([]*gateway.ProviderConfig, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT id, name, type, base_url, api_key_enc, models, priority, weight, enabled, max_rps, timeout_ms
		 FROM providers ORDER BY priority ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var providers []*gateway.ProviderConfig
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	return providers, rows.Err()
}

// UpdateProvider updates a provider configuration.
func (s *Store) UpdateProvider(ctx context.Context, p *gateway.ProviderConfig) error {
	models, err := marshalJSON(p.Models)
	if err != nil {
		return err
	}
	result, err := s.write.ExecContext(ctx,
		`UPDATE providers SET name=?, type=?, base_url=?, api_key_enc=?, models=?,
		 priority=?, weight=?, enabled=?, max_rps=?, timeout_ms=? WHERE id=?`,
		p.Name, p.Type, p.BaseURL, p.APIKeyEnc, models,
		p.Priority, p.Weight, boolToInt(p.Enabled), p.MaxRPS, p.TimeoutMs, p.ID,
	)
	if err != nil {
		return err
	}
	return checkRowsAffected(result, "provider")
}

// DeleteProvider removes a provider configuration.
func (s *Store) DeleteProvider(ctx context.Context, id string) error {
	result, err := s.write.ExecContext(ctx, `DELETE FROM providers WHERE id=?`, id)
	if err != nil {
		return err
	}
	return checkRowsAffected(result, "provider")
}

func scanProvider(s scanner) (*gateway.ProviderConfig, error) {
	var p gateway.ProviderConfig
	var modelsJSON sql.NullString
	var enabled int

	err := s.Scan(
		&p.ID, &p.Name, &p.Type, &p.BaseURL, &p.APIKeyEnc, &modelsJSON,
		&p.Priority, &p.Weight, &enabled, &p.MaxRPS, &p.TimeoutMs,
	)
	if err != nil {
		return nil, notFoundErr(err)
	}

	p.Enabled = enabled != 0
	models, err := unmarshalStringSlice(modelsJSON)
	if err != nil {
		return nil, err
	}
	p.Models = models
	return &p, nil
}
