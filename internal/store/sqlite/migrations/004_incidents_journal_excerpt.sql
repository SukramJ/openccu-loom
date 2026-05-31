-- +goose Up
-- +goose StatementBegin

ALTER TABLE incidents ADD COLUMN journal_excerpt TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- SQLite does not support DROP COLUMN in all versions; mark as no-op for down.
-- The column stays but is ignored by older binaries.
SELECT 1;

-- +goose StatementEnd
