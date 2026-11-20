-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 SukramJ.
-- +goose Up

-- instance_identity holds the one identity of this installation: a random id
-- created on first start and never changed afterwards. A client tells two
-- daemons apart by it — two daemons may serve the same CCU, so the CCU serial
-- cannot do that (ADR 0058). It lives in the database rather than in a file
-- so a backup restored on new hardware stays the same installation.
--
-- The single-row CHECK keeps a second identity from ever appearing.
CREATE TABLE instance_identity (
    singleton   INTEGER PRIMARY KEY CHECK (singleton = 1),
    instance_id TEXT    NOT NULL,
    created_at  TEXT    NOT NULL
);

-- Down is destructive: the identity is gone, and the next start creates a
-- new one, which a client sees as a different daemon.
-- +goose Down
DROP TABLE IF EXISTS instance_identity;
