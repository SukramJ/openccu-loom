-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 SukramJ.
-- +goose Up

-- scrub_pending is 1 when an erase of the Matter node removed its rows but
-- could not scrub their bytes out of the database file: go-fabric's erase
-- ends with a VACUUM and a WAL checkpoint (store.Scrub), and a read
-- transaction the running daemon holds makes either fail, so the erased
-- bytes may stay in the database file or its -wal. The next boot runs the
-- scrub right after the database opens, before the subsystems that read it
-- start, and clears the flag once the scrub finished.
ALTER TABLE boot_record ADD COLUMN scrub_pending INTEGER NOT NULL DEFAULT 0 CHECK (scrub_pending IN (0, 1));

-- Down drops the flag: a scrub an erase left pending is then not run by a
-- later boot, and the erased rows' bytes may stay in the file until a
-- later erase scrubs the file again.
-- +goose Down
ALTER TABLE boot_record DROP COLUMN scrub_pending;
