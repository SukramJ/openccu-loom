-- Incident source ledger: every data point that contributed to an
-- incident, not just the one that opened it.
--
-- Before this table an incident carried a single cause document, and
-- the engine's trigger path returns early once a zone is already
-- triggered — so a second detector going off while the siren sounds
-- left no trace anywhere. That is precisely the information needed to
-- answer "which detectors fired?" after the fact.
--
-- No FK on incident_id: consistent with the other alarm tables, the
-- store cascades. The row is written asynchronously off the engine
-- goroutine, so a slow disk can never delay a siren.

-- +goose Up
CREATE TABLE alarm_incident_sources (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    incident_id     INTEGER NOT NULL,
    zone_id         TEXT    NOT NULL,
    ref             TEXT    NOT NULL,
    central_name    TEXT    NOT NULL,
    interface_id    TEXT    NOT NULL DEFAULT '',
    channel_address TEXT    NOT NULL DEFAULT '',
    device_address  TEXT    NOT NULL DEFAULT '',
    parameter       TEXT    NOT NULL DEFAULT '',
    sensor_id       TEXT    NOT NULL DEFAULT '',
    name            TEXT    NOT NULL DEFAULT '',
    sensor_type     TEXT    NOT NULL DEFAULT '',
    class           TEXT    NOT NULL DEFAULT '',
    cause           TEXT    NOT NULL DEFAULT '',
    at_ms           INTEGER NOT NULL
);

-- One row per (incident, source): a detector that re-activates during
-- the same incident must not inflate the list. The engine relies on
-- this to make its append idempotent.
CREATE UNIQUE INDEX alarm_incident_sources_unique
    ON alarm_incident_sources(incident_id, ref);

-- Retention purges by incident; the detail view reads by incident.
CREATE INDEX alarm_incident_sources_by_incident
    ON alarm_incident_sources(incident_id, at_ms);

-- Down is destructive: the per-incident source ledger is deleted. The
-- record of every detector that contributed to a past incident, beyond the
-- one that opened it, is gone with no other source to rebuild it from.
-- +goose Down
DROP INDEX alarm_incident_sources_by_incident;
DROP INDEX alarm_incident_sources_unique;
DROP TABLE alarm_incident_sources;
