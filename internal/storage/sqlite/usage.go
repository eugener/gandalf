package sqlite

import (
	"context"
	"strings"
	"time"

	gateway "github.com/eugener/gandalf/internal"
)

// InsertUsage batch-inserts usage records.
func (s *Store) InsertUsage(ctx context.Context, records []gateway.UsageRecord) error {
	if len(records) == 0 {
		return nil
	}

	// cols must match the number of columns in the INSERT below.
	// Single multi-row INSERT avoids N round-trips for large batches.
	const cols = 18
	placeholders := make([]string, len(records))
	args := make([]any, 0, len(records)*cols)

	for i, r := range records {
		placeholders[i] = "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
		args = append(args,
			r.ID, r.KeyID, r.UserID, r.TeamID, r.OrgID,
			r.CallerJWTSub, r.CallerService,
			r.Model, r.ProviderID,
			r.PromptTokens, r.CompletionTokens, r.TotalTokens, r.CostUSD,
			boolToInt(r.Cached), r.LatencyMs, r.StatusCode,
			r.RequestID, r.CreatedAt.UTC().Format(time.RFC3339),
		)
	}

	query := `INSERT INTO usage_records
		(id, key_id, user_id, team_id, org_id, caller_jwt_sub, caller_service,
		 model, provider_id, prompt_tokens, completion_tokens, total_tokens, cost_usd,
		 cached, latency_ms, status_code, request_id, created_at)
		VALUES ` + strings.Join(placeholders, ", ")

	_, err := s.write.ExecContext(ctx, query, args...)
	return err
}

// SumUsageCost returns the total accumulated cost for a given API key.
func (s *Store) SumUsageCost(ctx context.Context, keyID string) (float64, error) {
	var total float64
	err := s.read.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(cost_usd), 0) FROM usage_records WHERE key_id = ?`, keyID,
	).Scan(&total)
	return total, err
}

// QueryUsage returns usage records matching the filter.
func (s *Store) QueryUsage(ctx context.Context, f gateway.UsageFilter) ([]gateway.UsageRecord, error) {
	where, args := usageWhere(f)
	query := `SELECT id, key_id, user_id, team_id, org_id, caller_jwt_sub, caller_service,
		model, provider_id, prompt_tokens, completion_tokens, total_tokens, cost_usd,
		cached, latency_ms, status_code, request_id, created_at
		FROM usage_records` + where + ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit, f.Offset)

	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []gateway.UsageRecord
	for rows.Next() {
		var r gateway.UsageRecord
		var cached int
		var createdAt string
		err := rows.Scan(
			&r.ID, &r.KeyID, &r.UserID, &r.TeamID, &r.OrgID,
			&r.CallerJWTSub, &r.CallerService,
			&r.Model, &r.ProviderID,
			&r.PromptTokens, &r.CompletionTokens, &r.TotalTokens, &r.CostUSD,
			&cached, &r.LatencyMs, &r.StatusCode,
			&r.RequestID, &createdAt,
		)
		if err != nil {
			return nil, err
		}
		r.Cached = cached != 0
		if t, e := time.Parse(time.RFC3339, createdAt); e == nil {
			r.CreatedAt = t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountUsage returns the count of usage records matching the filter.
func (s *Store) CountUsage(ctx context.Context, f gateway.UsageFilter) (int, error) {
	where, args := usageWhere(f)
	var n int
	err := s.read.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM usage_records`+where, args...,
	).Scan(&n)
	return n, err
}

func usageWhere(f gateway.UsageFilter) (string, []any) {
	var clauses []string
	var args []any
	if f.OrgID != "" {
		clauses = append(clauses, "org_id = ?")
		args = append(args, f.OrgID)
	}
	if f.KeyID != "" {
		clauses = append(clauses, "key_id = ?")
		args = append(args, f.KeyID)
	}
	if f.Model != "" {
		clauses = append(clauses, "model = ?")
		args = append(args, f.Model)
	}
	if f.Since != "" {
		clauses = append(clauses, "created_at >= ?")
		args = append(args, f.Since)
	}
	if f.Until != "" {
		clauses = append(clauses, "created_at < ?")
		args = append(args, f.Until)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// UpsertRollup inserts or updates usage rollup records in a single transaction
// with a prepared statement for efficiency.
func (s *Store) UpsertRollup(ctx context.Context, rollups []gateway.UsageRollup) error {
	if len(rollups) == 0 {
		return nil
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO usage_rollups (org_id, key_id, model, period, bucket,
		 request_count, prompt_tokens, completion_tokens, total_tokens, cost_usd, cached_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(org_id, key_id, model, period, bucket) DO UPDATE SET
		 request_count = request_count + excluded.request_count,
		 prompt_tokens = prompt_tokens + excluded.prompt_tokens,
		 completion_tokens = completion_tokens + excluded.completion_tokens,
		 total_tokens = total_tokens + excluded.total_tokens,
		 cost_usd = cost_usd + excluded.cost_usd,
		 cached_count = cached_count + excluded.cached_count`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range rollups {
		if _, err := stmt.ExecContext(ctx,
			r.OrgID, r.KeyID, r.Model, r.Period, r.Bucket,
			r.RequestCount, r.PromptTokens, r.CompletionTokens, r.TotalTokens, r.CostUSD, r.CachedCount,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// QueryRollups returns rollups matching the filter.
func (s *Store) QueryRollups(ctx context.Context, f gateway.RollupFilter) ([]gateway.UsageRollup, error) {
	var clauses []string
	var args []any
	if f.OrgID != "" {
		clauses = append(clauses, "org_id = ?")
		args = append(args, f.OrgID)
	}
	if f.KeyID != "" {
		clauses = append(clauses, "key_id = ?")
		args = append(args, f.KeyID)
	}
	if f.Model != "" {
		clauses = append(clauses, "model = ?")
		args = append(args, f.Model)
	}
	if f.Period != "" {
		clauses = append(clauses, "period = ?")
		args = append(args, f.Period)
	}
	if f.Since != "" {
		clauses = append(clauses, "bucket >= ?")
		args = append(args, f.Since)
	}
	if f.Until != "" {
		clauses = append(clauses, "bucket < ?")
		args = append(args, f.Until)
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}

	rows, err := s.read.QueryContext(ctx,
		`SELECT org_id, key_id, model, period, bucket,
		 request_count, prompt_tokens, completion_tokens, total_tokens, cost_usd, cached_count
		 FROM usage_rollups`+where+` ORDER BY bucket DESC`, args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []gateway.UsageRollup
	for rows.Next() {
		var r gateway.UsageRollup
		err := rows.Scan(&r.OrgID, &r.KeyID, &r.Model, &r.Period, &r.Bucket,
			&r.RequestCount, &r.PromptTokens, &r.CompletionTokens, &r.TotalTokens, &r.CostUSD, &r.CachedCount)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
