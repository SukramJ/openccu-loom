-- +goose Up
-- The bridged-endpoint identity moves onto go-fabric's endpoint/sqlitestore,
-- whose queries are written against its own schema (sqlitestore.Schema()):
-- one opaque source_key column instead of this daemon's five key columns.
--
-- Endpoint numbers are carried over row for row. A paired controller keys
-- its accessory cache on them and is never told to re-read it, so a lost or
-- renumbered row re-identifies that accessory in Apple Home / Google Home.
--
-- source_key is matterendpoint.SourceKey.String() — the five fields joined
-- by "|" with the channel in decimal. SQLite renders an INTEGER in decimal
-- with the same "%d" form Go uses, so the concatenation below is
-- byte-identical to the rendering the assembler writes from now on; the
-- migration test pins that equality.
CREATE TABLE matter_endpoints_next (
    source_key   TEXT    PRIMARY KEY,
    scope        TEXT    NOT NULL,
    endpoint_id  INTEGER NOT NULL UNIQUE CHECK(endpoint_id BETWEEN 1 AND 65534),
    device_type  INTEGER NOT NULL CHECK(device_type BETWEEN 0 AND 65535)
);

INSERT INTO matter_endpoints_next (source_key, scope, endpoint_id, device_type)
SELECT central_name || '|' || device_address || '|' || channel_no || '|' || dp_kind || '|' || dp_key,
       central_name, endpoint_id, device_type
FROM matter_endpoints;

-- A commissioner-written bridged NodeLabel moves to the settings key
-- go-fabric's node.EndpointPersistence reads ("bridged_node_label.<key>").
INSERT INTO matter_settings (key, value)
SELECT 'bridged_node_label.' || central_name || '|' || device_address || '|' || channel_no || '|' || dp_kind || '|' || dp_key,
       node_label
FROM matter_endpoints
WHERE node_label <> ''
ON CONFLICT(key) DO UPDATE SET value = excluded.value;

DROP TABLE matter_endpoints;
ALTER TABLE matter_endpoints_next RENAME TO matter_endpoints;
CREATE INDEX IF NOT EXISTS matter_endpoints_scope ON matter_endpoints(scope);

-- The high-water mark moves from matter_metadata into go-fabric's
-- single-row allocation table. sqlitestore lifts it past the highest
-- persisted number on every allocation, so a counter that lags a row is
-- harmless; the clamp only keeps the CHECK satisfied.
CREATE TABLE IF NOT EXISTS matter_endpoint_allocation (
    id               INTEGER PRIMARY KEY CHECK (id = 1),
    next_endpoint_id INTEGER NOT NULL CHECK(next_endpoint_id BETWEEN 2 AND 65535)
);

INSERT INTO matter_endpoint_allocation (id, next_endpoint_id)
SELECT 1, MIN(65535, MAX(2, COALESCE(
    (SELECT CAST(value AS INTEGER) FROM matter_metadata WHERE key = 'next_endpoint_id'),
    2)));

DELETE FROM matter_metadata WHERE key = 'next_endpoint_id';

-- The five key columns are recovered by splitting source_key at its first
-- four "|". A central name cannot contain one (hmtypes.ValidateCentralName)
-- and the store refuses to write a key whose rendering does not split back
-- into itself, so every row the daemon wrote splits exactly. A row that does
-- not name a known dp_kind is dropped rather than failing the CHECK; it
-- could not have been written by this daemon.
--
-- Loss on Down: the created_at / updated_at stamps restart at the time of
-- the down-migration. Endpoint numbers, NodeLabels and the counter survive.
-- +goose Down
CREATE TABLE matter_endpoints_prev (
    central_name    TEXT    NOT NULL,
    device_address  TEXT    NOT NULL,
    channel_no      INTEGER NOT NULL,
    dp_kind         TEXT    NOT NULL CHECK(dp_kind IN ('custom','generic','calculated','combined','measurement')),
    dp_key          TEXT    NOT NULL,
    endpoint_id     INTEGER NOT NULL CHECK(endpoint_id BETWEEN 1 AND 65534),
    device_type     INTEGER NOT NULL CHECK(device_type BETWEEN 0 AND 65535),
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    node_label      TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY(central_name, device_address, channel_no, dp_kind, dp_key)
);

WITH
s1 AS (SELECT source_key, endpoint_id, device_type,
              substr(source_key, 1, instr(source_key, '|') - 1) AS c,
              substr(source_key, instr(source_key, '|') + 1)    AS r1
       FROM matter_endpoints WHERE instr(source_key, '|') > 0),
s2 AS (SELECT *, substr(r1, 1, instr(r1, '|') - 1) AS a, substr(r1, instr(r1, '|') + 1) AS r2
       FROM s1 WHERE instr(r1, '|') > 0),
s3 AS (SELECT *, substr(r2, 1, instr(r2, '|') - 1) AS ch, substr(r2, instr(r2, '|') + 1) AS r3
       FROM s2 WHERE instr(r2, '|') > 0),
s4 AS (SELECT *, substr(r3, 1, instr(r3, '|') - 1) AS kind, substr(r3, instr(r3, '|') + 1) AS dpkey
       FROM s3 WHERE instr(r3, '|') > 0)
INSERT INTO matter_endpoints_prev (central_name, device_address, channel_no, dp_kind, dp_key,
                                   endpoint_id, device_type, node_label)
SELECT s4.c, s4.a, CAST(s4.ch AS INTEGER), s4.kind, s4.dpkey, s4.endpoint_id, s4.device_type,
       COALESCE((SELECT value FROM matter_settings WHERE key = 'bridged_node_label.' || s4.source_key), '')
FROM s4
WHERE s4.kind IN ('custom','generic','calculated','combined','measurement');

DELETE FROM matter_settings
WHERE key IN (SELECT 'bridged_node_label.' || source_key FROM matter_endpoints);

INSERT INTO matter_metadata (key, value)
SELECT 'next_endpoint_id', MIN(65534, next_endpoint_id) FROM matter_endpoint_allocation WHERE id = 1
ON CONFLICT(key) DO UPDATE SET value = excluded.value;

DROP TABLE matter_endpoint_allocation;
DROP TABLE matter_endpoints;
ALTER TABLE matter_endpoints_prev RENAME TO matter_endpoints;
