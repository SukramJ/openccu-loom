-- +goose Up
-- Per-datapoint recording overrides live beside the measurements they
-- steer, in the dedicated history database (opened only when the history
-- feature is enabled). A present row overrides the parameter-name glob
-- policy (HistoryConfig.Include/Exclude) for one specific data-point
-- instance; an absent row means "fall back to the glob policy". The table
-- is sparse — a row exists only where an operator has toggled recording
-- for a DP away from its default.
--
-- Partitioned by central_name (ADR 0002) and keyed on the same
-- (central_name, interface_id, channel_address, parameter) tuple as the
-- measurements primary key, so device-remove / central-remove purges match
-- the measurement purge exactly.
CREATE TABLE IF NOT EXISTS measurement_recording_overrides (
    central_name    TEXT    NOT NULL,
    interface_id    TEXT    NOT NULL,
    channel_address TEXT    NOT NULL,
    parameter       TEXT    NOT NULL,
    record          INTEGER NOT NULL, -- 1 = force record, 0 = force skip
    updated_by      TEXT    NOT NULL DEFAULT '',
    updated_at      TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (central_name, interface_id, channel_address, parameter)
) WITHOUT ROWID;

-- Down is destructive: every per-data-point recording override is deleted. A
-- data point an operator explicitly excluded from or forced into history
-- recording reverts to the parameter-name glob policy.
-- +goose Down
DROP TABLE IF EXISTS measurement_recording_overrides;
