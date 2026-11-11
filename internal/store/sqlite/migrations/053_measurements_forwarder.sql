-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 SukramJ.
-- +goose Up

-- The daemon no longer stores measurements; it forwards them to an external
-- time-series store. Four things follow for this database.

-- 1. The per-data-point recording overrides move here from the dedicated
--    history database, which the daemon stops opening. A present row
--    overrides the parameter-name glob policy (north.measurements.include /
--    exclude) for one data-point instance; an absent row means "fall back to
--    the policy". The table is sparse: a row exists only where an operator
--    toggled forwarding for a data point away from its default.
--
--    Partitioned by central_name, so a device or central removal purges its
--    rows by the same key the forwarder tags a sample with.
CREATE TABLE measurement_recording_overrides (
    central_name    TEXT    NOT NULL,
    interface_id    TEXT    NOT NULL,
    channel_address TEXT    NOT NULL,
    parameter       TEXT    NOT NULL,
    record          INTEGER NOT NULL, -- 1 = force forward, 0 = force skip
    updated_by      TEXT    NOT NULL DEFAULT '',
    updated_at      TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (central_name, interface_id, channel_address, parameter)
) WITHOUT ROWID;

-- 2. upgrade_steps records one-time work the daemon does outside a
--    migration because it needs something a migration cannot reach — here
--    the history database file, whose overrides are copied once. Without the
--    record the copy would run on every start and resurrect an override the
--    operator has since cleared.
CREATE TABLE upgrade_steps (
    name    TEXT NOT NULL PRIMARY KEY,
    done_at TEXT NOT NULL
) WITHOUT ROWID;

-- 3. The named chart definitions of the removed diagrams view have no reader.
DROP TABLE IF EXISTS diagram_configs;

-- 4. The settings move from the `history` sub-tree of the stored
--    `persistence` section to a section of their own, north.measurements.
--    The old block had two switches, capture and export; forwarding is on
--    only where both were and a target was named, because capture without a
--    target forwards nothing. Retention and the energy tariff described the
--    removed store and are dropped.
--
--    json_patch drops every key whose value is NULL, so a setting the
--    operator never made stays absent instead of being stored as null. The
--    JSON functions are nested directly: an array taken out by json_extract
--    keeps its JSON type only as a direct argument.
--
--    Idempotent: the guards stop matching once the sub-tree is gone.
INSERT INTO config_sections (section, value_json, updated_by)
SELECT 'north.measurements',
       json_patch('{}', json_object(
           'enabled', CASE
               WHEN json_extract(value_json, '$.history.enabled') = 1
                AND json_extract(value_json, '$.history.export.enabled') = 1
                AND COALESCE(json_extract(value_json, '$.history.export.endpoint'), '') <> ''
               THEN json('true')
           END,
           'kind',              json_extract(value_json, '$.history.export.kind'),
           'endpoint',          json_extract(value_json, '$.history.export.endpoint'),
           'org',               json_extract(value_json, '$.history.export.org'),
           'bucket',            json_extract(value_json, '$.history.export.bucket'),
           'token_env',         json_extract(value_json, '$.history.export.token_env'),
           'flush_interval',    json_extract(value_json, '$.history.flush_interval'),
           'include',           json_extract(value_json, '$.history.include'),
           'exclude',           json_extract(value_json, '$.history.exclude'),
           'disabled_centrals', json_extract(value_json, '$.history.disabled_centrals')
       )),
       'migration'
  FROM config_sections
 WHERE section = 'persistence'
   AND json_valid(value_json)
   AND json_type(value_json, '$.history') = 'object'
   AND NOT EXISTS (SELECT 1 FROM config_sections WHERE section = 'north.measurements');

UPDATE config_sections
   SET value_json = CASE
         WHEN json_valid(value_json) THEN json_remove(value_json, '$.history')
         ELSE value_json
       END
 WHERE section = 'persistence'
   AND value_json LIKE '%"history"%';

-- Down restores neither the chart definitions nor the old settings block:
-- no daemon that this schema version serves reads either. It removes what Up
-- created.
-- +goose Down
DROP TABLE IF EXISTS upgrade_steps;
DROP TABLE IF EXISTS measurement_recording_overrides;
