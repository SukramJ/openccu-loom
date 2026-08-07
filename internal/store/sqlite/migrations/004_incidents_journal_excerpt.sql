-- +goose Up
-- +goose StatementBegin

ALTER TABLE incidents ADD COLUMN journal_excerpt TEXT;

-- +goose StatementEnd

-- Down is destructive: every journal excerpt captured alongside a past
-- incident is deleted; that context cannot be regenerated once the source
-- ReGa journal entry has scrolled past it.
-- +goose Down
-- +goose StatementBegin

ALTER TABLE incidents DROP COLUMN journal_excerpt;

-- +goose StatementEnd
