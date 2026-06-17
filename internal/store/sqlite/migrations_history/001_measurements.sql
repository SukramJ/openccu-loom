-- +goose Up
-- Measurement history lives in a dedicated database (history.db) with its
-- own migration series, kept separate from the config/session store so an
-- append-heavy writer never contends with config writes. See ADR 0040.
--
-- The primary key IS the query index: every chart read is "one data point,
-- one time range", i.e. a prefix scan on (central_name, interface_id,
-- channel_address, parameter, ts). WITHOUT ROWID keeps the row compact and
-- avoids a second b-tree.
CREATE TABLE IF NOT EXISTS measurements (
    central_name    TEXT    NOT NULL,
    interface_id    TEXT    NOT NULL,
    channel_address TEXT    NOT NULL,
    parameter       TEXT    NOT NULL,
    ts              INTEGER NOT NULL, -- epoch ms (wire-reception time)
    value           REAL    NOT NULL, -- numeric measurement; nil is never recorded
    PRIMARY KEY (central_name, interface_id, channel_address, parameter, ts)
) WITHOUT ROWID;

-- +goose Down
DROP TABLE IF EXISTS measurements;
