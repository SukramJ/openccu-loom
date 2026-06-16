-- +goose Up
-- +goose StatementBegin

ALTER TABLE incidents ADD COLUMN journal_excerpt TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE incidents DROP COLUMN journal_excerpt;

-- +goose StatementEnd
