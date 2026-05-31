-- +goose Up
-- +goose StatementBegin

-- matter_endpoints persists the (source-identity → endpoint_id) mapping
-- so the same Custom / Generic / Calculated / Combined / Measurement
-- DP receives the same Matter endpoint identifier across daemon
-- restarts. Endpoint 0 is the root bridge endpoint and is never in
-- this table; bridged endpoints occupy 1..65534.
--
-- The 5-tuple (central_name, device_address, channel_no, dp_kind,
-- dp_key) is the same key used by the matter_exposures allowlist
-- table (added by U1 in docs/matter-ui-concept.md §2). Until that
-- migration arrives, this table is the canonical source for
-- "is this DP already known to the bridge?".
CREATE TABLE matter_endpoints (
    central_name    TEXT    NOT NULL,
    device_address  TEXT    NOT NULL,
    channel_no      INTEGER NOT NULL,
    dp_kind         TEXT    NOT NULL CHECK(dp_kind IN ('custom','generic','calculated','combined','measurement')),
    dp_key          TEXT    NOT NULL,
    endpoint_id     INTEGER NOT NULL CHECK(endpoint_id BETWEEN 1 AND 65534),
    device_type     INTEGER NOT NULL CHECK(device_type BETWEEN 0 AND 65535),
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(central_name, device_address, channel_no, dp_kind, dp_key)
);

-- endpoint_id is globally unique across the entire bridge (Matter
-- spec: a controller addresses endpoints by ID without disambiguation).
CREATE UNIQUE INDEX matter_endpoints_id_unique
    ON matter_endpoints(endpoint_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS matter_endpoints_id_unique;
DROP TABLE IF EXISTS matter_endpoints;

-- +goose StatementEnd
