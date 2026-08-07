-- +goose Up
-- +goose StatementBegin

-- Add schema_version column to paramsets so the daemon can wipe rows
-- written by an older version of the patch / normalisation logic.
-- Mirrors aiohomematic's BasePersistentCache.SCHEMA_VERSION (see
-- aiohomematic/store/persistent/paramset.py:50). Existing rows default
-- to 0 (= "unknown / pre-versioning"); the bootstrap-time wipe pass
-- removes them on first start, so the daemon refetches paramset
-- descriptions from the CCU.

ALTER TABLE paramsets ADD COLUMN schema_version INTEGER NOT NULL DEFAULT 0;

CREATE INDEX paramsets_by_schema_version ON paramsets(schema_version);

-- +goose StatementEnd

-- Down is destructive to the version marker, not to the paramset data
-- itself: every row reverts to schema_version 0 (unknown), which forces the
-- bootstrap wipe-and-refetch pass to re-fetch paramset descriptions from the
-- CCU on the next start.
-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS paramsets_by_schema_version;
ALTER TABLE paramsets DROP COLUMN schema_version;

-- +goose StatementEnd
