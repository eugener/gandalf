package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	gateway "github.com/eugener/gandalf/internal"
)

// CreateKey inserts a new API key.
func (s *Store) CreateKey(ctx context.Context, key *gateway.APIKey) error {
	models, err := marshalJSON(key.AllowedModels)
	if err != nil {
		return err
	}
	role := key.Role
	if role == "" {
		role = "member"
	}
	_, err = s.write.ExecContext(ctx,
		`INSERT INTO api_keys (id, key_hash, key_prefix, user_id, team_id, org_id, role,
		 allowed_models, rpm_limit, tpm_limit, max_budget, expires_at, blocked, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		key.ID, key.KeyHash, key.KeyPrefix,
		nullStr(key.UserID), nullStr(key.TeamID), key.OrgID, role,
		models, key.RPMLimit, key.TPMLimit, key.MaxBudget,
		timeToStr(key.ExpiresAt), boolToInt(key.Blocked), key.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

// GetKeyByHash retrieves an API key by its SHA-256 hash.
func (s *Store) GetKeyByHash(ctx context.Context, hash string) (*gateway.APIKey, error) {
	row := s.read.QueryRowContext(ctx,
		`SELECT id, key_hash, key_prefix, user_id, team_id, org_id, role,
		 allowed_models, rpm_limit, tpm_limit, max_budget, expires_at, blocked,
		 last_used_at, created_at
		 FROM api_keys WHERE key_hash = ?`, hash,
	)
	return scanKey(row)
}

// ListKeys returns API keys for an organization.
func (s *Store) ListKeys(ctx context.Context, orgID string, offset, limit int) ([]*gateway.APIKey, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT id, key_hash, key_prefix, user_id, team_id, org_id, role,
		 allowed_models, rpm_limit, tpm_limit, max_budget, expires_at, blocked,
		 last_used_at, created_at
		 FROM api_keys WHERE org_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		orgID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*gateway.APIKey
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// UpdateKey updates an existing API key.
func (s *Store) UpdateKey(ctx context.Context, key *gateway.APIKey) error {
	models, err := marshalJSON(key.AllowedModels)
	if err != nil {
		return err
	}
	role := key.Role
	if role == "" {
		role = "member"
	}
	result, err := s.write.ExecContext(ctx,
		`UPDATE api_keys SET role=?, allowed_models=?, rpm_limit=?, tpm_limit=?, max_budget=?,
		 expires_at=?, blocked=? WHERE id=?`,
		role, models, key.RPMLimit, key.TPMLimit, key.MaxBudget,
		timeToStr(key.ExpiresAt), boolToInt(key.Blocked), key.ID,
	)
	if err != nil {
		return err
	}
	return checkRowsAffected(result, "api key")
}

// DeleteKey removes an API key.
func (s *Store) DeleteKey(ctx context.Context, id string) error {
	result, err := s.write.ExecContext(ctx, `DELETE FROM api_keys WHERE id=?`, id)
	if err != nil {
		return err
	}
	return checkRowsAffected(result, "api key")
}

// ListBudgetedKeyIDs returns a map of key ID to max_budget for keys with budgets > 0.
func (s *Store) ListBudgetedKeyIDs(ctx context.Context) (map[string]float64, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT id, max_budget FROM api_keys WHERE max_budget > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]float64)
	for rows.Next() {
		var id string
		var budget float64
		if err := rows.Scan(&id, &budget); err != nil {
			return nil, err
		}
		out[id] = budget
	}
	return out, rows.Err()
}

// TouchKeyUsed updates the last_used_at timestamp.
func (s *Store) TouchKeyUsed(ctx context.Context, id string) error {
	_, err := s.write.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at=? WHERE id=?`,
		time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// notFoundErr translates sql.ErrNoRows to gateway.ErrNotFound.
func notFoundErr(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return gateway.ErrNotFound
	}
	return err
}

// GetKey retrieves an API key by its ID.
func (s *Store) GetKey(ctx context.Context, id string) (*gateway.APIKey, error) {
	row := s.read.QueryRowContext(ctx,
		`SELECT id, key_hash, key_prefix, user_id, team_id, org_id, role,
		 allowed_models, rpm_limit, tpm_limit, max_budget, expires_at, blocked,
		 last_used_at, created_at
		 FROM api_keys WHERE id = ?`, id,
	)
	return scanKey(row)
}

// CountKeys returns the total number of API keys for an organization.
func (s *Store) CountKeys(ctx context.Context, orgID string) (int, error) {
	var n int
	err := s.read.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM api_keys WHERE org_id = ?`, orgID,
	).Scan(&n)
	return n, err
}

func scanKey(s scanner) (*gateway.APIKey, error) {
	var k gateway.APIKey
	var modelsJSON sql.NullString
	var userID, teamID sql.NullString
	var role sql.NullString
	var expiresAt, lastUsedAt, createdAt sql.NullString
	var blocked int

	err := s.Scan(
		&k.ID, &k.KeyHash, &k.KeyPrefix, &userID, &teamID, &k.OrgID, &role,
		&modelsJSON, &k.RPMLimit, &k.TPMLimit, &k.MaxBudget,
		&expiresAt, &blocked, &lastUsedAt, &createdAt,
	)
	if err != nil {
		return nil, notFoundErr(err)
	}

	k.Blocked = blocked != 0
	k.UserID = userID.String
	k.TeamID = teamID.String
	k.Role = role.String
	if k.Role == "" {
		k.Role = "member"
	}

	models, err := unmarshalStringSlice(modelsJSON)
	if err != nil {
		return nil, err
	}
	k.AllowedModels = models
	k.ExpiresAt = parseTime(expiresAt)
	k.LastUsedAt = parseTime(lastUsedAt)
	if t := parseTime(createdAt); t != nil {
		k.CreatedAt = *t
	}
	return &k, nil
}

// helpers

func marshalJSON(v any) (sql.NullString, error) {
	if v == nil {
		return sql.NullString{}, nil
	}
	// Check for empty slice
	if s, ok := v.([]string); ok && len(s) == 0 {
		return sql.NullString{}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

func unmarshalStringSlice(ns sql.NullString) ([]string, error) {
	if !ns.Valid {
		return nil, nil
	}
	var s []string
	if err := json.Unmarshal([]byte(ns.String), &s); err != nil {
		return nil, fmt.Errorf("unmarshal string slice: %w", err)
	}
	return s, nil
}

func timeToStr(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: t.UTC().Format(time.RFC3339), Valid: true}
}

func parseTime(ns sql.NullString) *time.Time {
	if !ns.Valid {
		return nil
	}
	t, err := time.Parse(time.RFC3339, ns.String)
	if err != nil {
		return nil
	}
	return &t
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func checkRowsAffected(result sql.Result, entity string) error {
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%s: %w", entity, gateway.ErrNotFound)
	}
	return nil
}
