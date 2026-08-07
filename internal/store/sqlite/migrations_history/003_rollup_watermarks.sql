-- SPDX-License-Identifier: MIT
-- Copyright (C) 2026 OpenCCU-Loom authors.

-- +goose Up
-- Rollup watermarks + fold-scan indexes (see measurements.go).
--
-- The original rollup re-selected every source row below the lag cutoff on
-- every tick (WHERE ts < cutoff, no lower bound) and ON-CONFLICT-rewrote
-- every historical bucket in one write transaction. That full re-scan grew
-- with the table, held a long write lock, and starved the append-heavy
-- recorder. The redesign folds only the newly-eligible buckets between a
-- per-tier high-water-mark and a bucket-aligned cutoff, so each tick writes
-- a bounded, ever-newer slice and never re-touches a finalized bucket.
--
-- measurement_rollup_state holds one watermark per tier: the exclusive
-- upper bound (bucket-aligned, epoch ms) of the source rows already folded
-- into that tier. 'hourly' is a frontier over the raw `measurements.ts`
-- axis; 'daily' is a frontier over the `measurements_hourly.bucket_ts`
-- axis. The purge floors its delete cutoff by these watermarks so a source
-- row is never deleted before it has been folded.
CREATE TABLE IF NOT EXISTS measurement_rollup_state (
    tier      TEXT    NOT NULL PRIMARY KEY, -- 'hourly' | 'daily'
    watermark INTEGER NOT NULL              -- exclusive upper bound (epoch ms) of folded source rows
) WITHOUT ROWID;

-- Seed the watermarks from any already-rolled data so an existing history
-- database does not re-fold buckets that predate this migration. A fresh
-- database seeds both to 0 (nothing folded yet). The hourly watermark is
-- one hour past the newest hourly bucket; the daily watermark is one day
-- past the newest daily bucket (both measured on the shared epoch-ms axis,
-- so they line up with the hourly-bucket_ts frontier the daily fold reads).
INSERT INTO measurement_rollup_state (tier, watermark)
VALUES ('hourly', COALESCE((SELECT MAX(bucket_ts) + 3600000 FROM measurements_hourly), 0));
INSERT INTO measurement_rollup_state (tier, watermark)
VALUES ('daily', COALESCE((SELECT MAX(bucket_ts) + 86400000 FROM measurements_daily), 0));

-- The bounded fold and the retention purge both range-scan on the time
-- axis (raw: ts >= watermark AND ts < cutoff; hourly: bucket_ts >= ...).
-- The primary key leads with central_name, so it cannot serve a pure time
-- range; these secondary indexes give the fold and purge an ordered scan
-- instead of a full-table walk.
CREATE INDEX IF NOT EXISTS idx_measurements_ts ON measurements (ts);
CREATE INDEX IF NOT EXISTS idx_measurements_hourly_bucket_ts ON measurements_hourly (bucket_ts);

-- Down is destructive: the per-tier fold watermark is deleted. It is not a
-- silent no-op — the next Up reseeds it from whatever rollup rows still
-- exist, so any raw row that was already purged past the old watermark
-- before the round trip is skipped by the next fold instead of being folded
-- in, leaving a gap in the affected tier with no error raised.
-- +goose Down
DROP INDEX IF EXISTS idx_measurements_hourly_bucket_ts;
DROP INDEX IF EXISTS idx_measurements_ts;
DROP TABLE IF EXISTS measurement_rollup_state;
