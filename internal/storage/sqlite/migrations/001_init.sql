-- +goose Up
CREATE TABLE IF NOT EXISTS organizations (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    allowed_models TEXT, -- JSON array
    rpm_limit     INTEGER,
    tpm_limit     INTEGER,
    max_budget    REAL,
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS teams (
    id            TEXT PRIMARY KEY,
    org_id        TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    allowed_models TEXT, -- JSON array
    rpm_limit     INTEGER,
    tpm_limit     INTEGER,
    max_budget    REAL
);

CREATE TABLE IF NOT EXISTS providers (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    base_url    TEXT NOT NULL,
    api_key_enc TEXT NOT NULL,
    models      TEXT, -- JSON array
    priority    INTEGER NOT NULL DEFAULT 0,
    weight      INTEGER NOT NULL DEFAULT 1,
    enabled     INTEGER NOT NULL DEFAULT 1,
    max_rps     INTEGER NOT NULL DEFAULT 0,
    timeout_ms  INTEGER NOT NULL DEFAULT 30000
);

CREATE TABLE IF NOT EXISTS api_keys (
    id            TEXT PRIMARY KEY,
    key_hash      TEXT NOT NULL UNIQUE,
    key_prefix    TEXT NOT NULL,
    user_id       TEXT,
    team_id       TEXT REFERENCES teams(id) ON DELETE SET NULL,
    org_id        TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    allowed_models TEXT, -- JSON array
    rpm_limit     INTEGER,
    tpm_limit     INTEGER,
    max_budget    REAL,
    expires_at    TEXT,
    blocked       INTEGER NOT NULL DEFAULT 0,
    last_used_at  TEXT,
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(key_hash);
CREATE INDEX IF NOT EXISTS idx_api_keys_org ON api_keys(org_id);

CREATE TABLE IF NOT EXISTS routes (
    id          TEXT PRIMARY KEY,
    model_alias TEXT NOT NULL UNIQUE,
    targets     TEXT NOT NULL, -- JSON array
    strategy    TEXT NOT NULL DEFAULT 'priority',
    cache_ttl_s INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_routes_alias ON routes(model_alias);

CREATE TABLE IF NOT EXISTS usage_records (
    id                TEXT PRIMARY KEY,
    key_id            TEXT,
    user_id           TEXT,
    team_id           TEXT,
    org_id            TEXT,
    caller_jwt_sub    TEXT,
    caller_service    TEXT,
    model             TEXT,
    provider_id       TEXT,
    prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens      INTEGER NOT NULL DEFAULT 0,
    cost_usd          REAL NOT NULL DEFAULT 0,
    cached            INTEGER NOT NULL DEFAULT 0,
    latency_ms        INTEGER NOT NULL DEFAULT 0,
    status_code       INTEGER NOT NULL DEFAULT 0,
    request_id        TEXT,
    created_at        TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_usage_key_created ON usage_records(key_id, created_at);
CREATE INDEX IF NOT EXISTS idx_usage_org_created ON usage_records(org_id, created_at);

-- Insert default organization
INSERT OR IGNORE INTO organizations (id, name) VALUES ('default', 'Default Organization');

-- +goose Down
DROP TABLE IF EXISTS usage_records;
DROP TABLE IF EXISTS routes;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS providers;
DROP TABLE IF EXISTS teams;
DROP TABLE IF EXISTS organizations;
