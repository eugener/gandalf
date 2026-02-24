-- +goose Up
CREATE TABLE IF NOT EXISTS usage_rollups (
    org_id            TEXT NOT NULL,
    key_id            TEXT NOT NULL,
    model             TEXT NOT NULL,
    period            TEXT NOT NULL,  -- 'hourly', 'daily'
    bucket            TEXT NOT NULL,  -- ISO 8601 timestamp of bucket start
    request_count     INTEGER NOT NULL DEFAULT 0,
    prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens      INTEGER NOT NULL DEFAULT 0,
    cost_usd          REAL NOT NULL DEFAULT 0,
    cached_count      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (org_id, key_id, model, period, bucket)
);

CREATE INDEX IF NOT EXISTS idx_rollups_org_bucket ON usage_rollups(org_id, bucket);
CREATE INDEX IF NOT EXISTS idx_rollups_key_bucket ON usage_rollups(key_id, bucket);

-- +goose Down
DROP TABLE IF EXISTS usage_rollups;
