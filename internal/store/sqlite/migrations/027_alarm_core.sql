-- +goose Up
-- +goose StatementBegin

-- Alarm-engine core tables (docs/alarm-concept.md §14). Relational alarm
-- data (areas/sensors/outputs) is first-class domain data managed via
-- REST/UI, not config-file material. Timestamps across all alarm tables
-- are integer unix epoch milliseconds ("_ms" suffix) — the integer-epoch
-- convention is deliberate for timer/deadline arithmetic across restarts.

-- alarm_areas: one row per independently armable partition. The bounded
-- per-mode configuration document (delays, output policy, post-trigger
-- policy, central-loss policy, blocker policies) lives in config_json:
-- it is always loaded and saved as a whole and never queried relationally.
CREATE TABLE alarm_areas (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    position      INTEGER NOT NULL DEFAULT 0,
    config_json   TEXT NOT NULL DEFAULT '{}',
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL
);

-- alarm_sensors: one row per enrolled binary trigger source. The data
-- point identity is stored as discrete columns (central_name +
-- DataPointKey components) so enrolment lookups by data point stay
-- indexable; the mode matrix and behaviour flags live in config_json.
CREATE TABLE alarm_sensors (
    id              TEXT PRIMARY KEY,
    area_id         TEXT NOT NULL,
    central_name    TEXT NOT NULL,
    interface_id    TEXT NOT NULL,
    channel_address TEXT NOT NULL,
    parameter       TEXT NOT NULL,
    sensor_type     TEXT NOT NULL,
    name            TEXT NOT NULL DEFAULT '',
    config_json     TEXT NOT NULL DEFAULT '{}',
    created_at_ms   INTEGER NOT NULL,
    updated_at_ms   INTEGER NOT NULL
);

CREATE INDEX alarm_sensors_by_area ON alarm_sensors(area_id);
CREATE INDEX alarm_sensors_by_datapoint
    ON alarm_sensors(central_name, interface_id, channel_address, parameter);

-- alarm_outputs: one row per enrolled alarm consequence. class is the
-- user-declared output class (acoustic_siren, switched_siren,
-- smoke_sounder, optical_siren, alarm_light, chirp, notification,
-- sysvar_mirror) — the class, not the device type, decides which safety
-- invariants apply. Notification targets carry no data-point identity,
-- hence the empty-string defaults. Durations, tones, indoor/outdoor and
-- per-mode assignment live in config_json.
CREATE TABLE alarm_outputs (
    id              TEXT PRIMARY KEY,
    area_id         TEXT NOT NULL,
    class           TEXT NOT NULL,
    central_name    TEXT NOT NULL DEFAULT '',
    interface_id    TEXT NOT NULL DEFAULT '',
    channel_address TEXT NOT NULL DEFAULT '',
    name            TEXT NOT NULL DEFAULT '',
    config_json     TEXT NOT NULL DEFAULT '{}',
    created_at_ms   INTEGER NOT NULL,
    updated_at_ms   INTEGER NOT NULL
);

CREATE INDEX alarm_outputs_by_area ON alarm_outputs(area_id);

-- alarm_state: the continuously persisted arm-state of one area (one
-- row per area, written through on every transition). timers_json holds
-- the redundant timer tuples {kind, deadline_ms, remaining_ms,
-- persisted_at_ms, boot_count} that let restarts restore or expire
-- countdowns deterministically and detect implausible clocks.
-- context_json holds per-sensor runtime markers a restore must not
-- lose (e.g. which sensors were open at arm completion, the pending
-- cause sensor). incident_id references the active incident (0 = none).
CREATE TABLE alarm_state (
    area_id       TEXT PRIMARY KEY,
    state         TEXT NOT NULL,
    mode          TEXT NOT NULL,
    bypass_json   TEXT NOT NULL DEFAULT '[]',
    incident_id   INTEGER NOT NULL DEFAULT 0,
    timers_json   TEXT NOT NULL DEFAULT '[]',
    context_json  TEXT NOT NULL DEFAULT '{}',
    updated_at_ms INTEGER NOT NULL
);

-- alarm_incidents: one row per trigger episode. This table carries the
-- safety-critical persisted counters: the silenced flag (silence is
-- incident-scoped and survives restarts), the re-trigger cycle counter,
-- the cumulative acoustic-seconds ledger (stored in ms), and the
-- restore-driven re-fire counter feeding the restart-loop breaker —
-- crash/restart loops must not sum bounded activations into an
-- unbounded one. closed_at_ms = 0 marks the open incident of an area.
CREATE TABLE alarm_incidents (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    area_id             TEXT NOT NULL,
    mode                TEXT NOT NULL DEFAULT '',
    cause_json          TEXT NOT NULL DEFAULT '{}',
    started_at_ms       INTEGER NOT NULL,
    trigger_deadline_ms INTEGER NOT NULL DEFAULT 0,
    silenced            INTEGER NOT NULL DEFAULT 0,
    silenced_at_ms      INTEGER NOT NULL DEFAULT 0,
    silenced_by         TEXT NOT NULL DEFAULT '',
    retrigger_cycles    INTEGER NOT NULL DEFAULT 0,
    acoustic_ms         INTEGER NOT NULL DEFAULT 0,
    restore_refires     INTEGER NOT NULL DEFAULT 0,
    closed_at_ms        INTEGER NOT NULL DEFAULT 0,
    close_reason        TEXT NOT NULL DEFAULT ''
);

CREATE INDEX alarm_incidents_by_area ON alarm_incidents(area_id, closed_at_ms);

-- alarm_journal: the persistent, filterable event log of everything the
-- alarm engine does or observes. Append-only from the engine's
-- perspective; deletion happens only through the privileged retention
-- path. hidden rows exist for duress events, which stay out of
-- user-visible surfaces until resolved but are fully retained.
CREATE TABLE alarm_journal (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    ts_ms        INTEGER NOT NULL,
    area_id      TEXT NOT NULL DEFAULT '',
    class        TEXT NOT NULL,
    event        TEXT NOT NULL,
    actor        TEXT NOT NULL DEFAULT '',
    source       TEXT NOT NULL DEFAULT '',
    incident_id  INTEGER NOT NULL DEFAULT 0,
    hidden       INTEGER NOT NULL DEFAULT 0,
    details_json TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX alarm_journal_by_area_ts ON alarm_journal(area_id, ts_ms);
CREATE INDEX alarm_journal_by_ts ON alarm_journal(ts_ms);
CREATE INDEX alarm_journal_by_class_ts ON alarm_journal(class, ts_ms);

-- alarm_runtime: singleton row holding the engine boot counter. The
-- counter is incremented once per engine start; persisted timer tuples
-- reference it so a restore can tell same-boot re-reads from genuine
-- restarts (restart-loop breaker, clock plausibility).
CREATE TABLE alarm_runtime (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    boot_count    INTEGER NOT NULL DEFAULT 0,
    updated_at_ms INTEGER NOT NULL DEFAULT 0
);

INSERT INTO alarm_runtime (id, boot_count, updated_at_ms) VALUES (1, 0, 0);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE alarm_runtime;
DROP TABLE alarm_journal;
DROP TABLE alarm_incidents;
DROP TABLE alarm_state;
DROP TABLE alarm_outputs;
DROP TABLE alarm_sensors;
DROP TABLE alarm_areas;
-- +goose StatementEnd
