-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 SukramJ.
-- +goose Up
-- go-fabric's Matter store writes into this daemon's database, so every
-- table its store.Schema() creates exists here too. Copied verbatim from
-- go-fabric v0.12.1 store/migrations/v7.up.sql: the ICD Management
-- server's registered Check-In clients with their ICDToken keys (the keys
-- are sealed at rest by the store itself since v0.12.1; the shape is v7's).
CREATE TABLE IF NOT EXISTS matter_icd_registrations (
    fabric_index        INTEGER  NOT NULL,
    position            INTEGER  NOT NULL,
    check_in_node_id    BLOB     NOT NULL,
    monitored_subject   BLOB     NOT NULL,
    icd_key             BLOB     NOT NULL,
    client_type         INTEGER  NOT NULL CHECK(client_type BETWEEN 0 AND 255),
    PRIMARY KEY(fabric_index, position),
    FOREIGN KEY(fabric_index) REFERENCES matter_fabrics(fabric_index) ON DELETE CASCADE
);

-- Loss on Down: every ICD client's registration is discarded — a
-- registered Check-In client is no longer recognised until it registers
-- again (RegisterClient), and the Check-Ins the node sent it stop.
-- +goose Down
DROP TABLE IF EXISTS matter_icd_registrations;
