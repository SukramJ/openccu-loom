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

-- Down is destructive well beyond the column it drops. The next Up re-adds
-- schema_version with the same DEFAULT 0 this migration originally chose for
-- pre-versioning rows, so every section that survived the round trip comes
-- back at version 0. ConfigSectionStore.WipeOutdatedSections then deletes
-- every row whose schema_version differs from the current
-- ConfigSectionSchemaVersion on the very next boot — for an installation with
-- no rows written before this migration existed, that is every configured
-- section, i.e. a Down→Up cycle empties the entire config_sections table.
-- The DEFAULT 0 cannot be changed to avoid this: it is exactly what makes
-- WipeOutdatedSections do its real job on a genuine upgrade from a
-- pre-versioning database, wiping rows written before schema_version existed
-- because their serialisation format cannot be trusted. There is no value
-- that is simultaneously "known-stale" for a real upgrade and
-- "known-current" for a down/up round trip.
-- +goose Down
-- +goose StatementBegin
ALTER TABLE config_sections DROP COLUMN schema_version;
-- +goose StatementEnd
