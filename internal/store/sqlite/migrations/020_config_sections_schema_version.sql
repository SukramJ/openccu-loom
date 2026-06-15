-- +goose Up
-- +goose StatementBegin

-- Add schema_version to config_sections so stale rows written by an
-- older serialisation pipeline can be detected and wiped on boot.
-- Existing rows default to 0 (pre-versioning); the daemon's
-- WipeOutdatedSections helper removes them on first start with a
-- schema_version mismatch, falling back to defaults so the operator
-- re-saves via the SPA.
ALTER TABLE config_sections ADD COLUMN schema_version INTEGER NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE config_sections DROP COLUMN schema_version;
-- +goose StatementEnd
