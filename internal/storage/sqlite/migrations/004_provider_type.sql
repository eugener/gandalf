-- +goose Up
ALTER TABLE providers ADD COLUMN type TEXT NOT NULL DEFAULT '';
UPDATE providers SET type = name WHERE type = '';

-- +goose Down
ALTER TABLE providers DROP COLUMN type;
