-- +goose Up
-- +goose StatementBegin

-- values_cache persists the wire-side VALUES paramset (and event-only
-- parameters such as UNREACH, RSSI, LOW_BAT) per data point so the
-- daemon can repopulate them at startup without waiting for the CCU
-- to push every value again. The cache only ever holds wire-DPs;
-- calculated and custom data points are deterministically derived
-- from their wire sources during the restore pass.
--
-- Lifecycle of an entry:
--   * created on the first observed wire value
--   * updated on every push event (last_seen_at) or value change
--     (last_changed_at)
--   * flushed to disk by the periodic flusher (default 60 s) and on
--     graceful shutdown
--   * applied at boot, before wireInterface, so the UI / MQTT topics
--     show the last known value while the CCU init() is still in
--     flight
--   * cleared on device-remove (DeleteDevice) and by a periodic GC
--     that drops rows whose parameter is no longer in the device
--     profile description
--
-- Storage format:
--   value_json   carries the JSON-encoded wire value (any shape).
--   value_type   is a small discriminator that lets readers filter
--                without parsing JSON.  Values:
--                  "bool" | "int" | "float" | "string" | "null"
--
-- Multi-CCU partitioning: central_name is part of the primary key
-- so two centrals wired into the same daemon hold independent
-- caches (ADR 0002).
--
-- Schema versioning: cache_schema_version is written into every row
-- so a future migration can detect rows that need conversion before
-- they are restored.  The current code path writes version 1.
CREATE TABLE values_cache (
    central_name         TEXT    NOT NULL,
    interface_id         TEXT    NOT NULL,
    channel_address      TEXT    NOT NULL,
    parameter_name       TEXT    NOT NULL,
    value_json           TEXT    NOT NULL,
    value_type           TEXT    NOT NULL,
    last_seen_at         INTEGER NOT NULL,
    last_changed_at      INTEGER NOT NULL,
    cache_schema_version INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (central_name, interface_id, channel_address, parameter_name)
);

CREATE INDEX idx_values_cache_channel
    ON values_cache (central_name, interface_id, channel_address);

-- The GC scan walks rows ordered by last_seen_at to evict the
-- coldest entries first if a size budget ever gets introduced.
CREATE INDEX idx_values_cache_last_seen
    ON values_cache (last_seen_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_values_cache_last_seen;
DROP INDEX IF EXISTS idx_values_cache_channel;
DROP TABLE IF EXISTS values_cache;

-- +goose StatementEnd
