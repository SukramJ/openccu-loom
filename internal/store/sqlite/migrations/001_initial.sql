-- +goose Up
-- +goose StatementBegin

CREATE TABLE interfaces (
    central_name TEXT NOT NULL,
    interface_id TEXT NOT NULL,
    interface_name TEXT NOT NULL,
    url TEXT NOT NULL,
    last_seen_version TEXT,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (central_name, interface_id)
);

CREATE TABLE devices (
    central_name TEXT NOT NULL,
    interface_id TEXT NOT NULL,
    address TEXT NOT NULL,
    type TEXT NOT NULL,
    parent TEXT,
    firmware TEXT,
    model TEXT,
    manufacturer TEXT,
    product_group TEXT,
    hash TEXT NOT NULL,
    description_json TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (central_name, interface_id, address)
);

CREATE INDEX devices_by_parent ON devices(central_name, interface_id, parent);

CREATE TABLE paramsets (
    central_name TEXT NOT NULL,
    interface_id TEXT NOT NULL,
    channel_address TEXT NOT NULL,
    paramset_key TEXT NOT NULL,
    hash TEXT NOT NULL,
    paramset_json TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (central_name, interface_id, channel_address, paramset_key)
);

CREATE TABLE incidents (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    central_name TEXT NOT NULL,
    interface_id TEXT,
    type TEXT NOT NULL,
    severity TEXT NOT NULL,
    message TEXT NOT NULL,
    details TEXT,
    first_seen TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    count INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX incidents_by_central ON incidents(central_name, last_seen);
CREATE INDEX incidents_by_severity ON incidents(severity, last_seen);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS incidents;
DROP TABLE IF EXISTS paramsets;
DROP TABLE IF EXISTS devices;
DROP TABLE IF EXISTS interfaces;
-- +goose StatementEnd
