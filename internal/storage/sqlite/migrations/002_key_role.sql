-- +goose Up
ALTER TABLE api_keys ADD COLUMN role TEXT NOT NULL DEFAULT 'member';

-- +goose Down
-- SQLite does not support DROP COLUMN prior to 3.35; recreating the table
-- is overkill for a down migration in dev. The column is harmless if kept.
