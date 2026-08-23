-- SPDX-License-Identifier: MIT
-- Copyright (C) 2026 OpenCCU-Loom authors.

-- +goose Up
-- Adds the time-weighted average's numerator/denominator to both rollup
-- tiers, alongside — never instead of — the existing sum/count columns.
--
-- sum/count keep their original meaning exactly: sum is a plain sum of
-- sampled values, count is a sample count. Every measurements_hourly and
-- measurements_daily row written before this migration was written under
-- that meaning, and a later re-aggregation folds rows additively
-- (foldTierBuckets, foldEnergyRowsBy in measurements.go); merging a
-- pre-migration row's sum/count with a value that means something else
-- would silently corrupt the merged aggregate.
--
-- weighted_sum is the sum of value_i * span_i across the bucket's raw
-- samples (span_i in ms, the time each sample's value is held); weight_ms
-- is the sum of span_i, the covered span backing it. Both default to 0.
-- A real fold always writes weight_ms > 0 whenever it writes count > 0 (a
-- non-empty bucket always covers a positive span), so weight_ms = 0
-- unambiguously marks a row that predates weighted rollups — the reader
-- then falls back to sum/count instead of dividing by zero or dropping
-- the row.
ALTER TABLE measurements_hourly ADD COLUMN weighted_sum REAL    NOT NULL DEFAULT 0;
ALTER TABLE measurements_hourly ADD COLUMN weight_ms    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE measurements_daily  ADD COLUMN weighted_sum REAL    NOT NULL DEFAULT 0;
ALTER TABLE measurements_daily  ADD COLUMN weight_ms    INTEGER NOT NULL DEFAULT 0;

-- Down is lossy: dropping weighted_sum/weight_ms discards the covered span
-- behind every rollup bucket recorded since this migration ran. The rows
-- survive and keep reporting an average, but it silently reverts to the
-- plain sample mean — which is the very defect the columns exist to fix, and
-- for a mostly-idle series that mean can be several times the true value.
-- The spans cannot be recomputed once the raw `measurements` rows behind a
-- bucket have aged out of their retention window.
-- +goose Down
ALTER TABLE measurements_hourly DROP COLUMN weight_ms;
ALTER TABLE measurements_hourly DROP COLUMN weighted_sum;
ALTER TABLE measurements_daily  DROP COLUMN weight_ms;
ALTER TABLE measurements_daily  DROP COLUMN weighted_sum;
