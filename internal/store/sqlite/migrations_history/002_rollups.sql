-- +goose Up
-- Persisted downsample tiers for the raw `measurements` table (see
-- 001_measurements.sql). The recorder folds raw rows into
-- measurements_hourly, then measurements_hourly rows into
-- measurements_daily, before purging the source rows so long-term
-- history stays cheap without losing sum/min/max/first/last fidelity.
--
-- Same multi-CCU key + WITHOUT ROWID shape as the raw table: the primary
-- key is the query index for "one data point, one bucket range".
--
-- sum+count make avg exact (never average-of-averages across a re-roll).
-- min/max preserve the peak contract. first/last are the value observed
-- at the earliest/latest source timestamp in the bucket — required to
-- derive cumulative-counter consumption (last-first) per bucket.
CREATE TABLE IF NOT EXISTS measurements_hourly (
    central_name    TEXT    NOT NULL,
    interface_id    TEXT    NOT NULL,
    channel_address TEXT    NOT NULL,
    parameter       TEXT    NOT NULL,
    bucket_ts       INTEGER NOT NULL, -- epoch ms, truncated to hour start (ts - ts%3600000)
    sum             REAL    NOT NULL,
    min             REAL    NOT NULL,
    max             REAL    NOT NULL,
    count           INTEGER NOT NULL,
    first           REAL    NOT NULL, -- value at MIN(ts) within the bucket
    last            REAL    NOT NULL, -- value at MAX(ts) within the bucket
    PRIMARY KEY (central_name, interface_id, channel_address, parameter, bucket_ts)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS measurements_daily (
    central_name    TEXT    NOT NULL,
    interface_id    TEXT    NOT NULL,
    channel_address TEXT    NOT NULL,
    parameter       TEXT    NOT NULL,
    bucket_ts       INTEGER NOT NULL, -- epoch ms, truncated to UTC day start (ts - ts%86400000)
    sum             REAL    NOT NULL,
    min             REAL    NOT NULL,
    max             REAL    NOT NULL,
    count           INTEGER NOT NULL,
    first           REAL    NOT NULL, -- value of the earliest hourly bucket in the day
    last            REAL    NOT NULL, -- value of the latest hourly bucket in the day
    PRIMARY KEY (central_name, interface_id, channel_address, parameter, bucket_ts)
) WITHOUT ROWID;

-- Down is destructive: every hourly and daily rollup bucket is deleted. They
-- can in principle be refolded from the raw `measurements` tier, but only
-- within its retention window (13 months by default) — any bucket whose
-- source rows have already been purged past that window is gone for good,
-- not just re-derivable on demand.
-- +goose Down
DROP TABLE IF EXISTS measurements_daily;
DROP TABLE IF EXISTS measurements_hourly;
