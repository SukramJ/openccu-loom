-- SPDX-License-Identifier: MIT
-- Copyright (C) 2026 SukramJ.

-- +goose Up
-- Time-axis index for the daily tier.
--
-- The raw and hourly tiers each got a bucket-time index when the bounded
-- rollup landed (003_rollup_watermarks.sql), but the daily tier was left
-- without one. Its primary key leads with central_name, so it cannot serve
-- a pure time range: the daily retention purge
-- (DELETE FROM measurements_daily WHERE bucket_ts < ?) full-scans the table
-- on every retention tick, and the scan grows with every day retained. The
-- index turns that into an ordered range scan bounded by the rows the purge
-- actually deletes.
CREATE INDEX IF NOT EXISTS idx_measurements_daily_bucket_ts ON measurements_daily (bucket_ts);

-- +goose Down
DROP INDEX IF EXISTS idx_measurements_daily_bucket_ts;
