-- +goose Up
-- +goose StatementBegin

-- visibility_unignore stores per-central un-ignore patterns that promote
-- otherwise-hidden parameters into the visible data-point surface. The
-- pattern format follows the aiohomematic convention
-- MODEL:CHANNEL:PARAMETER, with `*` wildcards for MODEL and CHANNEL.
--
-- Partitioning by central_name follows ADR 0002 (multi-CCU first-class):
-- two centrals wired into the same daemon hold independent un-ignore
-- lists, so a parameter promoted on CCU A stays hidden on CCU B unless
-- explicitly promoted there too.
--
-- This table is the runtime source of truth. config.yaml seeds the list
-- once (when the table is empty for a given central_name); subsequent
-- writes via PUT /api/v1/visibility/unignore overwrite the rows for that
-- central and the YAML seed no longer applies.
CREATE TABLE visibility_unignore (
    central_name TEXT    NOT NULL,
    pattern      TEXT    NOT NULL,
    updated_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_by   TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (central_name, pattern)
);

CREATE INDEX idx_visibility_unignore_central
    ON visibility_unignore (central_name);

-- +goose StatementEnd

-- Down is destructive: every operator-added un-ignore pattern is deleted.
-- Parameters the operator explicitly promoted into visibility silently
-- revert to the config.yaml seed or the built-in ignore list.
-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_visibility_unignore_central;
DROP TABLE IF EXISTS visibility_unignore;

-- +goose StatementEnd
