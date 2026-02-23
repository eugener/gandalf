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
