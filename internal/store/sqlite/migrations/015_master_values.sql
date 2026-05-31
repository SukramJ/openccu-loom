-- +goose Up
-- +goose StatementBegin

-- master_values caches the current MASTER paramset values per channel so
-- the daemon can rehydrate them at startup without issuing
-- getParamset(MASTER) against the CCU. On a freshly booted CCU those
-- calls may force the interface daemon (rfd / HMIPServer) to validate
-- its sync state against each paired device by radio, which pushes the
-- CCU duty-cycle over the legal limit on installations with many HmIP
-- devices.
--
-- Cache-miss falls back to the CCU read once; on success the values are
-- written back here so subsequent restarts read from disk only.
--
-- Update sources:
--   * cache-miss read at hydration time
--   * operator-write through Channel.WriteParamset(MASTER) after the
--     CCU ack
--   * delayed targeted refresh after a CONFIG_PENDING True→False
--     settle (the CCU has just re-synced its file cache with the
--     device, so the read costs no radio)
--
-- Multi-CCU partitioning: central_name is part of the primary key so
-- two centrals wired into the same daemon hold independent caches
-- (ADR 0002).
--
-- value_json carries the raw wire value. We keep one row per parameter
-- (not per channel) so individual parameter updates do not rewrite the
-- whole channel and so schema-drift on the description side (a
-- parameter dropped from the device profile) simply leaves a dead row
-- that the apply phase ignores via Channel.MasterParameter(name) == nil.
CREATE TABLE master_values (
    central_name    TEXT    NOT NULL,
    interface_id    TEXT    NOT NULL,
    channel_address TEXT    NOT NULL,
    parameter_name  TEXT    NOT NULL,
    value_json      TEXT    NOT NULL,
    updated_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (central_name, interface_id, channel_address, parameter_name)
);

CREATE INDEX idx_master_values_channel
    ON master_values (central_name, interface_id, channel_address);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_master_values_channel;
DROP TABLE IF EXISTS master_values;

-- +goose StatementEnd
