-- SPDX-License-Identifier: MIT
-- Copyright (C) 2026 SukramJ.

-- +goose Up
-- Re-fold the daily tier onto local calendar days.
--
-- measurements_daily used to be bucketed on the UTC day
-- (bucket_ts - bucket_ts % 86400000) while every surface that shows a day
-- labels it with the viewer's local calendar. At UTC+2 a "day" therefore ran
-- from 02:00 to 02:00 local time under the label of the day before, so a
-- consumption at 00:30 local on the 6th was reported as the 5th and every
-- day and month total was shifted by the offset slice at both ends.
--
-- The rows cannot be corrected in place: a UTC day cannot be re-cut into
-- local days once its hours have been summed away. They are dropped and the
-- daily watermark is reset to 0, which makes the next rollup re-fold the
-- whole hourly tier into local days. The hourly tier is untouched — an hour
-- bucket is the same instant range in every whole-hour zone offset, so it
-- carries the values this re-fold rebuilds from.
--
-- Days whose hourly rows have already passed the hourly retention (13 months
-- by default) cannot be rebuilt and are lost. That is the price of the
-- correction; the alternative is a permanently mislabelled daily tier.
DELETE FROM measurements_daily;
UPDATE measurement_rollup_state SET watermark = 0 WHERE tier = 'daily';

-- +goose Down
-- The inverse is the same reset, and honestly so: rolling back restores the
-- UTC-bucketing code, which then has to re-fold the daily tier for exactly
-- the same reason this migration did — the local-day rows it would find are
-- as unusable to it as the UTC rows were to us. Emptying the tier and
-- rewinding the watermark is the only state both versions can rebuild from.
DELETE FROM measurements_daily;
UPDATE measurement_rollup_state SET watermark = 0 WHERE tier = 'daily';
