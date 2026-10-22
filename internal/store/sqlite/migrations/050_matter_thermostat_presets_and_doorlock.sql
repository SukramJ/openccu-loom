-- +goose Up
-- go-fabric's Matter store writes into this daemon's database, so every
-- table its store.Schema() creates exists here too. Copied verbatim from
-- go-fabric v0.10.0 store/schema.sql: schema v5 (matter_thermostat_presets,
-- the Thermostat Presets and ActivePresetHandle a controller wrote) and v6
-- (the DoorLock users, credentials and schedules).
CREATE TABLE IF NOT EXISTS matter_thermostat_presets (
    endpoint_key            TEXT       PRIMARY KEY,
    presets                 BLOB       NOT NULL,
    active_preset_handle    BLOB,
    updated_at              TIMESTAMP  NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS matter_doorlock_users (
    endpoint_key               TEXT     NOT NULL,
    user_index                 INTEGER  NOT NULL CHECK(user_index BETWEEN 1 AND 65535),
    user_name                  TEXT     NOT NULL,
    user_unique_id             INTEGER,
    user_status                INTEGER  NOT NULL,
    user_type                  INTEGER  NOT NULL,
    credential_rule            INTEGER  NOT NULL,
    credentials_json           TEXT     NOT NULL,
    creator_fabric_index       INTEGER  NOT NULL,
    last_modified_fabric_index INTEGER  NOT NULL,
    expiring_user_expires_at   INTEGER,
    PRIMARY KEY(endpoint_key, user_index)
);

CREATE TABLE IF NOT EXISTS matter_doorlock_credentials (
    endpoint_key               TEXT     NOT NULL,
    credential_type            INTEGER  NOT NULL,
    credential_index           INTEGER  NOT NULL CHECK(credential_index BETWEEN 0 AND 65535),
    credential_data            BLOB     NOT NULL,
    creator_fabric_index       INTEGER  NOT NULL,
    last_modified_fabric_index INTEGER  NOT NULL,
    PRIMARY KEY(endpoint_key, credential_type, credential_index)
);

CREATE TABLE IF NOT EXISTS matter_doorlock_schedules (
    endpoint_key        TEXT     NOT NULL,
    kind                TEXT     NOT NULL CHECK(kind IN ('week_day', 'year_day', 'holiday')),
    schedule_index      INTEGER  NOT NULL CHECK(schedule_index BETWEEN 0 AND 255),
    user_index          INTEGER  NOT NULL,
    days_mask           INTEGER  NOT NULL DEFAULT 0,
    start_hour          INTEGER  NOT NULL DEFAULT 0,
    start_minute        INTEGER  NOT NULL DEFAULT 0,
    end_hour            INTEGER  NOT NULL DEFAULT 0,
    end_minute          INTEGER  NOT NULL DEFAULT 0,
    local_start_time    INTEGER  NOT NULL DEFAULT 0,
    local_end_time      INTEGER  NOT NULL DEFAULT 0,
    operating_mode      INTEGER  NOT NULL DEFAULT 0,
    PRIMARY KEY(endpoint_key, kind, user_index, schedule_index)
);

-- Loss note: dropping these tables discards the Thermostat presets and the
-- DoorLock users, credentials and schedules a controller wrote; a controller
-- writes them again. No device or config data is lost.
-- +goose Down
DROP TABLE IF EXISTS matter_doorlock_schedules;
DROP TABLE IF EXISTS matter_doorlock_credentials;
DROP TABLE IF EXISTS matter_doorlock_users;
DROP TABLE IF EXISTS matter_thermostat_presets;
