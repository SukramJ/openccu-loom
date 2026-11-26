-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 SukramJ.
-- +goose Up

-- boot_record holds what the daemon knows about why it started, the facts
-- the Matter node's GeneralDiagnostics BootReason is stated from:
--
--   stop_requested          1 when the daemon's last graceful shutdown
--                           followed an operator's restart request (REST
--                           or MQTT); written at the end of that shutdown.
--   last_stop_requested     stop_requested as found at this boot. Every boot
--                           moves stop_requested here and clears it, so a
--                           marker describes one stop and never outlives
--                           the boot after it; a crash or kill writes none.
--   matter_started_version  the daemon version the Matter node last started
--                           with; a start into another version is a
--                           completed software update. Empty until the
--                           first Matter start that has this table.
--
-- The single-row CHECK keeps a second record from ever appearing.
CREATE TABLE boot_record (
    singleton              INTEGER PRIMARY KEY CHECK (singleton = 1),
    stop_requested         INTEGER NOT NULL DEFAULT 0 CHECK (stop_requested IN (0, 1)),
    last_stop_requested    INTEGER NOT NULL DEFAULT 0 CHECK (last_stop_requested IN (0, 1)),
    matter_started_version TEXT    NOT NULL DEFAULT ''
);

INSERT INTO boot_record (singleton) VALUES (1);

-- Down drops the record: the next start of a version that has the table
-- again reports BootReason Unspecified once, as on a fresh installation.
-- +goose Down
DROP TABLE IF EXISTS boot_record;
